package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"litepan/internal/adminauth"
	"litepan/internal/config"
	"litepan/internal/logx"
	"litepan/internal/settings"
	"litepan/pkg/security"

	_ "litepan/drivers"
)

// 真机联调：**走真 HTTP** 验「按实体搜索」那两条新接口。
//
// 默认跳过。跑它：
//
//	$env:LITEPAN_LIVE_DATA="E:\claude code\LitePan-main\data"
//	$env:LITEPAN_LIVE_ENTITIES="1"
//	go test ./internal/app/ -run TestLiveJavEntities -v -timeout 10m
//
// 真库**只读**：只从它取 secret.key、代理与上游地址这几项配置。
// 库用临时目录新开一个（种一个哈希过的管理员口令），不碰用户那份。
func TestLiveJavEntities(t *testing.T) {
	if os.Getenv("LITEPAN_LIVE_ENTITIES") == "" {
		t.Skip("未设置 LITEPAN_LIVE_ENTITIES，跳过真机测试")
	}
	secretDir := os.Getenv("LITEPAN_LIVE_DATA")
	if secretDir == "" {
		t.Skip("未设置 LITEPAN_LIVE_DATA（指向含 secret.key 的 data 目录）")
	}

	ctx := context.Background()
	logs, err := logx.New(logx.Options{Level: "warn", DisableFile: true})
	if err != nil {
		t.Fatalf("建日志失败：%v", err)
	}

	dataDir, err := os.MkdirTemp("", "litepan-live-entities-")
	if err != nil {
		t.Fatalf("建临时目录失败：%v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
	if raw, rerr := os.ReadFile(filepath.Join(secretDir, "secret.key")); rerr == nil {
		if werr := os.WriteFile(filepath.Join(dataDir, "secret.key"), raw, 0o600); werr != nil {
			t.Fatalf("复制 secret.key: %v", werr)
		}
	} else {
		t.Skipf("读不到 secret.key：%v", rerr)
	}

	cfg := config.Config{
		DataDir:    dataDir,
		DBPath:     filepath.Join(dataDir, "litepan.db"),
		StrmDir:    filepath.Join(dataDir, "strm"),
		ListenAddr: "127.0.0.1:0",
	}
	st, err := openStore(ctx, cfg, logs)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	t.Cleanup(func() { _ = st.db.Close() })

	const verifyUser = "verify-admin"
	const verifyPass = "verify-pass-3f9a"
	if err := st.store.Configs.Set(ctx, adminauth.KeyAdminUsername, verifyUser); err != nil {
		t.Fatalf("种管理员用户名失败：%v", err)
	}
	if err := st.store.Configs.Set(ctx, adminauth.KeyAdminPassword, security.HashPassword(verifyPass)); err != nil {
		t.Fatalf("种管理员口令失败：%v", err)
	}

	// 把真库里「决定能不能连上上游」的那几项抄过来。**只抄这几项**，
	// 别整库复制（280MB，且会带上认证调度器要刷新的 token）。
	copied := readEntityLiveSettings(ctx, filepath.Join(secretDir, "litepan.db"), t)
	if len(copied) > 0 {
		if err := st.settings.Update(ctx, copied); err != nil {
			t.Fatalf("写入上游配置失败：%v", err)
		}
	}
	t.Logf("上游配置：proxy=%q api=%q site=%q",
		copied[settings.KeyProxyURL], copied[settings.KeyJavAPIBase], copied[settings.KeyJavSiteBase])

	core, err := wireCore(ctx, cfg, logs, st)
	if err != nil {
		t.Fatalf("wireCore: %v", err)
	}
	svc := wireServices(cfg, logs, st, core)
	server, err := wireHTTPServer(cfg, logs, st, core, svc, func() {})
	if err != nil {
		t.Fatalf("wireHTTPServer: %v", err)
	}
	ts := httptest.NewServer(server.Handler)
	t.Cleanup(ts.Close)

	cookie := signAdminCookie(t, core.secret, verifyUser)

	type searchCase struct {
		kind, keyword string
	}
	cases := []searchCase{
		{"lists", "驾驶双马尾"},
		{"series", "Kissing"},
		{"maker", "S1"},
		{"director", "溜池"},
		{"actor", "三上悠亜"},
	}

	for _, c := range cases {
		status, body := doJSON(t, ts.URL, "GET",
			"/api/jav/search/entities?q="+url.QueryEscape(c.keyword)+"&type="+c.kind, nil, cookie)
		if status != 200 {
			t.Errorf("[%s] 搜索接口 %d：%s", c.kind, status, truncate(body, 300))
			continue
		}
		var resp struct {
			Data struct {
				Items []struct {
					ID    string `json:"id"`
					Name  string `json:"name"`
					Kind  string `json:"kind"`
					Count int    `json:"count"`
				} `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Errorf("[%s] 解析失败：%v", c.kind, err)
			continue
		}
		items := resp.Data.Items
		t.Logf("[%s] 搜 %q → %d 个候选", c.kind, c.keyword, len(items))
		if len(items) == 0 {
			t.Errorf("[%s] 候选是空的", c.kind)
			continue
		}
		for i, it := range items {
			if i >= 3 {
				break
			}
			t.Logf("    %d. id=%s name=%q kind=%s count=%d", i+1, it.ID, it.Name, it.Kind, it.Count)
		}
		if items[0].Kind != c.kind {
			t.Errorf("[%s] 回传的 kind 是 %q，应当是界面取值", c.kind, items[0].Kind)
		}

		status, body = doJSON(t, ts.URL, "GET",
			"/api/jav/entities/"+c.kind+"/"+url.QueryEscape(items[0].ID)+"/movies?page=1", nil, cookie)
		if status != 200 {
			t.Errorf("[%s] 作品接口 %d：%s", c.kind, status, truncate(body, 300))
			continue
		}
		var mv struct {
			Data struct {
				Items []struct {
					ID     string   `json:"id"`
					Number string   `json:"number"`
					Title  string   `json:"title"`
					Cover  string   `json:"cover"`
					Tags   []string `json:"tags"`
				} `json:"items"`
				Total int `json:"total"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &mv); err != nil {
			t.Errorf("[%s] 作品解析失败：%v", c.kind, err)
			continue
		}
		t.Logf("[%s] %s 第 1 页 → %d 部，total=%d", c.kind, items[0].Name, len(mv.Data.Items), mv.Data.Total)
		if len(mv.Data.Items) == 0 {
			t.Errorf("[%s] 作品页是空的", c.kind)
			continue
		}
		first := mv.Data.Items[0]
		if first.ID == "" || first.Number == "" {
			t.Errorf("[%s] 首条缺 id/number：%+v", c.kind, first)
		}
		if first.Cover == "" {
			t.Errorf("[%s] 首条没有封面（NormalizeListCover 那条路可能断了）", c.kind)
		}
		t.Logf("    首条：%s | %s | cover=%v", first.Number, truncate(first.Title, 40), first.Cover != "")
	}
}

// readEntityLiveSettings 只读地读真库里「连不连得上上游」那几项。
func readEntityLiveSettings(ctx context.Context, dbPath string, t *testing.T) map[string]string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("打开真库（只读）：%v", err)
	}
	defer func() { _ = db.Close() }()

	keys := []string{
		settings.KeyProxyEnabled,
		settings.KeyProxyURL,
		settings.KeyProxyUsername,
		settings.KeyProxyPassword,
		settings.KeyJavUseProxy,
		settings.KeyJavAPIBase,
		settings.KeyJavSiteBase,
		settings.KeyJavTimeoutSec,
		settings.KeyJavRetry,
		settings.KeyJavMinIntervalMS,
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		var v string
		err := db.QueryRowContext(ctx, `select value from configs where key = ?`, k).Scan(&v)
		if err != nil {
			// 真库里可能根本没有这一行（默认值走代码）—— 跳过，让默认值生效。
			continue
		}
		out[k] = v
	}
	return out
}
