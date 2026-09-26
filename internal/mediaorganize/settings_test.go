package mediaorganize

import (
	"context"
	"testing"

	"litepan/internal/settings"
	"litepan/internal/store"
)

// TestSettingsDictCarriesProxyURL 钉住「设置字典里必须带 proxy_url」。
//
// 下游有两条路（TMDB 连通性测试、目录整理的搜片）是拿这个 map 去喂
// PlannerProxyURL 的，而它读的就是 `proxy_url` —— 少了这个键，
// 用户在「系统设置 → 网络代理」里配的代理对这两条路**完全不生效**，一律直连。
// 症状极具迷惑性：界面上填了代理、点了保存、看着都对，但测试永远直连，
// 于是「代理明明通却报 API 连不上」。
//
// 另两条路走 EnrichPlannerSettings（那里本来就塞了这个键），两边必须一致。
func TestSettingsDictCarriesProxyURL(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc, err := settings.New(ctx, store.New(db).Configs)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}

	// 没启用代理时是空串 —— 下游据此直连。
	if got := SettingsDict(svc)["proxy_url"]; got != "" {
		t.Fatalf("未启用代理时应当是空串，got %v", got)
	}

	// 启用并填地址后，必须原样带出来。
	if err := svc.Update(ctx, map[string]string{
		settings.KeyProxyEnabled: "true",
		settings.KeyProxyURL:     "http://192.168.31.9:7893",
	}); err != nil {
		t.Fatalf("seed proxy: %v", err)
	}
	if got := SettingsDict(svc)["proxy_url"]; got != "http://192.168.31.9:7893" {
		t.Fatalf("启用后应当带出代理地址，got %v", got)
	}

	// 与 EnrichPlannerSettings 保持一致 —— 这两条路必须给出同一个答案，
	// 否则「测试说通、任务跑不通」还会再来一次。
	enriched := EnrichPlannerSettings(svc, nil)
	if enriched["proxy_url"] != SettingsDict(svc)["proxy_url"] {
		t.Errorf("两条路给出的代理不一致：SettingsDict=%v Enrich=%v",
			SettingsDict(svc)["proxy_url"], enriched["proxy_url"])
	}
}
