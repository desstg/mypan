package pan115open

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"litepan/internal/driver"
)

// roundTripFunc 把 http.Client 的传输层换成函数，好在不碰网络的前提下
// 同时假造开放平台的 add_task_urls 与 get_task_list 两个接口。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// 10008「任务已存在」时，必须靠 **btih 比对**把 info hash 补回来 ——
// 哪怕网盘回显的 URL 与提交时**不是同一个形态**。
//
// 这是真机踩到的：同一颗种子在 115 的任务列表里有两个任务，一个的 url 是裸磁力、
// 另一个带 `&dn=...&xl=...` 一串参数。原来的实现只做 URL 全等比较，直接漏掉，
// 于是「这颗种子其实早就在网盘上了」被记成一次推送失败 ——
// 用户看到的是 `推送失败：115 API 错误(10008)`，而文件好好地在网盘上。
func TestRecoverOfflineHashMatchesByBtihWhenURLDiffers(t *testing.T) {
	const hash = "1767ed7a1e99b9d5a70c03d03ad7ad0995ba3b48"
	// 提交的链接带 dn/xl 参数。
	submitted := "magnet:?xt=urn:btih:" + strings.ToUpper(hash) + "&dn=Toy.Story.5&xl=12345"
	// 网盘回显的是**裸磁力**，URL 全等比不出来。
	listed := "magnet:?xt=urn:btih:" + strings.ToUpper(hash)

	d := &Driver{}
	d.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "add_task_urls"):
			return jsonResponse(`{"state":true,"data":[{"state":false,"code":10008,` +
				`"message":"任务已存在，请勿输入重复的链接地址","url":"` + submitted + `"}]}`), nil
		case strings.Contains(r.URL.Path, "get_task_list"):
			return jsonResponse(`{"state":true,"data":{"page":1,"page_count":1,"count":1,"tasks":[` +
				`{"info_hash":"` + hash + `","url":"` + listed + `","name":"Toy Story 5","status":2}]}}`), nil
		}
		t.Fatalf("未预期的请求：%s", r.URL.Path)
		return nil, nil
	})}

	results, err := d.AddOfflineURLs(context.Background(), driver.OfflineURLRequest{
		URLs: []string{submitted}, ParentID: "0", FileName: "玩具总动员5 (2026)",
	})
	if err != nil {
		t.Fatalf("AddOfflineURLs: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("结果数 = %d，want 1", len(results))
	}
	if got := strings.ToLower(results[0].InfoHash); got != hash {
		t.Fatalf("info hash 没补回来：got %q, want %q\n"+
			"（URL 全等比不出来时要用 btih 比 —— 这正是 10008 最常见的形态）", got, hash)
	}
	// Success 仍然由网盘说了算：它回的是 false，我们不去改判它成功。
	// 公共层拿到 InfoHash 之后会自己认出「这颗种子已经在网盘上了」。
	if results[0].Success {
		t.Error("不该把网盘的 false 改成 true")
	}
}

// 115 拒收时**不回 url 字段**，只给 code/message —— 这时要按位置回填请求里的链接，
// 否则「这颗种子是不是已经在任务列表里」的兜底一条都对不上。
//
// 真机踩到：记录里写着「推送失败：115 API 错误(10008)」，
// 而那颗种子好好地在网盘任务列表第 2 页上，目录里文件也在。
func TestAddOfflineURLsFillsSourceFromRequestWhenResponseOmitsIt(t *testing.T) {
	const hash = "1767ed7a1e99b9d5a70c03d03ad7ad0995ba3b48"
	submitted := "magnet:?xt=urn:btih:" + strings.ToUpper(hash)

	d := &Driver{}
	d.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "add_task_urls") {
			// ⚠️ 关键：**没有 url 字段**，只有 code 与 message。
			return jsonResponse(`{"state":true,"data":[{"state":false,"code":10008,` +
				`"message":"任务已存在，请勿输入重复的链接地址"}]}`), nil
		}
		// 任务列表在第 2 页才出现 —— 兜底必须翻到那里。
		if strings.Contains(r.URL.Query().Get("page"), "2") {
			return jsonResponse(`{"state":true,"data":{"page":2,"page_count":65,"count":1940,"tasks":[` +
				`{"info_hash":"` + hash + `","url":"` + submitted + `","name":"Toy Story 5","status":2}]}}`), nil
		}
		return jsonResponse(`{"state":true,"data":{"page":1,"page_count":65,"count":1940,"tasks":[` +
			`{"info_hash":"0000000000000000000000000000000000000000","url":"magnet:?xt=urn:btih:0","name":"x"}]}}`), nil
	})}

	results, err := d.AddOfflineURLs(context.Background(), driver.OfflineURLRequest{
		URLs: []string{submitted}, ParentID: "0", FileName: "玩具总动员5 (2026)",
	})
	if err != nil {
		t.Fatalf("AddOfflineURLs: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("结果数 = %d", len(results))
	}
	if !results[0].AlreadyExists {
		t.Fatalf("没认出「已在网盘」：%+v\n"+
			"（115 拒收时不回 url，必须按位置回填请求里的链接才能比对）", results[0])
	}
	if strings.ToLower(results[0].InfoHash) != hash {
		t.Errorf("info hash = %q, want %q", results[0].InfoHash, hash)
	}
}

// 10008 是**信封层**的失败（`state:false` + `data:[]`），不是数组里某一项 ——
// 所以那个「按位置回填 Source」的循环根本走不到，兜底必须在错误分支上做。
//
// 这是真机踩到的真实响应形状：
//
//	{"state":false,"code":10008,"message":"任务已存在，请勿输入重复的链接地址","data":[]}
func TestAddOfflineURLsHandlesEnvelopeLevelDuplicate(t *testing.T) {
	const hash = "1767ed7a1e99b9d5a70c03d03ad7ad0995ba3b48"
	// 库里的形态是百分号编码的。
	submitted := "magnet:?xt=urn%3Abtih%3A" + strings.ToUpper(hash)

	d := &Driver{}
	d.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "add_task_urls") {
			// ⚠️ 整条响应失败，data 是空数组。
			return jsonResponse(`{"state":false,"code":10008,` +
				`"message":"任务已存在，请勿输入重复的链接地址","data":[]}`), nil
		}
		// 任务列表里那颗种子回显的是**裸冒号**形态，与库里的编码形态字面不等。
		return jsonResponse(`{"state":true,"data":{"page":1,"page_count":65,"count":1940,"tasks":[` +
			`{"info_hash":"` + hash + `",` +
			`"url":"magnet:?xt=urn:btih:` + strings.ToUpper(hash) + `","name":"Toy Story 5","status":2}]}}`), nil
	})}

	results, err := d.AddOfflineURLs(context.Background(), driver.OfflineURLRequest{
		URLs: []string{submitted}, ParentID: "0", FileName: "玩具总动员5 (2026)",
	})
	if err != nil {
		t.Fatalf("信封层的 10008 应当被认成「已在网盘」而不是报错，实际: %v", err)
	}
	if len(results) != 1 || !results[0].AlreadyExists {
		t.Fatalf("没认出「已在网盘」：%+v", results)
	}
	if strings.ToLower(results[0].InfoHash) != hash {
		t.Errorf("info hash = %q, want %q", results[0].InfoHash, hash)
	}
}

// 认不出来的失败照旧原样抛回去 —— 不能把真实的提交错误吞掉。
func TestAddOfflineURLsKeepsUnrecognizedError(t *testing.T) {
	d := &Driver{}
	d.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "add_task_urls") {
			return jsonResponse(`{"state":false,"code":10004,"message":"链接不合法","data":[]}`), nil
		}
		return jsonResponse(`{"state":true,"data":{"page":1,"page_count":1,"tasks":[]}}`), nil
	})}

	_, err := d.AddOfflineURLs(context.Background(), driver.OfflineURLRequest{
		URLs: []string{"magnet:?xt=urn%3Abtih:" + strings.Repeat("a", 40)},
	})
	if err == nil {
		t.Fatal("认不出来的失败必须抛回去")
	}
	if !strings.Contains(err.Error(), "10004") {
		t.Errorf("错误内容应当保留，实际 %v", err)
	}
}

// URL 全等的老路径不能被改坏。
func TestRecoverOfflineHashMatchesByURL(t *testing.T) {
	const hash = "abcdef0123456789abcdef0123456789abcdef01"
	url := "magnet:?xt=urn:btih:" + hash

	d := &Driver{}
	d.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "add_task_urls") {
			return jsonResponse(`{"state":true,"data":[{"state":false,"code":10008,"url":"` + url + `"}]}`), nil
		}
		return jsonResponse(`{"state":true,"data":{"page":1,"page_count":1,"tasks":[` +
			`{"info_hash":"` + hash + `","url":"` + url + `"}]}}`), nil
	})}

	results, _ := d.AddOfflineURLs(context.Background(), driver.OfflineURLRequest{URLs: []string{url}})
	if len(results) != 1 || strings.ToLower(results[0].InfoHash) != hash {
		t.Fatalf("URL 全等这条路径坏了：%+v", results)
	}
}

// 对不上任何任务时不乱填 hash —— 填错会让去重与对账全乱。
func TestRecoverOfflineHashLeavesUnknownAlone(t *testing.T) {
	d := &Driver{}
	d.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "add_task_urls") {
			return jsonResponse(`{"state":true,"data":[{"state":false,"code":10008,` +
				`"url":"magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`), nil
		}
		return jsonResponse(`{"state":true,"data":{"page":1,"page_count":1,"tasks":[` +
			`{"info_hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",` +
			`"url":"magnet:?xt=urn:btih:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}}`), nil
	})}

	results, _ := d.AddOfflineURLs(context.Background(), driver.OfflineURLRequest{
		URLs: []string{"magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	})
	if len(results) != 1 {
		t.Fatalf("结果数 = %d", len(results))
	}
	if results[0].InfoHash != "" {
		t.Fatalf("对不上时不该填 hash，got %q", results[0].InfoHash)
	}
}

func TestBtihOfMagnet(t *testing.T) {
	const hash = "1767ed7a1e99b9d5a70c03d03ad7ad0995ba3b48"
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"裸磁力", "magnet:?xt=urn:btih:" + hash, hash},
		// ⚠️ 百分号编码形态 —— 库里的磁力链就是这个样子（抽取阶段规范化过）。
		// 不还原编码的话这里会返回空串，兜底在最需要它的场景静默失效。
		{"百分号编码的冒号", "magnet:?xt=urn%3Abtih%3A" + hash, hash},
		{"大写编码", "magnet:?xt=urn%3Abtih%3A" + strings.ToUpper(hash), hash},
		{"大写", "magnet:?xt=urn:btih:" + strings.ToUpper(hash), hash},
		{"带参数", "magnet:?xt=urn:btih:" + hash + "&dn=x&xl=1", hash},
		{"参数在前", "magnet:?dn=x&xt=urn:btih:" + hash, hash},
		{"不是磁力", "https://115.com/s/abc", ""},
		{"空", "", ""},
		{"hash 太短", "magnet:?xt=urn:btih:abc", ""},
		{"hash 里有非 hex", "magnet:?xt=urn:btih:zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", ""},
	}
	for _, tc := range cases {
		if got := btihOfMagnet(tc.raw); got != tc.want {
			t.Errorf("%s: btihOfMagnet(%q) = %q, want %q", tc.name, tc.raw, got, tc.want)
		}
	}
}

// 顺带钉住响应解析：offlineAddURLItem 的字段名是 115 的真实契约。
func TestOfflineAddURLItemShape(t *testing.T) {
	raw := `{"state":false,"code":10008,"message":"任务已存在","info_hash":"","url":"magnet:?xt=urn:btih:x"}`
	var item offlineAddURLItem
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if item.Code != 10008 || item.State || item.URL == "" {
		t.Fatalf("解析结果不对：%+v", item)
	}
}
