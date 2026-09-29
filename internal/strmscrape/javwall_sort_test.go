package strmscrape

import (
	"testing"

	"litepan/internal/jav/emby"
)

// 排序键必须**先填好**再排 —— 这条钉的是一个真实踩过的坑。
//
// 背景：`ReleaseDate` / `AddedAt` 是建 nfo 时写进去的（生成器把侧车里的 release_date
// 与 dest.added_at 写进 `<premiered>` / `<dateadded>`），而列表这条路一度只解
// `<title>`/`<num>`，两个日期恒为零值 → 比较恒 false → 稳定排序原序返回 →
// **三种日期排序点下去毫无反应，且不报错**。
//
// 这里直接喂 javTitleLookup 的桩，不碰磁盘。
func TestSortJavWallRowsByDates(t *testing.T) {
	mk := func(stem, release, added string) javWallRow {
		return javWallRow{item: JavWallItem{Stem: stem, Number: stem, ReleaseDate: release, AddedAt: added}}
	}
	rows := func() []javWallRow {
		return []javWallRow{
			mk("A", "2024-03-15", "2026-09-20 10:00:00"),
			mk("B", "2025-01-01", "2026-09-22 10:00:00"),
			mk("C", "", "2026-09-21 10:00:00"), // 没有发行日期
		}
	}
	order := func(rs []javWallRow) string {
		out := ""
		for _, r := range rs {
			out += r.item.Stem
		}
		return out
	}

	// 发行日期（新→旧）：C 没有日期 → 排最后
	rs := rows()
	sortJavWallRows(rs, "release_desc")
	if got := order(rs); got != "BAC" {
		t.Errorf("release_desc = %s，期望 BAC（无日期的 C 排最后）", got)
	}

	// 添加时间（新→旧）
	rs = rows()
	sortJavWallRows(rs, "added_desc")
	if got := order(rs); got != "BCA" {
		t.Errorf("added_desc = %s，期望 BCA", got)
	}

	// 添加时间（旧→新）—— 必须与 added_desc **真的相反**（旧实现里两者完全相同）
	rs = rows()
	sortJavWallRows(rs, "added_asc")
	if got := order(rs); got != "ACB" {
		t.Errorf("added_asc = %s，期望 ACB", got)
	}

	// 番号两档仍然照旧
	rs = rows()
	sortJavWallRows(rs, "number_asc")
	if got := order(rs); got != "ABC" {
		t.Errorf("number_asc = %s，期望 ABC", got)
	}
	rs = rows()
	sortJavWallRows(rs, "number_desc")
	if got := order(rs); got != "CBA" {
		t.Errorf("number_desc = %s，期望 CBA", got)
	}

	// 空串与未知值都按「添加时间（新→旧）」兜底（用户定的默认）
	for _, key := range []string{"", "unknown_key"} {
		rs = rows()
		sortJavWallRows(rs, key)
		if got := order(rs); got != "BCA" {
			t.Errorf("sort=%q 应当兜底成 added_desc（BCA），got %s", key, got)
		}
	}
}

// 日期全空时**保持原序**（稳定排序）：不参与定序，而不是按空串乱排。
func TestSortJavWallRowsAllDatesEmptyKeepsOrder(t *testing.T) {
	rows := []javWallRow{
		{item: JavWallItem{Stem: "A"}},
		{item: JavWallItem{Stem: "B"}},
		{item: JavWallItem{Stem: "C"}},
	}
	sortJavWallRows(rows, "added_desc")
	if rows[0].item.Stem != "A" || rows[1].item.Stem != "B" || rows[2].item.Stem != "C" {
		t.Errorf("日期全空时该保持原序，got %s%s%s", rows[0].item.Stem, rows[1].item.Stem, rows[2].item.Stem)
	}
}

// fillJavWallSortKeys 把 nfo 里的两个日期填进行上 —— 少了它排序就是死的。
func TestFillJavWallSortKeys(t *testing.T) {
	rows := []javWallRow{
		{item: JavWallItem{Stem: "A"}},
		{item: JavWallItem{Stem: "B"}},
	}
	titles := func(row javWallRow) emby.JavNFOInfo {
		return emby.JavNFOInfo{
			Title:   "T-" + row.item.Stem,
			Number:  row.item.Stem,
			Release: "2024-0" + row.item.Stem + "-01",
			AddedAt: "2026-09-2" + row.item.Stem + " 10:00:00",
		}
	}
	fillJavWallSortKeys(rows, titles)
	if rows[0].item.ReleaseDate != "2024-0A-01" || rows[1].item.AddedAt != "2026-09-2B 10:00:00" {
		t.Errorf("日期没被填进去：%+v / %+v", rows[0].item, rows[1].item)
	}
}

// 默认（空 sort）在**端到端**那条路上也成立：listJavWall 走一遍，第一张应当是
// 添加时间最新的那一部。
func TestListJavWallDefaultsToAddedDesc(t *testing.T) {
	root := t.TempDir()
	writeJavLibrary(t, root)
	snap := javWallSnapshotForTest(t, root, []string{"未匹配"})

	// 用真实 nfo 读取器（读磁盘上那几份），这样测的是「日期真的从 nfo 出来了」。
	// 缓存 map 要自己建 —— 裸 &Service{} 的 map 是 nil，写它会 panic（生产走 New()）。
	s := &Service{javTitleCache: map[string]javTitleEntry{}}
	titles := s.newJavTitleLookup(snap.rows)
	out := listJavWall(snap, JavWallListQuery{}, titles)
	if len(out.Items) == 0 {
		t.Fatal("墙上应当有卡片")
	}
	// NIMA-086 的 nfo 带 dateadded（样本侧车里有 dest.added_at），
	// 另外几张 nfo 里没有 dateadded → 排最后。所以第一张应当是 NIMA-086。
	if out.Items[0].Number != "NIMA-086" {
		t.Errorf("默认排序该是「添加时间新→旧」，第一张应当是 NIMA-086，got %s", out.Items[0].Number)
	}
}
