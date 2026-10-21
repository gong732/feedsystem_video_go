package middleware

import (
	"net/http"
	"strings"

	"video-feed/config"
	"video-feed/utils"

	"github.com/gin-gonic/gin"
)

// Auth 登录认证中间件：校验 Authorization: Bearer <token>
// 通过后把用户 ID 塞进上下文，后续 handler 用 c.GetUint("user_id") 取
func Auth() gin.HandlerFunc {
	cfg := config.Load() // 每次请求加载配置（简单够用）
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "未登录"})
			c.Abort()
			return
		}
		claims, err := utils.ParseToken(&cfg.JWT, strings.TrimPrefix(auth, "Bearer "))
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "登录已过期"})
			c.Abort()
			return
		}
		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Next()
	}
}

// AuthOptional 可选认证：有 token 就解析出用户，没有也不拦截
// 用于"未登录也能看，登录了能点赞"的场景
func AuthOptional() gin.HandlerFunc {
	cfg := config.Load()
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth != "" && strings.HasPrefix(auth, "Bearer ") {
			if claims, err := utils.ParseToken(&cfg.JWT, strings.TrimPrefix(auth, "Bearer ")); err == nil {
				c.Set("user_id", claims.UserID)
			}
		}
		c.Next()
	}
}
