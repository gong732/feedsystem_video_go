package service

import (
	"context"

	"video-feed/apierror"
	"video-feed/models"
	"video-feed/repository"
)

// FeedService 关注流：热时间线（Redis）+ 冷区（MySQL keyset）组合。
type FeedService struct{}

// Get 关注流。三种模式：
//
//	since > 0        拉新：只要比 since 更新的视频（前端轮询用）
//	cursor > 0       翻页：直接从 MySQL 取比 cursor 更老的（保证翻页不重复）
//	since = cursor=0 首页：Redis 热时间线优先，不足再补 MySQL 冷区
func (s *FeedService) Get(ctx context.Context, me uint, since, cursor int64, limit int) (*Page[models.Video], error) {
	if limit <= 0 || limit > 50 {
		limit = 12
	}
	need := limit + 1

	followIDs, err := repository.FollowIDs(ctx, me)
	if err != nil {
		return nil, apierror.Internal(err)
	}
	if len(followIDs) == 0 {
		return newPage([]models.Video{}), nil
	}

	var pool []models.Video
	followSet := make(map[uint]struct{}, len(followIDs))
	for _, id := range followIDs {
		followSet[id] = struct{}{}
	}

	switch {
	case since > 0:
		// 拉新：先看热时间线，不够再查 MySQL（只取比 since 新的）。
		if hot, err := repository.FeedHot(ctx, me); err == nil {
			items := make([]repository.TimelineItem, 0, len(hot))
			for _, it := range hot {
				if it.Ts > since && len(items) < need {
					items = append(items, it)
				}
			}
			if len(items) > 0 {
				videos, err := s.loadByTimeline(ctx, items)
				if err != nil {
					return nil, apierror.Internal(err)
				}
				pool = videos
			}
		}
		if len(pool) < need {
			dbVideos, err := repository.ListNewDB(ctx, followIDs, since, need)
			if err != nil {
				return nil, apierror.Internal(err)
			}
			pool = mergeVideos(pool, dbVideos)
		}

	case cursor > 0:
		// 翻页：热区最多 200 条，翻页直接走 MySQL，避免"重复第一页"。
		dbVideos, err := repository.ListFeedColdDB(ctx, followIDs, cursor, nil, need)
		if err != nil {
			return nil, apierror.Internal(err)
		}
		pool = dbVideos

	default:
		// 首页：热时间线 + 冷区补齐。
		var hotIDs []uint
		if hot, err := repository.FeedHot(ctx, me); err == nil && len(hot) > 0 {
			items := hot
			if len(items) > need {
				items = items[:need]
			}
			videos, err := s.loadByTimeline(ctx, items)
			if err != nil {
				return nil, apierror.Internal(err)
			}
			pool = videos
			for _, it := range items {
				hotIDs = append(hotIDs, it.VideoID)
			}
		}
		if len(pool) < need {
			var cursorTs int64
			if len(pool) > 0 {
				cursorTs = pool[len(pool)-1].CreatedTs
			}
			dbVideos, err := repository.ListFeedColdDB(ctx, followIDs, cursorTs, hotIDs, need-len(pool))
			if err != nil {
				return nil, apierror.Internal(err)
			}
			pool = mergeVideos(pool, dbVideos)
		}
	}

	// 热时间线可能残留"取关前的旧视频"，按当前关注集合过滤。
	if len(pool) > 0 {
		kept := pool[:0]
		for _, v := range pool {
			if _, ok := followSet[v.UserID]; ok {
				kept = append(kept, v)
			}
		}
		pool = kept
	}

	sortByTs(pool)
	page := newPage[models.Video](nil)
	if len(pool) > limit {
		pool = pool[:limit]
		page.NextCursor = pool[len(pool)-1].CreatedTs
	}
	page.List = pool

	fillVideoAuthors(ctx, page.List)
	fillLikedStatus(ctx, me, page.List)
	return page, nil
}

func (s *FeedService) loadByTimeline(ctx context.Context, items []repository.TimelineItem) ([]models.Video, error) {
	ids := make([]uint, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.VideoID)
	}
	videos, err := repository.GetVideosByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return orderByTimeline(videos, items), nil
}

func mergeVideos(a, b []models.Video) []models.Video {
	seen := make(map[uint]bool, len(a)+len(b))
	out := make([]models.Video, 0, len(a)+len(b))
	for _, v := range a {
		if !seen[v.ID] {
			seen[v.ID] = true
			out = append(out, v)
		}
	}
	for _, v := range b {
		if !seen[v.ID] {
			seen[v.ID] = true
			out = append(out, v)
		}
	}
	return out
}
