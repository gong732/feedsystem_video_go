package models

import "time"

// Video 视频表
type Video struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	UserID     uint      `gorm:"index" json:"user_id"` // 发布者
	Title      string    `gorm:"size:128;not null" json:"title"`
	Description string   `gorm:"size:1024" json:"description"`
	VideoURL   string    `gorm:"size:255" json:"video_url"`  // 视频文件地址
	CoverURL   string    `gorm:"size:255" json:"cover_url"`  // 封面图地址
	Duration   int       `json:"duration"`                   // 时长（秒）
	Views      int64     `gorm:"default:0" json:"views"`     // 播放量
	LikesCount int64     `gorm:"default:0" json:"likes_count"`
	Status     int       `gorm:"default:1" json:"status"` // 1 正常 0 下架
	CreatedAt  time.Time `gorm:"index" json:"created_at"` // 展示用（可读时间）

	// CreatedTs 绝对时间戳（Unix 毫秒）——所有排序/游标/冷区边界只用它，不用 created_at。
	// 原因：created_at 是 DATETIME，受 MySQL/Go 会话时区影响，时区一变全部错位；
	// CreatedTs 是整数毫秒，与时区无关，是"唯一权威时间基准"。
	CreatedTs int64 `gorm:"autoCreateTime:milli;index" json:"created_ts"`

	// 关联信息（查询时填充，不入库）
	Author User `gorm:"-" json:"author"`
}

// Like 点赞表：谁给哪个视频点了赞
type Like struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index;uniqueIndex:idx_like_video" json:"user_id"`
	VideoID   uint      `gorm:"index;uniqueIndex:idx_like_video" json:"video_id"`
	CreatedAt time.Time `json:"created_at"`
}
