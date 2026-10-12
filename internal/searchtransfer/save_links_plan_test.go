package searchtransfer

import (
	"testing"

	"litepan/internal/offlinedownload"
)

// 离线批按通道分组 —— 这是 2026-10-12 那次修复的核心。
//
// 钉的是真机上必现的 bug：夸克账号下磁力/直链，界面写着「走内置下载器」，
// 提交时却因为 ProviderKind 留空被 AddURLs 当成 native，回
// 「当前网盘不支持离线下载」。界面承诺与提交行为必须同源。

// 夸克：没有原生离线，两种链都该落到内置下载器。
func TestPlanOfflineBatchesQuarkGoesAllBuiltin(t *testing.T) {
	caps := offlinedownload.Capabilities{
		Supported:         true,
		BuiltinEnabled:    true,
		BuiltinURLSchemes: []string{"http", "https", "magnet"},
	}
	byAccount := map[int64]offlineAccountCaps{5: {caps: caps}}

	batches, rejected := planOfflineBatches(byAccount, []offlinePlanItem{
		{accountID: 5, url: "magnet:?xt=urn:btih:abc", scheme: "magnet"},
		{accountID: 5, url: "https://example.com/a.mkv", scheme: "http"},
	})

	if len(rejected) != 0 {
		t.Fatalf("不该有被拒的条目：%+v", rejected)
	}
	if len(batches) != 1 {
		t.Fatalf("应当合成一批（都是内置通道），实际 %d 批", len(batches))
	}
	if batches[0].provider != offlinedownload.ProviderBuiltin {
		t.Errorf("通道应当是 builtin，实际 %q —— 这正是「界面说内置、提交走 native」那个 bug",
			batches[0].provider)
	}
	if len(batches[0].items) != 2 {
		t.Errorf("两条都该在这一批里，实际 %d 条", len(batches[0].items))
	}
}

// 123 没配网页凭据：http 走原生离线、magnet 走内置 —— **必须拆成两批**。
//
// 一个 ProviderKind 装不下两种通道；早先的实现在这里只能二选一，
// 另一条必然带着错的通道提交。
func TestPlanOfflineBatchesSplitsByProviderForPartialCredentials(t *testing.T) {
	caps := offlinedownload.Capabilities{
		Supported:    true,
		SupportsURLs: true,
		URLSchemes:   []string{"http", "https", "magnet"},
		DriverType:   "123_open",
		// 没配网页账号密码 → magnet 那条走不了原生。
		WebLoginReady:     false,
		BuiltinEnabled:    true,
		BuiltinURLSchemes: []string{"http", "https", "magnet"},
	}
	byAccount := map[int64]offlineAccountCaps{5: {caps: caps}}

	batches, rejected := planOfflineBatches(byAccount, []offlinePlanItem{
		{accountID: 5, url: "https://example.com/a.mkv", scheme: "http"},
		{accountID: 5, url: "magnet:?xt=urn:btih:abc", scheme: "magnet"},
	})

	if len(rejected) != 0 {
		t.Fatalf("不该有被拒的条目：%+v", rejected)
	}
	if len(batches) != 2 {
		t.Fatalf("应当拆成两批（native + builtin），实际 %d 批：%+v", len(batches), batches)
	}
	got := map[string][]string{}
	for _, b := range batches {
		for _, it := range b.items {
			got[b.provider] = append(got[b.provider], it.scheme)
		}
	}
	if len(got[offlinedownload.ProviderNative]) != 1 || got[offlinedownload.ProviderNative][0] != "http" {
		t.Errorf("http 该走 native，实际 %+v", got)
	}
	if len(got[offlinedownload.ProviderBuiltin]) != 1 || got[offlinedownload.ProviderBuiltin][0] != "magnet" {
		t.Errorf("magnet 该降级到内置，实际 %+v", got)
	}
}

// 两边都不吃的链接必须**在提交前**被拒并带上原因，而不是提交后报一句驱动错误。
func TestPlanOfflineBatchesRejectsUnsupported(t *testing.T) {
	caps := offlinedownload.Capabilities{
		Supported:         true,
		BuiltinEnabled:    true,
		BuiltinURLSchemes: []string{"http", "https", "magnet"},
	}
	byAccount := map[int64]offlineAccountCaps{5: {caps: caps}}

	batches, rejected := planOfflineBatches(byAccount, []offlinePlanItem{
		{accountID: 5, url: "ed2k://|file|a.mkv|1|0123456789abcdef0123456789abcdef|/", scheme: "ed2k"},
	})

	if len(batches) != 0 {
		t.Errorf("不该有任何分组：%+v", batches)
	}
	if len(rejected) != 1 || rejected[0].reason == "" {
		t.Fatalf("应当被拒且带上原因，实际 %+v", rejected)
	}
}

// 能力探测**失败**时的降级必须看 scheme：只有内置确实吃它才降级。
//
// 与 tgsubscribe.resolveProvider 同源。2026-09-17 真机踩到过：115 令牌失效 →
// 探测失败 → 无条件降级到内置 → 内置拒绝 ed2k，报出「离线下载链接格式不正确」，
// 把用户指去检查链接格式，而真正该做的是重新授权账号。
func TestPlanOfflineBatchesProbeFailureDegradesOnlyWhenBuiltinSupports(t *testing.T) {
	byAccount := map[int64]offlineAccountCaps{5: {probeFailed: true}}

	batches, rejected := planOfflineBatches(byAccount, []offlinePlanItem{
		{accountID: 5, url: "magnet:?xt=urn:btih:abc", scheme: "magnet"},
		{accountID: 5, url: "ed2k://|file|a.mkv|1|0123456789abcdef0123456789abcdef|/", scheme: "ed2k"},
	})
	if len(rejected) != 0 {
		t.Fatalf("探测失败时不该在这里断言「不支持」：%+v", rejected)
	}
	got := map[string][]string{}
	for _, b := range batches {
		for _, it := range b.items {
			got[b.provider] = append(got[b.provider], it.scheme)
		}
	}
	// magnet 内置支持 → 降级；ed2k 内置不支持 → 保留 native 让提交去报真正的错。
	if len(got[offlinedownload.ProviderBuiltin]) != 1 || got[offlinedownload.ProviderBuiltin][0] != "magnet" {
		t.Errorf("magnet 该降级到内置，实际 %+v", got)
	}
	if len(got[offlinedownload.ProviderNative]) != 1 || got[offlinedownload.ProviderNative][0] != "ed2k" {
		t.Errorf("ed2k 该保留 native（让提交报真实原因），实际 %+v", got)
	}
}

// 显式 pin 了通道时**同样要过能力校验** —— 光信用户选的会在提交时撞白名单。
func TestPlanOfflineBatchesValidatesPinnedProvider(t *testing.T) {
	caps := offlinedownload.Capabilities{
		Supported:         true,
		BuiltinEnabled:    true,
		BuiltinURLSchemes: []string{"http", "https", "magnet"},
	}
	byAccount := map[int64]offlineAccountCaps{5: {caps: caps}}

	// pin native 但网盘根本不支持 URL 离线 → 拒绝。
	batches, rejected := planOfflineBatches(byAccount, []offlinePlanItem{
		{accountID: 5, url: "magnet:?xt=urn:btih:abc", scheme: "magnet", pin: offlinedownload.ProviderNative},
	})
	if len(batches) != 0 || len(rejected) != 1 {
		t.Fatalf("pin 到用不了的通道应当被拒，实际 batches=%+v rejected=%+v", batches, rejected)
	}

	// pin builtin 且内置支持 → 放行。
	batches, rejected = planOfflineBatches(byAccount, []offlinePlanItem{
		{accountID: 5, url: "magnet:?xt=urn:btih:abc", scheme: "magnet", pin: offlinedownload.ProviderBuiltin},
	})
	if len(rejected) != 0 || len(batches) != 1 || batches[0].provider != offlinedownload.ProviderBuiltin {
		t.Fatalf("pin builtin 应当放行，实际 batches=%+v rejected=%+v", batches, rejected)
	}
}

// 不同账号不能混进同一批（现在接口只支持一个账号，但分组按账号是零成本的保险）。
func TestPlanOfflineBatchesGroupsByAccount(t *testing.T) {
	caps := offlinedownload.Capabilities{
		Supported:         true,
		BuiltinEnabled:    true,
		BuiltinURLSchemes: []string{"http", "https", "magnet"},
	}
	byAccount := map[int64]offlineAccountCaps{
		5: {caps: caps},
		6: {caps: caps},
	}

	batches, _ := planOfflineBatches(byAccount, []offlinePlanItem{
		{accountID: 5, url: "magnet:?xt=urn:btih:abc", scheme: "magnet"},
		{accountID: 6, url: "magnet:?xt=urn:btih:def", scheme: "magnet"},
	})
	if len(batches) != 2 {
		t.Fatalf("两个账号应当分成两批，实际 %d 批", len(batches))
	}
	for _, b := range batches {
		for _, it := range b.items {
			if it.accountID != b.accountID {
				t.Errorf("批 %d 里混进了账号 %d 的条目", b.accountID, it.accountID)
			}
		}
	}
}
