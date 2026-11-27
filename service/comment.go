package service

import (
	"context"
	"errors"
	"log"
	"strconv"
	"strings"
	"unicode/utf8"

	"video-feed/apierror"
	"video-feed/models"
	"video-feed/repository"

	"gorm.io/gorm"
)

// CommentService 评论发布/分页/删除。
type CommentService struct{}

// Create 发表评论：评论行 + 视频评论数在同一事务内更新。
func (s *CommentService) Create(ctx context.Context, uid, videoID uint, content string) (*models.Comment, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, apierror.BadRequest("评论内容不能为空")
	}
	if utf8.RuneCountInString(content) > 500 {
		return nil, apierror.BadRequest("评论最长 500 个字符")
	}

	exists, err := repository.VideoExists(ctx, videoID)
	if err != nil {
		return nil, apierror.Internal(err)
	}
	if !exists {
		return nil, apierror.NotFound("视频不存在")
	}

	c := models.Comment{UserID: uid, VideoID: videoID, Content: content, Status: 1}
	err = repository.Tx(ctx, func(tx *gorm.DB) error {
		if err := repository.CreateCommentTx(tx, &c); err != nil {
			return err
		}
		return repository.IncrCommentsTx(tx, videoID)
	})
	if err != nil {
		return nil, apierror.Internal(err)
	}

	_ = repository.CacheDel(ctx, repository.VideoInfoKey(videoID))

	if author, err := repository.FindUserByID(ctx, uid); err == nil {
		c.Author = *author
	} else {
		log.Printf("[comment] 回填作者失败 uid=%d: %v", uid, err)
	}
	return &c, nil
}

// List 评论分页：按 id 倒序，cursor 为上一页最后一条评论 id。
func (s *CommentService) List(ctx context.Context, videoID uint, cursor int64, limit int) (*Page[models.Comment], error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	exists, err := repository.VideoExists(ctx, videoID)
	if err != nil {
		return nil, apierror.Internal(err)
	}
	if !exists {
		return nil, apierror.NotFound("视频不存在")
	}

	var cursorID uint64
	if cursor > 0 {
		cursorID = uint64(cursor)
	}
	comments, err := repository.ListComments(ctx, videoID, cursorID, limit)
	if err != nil {
		return nil, apierror.Internal(err)
	}

	hasMore := len(comments) > limit
	if hasMore {
		comments = comments[:limit]
	}

	ids := make([]uint, 0, len(comments))
	seen := make(map[uint]bool, len(comments))
	for _, c := range comments {
		if !seen[c.UserID] {
			seen[c.UserID] = true
			ids = append(ids, c.UserID)
		}
	}
	if len(ids) > 0 {
		users, err := repository.FindUsersByIDs(ctx, ids)
		if err != nil {
			log.Printf("[comment] 回填作者失败: %v", err)
		} else {
			m := make(map[uint]models.User, len(users))
			for _, u := range users {
				m[u.ID] = u
			}
			for i := range comments {
				if u, ok := m[comments[i].UserID]; ok {
					comments[i].Author = u
				}
			}
		}
	}

	page := newPage(comments)
	if hasMore && len(comments) > 0 {
		page.NextCursor = strconv.FormatUint(uint64(comments[len(comments)-1].ID), 10)
	}
	return page, nil
}

// Delete 删除自己的评论（软删），并回减视频评论数。
func (s *CommentService) Delete(ctx context.Context, uid, commentID uint) error {
	c, err := repository.FindComment(ctx, commentID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apierror.NotFound("评论不存在")
		}
		return apierror.Internal(err)
	}
	if c.UserID != uid {
		return apierror.Forbidden("无权删除该评论")
	}

	err = repository.Tx(ctx, func(tx *gorm.DB) error {
		n, err := repository.SoftDeleteCommentTx(tx, commentID, uid)
		if err != nil {
			return err
		}
		if n == 0 {
			return gorm.ErrRecordNotFound
		}
		return repository.DecrCommentsTx(tx, c.VideoID)
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apierror.NotFound("评论不存在")
		}
		return apierror.Internal(err)
	}

	_ = repository.CacheDel(ctx, repository.VideoInfoKey(c.VideoID))
	return nil
}
