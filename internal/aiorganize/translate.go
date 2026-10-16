package aiorganize

import (
	"context"
	"strings"
)

// 本文件是给**别的模块**用的通用文本出口（第一个用户是番号刮削的「简介/标题翻译」）。
//
// # 为什么加在 aiorganize 里而不是新开一个 AI 客户端
//
// 这个包已经解决了「用户配的到底是 OpenAI 协议还是 Anthropic 协议」这件事
// （`chat` 里那套协议自动嗅探 + 记住上次成功的），还带着实例配置、超时、错误映射。
// 再写一份等于把「协议嗅探」这套又实现一遍，迟早两边行为不一致。
//
// 所以这里只做一件事：**把 `chat` 安全地暴露出去**，调用方给 system + user 两段文本。

// Translate 用用户配的 AI 把一段文本按 `systemPrompt` 处理，返回结果。
//
// 用法（番号刮削那边）：
//
//	system := "把下面的日文翻译成简体中文…"
//	out, err := ai.Translate(ctx, system, 待翻译的简介)
//
// `Available()` 为假（没开 / 没配）时返回 `ErrAIDisabled` ——
// 调用方据此**安静跳过**，不要把「没配 AI」当成错误弹给用户。
func (s *Service) Translate(ctx context.Context, systemPrompt, text string) (string, error) {
	if s == nil || !s.Available() {
		return "", ErrAIDisabled
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	system := strings.TrimSpace(systemPrompt)
	if system == "" {
		system = defaultTranslatePrompt
	}
	// ⚠️ 用户可能在提示词里写了**内容占位符** `{content}`（设置页那个框的自由度很高，
	// 用户就是这么写的：「在 {content} 处填入需要翻译的内容」）。
	// 那种写法必须把正文**替换进去**、整条当一条 user 消息发 ——
	// 拆成 system + user 的话，`{content}` 会作为**字面量**留在提示词里，
	// 模型看到的是一个没人替换的占位符。
	var messages []chatMessage
	if strings.Contains(system, translateContentPlaceholder) {
		messages = []chatMessage{{Role: "user",
			Content: strings.ReplaceAll(system, translateContentPlaceholder, text)}}
	} else {
		messages = []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: text},
		}
	}
	out, err := s.chat(ctx, s.runtimeConfig(), messages)
	if err != nil {
		return "", err
	}
	return cleanModelText(out), nil
}

// translateContentPlaceholder 是提示词里的**内容占位符**。
//
// 用户自定义提示词时可能写成「…在 {content} 处填入待翻译内容…」——
// 见到它就把正文替换进去（见 Translate）。
const translateContentPlaceholder = "{content}"

// defaultTranslatePrompt 是用户没自定义提示词时的兜底（与设置页里的默认值同一份，
// 见 settings.KeyJavTranslatePrompt —— 两处都留一份是因为设置层读不到这个常量，
// 而**只返回译文**这条要求必须写死在提示词里：模型多说一句「好的，以下是译文：」
// 就会把那句话写进用户的 nfo）。
const defaultTranslatePrompt = `你是翻译助手。把用户给你的文本翻译成简体中文。
要求：
1. 只输出译文本身，不要任何解释、前缀、引号、Markdown 代码块或「以下是翻译」之类的话。
2. 如果原文已经是中文，原样返回，不要改写、不要润色。
3. 保留原文里的换行与分段结构。
4. 人名、番号、专有名词照原样保留，不要音译或意译。`

// cleanModelText 去掉模型爱加的那层壳（代码块围栏、包裹引号、开头的客套话）。
//
// ⚠️ 这不是洁癖：**译文是要写进 nfo 的 `<plot>`** 的，模型多一句
// 「好的，以下是翻译：」就会永久留在用户的媒体库里。
func cleanModelText(s string) string {
	s = strings.TrimSpace(s)
	// ```…``` 围栏
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		// 去掉围栏后可选的语言标记（```json / ```text）
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			head := strings.TrimSpace(s[:nl])
			if head == "" || !strings.Contains(head, " ") {
				s = s[nl+1:]
			}
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	s = strings.TrimSpace(s)
	// 整段被引号包住（模型爱给「译文」加引号）。
	// ⚠️ 必须按 rune 比 —— 中文引号是多字节，拿 byte 比会 `overflows byte` 编译不过。
	if r := []rune(s); len(r) >= 2 {
		first, last := r[0], r[len(r)-1]
		if (first == '"' && last == '"') || (first == '「' && last == '」') || (first == '“' && last == '”') {
			s = strings.TrimSpace(string(r[1 : len(r)-1]))
		}
	}
	// 开头那类「以下是…译文：」的客套话（只在**很短**的一行时剥，避免误伤正文）
	if nl := strings.IndexByte(s, '\n'); nl > 0 && nl < 40 {
		head := s[:nl]
		for _, k := range []string{"以下是", "译文如下", "翻译如下", "好的", "Here is"} {
			if strings.Contains(head, k) {
				s = strings.TrimSpace(s[nl+1:])
				break
			}
		}
	}
	return strings.TrimSpace(s)
}
