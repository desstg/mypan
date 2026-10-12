package quark

import (
	"encoding/json"
	"testing"

	"litepan/internal/domain"
)

// 目录路径比对的归一化：转存前拿它挡「ID 与路径对不上」。
//
// 判错的两个方向都危险 —— 太松会把文件存进错目录（真实写入，不可撤销），
// 太紧会把本来正确的目标拒掉。与 123 驱动里的同名测试是同一份契约。
func TestSameDirPath(t *testing.T) {
	same := [][2]string{
		{"/电影资源", "电影资源"},
		{"/电影资源/", "/电影资源"},
		{"//a//b//", "/a/b"},
		{"/", ""},
		{"/", "/"},
	}
	for _, c := range same {
		if !sameDirPath(c[0], c[1]) {
			t.Errorf("sameDirPath(%q, %q) = false，应当视为同一目录", c[0], c[1])
		}
	}
	diff := [][2]string{
		{"/电影资源", "/电影"},
		{"/a/b", "/a/c"},
		{"/电影资源", "/电影资源/子目录"},
		// 空格不参与归一化 —— 带空格与不带是**两个**目录，
		// 把它们当成一个会让路径核对形同虚设。
		{"/电影 资源", "/电影资源"},
	}
	for _, c := range diff {
		if sameDirPath(c[0], c[1]) {
			t.Errorf("sameDirPath(%q, %q) = true，这是两个不同目录", c[0], c[1])
		}
	}
}

// 分享条目的 token 字段名各家版本不一，四种都要认。
//
// 少了任何一个的后果很具体：那一版接口返回的分享会「一个条目都取不到 token」，
// 整份转存被拒，而报错指向的是「没给令牌」这种看起来像接口坏了的话。
func TestShareEntryTokenPrefersShareFidToken(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"share_fid_token", `{"fid":"1","share_fid_token":"tok-a"}`, "tok-a"},
		{"fid_token", `{"fid":"1","fid_token":"tok-b"}`, "tok-b"},
		{"share_token", `{"fid":"1","share_token":"tok-c"}`, "tok-c"},
		{"file_token", `{"fid":"1","file_token":"tok-d"}`, "tok-d"},
		{"第一个优先", `{"fid":"1","share_fid_token":"first","fid_token":"second"}`, "first"},
		{"都没有", `{"fid":"1"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var e shareEntry
			if err := json.Unmarshal([]byte(tc.raw), &e); err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if got := e.token(); got != tc.want {
				t.Errorf("token() = %q，期望 %q", got, tc.want)
			}
		})
	}
}

func TestShareEntryIsDir(t *testing.T) {
	var dir shareEntry
	if err := json.Unmarshal([]byte(`{"fid":"1","dir":true}`), &dir); err != nil {
		t.Fatal(err)
	}
	if !dir.isDir() {
		t.Error("dir=true 应当算目录")
	}
	var folder shareEntry
	// file_type=0 是文件夹（夸克两套标记都要认，实测两者都会出现）。
	if err := json.Unmarshal([]byte(`{"fid":"1","file_type":0}`), &folder); err != nil {
		t.Fatal(err)
	}
	if !folder.isDir() {
		t.Error("file_type=0 应当算目录")
	}
	var file shareEntry
	if err := json.Unmarshal([]byte(`{"fid":"1","file_type":1,"dir":false}`), &file); err != nil {
		t.Fatal(err)
	}
	if file.isDir() {
		t.Error("file_type=1 应当算文件")
	}
}

// 分享详情缺字段不该让整包解析失败。
//
// 与 123 那条「任何一处形状变化都不该让整次刷新失败」同一条教训：
// 一个字段没了，不该把一次本来能成的转存判死。
func TestShareDetailAcceptsMissingOptionalFields(t *testing.T) {
	for _, raw := range []string{`{}`, `{"list":null}`, `{"list":[]}`, `{"share":null}`} {
		var data shareDetailData
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			t.Errorf("解析 %s 不该报错: %v", raw, err)
		}
		if len(data.List) != 0 {
			t.Errorf("解析 %s 应当得到空列表，实际 %+v", raw, data.List)
		}
	}
}

// task_id 有时是数字、有时是字符串 —— 两种形态都要收下。
//
// 收窄成 string 会让「数字形态」的那一次转存整包解析失败，
// 而 save 其实已经成功了（123 驱动被同一类形状变化坑过，见那边的 webNumber 说明）。
func TestShareSaveDataAcceptsNumericTaskID(t *testing.T) {
	for _, raw := range []string{`{"task_id":123456}`, `{"task_id":"123456"}`, `{"task_id":null}`, `{}`} {
		var data shareSaveData
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			t.Errorf("解析 %s 不该报错: %v", raw, err)
		}
	}
	var numeric shareSaveData
	if err := json.Unmarshal([]byte(`{"task_id":123456}`), &numeric); err != nil {
		t.Fatal(err)
	}
	if got := numeric.TaskID.String(); got != "123456" {
		t.Errorf("数字形态的 task_id 应当是 123456，实际 %q", got)
	}
	var text shareSaveData
	if err := json.Unmarshal([]byte(`{"task_id":"abc-def"}`), &text); err != nil {
		t.Fatal(err)
	}
	if got := text.TaskID.String(); got != "abc-def" {
		t.Errorf("字符串形态的 task_id 应当原样保留，实际 %q", got)
	}
}

// shareTokenData 兼容两个字段名。
func TestShareTokenDataReadsBothKeys(t *testing.T) {
	var a shareTokenData
	if err := json.Unmarshal([]byte(`{"stoken":"s1"}`), &a); err != nil {
		t.Fatal(err)
	}
	if a.value() != "s1" {
		t.Errorf("stoken 应当读出来，实际 %q", a.value())
	}
	var b shareTokenData
	if err := json.Unmarshal([]byte(`{"token":"s2"}`), &b); err != nil {
		t.Fatal(err)
	}
	if b.value() != "s2" {
		t.Errorf("token 别名应当读出来，实际 %q", b.value())
	}
}

// 分享的业务错误码要翻成**确定性失败**码。
//
// 这不只是文案问题：isPermanentDeliveryError 认的就是 CodeValidation / CodeNotFound，
// 没翻过来的话「提取码填错」会被当成可重试的失败退避重试 5 次 ——
// 对上游是白打的请求，也正是不想制造的那种无效重试。
func TestShareErrorCodesArePermanentFailures(t *testing.T) {
	cases := []struct {
		code int
		want domain.ErrorCode
	}{
		{41008, domain.CodeValidation}, // 需要提取码
		{41004, domain.CodeNotFound},   // 分享失效
		{41010, domain.CodeNotFound},
		{41011, domain.CodeNotFound},
	}
	for _, tc := range cases {
		err := mapBusinessError(quarkEnvelope{Code: tc.code, Message: "x"})
		ae, ok := domain.AsAppError(err)
		if !ok {
			t.Errorf("code=%d 应当翻成 AppError，实际 %v", tc.code, err)
			continue
		}
		if ae.Code != tc.want {
			t.Errorf("code=%d 应当翻成 %s，实际 %s（%s）", tc.code, tc.want, ae.Code, ae.Message)
		}
	}
	// 其它码保持通用错误，不误判成永久失败。
	other := mapBusinessError(quarkEnvelope{Code: 99999, Message: "未知"})
	if ae, ok := domain.AsAppError(other); !ok || ae.Code == domain.CodeValidation || ae.Code == domain.CodeNotFound {
		t.Errorf("未知码不该被当成确定性失败，实际 %v", other)
	}
}

// 换域名重试的判据：确定性错误不该换域名（换了还是同一个结果），
// 网络/宿主类问题才换。判错会让「分享已失效」这种确定的事白打两遍。
func TestIsHostRetryable(t *testing.T) {
	notRetryable := []error{
		domain.Errorf(domain.CodeNotFound, "分享已失效"),
		domain.Errorf(domain.CodeValidation, "需要提取码"),
		domain.Errorf(domain.CodeAuthExpired, "Cookie 失效"),
		domain.Errorf(domain.CodePermissionDenied, "权限不足"),
	}
	for _, err := range notRetryable {
		if isHostRetryable(err) {
			t.Errorf("%v 是确定性错误，不该换域名重试", err)
		}
	}
	if !isHostRetryable(domain.Errorf(domain.CodeDriverError, "夸克 HTTP 502")) {
		t.Error("HTTP 502 属于宿主/网络问题，应当换域名重试")
	}
	if !isHostRetryable(nil) {
		t.Error("非 AppError 一律按可重试处理")
	}
}
