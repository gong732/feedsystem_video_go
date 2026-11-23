package handlers

import (
	"video-feed/service"

	"github.com/gin-gonic/gin"
)

// ToggleLike 点赞 / 取消点赞（toggle 语义，唯一索引裁决并发）。
func ToggleLike(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	res, err := service.Likes.Toggle(c.Request.Context(), c.GetUint("user_id"), id)
	if err != nil {
		fail(c, err)
		return
	}
	msg := "已取消点赞"
	if res.Liked {
		msg = "点赞成功"
	}
	c.JSON(200, gin.H{"code": 0, "msg": msg, "data": res})
}

// GetLikeStatus 点赞状态（未登录也能看总数）。
func GetLikeStatus(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	res, err := service.Likes.Status(c.Request.Context(), c.GetUint("user_id"), id)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": res})
}
