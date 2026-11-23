package repository

import (
	"context"

	"video-feed/database"
	"video-feed/models"

	"gorm.io/gorm"
)

// CreateCommentTx 插入评论行。
func CreateCommentTx(tx *gorm.DB, c *models.Comment) error {
	return tx.Create(c).Error
}

// IncrCommentsTx 评论数原子 +1。
func IncrCommentsTx(tx *gorm.DB, vid uint) error {
	return tx.Model(&models.Video{}).Where("id = ?", vid).
		UpdateColumn("comments_count", gorm.Expr("comments_count + 1")).Error
}

// ListComments 按 id 倒序游标分页；多取一条用于判断 has_more。
func ListComments(ctx context.Context, vid uint, cursor uint64, limit int) ([]models.Comment, error) {
	q := database.DB.WithContext(ctx).Where("video_id = ? AND status = ?", vid, 1)
	if cursor > 0 {
		q = q.Where("id < ?", cursor)
	}
	var list []models.Comment
	err := q.Order("id DESC").Limit(limit + 1).Find(&list).Error
	return list, err
}

// FindComment 取未删除的评论（删除前校验归属用）。
func FindComment(ctx context.Context, id uint) (*models.Comment, error) {
	var c models.Comment
	err := database.DB.WithContext(ctx).
		Select("id, user_id, video_id").
		Where("id = ? AND status = ?", id, 1).First(&c).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// SoftDeleteCommentTx 软删除，返回实际更新行数（并发下可能为 0）。
func SoftDeleteCommentTx(tx *gorm.DB, id, uid uint) (int64, error) {
	res := tx.Model(&models.Comment{}).
		Where("id = ? AND user_id = ? AND status = ?", id, uid, 1).
		UpdateColumn("status", 0)
	return res.RowsAffected, res.Error
}

// DecrCommentsTx 评论数原子 -1，且不会减到负数。
func DecrCommentsTx(tx *gorm.DB, vid uint) error {
	return tx.Model(&models.Video{}).Where("id = ? AND comments_count > 0", vid).
		UpdateColumn("comments_count", gorm.Expr("comments_count - 1")).Error
}
