package strmscrape

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/jav"
	"litepan/internal/jav/emby"
)

// 2026-10-31 真机的整条链（不是只测 `ingestByNumber`，从**文件名**开始）：
//
//	群晖上 `MIAE-0071_LTX.strm`，没有侧车 json、没有 nfo，点「开始刮削」一张卡都刮不到；
//	把名字改成 `MIAE-071` 立刻就好。
//
// 这条用例把用户报告的那个现场原样铺出来：只有 `.strm`，上游只有 3 位的 `MIAE-071`。
//
// 它钉两件事：
//   - 取番号这一步**不替用户改名**：从 `MIAE-0071_LTX` 拆出来的仍是 `MIAE-0071`
//     （磁盘上的写法原样保留，只在比较时把补出来的零当空气）；
//   - 结论：这条链现在能命中上游 `MIAE-071`，并且真的落了一份侧车。
func TestScrapeOneJavToleratesPaddedNumber(t *testing.T) {
	m := upstreamMovie("qpzM", "MIAE-071")
	f := newReturnsFixture(t, m, m)

	dir := t.TempDir()
	// 现场与真机一致：**只有** .strm，没有 json、没有 nfo。
	mustWrite(t, filepath.Join(dir, "MIAE-0071_LTX.strm"), "http://127.0.0.1/x")

	stem := "MIAE-0071_LTX"
	files := map[string]os.DirEntry{}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			files[e.Name()] = e
		}
	}
	if number := javScrapeNumber(dir, files, emby.TargetNames(stem, false), stem, nil, nil); number != "MIAE-0071" {
		t.Fatalf("拆出来的番号该是 MIAE-0071（补出来的零保留，比较时才忽略），got %q", number)
	}

	scraped, notFound, failed := f.svc.scrapeOneJav(
		context.Background(), testJavTarget(dir, stem, "MIAE-0071"),
		jav.RouteInput{Number: "MIAE-0071"})

	if failed {
		t.Fatalf("写盘不该失败")
	}
	if notFound {
		t.Errorf("上游明明有 MIAE-071（就是补零形态的同一部），不该报 not_found")
	}
	if !scraped {
		t.Fatalf("应当刮到东西")
	}
	// 侧车落在**原主干**上（用户没改名，我们也不改名）。
	js := readTestFile(t, filepath.Join(dir, stem+".json"))
	if !strings.Contains(js, `"number": "MIAE-0071"`) {
		t.Errorf("侧车的 number 该是磁盘上的写法 MIAE-0071：\n%s", js)
	}
	if !strings.Contains(js, `"ltx": true`) {
		t.Errorf("`_LTX` 主干该补出 resource.ltx：\n%s", js)
	}
	// nfo **不在这里核**：新建侧车那条路只补「已存在的 nfo」
	// （`fillNFOFieldsIfMissingAt` 读不到就返回，等生成那一步自己带进去），
	// 而生成器是刮削收尾的 `FillJavArtifactsInRoot`，这个用例不跑它。
	// 现场本来也**只有** `.strm`（真机就是这样）。
}
