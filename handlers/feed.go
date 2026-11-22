package handlers

import (
	"video-feed/service"

	"github.com/gin-gonic/gin"
)

// GetFeed 关注流：since 拉新 / cursor 翻旧 / 首页热区+冷区。
func GetFeed(c *gin.Context) {
	page, err := service.Feeds.Get(
		c.Request.Context(),
		c.GetUint("user_id"),
		queryInt64(c, "since"),
		queryInt64(c, "cursor"),
		queryLimit(c, 10, 50),
	)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": page})
}
