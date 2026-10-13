package searchtransfer

import (
	"testing"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/offlinedownload"
)

// classifyLink 决定一条链接走哪条通道。判错的后果很具体：115 分享链被判成
// 「普通直链」会被丢给离线下载接口，拿回一句看不懂的驱动错误。
func TestClassifyLink(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		kind   string
		scheme string
	}{
		{"磁力", "magnet:?xt=urn:btih:abc", kindMagnet, "magnet"},
		{"ed2k", "ed2k://|file|a.mkv|1|0123456789abcdef0123456789abcdef|/", kindED2K, "ed2k"},
		{"迅雷", "thunder://QUFodHRwOi8v", kindThunder, "thunder"},
		{"ftp", "ftp://example.com/a.mkv", "ftp", "ftp"},
		{"普通直链", "https://example.com/a.mkv", "http", "http"},
		{"115 主域分享", "https://115.com/s/abcd1234?password=x", kindShare115, "share_115"},
		{"115 cdn 分享", "https://115cdn.com/s/abcd1234", kindShare115, "share_115"},
		{"115 子域分享", "https://sub.115.com/s/abcd1234", kindShare115, "share_115"},
		{"115 非分享路径仍是直链", "https://115.com/download/a.mkv", "http", "http"},
		{"123 分享", "https://www.123pan.com/s/abcd1234", kindShare123, "share_123"},
		{"123 换过的域名", "https://123865.com/s/abcd1234", kindShare123, "share_123"},
		{"123 非分享路径仍是直链", "https://123pan.com/xxx/a.mkv", "http", "http"},
		{"夸克分享", "https://pan.quark.cn/s/abcd1234", kindShareQuark, "share_quark"},
		{"夸克带提取码", "https://pan.quark.cn/s/abcd1234?pwd=xy9z", kindShareQuark, "share_quark"},
		{"夸克主域", "https://www.quark.cn/s/abcd1234", kindShareQuark, "share_quark"},
		{"夸克非分享路径仍是直链", "https://pan.quark.cn/list/abc", "http", "http"},
		{"光鸭分享", "https://www.guangyapan.com/s/1950788535291969602_aeWYeSwV3ZOZlzEy", kindShareGuangya, "share_guangya"},
		{"光鸭主域", "https://guangyapan.com/s/123_abcdef", kindShareGuangya, "share_guangya"},
		{"光鸭非分享路径仍是直链", "https://www.guangyapan.com/list/abc", "http", "http"},
		{"百度分享", "https://pan.baidu.com/s/1abcd1234", kindShareBaidu, "share_baidu"},
		{"百度带提取码", "https://pan.baidu.com/s/1abcd1234?pwd=xy9z", kindShareBaidu, "share_baidu"},
		{"百度 eyun 子域", "https://eyun.baidu.com/s/1abcd1234", kindShareBaidu, "share_baidu"},
		{"百度非分享路径仍是直链", "https://pan.baidu.com/disk/home", "http", "http"},
		{"天翼分享 /t/", "https://cloud.189.cn/t/AbCdEf123", kindShare189, "share_189"},
		{"天翼分享 /s/", "https://cloud.189.cn/s/AbCdEf123", kindShare189, "share_189"},
		{"天翼非分享路径仍是直链", "https://cloud.189.cn/web/main", "http", "http"},
		{"移动分享 /w/i/", "https://yun.139.com/w/i/abc12345", kindShare139, "share_139"},
		{"移动分享 caiyun", "https://caiyun.139.com/w/i/abc12345", kindShare139, "share_139"},
		{"移动分享 fragment 形态", "https://yun.139.com/shareweb/#/w/i/abc12345", kindShare139, "share_139"},
		{"伪造域不算 115", "https://115.com.evil.com/s/abcd1234", "http", "http"},
		{"认不出", "javascript:alert(1)", "", ""},
		{"裸字符串", "不是链接", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, scheme := classifyLink(tc.url)
			if kind != tc.kind || scheme != tc.scheme {
				t.Errorf("classifyLink(%q) = (%q, %q), want (%q, %q)", tc.url, kind, scheme, tc.kind, tc.scheme)
			}
		})
	}
}

// mapTaskStatus 是两个值域之间唯一的映射点。
//
// 「网盘说这颗种子已经在任务列表里了」（AlreadyExists）在 offlinedownload 里被
// 记成 pending 而不是 failed —— 这里必须跟着它走，否则一条其实成功的转存
// 会在记录里一直显示「保存失败」。
func TestMapTaskStatus(t *testing.T) {
	cases := []struct {
		name       string
		task       offlinedownload.Task
		wantStatus string
	}{
		{"成功", offlinedownload.Task{Status: driver.OfflineStatusSuccess}, domain.TransferStatusSuccess},
		{"失败", offlinedownload.Task{Status: driver.OfflineStatusFailed}, domain.TransferStatusFailed},
		{"等待中", offlinedownload.Task{Status: driver.OfflineStatusPending}, domain.TransferStatusPending},
		{"下载中", offlinedownload.Task{Status: driver.OfflineStatusRunning}, domain.TransferStatusPending},
		{"重试中", offlinedownload.Task{Status: driver.OfflineStatusRetrying}, domain.TransferStatusPending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := mapTaskStatus(tc.task)
			if got != tc.wantStatus {
				t.Errorf("mapTaskStatus = %q, want %q", got, tc.wantStatus)
			}
		})
	}
}

// 失败时备注必须带上原因 —— 只写「保存失败」用户不知道该做什么。
func TestMapTaskStatusKeepsFailureReason(t *testing.T) {
	_, note := mapTaskStatus(offlinedownload.Task{
		Status:  driver.OfflineStatusFailed,
		Message: "任务创建失败",
		Error:   "115 API 错误：链接格式不正确",
	})
	if note != "115 API 错误：链接格式不正确" {
		t.Errorf("失败备注应当优先取 Error，实际 %q", note)
	}

	// Error 为空时退到 Message，两者都空才用兜底文案。
	_, note = mapTaskStatus(offlinedownload.Task{Status: driver.OfflineStatusFailed, Message: "网盘拒绝了"})
	if note != "网盘拒绝了" {
		t.Errorf("Error 为空时应当退到 Message，实际 %q", note)
	}
	_, note = mapTaskStatus(offlinedownload.Task{Status: driver.OfflineStatusFailed})
	if note == "" {
		t.Error("两者都空时应当有兜底文案")
	}
}

// 分享链**一条都不能漏**地走分享通道。
//
// 钉的是 2026-10-11 真机踩到的那个 bug：SaveLinks 的分流只判了 kindShare115，
// 于是 123 分享链掉进离线判定，拿着 scheme="share_123" 去问能力表 ——
// 网盘与内置下载器当然都不认，用户看到的是「网盘和内置下载器都不支持下载」，
// 而真实原因是它压根不该走离线通道。
//
// 这条测试用**性质**而不是用例来钉：凡是 classifyLink 判成分享链的，
// isShareKind 必须认，且它的 scheme 绝不能出现在离线通道认得的 scheme 里。
func TestShareKindsNeverFallIntoOfflineChannel(t *testing.T) {
	shareLinks := []string{
		"https://115.com/s/abcd1234?password=x",
		"https://115cdn.com/s/abcd1234",
		"https://www.123pan.com/s/abcd1234",
		// 用户 2026-10-11 实际贴的那条（pwd 形态、123865 域名）。
		"https://123865.com/s/Oqtgvd-pYn3h?pwd=YSRG",
		// 夸克（2026-10-12 接上转存）。
		"https://pan.quark.cn/s/abcdef123456",
		"https://www.quark.cn/s/abcdef123456?pwd=xy9z",
		// 光鸭（2026-10-12 接上转存）—— 分享码是 `<id>_<token>` 形态。
		"https://www.guangyapan.com/s/1950788535291969602_aeWYeSwV3ZOZlzEy",
		// 百度 / 天翼（转存走各自网页接口，要网页 Cookie）。
		"https://pan.baidu.com/s/1abcd1234?pwd=xy9z",
		"https://cloud.189.cn/t/AbCdEf123",
		// 移动（只识别、不转存）—— 但仍然不能掉进离线通道。
		"https://yun.139.com/w/i/abc12345",
	}
	for _, raw := range shareLinks {
		kind, scheme := classifyLink(raw)
		if !isShareKind(kind) {
			t.Errorf("%s 被判成 %q，不是分享链 —— 会被丢进离线通道", raw, kind)
			continue
		}
		for _, offline := range offlineSchemes {
			if scheme == offline {
				t.Errorf("%s 的 scheme %q 与离线通道的 %q 撞了，分流会分错", raw, scheme, offline)
			}
		}
	}
}

// kindLabelOfShare 是「搜索转存」的 kind 与驱动侧 ShareKind* 两套词表之间的
// 唯一转换点。判错的后果很具体：夸克分享链会被拿去问 115 的驱动能力，
// 于是「界面说能转、提交时说这个驱动不支持」。
func TestKindLabelOfShareRoundTrips(t *testing.T) {
	cases := []struct {
		kind string
		want string
	}{
		{kindShare115, driver.ShareKind115},
		{kindShare123, driver.ShareKind123},
		{kindShareQuark, driver.ShareKindQuark},
		{kindShareGuangya, driver.ShareKindGuangya},
		{kindShareBaidu, driver.ShareKindBaidu},
		{kindShare189, driver.ShareKind189},
		{kindShare139, driver.ShareKind139},
	}
	for _, tc := range cases {
		if got := kindLabelOfShare(tc.kind); got != tc.want {
			t.Errorf("kindLabelOfShare(%q) = %q，期望 %q", tc.kind, got, tc.want)
		}
	}

	// 错误文案要走**中文名**那一套，不能把驱动短名大写拼进中文句子 ——
	// 真机上曾出现「当前网盘不支持 QUARK 分享转存」。
	zh := map[string]string{
		kindShare115: "115", kindShare123: "123",
		kindShareQuark: "夸克", kindShareGuangya: "光鸭",
		kindShareBaidu: "百度", kindShare189: "天翼", kindShare139: "移动",
	}
	for kind, want := range zh {
		if got := kindLabelZh(kind); got != want {
			t.Errorf("kindLabelZh(%q) = %q，期望 %q", kind, got, want)
		}
	}
}

// 没配仓储时记录功能整体关闭，但**转存本身照常工作** ——
// 留档不该是转存的前置条件。
func TestRecordsWithoutRepository(t *testing.T) {
	svc := New(Options{})
	if _, err := svc.Records(nil, RecordFilter{}, 1, 20); err == nil {
		t.Error("没有仓储时应当报错而不是 panic")
	}
	// recordCreate / recordUpdate 在没仓储时必须是静默 no-op。
	svc.recordCreate(nil, newRecord("magnet", "magnet:?xt=urn:btih:a", "", "", "manual", "", "", "", 1, domain.TransferStatusPending, ""))
	svc.recordUpdate(nil, &domain.TransferRecord{ID: 1})
}
