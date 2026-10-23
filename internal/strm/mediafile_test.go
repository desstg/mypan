package strm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 「一个作品的载体」的判据（见 mediafile.go）。
//
// 这一组盯的是**纯视频库**（海报墙挂上来的「本地目录」绝大多数是这种）：
// 那里一个 `.strm` 都没有，作品载体就是磁盘上那个 `.mp4` / `.mkv`。
// 判据错的表现全是**静默**的 —— 墙上一部片都列不出来、刮削一部都扫不到、
// 或者补图一部都没写，而且都不报错。

func TestIsMediaFileName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"NIMA-086-U.strm", true},
		{"NIMA-086-U.STRM", true}, // Windows 上大小写不定
		{"NIMA-086-U.mp4", true},
		{"NIMA-086-U.MKV", true},
		{"NIMA-086-U.m2ts", true},
		{"NIMA-086-U.rmvb", true},
		{"NIMA-086-U.nfo", false},
		{"NIMA-086-U.json", false},
		{"poster.jpg", false},
		{"NIMA-086-U.zh-CN.srt", false},
		{"NIMA-086-U", false}, // 没有扩展名
		{"", false},
	}
	for _, c := range cases {
		if got := IsMediaFileName(c.name); got != c.want {
			t.Errorf("IsMediaFileName(%q) = %v，期望 %v", c.name, got, c.want)
		}
	}
	// 两种载体不能混：`.strm` 是「读正文」，视频是「服务端发文件」，
	// 分不清就会拿视频当文本去 ReadFile。
	if IsVideoFileName("A.strm") || IsVideoFileName("A.mp4") == false {
		t.Fatal("IsVideoFileName 把两类载体判混了")
	}
	if !IsStrmFileName("A.strm") || IsStrmFileName("A.mp4") {
		t.Fatal("IsStrmFileName 把两类载体判混了")
	}
}

// TestPickMediaNamesCollapsesSameStem 同一主干的两种载体只算一部，且 `.strm` 优先。
func TestPickMediaNamesCollapsesSameStem(t *testing.T) {
	got := PickMediaNames([]string{
		"NIMA-086-U.mp4", "NIMA-086-U.strm", "NIMA-086-U.nfo", "poster.jpg",
		"LY014-cd1.mkv", "LY014-cd2.mkv", "LY014.json",
	})
	want := []string{"LY014-cd1.mkv", "LY014-cd2.mkv", "NIMA-086-U.strm"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("PickMediaNames = %v，期望 %v", got, want)
	}

	// 主干比较忽略大小写；返回的名字**保持磁盘原样**（大小写不能动，
	// 否则 Linux 部署上产物名与视频名配不上对，而且是静默的）。
	got = PickMediaNames([]string{"nima-086-u.MP4", "NIMA-086-U.strm"})
	if len(got) != 1 || got[0] != "NIMA-086-U.strm" {
		t.Fatalf("同主干忽略了大小写后应收成一个 .strm，得到 %v", got)
	}
	// 只有视频时保留磁盘上那一份的名字（含大写后缀）。
	got = PickMediaNames([]string{"Abc-001.MKV", "Abc-001.mp4"})
	if len(got) != 1 || got[0] != "Abc-001.MKV" {
		t.Fatalf("同一份视频的两种扩展名之间应取字典序最小的那个，得到 %v", got)
	}
	// 空目录 / 全是侧车：一个都不算。
	if got := PickMediaNames([]string{"poster.jpg", "x.nfo"}); len(got) != 0 {
		t.Fatalf("非媒体文件不该被当成作品，得到 %v", got)
	}
}

// TestPickMediaNamesOfEntriesSkipsDirs 子目录不进候选（影片目录本身不是作品）。
func TestPickMediaNamesOfEntriesSkipsDirs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "extrafanart"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "NIMA-086-U.mp4"), []byte("v"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	got := PickMediaNamesOfEntries(entries)
	if len(got) != 1 || got[0] != "NIMA-086-U.mp4" {
		t.Fatalf("PickMediaNamesOfEntries = %v，期望只有那个 mp4", got)
	}
}

// TestMediaNameForStem 手上只有主干时，要能找回磁盘上真正的载体名字。
//
// 这个函数存在的理由就是「别处别再拼 `stem+".strm"`」：纯视频库里那个拼法
// 指向一个不存在的文件，而下游（FindLocalSidecar）遇到不存在的名字只会静默
// 返回「没有侧车」。
func TestMediaNameForStem(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"NIMA-086-U.mp4", "LY014-cd1.strm", "poster.jpg"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := mediaNameForStem(root, "nima-086-u"); got != "NIMA-086-U.mp4" {
		t.Errorf("纯视频库应找回 mp4，得到 %q", got)
	}
	if got := mediaNameForStem(root, "LY014-cd1"); got != "LY014-cd1.strm" {
		t.Errorf("应找回 .strm，得到 %q", got)
	}
	if got := mediaNameForStem(root, "NOPE-001"); got != "" {
		t.Errorf("不存在的应返回空串，得到 %q", got)
	}
	if got := mediaNameForStem(root, ""); got != "" {
		t.Errorf("空主干应返回空串，得到 %q", got)
	}
}

// TestJavArtifactNamesPureVideoLibrary 纯视频库里的守卫名单必须非空。
//
// 原来 `javArtifactNames` 只数 `.strm`，纯视频库里得到 0 个主干 → 返回零值守卫
// → 这个程序写的 nfo / 海报 / 字幕被 `cloud_primary` **每轮删一次**、
// 再由生成器每轮写一次。静默且昂贵，所以这条要钉住。
func TestJavArtifactNamesPureVideoLibrary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SSIS-001.mp4"), []byte("v"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	guard := javArtifactNames(entries)
	for _, want := range []string{"ssis-001.nfo", "poster.jpg", "ssis-001-poster.jpg", "ssis-001.zh-CN.srt"} {
		if !guard.isOurs(want) {
			t.Errorf("纯视频库的守卫该认下 %s", want)
		}
	}
	// 别人的东西仍然不能放过。
	for _, want := range []string{"moil-002.zh-CN.srt", "x.json"} {
		if guard.isOurs(want) {
			t.Errorf("守卫不该认下 %s", want)
		}
	}
}

// TestCountMediaCountsVideos 平铺/独占的判据数的是**载体数**，不是 `.strm` 数。
func TestCountMediaCountsVideos(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"A.mp4", "B.mkv", "A.strm", "poster.jpg"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	// A.mp4 与 A.strm 同主干 → 一部；B.mkv → 一部。共 2。
	if got := countMedia(entries); got != 2 {
		t.Fatalf("countMedia = %d，期望 2（纯视频 + .strm 都要数，同主干只算一部）", got)
	}
}

// TestSyncLocalSidecarSummaryPureVideoLibrary 纯视频库里补简介要能落到侧车上。
//
// 这条是「静默降级」的典型：以前这里硬拼 `stem+".strm"`，纯视频库里那个名字
// 不存在，`FindLocalSidecar` 配对不上就返回 false，而调用方把 false 当成
// 「本地还没有那份 json」—— 于是简介永远补不进去，且没有任何日志。
func TestSyncLocalSidecarSummaryPureVideoLibrary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SSIS-001-UC.mp4"), []byte("v"), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := map[string]any{"schema": "litepan.jav.sidecar/1", "number": "SSIS-001", "summary": ""}
	body, err := json.Marshal(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SSIS-001-UC.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := SyncLocalSidecarSummary(root, "", "SSIS-001-UC", "新补的剧情简介")
	if err != nil {
		t.Fatalf("SyncLocalSidecarSummary: %v", err)
	}
	if !changed {
		t.Fatal("纯视频库里补简介也必须真的写进侧车（拼 stem+\".strm\" 会静默找不到）")
	}
	raw, err := os.ReadFile(filepath.Join(root, "SSIS-001-UC.json"))
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back["summary"] != "新补的剧情简介" {
		t.Fatalf("侧车里的 summary = %v", back["summary"])
	}
	// 再写一次同样的内容：不该认为是「改了」（免得白刷 mtime）。
	if again, err := SyncLocalSidecarSummary(root, "", "SSIS-001-UC", "新补的剧情简介"); err != nil || again {
		t.Fatalf("重复写同一份内容应返回 false，得到 changed=%v err=%v", again, err)
	}
}
