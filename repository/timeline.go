package repository

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"video-feed/database"

	"github.com/redis/go-redis/v9"
)

const (
	allVideosKey      = "all_videos"
	feedHotKeyPrefix  = "feed:hot:"
	hotWindowPrefix   = "hot:video:1m:"
	hotMergePrefix    = "hot:video:merge:1m:"
	viewKeyPrefix     = "video:views:"
	uploadQuotaPrefix = "upload:quota:"
)

func feedHotKey(uid uint) string {
	return fmt.Sprintf("%s%d", feedHotKeyPrefix, uid)
}

func viewKey(id uint) string {
	return fmt.Sprintf("%s%d", viewKeyPrefix, id)
}

// DiscoverPage 发现页时间线。
func DiscoverPage(ctx context.Context, cursorTs int64, cursorID uint, limit int) ([]TimelineItem, error) {
	if !RedisReady() {
		return nil, ErrCacheDisabled
	}

	// 第一页：直接取最新 limit 条
	if cursorTs == 0 {
		zs, err := database.RDB.ZRevRangeWithScores(ctx, allVideosKey, 0,
			int64(limit-1)).Result()
		if err != nil {
			return nil, NoteRedisError(err)
		}
		return redisZToItems(zs), nil
	}

	items := make([]TimelineItem, 0, limit)

	// ① 同毫秒（score == cursorTs）且 id < cursorID 的条目，按 id 倒序排在前面
	same, err := database.RDB.ZRangeByScoreWithScores(ctx, allVideosKey,
		&redis.ZRangeBy{
			Min: strconv.FormatInt(cursorTs, 10),
			Max: strconv.FormatInt(cursorTs, 10),
		}).Result()
	if err != nil {
		return nil, NoteRedisError(err)
	}
	sameItems := redisZToItems(same)
	kept := make([]TimelineItem, 0, len(sameItems))
	for _, it := range sameItems {
		if it.VideoID < cursorID {
			kept = append(kept, it)
		}
	}
	sort.Slice(kept, func(i, j int) bool {
		return kept[i].VideoID >
			kept[j].VideoID
	})
	items = append(items, kept...)

	// ② 严格更老（score < cursorTs）的条目，补足剩余
	if len(items) < limit {
		older, err := database.RDB.ZRevRangeByScoreWithScores(ctx,
			allVideosKey, &redis.ZRangeBy{
				Max:    "(" + strconv.FormatInt(cursorTs, 10),
				Min:    "-inf",
				Offset: 0,
				Count:  int64(limit - len(items)),
			}).Result()
		if err != nil {
			return nil, NoteRedisError(err)
		}
		items = append(items, redisZToItems(older)...)
	}

	return items, nil
}

// RebuildDiscover 用 MySQL 重新构建全站时间线。
// DEL + ZADD 放在同一个 MULTI 里，重建期间不会出现"半个时间线"。
func RebuildDiscover(ctx context.Context) error {
	if !RedisReady() {
		return ErrCacheDisabled
	}
	videos, err := ListDiscoverDB(ctx, 0, 0, 1000)
	if err != nil {
		return err
	}

	pipe := database.RDB.TxPipeline()
	pipe.Del(ctx, allVideosKey)
	for _, v := range videos {
		pipe.ZAdd(ctx, allVideosKey, redis.Z{
			Score:  float64(v.CreatedTs),
			Member: strconv.FormatUint(uint64(v.ID), 10),
		})
	}
	_, err = pipe.Exec(ctx)
	return NoteRedisError(err)
}

// PushDiscover 发布视频时写入全站时间线，并裁剪到最新 10000 条。
func PushDiscover(ctx context.Context, videoID uint, ts int64) error {
	if !RedisReady() {
		return ErrCacheDisabled
	}
	pipe := database.RDB.TxPipeline()
	pipe.ZAdd(ctx, allVideosKey, redis.Z{
		Score: float64(ts), Member: strconv.FormatUint(uint64(videoID), 10),
	})
	pipe.ZRemRangeByRank(ctx, allVideosKey, 0, -10001)
	_, err := pipe.Exec(ctx)
	return NoteRedisError(err)
}

// FeedHot 取某用户热时间线（时间倒序）。
func FeedHot(ctx context.Context, uid uint) ([]TimelineItem, error) {
	if !RedisReady() {
		return nil, ErrCacheDisabled
	}
	zs, err := database.RDB.ZRevRangeWithScores(ctx, feedHotKey(uid), 0, -1).Result()
	if err != nil {
		return nil, NoteRedisError(err)
	}
	return redisZToItems(zs), nil
}

// PushFeedHotBatch 发布 fanout：一次 pipeline 写所有粉丝，避免 N 次往返。
func PushFeedHotBatch(ctx context.Context, followerIDs []uint, videoID uint, ts int64) error {
	if !RedisReady() {
		return ErrCacheDisabled
	}
	if len(followerIDs) == 0 {
		return nil
	}
	pipe := database.RDB.Pipeline()
	for _, uid := range followerIDs {
		key := feedHotKey(uid)
		pipe.ZAdd(ctx, key, redis.Z{
			Score: float64(ts), Member: strconv.FormatUint(uint64(videoID), 10),
		})
		pipe.ZRemRangeByRank(ctx, key, 0, -201) // 每个用户只留最新 200 条
	}
	_, err := pipe.Exec(ctx)
	return NoteRedisError(err)
}

// BackfillFeedHot 关注成功时把对方最近视频补写进新粉丝的热时间线。
func BackfillFeedHot(ctx context.Context, uid uint, items []TimelineItem) error {
	if !RedisReady() {
		return ErrCacheDisabled
	}
	if len(items) == 0 {
		return nil
	}
	pipe := database.RDB.TxPipeline()
	for _, it := range items {
		pipe.ZAdd(ctx, feedHotKey(uid), redis.Z{
			Score: float64(it.Ts), Member: strconv.FormatUint(uint64(it.VideoID), 10),
		})
	}
	pipe.ZRemRangeByRank(ctx, feedHotKey(uid), 0, -201)
	_, err := pipe.Exec(ctx)
	return NoteRedisError(err)
}

// IncrHotWindow 播放 +1 记到"当前分钟"的窗口 ZSet，2 小时后自动过期。
// 用 UTC 分钟，避免服务器时区变化导致窗口 key 错位。
func IncrHotWindow(ctx context.Context, videoID uint) error {
	if !RedisReady() {
		return ErrCacheDisabled
	}
	minute := time.Now().UTC().Format("200601021504")
	key := hotWindowPrefix + minute
	pipe := database.RDB.TxPipeline()
	pipe.ZIncrBy(ctx, key, 1, strconv.FormatUint(uint64(videoID), 10))
	pipe.Expire(ctx, key, 2*time.Hour)
	_, err := pipe.Exec(ctx)
	return NoteRedisError(err)
}

// HotVideoIDs 合并最近 60 个分钟窗口，返回热度倒序的视频 id。
// 结果写入带 TTL 的快照 key，翻页期间榜单稳定。
func HotVideoIDs(ctx context.Context, n int) ([]uint, error) {
	if !RedisReady() {
		return nil, ErrCacheDisabled
	}

	now := time.Now().UTC()
	keys := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		keys = append(keys, hotWindowPrefix+now.Add(-time.Duration(i)*time.Minute).Format("200601021504"))
	}
	dest := hotMergePrefix + now.Format("200601021504")

	pipe := database.RDB.TxPipeline()
	pipe.ZUnionStore(ctx, dest, &redis.ZStore{Keys: keys, Aggregate: "SUM"})
	pipe.Expire(ctx, dest, 90*time.Second)
	cmd := pipe.ZRevRange(ctx, dest, 0, int64(n-1))
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, NoteRedisError(err)
	}

	members, err := cmd.Result()
	if err != nil {
		return nil, NoteRedisError(err)
	}
	ids := make([]uint, 0, len(members))
	for _, m := range members {
		id, err := strconv.ParseUint(m, 10, 64)
		if err != nil || id == 0 {
			continue
		}
		ids = append(ids, uint(id))
	}
	return ids, nil
}

// IncrViewCounter 实时播放量 +1，返回新值。
func IncrViewCounter(ctx context.Context, videoID uint) (int64, error) {
	if !RedisReady() {
		return 0, ErrCacheDisabled
	}
	v, err := database.RDB.Incr(ctx, viewKey(videoID)).Result()
	return v, NoteRedisError(err)
}

// GetViewCounter 读实时播放量。
func GetViewCounter(ctx context.Context, videoID uint) (int64, bool, error) {
	if !RedisReady() {
		return 0, false, ErrCacheDisabled
	}
	v, err := database.RDB.Get(ctx, viewKey(videoID)).Int64()
	if err == redis.Nil {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, NoteRedisError(err)
	}
	return v, true, nil
}

// SeedViewCounter 首次访问时用 MySQL 值初始化实时计数器（不覆盖已有值）。
func SeedViewCounter(ctx context.Context, videoID uint, views int64) error {
	if !RedisReady() {
		return ErrCacheDisabled
	}
	return NoteRedisError(database.RDB.SetNX(ctx, viewKey(videoID), views, 0).Err())
}

// UploadQuotaUsed 当天已上传字节数。
func UploadQuotaUsed(ctx context.Context, uid uint, day string) (int64, error) {
	if !RedisReady() {
		return 0, ErrCacheDisabled
	}
	v, err := database.RDB.Get(ctx, uploadQuotaKey(uid, day)).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return v, NoteRedisError(err)
}

// AddUploadQuota 上传成功后累加当天用量。
func AddUploadQuota(ctx context.Context, uid uint, day string, add int64) error {
	if !RedisReady() {
		return ErrCacheDisabled
	}
	key := uploadQuotaKey(uid, day)
	pipe := database.RDB.TxPipeline()
	pipe.IncrBy(ctx, key, add)
	pipe.Expire(ctx, key, 48*time.Hour)
	_, err := pipe.Exec(ctx)
	return NoteRedisError(err)
}

func uploadQuotaKey(uid uint, day string) string {
	return fmt.Sprintf("%s%d:%s", uploadQuotaPrefix, uid, day)
}
