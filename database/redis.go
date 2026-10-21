package database

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"video-feed/config"
	"video-feed/models"

	"github.com/redis/go-redis/v9"
)

// RDB 全局 Redis 客户端
var RDB *redis.Client

// hotVideosKey 热门视频榜 ZSet 的 key
const hotVideosKey = "hot_videos"

// feedHotKeyPrefix 关注流"热时间线" ZSet 的 key 前缀
// 每个用户一条：feed:hot:{user_id}，score=发布时间戳，member=video_id
const feedHotKeyPrefix = "feed:hot:"

// allVideosKey 发现页"全站时间线" ZSet 的 key
// 全站只有一条：score=created_ts（毫秒），member=video_id
// 与关注流的区别：没有 fanout（不存在写放大），Redis 直接就是发现页的时间线
const allVideosKey = "all_videos"

// InitRedis 连接 Redis
func InitRedis(cfg *config.RedisConfig) {
	RDB = redis.NewClient(&redis.Options{
		Addr: cfg.Host + ":" + cfg.Port,
	})
	ctx := context.Background()
	if _, err := RDB.Ping(ctx).Result(); err != nil {
		log.Fatalf("连接 Redis 失败: %v", err)
	}
	log.Println("✅ Redis 连接成功")
}

// InitHotRank 启动时把已有视频的播放量灌入热门榜（ZSet）
func InitHotRank() {
	var videos []models.Video
	DB.Model(&models.Video{}).Where("status = ?", 1).Find(&videos)
	if len(videos) == 0 {
		return
	}
	ctx := context.Background()
	for _, v := range videos {
		RDB.ZAdd(ctx, hotVideosKey, redis.Z{
			Score:  float64(v.Views),
			Member: strconv.FormatUint(uint64(v.ID), 10),
		})
	}
	log.Printf("✅ 热门榜初始化完成，共 %d 条视频", len(videos))
}

// IncrHotScore 视频播放 +1（同时更新 MySQL 与 Redis 热榜）
func IncrHotScore(videoID uint) {
	ctx := context.Background()
	RDB.ZIncrBy(ctx, hotVideosKey, 1, strconv.FormatUint(uint64(videoID), 10))
}

// GetHotVideos 取热门榜前 n 条视频 ID（按热度倒序）
func GetHotVideos(n int) []uint {
	ctx := context.Background()
	members, err := RDB.ZRevRange(ctx, hotVideosKey, 0, int64(n-1)).Result()
	if err != nil {
		log.Println("读取热门榜失败:", err)
		return nil
	}
	ids := make([]uint, 0, len(members))
	for _, m := range members {
		id, _ := strconv.ParseUint(m, 10, 64)
		ids = append(ids, uint(id))
	}
	return ids
}

// GetHotScore 取视频实时播放量（热榜 ZSet 的 score）
// 播放量是"高频写"，不适合放详情缓存（每次访问都自增，缓存会写放大）。
// 所以详情接口返回时，播放量实时从 Redis ZSet 取，只此一次 O(log N)。
func GetHotScore(videoID uint) int64 {
	ctx := context.Background()
	v, err := RDB.ZScore(ctx, hotVideosKey, strconv.FormatUint(uint64(videoID), 10)).Result()
	if err != nil {
		return 0 // 不在热榜（如刚发布还没人看）按 0 处理
	}
	return int64(v)
}

// CacheSet 通用缓存写入（第二阶段 LLM 结果缓存会复用）
func CacheSet(key string, val interface{}, ttl time.Duration) error {
	return RDB.Set(context.Background(), key, val, ttl).Err()
}

// CacheGet 通用缓存读取
func CacheGet(key string) (string, bool) {
	v, err := RDB.Get(context.Background(), key).Result()
	if err != nil {
		return "", false
	}
	return v, true
}

// DeleteCache 删除缓存 key
func DeleteCache(key string) {
	RDB.Del(context.Background(), key)
}

// FeedHotKey 某用户热时间线的 ZSet key
func FeedHotKey(uid uint) string {
	return fmt.Sprintf("%s%d", feedHotKeyPrefix, uid)
}

// PushFeedHot 把新发布的视频推送到粉丝的"热时间线"（写扩散 / fanout）
// 容量裁剪：ZSet 只保留最新 200 条，更老的自动淘汰（走 MySQL 冷数据）
func PushFeedHot(uid, videoID uint, ts int64) {
	ctx := context.Background()
	key := FeedHotKey(uid)
	pipe := RDB.TxPipeline() // 管道：一次网络往返执行多条命令
	pipe.ZAdd(ctx, key, redis.Z{
		Score:  float64(ts), // score = 发布时间戳，天然按时间排序
		Member: strconv.FormatUint(uint64(videoID), 10),
	})
	pipe.ZRemRangeByRank(ctx, key, 0, -201) // 只留最新 200 条
	pipe.Exec(ctx)
}

// BackfillFeedHot 关注成功后的"历史补写"：把被关注者最近的视频批量写进新粉丝的热时间线。
// 推模式只推"发布那一刻已存在的粉丝"——新粉丝看不到被关注者的历史视频，
// 所以关注成功时补一次（backfill），避免新粉丝的关注流一片空白 / 历史内容缺失。
func BackfillFeedHot(uid uint, items []redis.Z) {
	if len(items) == 0 {
		return
	}
	ctx := context.Background()
	key := FeedHotKey(uid)
	pipe := RDB.TxPipeline()
	pipe.ZAdd(ctx, key, items...)
	pipe.ZRemRangeByRank(ctx, key, 0, -201) // 同样只留最新 200 条
	pipe.Exec(ctx)
}

// GetFeedHot 取某用户热时间线的全部视频（已按时间倒序），返回 id + 发布时间戳
func GetFeedHot(uid uint) []redis.Z {
	ctx := context.Background()
	members, err := RDB.ZRevRangeWithScores(ctx, FeedHotKey(uid), 0, -1).Result()
	if err != nil {
		log.Println("读取热时间线失败:", err)
		return nil
	}
	return members
}

// InitDiscoverTimeline 启动时把已有视频灌入"全站时间线"（发现页用）
// 幂等：重复执行只是覆盖 score，不会产生脏数据
func InitDiscoverTimeline() {
	var videos []models.Video
	DB.Model(&models.Video{}).Where("status = ?", 1).Find(&videos)
	if len(videos) == 0 {
		return
	}
	ctx := context.Background()
	items := make([]redis.Z, 0, len(videos))
	for _, v := range videos {
		items = append(items, redis.Z{
			Score:  float64(v.CreatedTs), // 绝对时间戳（毫秒），与时区无关
			Member: strconv.FormatUint(uint64(v.ID), 10),
		})
	}
	RDB.ZAdd(ctx, allVideosKey, items...)
	log.Printf("✅ 发现页时间线初始化完成，共 %d 条视频", len(videos))
}

// PushAllVideos 发布视频时写入全站时间线（发现页用）
func PushAllVideos(videoID uint, ts int64) {
	ctx := context.Background()
	RDB.ZAdd(ctx, allVideosKey, redis.Z{
		Score:  float64(ts),
		Member: strconv.FormatUint(uint64(videoID), 10),
	})
}

// GetDiscoverVideos 发现页翻页：Redis 侧游标取一页 id 列表
//   - cursor=0  → 取最新 limit 条（ZREVRANGE）
//   - cursor>0  → 取 score 严格小于 cursor 的 limit 条（ZREVRANGEBYSCORE，( 开区间）
//
// 返回 (id + score 列表)。调用方回表补详情后，按 score 倒序重排即可。
// next_cursor 的计算交给调用方：取满一整页 → 还有更多；没取满 → 到底。
func GetDiscoverVideos(cursor int64, limit int) []redis.Z {
	ctx := context.Background()
	var (
		members []redis.Z
		err     error
	)
	if cursor > 0 {
		members, err = RDB.ZRevRangeByScoreWithScores(ctx, allVideosKey, &redis.ZRangeBy{
			Max:    "(" + strconv.FormatInt(cursor, 10), // ( 前缀 = 开区间，不含 cursor 本身
			Min:    "-inf",
			Offset: 0,
			Count:  int64(limit),
		}).Result()
	} else {
		members, err = RDB.ZRevRangeWithScores(ctx, allVideosKey, 0, int64(limit-1)).Result()
	}
	if err != nil {
		log.Println("读取发现页时间线失败:", err)
		return nil
	}
	return members
}
