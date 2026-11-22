package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"video-feed/database"
	"video-feed/repository"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// RateLimit 按 scope + 维度做固定窗口限流。
//
//	scope     限流域（login / register / like / comment / upload ...）
//	limit     窗口内允许的请求数
//	window    窗口长度
//	byAccount true=按登录用户，false=按客户端 IP
//
// Redis 可用时用 INCR + EXPIRE 做分布式固定窗口；Redis 不可用时降级为进程内令牌桶，
// 保证单机仍然有基本保护（多实例部署时应确保 Redis 可用）。
func RateLimit(scope string, limit int, window time.Duration, byAccount bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := rateKey(c, byAccount)

		ok, err := allowRedis(c, scope, key, limit, window)
		if err != nil {
			// 降级：Redis 挂了不能把整个服务变成无限流。
			ok = allowLocal(scope+":"+key, limit, window)
		}

		if !ok {
			c.JSON(http.StatusTooManyRequests, gin.H{"code": http.StatusTooManyRequests, "msg": "请求过于频繁，请稍后再试"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func rateKey(c *gin.Context, byAccount bool) string {
	if byAccount {
		if uid := c.GetUint("user_id"); uid > 0 {
			return "u:" + strconv.FormatUint(uint64(uid), 10)
		}
	}
	return "ip:" + c.ClientIP()
}

func allowRedis(c *gin.Context, scope, key string, limit int, window time.Duration) (bool, error) {
	if !repository.RedisReady() {
		return false, repository.ErrCacheDisabled
	}

	ctx := c.Request.Context()
	bucket := time.Now().UnixNano() / int64(window)
	rkey := fmt.Sprintf("rl:%s:%s:%d", scope, key, bucket)

	pipe := database.RDB.TxPipeline()
	incr := pipe.Incr(ctx, rkey)
	pipe.Expire(ctx, rkey, 2*window)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, repository.NoteRedisError(err)
	}
	return incr.Val() <= int64(limit), nil
}

// ---------- 进程内降级实现 ----------

type localEntry struct {
	limiter *rate.Limiter
	last    time.Time
}

var (
	localMu      sync.Mutex
	localBuckets = make(map[string]*localEntry)
	localOnce    sync.Once
)

func allowLocal(key string, limit int, window time.Duration) bool {
	localOnce.Do(func() { go janitor() })

	localMu.Lock()
	e, ok := localBuckets[key]
	if !ok {
		perSecond := float64(limit) / window.Seconds()
		e = &localEntry{limiter: rate.NewLimiter(rate.Limit(perSecond), limit)}
		localBuckets[key] = e
	}
	e.last = time.Now()
	localMu.Unlock()
	return e.limiter.Allow()
}

// janitor 定期清理长期不用的 key，避免 map 无界增长（原实现的内存泄漏点）。
func janitor() {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for range t.C {
		cutoff := time.Now().Add(-30 * time.Minute)
		localMu.Lock()
		for k, e := range localBuckets {
			if e.last.Before(cutoff) {
				delete(localBuckets, k)
			}
		}
		localMu.Unlock()
	}
}

// BodyLimit 限制 JSON 请求体大小；上传接口由自身按文件大小限制，这里跳过。
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/upload") {
			c.Next()
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
