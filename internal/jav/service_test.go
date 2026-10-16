package jav

import (
	"testing"

	"litepan/internal/jav/synopsis"
)

// TestRouteSiteIDsAreImplemented 钉住「路由表里的站点 id」与「真的建了客户端」这两边。
//
// 这两处分家的表现是**静默**的：路由表里排了某家，`pickSiteEnrichers` 查不到就跳过 ——
// 不报错、没日志，只是那一段顺序白排、那一档少一家源。而「少一家源」的后果是
// 「这批片就是补不上」，与「上游真的没有」无法区分，最难查。
//
// 所以判据是：`routeSiteTable` 里出现的每个 id，必须**要么**在 buildSiteEnrichers
// 里真的建了，**要么**在 pendingSiteIDs 里显式登记为「还没实现」。
// 两边都不在 = 拼错的 id（或忘了登记），用例失败。
func TestRouteSiteIDsAreImplemented(t *testing.T) {
	built := buildSiteEnrichers(siteOptions{})
	for _, route := range AllRoutes() {
		for _, id := range RouteSites(route) {
			if _, ok := built[id]; ok {
				continue
			}
			reason, pending := pendingSiteIDs[id]
			if pending {
				t.Logf("档位 %-11s 的站点 %-11q 尚未实现（%s）", route, id, reason)
				continue
			}
			t.Errorf("档位 %q 的站点 %q：既没有实现，也不在 pendingSiteIDs 里 —— "+
				"要么 id 拼错了，要么忘了登记未实现", route, id)
		}
	}
}

// TestPendingSiteIDsAreActuallyUsed 反向盯一遍：pendingSiteIDs 里登记的必须**真的**
// 出现在某条路由里，否则那份名单会随时间烂掉（登记了却没人用 = 误导）。
func TestPendingSiteIDsAreActuallyUsed(t *testing.T) {
	used := map[string]bool{}
	for _, route := range AllRoutes() {
		for _, id := range RouteSites(route) {
			used[id] = true
		}
	}
	for id, reason := range pendingSiteIDs {
		if !used[id] {
			t.Errorf("pendingSiteIDs 登记了 %q（%s），但它不在任何路由链里 —— "+
				"这份名单该跟着路由表一起更新", id, reason)
		}
	}
}

// TestSiteClientsAreSharedAcrossRoutes 钉住「客户端只建一份」这条判据。
//
// 为什么它重要：synopsis 的 conn 内建 `throttler{gap}`，而 `lastReq` 是**客户端
// 自己的状态**。若按路由各建一份，同一个站就会被 N 份限速器同时打 —— 对
// FC2 官方站这种有反爬的，等于自毁节流（`jav_request_gap_ms` 那条设置会形同虚设）。
//
// 判据：同一次 buildSiteEnrichers 出来的 map，被两条不同档位挑中同一家时，
// 拿到的必须是**同一个实例**。
func TestSiteClientsAreSharedAcrossRoutes(t *testing.T) {
	built := buildSiteEnrichers(siteOptions{})
	// missav 同时出现在很多档里 —— 拿两条都含它的档来对。
	a := pickFrom(built, RouteDefault)
	b := pickFrom(built, RouteCensored)
	if a["missav"] == nil || b["missav"] == nil {
		t.Fatalf("前提变了：missav 应同时出现在 RouteDefault 与 RouteCensored 里")
	}
	if a["missav"] != b["missav"] {
		t.Error("两条路由拿到的是不同的 missav 实例 —— 限速器被复制了，抓取间隔会形同虚设")
	}
}

// pickFrom 是 pickSiteEnrichers 的纯函数版本（用例里不构造整个 Service）。
func pickFrom(built map[string]synopsis.Enricher, route JavRoute) map[string]synopsis.Enricher {
	out := map[string]synopsis.Enricher{}
	for _, id := range RouteSites(route) {
		if e, ok := built[id]; ok {
			out[id] = e
		}
	}
	return out
}

// TestFilteredChainPerRoute 钉住**实际会跑的那条链**（过滤掉未实现的站之后）。
//
// 为什么值得单独一条：`routeSiteTable` 是「我们打算照 mdc-ng 走」的意图，而
// 真正打出去的是过滤后的切片。两者混起来看会得出错的结论 ——
// 比如以为 FC2 那档有 5 家源，其实 pending 了 2 家、只剩 3 家。
func TestFilteredChainPerRoute(t *testing.T) {
	built := buildSiteEnrichers(siteOptions{})
	names := func(route JavRoute) []string {
		out := []string{}
		for _, id := range RouteSites(route) {
			if _, ok := built[id]; ok {
				out = append(out, id)
			}
		}
		return out
	}
	cases := map[JavRoute][]string{
		// FC2 那 1426 部实际会打的站 —— fc2 官方打头。
		RouteFC2: {"fc2", "avsox", "airav", "freejavbt", "missav"},
		// 有码：只剩 dmm 未实现（它要过年龄墙 + 走 GraphQL）。
		// 有码：**dmm 打头**（与 mdc-ng 一致 —— 它把 dmm 排在有码链第一位）。
		// ⚠️ 它需要日本出口，见 dmm.go 文件头。
		RouteCensored:   {"dmm", "javbus", "jav321", "airav", "freejavbt", "missav"},
		RouteUncensored: {"javbus", "avsox", "caribbeancom", "airav", "freejavbt", "missav"},
		// 国产：madouqu 打头（唯一真国产站），freejavbt 兜底。
		RouteChinese: {"madouqu", "airav", "freejavbt", "missav"},
		// 素人：全是已实现的站。
		RouteAmateur: {"caribbeancom", "avsox", "javbus", "jav321", "airav", "freejavbt", "missav"},
		// 欧美：theporndb 已实现（**要 key**，没 key 时它每个号立刻返回、不发请求）。
		RouteWestern: {"theporndb", "freejavbt", "missav"},
		// 兜底：与改造前逐字相同的四家（回归锚）。
		RouteDefault: {"missav", "jav321", "caribbeancom", "javbus"},
	}
	for route, want := range cases {
		got := names(route)
		if len(got) != len(want) {
			t.Errorf("%s: 实际链 = %v，期望 %v", route, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: 实际链 = %v，期望 %v", route, got, want)
				break
			}
		}
	}
}

// TestTitleChainIsFieldFirst 钉住中文标题链是**字段优先 → 路由链**（照 mdc-ng 的字段优先级）。
//
// airav 必须排第一（它给的是干净的一行中文标题，mdc-ng 的「标题」字段优先级里
// 也只有它一家），后面接路由链兜底。
func TestTitleChainIsFieldFirst(t *testing.T) {
	if len(titleFieldSites) == 0 || titleFieldSites[0] != "airav" {
		t.Fatalf("标题字段优先第一位该是 airav（干净中文标题），得到 %v", titleFieldSites)
	}
	// 拼接顺序 = 字段优先 + 路由链，且**去重**（airav 也在路由链里出现过）。
	built := buildSiteEnrichers(siteOptions{})
	seen := map[string]int{}
	for _, id := range titleFieldSites {
		seen[id]++
	}
	for _, id := range RouteSites(RouteCensored) {
		if _, ok := built[id]; ok {
			seen[id]++
		}
	}
	// airav 只在 titleFieldSites 与路由链里各出现一次 —— 拼接时要去重，
	// 否则同一个站会在一条链里被问两遍（白打一次请求）。
	if seen["airav"] < 1 {
		t.Fatal("airav 该在标题链里")
	}
}
