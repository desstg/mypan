package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/strm"
)

type rematchTaskRepo struct {
	task *domain.StrmTask
}

func (r *rematchTaskRepo) Create(context.Context, *domain.StrmTask) (int64, error) { return 0, nil }
func (r *rematchTaskRepo) Update(context.Context, *domain.StrmTask) error          { return nil }
func (r *rematchTaskRepo) Delete(context.Context, int64) error                     { return nil }
func (r *rematchTaskRepo) Get(context.Context, int64) (*domain.StrmTask, error)    { return r.task, nil }
func (r *rematchTaskRepo) List(context.Context) ([]*domain.StrmTask, error) {
	return []*domain.StrmTask{r.task}, nil
}
func (r *rematchTaskRepo) ListByAccount(context.Context, int64) ([]*domain.StrmTask, error) {
	return []*domain.StrmTask{r.task}, nil
}
func (r *rematchTaskRepo) UpdateScan(context.Context, int64, domain.StrmScanPatch) error {
	return nil
}

func TestConfirmExistingMatchClearsDoubtWhenMetadataComplete(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "海贼王 (1999)")
	season := filepath.Join(show, "Season 01")
	mustMkdir(t, season)
	mustWrite(t, filepath.Join(season, "海贼王.S01E01.strm"), "x")
	mustWrite(t, filepath.Join(season, "海贼王.S01E01.nfo"), "<episodedetails></episodedetails>\n")
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>航海王</title><year>1999</year><tmdbid>37854</tmdbid></tvshow>\n")
	mustWrite(t, filepath.Join(show, "poster.jpg"), "image")

	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	g := works[0]
	if err := writePendingState(g, scrapeState{Status: PendingDoubt, EpLocal: 1, EpTMDB: 1}); err != nil {
		t.Fatal(err)
	}
	if !workTMDBIDMatches(g, MediaTypeTV, "37854") {
		t.Fatal("相同 TMDB ID 未被识别")
	}

	confirmExistingMatch(g, MediaTypeTV)
	if _, ok := readPendingState(g); ok {
		t.Fatal("确认相同匹配后应清除存疑状态")
	}
}

func TestMarkNormalAllowsUnmatchedWorkAndRescrapeRestoresIt(t *testing.T) {
	strmRoot := t.TempDir()
	outputFolder := "本地短剧"
	root := strm.TaskOutputDir(strmRoot, outputFolder)
	show := filepath.Join(root, "自制短剧")
	mustMkdir(t, show)
	mustWrite(t, filepath.Join(show, "S01E01.strm"), "x")
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	task := &domain.StrmTask{ID: 9, OutputFolder: outputFolder}
	strmSvc := strm.NewService(strm.ServiceOptions{
		Repo:    &rematchTaskRepo{task: task},
		StrmDir: strmRoot,
	})
	svc := New(Options{Strm: strmSvc, StrmDir: strmRoot, DataDir: t.TempDir()})

	item, err := svc.MarkNormal(context.Background(), MarkNormalRequest{
		StrmTaskID: 9,
		ItemID:     pathToItemID(works[0].relKey),
		MediaType:  MediaTypeTV,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !item.ManualDone || item.Status != ItemStatusOK || item.TMDBID != "" {
		t.Fatalf("未匹配作品未进入手动完成：%+v", item)
	}

	item, started, err := svc.Rescrape(context.Background(), RescrapeRequest{
		StrmTaskID: 9,
		ItemID:     pathToItemID(works[0].relKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	if started || item.ManualDone || item.Status != ItemStatusMiss {
		t.Fatalf("重新刮削应先恢复为待刮削：started=%v item=%+v", started, item)
	}
}

func TestClearMatchRemovesMetadataByType(t *testing.T) {
	strmRoot := t.TempDir()
	outputFolder := "错误匹配"
	root := strm.TaskOutputDir(strmRoot, outputFolder)
	show := filepath.Join(root, "自制作品")
	mustMkdir(t, show)
	strmPath := filepath.Join(show, "S01E01.strm")
	ownedNFO := filepath.Join(show, "tvshow.nfo")
	ownedPoster := filepath.Join(show, "poster.jpg")
	userPoster := filepath.Join(show, "fanart.jpg")
	subtitle := filepath.Join(show, "S01E01.ass")
	for path, body := range map[string]string{
		strmPath:    "x",
		ownedNFO:    "<tvshow><title>错误作品</title><tmdbid>123</tmdbid></tvshow>\n",
		ownedPoster: "wrong",
		userPoster:  "keep",
		subtitle:    "Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,字幕",
	} {
		mustWrite(t, path, body)
	}
	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	g := works[0]
	if g.flatFile != "" || g.absDir != show {
		t.Fatalf("workGroup 定位错误：%+v", g)
	}
	task := &domain.StrmTask{ID: 10, OutputFolder: outputFolder}
	strmSvc := strm.NewService(strm.ServiceOptions{Repo: &rematchTaskRepo{task: task}, StrmDir: strmRoot})
	svc := New(Options{Strm: strmSvc, StrmDir: strmRoot, DataDir: t.TempDir()})

	item, err := svc.MarkNormal(context.Background(), MarkNormalRequest{
		StrmTaskID: 10,
		ItemID:     pathToItemID(g.relKey),
		MediaType:  MediaTypeTV,
		ClearMatch: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !item.ManualDone || item.TMDBID != "" || item.Title != "自制作品" {
		t.Fatalf("错误匹配未正确取消：%+v", item)
	}
	for _, path := range []string{ownedNFO, ownedPoster, userPoster} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("元数据未清理：%s", path)
		}
	}
	for _, path := range []string{strmPath, subtitle} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("非元数据不应删除：%s: %v", path, err)
		}
	}
}

func TestConfirmExistingMatchKeepsUpdatingState(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "追更剧 (2026)")
	season := filepath.Join(show, "Season 01")
	mustMkdir(t, season)
	mustWrite(t, filepath.Join(season, "追更剧.S01E01.strm"), "x")
	mustWrite(t, filepath.Join(season, "追更剧.S01E01.nfo"), "<episodedetails></episodedetails>\n")
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>追更剧</title><tmdbid>1</tmdbid></tvshow>\n")
	mustWrite(t, filepath.Join(show, "poster.jpg"), "image")

	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	g := works[0]
	if err := writePendingState(g, scrapeState{Status: PendingUpdating, EpLocal: 1, EpTMDB: 2}); err != nil {
		t.Fatal(err)
	}
	confirmExistingMatch(g, MediaTypeTV)
	pending, ok := readPendingState(g)
	if !ok || pending.Status != PendingUpdating {
		t.Fatalf("确认匹配不能误清除追更状态，实际=%+v", pending)
	}
}

func TestMarkNormalConfirmsDoubtAndKeepsUpdatingState(t *testing.T) {
	strmRoot := t.TempDir()
	outputFolder := "123剧集"
	root := strm.TaskOutputDir(strmRoot, outputFolder)
	show := filepath.Join(root, "追更剧 (2026)")
	season := filepath.Join(show, "Season 01")
	mustMkdir(t, season)
	mustWrite(t, filepath.Join(season, "追更剧.S01E01.strm"), "x")
	mustWrite(t, filepath.Join(season, "追更剧.S01E01.nfo"), "<episodedetails></episodedetails>\n")
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>追更剧</title><tmdbid>1</tmdbid></tvshow>\n")
	mustWrite(t, filepath.Join(show, "poster.jpg"), "image")

	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	g := works[0]
	if err := writePendingState(g, scrapeState{Status: PendingDoubt, EpLocal: 1, EpTMDB: 2}); err != nil {
		t.Fatal(err)
	}
	task := &domain.StrmTask{ID: 8, OutputFolder: outputFolder}
	strmSvc := strm.NewService(strm.ServiceOptions{
		Repo:    &rematchTaskRepo{task: task},
		StrmDir: strmRoot,
	})
	svc := New(Options{Strm: strmSvc, StrmDir: strmRoot, DataDir: t.TempDir()})

	item, err := svc.MarkNormal(context.Background(), MarkNormalRequest{
		StrmTaskID: 8,
		ItemID:     pathToItemID(g.relKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != ItemStatusOK || item.TVState != TVStateUpdating || !item.HasPending {
		t.Fatalf("确认存疑后应恢复正常但保持追更，实际=%+v", item)
	}
}

func TestRematchSameCompleteIDRequiresTMDBClient(t *testing.T) {
	strmRoot := t.TempDir()
	outputFolder := "123电影"
	root := strm.TaskOutputDir(strmRoot, outputFolder)
	show := filepath.Join(root, "动漫剧", "海贼王 (1999)")
	season := filepath.Join(show, "Season 01")
	mustMkdir(t, season)
	mustWrite(t, filepath.Join(season, "海贼王.S01E01.strm"), "x")
	mustWrite(t, filepath.Join(season, "海贼王.S01E01.nfo"), "<episodedetails></episodedetails>\n")
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>航海王</title><year>1999</year><tmdbid>37854</tmdbid></tvshow>\n")
	mustWrite(t, filepath.Join(show, "poster.jpg"), "image")

	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	g := works[0]
	if err := writePendingState(g, scrapeState{Status: PendingDoubt, EpLocal: 1, EpTMDB: 1}); err != nil {
		t.Fatal(err)
	}
	task := &domain.StrmTask{ID: 7, OutputFolder: outputFolder}
	strmSvc := strm.NewService(strm.ServiceOptions{
		Repo:    &rematchTaskRepo{task: task},
		StrmDir: strmRoot,
	})
	svc := New(Options{
		Strm:    strmSvc,
		StrmDir: strmRoot,
		DataDir: t.TempDir(),
	})

	_, _, err = svc.Rematch(context.Background(), RematchRequest{
		StrmTaskID: 7,
		ItemID:     pathToItemID(g.relKey),
		TMDBID:     "37854",
		MediaType:  MediaTypeTV,
		Title:      "航海王",
	})
	if err == nil {
		t.Fatal("重新匹配复用开始刮削路径，未配置 TMDB 时应失败")
	}
}

func TestOverwriteForMatchFollowsWriteMode(t *testing.T) {
	svc := New(Options{})
	if !svc.overwriteForMatch(false) {
		t.Fatal("换 TMDB ID 应强制覆盖")
	}
	if svc.overwriteForMatch(true) {
		t.Fatal("同 ID 且默认写入策略应为仅补缺")
	}
}

func TestRescrapeRequiresTMDBClient(t *testing.T) {
	strmRoot := t.TempDir()
	outputFolder := "123完结"
	root := strm.TaskOutputDir(strmRoot, outputFolder)
	show := filepath.Join(root, "海贼王 (1999)")
	season := filepath.Join(show, "Season 01")
	mustMkdir(t, season)
	mustWrite(t, filepath.Join(season, "海贼王.S01E01.strm"), "x")
	mustWrite(t, filepath.Join(season, "海贼王.S01E01.nfo"), "<episodedetails></episodedetails>\n")
	mustWrite(t, filepath.Join(show, "tvshow.nfo"), "<tvshow><title>航海王</title><year>1999</year><tmdbid>37854</tmdbid></tvshow>\n")
	mustWrite(t, filepath.Join(show, "poster.jpg"), "image")

	works, err := scanWorks(root)
	if err != nil {
		t.Fatal(err)
	}
	g := works[0]
	task := &domain.StrmTask{ID: 9, OutputFolder: outputFolder}
	strmSvc := strm.NewService(strm.ServiceOptions{
		Repo:    &rematchTaskRepo{task: task},
		StrmDir: strmRoot,
	})
	svc := New(Options{Strm: strmSvc, StrmDir: strmRoot, DataDir: t.TempDir()})

	_, _, err = svc.Rescrape(context.Background(), RescrapeRequest{
		StrmTaskID: 9,
		ItemID:     pathToItemID(g.relKey),
	})
	if err == nil {
		t.Fatal("重刮复用开始刮削路径，未配置 TMDB 时应失败")
	}
}
