package middleware

import (
	"net/http"
	"strings"

	"video-feed/config"
	"video-feed/utils"

	"github.com/gin-gonic/gin"
)

// 认证契约（handler 侧只看 user_id）：
//
//	Auth          → user_id 必定存在；缺失或无效 token 一律 401
//	AuthOptional  → 没带 token 视为游客；带了但无效（过期/伪造）返回 401
//	token 里只放 user_id：昵称等展示信息按 user_id 查库，避免签发时的快照过期。
//
// 注意：cfg 在注册路由时（Auth() 被调用时）加载并捕获，不是每次请求都读环境变量。
func Auth() gin.HandlerFunc {
	cfg := config.Load()
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
		c.Next()
	}
}

// AuthOptional 可选认证：
//   - 完全没带 Authorization：游客，放行，user_id 取到 0
//   - 带了 Bearer 但格式错/过期/伪造：明确返回 401，让前端清掉本地登录态
//   - 带了且有效：写入 user_id 后放行
//
// 为什么不再静默忽略无效 token：否则前端会一直显示"已登录"，
// 但列表里点赞状态全是游客态，直到用户点写操作才突然 401。
func AuthOptional() gin.HandlerFunc {
	cfg := config.Load()
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" {
			c.Next()
			return
		}
		if !strings.HasPrefix(auth, "Bearer ") {
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
		c.Next()
	}
}
