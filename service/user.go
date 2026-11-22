package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"video-feed/apierror"
	"video-feed/config"
	"video-feed/models"
	"video-feed/repository"
	"video-feed/utils"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// UserService 账号注册/登录/资料/退出登录。
type UserService struct{}

// UserInfo 对外资料（不含密码、token 版本）。
type UserInfo struct {
	ID        uint      `json:"id"`
	Username  string    `json:"username"`
	Nickname  string    `json:"nickname"`
	Avatar    string    `json:"avatar"`
	CreatedAt time.Time `json:"created_at"`
	Following int64     `json:"following"`
	Followers int64     `json:"followers"`
}

// dummyHash 用于"用户不存在"时也做一次 bcrypt 比较，抹平响应时间差，防用户名枚举。
var dummyHash []byte

func init() {
	dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password-for-timing"), bcrypt.DefaultCost)
}

// Register 注册：靠数据库唯一索引兜底并发，不再"先 COUNT 再 INSERT"。
func (s *UserService) Register(ctx context.Context, username, password, nickname string) (*models.User, error) {
	username = strings.TrimSpace(username)
	nickname = strings.TrimSpace(nickname)

	if utf8.RuneCountInString(username) < 2 || utf8.RuneCountInString(username) > 32 {
		return nil, apierror.BadRequest("用户名长度需为 2-32 个字符")
	}
	// bcrypt 只处理前 72 字节，超长密码会被静默截断，必须直接拒绝。
	if len(password) < 6 || len(password) > 72 {
		return nil, apierror.BadRequest("密码长度需为 6-72 个字节")
	}
	if nickname == "" {
		nickname = username
	}
	if utf8.RuneCountInString(nickname) > 32 {
		return nil, apierror.BadRequest("昵称最长 32 个字符")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, apierror.Internal(err)
	}

	u := models.User{Username: username, Nickname: nickname, Password: string(hash)}
	if err := repository.CreateUser(ctx, &u); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, apierror.Conflict("用户名已存在")
		}
		return nil, apierror.Internal(err)
	}
	return &u, nil
}

// Login 登录：统一"用户名或密码错误"，不区分用户不存在与密码错。
func (s *UserService) Login(ctx context.Context, username, password string) (string, *models.User, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return "", nil, apierror.BadRequest("用户名或密码错误")
	}

	u, err := repository.FindUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
			return "", nil, apierror.BadRequest("用户名或密码错误")
		}
		return "", nil, apierror.Internal(err)
	}

	if bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)) != nil {
		return "", nil, apierror.BadRequest("用户名或密码错误")
	}

	cfg := config.Load()
	token, err := utils.GenerateToken(&cfg.JWT, u.ID, u.TokenVersion)
	if err != nil {
		return "", nil, apierror.Internal(err)
	}
	u.Password = ""
	return token, u, nil
}

// Info 用户主页资料 + 关注/粉丝数。
func (s *UserService) Info(ctx context.Context, id uint) (*UserInfo, error) {
	u, err := repository.FindUserByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apierror.NotFound("用户不存在")
		}
		return nil, apierror.Internal(err)
	}

	following, err := repository.CountFollowing(ctx, id)
	if err != nil {
		return nil, apierror.Internal(err)
	}
	followers, err := repository.CountFollowers(ctx, id)
	if err != nil {
		return nil, apierror.Internal(err)
	}

	return &UserInfo{
		ID:        u.ID,
		Username:  u.Username,
		Nickname:  u.Nickname,
		Avatar:    u.Avatar,
		CreatedAt: u.CreatedAt,
		Following: following,
		Followers: followers,
	}, nil
}

// Logout 退出登录：递增 token 版本，使该用户所有已签发 token 立即失效。
func (s *UserService) Logout(ctx context.Context, uid uint) error {
	if _, err := repository.BumpTokenVersion(ctx, uid); err != nil {
		return apierror.Internal(err)
	}
	return nil
}
