package handlers

import "golang.org/x/sync/singleflight"

// 三个独立的 singleflight.Group：热榜回源 / 视频详情读 / 关注流冷数据回源
//
// singleflight 解决"缓存击穿"：热点数据（如热榜、爆款视频）的缓存同时过期时，
// 若 1000 个并发请求同时回源 MySQL，DB 会被打爆。
// 它的做法是：并发请求同一个 key 时，只有一个 goroutine 真正执行函数，
// 其余 goroutine 阻塞等待，共享同一个结果 —— 回源只发生一次。
var (
	hotSF   singleflight.Group // 热榜：ids → 查视频详情（回表）
	videoSF singleflight.Group // 视频详情：id → 查视频行
	feedSF  singleflight.Group // 关注流冷数据：MySQL 时间线回源
)
