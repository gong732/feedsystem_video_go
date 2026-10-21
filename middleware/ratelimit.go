package middleware

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// ipLimiters 按客户端 IP 维护各自的令牌桶（map 缓存，懒创建）
var (
	ipLimiters   = make(map[string]*rate.Limiter)
	ipLimitersMu sync.Mutex
)

// getLimiter 取某个 IP 的限流器，第一次见这个 IP 才建桶
// 局限：单机内存 map 只增不减——生产上 IP 规模大时需定期清理，
// 或改用 Redis 计数器做分布式限流（多实例共享同一个计数）
func getLimiter(ip string, r rate.Limit, burst int) *rate.Limiter {
	ipLimitersMu.Lock()
	defer ipLimitersMu.Unlock()
	l, ok := ipLimiters[ip]
	if !ok {
		l = rate.NewLimiter(r, burst)
		ipLimiters[ip] = l
	}
	return l
}

// RateLimit 令牌桶限流中间件（按 IP 维度）
//   - r     每秒补充的令牌数 → 长期平均速率
//   - burst 桶容量 → 允许短时间突发（正常用户 3 个并发请求不会误伤）
//
// 令牌桶思想：匀速产令牌、可突发消费；没令牌 → 429 拒绝，c.Abort 不进 Handler
func RateLimit(r rate.Limit, burst int) gin.HandlerFunc {
	return func(c *gin.Context) {
		limiter := getLimiter(c.ClientIP(), r, burst)
		if !limiter.Allow() { // Allow() 非阻塞：尝试取 1 个令牌，取不到返回 false
			c.JSON(http.StatusTooManyRequests, gin.H{"code": 429, "msg": "请求过于频繁，请稍后再试"})
			c.Abort()
			return
		}
		c.Next()
	}
}
