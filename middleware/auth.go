package middleware

import (
	"net/http"
	"strings"

	"video-feed/config"
	"video-feed/repository"
	"video-feed/utils"

	"github.com/gin-gonic/gin"
)

// 认证契约（handler 侧只看 user_id）：
//
//	Auth          → user_id 必定存在；缺失、格式错、无效、已撤销一律 401
//	AuthOptional  → 没带 token 视为游客；带了但无效（过期/伪造/已撤销）返回 401
//
// token 里只放 user_id + token_version：昵称按 user_id 查库，版本号用于退出登录后立即失效。
func Auth() gin.HandlerFunc { return authenticate(false) }

func AuthOptional() gin.HandlerFunc { return authenticate(true) }

func authenticate(optional bool) gin.HandlerFunc {
	cfg := config.Load()
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" {
			if optional {
				c.Next()
				return
			}
			unauthorized(c, "未登录")
			return
		}

		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
			unauthorized(c, "未登录")
			return
		}

		claims, err := utils.ParseToken(&cfg.JWT, parts[1])
		if err != nil {
			unauthorized(c, "登录已过期")
			return
		}

		// 校验登录态版本：退出登录会递增 users.token_version，旧 token 立即失效。
		// Redis 缓存 5 分钟，Redis 不可用时回源 MySQL。
		ver, err := repository.TokenVersion(c.Request.Context(), claims.UserID)
		if err != nil || ver != claims.Version {
			unauthorized(c, "登录已过期")
			return
		}

		c.Set("user_id", claims.UserID)
		c.Next()
	}
}

func unauthorized(c *gin.Context, msg string) {
	c.JSON(http.StatusUnauthorized, gin.H{"code": http.StatusUnauthorized, "msg": msg})
	c.Abort()
}
