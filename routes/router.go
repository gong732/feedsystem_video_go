package routes

import (
	"video-feed/handlers"
	"video-feed/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes 注册全部路由
func RegisterRoutes(r *gin.Engine) {
	// 静态前端
	r.Static("/static", "./static")
	r.GET("/", func(c *gin.Context) {
		c.File("./static/index.html")
	})

	// 健康检查
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"msg": "pong"})
	})

	api := r.Group("/api")

	// 全局限流：每 IP 每秒 10 个令牌、桶容量 20（允许短突发）
	// 令牌桶中间件挡脚本刷接口，保护 MySQL/Redis 不被打挂
	api.Use(middleware.RateLimit(10, 20))

	// ---- 用户：注册/登录 不需要登录 ----
	api.POST("/users/register", handlers.Register)
	api.POST("/users/login", handlers.Login)
	api.GET("/users/:id", middleware.AuthOptional(), handlers.GetUserInfo)

	// ---- 关注：需要登录 ----
	follow := api.Group("", middleware.Auth())
	follow.POST("/follow/:user_id", handlers.FollowUser)
	follow.GET("/following/:user_id", handlers.FollowingList)
	follow.GET("/followers/:user_id", handlers.FollowerList)
	follow.GET("/is_following/:user_id", handlers.IsFollowing)

	// ---- 视频：浏览公开，但挂软鉴权（登录了就能带上身份，为第三批 AI 个性化铺路）----
	api.GET("/videos", middleware.AuthOptional(), handlers.GetVideoList)
	api.GET("/videos/:id", middleware.AuthOptional(), handlers.GetVideoDetail)
	api.POST("/videos", middleware.Auth(), handlers.CreateVideo)

	// ---- Feed 时间线：需要登录（知道你是谁才知道关注了谁）----
	api.GET("/feed", middleware.Auth(), handlers.GetFeed)

	// ---- 热门榜：公开 + 软鉴权 ----
	api.GET("/hot", middleware.AuthOptional(), handlers.GetHotVideos)
}
