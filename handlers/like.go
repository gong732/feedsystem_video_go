package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"video-feed/database"
	"video-feed/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ToggleLike 点赞 / 取消点赞（同一个接口，toggle 语义，和关注按钮一个套路）
//
// 并发安全的核心：不做"先查有没有点过、再决定插入还是删除"——
// 查和写之间有竞态窗口，两个并发请求可能都查到"没点过"，然后都去插入。
//
// 正确做法是让数据库的复合唯一索引 (user_id, video_id) 当唯一裁判：
//   - INSERT 成功            → 本次是新点赞，计数 +1
//   - INSERT 撞 1062（重复键）→ 说明已经赞过，本次语义翻转为取消，删明细、计数 -1
//
// 计数为什么用 likes_count = likes_count + 1 而不是先 SELECT 再 UPDATE：
// 后者两个并发请求会读到同一个旧值（比如 10），各自算成 11 再写回，最终只 +1 而不是 +2，
// 这就是典型的"丢失更新"。原子表达式把计算下推给 MySQL 的行锁，天然串行。
func ToggleLike(c *gin.Context) {
	me := c.GetUint("user_id")
	vid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || vid == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误"})
		return
	}
	videoID := uint(vid)
	if !videoExists(videoID) {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "视频不存在"})
		return
	}

	// ① 先尝试"点赞"：明细和计数放在同一个事务里，
	//    否则可能出现"点赞记录写进去了、likes_count 没加"，列表显示 0 赞但点进去是已赞。
	like := models.Like{UserID: me, VideoID: videoID}
	err = database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&like).Error; err != nil {
			return err
		}
		return tx.Model(&models.Video{}).Where("id = ?", videoID).
			UpdateColumn("likes_count", gorm.Expr("likes_count + 1")).Error
	})

	if err == nil {
		// 写库成功后才删缓存 —— 顺序不能反：先删缓存再写库，中间有请求回源读到旧值
		invalidateVideoCache(videoID)
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "点赞成功", "data": gin.H{
			"liked":       true,
			"likes_count": videoLikesCount(videoID),
		}})
		return
	}

	// ② 只有"重复键"才代表已经点过；其它错误（连接断开、超时）必须如实报 500，
	//    不能像早期版本那样一律当成"已关注过"去走删除逻辑。
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "点赞失败"})
		return
	}

	// ③ 取消点赞：DELETE 的 RowsAffected 是并发下的第二道裁判。
	//    两个并发"取消"请求，只有一个能删到行（RowsAffected=1），另一个是 0，不能重复减计数。
	var rows int64
	err = database.DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("user_id = ? AND video_id = ?", me, videoID).Delete(&models.Like{})
		if res.Error != nil {
			return res.Error
		}
		rows = res.RowsAffected
		if rows == 0 {
			return nil // 已经被并发的另一个请求删掉了，什么都不用做
		}
		// likes_count > 0 兜底，避免任何历史脏数据把计数减成负数
		return tx.Model(&models.Video{}).Where("id = ? AND likes_count > 0", videoID).
			UpdateColumn("likes_count", gorm.Expr("likes_count - 1")).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "取消点赞失败"})
		return
	}

	invalidateVideoCache(videoID)
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "已取消点赞", "data": gin.H{
		"liked":       false,
		"likes_count": videoLikesCount(videoID),
	}})
}

// GetLikeStatus 查询某视频的点赞状态（未登录也能查总数，登录了才知道自己点没点）
func GetLikeStatus(c *gin.Context) {
	vid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || vid == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误"})
		return
	}
	videoID := uint(vid)
	if !videoExists(videoID) {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "视频不存在"})
		return
	}
	me := c.GetUint("user_id") // AuthOptional：未登录是 0
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"liked":       userLiked(me, videoID),
		"likes_count": videoLikesCount(videoID),
	}})
}
