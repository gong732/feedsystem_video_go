package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"video-feed/config"
	"video-feed/database"
	"video-feed/repository"
	"video-feed/routes"

	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.Load()

	if err := database.InitMySQL(&cfg.MySQL); err != nil {
		log.Fatalf("MySQL 初始化失败: %v", err)
	}
	defer database.CloseMySQL()

	// Redis 是可选依赖：连不上就降级为 MySQL-only，服务仍然可用。
	if err := database.InitRedis(&cfg.Redis); err != nil {
		log.Printf("⚠️ Redis 不可用，降级为 MySQL-only: %v", err)
	} else {
		defer database.CloseRedis()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := repository.RebuildDiscover(ctx); err != nil {
			log.Printf("⚠️ 重建发现页时间线失败（读时仍会回源 MySQL）: %v", err)
		}
		cancel()
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	// 不信任任何代理头：ClientIP 直接取 RemoteAddr，防止伪造 X-Forwarded-For 绕过限流。
	if err := r.SetTrustedProxies(nil); err != nil {
		log.Printf("⚠️ 设置可信代理失败: %v", err)
	}
	routes.RegisterRoutes(r)

	srv := &http.Server{
		Addr:              cfg.Server.Host + ":" + cfg.Server.Port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("🚀 Video Feed 服务启动: http://%s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Println("收到退出信号，开始优雅关闭...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("⚠️ 优雅关闭超时: %v", err)
	}
	log.Println("服务已退出")
}
