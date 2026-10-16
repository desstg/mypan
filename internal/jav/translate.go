package jav

import (
	"context"
	"strings"
	"unicode"

	"litepan/internal/domain"
	"litepan/internal/settings"
)

// 本文件是「**刮削时顺手把日文简介/标题翻成中文**」那一步。
//
// # 为什么需要它
//
// 补缺链抓回来的简介**绝大多数是日文**（DMM 的 `og:description`、avsox 的
// `description_ja`、missav 的原文…），而用户要的是能读的中文。`title` 那一栏同理
// （JAVDB 真库 8650 部里 6356 部的 title 含假名，也就是大面积日文）。
//
// # 用户定的两条规矩（2026-10-15）
//
//  1. **已经是中文就不翻译**（见 isChinese）—— 别拿 AI 去「润色」中文，
//     那只会把原文改坏，还白花一次请求。
//  2. **翻哪些字段用户选**（见 settings.KeyJavTranslateFields）—— 有人只要简介，
//     有人只要标题，不强制全翻。
//
// # 提示词用户可改
//
// `settings.KeyJavTranslatePrompt`，空 = 用 aiorganize 里的默认那份。
// 用户想加「台湾用语」「保留番号」这类要求，改这一条即可。

// translateSourceAI 是「这行中文标题是 AI 翻的」这个来源标记。
//
// 用它区分「AI 翻的」与「别站补来的」（airav / missav 那些站自己就给中文标题）。
// `translateAndPersist` 据此决定要不要走标题那条侧车通道 ——
// 别站补来的标题由补缺链那条路自己推，不该重复推。
const translateSourceAI = "ai"

// translateFields 把这部片的字段翻译一遍（**已经是中文的跳过**）。
//
// 返回「真的翻到了什么」—— 调用方据此决定要不要再推一次侧车。
//
// ⚠️ **在 applyPatch 之后调**（那时字段已经在 movie 上了）。译文写回 movie 的
// 同一个字段，由调用方随后的 Upsert + 侧车推送一起落盘。
func (s *Service) translateFields(ctx context.Context, movie *domain.JavMovie) bool {
	if s.translator == nil || movie == nil {
		return false
	}
	if !s.translateEnabled() {
		return false
	}
	if !s.translator.Available() {
		// 用户开了翻译但没配 AI（或没开 AI）：**安静跳过**。
		// 这是配置状态，不是故障 —— 与补缺链某一家挂了的处理同一条规矩。
		return false
	}
	fields := s.translateFieldSet()
	if len(fields) == 0 {
		return false
	}
	prompt := s.translatePrompt()
	changed := false

	// 翻成功的记录一笔（否则真机验证时**看不见它跑没跑** —— 成功路径原来是静默的）。
	defer func() {
		if changed {
			s.logInfo("jav translated", "number", movie.Number,
				"fields", strings.Join(translatedFields(fields), ","))
		}
	}()

	if fields["summary"] {
		if src := strings.TrimSpace(movie.Summary); src != "" && !isChinese(src) {
			if out, err := s.translator.Translate(ctx, prompt, src); err == nil && strings.TrimSpace(out) != "" {
				// ⚠️ **原文保留在侧车 json 的 `summary_original`**（由调用方的
				// 侧车推送带出去）—— 译文覆盖 `Summary` 是为了让 nfo 的 `<plot>`
				// 直接是中文（用户要的），但原文不该在这一步永久丢掉：
				// 它是唯一能核「AI 翻得对不对」的依据。
				movie.SummaryOriginal = src
				movie.Summary = strings.TrimSpace(out)
				changed = true
			} else if err != nil {
				s.logInfo("jav translate summary failed", "number", movie.Number, "err", err)
			}
		}
	}
	if fields["title"] {
		// 标题翻进 `TitleZH`（那一列的语义就是「另存的中文标题」）——
		// 所以**不会动** JAVDB 那行原文（它在 `Title` / `OriginTitle` 里）。
		if src := strings.TrimSpace(movie.Title); src != "" && strings.TrimSpace(movie.TitleZH) == "" && !isChinese(src) {
			if out, err := s.translator.Translate(ctx, prompt, src); err == nil && strings.TrimSpace(out) != "" {
				movie.TitleZH = strings.TrimSpace(out)
				movie.TitleZHSource = translateSourceAI
				changed = true
			} else if err != nil {
				s.logInfo("jav translate title failed", "number", movie.Number, "err", err)
			}
		}
	}
	return changed
}

// translatedFields 取这次开了哪几个字段（给日志用，顺序稳定）。
func translatedFields(set map[string]bool) []string {
	out := make([]string, 0, 2)
	for _, f := range []string{"summary", "title"} {
		if set[f] {
			out = append(out, f)
		}
	}
	return out
}

// isChinese 报告一段文本「已经是中文」。
//
// 判据：**汉字占非空白字符的比例 ≥ 一半**。
//
// # 为什么不是「含几个汉字就算」
//
// 日文里大量用汉字（`一ヶ月間の禁欲の果てに彼女の…` 里汉字不少），
// 按「含汉字即中文」会把日文全判成中文、一句都不翻。
// 反过来，日文里有**假名**（ひらがな / カタカナ）这一显著特征 ——
// 所以判据是「汉字占比高 **且** 假名很少」。
//
// 这个组合实测有效：`刺激您五感的三上悠亞頂級自慰協助…`（中文）与
// `S1スリム美女優の豪華共演エモドラマ作！僕の彼女は…`（日文）能被正确分开。
//
// # 已知的局限：**纯汉字的日文标题会被判成中文（跳过）**
//
// `極上泡姫物語 Vol.156 : 新城由衣` 这种标题**一个假名都没有**，与中文在字面上
// 无法区分（没有词典就只能猜）。这里刻意让它落到「中文 → 跳过」那一侧：
//
//   - 跳过最多是「标题没翻」，而纯汉字串中文读者本来就读得懂；
//   - 判反了会**白花一次 AI 额度、还可能把原本通顺的中文改写坏**。
//
// 两害相权，跳过是安全的那一侧。
func isChinese(s string) bool {
	var han, kana, other int
	for _, r := range s {
		switch {
		case r == ' ' || r == '\n' || r == '\t' || r == '\r':
			// 空白不计入分母
		case unicode.In(r, unicode.Han):
			han++
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			kana++
		default:
			other++
		}
	}
	total := han + kana + other
	if total == 0 {
		return false
	}
	// 假名超过 5% 就按日文处理（中文文本里几乎不出现假名）。
	if float64(kana)/float64(total) > 0.05 {
		return false
	}
	// 汉字要占非空白字符的一半以上（英文/数字占多数时不算中文）。
	return float64(han)/float64(total) >= 0.5
}

// ————————————————————— 设置读取 —————————————————————

func (s *Service) translateEnabled() bool {
	return s.settings != nil && s.settings.Bool(settings.KeyJavTranslateEnabled)
}

// translateFieldSet 读「翻哪些字段」，返回一个集合。
//
// 解析失败/空 → 返回空集（= 不翻），**不回落成「全翻」**：
// 静默地开始花用户的 AI 额度，比什么都不做更糟。
func (s *Service) translateFieldSet() map[string]bool {
	out := map[string]bool{}
	if s.settings == nil {
		return out
	}
	raw := s.settings.StringAllowEmpty(settings.KeyJavTranslateFields)
	for _, f := range []string{"summary", "title"} {
		if jsonListHas(raw, f) {
			out[f] = true
		}
	}
	return out
}

func (s *Service) translatePrompt() string {
	if s.settings == nil {
		return ""
	}
	return strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyJavTranslatePrompt))
}

// jsonListHas 在一个 JSON 字符串数组里找一个元素（`["summary","title"]`）。
//
// 手写而不是 `json.Unmarshal`：这个字符串来自设置页、可能被用户改坏，
// 而这里要的只是「有没有这一项」，解析失败时返回 false 就够了。
func jsonListHas(raw, want string) bool {
	return strings.Contains(raw, `"`+want+`"`)
}
