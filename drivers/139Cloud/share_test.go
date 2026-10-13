package cloud139

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

func write139JSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Errorf("encode: %v", err)
	}
}

// fake139ShareAPI 打桩分享侧与个人云两个宿主。
type fake139ShareAPI struct {
	mu sync.Mutex

	// shareOK 为 false 时 getOutLinkInfoV6 回业务错误。
	shareOK  bool
	shareMsg string
	caLst    []map[string]any
	coLst    []map[string]any

	// taskStatuses 依次回给 hcy/task/get。
	taskStatuses []string

	createCalls int
	taskCalls   int
	outerBody   map[string]any
}

func (f *fake139ShareAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/yun-share/richlifeApp/devapp/IOutLink/getOutLinkInfoV6", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		ok, msg, ca, co := f.shareOK, f.shareMsg, f.caLst, f.coLst
		f.mu.Unlock()
		if !ok {
			write139JSON(t, w, map[string]any{"success": false, "code": "1", "message": msg})
			return
		}
		write139JSON(t, w, map[string]any{
			"success": true, "code": "0",
			"data": map[string]any{"caLst": ca, "coLst": co},
		})
	})

	mux.HandleFunc("/yun-share/richlifeApp/devapp/IBatchOprTask/createOuterLinkBatchOprTask", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.createCalls++
		f.outerBody = body
		f.mu.Unlock()
		write139JSON(t, w, map[string]any{
			"success": true, "code": "0",
			"data": map[string]any{"taskID": "T-1"},
		})
	})

	mux.HandleFunc("/hcy/task/get", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		idx := f.taskCalls
		f.taskCalls++
		statuses := f.taskStatuses
		f.mu.Unlock()
		status := "2"
		if len(statuses) > 0 {
			status = statuses[len(statuses)-1]
			if idx < len(statuses) {
				status = statuses[idx]
			}
		}
		write139JSON(t, w, map[string]any{
			"success": true, "code": "0",
			"data": map[string]any{"taskId": "T-1", "status": status},
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// new139ShareDriver 把两个宿主都指向桩服务，并注入一份「登录令牌」。
func new139ShareDriver(t *testing.T, api *fake139ShareAPI) *Driver {
	t.Helper()
	srv := api.server(t)
	prevShare, prevPersonal := shareHostBase, personalShareHostURL
	shareHostBase, personalShareHostURL = srv.URL, srv.URL
	t.Cleanup(func() { shareHostBase, personalShareHostURL = prevShare, prevPersonal })

	d := &Driver{client: httpx.NewClient(httpx.ClientOptions{Timeout: 5 * time.Second})}
	// account 不是从 SetAuthCredentials 来的（那是 Authorization 的原始串），
	// 所以测试里直接塞字段 —— 生产路径由 parseAuthorization 填。
	d.account = "13800138000"
	return d
}

func Test139ShareReceiveCapabilitiesNeedsLogin(t *testing.T) {
	d := &Driver{}
	if d.ShareReceiveCapabilities().Ready {
		t.Fatal("没登录时不该报告可用")
	}
	if !strings.Contains(d.ShareReceiveCapabilities().Reason, "登录") {
		t.Errorf("原因要说清缺什么: %q", d.ShareReceiveCapabilities().Reason)
	}

	// 有令牌但解析不出手机号：也要挡住 —— msisdn 是转存请求体的必填项。
	d.authorization = "abc"
	if d.ShareReceiveCapabilities().Ready {
		t.Fatal("解析不出手机号时不该报告可用")
	}

	d.account = "13800138000"
	if !d.ShareReceiveCapabilities().Ready {
		t.Error("登录且能取到手机号时应当报告可用")
	}
}

func Test139ShareKinds(t *testing.T) {
	if got := (&Driver{}).ShareKinds(); got != driver.ShareKind139 {
		t.Errorf("ShareKinds = %q, want %q", got, driver.ShareKind139)
	}
}

func Test139ReceiveShareHappyPath(t *testing.T) {
	api := &fake139ShareAPI{
		shareOK: true,
		caLst: []map[string]any{
			{"catalogID": "CA1", "catalogName": "某剧 S01", "path": "某剧 S01"},
		},
		coLst: []map[string]any{
			{"contentID": "CO1", "contentName": "01.mkv", "path": "01.mkv", "coSize": 100},
		},
	}
	d := new139ShareDriver(t, api)

	got, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{
		ShareCode: "linkABC123",
		TargetCID: "", // 根目录：跳过核对
	})
	if err != nil {
		t.Fatalf("ReceiveShare: %v", err)
	}
	if got.Count != 2 {
		t.Errorf("Count = %d, want 2", got.Count)
	}
	if api.createCalls != 1 {
		t.Fatalf("createOuterLinkBatchOprTask 调了 %d 次, want 1", api.createCalls)
	}
	// 转存交上去的是**分享侧路径**，不是 id。
	req, _ := api.outerBody["createOuterLinkBatchOprTaskReq"].(map[string]any)
	if req == nil {
		t.Fatalf("请求体形状不对: %+v", api.outerBody)
	}
	if req["msisdn"] != "13800138000" {
		t.Errorf("msisdn = %v", req["msisdn"])
	}
	taskInfo, _ := req["taskInfo"].(map[string]any)
	if taskInfo == nil {
		t.Fatalf("缺 taskInfo: %+v", req)
	}
	co, _ := taskInfo["contentInfoList"].([]any)
	ca, _ := taskInfo["catalogInfoList"].([]any)
	if len(co) != 1 || co[0] != "01.mkv" {
		t.Errorf("contentInfoList = %v", taskInfo["contentInfoList"])
	}
	if len(ca) != 1 || ca[0] != "某剧 S01" {
		t.Errorf("catalogInfoList = %v", taskInfo["catalogInfoList"])
	}
}

// 分享失效：报 NOT_FOUND（确定性失败），且不发起转存。
func Test139ReceiveShareRejectsDeadShare(t *testing.T) {
	api := &fake139ShareAPI{shareOK: false, shareMsg: "分享不存在或已失效"}
	d := new139ShareDriver(t, api)

	_, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{ShareCode: "dead"})
	if err == nil {
		t.Fatal("分享失效时应当报错")
	}
	if ae, ok := domain.AsAppError(err); !ok || ae.Code != domain.CodeNotFound {
		t.Errorf("应当是 NOT_FOUND: %v", err)
	}
	if api.createCalls != 0 {
		t.Fatal("分享失效时绝不能发起转存")
	}
}

// 转存任务在网盘侧失败时，不能记成成功。
func Test139ReceiveSharePropagatesTaskFailure(t *testing.T) {
	api := &fake139ShareAPI{
		shareOK:      true,
		coLst:        []map[string]any{{"contentID": "CO1", "contentName": "a.mkv", "path": "a.mkv"}},
		taskStatuses: []string{"3"},
	}
	d := new139ShareDriver(t, api)

	_, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{ShareCode: "abc"})
	if err == nil {
		t.Fatal("任务失败时应当报错")
	}
	if !strings.Contains(err.Error(), "转存失败") {
		t.Errorf("报错要说明是转存失败: %v", err)
	}
}

// 没登录时要在发请求之前就拦下。
func Test139ReceiveShareWithoutLoginFailsFast(t *testing.T) {
	d := &Driver{client: httpx.NewClient(httpx.ClientOptions{Timeout: time.Second})}
	_, err := d.ReceiveShare(context.Background(), driver.ShareReceiveRequest{ShareCode: "abc"})
	if err == nil {
		t.Fatal("没登录时应当报错")
	}
}

// PeekShare 也要能在不写入的前提下拿到标题与条目数。
func Test139PeekShare(t *testing.T) {
	api := &fake139ShareAPI{
		shareOK: true,
		coLst: []map[string]any{
			{"contentID": "CO1", "contentName": "a.mkv", "path": "a.mkv", "coSize": 100},
			{"contentID": "CO2", "contentName": "b.mkv", "path": "b.mkv", "coSize": 200},
		},
	}
	d := new139ShareDriver(t, api)
	got, err := d.PeekShare(context.Background(), driver.SharePeekRequest{ShareCode: "abc"})
	if err != nil {
		t.Fatalf("PeekShare: %v", err)
	}
	if got.FileCount != 2 {
		t.Errorf("FileCount = %d, want 2", got.FileCount)
	}
	if got.TotalSize != 300 {
		t.Errorf("TotalSize = %d, want 300", got.TotalSize)
	}
	if api.createCalls != 0 {
		t.Fatal("偷看绝不能触发转存")
	}
}
