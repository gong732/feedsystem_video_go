package handlers

import (
	"video-feed/apierror"
	"video-feed/service"

	"github.com/gin-gonic/gin"
)

type createVideoReq struct {
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
	VideoURL    string `json:"video_url" binding:"required"`
	CoverURL    string `json:"cover_url"`
	Duration    int    `json:"duration"`
}

// CreateVideo 发布视频（user_id 来自 Auth 中间件）。
func CreateVideo(c *gin.Context) {
	var req createVideoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apierror.BadRequest("参数错误"))
		return
	}
	v, err := service.Videos.Create(c.Request.Context(), c.GetUint("user_id"), service.CreateVideoInput{
		Title:       req.Title,
		Description: req.Description,
		VideoURL:    req.VideoURL,
		CoverURL:    req.CoverURL,
		Duration:    req.Duration,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "msg": "发布成功", "data": v})
}

// GetVideoList 发现页：游标翻页。
func GetVideoList(c *gin.Context) {
	page, err := service.Videos.Discover(
		c.Request.Context(),
		c.GetUint("user_id"),
		c.Query("cursor"),
		queryLimit(c, 6, 50),
	)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": page})
}

// GetVideoDetail 视频详情（播放量自增）。
func GetVideoDetail(c *gin.Context) {
	id, ok := parseID(c, "id")
	if !ok {
		return
	}
	v, err := service.Videos.Detail(c.Request.Context(), c.GetUint("user_id"), id)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": v})
}

// GetHotVideos 热榜（分钟窗口，MySQL 兜底）。
func GetHotVideos(c *gin.Context) {
	videos, err := service.Videos.Hot(c.Request.Context(), c.GetUint("user_id"), queryLimit(c, 10, 50))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": videos})
}
