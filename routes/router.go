package routes

import (
	"time"

	"video-feed/handlers"
	"video-feed/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes 注册全部路由。
//
// 限流分两类：
//   - 写操作按账号（登录后可精确定位到人）
//   - 登录/注册按 IP（此时还没有身份）
func RegisterRoutes(r *gin.Engine) {
	r.Static("/static", "./static")
	r.GET("/", func(c *gin.Context) { c.File("./static/index.html") })
	r.GET("/ping", func(c *gin.Context) { c.JSON(200, gin.H{"msg": "pong"}) })

	api := r.Group("/api")
	// 全局：每 IP 30 req/s；JSON 请求体最多 1MB（上传接口自行放宽）。
	api.Use(middleware.RateLimit("global", 30, time.Second, false))
	api.Use(middleware.BodyLimit(1 << 20))

	// ---- 用户 ----
	api.POST("/users/register", middleware.RateLimit("register", 5, time.Hour, false), handlers.Register)
	api.POST("/users/login", middleware.RateLimit("login", 10, time.Minute, false), handlers.Login)
	api.POST("/users/logout", middleware.Auth(), handlers.Logout)
	api.GET("/users/:id", middleware.AuthOptional(), handlers.GetUserInfo)

	// ---- 视频浏览/发布 ----
	api.GET("/videos", middleware.AuthOptional(), handlers.GetVideoList)
	api.GET("/videos/:id", middleware.AuthOptional(), handlers.GetVideoDetail)
	api.POST("/videos", middleware.Auth(), handlers.CreateVideo)

	// ---- 文件上传 ----
	api.POST("/upload/video", middleware.Auth(), middleware.RateLimit("upload", 5, time.Hour, true), handlers.UploadVideo)
	api.POST("/upload/cover", middleware.Auth(), middleware.RateLimit("upload", 5, time.Hour, true), handlers.UploadCover)

	// ---- 点赞 ----
	api.POST("/videos/:id/like", middleware.Auth(), middleware.RateLimit("like", 30, time.Minute, true), handlers.ToggleLike)
	api.GET("/videos/:id/like", middleware.AuthOptional(), handlers.GetLikeStatus)

	// ---- 评论 ----
	api.POST("/videos/:id/comments", middleware.Auth(), middleware.RateLimit("comment", 10, time.Minute, true), handlers.CreateComment)
	api.GET("/videos/:id/comments", middleware.AuthOptional(), handlers.ListComments)
	api.DELETE("/comments/:id", middleware.Auth(), handlers.DeleteComment)

	// ---- 关注 ----
	api.POST("/follow/:user_id", middleware.Auth(), middleware.RateLimit("follow", 20, time.Minute, true), handlers.FollowUser)
	api.GET("/following/:user_id", middleware.Auth(), handlers.FollowingList)
	api.GET("/followers/:user_id", middleware.Auth(), handlers.FollowerList)
	api.GET("/is_following/:user_id", middleware.Auth(), handlers.IsFollowing)

	// ---- 关注流 / 热榜 ----
	api.GET("/feed", middleware.Auth(), handlers.GetFeed)
	api.GET("/hot", middleware.AuthOptional(), handlers.GetHotVideos)
}
