package cloud139

import (
	"context"
	"os"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

// 真机联调：拿真账号跑一遍移动云盘的分享转存链路。
//
// 默认跳过。跑它要显式给环境变量：
//
//	LITEPAN_139_AUTH='<网页请求头里的 Authorization 值>' \
//	LITEPAN_139_SHARE_URL='https://yun.139.com/w/i/xxxx' \
//	go test ./drivers/139Cloud/ -run TestLive139Share -v
//
// 留着它是因为这条路上有几处**只能真机确认**的东西：分享侧接口不带签名、
// 个人云侧要 mcloud-sign、转存交的是**分享内路径**而不是 id、
// `caLst/coLst` 的字段名在参考实现里还出现过两套。
func TestLive139Share(t *testing.T) {
	auth := strings.TrimSpace(os.Getenv("LITEPAN_139_AUTH"))
	shareURL := strings.TrimSpace(os.Getenv("LITEPAN_139_SHARE_URL"))
	if auth == "" || shareURL == "" {
		t.Skip("未设置 LITEPAN_139_AUTH / LITEPAN_139_SHARE_URL，跳过真机测试")
	}

	code := extract139LinkIDForTest(shareURL)
	if code == "" {
		t.Fatalf("从 %s 里取不出分享码", shareURL)
	}

	d := &Driver{client: httpx.NewClient(httpx.ClientOptions{})}
	d.SetAuthCredentials(domain.AuthCredentials{AccessToken: auth})
	ctx := context.Background()
	if err := d.Init(ctx); err != nil {
		t.Fatalf("初始化失败（Authorization 可能不对）：%v", err)
	}
	if caps := d.ShareReceiveCapabilities(); !caps.Ready {
		t.Fatalf("能力探测说不可用：%s", caps.Reason)
	}
	t.Logf("账号 = %s", d.currentAccount())

	entries, err := d.shareListRoot(ctx, code, "")
	if err != nil {
		t.Fatalf("列分享目录失败：%v", err)
	}
	t.Logf("分享根目录下有 %d 个条目", len(entries))
	for i, e := range entries {
		if i >= 5 {
			break
		}
		t.Logf("  [%d] 名称=%q 路径=%q 目录=%v 大小=%d", i, e.Name, e.Path, e.IsDir, e.Size)
	}
	if len(entries) == 0 {
		t.Fatal("分享里一个条目都没有 —— 不是空分享就是字段名对不上（参考实现里有过两套）")
	}

	// 只读部分到此为止。要真的转存，把下面这段注释去掉（会往网盘根目录写东西）：
	//
	// res, err := d.ReceiveShare(ctx, driver.ShareReceiveRequest{ShareCode: code})
	// if err != nil {
	// 	t.Fatalf("转存失败：%v", err)
	// }
	// t.Logf("转存成功：%d 个条目（记得去网盘里删掉刚写进去的那一份）", res.Count)
	_ = driver.ShareKind139
}

// extract139LinkIDForTest 从一条移动云盘分享链里取 linkID（路径末段，或 fragment 里那段）。
func extract139LinkIDForTest(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// 去掉 fragment 前缀与查询串，取最后一段 [A-Za-z0-9_-]{4,64}。
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		tail := raw[i+1:]
		tail = strings.TrimPrefix(tail, "/")
		for _, seg := range strings.Split(tail, "/") {
			seg = strings.TrimSpace(strings.SplitN(seg, "?", 2)[0])
			if is139LinkIDSegment(seg) {
				return seg
			}
		}
		raw = raw[:i]
	}
	segments := strings.Split(strings.TrimRight(raw, "/"), "/")
	for i := len(segments) - 1; i >= 0; i-- {
		if is139LinkIDSegment(strings.TrimSpace(segments[i])) {
			return strings.TrimSpace(segments[i])
		}
	}
	return ""
}

func is139LinkIDSegment(s string) bool {
	if len(s) < 4 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}
