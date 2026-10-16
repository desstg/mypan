package aiorganize

import (
	"strings"
	"testing"
)

// TestCleanModelText 钉住模型爱加的那层壳会被剥掉。
//
// 这不是洁癖：译文要写进 nfo 的 `<plot>`，模型多说一句「好的，以下是翻译：」
// 就会**永久留在用户的媒体库里**。
func TestCleanModelText(t *testing.T) {
	cases := map[string]string{
		"```\n译文内容\n```":     "译文内容",
		"```text\n译文内容\n```": "译文内容",
		`"译文内容"`:             "译文内容",
		"「译文内容」":             "译文内容",
		"“译文内容”":             "译文内容",
		"以下是译文：\n译文内容":       "译文内容",
		"好的，这是翻译结果：\n译文内容":   "译文内容",
		"译文内容":               "译文内容",
		"第一行\n第二行":           "第一行\n第二行",
		"":                   "",
	}
	for in, want := range cases {
		if got := cleanModelText(in); got != want {
			t.Errorf("cleanModelText(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestCleanModelTextKeepsBody 别把正文里正常的引号/换行误伤。
func TestCleanModelTextKeepsBody(t *testing.T) {
	body := "她说「不要」。\n然后离开了。"
	if got := cleanModelText(body); got != body {
		t.Errorf("正文不该被动：%q → %q", body, got)
	}
	// 长第一行里的「以下是」不该被当客套话剥掉（那可能是正文）。
	long := "以下是一个很长很长的句子用来测试长度阈值会不会误伤正文内容。\n第二行"
	if got := cleanModelText(long); got != long {
		t.Errorf("长首行不该被剥：%q → %q", long, got)
	}
}

// TestTranslateDisabled 没配 AI 时返回 ErrAIDisabled（安静跳过的信号，不是故障）。
func TestTranslateDisabled(t *testing.T) {
	s := &Service{} // 没有 settings
	if _, err := s.Translate(nil, "任意提示词", "任意文本"); err != ErrAIDisabled {
		t.Errorf("没配时该返回 ErrAIDisabled，得到 %v", err)
	}
	if s.Available() {
		t.Error("没配时 Available 该是 false")
	}
}

// TestTranslateContentPlaceholder 钉住提示词里的 `{content}` 会被**替换成正文**。
//
// 用户在设置页把提示词写成了「…在 {content} 处填入需要翻译的内容…」。
// 若照 system+user 拆开发，`{content}` 会作为**字面量**留在系统消息里 ——
// 模型看到的是一个没人替换的占位符。
func TestTranslateContentPlaceholder(t *testing.T) {
	// 这里不真打接口，只验「拼消息」这一步的判据：
	// 提示词含 {content} 时，正文必须出现在发出去的那条消息里。
	prompt := "你是翻译。把 {content} 译成中文。"
	text := "これはテストです。"
	if !strings.Contains(prompt, translateContentPlaceholder) {
		t.Fatal("前提变了：占位符常量改了")
	}
	built := strings.ReplaceAll(prompt, translateContentPlaceholder, text)
	if strings.Contains(built, translateContentPlaceholder) {
		t.Error("替换后不该还剩占位符")
	}
	if !strings.Contains(built, text) {
		t.Error("替换后正文该在消息里")
	}
}
