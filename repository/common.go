// Package repository 负责所有数据访问：MySQL（GORM）与 Redis。
//
// 约定：
//   - 每个方法第一个参数都是 context.Context，由 handler 的 c.Request.Context() 一路传下来；
//   - SQL 只出现在这里，service 不直接拼 SQL；
//   - 事务边界由 service 控制，repository 提供 Tx / *Tx 方法。
package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"video-feed/database"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// ErrCacheDisabled Redis 未启用时的哨兵错误，调用方据此回源 MySQL。
var ErrCacheDisabled = errors.New("redis disabled")

// redisDownUntil 熔断时间点（Unix 纳秒）。一次网络类错误后短暂跳过 Redis，
// 避免 Redis/DNS 故障时每个请求都卡在连接重试上。
var redisDownUntil atomic.Int64

const redisCooldown = 10 * time.Second

// RedisReady 当前是否可以尝试 Redis。熔断期间直接返回 false，调用方回源 MySQL。
func RedisReady() bool {
	return database.RDB != nil && time.Now().UnixNano() >= redisDownUntil.Load()
}

func markRedisDown() {
	redisDownUntil.Store(time.Now().Add(redisCooldown).UnixNano())
}

// NoteRedisError 记录 Redis 错误并触发熔断；redis.Nil 是正常的"键不存在"，不算故障。
func NoteRedisError(err error) error {
	if err != nil && !errors.Is(err, redis.Nil) {
		markRedisDown()
	}
	return err
}

// Tx 在 MySQL 事务中执行 fn；fn 内必须使用传入的 tx，不能用全局 DB。
func Tx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return database.DB.WithContext(ctx).Transaction(fn)
}

// CacheGet 读缓存。Redis 不可用或键不存在都返回 false。
func CacheGet(ctx context.Context, key string) (string, bool) {
	if !RedisReady() {
		return "", false
	}
	v, err := database.RDB.Get(ctx, key).Result()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			markRedisDown()
		}
		return "", false
	}
	return v, true
}

// CacheSet 写缓存。Redis 不可用时返回 ErrCacheDisabled。
func CacheSet(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	if !RedisReady() {
		return ErrCacheDisabled
	}
	return NoteRedisError(database.RDB.Set(ctx, key, val, ttl).Err())
}

// CacheDel 删除缓存。Redis 不可用时返回 ErrCacheDisabled。
func CacheDel(ctx context.Context, key string) error {
	if !RedisReady() {
		return ErrCacheDisabled
	}
	return NoteRedisError(database.RDB.Del(ctx, key).Err())
}

// VideoInfoKey 视频详情缓存键。
func VideoInfoKey(id uint) string { return fmt.Sprintf("video:info:%d", id) }

// VideoNullKey 视频空值缓存键（防穿透）。
func VideoNullKey(id uint) string { return fmt.Sprintf("video:null:%d", id) }

// TimelineItem 时间线条目：视频 id + 毫秒时间戳。
// service 不直接依赖 redis.Z，仓库层负责转换。
type TimelineItem struct {
	VideoID uint
	Ts      int64
}

// redisZToItems 把 ZSet 结果转成领域结构。
func redisZToItems(zs []redis.Z) []TimelineItem {
	items := make([]TimelineItem, 0, len(zs))
	for _, z := range zs {
		member, ok := z.Member.(string)
		if !ok {
			continue
		}
		vid, err := strconv.ParseUint(member, 10, 64)
		if err != nil || vid == 0 {
			continue
		}
		items = append(items, TimelineItem{VideoID: uint(vid), Ts: int64(z.Score)})
	}
	return items
}
