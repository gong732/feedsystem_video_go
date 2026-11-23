package service

import (
	"context"
	"errors"
	"log"

	"video-feed/apierror"
	"video-feed/models"
	"video-feed/repository"

	"gorm.io/gorm"
)

// FollowService 关注/取关、关注列表、粉丝列表。
type FollowService struct{}

// UserCard 关注/粉丝列表里的用户卡片（不含统计，避免 N+1）。
type UserCard struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}

// Toggle 关注/取关：唯一索引冲突 = 已经关注过，转成取关。
func (s *FollowService) Toggle(ctx context.Context, me, target uint) (string, error) {
	if me == target {
		return "", apierror.BadRequest("不能关注自己")
	}
	exists, err := repository.UserExists(ctx, target)
	if err != nil {
		return "", apierror.Internal(err)
	}
	if !exists {
		return "", apierror.NotFound("用户不存在")
	}

	err = repository.CreateFollow(ctx, me, target)
	if err == nil {
		// 关注成功后把对方最近的视频补进我的热时间线，避免关注流首页空白。
		videos, qerr := repository.RecentVideosByAuthor(ctx, target, 50)
		if qerr != nil {
			log.Printf("[follow] 回填时间线查询失败 me=%d target=%d: %v", me, target, qerr)
		} else {
			_ = repository.BackfillFeedHot(ctx, me, videosToTimeline(videos))
		}
		return "关注成功", nil
	}
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		return "", apierror.Internal(err)
	}

	if err := repository.DeleteFollow(ctx, me, target); err != nil {
		return "", apierror.Internal(err)
	}
	return "已取消关注", nil
}

// Following 我关注的人，游标为 follow 行 id。
func (s *FollowService) Following(ctx context.Context, uid uint, cursor int64, limit int) (*Page[UserCard], error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var c uint
	if cursor > 0 {
		c = uint(cursor)
	}
	rows, err := repository.ListFollowingPage(ctx, uid, c, limit)
	if err != nil {
		return nil, apierror.Internal(err)
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	ids := make([]uint, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.FollowUserID)
	}
	cards := make([]UserCard, 0, len(rows))
	if len(ids) > 0 {
		users, err := repository.FindUsersByIDs(ctx, ids)
		if err != nil {
			return nil, apierror.Internal(err)
		}
		m := make(map[uint]models.User, len(users))
		for _, u := range users {
			m[u.ID] = u
		}
		for _, r := range rows {
			if u, ok := m[r.FollowUserID]; ok {
				cards = append(cards, UserCard{ID: u.ID, Username: u.Username, Nickname: u.Nickname, Avatar: u.Avatar})
			}
		}
	}

	page := newPage(cards)
	if hasMore && len(rows) > 0 {
		page.NextCursor = int64(rows[len(rows)-1].ID)
	}
	return page, nil
}

// Followers 我的粉丝，游标为 follow 行 id。
func (s *FollowService) Followers(ctx context.Context, uid uint, cursor int64, limit int) (*Page[UserCard], error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var c uint
	if cursor > 0 {
		c = uint(cursor)
	}
	rows, err := repository.ListFollowerPage(ctx, uid, c, limit)
	if err != nil {
		return nil, apierror.Internal(err)
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	ids := make([]uint, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.UserID)
	}
	cards := make([]UserCard, 0, len(rows))
	if len(ids) > 0 {
		users, err := repository.FindUsersByIDs(ctx, ids)
		if err != nil {
			return nil, apierror.Internal(err)
		}
		m := make(map[uint]models.User, len(users))
		for _, u := range users {
			m[u.ID] = u
		}
		for _, r := range rows {
			if u, ok := m[r.UserID]; ok {
				cards = append(cards, UserCard{ID: u.ID, Username: u.Username, Nickname: u.Nickname, Avatar: u.Avatar})
			}
		}
	}

	page := newPage(cards)
	if hasMore && len(rows) > 0 {
		page.NextCursor = int64(rows[len(rows)-1].ID)
	}
	return page, nil
}

// IsFollowing 我是否关注了目标用户（游客返回 false）。
func (s *FollowService) IsFollowing(ctx context.Context, uid, target uint) (bool, error) {
	if uid == 0 {
		return false, nil
	}
	ok, err := repository.IsFollowing(ctx, uid, target)
	if err != nil {
		return false, apierror.Internal(err)
	}
	return ok, nil
}
