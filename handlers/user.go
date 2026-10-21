package handlers

import (
	"net/http"
	"strconv"

	"video-feed/config"
	"video-feed/database"
	"video-feed/models"
	"video-feed/utils"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// RegisterReq 注册请求体
type RegisterReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Nickname string `json:"nickname"`
}

// Register 注册：用户名唯一校验 + 密码 bcrypt 哈希入库
func Register(c *gin.Context) {
	var req RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误"})
		return
	}
	// 用户名查重
	var count int64
	database.DB.Model(&models.User{}).Where("username = ?", req.Username).Count(&count)
	if count > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "用户名已存在"})
		return
	}
	// bcrypt 加盐哈希（和博客系统一致：数据库里存的是哈希，不是明文）
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	user := models.User{
		Username: req.Username,
		Nickname: req.Nickname,
		Password: string(hash),
	}
	if user.Nickname == "" {
		user.Nickname = req.Username
	}
	if err := database.DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "注册失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "注册成功", "data": user})
}

// Login 登录：查用户 → bcrypt 比对 → 签发 JWT
func Login(c *gin.Context) {
	var req RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误"})
		return
	}
	var user models.User
	err := database.DB.Where("username = ?", req.Username).First(&user).Error
	if err == gorm.ErrRecordNotFound {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "用户不存在"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "密码错误"})
		return
	}
	cfg := config.Load()
	token, _ := utils.GenerateToken(&cfg.JWT, user.ID, user.Username)
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "登录成功", "data": gin.H{
		"token": token,
		"user":  user,
	}})
}

// GetUserInfo 获取用户资料（含关注数/粉丝数）
func GetUserInfo(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var user models.User
	if err := database.DB.First(&user, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "用户不存在"})
		return
	}
	var following, followers int64
	database.DB.Model(&models.Follow{}).Where("user_id = ?", user.ID).Count(&following)
	database.DB.Model(&models.Follow{}).Where("follow_user_id = ?", user.ID).Count(&followers)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"id":        user.ID,
		"username":  user.Username,
		"nickname":  user.Nickname,
		"avatar":    user.Avatar,
		"following": following,
		"followers": followers,
		"created_at": user.CreatedAt,
	}})
}
