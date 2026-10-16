package jav

import (
	"strings"
	"testing"

	"litepan/internal/mediaorganize/javrules"
)

// TestRouteOf 用**真机番号**钉死每一档。
//
// 这些号码都是从 `data/litepan.db`（16811 部）与用户库里捞出来的真实形态，
// 不是编的 —— 路由判错的代价是「整档片子去错站补缺」，而那种错**不报错**，
// 只表现为「这批片就是补不上」，最难查。
func TestRouteOf(t *testing.T) {
	cases := []struct {
		name string
		in   RouteInput
		want JavRoute
	}{
		// —— FC2（真机 1426 部，当前简介缺失 100%）——
		{"FC2 带连字符", RouteInput{Number: "FC2-4939195"}, RouteFC2},
		{"FC2-PPV 形态", RouteInput{Number: "FC2-PPV-4983582"}, RouteFC2},
		{"FC2PPV 无分隔", RouteInput{Number: "FC2PPV1234567"}, RouteFC2},
		{"FC2 小写", RouteInput{Number: "fc2-4939195"}, RouteFC2},
		{"FC2 靠 type 兜底", RouteInput{Number: "4983582", MovieType: "3"}, RouteFC2},

		// —— 有码（真机 12586 部）——
		{"日式标准番号", RouteInput{Number: "SSIS-001"}, RouteCensored},
		{"带质量后缀已剥的裸番号", RouteInput{Number: "CAWD-987"}, RouteCensored},
		{"连字符后带字母", RouteInput{Number: "MKD-S03"}, RouteCensored},
		{"靠 type", RouteInput{Number: "ABCD-123", MovieType: "0"}, RouteCensored},

		// —— 无码 / 素人（真机 2323 部）——
		{"日期序号型无站名", RouteInput{Number: "091926-001"}, RouteUncensored},
		{"日期序号型下划线", RouteInput{Number: "092426_01"}, RouteUncensored},
		{"素人站名 CARIB", RouteInput{Number: "091926-001", Station: "CARIB"}, RouteAmateur},
		{"素人站名 1PON", RouteInput{Number: "082410-462", Station: "1PON"}, RouteAmateur},
		{"素人站名 HEYZO", RouteInput{Number: "HEYZO-3837", Station: "HEYZO"}, RouteAmateur},
		{"靠 type 判无码", RouteInput{Number: "SIRO-5071", MovieType: "1"}, RouteUncensored},

		// —— 欧美（真机 471 部）——
		{"欧美点分日期型", RouteInput{Number: "Tushy.2026.09.20"}, RouteWestern},
		{"欧美短年份", RouteInput{Number: "RKPrime.26.09.21"}, RouteWestern},
		{"欧美靠 type", RouteInput{Number: "SomeSite.24.01.01", MovieType: "2"}, RouteWestern},

		// —— 国产 ——
		{"国产形状（大写字母接数字）", RouteInput{Number: "XKG147"}, RouteChinese},
		// ⚠️ **国产形状必须压过 type=1**（2026-10-15 真机修的错判）：
		// JAVDB 把国产片大面积标成 type=1（无码），16811 部里 1120 部国产形状、
		// 其中 1115 部 type=1。国产形状若排在 type 之后，那 1115 部麻豆系
		// （MD0309 / MDSR0005-1 / MGL0002 / BLX0002…）会**整批走无码链**，
		// 国产链上一家都轮不到 —— 不报错，只是国产片永远补不到国产站的料。
		{"国产形状 + type=1（必须仍判国产）", RouteInput{Number: "MD0309", MovieType: "1"}, RouteChinese},
		{"国产形状 + type=1（BLX 系）", RouteInput{Number: "BLX0002", MovieType: "1"}, RouteChinese},
		{"国产形状 + type=0", RouteInput{Number: "MGL0002", MovieType: "0"}, RouteChinese},
		{"国产形状 DYXO002", RouteInput{Number: "DYXO002"}, RouteChinese},
		{"国产形状 RS034", RouteInput{Number: "RS034"}, RouteChinese},
		// ⚠️ 带连字符的国产厂牌（`MD-0292`）**判不出国产**，落有码 —— 这是有意的
		// 漏判而不是误判（形状与 `ABP-123` 完全一致，只有用户的规则表能分开）。
		// 见 routes.go 第 ⑥ 步那段说明。这条用例把这个行为**钉住**，
		// 免得以后有人顺手「修」成误判。
		{"国产厂牌带连字符 → 落有码（有意漏判）", RouteInput{Number: "MD-0292"}, RouteCensored},

		// —— 兜底 ——
		{"认不出", RouteInput{Number: ""}, RouteDefault},
		{"目录名提示（形状判不出时）", RouteInput{Number: "ZZZ", Category: "欧美"}, RouteWestern},
		{"目录名是用户改过的（命中不了 → 默认链）", RouteInput{Number: "ZZZ", Category: "日本"}, RouteDefault},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RouteOf(c.in); got != c.want {
				t.Errorf("RouteOf(%+v) = %q，期望 %q", c.in, got, c.want)
			}
		})
	}
}

// TestRouteOfShapeBeatsMovieType 钉住「形状优先于 type」这条判据。
//
// 起因是一个真 bug 的机制：`upsertSummaries` 的插入分支传的 `raw_json` 是空串，
// 而 `type` 列的 SQL 是「`raw_json` 非空才更新」—— 所以**新片第一次被点开时
// type 可能是兜底值 `'0'`**。若路由无条件信 type，无码片会被送进有码链。
func TestRouteOfShapeBeatsMovieType(t *testing.T) {
	// FC2 的番号 + 一个错的 type：番号形状必须赢。
	if got := RouteOf(RouteInput{Number: "FC2-4939195", MovieType: "0"}); got != RouteFC2 {
		t.Errorf("FC2 番号配错的 type 应仍判 FC2，得到 %q", got)
	}
	// 欧美点分型的番号 + type=0（新片的兜底值）：形状必须赢。
	if got := RouteOf(RouteInput{Number: "Tushy.2026.09.20", MovieType: "0"}); got != RouteWestern {
		t.Errorf("欧美番号配 type=0 应仍判欧美，得到 %q", got)
	}
}

// TestRouteSitesDefaultIsRegressionAnchor 钉住**回归锚**。
//
// RouteDefault 的站点链必须与「加路由之前」service.go 里那条硬编码链逐字相同
// —— 判不出档位时的行为不变，是这次改造能安全上线的依据。
func TestRouteSitesDefaultIsRegressionAnchor(t *testing.T) {
	want := []string{"missav", "jav321", "caribbeancom", "javbus"}
	got := RouteSites(RouteDefault)
	if len(got) != len(want) {
		t.Fatalf("默认链长度 = %d，期望 %d（%v）", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("默认链[%d] = %q，期望 %q", i, got[i], want[i])
		}
	}
	// 改过的切片不能污染原表（返回的是副本）。
	got[0] = "mutated"
	if again := RouteSites(RouteDefault); again[0] != "missav" {
		t.Error("RouteSites 返回的切片必须是副本，改它会污染路由表")
	}
}

// TestEveryRouteHasSites 每个档位都得有站点链 —— 空链等于「这档片子静默不补」。
func TestEveryRouteHasSites(t *testing.T) {
	for _, r := range AllRoutes() {
		if len(RouteSites(r)) == 0 {
			t.Errorf("档位 %q 没有站点链", r)
		}
	}
}

// TestRouteOfVerdictsMatchJavrules 拿真实番号把 RouteOf 与 javrules 对跑一遍。
//
// 为什么要这一条：RouteOf 复用 javrules 的形状判据（IsWesternCode / IsDateSeqCode /
// LooksLikeCNNumber），而 javrules 是**归档分类**的权威。两边分家的表现是
// 「刮削按 A 档走、归档按 B 档摆」—— 不报错，只是补错地方。
//
// ⚠️ **只在「归档与路由的目标档位本来就相同」的形态上核对**。两类必然的差异要排除：
//   - `FC2-*` 与 `091926-001-CARIB`：归档把它们并进「无码」那一档（媒体库目录
//     只有 5 档），而路由**必须**分开（FC2 与素人各有各的站，合起来就等于
//     那 1426 部 FC2 永远补不上）。这两种差异是设计，不是 bug。
//   - `MD-0292`：归档靠**用户规则表**认得出国产，路由只有形状、认不出（见 routes.go）。
func TestRouteOfVerdictsMatchJavrules(t *testing.T) {
	rules := javrules.Parse("")
	tokens := javrules.StationTokens(rules.ClassifyRules)

	type sample struct {
		number, name string // name 是磁盘上的名字（可能带站名后缀）
	}
	// 只收「归档档位 == 路由档位」的形态。
	samples := []sample{
		{"SSIS-001", "SSIS-001-U"},               // 有码 ↔ 有码
		{"Tushy.2026.09.20", "Tushy.2026.09.20"}, // 欧美 ↔ 欧美
	}
	for _, s := range samples {
		station := javrules.MatchStation(s.name, s.number, tokens)
		got := RouteOf(RouteInput{Number: s.number, Station: station})

		idx := javrules.ClassifyNameFallback(s.name, s.name, rules.ClassifyRules)
		target := ""
		if idx >= 0 {
			target = rules.ClassifyRules[idx].TargetName
		}
		want, ok := categoryRoutes[target]
		if !ok {
			t.Fatalf("%s: 归档归到 %q，而 categoryRoutes 里没有它 —— 两处的档位名分家了", s.number, target)
		}
		if got != want {
			t.Errorf("%s（归档归到 %q）: RouteOf 得 %q，期望 %q", s.number, target, got, want)
		}
	}
}

// TestRouteOfSeparatesWhatJavrulesMerges 钉住「路由比归档分得更细」这条**有意**差异。
//
// 归档只有 5 档（媒体库目录结构），路由有 7 档。FC2 与素人被归档并进「无码」，
// 而路由必须分开 —— 合并回去就等于那 1426 部 FC2 永远补不上（missav 对
// `fc2ppv-*` 直接 404，无码那条链上没有一家有 FC2 数据）。
func TestRouteOfSeparatesWhatJavrulesMerges(t *testing.T) {
	rules := javrules.Parse("")
	// 归档侧：**带站名后缀**的形态（`FC2-4939195` 与 `091926-001-carib`）都落「无码」。
	// ⚠️ 裸的 `091926-001`（不带站名）在归档里**什么规则都命中不了**（落兜底），
	// 这是 javrules/station.go 顶部实测记着的事实 —— 所以这里必须用带站名的形态。
	for _, c := range []struct{ number, name string }{
		{"FC2-4939195", "FC2-4939195"},
		{"091926-001", "091926-001-carib"},
	} {
		idx := javrules.ClassifyNameFallback(c.name, c.name, rules.ClassifyRules)
		if idx < 0 || !strings.HasPrefix(rules.ClassifyRules[idx].TargetName, "无码") {
			t.Fatalf("前提变了：%s 在归档里不再落「无码」（idx=%d）—— 这条用例的前提要重新核", c.name, idx)
		}
	}
	// 路由侧：同样这两个必须分开 —— 合并回去就等于 FC2 那 1426 部永远补不上。
	if got := RouteOf(RouteInput{Number: "FC2-4939195"}); got != RouteFC2 {
		t.Errorf("FC2-4939195 应路由到 %q，得到 %q", RouteFC2, got)
	}
	if got := RouteOf(RouteInput{Number: "091926-001", Station: "CARIB"}); got != RouteAmateur {
		t.Errorf("091926-001+CARIB 应路由到 %q，得到 %q", RouteAmateur, got)
	}
}

// TestIsFC2Number 钉住「前缀 FC2」这条判据（不是「名字里有 FC2」）。
func TestIsFC2Number(t *testing.T) {
	yes := []string{"FC2-4939195", "fc2ppv1234567", "FC2-PPV-4983582", "FC2PPV-1"}
	no := []string{"SSIS-001", "XXX-123 FC2版", "MYFC2-001", "", "AFC2-1"}
	for _, n := range yes {
		if !isFC2Number(n) {
			t.Errorf("isFC2Number(%q) 应为真", n)
		}
	}
	for _, n := range no {
		if isFC2Number(n) {
			t.Errorf("isFC2Number(%q) 应为假（必须是前缀，不是包含）", n)
		}
	}
}

// TestAmateurStationTokens 素人档的 token 名单不能把 FC2 混进来（FC2 有自己一档）。
func TestAmateurStationTokens(t *testing.T) {
	for _, tok := range AmateurStationTokens() {
		if isFC2Number(tok) {
			t.Errorf("素人 token 里混进了 FC2 系：%q", tok)
		}
	}
	if !isAmateurStation("CARIB") || !isAmateurStation("HEYZO") {
		t.Error("CARIB / HEYZO 应被认作素人站")
	}
	if isAmateurStation("FC2PPV") || isAmateurStation("TUSHY") {
		t.Error("FC2PPV（有自己一档）与 TUSHY（欧美）不该被认作素人站")
	}
}
