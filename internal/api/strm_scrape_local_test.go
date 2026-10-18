package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"litepan/internal/settings"
	"litepan/internal/strmscrape"
)

// 本地目录（tmdb 档）挂上海报墙 —— **HTTP 那一层**的钉子。
//
// 为什么还要这一条：`strmscrape` 里那两条（TestLocalTmdbListItemsByRef /
// RefreshIndexByRef）钉的是服务层，而这次改动最容易漏的恰恰是**handler 侧的选择器**。
// 改造前 `GET /items` 是 `ParseInt(r.URL.Query().Get("strm_task_id"))`，本地目录发上来的
// `local_dir_id` 会被**静默丢掉**（解析失败 → 0 → 「任务不存在」或空列表），
// 墙上就是「挂了 TMDB 本地目录，永远是空」。
//
// 所以这里走真 handler：query / body 里只给 `local_dir_id`（一个 `strm_task_id` 都不带），
// 断言能列出作品、且索引库落在 `local-<id>.sqlite`。

// memConfigRepo 是 domain.ConfigRepository 的最小内存实现（settings.New 要一个）。
type memConfigRepo struct{ values map[string]string }

func (m *memConfigRepo) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := m.values[key]
	return v, ok, nil
}

func (m *memConfigRepo) Set(_ context.Context, key, value string) error {
	if m.values == nil {
		m.values = map[string]string{}
	}
	m.values[key] = value
	return nil
}

func (m *memConfigRepo) All(context.Context) (map[string]string, error) {
	out := make(map[string]string, len(m.values))
	for k, v := range m.values {
		out[k] = v
	}
	return out, nil
}

// newLocalTMDBSourceFixture 造一个「tmdb 档本地目录源 + 一个 handler」。
// 返回的 dataDir 是索引库的落点（`<dataDir>/strmscrape/`）。
func newLocalTMDBSourceFixture(t *testing.T) (h *Handler, dirID, dataDir string) {
	t.Helper()
	root := t.TempDir()
	const work = "护肝人 (2026)"
	if err := os.MkdirAll(filepath.Join(root, work), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, work, work+".strm"), []byte("url"), 0o644); err != nil {
		t.Fatalf("write strm: %v", err)
	}
	dirID = "0011223344556677"
	settingsSvc, err := settings.New(context.Background(), &memConfigRepo{values: map[string]string{
		settings.KeyStrmWallLocalDirs: settings.EncodeWallLocalDirs([]settings.WallLocalDir{{
			ID: dirID, Name: "本地电影", Root: root, MediaKind: settings.WallLocalKindTmdb,
		}}),
	}})
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	dataDir = t.TempDir()
	svc := strmscrape.New(strmscrape.Options{
		StrmDir:  t.TempDir(),
		DataDir:  dataDir,
		Settings: settingsSvc,
	})
	return &Handler{strmScrape: svc}, dirID, dataDir
}

// decodeItems 解开统一响应壳（`{success,data:{items…}}`）拿列表结果。
func decodeItems(t *testing.T, rec *httptest.ResponseRecorder) strmscrape.ItemListResult {
	t.Helper()
	var env struct {
		Success bool                      `json:"success"`
		Data    strmscrape.ItemListResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("解 JSON: %v，body=%s", err, rec.Body.String())
	}
	if !env.Success {
		t.Fatalf("响应 success=false：%s", rec.Body.String())
	}
	return env.Data
}

// TestLocalTMDBSourceListsItemsOverHTTP 只给 `local_dir_id`（不带 strm_task_id）时，
// `GET /items` 要能列出本地目录里的作品。
func TestLocalTMDBSourceListsItemsOverHTTP(t *testing.T) {
	h, dirID, _ := newLocalTMDBSourceFixture(t)

	rec := httptest.NewRecorder()
	h.listStrmScrapeItems(rec, httptest.NewRequest(http.MethodGet, "/items?local_dir_id="+dirID, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d，body=%s", rec.Code, rec.Body.String())
	}
	out := decodeItems(t, rec)
	if out.Total != 1 || len(out.Items) != 1 {
		t.Fatalf("应列出 1 项，实际 total=%d items=%d：%s", out.Total, len(out.Items), rec.Body.String())
	}
	if out.Items[0].Title == "" {
		t.Errorf("列出来的条目没有标题：%+v", out.Items[0])
	}
}

// TestLocalTMDBSourceRefreshIndexOverHTTP 首屏那条路（空库时前端发 `refresh-index`）：
// body 里只给 `local_dir_id` 也要能建索引并列出，索引库落在 `local-<id>.sqlite`。
func TestLocalTMDBSourceRefreshIndexOverHTTP(t *testing.T) {
	h, dirID, dataDir := newLocalTMDBSourceFixture(t)

	body := `{"local_dir_id":"` + dirID + `","limit":60}`
	rec := httptest.NewRecorder()
	h.refreshStrmScrapeIndex(rec, httptest.NewRequest(http.MethodPost, "/refresh-index", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d，body=%s", rec.Code, rec.Body.String())
	}
	out := decodeItems(t, rec)
	if out.Total != 1 {
		t.Fatalf("应列出 1 项，实际 %d：%s", out.Total, rec.Body.String())
	}
	// ⚠️ 索引库必须按源分家：本地源是 `local-<id>.sqlite`。
	// 共用一份的话，挂两个本地目录时第二个会把第一个的条目覆盖掉。
	index := strmscrape.SourceIndexPath(dataDir, "local-"+dirID)
	if _, err := os.Stat(index); err != nil {
		t.Fatalf("索引库没建在 %s：%v", index, err)
	}
}

// TestStrmScrapeItemsWithoutSelectorIsRejected 两个选择器都不给时要报校验错
// （而不是静默列任务 0 的空列表 —— 那正是改造前的表现）。
func TestStrmScrapeItemsWithoutSelectorIsRejected(t *testing.T) {
	h, _, _ := newLocalTMDBSourceFixture(t)
	rec := httptest.NewRecorder()
	h.listStrmScrapeItems(rec, httptest.NewRequest(http.MethodGet, "/items", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("一个选择器都不给时不该回 200：%s", rec.Body.String())
	}
}
