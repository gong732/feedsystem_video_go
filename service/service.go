package service

// 单例：handler 只依赖这些入口，不直接碰 repository。
var (
	Users    = &UserService{}
	Videos   = &VideoService{}
	Likes    = &LikeService{}
	Comments = &CommentService{}
	Follows  = &FollowService{}
	Feeds    = &FeedService{}
)
