package models

import "time"

// User 用户表
type User struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Username  string    `gorm:"size:32;not null;uniqueIndex" json:"username"` // 登录名，唯一
	Nickname  string    `gorm:"size:32" json:"nickname"`                      // 展示昵称
	Password  string    `gorm:"size:128;not null" json:"-"`                   // bcrypt 哈希，json 不返回
	Avatar    string    `gorm:"size:255" json:"avatar"`
	CreatedAt time.Time `json:"created_at"`

	// TokenVersion 登录态版本号：退出登录时 +1，使旧 token 全部失效。
	// token 里携带签发时的版本，中间件比对当前版本；不改密码也能主动踢掉旧会话。
	TokenVersion int `gorm:"default:0" json:"-"`
}

// Follow 关注关系表：谁关注了谁
// 复合唯一索引 (user_id, follow_user_id) 保证不能重复关注（和博客的点赞表一个思路）
type Follow struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	UserID       uint      `gorm:"index;uniqueIndex:idx_follow" json:"user_id"`        // 粉丝
	FollowUserID uint      `gorm:"index;uniqueIndex:idx_follow" json:"follow_user_id"` // 被关注的人
	CreatedAt    time.Time `json:"created_at"`
}
