package strmscrape

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"testing"

	"litepan/internal/settings"
)

// 海报墙详情抽屉里那颗「播放」键：**本地目录源（TMDB 档）的单文件作品**必须有
// `playable`，否则抽屉里什么都不显示（用户报的就是这个）。
//
// 为什么单列一条：`TestLocalTmdbListItemsByRef` 只钉到「列得出来」，而列表用的是
// `buildItem`（只读 nfo/图），**详情**走的是 `TMDBWallDetail` → `findWorkByID` →
// `playableForWork`。后半段一旦按 `g.absDir` 找 `.strm`，单文件作品的
// `entries`/`flatFile` 任一为空就会静默给出空 playable。
func TestLocalTmdbDetailHasPlayableForFlatEntry(t *testing.T) {
	playURL := litePanPlayURL(t, "护肝人 (2026)", "3536705890302298065")
	detail := localTmdbDetailForWork(t, "护肝人 (2026)", playURL)
	if len(detail.Playable) != 1 {
		t.Fatalf("单文件作品应有 1 条 playable，实际 %d（抽屉里就看不到播放键）：%+v",
			len(detail.Playable), detail.Playable)
	}
	if detail.Playable[0].Path != playURL {
		t.Fatalf("playable.path 应原样等于 .strm 正文：\n got=%q\nwant=%q",
			detail.Playable[0].Path, playURL)
	}
}

// 用户报的「tmdb 本地目录点开详情没有播放键」的**真身**：本地目录里的 `.strm`
// 正文不是本应用的播放地址，而是**别的工具产的直链**（实测 `X:\Strm\mypan\影片2`
// 是 `http://192.168.31.4:9527/d/<id>?/名字.mp4`，OpenList 那种）。
//
// 这条必须钉住：本地目录这条路当初把 `ParseLitePanSTRMURL` 当成了唯一的
// 「能不能播」判据，于是整柜作品都被 `strm wall skip non-litepan play url` 静默丢掉，
// 表现就是详情抽屉里连播放键都不出现 —— 而**任务**那面必须继续卡格式（见下一条）。
func TestLocalTmdbDetailKeepsForeignPlayURL(t *testing.T) {
	const foreign = "http://192.168.31.4:9527/d/e5iamyeov28kvzw45.mp4?/爱情有烟火.2026.S01E01.2160p.WEB-DL.HDR10.H265.AAC.mp4"
	detail := localTmdbDetailForWork(t, "爱情有烟火 (2026)", foreign)
	if len(detail.Playable) != 1 {
		t.Fatalf("本地目录的「外站直链」也应给出 1 条 playable（这正是用户报的那条），实际 %d：%+v",
			len(detail.Playable), detail.Playable)
	}
	if detail.Playable[0].Path != foreign {
		t.Fatalf("外站直链应原样返回、不做任何改写：\n got=%q\nwant=%q",
			detail.Playable[0].Path, foreign)
	}
}

// 反面钉子：**任务**来源必须继续按本应用的地址格式卡。
//
// 「不影响现有功能」这条硬约束就落在这一条上 —— 一旦哪天有人图省事把
// `readPlayableFiles` 里的校验整个删掉，任务那面就会把用户手改坏的 `.strm`
// 也当成能播，变成一个更难查的问题。
func TestTaskDetailStillRejectsNonLitePanPlayURL(t *testing.T) {
	const foreign = "http://192.168.31.4:9527/d/e5iamyeov28kvzw45.mp4?/x.mp4"
	const name = workName
	if got := playableCountForSource(t, false, name, foreign); got != 0 {
		t.Fatalf("任务来源的正文不是 /api/strm/play/... 时必须丢掉，实际留下 %d 条", got)
	}
	if got := playableCountForSource(t, true, name, foreign); got != 1 {
		t.Fatalf("同一份正文在本地目录来源下应留下 1 条，实际 %d 条", got)
	}
	// 反过来：合法地址两档都要留下，证明分档只放开了「格式」这一层。
	okURL := litePanPlayURL(t, name, "3536705890302298065")
	if got := playableCountForSource(t, false, name, okURL); got != 1 {
		t.Fatalf("任务来源的合法地址应留下 1 条，实际 %d 条", got)
	}
	if got := playableCountForSource(t, true, name, okURL); got != 1 {
		t.Fatalf("本地目录来源的合法地址应留下 1 条，实际 %d 条", got)
	}
}

// workName 是这几条测试共用的作品名（避免两处写得不一致让 fixture 对不上）。
const workName = "护肝人 (2026)"

// litePanPlayURL 拼一条**真格式**的本应用播放地址：`ParseLitePanSTRMURL` 是任务那档
// 唯一的判据，正文不合法时会被静默 logWarn 丢掉（表现就是「没有播放键」）。
func litePanPlayURL(t *testing.T, work, fileID string) string {
	t.Helper()
	key := base64.RawURLEncoding.EncodeToString([]byte(fileID))
	return "http://192.168.31.4:5311/api/strm/play/1/" + key + "/t/lpk_test/n/" + work + ".mp4"
}

// localTmdbDetailForWork 用真的 `.strm` 文件铺一个 tmdb 本地目录，再走一遍「列表 → 详情」。
// 抽出来是因为下面几条测试除了正文之外完全同形。
func localTmdbDetailForWork(t *testing.T, work, strmBody string) *WallDetail {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, work)
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, work+".strm"), strmBody)
	mustWrite(t, filepath.Join(dir, work+".nfo"), "<movie><title>"+work+"</title></movie>")
	mustWrite(t, filepath.Join(dir, "poster.jpg"), "x")

	svc, id := newLocalTmdbService(t, root)
	ctx := context.Background()
	list, err := svc.ListItemsByRef(ctx, WallSourceRef{LocalDirID: id}, ItemListQuery{})
	if err != nil {
		t.Fatalf("ListItemsByRef: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("应列出 1 项，实际 %d", len(list.Items))
	}
	detail, err := svc.TMDBWallDetail(ctx, WallSourceRef{LocalDirID: id}, list.Items[0].ID)
	if err != nil {
		t.Fatalf("TMDBWallDetail: %v", err)
	}
	return detail
}

// newLocalTmdbService 铺一个只挂着一个 tmdb 本地目录的 Service（这块 fixture 上面几条共用）。
func newLocalTmdbService(t *testing.T, root string) (*Service, string) {
	t.Helper()
	const id = "ffeeddccbbaa0099"
	svc := New(Options{
		StrmDir: t.TempDir(),
		DataDir: t.TempDir(),
		Settings: newWallLocalDirsSettings(t, map[string]string{
			settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs([]settings.WallLocalDir{{
				ID: id, Name: "本地电影", Root: root, MediaKind: settings.WallLocalKindTmdb,
			}}),
		}),
	})
	return svc, id
}

// playableCountForSource 只测**分档的那一层**（不铺整棵作品树）：
// `strict=true` 走任务那档、`false` 走本地目录那档。
func playableCountForSource(t *testing.T, local bool, work, strmBody string) int {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, work+".strm"), strmBody)

	var ref posterRef
	if local {
		ref = posterRef{LocalDirID: "ffeeddccbbaa0099"}
	} else {
		ref = posterRef{TaskID: 7}
	}
	svc := New(Options{StrmDir: t.TempDir(), DataDir: t.TempDir()})
	if local {
		count := len(svc.readPlayableFilesAny(ref, dir, "", []string{work + ".strm"}))
		return count
	}
	return len(svc.readPlayableFiles(ref, dir, "", []string{work + ".strm"}))
}
