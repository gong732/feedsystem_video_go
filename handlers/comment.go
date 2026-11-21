package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"video-feed/database"
	"video-feed/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// maxCommentLen 单条评论最大长度（按"字符"算，不是字节）
const maxCommentLen = 500

// errCommentGone 并发删除时的哨兵：查到了但真正 UPDATE 时已经没了
var errCommentGone = errors.New("comment already gone")

type createCommentReq struct {
	Content string `json:"content" binding:"required"`
}

// CreateComment 发布评论
//
// 两个写操作必须在一个事务里：
//  1. comments 表插入一行
//  2. videos.comments_count + 1
//
// 只做其中一个会出现"评论列表里有、但视频卡片显示 0 条评论"的不一致。
func CreateComment(c *gin.Context) {
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

	var req createCommentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "评论内容不能为空"})
		return
	}
	content := strings.TrimSpace(req.Content)
	// binding:"required" 只挡空字符串，挡不住全是空格；
	// 用 RuneCountInString 按字符计数：500 个汉字不会被按 1500 字节误判超长。
	if content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "评论内容不能为空"})
		return
	}
	if utf8.RuneCountInString(content) > maxCommentLen {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "评论最多 500 字"})
		return
	}

	comment := models.Comment{UserID: me, VideoID: videoID, Content: content}
	err = database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&comment).Error; err != nil {
			return err
		}
		return tx.Model(&models.Video{}).Where("id = ?", videoID).
			UpdateColumn("comments_count", gorm.Expr("comments_count + 1")).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "评论失败"})
		return
	}

	// 详情接口的 video:info 缓存里带 comments_count，写库后必须让它失效
	invalidateVideoCache(videoID)

	comment.Author = currentAuthor(me)
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "评论成功", "data": comment})
}

// ListComments 评论列表：按 id 倒序的游标分页
//
// 为什么用 id 而不用 created_ts 当游标：
//
//	评论在一次自增插入中产生，id 单调且唯一；毫秒时间戳会出现同一毫秒多条并列，
//	用时间戳做 "<" 边界会把同毫秒的其它评论一起跳过（漏评论）。
//
// 为什么不用 OFFSET：和发现页同理，翻得越深越慢，且新评论插入会让页间重复。
func ListComments(c *gin.Context) {
	vid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || vid == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误"})
		return
	}
	videoID := uint(vid)

	cursor, _ := strconv.ParseUint(c.DefaultQuery("cursor", "0"), 10, 64)
	limit := 10
	if l := c.Query("limit"); l != "" {
		if v, e := strconv.Atoi(l); e == nil && v > 0 && v <= 50 {
			limit = v
		}
	}

	query := database.DB.Where("video_id = ? AND status = ?", videoID, 1)
	if cursor > 0 {
		query = query.Where("id < ?", cursor)
	}
	var list []models.Comment
	// 多取一条：用"有没有第 limit+1 条"判断是否还有下一页，
	// 比再发一次 COUNT(*) 更省，也不会因为并发插入而算错。
	query.Order("id DESC").Limit(limit + 1).Find(&list)

	hasMore := len(list) > limit
	if hasMore {
		list = list[:limit]
	}
	nextCursor := uint64(0)
	if hasMore && len(list) > 0 {
		nextCursor = uint64(list[len(list)-1].ID)
	}

	fillCommentAuthors(list)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"list":        list,
		"next_cursor": nextCursor,
	}})
}

// DeleteComment 删除自己的评论（软删）
//
// 水平越权（IDOR）防护：绝不能只按 comment_id 删。
// 否则任何人拿到别人的评论 id，就能删掉别人的评论。
// 这里做两层校验：先查出来判断归属（给前端明确的 403），
// 再在 UPDATE 的 WHERE 里带上 user_id（防止"查完到删之间"状态被改的 TOCTOU）。
func DeleteComment(c *gin.Context) {
	me := c.GetUint("user_id")
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误"})
		return
	}
	commentID := uint(id)

	var comment models.Comment
	if err := database.DB.Select("id, user_id, video_id").
		Where("id = ? AND status = ?", commentID, 1).First(&comment).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "评论不存在"})
		return
	}
	if comment.UserID != me {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "msg": "只能删除自己的评论"})
		return
	}

	// 软删 + 计数 -1 同一事务；软删而不是物理删，保留审计痕迹
	err = database.DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&models.Comment{}).
			Where("id = ? AND user_id = ? AND status = ?", commentID, me, 1).
			UpdateColumn("status", 0)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errCommentGone
		}
		return tx.Model(&models.Video{}).
			Where("id = ? AND comments_count > 0", comment.VideoID).
			UpdateColumn("comments_count", gorm.Expr("comments_count - 1")).Error
	})
	if errors.Is(err, errCommentGone) {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "评论已被删除"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "删除失败"})
		return
	}

	invalidateVideoCache(comment.VideoID)
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "已删除"})
}

// fillCommentAuthors 批量补评论作者（一次 IN 查询，避免 N+1）
func fillCommentAuthors(list []models.Comment) {
	if len(list) == 0 {
		return
	}
	ids := make([]uint, 0, len(list))
	for _, cm := range list {
		ids = append(ids, cm.UserID)
	}
	var users []models.User
	database.DB.Select("id, username, nickname, avatar").Where("id IN ?", ids).Find(&users)
	userMap := make(map[uint]models.User, len(users))
	for _, u := range users {
		userMap[u.ID] = u
	}
	for i := range list {
		list[i].Author = userMap[list[i].UserID]
	}
}
