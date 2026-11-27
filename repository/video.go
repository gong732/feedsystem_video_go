package repository

import (
	"context"

	"video-feed/database"
	"video-feed/models"

	"gorm.io/gorm"
)

// CreateVideo 插入视频行。CreatedTs 由 GORM 的 autoCreateTime:milli 自动写入。
func CreateVideo(ctx context.Context, v *models.Video) error {
	return database.DB.WithContext(ctx).Create(v).Error
}

// GetVideo 取在架视频；不存在返回 gorm.ErrRecordNotFound。
func GetVideo(ctx context.Context, id uint) (*models.Video, error) {
	var v models.Video
	if err := database.DB.WithContext(ctx).
		Where("id = ? AND status = ?", id, 1).First(&v).Error; err != nil {
		return nil, err
	}
	return &v, nil
}

// GetVideosByIDs 批量取在架视频；IN 查询不保证顺序，由调用方重排。
func GetVideosByIDs(ctx context.Context, ids []uint) ([]models.Video, error) {
	if len(ids) == 0 {
		return []models.Video{}, nil
	}
	var videos []models.Video
	err := database.DB.WithContext(ctx).
		Where("id IN ? AND status = ?", ids, 1).Find(&videos).Error
	return videos, err
}

// VideoExists 判断视频是否存在且在架。
func VideoExists(ctx context.Context, id uint) (bool, error) {
	var n int64
	err := database.DB.WithContext(ctx).Model(&models.Video{}).
		Where("id = ? AND status = ?", id, 1).Count(&n).Error
	return n > 0, err
}

// ListDiscoverDB 发现页冷数据/Redis 不可用时的回源查询。
// 游标使用 created_ts（毫秒），按时间倒序 keyset 分页。
func ListDiscoverDB(ctx context.Context, cursorTs int64, cursorID uint, limit int) ([]models.Video, error) {
	q := database.DB.WithContext(ctx).Where("status = ?", 1)
	if cursorTs > 0 {
		// 元组比较：比 (cursorTs, cursorID) 更老，才进入下一页
		q = q.Where("(created_ts < ?) OR (created_ts = ? AND id < ?)",
			cursorTs, cursorTs, cursorID)
	}
	var videos []models.Video
	err := q.Order("created_ts DESC, id DESC").Limit(limit).Find(&videos).Error
	return videos, err
}

// ListNewDB 关注流"拉新"冷区：比 since 更新的视频。
func ListNewDB(ctx context.Context, followIDs []uint, since int64, limit int) ([]models.Video, error) {
	if len(followIDs) == 0 {
		return []models.Video{}, nil
	}
	var videos []models.Video
	err := database.DB.WithContext(ctx).
		Where("user_id IN ? AND status = ? AND created_ts > ?", followIDs, 1, since).
		Order("created_ts DESC, id DESC").Limit(limit).Find(&videos).Error
	return videos, err
}

// ListFeedColdDB 关注流"翻旧"冷区：排除热区已取到的 id，取更老的数据。
func ListFeedColdDB(ctx context.Context, followIDs []uint, cursorTs int64, cursorID uint, exclude []uint, limit int) ([]models.Video, error) {
	if len(followIDs) == 0 || limit <= 0 {
		return []models.Video{}, nil
	}
	q := database.DB.WithContext(ctx).
		Where("user_id IN ? AND status = ?", followIDs, 1)
	if len(exclude) > 0 {
		q = q.Where("id NOT IN ?", exclude)
	}
	if cursorTs > 0 {
		q = q.Where("(created_ts < ?) OR (created_ts = ? AND id < ?)",
			cursorTs, cursorTs, cursorID)
	}
	var videos []models.Video
	err := q.Order("created_ts DESC, id DESC").Limit(limit).Find(&videos).Error
	return videos, err
}

// ListHotDB 热榜兜底：Redis 不可用或窗口为空时按总播放量排序。
func ListHotDB(ctx context.Context, limit int) ([]models.Video, error) {
	var videos []models.Video
	err := database.DB.WithContext(ctx).
		Where("status = ?", 1).
		Order("views DESC, created_ts DESC, id DESC").
		Limit(limit).Find(&videos).Error
	return videos, err
}

// RecentVideosByAuthor 作者最近的视频（关注成功时回填热时间线用）。
func RecentVideosByAuthor(ctx context.Context, uid uint, limit int) ([]models.Video, error) {
	var videos []models.Video
	err := database.DB.WithContext(ctx).
		Select("id, created_ts").
		Where("user_id = ? AND status = ?", uid, 1).
		Order("created_ts DESC").Limit(limit).Find(&videos).Error
	return videos, err
}

// IncrementViewsMySQL 播放量持久化：先原子 +1，再读回新值。
func IncrementViewsMySQL(ctx context.Context, id uint) (int64, error) {
	res := database.DB.WithContext(ctx).Model(&models.Video{}).
		Where("id = ? AND status = ?", id, 1).
		UpdateColumn("views", gorm.Expr("views + 1"))
	if res.Error != nil {
		return 0, res.Error
	}
	var views int64
	if err := database.DB.WithContext(ctx).Model(&models.Video{}).
		Where("id = ?", id).Pluck("views", &views).Error; err != nil {
		return 0, err
	}
	return views, nil
}

// GetViewsMySQL 读 MySQL 里的播放量。
func GetViewsMySQL(ctx context.Context, id uint) (int64, error) {
	var views int64
	err := database.DB.WithContext(ctx).Model(&models.Video{}).
		Where("id = ?", id).Pluck("views", &views).Error
	return views, err
}
