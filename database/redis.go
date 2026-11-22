package database

import (
	"context"
	"time"

	"video-feed/config"

	"github.com/redis/go-redis/v9"
)

// RDB 全局 Redis 客户端。Redis 不可用时为 nil，所有读写路径都要能降级到 MySQL。
var RDB *redis.Client

// InitRedis 连接 Redis。失败只返回错误，不终止进程：
// 缓存/热榜/时间线缺失时由上层回源 MySQL，服务本身仍然可用。
func InitRedis(cfg *config.RedisConfig) error {
	client := redis.NewClient(&redis.Options{
		Addr: cfg.Host + ":" + cfg.Port,
		// Redis 是可选依赖：超时要短、禁止重试，故障时快速失败并回源 MySQL，
		// 不能让每个请求都卡在连接重试上。
		DialTimeout:   2 * time.Second,
		ReadTimeout:   time.Second,
		WriteTimeout:  time.Second,
		PoolTimeout:   2 * time.Second,
		MaxRetries:    -1,
		DialerRetries: 1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := client.Ping(ctx).Result(); err != nil {
		_ = client.Close()
		RDB = nil
		return err
	}
	RDB = client
	return nil
}

// CloseRedis 关闭连接。
func CloseRedis() {
	if RDB != nil {
		_ = RDB.Close()
		RDB = nil
	}
}
