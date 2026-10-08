package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"litepan/internal/domain"
)

// fakeFileInfo 是 FileInfoLookup 的测试替身：按 file_id 返回预先写好的文件信息。
type fakeFileInfo struct {
	got     *domain.FileItem
	err     error
	account int64
	fileID  string
	calls   int
}

func (f *fakeFileInfo) Info(_ context.Context, accountID int64, fileID string) (*domain.FileItem, error) {
	f.calls++
	f.account, f.fileID = accountID, fileID
	return f.got, f.err
}

// TestWorkWallSourceFromSTRM 钉住 TMDB 那面的「源媒体信息」。
//
// 番号那面的大小来自侧车的 dest.files；TMDB 这面没有侧车，只能从 `.strm` 正文里解出
// `(account_id, file_id)` 再问一次网盘。这里钉住三件事：
//  1. 真的去查了，且用的是 `.strm` 里那对 id；
//  2. **剧集的路径要对** —— 剧集的 `.strm` 在 `Season 01/` 子目录里，按作品根拼路径
//     会读不到文件（表现是「只有文件名，没有大小」）；
//  3. 查不到时不报错、退化成只有文件名与路径。
func TestWorkWallSourceFromSTRM(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "电视剧", "某剧 (2026) {tmdb-1}")
	season := filepath.Join(show, "Season 01")
	mustMkdir(t, season)
	strmPath := filepath.Join(season, "某剧.S01E01.strm")
	// 一个能被 proxybase.ParseLitePanSTRMURL 认出来的地址。
	mustWrite(t, strmPath, "http://host/api/strm/play/7/MzUzMDAxNTYxOTgxMjQyNjgzNw/t/tok/n/a.mkv")
	works, err := scanWorks(root)
	if err != nil || len(works) != 1 {
		t.Fatalf("scanWorks: %v, %d", err, len(works))
	}
	g := works[0]

	fake := &fakeFileInfo{got: &domain.FileItem{Name: "某剧.S01E01.mkv", Size: 5 << 30}}
	svc := &Service{files: fake}

	src := svc.workWallSource(context.Background(), root, g)
	if src == nil {
		t.Fatal("拿不到源媒体信息")
	}
	if src.FileName != "某剧.S01E01.strm" {
		t.Fatalf("文件名 = %q", src.FileName)
	}
	if fake.calls != 1 || fake.account != 7 {
		t.Fatalf("没按 .strm 里的账号去查：calls=%d account=%d", fake.calls, fake.account)
	}
	if src.Size != 5<<30 || src.SizeText == "" {
		t.Fatalf("源文件大小 = %d / %q（应从网盘查到）", src.Size, src.SizeText)
	}
	if src.Ext != "mkv" {
		t.Fatalf("类型 = %q（取源文件名的扩展名）", src.Ext)
	}
	// 剧集那条路径必须是 Season 子目录里的那一个。
	if filepath.Base(src.Path) != "某剧.S01E01.strm" {
		t.Fatalf("路径 = %q", src.Path)
	}
}

// TestWorkWallSourceDegradesGracefully 钉住「查不到就退化，不报错」。
func TestWorkWallSourceDegradesGracefully(t *testing.T) {
	root := t.TempDir()
	movie := filepath.Join(root, "电影", "某片 (2026) {tmdb-2}")
	mustMkdir(t, movie)
	mustWrite(t, filepath.Join(movie, "某片.strm"), "http://host/api/strm/play/7/MzUzMDAxNTYxOTgxMjQyNjgzNw/t/tok/n/a.mkv")
	works, err := scanWorks(root)
	if err != nil || len(works) != 1 {
		t.Fatalf("scanWorks: %v, %d", err, len(works))
	}

	// ① 没有注入 FileInfo（测试 / 未装配）—— 只有文件名与路径。
	svc := &Service{}
	src := svc.workWallSource(context.Background(), root, works[0])
	if src == nil || src.FileName != "某片.strm" || src.Size != 0 || src.SizeText != "" {
		t.Fatalf("无 FileInfo 时 = %+v", src)
	}

	// ② 网盘查询失败 —— 同上，不报错。
	svc = &Service{files: &fakeFileInfo{err: os.ErrDeadlineExceeded}}
	src = svc.workWallSource(context.Background(), root, works[0])
	if src == nil || src.Size != 0 {
		t.Fatalf("查询失败时 = %+v", src)
	}

	// ③ `.strm` 正文不是本程序的播放地址 —— 不去查网盘。
	fake := &fakeFileInfo{}
	mustWrite(t, filepath.Join(movie, "某片.strm"), "https://other.example.com/whatever.mkv")
	svc = &Service{files: fake}
	src = svc.workWallSource(context.Background(), root, works[0])
	if src == nil || fake.calls != 0 {
		t.Fatalf("非本程序地址不该去查网盘：calls=%d src=%+v", fake.calls, src)
	}
}
