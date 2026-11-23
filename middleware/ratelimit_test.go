package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRateLimitLocalFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/limited", RateLimit("unittest-local", 2, time.Minute, false), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	do := func(remote string) int {
		req := httptest.NewRequest(http.MethodGet, "/limited", nil)
		req.RemoteAddr = remote
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	if code := do("10.1.1.1:1000"); code != http.StatusOK {
		t.Fatalf("第 1 次请求 = %d, want 200", code)
	}
	if code := do("10.1.1.1:1000"); code != http.StatusOK {
		t.Fatalf("第 2 次请求 = %d, want 200", code)
	}
	if code := do("10.1.1.1:1000"); code != http.StatusTooManyRequests {
		t.Fatalf("第 3 次请求 = %d, want 429", code)
	}

	// 不同 IP 各自独立计数。
	if code := do("10.1.1.2:1000"); code != http.StatusOK {
		t.Fatalf("另一个 IP 第 1 次请求 = %d, want 200", code)
	}
}
