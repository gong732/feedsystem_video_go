package handlers

import (
	"fmt"
	"strconv"

	"video-feed/database"
	"video-feed/models"

	"github.com/gin-gonic/gin"
)

// parseLimit 解析 limit 参数，越界兜底
func parseLimit(s string) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 || v > 50 {
		return 10, err
	}
	return v, nil
}

// videoExists 视频是否存在且在架（点赞/评论前的存在性校验）
func videoExists(id uint) bool {
	var n int64
	database.DB.Model(&models.Video{}).
		Where("id = ? AND status = ?", id, 1).Count(&n)
	return n > 0
}

// invalidateVideoCache 删详情缓存（Cache-Aside：先写库，再删缓存）
//
// 为什么是"删"而不是"改"：并发写时两条 UPDATE 的先后顺序和缓存写入顺序可能交错，
// 把旧值写回缓存会造成长期脏读；删掉之后下一次读自然回源重建，最终一致。
func invalidateVideoCache(id uint) {
	database.DeleteCache(fmt.Sprintf("video:info:%d", id))
}

// fillLikedStatus 批量回填"当前登录用户是否点过赞"
// 一次 IN 查询覆盖整页视频，避免在 for 循环里逐个 COUNT（N+1）。
// 未登录时直接跳过，省略一次无意义的 DB 查询。
func fillLikedStatus(c *gin.Context, videos []models.Video) {
	if len(videos) == 0 {
		return
	}
	uid := c.GetUint("user_id")
	if uid == 0 {
		return
	}
	ids := make([]uint, 0, len(videos))
	for i := range videos {
		ids = append(ids, videos[i].ID)
	}
	var likes []models.Like
	database.DB.Select("video_id").
		Where("user_id = ? AND video_id IN ?", uid, ids).Find(&likes)
	liked := make(map[uint]bool, len(likes))
	for _, l := range likes {
		liked[l.VideoID] = true
	}
	for i := range videos {
		videos[i].Liked = liked[videos[i].ID]
	}
}

// userLiked 单个视频的点赞状态（详情页用）
func userLiked(uid, videoID uint) bool {
	if uid == 0 {
		return false
	}
	var n int64
	database.DB.Model(&models.Like{}).
		Where("user_id = ? AND video_id = ?", uid, videoID).Count(&n)
	return n > 0
}

// videoLikesCount 从库里读当前点赞数（写操作后返回给前端做即时更新）
func videoLikesCount(id uint) int64 {
	var v models.Video
	database.DB.Select("likes_count").Where("id = ?", id).First(&v)
	return v.LikesCount
}

// currentAuthor 取当前用户展示信息（评论发布后直接回给前端渲染）
func currentAuthor(uid uint) models.User {
	var u models.User
	database.DB.Select("id, username, nickname, avatar").First(&u, uid)
	return u
}
