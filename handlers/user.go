package handlers

import (
	"video-feed/apierror"
	"video-feed/service"

	"github.com/gin-gonic/gin"
)

type registerReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Nickname string `json:"nickname"`
}

// Register 注册。
func Register(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apierror.BadRequest("参数错误"))
		return
	}
	u, err := service.Users.Register(c.Request.Context(), req.Username, req.Password, req.Nickname)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "msg": "注册成功", "data": u})
}

// Login 登录，返回 token + 用户信息。
func Login(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apierror.BadRequest("参数错误"))
		return
	}
	token, u, err := service.Users.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "msg": "登录成功", "data": gin.H{
		"token": token,
		"user":  u,
	}})
}

// Logout 退出登录：递增 token_version，旧 token 全部失效。
func Logout(c *gin.Context) {
	uid := c.GetUint("user_id")
	if err := service.Users.Logout(c.Request.Context(), uid); err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "msg": "已退出登录"})
}

// GetUserInfo 用户资料（含关注数/粉丝数）。
func GetUserInfo(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	info, err := service.Users.Info(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": info})
}
