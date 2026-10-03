package strmscrape

import (
	"context"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/strm"
)

// TestTmdbEntriesRejectJavTask 钉住「拿 TMDB 那套去刮番号任务」会被挡住。
//
// 之前这条路由前端按钮藏起来兜着（AuxToolsManagement.vue 的 isJavTask），后端没有
// 守卫 —— 直接打 API 就能让 TMDB 匹配结果覆盖掉用户整理好的番号 nfo/海报。
//
// 这里只验守卫本身（任务级拒绝），不碰后面的扫盘与网络：被拒时不该有任何副作用。
func TestTmdbEntriesRejectJavTask(t *testing.T) {
	strmRoot := t.TempDir()
	task := &domain.StrmTask{
		ID:           7,
		Name:         "番号库",
		OutputFolder: "番号库",
		MediaKind:    domain.StrmMediaKindJav,
	}
	strmSvc := strm.NewService(strm.ServiceOptions{
		Repo:    &rematchTaskRepo{task: task},
		StrmDir: strmRoot,
	})
	svc := New(Options{Strm: strmSvc, StrmDir: strmRoot, DataDir: t.TempDir()})
	ctx := context.Background()

	assertRejected := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s 应拒绝番号任务", name)
			return
		}
		if !strings.Contains(err.Error(), "不是 tmdb 影片任务") {
			t.Errorf("%s 错误信息不对：%v", name, err)
		}
	}

	assertRejected("RunAsync", svc.RunAsync(ctx, RunRequest{StrmTaskID: 7}))
	assertRejected("RebuildIndex", svc.RebuildIndex(ctx, 7))
	_, err := svc.ListItems(ctx, 7, ItemListQuery{})
	assertRejected("ListItems", err)
	_, _, err = svc.Rescrape(ctx, RescrapeRequest{StrmTaskID: 7, ItemID: "x"})
	assertRejected("Rescrape", err)
	_, _, err = svc.Rematch(ctx, RematchRequest{StrmTaskID: 7, ItemID: "x", TMDBID: "1"})
	assertRejected("Rematch", err)
	_, err = svc.MarkNormal(ctx, MarkNormalRequest{StrmTaskID: 7, ItemID: "x"})
	assertRejected("MarkNormal", err)
}

// TestTmdbEntriesStillAcceptTmdbTask 反向：tmdb 任务不该被这道新闸误伤。
// 用一个不存在的 item id 让它们走到「找不到作品」那一步 —— 只要错误不是
// 「不是 tmdb 影片任务」，就说明守卫放行了。
func TestTmdbEntriesStillAcceptTmdbTask(t *testing.T) {
	strmRoot := t.TempDir()
	task := &domain.StrmTask{
		ID:           8,
		Name:         "电影库",
		OutputFolder: "电影库",
		MediaKind:    domain.StrmMediaKindTmdb,
	}
	strmSvc := strm.NewService(strm.ServiceOptions{
		Repo:    &rematchTaskRepo{task: task},
		StrmDir: strmRoot,
	})
	svc := New(Options{Strm: strmSvc, StrmDir: strmRoot, DataDir: t.TempDir()})
	ctx := context.Background()

	_, _, err := svc.Rescrape(ctx, RescrapeRequest{StrmTaskID: 8, ItemID: "不存在的作品"})
	if err == nil {
		t.Fatal("不存在的作品应报错")
	}
	if strings.Contains(err.Error(), "不是 tmdb 影片任务") {
		t.Errorf("tmdb 任务被媒体类型闸误伤：%v", err)
	}
}
