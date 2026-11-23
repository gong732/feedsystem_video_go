package service

import (
	"testing"

	"video-feed/models"
)

func TestSortByTsDesc(t *testing.T) {
	list := []models.Video{
		{ID: 1, CreatedTs: 100},
		{ID: 3, CreatedTs: 300},
		{ID: 2, CreatedTs: 200},
	}
	sortByTs(list)
	want := []uint{3, 2, 1}
	for i, id := range want {
		if list[i].ID != id {
			t.Fatalf("位置 %d = %d, want %d", i, list[i].ID, id)
		}
	}
}

func TestSortByTsSameMillisecondByIDDesc(t *testing.T) {
	list := []models.Video{
		{ID: 5, CreatedTs: 100},
		{ID: 9, CreatedTs: 100},
		{ID: 7, CreatedTs: 100},
	}
	sortByTs(list)
	want := []uint{9, 7, 5}
	for i, id := range want {
		if list[i].ID != id {
			t.Fatalf("位置 %d = %d, want %d", i, list[i].ID, id)
		}
	}
}

func TestMergeVideosDedup(t *testing.T) {
	a := []models.Video{{ID: 1}, {ID: 2}}
	b := []models.Video{{ID: 2}, {ID: 3}}
	got := mergeVideos(a, b)
	if len(got) != 3 {
		t.Fatalf("去重后长度 = %d, want 3", len(got))
	}
	if got[0].ID != 1 || got[1].ID != 2 || got[2].ID != 3 {
		t.Fatalf("顺序错误: %+v", got)
	}
}
