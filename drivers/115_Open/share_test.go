package pan115open

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

// 网页接口的 errno 两种拼写都要认：实测 990001 那条响应里 errNo 与 errno 同时出现。
func TestWebEnvelopeCode(t *testing.T) {
	cases := map[string]int64{
		`{"errNo":990001}`:       990001,
		`{"errno":4100010}`:      4100010,
		`{"errNo":0,"errno":77}`: 77,
		`{"state":true}`:         0,
	}
	for raw, want := range cases {
		var env webEnvelope
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		if got := env.code(); got != want {
			t.Errorf("code(%s) = %d, want %d", raw, got, want)
		}
	}
}

// 每个已知 errno 都要翻成能指导下一步动作的中文，而不是把数字抛给用户。
func TestMapWebErrno(t *testing.T) {
	cases := []struct {
		code     int64
		contains string
		appCode  domain.ErrorCode
	}{
		{990001, "Cookie", domain.CodeAuthExpired},
		{990002, "分享不存在", domain.CodeNotFound},
		{4100010, "参数错误", domain.CodeValidation},
		{4100008, "提取码", domain.CodeValidation},
		// 重复转存：必须落在**确定性失败**那一档，否则会白重试 5 次。
		{4200045, "已接收过", domain.CodeValidation},
		{12345, "115 网页接口错误", domain.CodeDriverError},
	}
	for _, tc := range cases {
		err := mapWebErrno(tc.code, "")
		ae, ok := domain.AsAppError(err)
		if !ok {
			t.Errorf("errno %d 没有返回 AppError: %v", tc.code, err)
			continue
		}
		if ae.Code != tc.appCode {
			t.Errorf("errno %d 的 code = %s, want %s", tc.code, ae.Code, tc.appCode)
		}
		if !strings.Contains(err.Error(), tc.contains) {
			t.Errorf("errno %d 的文案 %q 里没有 %q", tc.code, err.Error(), tc.contains)
		}
	}
}

// HTTP 状态码要说结论，不要说「请重试」—— 403/405 重试多少次都不会好。
func TestWebHTTPErrorStatesCause(t *testing.T) {
	hk := webHTTPError(http.StatusForbidden, nil)
	if !strings.Contains(hk.Error(), "香港") {
		t.Errorf("403 没说明是出口 IP 被屏蔽: %v", hk)
	}
	if !strings.Contains(hk.Error(), "换") {
		t.Errorf("403 没给出处置办法: %v", hk)
	}
	risk := webHTTPError(405, nil)
	if !strings.Contains(risk.Error(), "风控") {
		t.Errorf("405 没说明是风控: %v", risk)
	}
	if ae, ok := domain.AsAppError(risk); !ok || ae.Code != domain.CodeRateLimited {
		t.Errorf("405 应当是可读的限流错误: %v", risk)
	}
	if ae, ok := domain.AsAppError(webHTTPError(http.StatusUnauthorized, nil)); !ok || ae.Code != domain.CodeAuthExpired {
		t.Errorf("401 应当映射成 Cookie 失效: %v", webHTTPError(http.StatusUnauthorized, nil))
	}
}

// 面包屑拼接：第一项固定是根目录，不计入路径。
func TestJoinBreadcrumb(t *testing.T) {
	entry := func(name, cid string) webPathEntry { return webPathEntry{Name: name, Cid: cid} }
	cases := []struct {
		name    string
		entries []webPathEntry
		want    string
	}{
		{"空", nil, "/"},
		{"只有根", []webPathEntry{entry("根目录", "0")}, "/"},
		{"一层", []webPathEntry{entry("根目录", "0"), entry("电视剧", "310")}, "/电视剧"},
		{"两层", []webPathEntry{entry("根目录", "0"), entry("电视剧", "310"), entry("生逢其时", "349")}, "/电视剧/生逢其时"},
		// 名字里带空白的项跳过，不能让空白拼出一个「//」来。
		{"空白项", []webPathEntry{entry("根目录", "0"), entry("  ", "310")}, "/"},
	}
	for _, tc := range cases {
		if got := joinBreadcrumb(tc.entries); got != tc.want {
			t.Errorf("%s: joinBreadcrumb = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// 目录核对只容忍写法差异，不容忍真的不同。
func TestSameDirPath(t *testing.T) {
	same := [][2]string{
		{"/电视剧/生逢其时", "电视剧/生逢其时"},
		{"/电视剧/生逢其时/", "/电视剧/生逢其时"},
		{"/", ""},
		{"\\电视剧\\生逢其时", "/电视剧/生逢其时"},
	}
	for _, pair := range same {
		if !sameDirPath(pair[0], pair[1]) {
			t.Errorf("%q 与 %q 应当判为同一目录", pair[0], pair[1])
		}
	}
	diff := [][2]string{
		{"/电视剧/生逢其时", "/电视剧"},
		// 115 是大小写敏感的目录名，不能当同一个。
		{"/Movies", "/movies"},
		{"/", "/电视剧"},
	}
	for _, pair := range diff {
		if sameDirPath(pair[0], pair[1]) {
			t.Errorf("%q 与 %q 不该判为同一目录", pair[0], pair[1])
		}
	}
}

func TestShareReceiveCapabilitiesNeedsCookie(t *testing.T) {
	d := &Driver{}
	cap := d.ShareReceiveCapabilities()
	if cap.Ready {
		t.Fatal("没配 Cookie 时不该报告可用")
	}
	if !strings.Contains(cap.Reason, "Cookie") {
		t.Errorf("原因里要说清缺什么、去哪儿配: %q", cap.Reason)
	}
	d.SetAuthCredentials(domain.AuthCredentials{Cookie: "UID=x; CID=y"})
	if !d.ShareReceiveCapabilities().Ready {
		t.Error("配了 Cookie 之后应当报告可用")
	}
}

// fakeShareAPI 打桩 115 网页接口，并记录被调用了哪些端点。
type fakeShareAPI struct {
	mu sync.Mutex
	// pathBreadcrumb 是 /files 返回的面包屑（用于核对目标目录）。
	pathBreadcrumb []webPathEntry
	shareState     int
	userID         string
	entryFIDs      []string
	// snapList 直接指定 /share/snap 的 list；为空时按 entryFIDs 生成文件条目。
	// 需要**文件夹条目**（有 cid、没有 fid）时必须用它。
	snapList []map[string]any
	// entries 是 /files 的 data 数组（真实响应里 data 就是文件条目列表）。
	entries      []map[string]any
	receiveCalls []url.Values
	receiveForm  url.Values
}

func (f *fakeShareAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/files", func(w http.ResponseWriter, r *http.Request) {
		// ⚠️ 形状必须与真实响应一致：path 与 data 是**顶层兄弟字段**，data 是数组。
		// 早先这里写成 `data: {count, path}`，把「整包解析」这个要求整个盖住了，
		// 结果真实环境一跑就报 `cannot unmarshal array into ...filesResponse`。
		writeJSON(t, w, map[string]any{
			"state": true, "errno": 0, "count": len(f.entries),
			"path": f.pathBreadcrumb,
			"data": f.entries,
		})
	})
	mux.HandleFunc("/share/snap", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		list := f.snapList
		if list == nil {
			list = make([]map[string]any, 0, len(f.entryFIDs))
			for _, fid := range f.entryFIDs {
				list = append(list, map[string]any{"fid": fid, "n": "e.mkv", "s": 100})
			}
		}
		writeJSON(t, w, map[string]any{
			"state": true, "errno": 0,
			"data": map[string]any{
				"count":       len(list),
				"list":        list,
				"share_state": f.shareState,
				"shareinfo":   map[string]any{"share_title": "x"},
				"userinfo":    map[string]any{"user_id": f.userID, "user_name": "u"},
			},
		})
	})
	mux.HandleFunc("/share/receive", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.receiveCalls = append(f.receiveCalls, r.PostForm)
		f.receiveForm = r.PostForm
		f.mu.Unlock()
		writeJSON(t, w, map[string]any{
			"state": true, "errno": 0,
			"data": map[string]any{"file_ids": []string{"9001"}, "count": 1},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Errorf("encode: %v", err)
	}
}

// newShareTestDriver 把驱动的 webBaseURL 指向桩服务，并注入一份「Cookie」。
func newShareTestDriver(t *testing.T, api *fakeShareAPI) *Driver {
	t.Helper()
	srv := api.server(t)
	prev := webBaseURL
	webBaseURL = srv.URL
	t.Cleanup(func() { webBaseURL = prev })

	d := &Driver{client: httpx.NewClient(httpx.ClientOptions{Timeout: 5 * time.Second})}
	d.SetAuthCredentials(domain.AuthCredentials{Cookie: "UID=1; CID=2; SEID=3"})
	return d
}

func TestReceiveShareHappyPath(t *testing.T) {
	api := &fakeShareAPI{
		pathBreadcrumb: []webPathEntry{{Name: "根目录", Cid: "0"}, {Name: "电视剧", Cid: "310"}, {Name: "生逢其时 (2026)", Cid: "349"}},
		shareState:     1,
		userID:         "342290996",
		entryFIDs:      []string{"111", "222"},
	}
	d := newShareTestDriver(t, api)

	got, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{
		ShareCode:   "swsa2t23zrk",
		ReceiveCode: "t58d",
		TargetCID:   "349",
		TargetPath:  "/电视剧/生逢其时 (2026)",
	})
	if err != nil {
		t.Fatalf("ReceiveShare: %v", err)
	}
	if got.TargetPath != "/电视剧/生逢其时 (2026)" {
		t.Errorf("TargetPath = %q", got.TargetPath)
	}
	if got.Count != 1 {
		t.Errorf("Count = %d", got.Count)
	}
	if len(api.receiveCalls) != 1 {
		t.Fatalf("receive 被调用了 %d 次，应当恰好 1 次", len(api.receiveCalls))
	}
	// user_id 必须来自 snap 的 data.userinfo —— 拿成 shareinfo 里的字段会是空串，
	// 表单一交上去就失败，而且报错完全看不出原因。
	if api.receiveForm.Get("user_id") != "342290996" {
		t.Errorf("user_id = %q", api.receiveForm.Get("user_id"))
	}
	if api.receiveForm.Get("cid") != "349" {
		t.Errorf("cid = %q", api.receiveForm.Get("cid"))
	}
	if api.receiveForm.Get("share_code") != "swsa2t23zrk" {
		t.Errorf("share_code = %q", api.receiveForm.Get("share_code"))
	}
	if api.receiveForm.Get("receive_code") != "t58d" {
		t.Errorf("receive_code = %q", api.receiveForm.Get("receive_code"))
	}
	// 整份接收 = 分享根下的全部条目一起交上去。
	if api.receiveForm.Get("file_id") != "111,222" {
		t.Errorf("file_id = %q, want \"111,222\"", api.receiveForm.Get("file_id"))
	}
}

// 分享的根是**文件夹**时也要能转存。
//
// 实测（2026-09-18，浴血黑帮 第六季）：这种分享的 snap 条目只有 cid、没有 fid，
// 只认 fid 会让它 100% 失败，还报成「没有可转存的文件」—— 而 115 频道里
// 绝大多数分享的根都是文件夹，只有原盘 ISO 那种才是单文件。
func TestReceiveShareAcceptsFolderEntry(t *testing.T) {
	api := &fakeShareAPI{
		pathBreadcrumb: []webPathEntry{{Name: "根目录", Cid: "0"}, {Name: "电视剧", Cid: "349"}},
		shareState:     1,
		userID:         "344385180",
		// 真实响应形状：有 cid、没有 fid。
		snapList: []map[string]any{
			{"cid": "3169479781959860045", "pid": "0", "n": "浴血黑帮 第六季 杜比视界 NF版", "s": 52721994057},
		},
	}
	d := newShareTestDriver(t, api)

	if _, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{
		ShareCode: "swwwjyn3no3", ReceiveCode: "l822", TargetCID: "349",
	}); err != nil {
		t.Fatalf("根是文件夹的分享应当能转存: %v", err)
	}
	// receive 的 file_id 收的就是「文件(夹)ID」，文件夹交它的 cid。
	if got := api.receiveForm.Get("file_id"); got != "3169479781959860045" {
		t.Errorf("file_id = %q, want 文件夹的 cid", got)
	}
}

// 文件条目的 cid 是数字 0、文件夹的是字符串。收错类型会让**本来能用的单文件
// 分享**整包解析失败 —— 那是比原 bug 更糟的回归，所以单独钉一次。
func TestReceiveShareParsesMixedIDTypes(t *testing.T) {
	api := &fakeShareAPI{
		pathBreadcrumb: []webPathEntry{{Name: "根目录", Cid: "0"}, {Name: "电影", Cid: "777"}},
		shareState:     1,
		userID:         "1",
		snapList: []map[string]any{
			{"fid": "3407154935790578358", "cid": 0, "n": "刀.iso", "s": 94074241024},
		},
	}
	d := newShareTestDriver(t, api)

	if _, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{
		ShareCode: "swfj1o236ty", TargetCID: "777",
	}); err != nil {
		t.Fatalf("单文件分享应当能转存: %v", err)
	}
	// fid 优先：数字 0 的 cid 不能顶上来。
	if got := api.receiveForm.Get("file_id"); got != "3407154935790578358" {
		t.Errorf("file_id = %q, want 文件的 fid", got)
	}
}

// 条目拿不到 ID 时，报错要指向接口形状，而不是让人去查「分享是不是空的」。
func TestReceiveShareReportsStructuralFailureDistinctly(t *testing.T) {
	api := &fakeShareAPI{
		pathBreadcrumb: []webPathEntry{{Name: "根目录", Cid: "0"}},
		shareState:     1,
		userID:         "1",
		snapList:       []map[string]any{{"n": "??", "s": 1}},
	}
	d := newShareTestDriver(t, api)

	_, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{ShareCode: "abc", TargetCID: "0"})
	if err == nil {
		t.Fatal("取不到可转存的 ID 时应当报错")
	}
	if !strings.Contains(err.Error(), "接口结构") {
		t.Errorf("报错应指向接口结构而不是分享本身: %v", err)
	}
	if len(api.receiveCalls) != 0 {
		t.Fatal("取不到 ID 时不该发起 receive")
	}
}

// 目标目录对不上时**绝不能**发起 receive —— 这道闸的全部意义就在这里。
func TestReceiveShareRefusesMismatchedTarget(t *testing.T) {
	api := &fakeShareAPI{
		pathBreadcrumb: []webPathEntry{{Name: "根目录", Cid: "0"}, {Name: "电影", Cid: "777"}},
		shareState:     1,
		userID:         "342290996",
		entryFIDs:      []string{"111"},
	}
	d := newShareTestDriver(t, api)

	_, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{
		ShareCode:  "swsa2t23zrk",
		TargetCID:  "777",
		TargetPath: "/电视剧/生逢其时 (2026)",
	})
	if err == nil {
		t.Fatal("目录对不上时应当报错")
	}
	if !strings.Contains(err.Error(), "目标目录对不上") {
		t.Errorf("报错没说清是目录对不上: %v", err)
	}
	if len(api.receiveCalls) != 0 {
		t.Fatal("目录核对失败后仍然写入了网盘 —— 这正是这道闸要防的事故")
	}
}

// TargetPath 留空时只解析不比对（调用方没要求核对）。
func TestReceiveShareSkipsCheckWithoutExpectedPath(t *testing.T) {
	api := &fakeShareAPI{
		pathBreadcrumb: []webPathEntry{{Name: "根目录", Cid: "0"}, {Name: "电影", Cid: "777"}},
		shareState:     1,
		userID:         "1",
		entryFIDs:      []string{"111"},
	}
	d := newShareTestDriver(t, api)

	got, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{
		ShareCode: "abc", TargetCID: "777",
	})
	if err != nil {
		t.Fatalf("ReceiveShare: %v", err)
	}
	if got.TargetPath != "/电影" {
		t.Errorf("TargetPath = %q（应回报实际解析到的路径）", got.TargetPath)
	}
	if len(api.receiveCalls) != 1 {
		t.Fatal("没要求核对时不该被拦下")
	}
}

func TestReceiveShareRejectsBadShareState(t *testing.T) {
	api := &fakeShareAPI{
		pathBreadcrumb: []webPathEntry{{Name: "根目录", Cid: "0"}},
		shareState:     0,
		userID:         "1",
		entryFIDs:      []string{"111"},
	}
	d := newShareTestDriver(t, api)

	_, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{ShareCode: "abc", TargetCID: "0"})
	if err == nil {
		t.Fatal("分享失效时应当报错")
	}
	if !strings.Contains(err.Error(), "不可用") {
		t.Errorf("报错没说清分享不可用: %v", err)
	}
	if len(api.receiveCalls) != 0 {
		t.Fatal("分享失效时不该发起 receive")
	}
}

func TestReceiveShareRejectsEmptyShareCode(t *testing.T) {
	d := &Driver{}
	if _, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{ShareCode: "  "}); err == nil {
		t.Fatal("空分享码应当报错")
	}
}

// 没配 Cookie 时要在**发请求之前**就报出来，而不是去撞一个「登录超时」。
func TestReceiveShareWithoutCookieFailsFast(t *testing.T) {
	d := &Driver{}
	_, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{ShareCode: "abc"})
	if err == nil || !strings.Contains(err.Error(), "Cookie") {
		t.Fatalf("应当在本地就拦下并说明缺 Cookie: %v", err)
	}
}

// 偷看分享**不需要 Cookie** —— 这是它存在的全部理由。
//
// 实测（2026-09-17）：`share/snap` 连裸请求都能拿到完整响应，
// 只有写入的 `share/receive` 才要登录态。如果哪天这里开始报「缺 Cookie」，
// 说明有人把凭据检查加到了不该加的地方。
func TestPeekShareNeedsNoCookie(t *testing.T) {
	api := &fakeShareAPI{
		shareState: 1,
		userID:     "342290996",
		entryFIDs:  []string{"111"},
	}
	srv := api.server(t)
	prev := webBaseURL
	webBaseURL = srv.URL
	defer func() { webBaseURL = prev }()

	// 刻意**不注入任何凭据**。
	d := &Driver{client: httpx.NewClient(httpx.ClientOptions{Timeout: 5 * time.Second})}

	got, err := d.PeekShare(context.Background(), driver.SharePeekRequest{ShareCode: "swsa2t23zrk"})
	if err != nil {
		t.Fatalf("没配 Cookie 时也该能偷看: %v", err)
	}
	if got.OwnerID != "342290996" {
		t.Errorf("OwnerID = %q", got.OwnerID)
	}
	if got.FileName == "" {
		t.Error("应当带出分享里第一个文件的名字")
	}
	if api.receiveForm != nil {
		t.Fatal("偷看绝不能触发 receive —— 那是写操作")
	}
}

func TestPeekShareRejectsBadShareState(t *testing.T) {
	api := &fakeShareAPI{shareState: 0, userID: "1", entryFIDs: []string{"1"}}
	srv := api.server(t)
	prev := webBaseURL
	webBaseURL = srv.URL
	defer func() { webBaseURL = prev }()

	d := &Driver{client: httpx.NewClient(httpx.ClientOptions{Timeout: 5 * time.Second})}
	if _, err := d.PeekShare(context.Background(), driver.SharePeekRequest{ShareCode: "abc"}); err == nil {
		t.Fatal("分享失效时应当报错")
	}
}

func TestPeekShareRejectsEmptyCode(t *testing.T) {
	d := &Driver{}
	if _, err := d.PeekShare(context.Background(), driver.SharePeekRequest{ShareCode: "  "}); err == nil {
		t.Fatal("空分享码应当报错")
	}
}
