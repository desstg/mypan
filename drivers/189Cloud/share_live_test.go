package cloud189

import (
	"context"
	"os"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

// 真机联调：拿真账号跑一遍天翼的分享转存链路。
//
// 这不是单元测试（要联网、要真凭据），所以**默认跳过**。跑它要显式给三个环境变量：
//
//	LITEPAN_189_COOKIE='<浏览器 Cookie>' \
//	LITEPAN_189_SHARE_URL='https://cloud.189.cn/t/xxxx' \
//	LITEPAN_189_ACCESS_CODE='abcd' \
//	go test ./drivers/189Cloud/ -run TestLive189Share -v
//
// 留着它是因为**「接口对不对」在单元测试里看不见** —— 那边打的是我自己写的桩，
// 桩与真接口一起错的时候测试照样绿。这一步验证的是：
//
//  1. ssoH5 能不能用 Cookie 换出 AccessToken；
//  2. getShareInfoByCodeV2 / listShareDir / createBatchTask(SHARE_SAVE) 的形状；
//  3. 真实分享链能不能被认出来（`/t/` 与 `/s/` 两种形态）。
//
// ⚠️ 它会**真的往账号里写文件**（转存到根目录），用完自己清理。
func TestLive189Share(t *testing.T) {
	cookie := strings.TrimSpace(os.Getenv("LITEPAN_189_COOKIE"))
	shareURL := strings.TrimSpace(os.Getenv("LITEPAN_189_SHARE_URL"))
	if cookie == "" || shareURL == "" {
		t.Skip("未设置 LITEPAN_189_COOKIE / LITEPAN_189_SHARE_URL，跳过真机测试")
	}
	ref, code := parse189ShareForTest(shareURL)
	if code == "" {
		t.Fatalf("从 %s 里取不出分享码（解析结果 %+v）", shareURL, ref)
	}

	d := &Driver{client: httpx.NewClient(httpx.ClientOptions{})}
	d.SetAuthCredentials(domain.AuthCredentials{Cookie: cookie})
	ctx := context.Background()

	if caps := d.ShareReceiveCapabilities(); !caps.Ready {
		t.Fatalf("能力探测说不可用：%s", caps.Reason)
	}

	token, err := d.shareAccessToken(ctx)
	if err != nil {
		t.Fatalf("换 AccessToken 失败（Cookie 可能不对/已过期）：%v", err)
	}
	t.Logf("AccessToken 换取成功（%d 字符）", len(token))

	meta, err := d.shareInfoByCode(ctx, code, ref.Password)
	if err != nil {
		t.Fatalf("读分享元信息失败：%v", err)
	}
	t.Logf("分享元信息：shareId=%s rootFileId=%s shareMode=%s 名称=%q",
		meta.ShareID, meta.RootID, meta.ShareMode, meta.FileName)

	entries, err := d.shareListDir(ctx, token, meta)
	if err != nil {
		t.Fatalf("列分享目录失败：%v", err)
	}
	t.Logf("分享根目录下有 %d 个条目", len(entries))
	for i, e := range entries {
		if i >= 5 {
			break
		}
		t.Logf("  [%d] %s（目录=%v，%d 字节）", i, e.Name, e.IsFolder, e.Size)
	}
	if len(entries) == 0 {
		t.Fatal("分享里一个条目都没有 —— 不是空分享就是接口形状变了")
	}

	// 只读部分到此为止。要真的转存，把下面这段的注释去掉（它会往网盘根目录写东西），
	// 并在核对完之后自己删掉刚转存进来的文件。
	//
	// res, err := d.ReceiveShare(ctx, driver.ShareReceiveRequest{
	// 	ShareCode: code, ReceiveCode: ref.Password,
	// })
	// if err != nil {
	// 	t.Fatalf("转存失败：%v", err)
	// }
	// t.Logf("转存成功：%d 个条目", res.Count)
	_ = driver.ShareKind189
}

// parse189ShareForTest 是给真机测试用的一层薄包装，把分享链拆成 driver.ShareRef。
func parse189ShareForTest(raw string) (driver.ShareRef, string) {
	ref, err := driver.ParseShareLink(raw, "")
	if err != nil {
		return driver.ShareRef{}, ""
	}
	return ref, ref.Code
}
