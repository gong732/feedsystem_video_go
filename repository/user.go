package repository

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"video-feed/database"
	"video-feed/models"

	"gorm.io/gorm"
)

const tokenVersionTTL = 5 * time.Minute

func tokenVersionKey(uid uint) string {
	return fmt.Sprintf("user:tokenver:%d", uid)
}

// CreateUser 插入用户；用户名唯一索引冲突会返回 gorm.ErrDuplicatedKey。
func CreateUser(ctx context.Context, u *models.User) error {
	return database.DB.WithContext(ctx).Create(u).Error
}

// FindUserByUsername 登录用，返回完整字段（含密码哈希与 token_version）。
func FindUserByUsername(ctx context.Context, username string) (*models.User, error) {
	var u models.User
	if err := database.DB.WithContext(ctx).Where("username = ?", username).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// FindUserByID 返回完整用户行。
func FindUserByID(ctx context.Context, id uint) (*models.User, error) {
	var u models.User
	if err := database.DB.WithContext(ctx).First(&u, id).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// FindUsersByIDs 批量取展示字段，避免 N+1。
func FindUsersByIDs(ctx context.Context, ids []uint) ([]models.User, error) {
	if len(ids) == 0 {
		return []models.User{}, nil
	}
	var users []models.User
	err := database.DB.WithContext(ctx).
		Select("id, username, nickname, avatar").
		Where("id IN ?", ids).Find(&users).Error
	return users, err
}

// UserExists 判断用户是否存在。
func UserExists(ctx context.Context, id uint) (bool, error) {
	var n int64
	err := database.DB.WithContext(ctx).Model(&models.User{}).
		Where("id = ?", id).Count(&n).Error
	return n > 0, err
}

// TokenVersion 读用户当前的 token 版本：Redis 优先，DB 兜底。
func TokenVersion(ctx context.Context, uid uint) (int, error) {
	if v, ok := CacheGet(ctx, tokenVersionKey(uid)); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n, nil
		}
	}

	var u models.User
	if err := database.DB.WithContext(ctx).Select("id, token_version").First(&u, uid).Error; err != nil {
		return 0, err
	}
	_ = CacheSet(ctx, tokenVersionKey(uid), []byte(strconv.Itoa(u.TokenVersion)), tokenVersionTTL)
	return u.TokenVersion, nil
}

// BumpTokenVersion 递增版本号：使该用户已签发的所有旧 token 立即失效。
func BumpTokenVersion(ctx context.Context, uid uint) (int, error) {
	var ver int
	err := Tx(ctx, func(tx *gorm.DB) error {
		if err := tx.Model(&models.User{}).Where("id = ?", uid).
			UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error; err != nil {
			return err
		}
		return tx.Model(&models.User{}).Where("id = ?", uid).
			Pluck("token_version", &ver).Error
	})
	if err != nil {
		return 0, err
	}
	_ = CacheSet(ctx, tokenVersionKey(uid), []byte(strconv.Itoa(ver)), tokenVersionTTL)
	return ver, nil
}
