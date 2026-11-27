package service

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"video-feed/models"
	"video-feed/repository"

	"golang.org/x/sync/singleflight"
)

var (
	videoSF    singleflight.Group
	discoverSF singleflight.Group
	feedSF     singleflight.Group
)

// Page 统一分页响应：list 永远非 nil，next_cursor=0 表示没有下一页。
type Page[T any] struct {
	List       []T    `json:"list"`
	NextCursor string `json:"next_cursor"` // 格式:"ts:id"，空串=到底
}

// formatCursor 把游标编码成 "ts:id"。
func formatCursor(ts int64, id uint) string {
	return fmt.Sprintf("%d:%d", ts, id)
}

// parseCursor 解析 "ts:id"；空串或非法格式都返回 (0,0)，即第一页。
func parseCursor(s string) (int64, uint) {
	if s == "" {
		return 0, 0
	}
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	ts, err1 := strconv.ParseInt(parts[0], 10, 64)
	id, err2 := strconv.ParseUint(parts[1], 10, 64)
	if err1 != nil || err2 != nil || ts <= 0 || id == 0 {
		return 0, 0
	}
	return ts, uint(id)
}
func newPage[T any](list []T) *Page[T] {
	if list == nil {
		list = make([]T, 0)
	}
	return &Page[T]{List: list}
}

// fillVideoAuthors 批量回填作者信息，避免每条视频一次查询。
func fillVideoAuthors(ctx context.Context, videos []models.Video) {
	if len(videos) == 0 {
		return
	}
	ids := make([]uint, 0, len(videos))
	seen := make(map[uint]bool, len(videos))
	for _, v := range videos {
		if !seen[v.UserID] {
			seen[v.UserID] = true
			ids = append(ids, v.UserID)
		}
	}

	users, err := repository.FindUsersByIDs(ctx, ids)
	if err != nil {
		log.Printf("[service] 回填作者失败: %v", err)
		return
	}
	m := make(map[uint]models.User, len(users))
	for _, u := range users {
		m[u.ID] = u
	}
	for i := range videos {
		if u, ok := m[videos[i].UserID]; ok {
			videos[i].Author = u
		}
	}
}

// fillLikedStatus 批量回填当前用户的点赞状态（游客不查）。
func fillLikedStatus(ctx context.Context, uid uint, videos []models.Video) {
	if uid == 0 || len(videos) == 0 {
		return
	}
	ids := make([]uint, 0, len(videos))
	for _, v := range videos {
		ids = append(ids, v.ID)
	}
	set, err := repository.LikedSet(ctx, uid, ids)
	if err != nil {
		log.Printf("[service] 回填点赞状态失败: %v", err)
		return
	}
	for i := range videos {
		videos[i].Liked = set[videos[i].ID]
	}
}

// sortByTs 按 created_ts 倒序；同一毫秒用 id 倒序保证稳定。
func sortByTs(list []models.Video) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedTs != list[j].CreatedTs {
			return list[i].CreatedTs > list[j].CreatedTs
		}
		return list[i].ID > list[j].ID
	})
}

// orderByTimeline 按 Redis 时间线顺序重排 IN 查询结果，跳过已删除/下架的 id。
func orderByTimeline(videos []models.Video, items []repository.TimelineItem) []models.Video {
	m := make(map[uint]models.Video, len(videos))
	for _, v := range videos {
		m[v.ID] = v
	}
	out := make([]models.Video, 0, len(items))
	for _, it := range items {
		if v, ok := m[it.VideoID]; ok {
			out = append(out, v)
		}
	}
	return out
}

// videosToTimeline 把视频列表转成时间线条目。
func videosToTimeline(videos []models.Video) []repository.TimelineItem {
	items := make([]repository.TimelineItem, 0, len(videos))
	for _, v := range videos {
		items = append(items, repository.TimelineItem{VideoID: v.ID, Ts: v.CreatedTs})
	}
	return items
}
