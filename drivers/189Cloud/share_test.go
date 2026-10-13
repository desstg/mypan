package cloud189

import (
	"context"
	"encoding/json"
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

func write189JSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Errorf("encode: %v", err)
	}
}

// fake189ShareAPI 打桩网页接口，并记录被调用了哪些端点。
//
// 三个端点对应转存三步：换 AccessToken → 列分享目录 → 提交 SHARE_SAVE 批量任务。
// 目录核对走的是**签名接口**（getFileInfo.action），所以那部分不在这里打桩，
// 用 ssoH5 返回一个空 token 之外单独处理（见 new189ShareDriver 的 withToken）。
type fake189ShareAPI struct {
	mu sync.Mutex

	// token 是 ssoH5 返回的 AccessToken；空串模拟 Cookie 失效。
	token string

	// metaOK 为 false 时 getShareInfoByCodeV2 回失败。
	metaOK   bool
	shareID  string
	rootID   string
	fileName string

	// entries 是 listShareDir 的根目录条目。
	entries []map[string]any

	// taskStatuses 依次回给 checkBatchTask；用完最后一个就一直用它。
	taskStatuses []int
	taskError    string

	// 记录调用
	tokenCalls   int
	listCalls    int
	saveForms    []url.Values
	checkCalls   int
	hasAccessHdr []string
}

func (f *fake189ShareAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/open/oauth2/ssoH5.action", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.tokenCalls++
		token := f.token
		f.mu.Unlock()
		if token == "" {
			// 未登录：回一个空报文的 200，让 extract189AccessToken 取不到。
			_, _ = w.Write([]byte(`{}`))
			return
		}
		write189JSON(t, w, map[string]any{"accessToken": token})
	})

	mux.HandleFunc("/open/share/getShareInfoByCodeV2.action", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		ok := f.metaOK
		shareID, rootID, name := f.shareID, f.rootID, f.fileName
		f.mu.Unlock()
		if !ok {
			write189JSON(t, w, map[string]any{"res_code": -1, "res_message": "分享不存在"})
			return
		}
		write189JSON(t, w, map[string]any{
			"res_code": 0, "shareId": shareID, "fileId": rootID,
			"shareMode": "3", "fileName": name, "isFolder": true,
		})
	})

	mux.HandleFunc("/open/share/listShareDir.action", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.listCalls++
		f.hasAccessHdr = append(f.hasAccessHdr, r.Header.Get("AccessToken"))
		entries := f.entries
		f.mu.Unlock()
		folders := make([]map[string]any, 0, len(entries))
		files := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			if b, _ := e["isFolder"].(bool); b {
				folders = append(folders, e)
			} else {
				files = append(files, e)
			}
		}
		write189JSON(t, w, map[string]any{
			"res_code": 0,
			"fileListAO": map[string]any{
				"count":      len(entries),
				"folderList": folders,
				"fileList":   files,
			},
		})
	})

	mux.HandleFunc("/open/batch/createBatchTask.action", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.saveForms = append(f.saveForms, r.PostForm)
		f.mu.Unlock()
		write189JSON(t, w, map[string]any{"res_code": 0, "taskId": 12345})
	})

	mux.HandleFunc("/open/batch/checkBatchTask.action", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		idx := f.checkCalls
		f.checkCalls++
		statuses := f.taskStatuses
		errMsg := f.taskError
		f.mu.Unlock()
		status := 3
		if len(statuses) > 0 {
			status = statuses[len(statuses)-1]
			if idx < len(statuses) {
				status = statuses[idx]
			}
		}
		payload := map[string]any{"res_code": 0, "taskStatus": status}
		if errMsg != "" {
			payload["errorCode"] = "1"
			payload["errorMessage"] = errMsg
		}
		write189JSON(t, w, payload)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// new189ShareDriver 把驱动的网页 base 指向桩服务，注入一份 Cookie。
func new189ShareDriver(t *testing.T, api *fake189ShareAPI) *Driver {
	t.Helper()
	srv := api.server(t)
	prev := shareWebBase
	shareWebBase = srv.URL
	t.Cleanup(func() { shareWebBase = prev })

	d := &Driver{client: httpx.NewClient(httpx.ClientOptions{Timeout: 5 * time.Second})}
	d.SetAuthCredentials(domain.AuthCredentials{Cookie: "SESSION=x; SSON=y"})
	return d
}

func Test189ShareReceiveCapabilitiesNeedsCookie(t *testing.T) {
	d := &Driver{}
	cap := d.ShareReceiveCapabilities()
	if cap.Ready {
		t.Fatal("没配 Cookie 时不该报告可用")
	}
	if !strings.Contains(cap.Reason, "Cookie") {
		t.Errorf("原因里要说清缺什么: %q", cap.Reason)
	}
	d.SetAuthCredentials(domain.AuthCredentials{Cookie: "SSON=y"})
	if !d.ShareReceiveCapabilities().Ready {
		t.Error("配了 Cookie 之后应当报告可用")
	}
}

func Test189ShareKinds(t *testing.T) {
	if got := (&Driver{}).ShareKinds(); got != driver.ShareKind189 {
		t.Errorf("ShareKinds = %q, want %q", got, driver.ShareKind189)
	}
}

// 转存三步要真的走全：换 token（带在列表请求头上）→ 列目录 → 提交 SHARE_SAVE。
func Test189ReceiveShareHappyPath(t *testing.T) {
	api := &fake189ShareAPI{
		token:    "AT-123",
		metaOK:   true,
		shareID:  "SHARE-1",
		rootID:   "ROOT-1",
		fileName: "某剧 S01",
		entries: []map[string]any{
			{"id": "F1", "name": "某剧 S01", "isFolder": true},
		},
	}
	d := new189ShareDriver(t, api)

	// 目标目录核对走签名接口 —— 这里让它直接走根目录（跳过核对）。
	got, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{
		ShareCode:   "abc123",
		ReceiveCode: "",
		TargetCID:   "",
	})
	if err != nil {
		t.Fatalf("ReceiveShare: %v", err)
	}
	if got.Count != 1 {
		t.Errorf("Count = %d, want 1", got.Count)
	}
	if api.tokenCalls == 0 {
		t.Error("没有调 ssoH5 换 token")
	}
	if api.listCalls != 1 {
		t.Errorf("listShareDir 调了 %d 次, want 1", api.listCalls)
	}
	for _, h := range api.hasAccessHdr {
		if h != "AT-123" {
			t.Errorf("列表请求没带上换来的 AccessToken: %q", h)
		}
	}
	if len(api.saveForms) != 1 {
		t.Fatalf("createBatchTask 调了 %d 次, want 1", len(api.saveForms))
	}
	form := api.saveForms[0]
	if form.Get("type") != "SHARE_SAVE" {
		t.Errorf("type = %q, want SHARE_SAVE", form.Get("type"))
	}
	if form.Get("shareId") != "SHARE-1" {
		t.Errorf("shareId = %q", form.Get("shareId"))
	}
	// taskInfos 必须带上条目 id 与 isFolder。
	if !strings.Contains(form.Get("taskInfos"), "F1") {
		t.Errorf("taskInfos 里没有条目 id: %s", form.Get("taskInfos"))
	}
}

// 分享失效：要在列目录之前就报 NOT_FOUND，且不发起任何写入。
func Test189ReceiveShareRejectsDeadShare(t *testing.T) {
	api := &fake189ShareAPI{token: "AT", metaOK: false}
	d := new189ShareDriver(t, api)

	_, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{ShareCode: "dead"})
	if err == nil {
		t.Fatal("分享失效时应当报错")
	}
	if ae, ok := domain.AsAppError(err); !ok || ae.Code != domain.CodeNotFound {
		t.Errorf("应当是 NOT_FOUND（确定性失败，不进重试队列）: %v", err)
	}
	if len(api.saveForms) != 0 {
		t.Fatal("分享失效时绝不能发起转存")
	}
}

// 没配 Cookie 时要在**发请求之前**就报出来。
func Test189ReceiveShareWithoutCookieFailsFast(t *testing.T) {
	d := &Driver{client: httpx.NewClient(httpx.ClientOptions{Timeout: time.Second})}
	_, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{ShareCode: "abc"})
	if err == nil || !strings.Contains(err.Error(), "Cookie") {
		t.Fatalf("应当在本地就拦下并说明缺 Cookie: %v", err)
	}
}

// 转存任务在网盘侧失败时，不能记成成功。
func Test189ReceiveSharePropagatesTaskFailure(t *testing.T) {
	api := &fake189ShareAPI{
		token: "AT", metaOK: true, shareID: "S1", rootID: "R1",
		entries:      []map[string]any{{"id": "F1", "name": "a.mkv"}},
		taskStatuses: []int{3},
		taskError:    "存储空间不足",
	}
	d := new189ShareDriver(t, api)

	_, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{ShareCode: "abc"})
	if err == nil {
		t.Fatal("任务失败时应当报错")
	}
	if !strings.Contains(err.Error(), "存储空间不足") {
		t.Errorf("报错应带上网盘给的原因: %v", err)
	}
}

// res_code 的三种形态都要判对：数字 0、字符串 "0"、缺失。
func Test189ResCodeOK(t *testing.T) {
	ok := map[string]bool{
		`null`:  true,
		`0`:     true,
		`"0"`:   true,
		`""`:    true,
		`1`:     false,
		`-1`:    false,
		`"100"`: false,
		`"err"`: false,
	}
	for raw, want := range ok {
		if got := is189ResCodeOK(json.RawMessage(raw)); got != want {
			t.Errorf("is189ResCodeOK(%s) = %v, want %v", raw, got, want)
		}
	}
}

func Test189AccessTokenFromURL(t *testing.T) {
	cases := map[string]string{
		"https://cloud.189.cn/web/main.action?accessToken=ABC": "ABC",
		"https://cloud.189.cn/x#accessToken=XYZ":               "XYZ",
		"https://cloud.189.cn/web/main.action":                 "",
		"":                                                     "",
	}
	for raw, want := range cases {
		if got := accessTokenFromURL(raw); got != want {
			t.Errorf("accessTokenFromURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

// PeekShare 没配 Cookie 时也该能读到分享元信息（这部分接口不吃登录态）。
func Test189PeekShare(t *testing.T) {
	api := &fake189ShareAPI{token: "", metaOK: true, shareID: "S1", rootID: "R1", fileName: "某剧 S01"}
	d := new189ShareDriver(t, api)
	got, err := d.PeekShare(context.Background(), driver.SharePeekRequest{ShareCode: "abc"})
	if err != nil {
		t.Fatalf("PeekShare: %v", err)
	}
	if got.Title != "某剧 S01" {
		t.Errorf("Title = %q", got.Title)
	}
}
