package models

import "time"

// Comment 评论表
//
// 索引设计：idx_comment_video(video_id, status)
//
//	InnoDB 的二级索引叶子节点会隐式追加主键（id），所以实际索引等价于 (video_id, status, id)，
//	评论列表 "WHERE video_id = ? AND status = 1 ORDER BY id DESC" 可以整条走索引，
//	既不用回表排序，也不用额外再建 (video_id, id) 复合索引。
//
// 不用 created_ts 当游标：评论是同一张表内单调自增的，id 天生唯一且无"同毫秒并列"问题；
// 而视频跨表/跨 Redis 需要绝对时间锚，所以那边才用毫秒时间戳。
type Comment struct {
	ID      uint   `gorm:"primaryKey" json:"id"`
	UserID  uint   `gorm:"index" json:"user_id"`
	VideoID uint   `gorm:"index:idx_comment_video,priority:1" json:"video_id"`
	Content string `gorm:"size:500;not null" json:"content"`

	// 软删除：不物理删行，避免外键/统计丢失，也方便后续做"评论审核/恢复"
	Status int `gorm:"default:1;index:idx_comment_video,priority:2" json:"-"` // 1 正常 0 已删除

	CreatedTs int64     `gorm:"autoCreateTime:milli;index" json:"created_ts"` // 毫秒绝对时间戳（展示/排序备用）
	CreatedAt time.Time `json:"created_at"`                                   // 展示用可读时间

	// 关联信息（查询时填充，不入库）
	Author User `gorm:"-" json:"author"`
}
