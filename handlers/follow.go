package handlers

import (
	"net/http"
	"strconv"

	"video-feed/database"
	"video-feed/models"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// FollowUser 关注：靠复合唯一索引防重复关注（和博客防重复点赞一个套路）
func FollowUser(c *gin.Context) {
	me := c.GetUint("user_id") // 中间件塞进来的登录用户
	targetID, _ := strconv.Atoi(c.Param("user_id"))
	if me == uint(targetID) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "不能关注自己"})
		return
	}
	// 目标用户必须存在
	var count int64
	database.DB.Model(&models.User{}).Where("id = ?", targetID).Count(&count)
	if count == 0 {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "用户不存在"})
		return
	}
	follow := models.Follow{UserID: me, FollowUserID: uint(targetID)}
	err := database.DB.Create(&follow).Error
	if err != nil {
		// 唯一索引冲突(1062) = 已经关注过 → 走取消关注（toggle 语义）
		database.DB.Where("user_id = ? AND follow_user_id = ?", me, targetID).Delete(&models.Follow{})
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "已取消关注"})
		return
	}

	// 关注成功 → backfill：把对方最近的视频补写进我的热时间线，
	// 否则新粉丝看不到对方的历史视频（推模式只推发布时的粉丝）
	var recent []models.Video
	database.DB.Select("id, created_ts").Where("user_id = ? AND status = ?", targetID, 1).
		Order("created_ts DESC").Limit(50).Find(&recent)
	items := make([]redis.Z, 0, len(recent))
	for _, v := range recent {
		items = append(items, redis.Z{
			Score:  float64(v.CreatedTs), // 毫秒绝对时间戳
			Member: strconv.FormatUint(uint64(v.ID), 10),
		})
	}
	database.BackfillFeedHot(me, items)

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "关注成功"})
}

// FollowingList 我关注了谁
func FollowingList(c *gin.Context) {
	userID, _ := strconv.Atoi(c.Param("user_id"))
	var follows []models.Follow
	database.DB.Where("user_id = ?", userID).Find(&follows)
	if len(follows) == 0 {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": []gin.H{}})
		return
	}
	// 批量查用户（避免 N+1）
	ids := make([]uint, 0, len(follows))
	for _, f := range follows {
		ids = append(ids, f.FollowUserID)
	}
	var users []models.User
	database.DB.Select("id, username, nickname, avatar").Where("id IN ?", ids).Find(&users)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": users})
}

// FollowerList 谁关注了我
func FollowerList(c *gin.Context) {
	userID, _ := strconv.Atoi(c.Param("user_id"))
	var follows []models.Follow
	database.DB.Where("follow_user_id = ?", userID).Find(&follows)
	if len(follows) == 0 {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": []gin.H{}})
		return
	}
	ids := make([]uint, 0, len(follows))
	for _, f := range follows {
		ids = append(ids, f.UserID)
	}
	var users []models.User
	database.DB.Select("id, username, nickname, avatar").Where("id IN ?", ids).Find(&users)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": users})
}

// IsFollowing 查询是否已关注（前端展示"已关注/关注"按钮用）
func IsFollowing(c *gin.Context) {
	me := c.GetUint("user_id")
	targetID, _ := strconv.Atoi(c.Param("user_id"))
	var count int64
	database.DB.Model(&models.Follow{}).
		Where("user_id = ? AND follow_user_id = ?", me, targetID).
		Count(&count)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": count > 0})
}

// 冗余保留：清理空引用编译
var _ = gorm.ErrRecordNotFound
