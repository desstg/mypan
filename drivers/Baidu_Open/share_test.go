package baiduopen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

func writePanJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Errorf("encode: %v", err)
	}
}

// fakePanAPI 打桩网页接口。
type fakePanAPI struct {
	mu sync.Mutex

	// bdstoken 是 gettemplatevariable 返回的令牌；空串模拟取不到。
	bdstoken string

	// 分享页 HTML（含 loginstate 那段 JSON）。
	sharePageHTML string

	verifyErrno int
	// shareList 是 /share/list 的条目。
	shareList []map[string]any
	// transferErrno / transferInfo 用于模拟转存撞名。
	transferErrno int
	transferInfo  string

	// 记录
	transferForms []url.Values
	verifyForms   []url.Values
	// 离线下载
	cloudDlAddForms []url.Values
	cloudDlQuery    []url.Values
}

func (f *fakePanAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/gettemplatevariable", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		token := f.bdstoken
		f.mu.Unlock()
		writePanJSON(t, w, map[string]any{"errno": 0, "result": map[string]any{"bdstoken": token}})
	})

	// 分享页现在走 `/share/init?surl=...`（`/s/<code>` 是 302 短链，见 panSharePageURL）。
	// 两个路径都注册上，顺手钉住「归一化之后打的是哪一页」。
	sharePage := func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		html := f.sharePageHTML
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}
	mux.HandleFunc("/share/init", sharePage)
	mux.HandleFunc("/s/", sharePage)

	mux.HandleFunc("/share/verify", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.verifyForms = append(f.verifyForms, r.PostForm)
		errno := f.verifyErrno
		f.mu.Unlock()
		writePanJSON(t, w, map[string]any{"errno": errno, "errmsg": ""})
	})

	mux.HandleFunc("/share/list", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		list := f.shareList
		f.mu.Unlock()
		writePanJSON(t, w, map[string]any{"errno": 0, "list": list})
	})

	mux.HandleFunc("/share/transfer", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.transferForms = append(f.transferForms, r.PostForm)
		errno := f.transferErrno
		info := f.transferInfo
		f.mu.Unlock()
		payload := map[string]any{"errno": errno, "errmsg": ""}
		if info != "" {
			payload["info"] = json.RawMessage(info)
		}
		writePanJSON(t, w, payload)
	})

	mux.HandleFunc("/rest/2.0/services/cloud_dl", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		method := r.URL.Query().Get("method")
		f.mu.Lock()
		switch method {
		case "add_task":
			f.cloudDlAddForms = append(f.cloudDlAddForms, r.PostForm)
			f.mu.Unlock()
			writePanJSON(t, w, map[string]any{"errno": 0, "task_id": 123456})
		case "query_magnetinfo":
			f.mu.Unlock()
			writePanJSON(t, w, map[string]any{
				"errno": 0,
				"magnet_info": []map[string]any{
					{"file_name": "a.mkv", "file_size": 100, "idx": 0},
					{"file_name": "b.mkv", "file_size": 200, "idx": 1},
				},
			})
		case "query_task":
			f.cloudDlQuery = append(f.cloudDlQuery, r.PostForm)
			f.mu.Unlock()
			writePanJSON(t, w, map[string]any{
				"errno": 0,
				"task_info": map[string]any{
					"123456": map[string]any{
						"status": "1", "file_size": "200", "finished_size": "50",
						"task_name": "某片 (2026)", "save_path": "/电视剧",
					},
				},
			})
		case "delete_task":
			f.mu.Unlock()
			writePanJSON(t, w, map[string]any{"errno": 0})
		default:
			f.mu.Unlock()
			writePanJSON(t, w, map[string]any{"errno": 1, "errmsg": "unknown method"})
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newPanTestDriver 把 base URL 指向桩服务，注入 Cookie。
//
// ⚠️ client 的 Transport 是一个**分流器**：目标目录核对走的是开放平台
// （`/rest/2.0/xpan/file`，baseURL 是 const，测试改不了），所以那一路由本地
// RoundTripper 直接回一个「目录存在」的空列表；其余请求照常打到 httptest。
func newPanTestDriver(t *testing.T, api *fakePanAPI) *Driver {
	t.Helper()
	srv := api.server(t)
	prevPan, prevPcs := panBaseURL, pcsBaseURLWeb
	panBaseURL, pcsBaseURLWeb = srv.URL, srv.URL
	t.Cleanup(func() { panBaseURL, pcsBaseURLWeb = prevPan, prevPcs })

	real := httpx.NewClient(httpx.ClientOptions{Timeout: 5 * time.Second})
	d := &Driver{client: &http.Client{
		Timeout: 5 * time.Second,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/rest/2.0/xpan/") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"errno":0,"list":[]}`)),
					Request:    req,
				}, nil
			}
			return real.Transport.RoundTrip(req)
		}),
	}}
	d.SetAuthCredentials(domain.AuthCredentials{Cookie: "BDUSS=abc; STOKEN=def"})
	return d
}

// roundTripFunc 让测试能把 client 的 Transport 换成一段函数。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// 分享页 HTML 的样本形状：locals.mset({...}) 里带 loginstate / shareid / share_uk。
// 真机形状（2026-10-13 抓的真实页面）：`shareid`/`share_uk` 只在
// `yunData.setData({...})` 那段 **JS 字面量**里（键不带引号、值是裸数字），
// 而 `locals.mset({...})` 那段虽然也是对象但**没有** shareid。
// 桩必须照这个形状写 —— 写成一份「好看的 JSON」会让测试在真机改版时全绿。
const panShareHTML = `<html><script>
locals.mset({"csrf":"x","uk":"111","loginstate":1,"bdstoken":"a33f457a3d2245e4f03fa4d104c0a877"});
yunData.setData({skinName:'white', "share_uk":"222", "shareid":333, sharetype:0});
</script></html>`

func TestPanShareReceiveCapabilitiesNeedsCookie(t *testing.T) {
	d := &Driver{}
	if d.ShareReceiveCapabilities().Ready {
		t.Fatal("没配 Cookie 时不该报告可用")
	}
	if !strings.Contains(d.ShareReceiveCapabilities().Reason, "Cookie") {
		t.Errorf("原因要说清缺什么: %q", d.ShareReceiveCapabilities().Reason)
	}
	d.SetAuthCredentials(domain.AuthCredentials{Cookie: "BDUSS=x"})
	if !d.ShareReceiveCapabilities().Ready {
		t.Error("配了 Cookie 之后应当报告可用")
	}
}

func TestPanShareKinds(t *testing.T) {
	if got := (&Driver{}).ShareKinds(); got != driver.ShareKindBaidu {
		t.Errorf("ShareKinds = %q, want %q", got, driver.ShareKindBaidu)
	}
}

func TestSurlOfStripsLeadingOne(t *testing.T) {
	cases := map[string]string{
		"https://pan.baidu.com/s/1abcdEFG?pwd=xx": "abcdEFG",
		"https://pan.baidu.com/s/1abcdEFG":        "abcdEFG",
		"https://pan.baidu.com/s/abcdEFG":         "abcdEFG",
	}
	for raw, want := range cases {
		if got := surlOf(raw); got != want {
			t.Errorf("surlOf(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestPanReceiveShareHappyPath(t *testing.T) {
	api := &fakePanAPI{
		bdstoken:      "TOK",
		sharePageHTML: panShareHTML,
		shareList: []map[string]any{
			{"fs_id": 111, "server_filename": "某片", "isdir": 1, "size": 0},
			{"fs_id": 222, "server_filename": "a.mkv", "isdir": 0, "size": 100},
		},
	}
	d := newPanTestDriver(t, api)

	got, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{
		ShareCode:   "abcdEFG",
		ReceiveCode: "xy9z",
		TargetCID:   "/",
	})
	if err != nil {
		t.Fatalf("ReceiveShare: %v", err)
	}
	if got.Count != 2 {
		t.Errorf("Count = %d, want 2", got.Count)
	}
	if len(api.verifyForms) != 1 {
		t.Errorf("有提取码时应当先过 verify，实际 %d 次", len(api.verifyForms))
	}
	if len(api.transferForms) != 1 {
		t.Fatalf("transfer 调了 %d 次, want 1", len(api.transferForms))
	}
	form := api.transferForms[0]
	if form.Get("fsidlist") != "[111,222]" {
		t.Errorf("fsidlist = %q", form.Get("fsidlist"))
	}
	if form.Get("path") != "/" {
		t.Errorf("path = %q", form.Get("path"))
	}
}

// 提取码错：要报成确定性的 VALIDATION，且绝不能发起 transfer。
func TestPanReceiveShareRejectsWrongPassword(t *testing.T) {
	api := &fakePanAPI{
		bdstoken:      "TOK",
		sharePageHTML: panShareHTML,
		verifyErrno:   -9,
		shareList:     []map[string]any{{"fs_id": 1, "server_filename": "a.mkv"}},
	}
	d := newPanTestDriver(t, api)

	_, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{
		ShareCode: "abcdEFG", ReceiveCode: "wrong",
	})
	if err == nil {
		t.Fatal("提取码错时应当报错")
	}
	if ae, ok := domain.AsAppError(err); !ok || ae.Code != domain.CodeValidation {
		t.Errorf("应当是 VALIDATION（确定性失败）: %v", err)
	}
	if len(api.transferForms) != 0 {
		t.Fatal("提取码错时不该发起 transfer")
	}
}

// 转存撞名（errno=12 + info[0].errno=-30）要给出可执行的话，而不是一个数字。
func TestPanTransferDuplicateGivesActionableMessage(t *testing.T) {
	api := &fakePanAPI{
		bdstoken:      "TOK",
		sharePageHTML: panShareHTML,
		shareList:     []map[string]any{{"fs_id": 1, "server_filename": "a.mkv"}},
		transferErrno: 12,
		transferInfo:  `[{"errno":-30,"path":"/电视剧/a.mkv"}]`,
	}
	d := newPanTestDriver(t, api)

	_, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{
		ShareCode: "abcdEFG", TargetCID: "/电视剧",
	})
	if err == nil {
		t.Fatal("撞名时应当报错")
	}
	if !strings.Contains(err.Error(), "同名") {
		t.Errorf("报错要说清是撞名: %v", err)
	}
	if ae, ok := domain.AsAppError(err); !ok || ae.Code != domain.CodeValidation {
		t.Errorf("应当是 VALIDATION（确定性失败，别重试）: %v", err)
	}
}

// 分享页解析不出来时要指向「页面结构可能已变」，而不是含糊的解析错误。
func TestPanSharePageStructureFailureIsActionable(t *testing.T) {
	api := &fakePanAPI{bdstoken: "TOK", sharePageHTML: `<html>nothing here</html>`}
	d := newPanTestDriver(t, api)

	_, err := d.PeekShare(context.Background(), driver.SharePeekRequest{ShareCode: "abcdEFG"})
	if err == nil {
		t.Fatal("解析不出 shareid 时应当报错")
	}
	if !strings.Contains(err.Error(), "结构") {
		t.Errorf("报错要指向分享页结构: %v", err)
	}
}

func TestPanOfflineCapabilities(t *testing.T) {
	caps := (&Driver{}).OfflineDownloadCapabilities()
	if !caps.SupportsURLs {
		t.Error("百度应当声明支持链接离线下载")
	}
	if caps.SupportsBatchURLs {
		t.Error("cloud_dl 一次只收一条，不该声明支持批量")
	}
	if caps.SupportsTorrent {
		t.Error("没有「先传种子」那条轨，不该声明支持种子")
	}
	if !caps.RemoteDelete {
		t.Error("delete_task 存在，应当声明支持远程删除")
	}
}

func TestPanWebLoginReady(t *testing.T) {
	d := &Driver{}
	if d.WebLoginReady() {
		t.Error("没配 Cookie 时不该报告网页登录态可用")
	}
	d.SetAuthCredentials(domain.AuthCredentials{Cookie: "BDUSS=x"})
	if !d.WebLoginReady() {
		t.Error("配了 Cookie 之后应当报告可用")
	}
}

func TestPanAddOfflineURLDirectLink(t *testing.T) {
	api := &fakePanAPI{bdstoken: "TOK"}
	d := newPanTestDriver(t, api)

	got, err := d.AddOfflineURLs(context.Background(), driver.OfflineURLRequest{
		URLs:     []string{"https://example.com/a.mkv"},
		ParentID: "/电视剧",
	})
	if err != nil {
		t.Fatalf("AddOfflineURLs: %v", err)
	}
	if len(got) != 1 || !got[0].Success {
		t.Fatalf("应当成功: %+v", got)
	}
	if got[0].ProviderTaskID != "123456" {
		t.Errorf("ProviderTaskID = %q", got[0].ProviderTaskID)
	}
	if len(api.cloudDlAddForms) != 1 {
		t.Fatalf("add_task 调了 %d 次", len(api.cloudDlAddForms))
	}
	form := api.cloudDlAddForms[0]
	if form.Get("save_path") != "/电视剧" {
		t.Errorf("save_path = %q", form.Get("save_path"))
	}
	if form.Get("source_url") != "https://example.com/a.mkv" {
		t.Errorf("source_url = %q", form.Get("source_url"))
	}
	// 直链不该带磁力那套参数。
	if form.Get("type") != "" {
		t.Errorf("直链不该带 type=4，实际 %q", form.Get("type"))
	}
}

// 磁力要先 resolve，且 selected_idx 是 **1-based**。
func TestPanAddOfflineURLMagnetUsesOneBasedIndex(t *testing.T) {
	api := &fakePanAPI{bdstoken: "TOK"}
	d := newPanTestDriver(t, api)

	got, err := d.AddOfflineURLs(context.Background(), driver.OfflineURLRequest{
		URLs:     []string{"magnet:?xt=urn:btih:abc&dn=某片"},
		ParentID: "/",
	})
	if err != nil {
		t.Fatalf("AddOfflineURLs: %v", err)
	}
	if len(got) != 1 || !got[0].Success {
		t.Fatalf("应当成功: %+v", got)
	}
	if len(api.cloudDlAddForms) != 1 {
		t.Fatalf("add_task 调了 %d 次", len(api.cloudDlAddForms))
	}
	form := api.cloudDlAddForms[0]
	if form.Get("type") != "4" {
		t.Errorf("磁力应当带 type=4，实际 %q", form.Get("type"))
	}
	if form.Get("selected_idx") != "1,2" {
		t.Errorf("selected_idx = %q, want \"1,2\"（1-based）", form.Get("selected_idx"))
	}
	if form.Get("task_from") != "1" {
		t.Errorf("磁力的 task_from 应当是 1，实际 %q", form.Get("task_from"))
	}
}

// 批量提交要被本地挡下（能力表声明的是不支持批量）。
func TestPanAddOfflineURLRejectsBatch(t *testing.T) {
	d := &Driver{}
	_, err := d.AddOfflineURLs(context.Background(), driver.OfflineURLRequest{
		URLs: []string{"https://a/1.mkv", "https://a/2.mkv"},
	})
	if err == nil {
		t.Fatal("一次多条应当报错")
	}
}

func TestPanRefreshOfflineTasksParsesStringNumbers(t *testing.T) {
	api := &fakePanAPI{bdstoken: "TOK"}
	d := newPanTestDriver(t, api)

	updates, err := d.RefreshOfflineTasks(context.Background(), []driver.OfflineTaskRef{{ProviderTaskID: "123456"}})
	if err != nil {
		t.Fatalf("RefreshOfflineTasks: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("应当有 1 条更新: %+v", updates)
	}
	up := updates[0]
	if up.Status != driver.OfflineStatusRunning {
		t.Errorf("status = %q, want running", up.Status)
	}
	// finished=50 / total=200 → 25%
	if up.Progress != 25 {
		t.Errorf("Progress = %d, want 25", up.Progress)
	}
	if up.Size != 200 {
		t.Errorf("Size = %d, want 200", up.Size)
	}
}

// 状态码台账：0 成功 / 1 进行中 / 其余失败。
func TestCloudDlStatusMapping(t *testing.T) {
	cases := map[int64]string{
		0: driver.OfflineStatusSuccess,
		1: driver.OfflineStatusRunning,
		2: driver.OfflineStatusFailed,
		3: driver.OfflineStatusFailed,
		4: driver.OfflineStatusFailed,
		5: driver.OfflineStatusFailed,
		6: driver.OfflineStatusFailed,
		7: driver.OfflineStatusFailed,
	}
	for code, want := range cases {
		if got := cloudDlStatus(code); got != want {
			t.Errorf("cloudDlStatus(%d) = %q, want %q", code, got, want)
		}
	}
}

// 没配 Cookie 时要在发请求之前就拦下。
func TestPanOfflineWithoutCookieFailsFast(t *testing.T) {
	d := &Driver{client: httpx.NewClient(httpx.ClientOptions{Timeout: time.Second})}
	_, err := d.AddOfflineURLs(context.Background(), driver.OfflineURLRequest{URLs: []string{"https://a/b.mkv"}})
	if err == nil || !strings.Contains(err.Error(), "Cookie") {
		t.Fatalf("应当在本地拦下并说明缺 Cookie: %v", err)
	}
}
