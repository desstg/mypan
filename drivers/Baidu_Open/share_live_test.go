package baiduopen

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"

	_ "modernc.org/sqlite"
)

// 真机联调：拿真账号跑一遍百度的**分享转存**与**离线下载**全过程。
//
// 默认跳过。跑它要显式给环境变量：
//
//	LITEPAN_PAN_COOKIE='<浏览器 Cookie（BDUSS/STOKEN 那一串）>' \
//	LITEPAN_PAN_SHARE_URL='https://pan.baidu.com/s/1xxxx?pwd=abcd' \
//	go test ./drivers/Baidu_Open/ -run TestLivePan -v
//
// ⚠️ **它会真的往网盘里写东西** —— 转存到 `LITEPAN_PAN_TARGET`（默认 `/LitePan-测试转存`，
// 没有就新建）。跑完自己去网盘里把那目录删掉。
//
// 留着它是因为这条链路上**最脆的一环是分享页 HTML**（`shareid`/`share_uk` 只出现在
// `yunData.setData({...})` 那段 JS 字面量里，不是 JSON），桩数据挡不住「百度又改页面了」。
func TestLivePan(t *testing.T) {
	cookie := livePanCookie()
	shareURL := strings.TrimSpace(os.Getenv("LITEPAN_PAN_SHARE_URL"))
	if cookie == "" || shareURL == "" {
		t.Skip("未设置 LITEPAN_PAN_COOKIE（或 LITEPAN_PAN_COOKIE_FILE）/ LITEPAN_PAN_SHARE_URL，跳过真机测试")
	}
	target := strings.TrimSpace(os.Getenv("LITEPAN_PAN_TARGET"))
	if target == "" {
		target = "/LitePan-测试转存"
	}

	share, err := driver.ParseShareLink(shareURL, "")
	if err != nil {
		t.Fatalf("从 %s 里取不出分享码：%v", shareURL, err)
	}

	d := &Driver{client: httpx.NewClient(httpx.ClientOptions{Timeout: 30 * time.Second})}
	// 目标目录核对走的是**开放平台**那条通道（路径本来就来自它），所以真机测试
	// 也要把账号的 OAuth 令牌带上 —— 只给 Cookie 的话那一步会 AUTH_EXPIRED。
	// 令牌与 Cookie 都可以直接从一份**库副本**里读（见 livePanCreds），
	// 免得把凭据在 shell 里传来传去。
	creds := livePanCreds(cookie)
	d.SetAuthCredentials(creds)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if !d.WebLoginReady() {
		t.Fatal("WebLoginReady=false —— 离线下载那条路会被降级到内置下载器")
	}
	token, err := d.panBdstoken(ctx)
	if err != nil {
		t.Fatalf("取 bdstoken 失败（Cookie 可能不完整/已失效）：%v", err)
	}
	t.Logf("① bdstoken 取得（%d 字符）", len(token))

	tokens, err := d.panSharePage(ctx, panBaseURL+"/s/"+share.Code, share.Code)
	if err != nil {
		t.Fatalf("解析分享页失败：%v", err)
	}
	t.Logf("② 分享页解析成功：shareid=%s share_uk=%s", tokens.ShareID, tokens.ShareUK)

	if share.Password != "" {
		if err := d.shareVerify(ctx, share.Code, share.Password); err != nil {
			t.Fatalf("提取码校验失败：%v", err)
		}
		t.Logf("③ 提取码 %s 校验通过", share.Password)
	}

	ids, names, err := d.shareList(ctx, tokens, "/")
	if err != nil {
		t.Fatalf("列分享目录失败：%v", err)
	}
	if len(ids) == 0 {
		t.Fatal("分享里一个条目都没有 —— 不是空分享就是接口形状变了")
	}
	t.Logf("④ 分享根目录下有 %d 个条目，头几个：", len(ids))
	for i := range ids {
		if i >= 5 {
			break
		}
		t.Logf("     fs_id=%s 名称=%q", ids[i], names[i])
	}

	// ⑤ 真的转存（新建一个专用目录，方便事后清理）。
	if _, err := d.CreateFolder(ctx, "/", strings.TrimPrefix(target, "/")); err != nil {
		t.Logf("（建目录失败，可能已存在，忽略）%v", err)
	}
	res, err := d.ReceiveShare(ctx, driver.ShareReceiveRequest{
		ShareCode:   share.Code,
		ReceiveCode: share.Password,
		TargetCID:   target,
		TargetPath:  target,
	})
	if err != nil {
		t.Fatalf("转存失败：%v", err)
	}
	t.Logf("⑤ 转存成功：%d 个条目 → %s（记得去网盘里删掉这个目录）", res.Count, res.TargetPath)
}

// TestLivePanCleanup 删掉真机测试留下的目录。
//
//	LITEPAN_PAN_COOKIE_FILE=... LITEPAN_PAN_ACCESS_TOKEN=... \
//	LITEPAN_PAN_CLEANUP='/LitePan-测试转存' go test ./drivers/Baidu_Open/ -run TestLivePanCleanup -v
func TestLivePanCleanup(t *testing.T) {
	target := strings.TrimSpace(os.Getenv("LITEPAN_PAN_CLEANUP"))
	if target == "" {
		t.Skip("未设置 LITEPAN_PAN_CLEANUP，跳过")
	}
	d := &Driver{client: httpx.NewClient(httpx.ClientOptions{Timeout: 30 * time.Second})}
	d.SetAuthCredentials(livePanCreds(livePanCookie()))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := d.DeleteFiles(ctx, []string{target}); err != nil {
		t.Fatalf("删除 %s 失败：%v", target, err)
	}
	t.Logf("已删除 %s", target)
}

// livePanCookie 允许把 Cookie 写在文件里再指过来（`LITEPAN_PAN_COOKIE_FILE`）。
//
// 加它是因为 Cookie 有 1.8 KB 且带分号与百分号 —— 直接塞进环境变量在 shell 里
// 很容易被截断或转义搞坏，症状是「Cookie 看着填了但服务端不认」。
func livePanCookie() string {
	if v := strings.TrimSpace(os.Getenv("LITEPAN_PAN_COOKIE")); v != "" {
		return v
	}
	path := strings.TrimSpace(os.Getenv("LITEPAN_PAN_COOKIE_FILE"))
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// livePanCreds 把 Cookie 与 OAuth 令牌凑成一份凭据。
//
// 令牌优先取环境变量；没有的话从 `LITEPAN_PAN_DB`（一份**库副本**）里读
// `account_auth_states` —— 真机测试要跑通「目标目录核对」就必须有开放平台令牌，
// 而从副本里读比在 shell 里传一长串 token 稳得多（也免得凭据进 shell 历史）。
func livePanCreds(cookie string) domain.AuthCredentials {
	creds := domain.AuthCredentials{
		Cookie:       cookie,
		AccessToken:  strings.TrimSpace(os.Getenv("LITEPAN_PAN_ACCESS_TOKEN")),
		RefreshToken: strings.TrimSpace(os.Getenv("LITEPAN_PAN_REFRESH_TOKEN")),
	}
	dbPath := strings.TrimSpace(os.Getenv("LITEPAN_PAN_DB"))
	if creds.AccessToken != "" || dbPath == "" {
		return creds
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return creds
	}
	defer db.Close()
	var access, refresh string
	if err := db.QueryRow(
		`select access_token, refresh_token from account_auth_states
		  where account_id = (select id from cloud_accounts where driver_type='baidu_open' limit 1)`,
	).Scan(&access, &refresh); err == nil {
		creds.AccessToken = strings.TrimSpace(access)
		creds.RefreshToken = strings.TrimSpace(refresh)
	}
	return creds
}

// TestLivePanOfflineDownload 真机验证离线下载（磁力）那条路。
//
//	LITEPAN_PAN_COOKIE='...' LITEPAN_PAN_MAGNET='magnet:?xt=...' \
//	go test ./drivers/Baidu_Open/ -run TestLivePanOffline -v
func TestLivePanOfflineDownload(t *testing.T) {
	cookie := strings.TrimSpace(os.Getenv("LITEPAN_PAN_COOKIE"))
	magnet := strings.TrimSpace(os.Getenv("LITEPAN_PAN_MAGNET"))
	if cookie == "" || magnet == "" {
		t.Skip("未设置 LITEPAN_PAN_COOKIE / LITEPAN_PAN_MAGNET，跳过真机测试")
	}
	d := &Driver{client: httpx.NewClient(httpx.ClientOptions{Timeout: 30 * time.Second})}
	d.SetAuthCredentials(domain.AuthCredentials{Cookie: cookie})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	results, err := d.AddOfflineURLs(ctx, driver.OfflineURLRequest{URLs: []string{magnet}, ParentID: "/"})
	if err != nil {
		t.Fatalf("提交离线任务失败：%v", err)
	}
	for _, r := range results {
		t.Logf("提交结果：success=%v taskID=%s name=%q message=%q", r.Success, r.ProviderTaskID, r.Name, r.Message)
	}
	if len(results) == 0 || !results[0].Success {
		t.Fatalf("离线任务没提交成功：%+v", results)
	}

	updates, err := d.RefreshOfflineTasks(ctx, []driver.OfflineTaskRef{{ProviderTaskID: results[0].ProviderTaskID}})
	if err != nil {
		t.Fatalf("查任务状态失败：%v", err)
	}
	for _, u := range updates {
		t.Logf("任务状态：status=%s progress=%d size=%d name=%q", u.Status, u.Progress, u.Size, u.Name)
	}

	if err := d.DeleteOfflineTask(ctx, driver.OfflineTaskRef{ProviderTaskID: results[0].ProviderTaskID}, false); err != nil {
		t.Logf("（清理失败，去网盘里手动删一下）删任务失败：%v", err)
	} else {
		t.Log("已清理该离线任务")
	}
}
