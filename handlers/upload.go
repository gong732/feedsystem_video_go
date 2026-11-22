package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// 上传落盘根目录：static/uploads。因为 routes 里已经 r.Static("/static", "./static")，
// 写进这个目录的文件天然可被 /static/... 访问，不需要额外注册路由。
const uploadRoot = "./static/uploads"

const (
	maxVideoSize = 200 << 20 // 200MB，和参考实现对齐
	maxCoverSize = 10 << 20  // 10MB
)

var videoExtWhitelist = map[string]bool{".mp4": true}

var coverExtWhitelist = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
}

// UploadVideo 上传视频文件：POST /api/upload/video，multipart 字段名 file。
// 返回站内相对 URL（/static/uploads/...），不拼 Host：
//   - 换域名/换端口/反代都不会失效
//   - 数据库里存的还是 URL 字符串，字段结构不变
func UploadVideo(c *gin.Context) {
	uploadFile(c, "video", "videos", maxVideoSize, videoExtWhitelist)
}

// UploadCover 上传封面图：POST /api/upload/cover，multipart 字段名 file。
func UploadCover(c *gin.Context) {
	uploadFile(c, "cover", "covers", maxCoverSize, coverExtWhitelist)
}

// uploadFile 公共上传逻辑：鉴权 → 限大小 → 白名单扩展名 → 随机文件名 → 落盘 → 返回 URL。
// 文件名完全由服务端生成，不使用客户端文件名，避免 ../ 路径穿越和覆盖已有文件。
func uploadFile(c *gin.Context, kind, subDir string, maxSize int64, allowed map[string]bool) {
	uid := c.GetUint("user_id")
	if uid == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "请先登录"})
		return
	}

	// 在解析表单前限制请求体大小，避免超大文件把磁盘/内存打满。
	// 多留 1MB 给 multipart 边界和表单其他字段。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSize+(1<<20))

	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "未收到文件（multipart 字段名应为 file）"})
		return
	}
	if fh.Size <= 0 || fh.Size > maxSize {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": fmt.Sprintf("文件大小需在 1B~%dMB 之间", maxSize>>20)})
		return
	}

	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if !allowed[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "不支持的文件类型：" + ext})
		return
	}

	name, err := randHexName(16)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "生成文件名失败"})
		return
	}

	// 按 用户/日期 分目录：便于排查，也避免单目录文件过多
	relDir := filepath.Join("uploads", subDir, fmt.Sprintf("%d", uid), time.Now().Format("20060102"))
	absDir := filepath.Join("static", relDir)
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "创建上传目录失败"})
		return
	}

	filename := name + ext
	absPath := filepath.Join(absDir, filename)
	if err := c.SaveUploadedFile(fh, absPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "保存文件失败"})
		return
	}

	// URL 用正斜杠，Windows 下 filepath.Join 会产出反斜杠，不能直接拼进 URL
	urlPath := "/static/" + filepath.ToSlash(filepath.Join(relDir, filename))

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"url":  urlPath,
		"kind": kind,
		"size": fh.Size,
	}})
}

// randHexName 生成 n 字节随机数的十六进制串（2n 个字符）。
func randHexName(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
