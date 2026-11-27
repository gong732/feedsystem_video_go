package repository

import (
	"context"
	"testing"
	"time"

	"video-feed/config"
	"video-feed/database"
	"video-feed/models"
)

// TestListDiscoverDBSameMsNoSkip 验证同毫秒视频跨页不遗漏。
// 需要本地 MySQL（docker compose up -d mysql），无库时自动跳过。
func TestListDiscoverDBSameMsNoSkip(t *testing.T) {
	cfg := config.Load()
	if err := database.InitMySQL(&cfg.MySQL); err != nil {
		t.Skipf("需要 MySQL（docker compose up -d mysql 后再跑），跳过: %v", err)
	}
	defer database.CloseMySQL()

	ctx := context.Background()
	// 用远超现有数据的时间戳，保证这 5 条是排序最前的同一毫秒块
	ts := time.Now().Add(24 * time.Hour).UnixMilli()

	want := map[uint]bool{}
	var ids []uint
	for i := 0; i < 5; i++ {
		v := models.Video{
			UserID:    1,
			Title:     "游标测试",
			VideoURL:  "http://example.com/x.mp4",
			Status:    1,
			CreatedTs: ts, // 显式非零值，绕过 autoCreateTime
		}
		if err := CreateVideo(ctx, &v); err != nil {
			t.Fatalf("插入视频失败: %v", err)
		}
		want[v.ID] = true
		ids = append(ids, v.ID)
	}
	defer func() {
		_ = database.DB.Where("id IN ?", ids).Delete(&models.Video{}).Error
	}()

	// limit=2 翻页，收集这 5 条测试视频的出现次数
	var got []uint
	var cTs int64
	var cID uint
	for i := 0; i < 1000; i++ {
		list, err := ListDiscoverDB(ctx, cTs, cID, 2)
		if err != nil {
			t.Fatalf("翻页查询失败: %v", err)
		}
		if len(list) == 0 {
			break
		}
		for _, v := range list {
			if want[v.ID] {
				got = append(got, v.ID)
			}
		}
		last := list[len(list)-1]
		cTs, cID = last.CreatedTs, last.ID
		if len(list) < 2 {
			break
		}
	}

	if len(got) != len(want) {
		t.Fatalf("翻页共取到测试视频 %d 条, want %d（存在同毫秒漏数据）", len(got), len(want))
	}
	seen := map[uint]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("测试视频 id=%d 重复出现", id)
		}
		seen[id] = true
	}
}
