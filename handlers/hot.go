package handlers

import (
	"fmt"
	"net/http"

	"video-feed/database"
	"video-feed/models"

	"github.com/gin-gonic/gin"
)

// GetHotVideos 热门视频榜：从 Redis ZSet 取前 N 个视频 ID，再回表查详情
// 播放量在 GetVideoDetail 里已经 ZIncrBy +1，这里只读
func GetHotVideos(c *gin.Context) {
	n := 10
	if v := c.Query("limit"); v != "" {
		if l, err := parseLimit(v); err == nil {
			n = l
		}
	}
	ids := database.GetHotVideos(n)
	if len(ids) == 0 {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": []gin.H{}})
		return
	}

	// singleflight 合并回源：同一时刻大量并发请求热榜，只让一个 goroutine 查 DB
	// key 用榜单规模 n（同一秒内 n 相同即可命中合并）
	v, _, _ := hotSF.Do(fmt.Sprintf("hot:%d", n), func() (interface{}, error) {
		return loadHotVideos(ids), nil
	})
	ranked := v.([]models.Video)

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": ranked})
}

// loadHotVideos 热榜回表：ids → 查视频 → 按热榜顺序重排 → 批量补作者
func loadHotVideos(ids []uint) []models.Video {
	var videos []models.Video
	database.DB.Where("id IN ? AND status = ?", ids, 1).Find(&videos)

	// 按热榜顺序重排（MySQL IN 不保证顺序）
	order := make(map[uint]int, len(ids))
	for i, id := range ids {
		order[id] = i
	}
	ranked := make([]models.Video, len(videos))
	for _, v := range videos {
		if idx, ok := order[v.ID]; ok {
			ranked[idx] = v
		}
	}
	fillVideoAuthors(&ranked)
	return ranked
}
