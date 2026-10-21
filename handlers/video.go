package handlers

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"video-feed/database"
	"video-feed/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// CreateVideoReq 发布视频请求体（骨架阶段先收 URL，第二阶段加文件上传）
type CreateVideoReq struct {
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
	VideoURL    string `json:"video_url" binding:"required"`
	CoverURL    string `json:"cover_url"`
	Duration    int    `json:"duration"`
}

// CreateVideo 发布视频：user_id 由 Auth 中间件塞入
func CreateVideo(c *gin.Context) {
	userID := c.GetUint("user_id")
	var req CreateVideoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误"})
		return
	}
	video := models.Video{
		UserID:      userID,
		Title:       req.Title,
		Description: req.Description,
		VideoURL:    req.VideoURL,
		CoverURL:    req.CoverURL,
		Duration:    req.Duration,
	}
	if err := database.DB.Create(&video).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "发布失败"})
		return
	}

	// 写扩散（fanout）：把新视频推送到所有粉丝的"热时间线"ZSet
	// 这样粉丝刷关注流时，最新视频直接命中 Redis，不用实时查 MySQL
	var followers []models.Follow
	database.DB.Where("follow_user_id = ?", video.UserID).Find(&followers)
	ts := video.CreatedTs // 毫秒级绝对时间戳（与时区无关）
	for _, f := range followers {
		database.PushFeedHot(f.UserID, video.ID, ts)
	}

	// 发现页：写入全站时间线（一条 ZSet，无 fanout）
	database.PushAllVideos(video.ID, ts)

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "发布成功", "data": video})
}

// GetVideoList 发现页（全站时间线）：Redis ZSet 游标翻页 + MySQL 回表补详情
// 替代原 OFFSET 页码分页的原因：
//   1. OFFSET 越大越慢（要扫掉前 N 行）；游标走索引 O(log N)
//   2. 新视频插入导致页码分页"页间重复/漏掉"；游标以绝对时间为锚，稳定不重不漏
func GetVideoList(c *gin.Context) {
	cursor, _ := strconv.ParseInt(c.DefaultQuery("cursor", "0"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "6"))
	if limit < 1 || limit > 50 {
		limit = 6
	}

	// 1. Redis 侧游标取一页 id（score = created_ts 毫秒，天然按时间倒序）
	members := database.GetDiscoverVideos(cursor, limit)
	if len(members) == 0 {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"list": []models.Video{}, "next_cursor": int64(0)}})
		return
	}

	// 2. 回表补详情（MySQL IN 不保证顺序，用 tsMap 重排）
	ids := make([]uint, 0, len(members))
	tsMap := make(map[uint]int64, len(members))
	for _, m := range members {
		id, _ := strconv.ParseUint(m.Member.(string), 10, 64)
		ids = append(ids, uint(id))
		tsMap[uint(id)] = int64(m.Score)
	}
	var videos []models.Video
	database.DB.Where("id IN ? AND status = ?", ids, 1).Find(&videos)
	sortByTs(videos, tsMap) // 复用 feed.go 的排序：按 ts 倒序 + 同毫秒 ID 兜底

	// 3. next_cursor：取满整页说明可能还有更多 → 下一页从最后一条 ts 继续
	nextCursor := int64(0)
	if len(members) >= limit {
		nextCursor = int64(members[len(members)-1].Score)
	}

	fillVideoAuthors(&videos)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"list":       videos,
		"next_cursor": nextCursor,
	}})
}

// GetVideoDetail 视频详情 —— 缓存三件套落地样板：
//   ① 读缓存：video:info:{id}（JSON，不含播放量）
//   ② 防击穿：缓存过期瞬间并发 → singleflight 只放一个查 DB，回填缓存
//   ③ 防雪崩：回填 TTL = 300s + 随机 0~60s，避免大批 key 同时过期
//   ④ 防穿透：DB 也不存在 → 缓存空值 video:null:{id}（短 TTL），攻击者循环查不存在的 id 也打不到 DB
// 播放量是高频写（每次访问自增），不进缓存：返回时实时从热榜 ZSet 取（GetHotScore）
func GetVideoDetail(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "参数错误"})
		return
	}
	uid := uint(id)
	infoKey := fmt.Sprintf("video:info:%d", id)
	nullKey := fmt.Sprintf("video:null:%d", id)

	// ① 读缓存（命中：Redis 一次读 + 播放量自增，完全不碰详情查询）
	if cached, ok := database.CacheGet(infoKey); ok {
		var video models.Video
		if err := json.Unmarshal([]byte(cached), &video); err == nil {
			// 播放量实时取 + 自增（写操作不进缓存）
			video.Views = database.GetHotScore(uid)
			database.DB.Model(&video).UpdateColumn("views", gorm.Expr("views + 1"))
			database.IncrHotScore(uid)
			video.Views++
			c.JSON(http.StatusOK, gin.H{"code": 0, "data": video})
			return
		}
	}

	// ④ 防穿透：先查空值标记（DB 里确定不存在），直接 404，不碰 DB
	if _, ok := database.CacheGet(nullKey); ok {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "视频不存在"})
		return
	}

	// ② 防击穿：singleflight 合并并发，只放一个查 DB，其余等待共享结果
	shared, err, _ := videoSF.Do(fmt.Sprintf("v:%d", id), func() (interface{}, error) {
		var video models.Video
		if err := database.DB.First(&video, id).Error; err != nil {
			return nil, err
		}
		// 回填缓存：补作者后整体序列化（命中路径不再查 author）
		var author models.User
		database.DB.Select("id, username, nickname, avatar").First(&author, video.UserID)
		video.Author = author
		video.Views = 0 // 播放量不进缓存，命中时实时覆盖
		data, _ := json.Marshal(video)
		// ③ 防雪崩：TTL 加随机抖动，避免所有 key 同一秒失效
		ttl := 300*time.Second + time.Duration(rand.Intn(60))*time.Second
		database.CacheSet(infoKey, data, ttl)
		return &video, nil
	})
	if err != nil {
		// DB 查不到 → 缓存空值（短 TTL），下次直接 404
		database.CacheSet(nullKey, "1", 60*time.Second)
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "msg": "视频不存在"})
		return
	}
	// 值拷贝：singleflight 返回共享指针，不能互相改 Views
	video := *shared.(*models.Video)

	// 播放量：实时从热榜取 + 自增
	video.Views = database.GetHotScore(uid)
	database.DB.Model(&video).UpdateColumn("views", gorm.Expr("views + 1"))
	database.IncrHotScore(uid)
	video.Views++
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": video})
}

// fillVideoAuthors 批量补作者（避免 N+1 查询，和博客 fillArticleRelations 同思路）
func fillVideoAuthors(videos *[]models.Video) {
	if len(*videos) == 0 {
		return
	}
	ids := make([]uint, 0, len(*videos))
	for i := range *videos {
		ids = append(ids, (*videos)[i].UserID)
	}
	var users []models.User
	database.DB.Select("id, username, nickname, avatar").Where("id IN ?", ids).Find(&users)
	userMap := make(map[uint]models.User, len(users))
	for _, u := range users {
		userMap[u.ID] = u
	}
	for i := range *videos {
		(*videos)[i].Author = userMap[(*videos)[i].UserID]
	}
}
