package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"video-feed/apierror"
	"video-feed/config"
	"video-feed/repository"

	"github.com/gin-gonic/gin"
)

const (
	maxVideoSize = 200 << 20 // 200MB
	maxCoverSize = 10 << 20  // 10MB
)

var videoExtWhitelist = map[string]bool{".mp4": true}

var coverExtWhitelist = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
}

// UploadVideo 上传视频文件：POST /api/upload/video，字段名 file。
func UploadVideo(c *gin.Context) {
	uploadFile(c, "video", "videos", maxVideoSize, videoExtWhitelist)
}

// UploadCover 上传封面图：POST /api/upload/cover，字段名 file。
func UploadCover(c *gin.Context) {
	uploadFile(c, "cover", "covers", maxCoverSize, coverExtWhitelist)
}

// uploadFile 鉴权 → 配额 → 大小 → 扩展名白名单 + 魔数校验 → 随机文件名落盘。
func uploadFile(c *gin.Context, kind, subDir string, maxSize int64, allowed map[string]bool) {
	uid := c.GetUint("user_id")
	if uid == 0 {
		fail(c, apierror.Unauthorized("请先登录"))
		return
	}
	ctx := c.Request.Context()
	day := time.Now().Format("20060102")
	maxDaily := int64(config.Load().Upload.DailyMB) << 20

	// Redis 不可用时不阻塞上传（配额只是运营保护，不是正确性依赖）。
	used, quotaOK := int64(0), false
	if maxDaily > 0 {
		v, err := repository.UploadQuotaUsed(ctx, uid, day)
		switch {
		case err == nil:
			used, quotaOK = v, true
		case errors.Is(err, repository.ErrCacheDisabled):
			// 忽略：无 Redis 无法计数
		default:
			log.Printf("[upload] 读取配额失败 uid=%d: %v", uid, err)
		}
	}

	// 多留 1MB 给 multipart 边界和表单其它字段。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSize+(1<<20))

	fh, err := c.FormFile("file")
	if err != nil {
		fail(c, apierror.BadRequest("未收到文件（multipart 字段名应为 file）"))
		return
	}
	if fh.Size <= 0 || fh.Size > maxSize {
		fail(c, apierror.BadRequest(fmt.Sprintf("文件大小需在 1B~%dMB 之间", maxSize>>20)))
		return
	}
	if quotaOK && used+fh.Size > maxDaily {
		fail(c, apierror.TooMany("今日上传额度已用完"))
		return
	}

	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if !allowed[ext] {
		fail(c, apierror.BadRequest("不支持的文件类型："+ext))
		return
	}

	// 魔数校验：扩展名可以随便改，文件头不会。防止把任意文件当 mp4/jpg 上传。
	detected := ""
	if f, err := fh.Open(); err == nil {
		head := make([]byte, 12)
		n, _ := f.Read(head)
		detected = sniffExt(head[:n])
		_ = f.Close()
	}
	if detected == "" || normalizeImageExt(detected) != normalizeImageExt(ext) {
		fail(c, apierror.BadRequest("文件内容与扩展名不符"))
		return
	}

	name, err := randHexName(16)
	if err != nil {
		fail(c, apierror.Internal(err))
		return
	}

	relDir := filepath.Join("uploads", subDir, fmt.Sprintf("%d", uid), day)
	absDir := filepath.Join("static", relDir)
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		fail(c, apierror.Internal(err))
		return
	}

	filename := name + ext
	if err := c.SaveUploadedFile(fh, filepath.Join(absDir, filename)); err != nil {
		fail(c, apierror.Internal(err))
		return
	}

	// 保存成功后再累加配额；失败只记日志，不回滚已落盘文件。
	if err := repository.AddUploadQuota(ctx, uid, day, fh.Size); err != nil && !errors.Is(err, repository.ErrCacheDisabled) {
		log.Printf("[upload] 累加配额失败 uid=%d: %v", uid, err)
	}

	urlPath := "/static/" + filepath.ToSlash(filepath.Join(relDir, filename))
	c.JSON(200, gin.H{"code": 0, "data": gin.H{
		"url":  urlPath,
		"kind": kind,
		"size": fh.Size,
	}})
}

// sniffExt 用文件头判断真实类型，识别不了返回空串。
func sniffExt(b []byte) string {
	switch {
	case len(b) >= 8 && b[4] == 'f' && b[5] == 't' && b[6] == 'y' && b[7] == 'p':
		return ".mp4"
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return ".jpg"
	case len(b) >= 8 && b[0] == 0x89 && b[1] == 'P' && b[2] == 'N' && b[3] == 'G' &&
		b[4] == 0x0D && b[5] == 0x0A && b[6] == 0x1A && b[7] == 0x0A:
		return ".png"
	case len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return ".webp"
	}
	return ""
}

func normalizeImageExt(ext string) string {
	if ext == ".jpeg" {
		return ".jpg"
	}
	return ext
}

// randHexName 生成 n 字节随机数的十六进制串（2n 个字符）。
func randHexName(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
