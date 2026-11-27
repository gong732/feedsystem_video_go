package service

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math/rand"
	"strings"
	"time"

	"video-feed/apierror"
	"video-feed/models"
	"video-feed/repository"

	"gorm.io/gorm"
)

// VideoService 视频发布、发现页、详情、热榜。
type VideoService struct{}

// CreateVideoInput 发布视频的入参（handler 绑定后传入）。
type CreateVideoInput struct {
	Title       string
	Description string
	VideoURL    string
	CoverURL    string
	Duration    int
}

// Create 发布视频：先写 MySQL（唯一真相源），再异步 fanout 到 Redis。
// fanout 失败不影响发布结果：发现页/关注流读时都有 MySQL 回源兜底。
func (s *VideoService) Create(ctx context.Context, uid uint, in CreateVideoInput) (*models.Video, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, apierror.BadRequest("标题不能为空")
	}
	if len([]rune(title)) > 128 {
		return nil, apierror.BadRequest("标题最长 128 个字符")
	}
	if strings.TrimSpace(in.VideoURL) == "" {
		return nil, apierror.BadRequest("视频地址不能为空")
	}
	if in.Duration < 0 {
		return nil, apierror.BadRequest("时长不合法")
	}

	v := models.Video{
		UserID:      uid,
		Title:       title,
		Description: strings.TrimSpace(in.Description),
		VideoURL:    strings.TrimSpace(in.VideoURL),
		CoverURL:    strings.TrimSpace(in.CoverURL),
		Duration:    in.Duration,
		Status:      1,
	}
	if err := repository.CreateVideo(ctx, &v); err != nil {
		return nil, apierror.Internal(err)
	}
	if v.CreatedTs == 0 {
		v.CreatedTs = time.Now().UnixMilli()
	}

	// 不能把请求 ctx 带进 goroutine：响应返回后它会被 cancel。
	go s.fanout(uid, v.ID, v.CreatedTs)
	return &v, nil
}

func (s *VideoService) fanout(authorID, videoID uint, ts int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	followers, err := repository.FollowerIDs(ctx, authorID)
	if err != nil {
		log.Printf("[fanout] 查粉丝失败 video=%d: %v", videoID, err)
		return
	}
	if err := repository.PushFeedHotBatch(ctx, followers, videoID, ts); err != nil {
		log.Printf("[fanout] 写关注流失败 video=%d: %v", videoID, err)
	}
	if err := repository.PushDiscover(ctx, videoID, ts); err != nil {
		log.Printf("[fanout] 写发现页失败 video=%d: %v", videoID, err)
	}
}

// Discover 发现页：Redis 时间线优先，空/故障时 singleflight 重建，再回源 MySQL。
func (s *VideoService) Discover(ctx context.Context, uid uint, cursor string, limit int) (*Page[models.Video], error) {
	cursorTs, cursorID := parseCursor(cursor)
	if limit <= 0 || limit > 50 {
		limit = 12
	}

	members, err := repository.DiscoverPage(ctx, cursorTs, cursorID, limit)
	if err != nil && cursorTs == 0 {
		// 时间线丢了就重建一次；singleflight 保证并发只重建一次。
		_, _, _ = discoverSF.Do("discover-rebuild", func() (interface{}, error) {
			rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if e := repository.RebuildDiscover(rctx); e != nil {
				log.Printf("[discover] 重建时间线失败: %v", e)
			}
			return nil, nil
		})
		members, err = repository.DiscoverPage(ctx, cursorTs, cursorID, limit)
	}

	if err == nil && len(members) > 0 {
		ids := make([]uint, 0, len(members))
		for _, it := range members {
			ids = append(ids, it.VideoID)
		}
		videos, qerr := repository.GetVideosByIDs(ctx, ids)
		if qerr != nil {
			return nil, apierror.Internal(qerr)
		}
		ordered := orderByTimeline(videos, members)
		if len(ordered) > 0 {
			fillVideoAuthors(ctx, ordered)
			fillLikedStatus(ctx, uid, ordered)

			page := newPage(ordered)
			if len(members) >= limit {
				last := members[len(members)-1]
				page.NextCursor = formatCursor(last.Ts, last.VideoID)
			}
			return page, nil
		}
	}

	// 冷数据 / Redis 不可用：MySQL keyset 回源。
	videos, err := repository.ListDiscoverDB(ctx, cursorTs, cursorID, limit)
	if err != nil {
		return nil, apierror.Internal(err)
	}
	fillVideoAuthors(ctx, videos)
	fillLikedStatus(ctx, uid, videos)

	page := newPage(videos)
	if len(videos) == limit {
		last := videos[len(videos)-1]
		page.NextCursor = formatCursor(last.CreatedTs, last.ID)
	}
	return page, nil
}

// Detail 视频详情：缓存 -> singleflight 查库 -> 空值缓存；播放量走 Redis 实时值 + MySQL 持久化。
func (s *VideoService) Detail(ctx context.Context, uid, id uint) (*models.Video, error) {
	infoKey := repository.VideoInfoKey(id)
	nullKey := repository.VideoNullKey(id)

	if raw, ok := repository.CacheGet(ctx, infoKey); ok {
		var v models.Video
		if json.Unmarshal([]byte(raw), &v) == nil {
			v.Views = s.bumpViews(ctx, id)
			v.Liked, _ = repository.HasLike(ctx, uid, id)
			return &v, nil
		}
	}
	if _, ok := repository.CacheGet(ctx, nullKey); ok {
		return nil, apierror.NotFound("视频不存在")
	}

	shared, err, _ := videoSF.Do(infoKey, func() (interface{}, error) {
		v, err := repository.GetVideo(ctx, id)
		if err != nil {
			return nil, err
		}
		author, err := repository.FindUserByID(ctx, v.UserID)
		if err != nil {
			return nil, err
		}
		v.Author = *author
		v.Views = 0 // 实时播放量单独读，缓存里不存

		if data, mErr := json.Marshal(v); mErr == nil {
			ttl := 300*time.Second + time.Duration(rand.Intn(60))*time.Second
			_ = repository.CacheSet(ctx, infoKey, data, ttl)
		}
		return v, nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = repository.CacheSet(ctx, nullKey, []byte("1"), 60*time.Second)
			return nil, apierror.NotFound("视频不存在")
		}
		return nil, apierror.Internal(err)
	}

	v, ok := shared.(*models.Video)
	if !ok {
		return nil, apierror.Internal(errors.New("视频详情类型断言失败"))
	}
	out := *v
	out.Views = s.bumpViews(ctx, id)
	out.Liked, _ = repository.HasLike(ctx, uid, id)
	return &out, nil
}

// bumpViews 播放一次：MySQL +1 持久化，Redis 计数器 +1 供实时展示，分钟窗口 +1 供热榜。
func (s *VideoService) bumpViews(ctx context.Context, id uint) int64 {
	mysqlViews, err := repository.IncrementViewsMySQL(ctx, id)
	if err != nil {
		log.Printf("[views] MySQL 播放量更新失败 video=%d: %v", id, err)
		return 0
	}

	// 首次访问用 MySQL 值初始化计数器（写 mysqlViews-1，再 INCR 得到 mysqlViews）。
	if _, ok, err := repository.GetViewCounter(ctx, id); err != nil || !ok {
		_ = repository.SeedViewCounter(ctx, id, mysqlViews-1)
	}
	if v, err := repository.IncrViewCounter(ctx, id); err == nil {
		mysqlViews = v
	}
	_ = repository.IncrHotWindow(ctx, id)
	return mysqlViews
}

// Hot 热榜：最近 60 个分钟窗口合并；为空或 Redis 故障时按 MySQL 总播放量兜底。
func (s *VideoService) Hot(ctx context.Context, uid uint, n int) ([]models.Video, error) {
	if n <= 0 || n > 50 {
		n = 10
	}

	ids, err := repository.HotVideoIDs(ctx, n)
	if err != nil || len(ids) == 0 {
		return s.hotFromDB(ctx, uid, n)
	}

	videos, err := repository.GetVideosByIDs(ctx, ids)
	if err != nil {
		return nil, apierror.Internal(err)
	}
	ordered := make([]models.Video, 0, len(ids))
	m := make(map[uint]models.Video, len(videos))
	for _, v := range videos {
		m[v.ID] = v
	}
	for _, id := range ids {
		if v, ok := m[id]; ok {
			ordered = append(ordered, v)
		}
	}
	if len(ordered) < n {
		// 分钟窗口只有最近 1 小时，重启后通常很稀疏：用 MySQL 总播放量补足榜单，
		// 保证首屏不是"只有 1 条"。
		dbVideos, derr := repository.ListHotDB(ctx, n)
		if derr == nil {
			seen := make(map[uint]bool, len(ordered))
			for _, v := range ordered {
				seen[v.ID] = true
			}
			for _, v := range dbVideos {
				if seen[v.ID] {
					continue
				}
				ordered = append(ordered, v)
				if len(ordered) >= n {
					break
				}
			}
		}
	}
	if len(ordered) == 0 {
		return s.hotFromDB(ctx, uid, n)
	}
	fillVideoAuthors(ctx, ordered)
	fillLikedStatus(ctx, uid, ordered)
	return ordered, nil
}

func (s *VideoService) hotFromDB(ctx context.Context, uid uint, n int) ([]models.Video, error) {
	videos, err := repository.ListHotDB(ctx, n)
	if err != nil {
		return nil, apierror.Internal(err)
	}
	fillVideoAuthors(ctx, videos)
	fillLikedStatus(ctx, uid, videos)
	return videos, nil
}
