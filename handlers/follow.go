package handlers

import (
	"video-feed/service"

	"github.com/gin-gonic/gin"
)

// FollowUser 关注 / 取关（toggle）。
func FollowUser(c *gin.Context) {
	target, ok := parseID(c, "user_id")
	if !ok {
		return
	}
	msg, err := service.Follows.Toggle(c.Request.Context(), c.GetUint("user_id"), target)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "msg": msg})
}

// FollowingList 我关注的人（分页，游标为 follow 行 id）。
func FollowingList(c *gin.Context) {
	uid, ok := parseID(c, "user_id")
	if !ok {
		return
	}
	page, err := service.Follows.Following(c.Request.Context(), uid, queryInt64(c, "cursor"), queryLimit(c, 20, 50))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": page})
}

// FollowerList 我的粉丝（分页）。
func FollowerList(c *gin.Context) {
	uid, ok := parseID(c, "user_id")
	if !ok {
		return
	}
	page, err := service.Follows.Followers(c.Request.Context(), uid, queryInt64(c, "cursor"), queryLimit(c, 20, 50))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": page})
}

// IsFollowing 是否已关注（游客返回 false）。
func IsFollowing(c *gin.Context) {
	target, ok := parseID(c, "user_id")
	if !ok {
		return
	}
	okFollow, err := service.Follows.IsFollowing(c.Request.Context(), c.GetUint("user_id"), target)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": okFollow})
}
