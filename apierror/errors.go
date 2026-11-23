// Package apierror 统一业务错误与 HTTP 响应映射。
//
// service 层只返回 *apierror.Error（或底层 error），handler 统一调用 Write，
// 避免每个接口各写一套状态码和 {code,msg} 拼装逻辑。
package apierror

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Error 业务错误：Status 决定 HTTP 状态码，Msg 是给用户看的文案。
type Error struct {
	Status int
	Msg    string
	Err    error // 底层错误，只进日志，不返回给客户端
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Msg + ": " + e.Err.Error()
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

func New(status int, msg string, err error) *Error {
	return &Error{Status: status, Msg: msg, Err: err}
}

func BadRequest(msg string) *Error   { return New(http.StatusBadRequest, msg, nil) }
func Unauthorized(msg string) *Error { return New(http.StatusUnauthorized, msg, nil) }
func Forbidden(msg string) *Error    { return New(http.StatusForbidden, msg, nil) }
func NotFound(msg string) *Error     { return New(http.StatusNotFound, msg, nil) }
func Conflict(msg string) *Error     { return New(http.StatusConflict, msg, nil) }
func TooMany(msg string) *Error      { return New(http.StatusTooManyRequests, msg, nil) }

// Internal 包装底层错误；对外只返回"服务内部错误"，细节写日志。
func Internal(err error) *Error {
	return New(http.StatusInternalServerError, "服务内部错误", err)
}

// Write 把错误转换成统一的 JSON 响应。
func Write(c *gin.Context, err error) {
	if err == nil {
		return
	}

	var e *Error
	if errors.As(err, &e) {
		if e.Status >= http.StatusInternalServerError && e.Err != nil {
			log.Printf("[api] %s %s: %v", c.Request.Method, c.Request.URL.Path, e.Err)
		}
		c.JSON(e.Status, gin.H{"code": e.Status, "msg": e.Msg})
		return
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"code": http.StatusNotFound, "msg": "资源不存在"})
		return
	}

	log.Printf("[api] %s %s unhandled: %v", c.Request.Method, c.Request.URL.Path, err)
	c.JSON(http.StatusInternalServerError, gin.H{"code": http.StatusInternalServerError, "msg": "服务内部错误"})
}
