package handlers

import (
	"net/http"
	"strconv"

	"video-feed/database"
	"video-feed/models"

	"github.com/gin-gonic/gin"
)

// GetFeed 关注时间线 —— Feed 流核心：游标翻旧 + 增量拉新（B站式）
//
// 三种模式（由参数决定，互斥）：
//   1. since > 0  → 拉新：只取比 since 更新的视频（前端刷新时插到列表顶部）
//   2. cursor > 0 → 翻旧：只取比 cursor 更老的视频（下滑加载更多）
//   3. 都不传      → 首次：从头拉最新一批
//
// 第二阶段升级为"推拉结合 + 冷热分离"：
//   热数据：发布时写扩散到粉丝的 Redis ZSet（feed:hot:{uid}），最新 200 条
//   冷数据：翻过热区后回源 MySQL（拉模式）
//   读取：先查热区 ZSet（快，不打 MySQL），不足再回源冷区（singleflight 防击穿）
func GetFeed(c *gin.Context) {
	me := c.GetUint("user_id")

	// 游标：上次看到的最新位置（前端传过来的时间戳），不传则从头看
	cursor := int64(0)
	if cs := c.Query("cursor"); cs != "" {
		cursor, _ = strconv.ParseInt(cs, 10, 64)
	}
	// 拉新锚：当前列表顶部第一条的时间戳，只取比它更新的
	since := int64(0)
	if ss := c.Query("since"); ss != "" {
		since, _ = strconv.ParseInt(ss, 10, 64)
	}
	limit := 10
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 50 {
			limit = v
		}
	}

	// 1. 先查出我关注了哪些人
	var follows []models.Follow
	database.DB.Where("user_id = ?", me).Find(&follows)
	if len(follows) == 0 {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"list": []models.Video{}, "next_cursor": 0}})
		return
	}
	followIDs := make([]uint, 0, len(follows))
	for _, f := range follows {
		followIDs = append(followIDs, f.FollowUserID)
	}

	// 2. 拉新模式：刷新时增量插顶（B站式）
	if since > 0 {
		videos := fetchNewVideos(me, followIDs, since, int64(limit))
		fillVideoAuthors(&videos)
		nextCursor := int64(0)
		if len(videos) > 0 {
			nextCursor = videos[len(videos)-1].CreatedAt.Unix()
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"list": videos, "next_cursor": nextCursor}})
		return
	}

	// 3. 翻旧 / 首次模式：热区优先，冷区兜底
	hotMembers := database.GetFeedHot(me) // []redis.Z，已按 score(发布时间戳·毫秒) 倒序
	hotVideos := make([]models.Video, 0, limit)
	excludedIDs := make([]uint, 0) // 已从热区取到的 id，冷区回源时排除

	if len(hotMembers) > 0 {
		// 3.1 游标过滤：跳过比 cursor 更新的（翻页时不应重复出现）
		type hotItem struct {
			id uint
			ts int64
		}
		candidates := make([]hotItem, 0, len(hotMembers))
		for _, m := range hotMembers {
			id, _ := strconv.ParseUint(m.Member.(string), 10, 64)
			ts := int64(m.Score) // 毫秒
			if cursor > 0 && ts >= cursor {
				continue // 游标之后的新视频，跳过
			}
			candidates = append(candidates, hotItem{id: uint(id), ts: ts})
		}

		// 3.2 回表查热区视频（一次 IN 查询），按 score 倒序
		if len(candidates) > 0 {
			candIDs := make([]uint, 0, len(candidates))
			tsMap := make(map[uint]int64, len(candidates))
			for _, it := range candidates {
				candIDs = append(candIDs, it.id)
				tsMap[it.id] = it.ts
			}
			var hotVideosRaw []models.Video
			database.DB.Where("id IN ? AND status = ?", candIDs, 1).Find(&hotVideosRaw)

			// 过滤取关残留（取关后热区可能还留着旧作者视频）+ 按时间倒序
			hotOrdered := make([]models.Video, 0, len(hotVideosRaw))
			for _, v := range hotVideosRaw {
				if !containsUint(followIDs, v.UserID) {
					continue
				}
				hotOrdered = append(hotOrdered, v)
			}
			// 按 ts 倒序（热区 ZSet 本身有序，回表后按 tsMap 重排保持顺序）
			sortByTs(hotOrdered, tsMap)

			// 取前 limit 条进结果
			if len(hotOrdered) > limit {
				hotOrdered = hotOrdered[:limit]
			}
			hotVideos = hotOrdered
			for _, v := range hotVideos {
				excludedIDs = append(excludedIDs, v.ID)
			}
		}
	}

	// 4. 冷区：热区不足 limit 时，回源 MySQL 补更老的数据
	if len(hotVideos) < limit {
		need := limit - len(hotVideos)
		var coldVideos []models.Video

		// 冷区查询条件：我关注的人 + 未下架 + 排除已从热区取的 + 比热区最老那条更早
		query := database.DB.Where("user_id IN ? AND status = ?", followIDs, 1)
		if len(excludedIDs) > 0 {
			query = query.Where("id NOT IN ?", excludedIDs)
		}
		// 边界时间：取"有效热区结果"(hotVideos) 最老那条的 created_ts，而不是全部 hotMembers。
		// hotMembers 可能含已删视频/取关残留（回表查不到），用它当边界会把比它新的
		// 冷区数据（如新关注者的历史视频）错误地排除，导致首次列表缺失、刷新时"回捞"成重复。
		coldCursor := cursor
		if len(hotVideos) > 0 {
			oldest := hotVideos[len(hotVideos)-1].CreatedTs // hotVideos 已按 ts 倒序，最后一条最老
			if coldCursor == 0 || oldest < coldCursor {
				coldCursor = oldest
			}
		}
		if coldCursor > 0 {
			query = query.Where("created_ts < ?", coldCursor)
		}

		// singleflight 合并冷区回源：同一时刻大量翻页请求，只查一次
		key := "feed:" + strconv.FormatUint(uint64(me), 10) + ":" + strconv.FormatInt(coldCursor, 10) + ":" + strconv.Itoa(need)
		v, _, _ := feedSF.Do(key, func() (interface{}, error) {
			var list []models.Video
			query.Order("created_ts DESC").Limit(need).Find(&list)
			return list, nil
		})
		coldVideos = v.([]models.Video)

		hotVideos = append(hotVideos, coldVideos...)
	}

	// 5. 计算下一次的游标 = 本次最后一条的 created_ts
	nextCursor := int64(0)
	if len(hotVideos) > 0 {
		nextCursor = hotVideos[len(hotVideos)-1].CreatedTs
	}

	fillVideoAuthors(&hotVideos)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"list":        hotVideos,
		"next_cursor": nextCursor, // 前端下次请求把它当 cursor 传回来
	}})
}

// fetchNewVideos 拉新：热区（Redis ts>since）+ 冷区（MySQL created_ts>since）合并，
// 按时间倒序（最新在前），最多 limit 条。用于前端"刷新插顶"。
func fetchNewVideos(me uint, followIDs []uint, since, limit int64) []models.Video {
	tsMap := make(map[uint]int64)
	result := make([]models.Video, 0, limit)
	excluded := make([]uint, 0)

	// 热区：只留比 since 新的（Redis 全取 ≤200 条后过滤，量小够用）
	hotMembers := database.GetFeedHot(me)
	candIDs := make([]uint, 0, len(hotMembers))
	for _, m := range hotMembers {
		id, _ := strconv.ParseUint(m.Member.(string), 10, 64)
		ts := int64(m.Score) // 毫秒
		if ts <= since {
			continue // 不比 since 新的，跳过
		}
		candIDs = append(candIDs, uint(id))
		tsMap[uint(id)] = ts
	}
	if len(candIDs) > 0 {
		var raws []models.Video
		database.DB.Where("id IN ? AND status = ?", candIDs, 1).Find(&raws)
		for _, v := range raws {
			if containsUint(followIDs, v.UserID) {
				result = append(result, v)
				excluded = append(excluded, v.ID)
			}
		}
		sortByTs(result, tsMap)
	}

	// 冷区：MySQL 里比 since 新的（排除热区已取，防重复；created_ts 毫秒整数比较，无时区歧义）
	if int64(len(result)) < limit {
		need := limit - int64(len(result))
		query := database.DB.Where("user_id IN ? AND status = ? AND created_ts > ?",
			followIDs, 1, since)
		if len(excluded) > 0 {
			query = query.Where("id NOT IN ?", excluded)
		}
		var cold []models.Video
		query.Order("created_ts DESC").Limit(int(need)).Find(&cold)
		for _, v := range cold {
			tsMap[v.ID] = v.CreatedTs
		}
		result = append(result, cold...)
		sortByTs(result, tsMap)
	}
	if int64(len(result)) > limit {
		result = result[:limit]
	}
	return result
}

// sortByTs 按 tsMap 的时间戳倒序（简单插入排序，数据量 ≤200，够用）
// 同分（同一毫秒发布）时按 ID 倒序兜底：ID 大 = 发布稍晚，保证顺序稳定不抖动
func sortByTs(videos []models.Video, tsMap map[uint]int64) {
	for i := 1; i < len(videos); i++ {
		for j := i; j > 0; j-- {
			a, b := tsMap[videos[j].ID], tsMap[videos[j-1].ID]
			if a > b || (a == b && videos[j].ID > videos[j-1].ID) {
				videos[j], videos[j-1] = videos[j-1], videos[j]
			} else {
				break
			}
		}
	}
}

// containsUint 判断切片是否包含某元素（关注列表过滤用）
func containsUint(list []uint, v uint) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
