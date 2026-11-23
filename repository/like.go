package repository

import (
	"context"

	"video-feed/database"
	"video-feed/models"

	"gorm.io/gorm"
)

// CreateLikeTx 插入点赞明细；复合唯一索引冲突返回 gorm.ErrDuplicatedKey。
func CreateLikeTx(tx *gorm.DB, uid, vid uint) error {
	return tx.Create(&models.Like{UserID: uid, VideoID: vid}).Error
}

// IncrLikesTx 点赞数原子 +1。
func IncrLikesTx(tx *gorm.DB, vid uint) error {
	return tx.Model(&models.Video{}).Where("id = ?", vid).
		UpdateColumn("likes_count", gorm.Expr("likes_count + 1")).Error
}

// DeleteLikeTx 删除点赞明细，返回实际删除行数。
func DeleteLikeTx(tx *gorm.DB, uid, vid uint) (int64, error) {
	res := tx.Where("user_id = ? AND video_id = ?", uid, vid).Delete(&models.Like{})
	return res.RowsAffected, res.Error
}

// DecrLikesTx 点赞数原子 -1，且不会减到负数。
func DecrLikesTx(tx *gorm.DB, vid uint) error {
	return tx.Model(&models.Video{}).Where("id = ? AND likes_count > 0", vid).
		UpdateColumn("likes_count", gorm.Expr("likes_count - 1")).Error
}

// LikesCount 读当前点赞数。
func LikesCount(ctx context.Context, vid uint) (int64, error) {
	var n int64
	err := database.DB.WithContext(ctx).Model(&models.Video{}).
		Where("id = ?", vid).Pluck("likes_count", &n).Error
	return n, err
}

// HasLike 查询单个"我点没点过"。
func HasLike(ctx context.Context, uid, vid uint) (bool, error) {
	if uid == 0 {
		return false, nil
	}
	var n int64
	err := database.DB.WithContext(ctx).Model(&models.Like{}).
		Where("user_id = ? AND video_id = ?", uid, vid).Count(&n).Error
	return n > 0, err
}

// LikedSet 批量查询一页视频里当前用户点过赞的集合。
func LikedSet(ctx context.Context, uid uint, vids []uint) (map[uint]bool, error) {
	set := make(map[uint]bool, len(vids))
	if uid == 0 || len(vids) == 0 {
		return set, nil
	}
	var likes []models.Like
	err := database.DB.WithContext(ctx).Select("video_id").
		Where("user_id = ? AND video_id IN ?", uid, vids).Find(&likes).Error
	if err != nil {
		return nil, err
	}
	for _, l := range likes {
		set[l.VideoID] = true
	}
	return set, nil
}
