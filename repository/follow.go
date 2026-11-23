package repository

import (
	"context"

	"video-feed/database"
	"video-feed/models"
)

// CreateFollow 插入关注关系；复合唯一索引冲突返回 gorm.ErrDuplicatedKey。
func CreateFollow(ctx context.Context, uid, target uint) error {
	return database.DB.WithContext(ctx).Create(&models.Follow{
		UserID: uid, FollowUserID: target,
	}).Error
}

// DeleteFollow 删除关注关系。
func DeleteFollow(ctx context.Context, uid, target uint) error {
	return database.DB.WithContext(ctx).
		Where("user_id = ? AND follow_user_id = ?", uid, target).
		Delete(&models.Follow{}).Error
}

// FollowIDs 我关注了谁（只取 id，热区过滤/冷区查询用）。
func FollowIDs(ctx context.Context, uid uint) ([]uint, error) {
	var follows []models.Follow
	err := database.DB.WithContext(ctx).Select("follow_user_id").
		Where("user_id = ?", uid).Find(&follows).Error
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(follows))
	for _, f := range follows {
		ids = append(ids, f.FollowUserID)
	}
	return ids, nil
}

// FollowerIDs 我的粉丝（发布 fanout 用）。
func FollowerIDs(ctx context.Context, uid uint) ([]uint, error) {
	var follows []models.Follow
	err := database.DB.WithContext(ctx).Select("user_id").
		Where("follow_user_id = ?", uid).Find(&follows).Error
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(follows))
	for _, f := range follows {
		ids = append(ids, f.UserID)
	}
	return ids, nil
}

// ListFollowingPage 我关注的人，按 follow 行 id 倒序分页。
func ListFollowingPage(ctx context.Context, uid, cursor uint, limit int) ([]models.Follow, error) {
	q := database.DB.WithContext(ctx).Where("user_id = ?", uid)
	if cursor > 0 {
		q = q.Where("id < ?", cursor)
	}
	var rows []models.Follow
	err := q.Order("id DESC").Limit(limit + 1).Find(&rows).Error
	return rows, err
}

// ListFollowerPage 我的粉丝，按 follow 行 id 倒序分页。
func ListFollowerPage(ctx context.Context, uid, cursor uint, limit int) ([]models.Follow, error) {
	q := database.DB.WithContext(ctx).Where("follow_user_id = ?", uid)
	if cursor > 0 {
		q = q.Where("id < ?", cursor)
	}
	var rows []models.Follow
	err := q.Order("id DESC").Limit(limit + 1).Find(&rows).Error
	return rows, err
}

// CountFollowing 关注数。
func CountFollowing(ctx context.Context, uid uint) (int64, error) {
	var n int64
	err := database.DB.WithContext(ctx).Model(&models.Follow{}).
		Where("user_id = ?", uid).Count(&n).Error
	return n, err
}

// CountFollowers 粉丝数。
func CountFollowers(ctx context.Context, uid uint) (int64, error) {
	var n int64
	err := database.DB.WithContext(ctx).Model(&models.Follow{}).
		Where("follow_user_id = ?", uid).Count(&n).Error
	return n, err
}

// IsFollowing 是否已关注。
func IsFollowing(ctx context.Context, uid, target uint) (bool, error) {
	var n int64
	err := database.DB.WithContext(ctx).Model(&models.Follow{}).
		Where("user_id = ? AND follow_user_id = ?", uid, target).Count(&n).Error
	return n > 0, err
}
