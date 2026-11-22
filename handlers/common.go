package handlers

import (
	"strconv"

	"video-feed/apierror"

	"github.com/gin-gonic/gin"
)

// parseID 解析路径里的 uint id；非法时直接写 400 并返回 false。
func parseID(c *gin.Context, name string) (uint, bool) {
	v, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || v == 0 {
		apierror.Write(c, apierror.BadRequest("参数错误"))
		return 0, false
	}
	return uint(v), true
}

// queryInt64 解析整数 query，缺失或非法返回 0。
func queryInt64(c *gin.Context, key string) int64 {
	if s := c.Query(key); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	}
	return 0
}

// queryLimit 解析 limit，默认 def，范围 [1, max]。
func queryLimit(c *gin.Context, def, max int) int {
	if s := c.Query("limit"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 && v <= max {
			return v
		}
	}
	return def
}

// fail 统一错误出口：service 的 *apierror.Error 直接映射状态码，其余 500。
func fail(c *gin.Context, err error) {
	apierror.Write(c, err)
}
