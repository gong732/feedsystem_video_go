package service

import (
	"context"
	"errors"
	"log"

	"video-feed/apierror"
	"video-feed/repository"

	"gorm.io/gorm"
)

// LikeService 点赞/取消点赞。
type LikeService struct{}

// LikeResult 点赞接口响应。
type LikeResult struct {
	Liked      bool  `json:"liked"`
	LikesCount int64 `json:"likes_count"`
}

// Toggle 切换点赞状态：点赞/取消 + 计数更新在同一事务里完成。
func (s *LikeService) Toggle(ctx context.Context, uid, videoID uint) (*LikeResult, error) {
	exists, err := repository.VideoExists(ctx, videoID)
	if err != nil {
		return nil, apierror.Internal(err)
	}
	if !exists {
		return nil, apierror.NotFound("视频不存在")
	}

	// 先尝试点赞。唯一索引冲突说明已经点过，转为取消点赞，而不是把冲突当 500。
	err = repository.Tx(ctx, func(tx *gorm.DB) error {
		if err := repository.CreateLikeTx(tx, uid, videoID); err != nil {
			return err
		}
		return repository.IncrLikesTx(tx, videoID)
	})

	liked := true
	if err != nil {
		if !errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, apierror.Internal(err)
		}

		liked = false
		err = repository.Tx(ctx, func(tx *gorm.DB) error {
			n, err := repository.DeleteLikeTx(tx, uid, videoID)
			if err != nil {
				return err
			}
			if n == 0 {
				return nil
			}
			return repository.DecrLikesTx(tx, videoID)
		})
		if err != nil {
			return nil, apierror.Internal(err)
		}
	}

	_ = repository.CacheDel(ctx, repository.VideoInfoKey(videoID))

	count, err := repository.LikesCount(ctx, videoID)
	if err != nil {
		log.Printf("[like] 查询点赞数失败 video=%d: %v", videoID, err)
	}
	return &LikeResult{Liked: liked, LikesCount: count}, nil
}

// Status 当前用户对该视频的点赞状态。
func (s *LikeService) Status(ctx context.Context, uid, videoID uint) (*LikeResult, error) {
	exists, err := repository.VideoExists(ctx, videoID)
	if err != nil {
		return nil, apierror.Internal(err)
	}
	if !exists {
		return nil, apierror.NotFound("视频不存在")
	}

	liked, err := repository.HasLike(ctx, uid, videoID)
	if err != nil {
		return nil, apierror.Internal(err)
	}
	count, err := repository.LikesCount(ctx, videoID)
	if err != nil {
		return nil, apierror.Internal(err)
	}
	return &LikeResult{Liked: liked, LikesCount: count}, nil
}
