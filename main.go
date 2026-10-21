package main

import (
	"log"

	"video-feed/config"
	"video-feed/database"
	"video-feed/routes"

	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.Load()

	// 1. 连接 MySQL + 自动建表
	if err := database.InitMySQL(&cfg.MySQL); err != nil {
		log.Fatalf("MySQL 初始化失败: %v", err)
	}

	// 2. 连接 Redis
	database.InitRedis(&cfg.Redis)

	// 3. 启动时把已有视频灌入热门榜 + 发现页全站时间线
	database.InitHotRank()
	database.InitDiscoverTimeline()

	// 4. 启动 HTTP 服务
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	routes.RegisterRoutes(r)

	addr := cfg.Server.Host + ":" + cfg.Server.Port
	log.Printf("🚀 Video Feed 服务启动: http://%s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}
