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
		//
		// ⚠️ **2026-10-16 修的一处真 bug：判据只看 `Title`，不看 `TitleZH`。**
		//
		// 原来还带一条 `strings.TrimSpace(movie.TitleZH) == ""`（「另存栏是空的才翻」），
		// 而事实是补缺链**自己就把标题写进 `TitleZH`** —— 而且是**日文**：
		//
		//   - dmm / missav / airav / avsox / freejavbt / javbus 这些站给的标题
		//     走的就是 `TitleZH` 这一个字段（见各站的 enricher）；
		//   - 真机样本：DVAJ-743 的 `TitleZH` 是「ニップルチェーン嵌められ…」、
		//     HAWA-370 是「生活に困り自宅でメンズエステを…」、CAWB-033 是
		//     「少女界史上、過去最高イニイオンナ…」—— 三栏全假名。
		//
		// 于是这条闸把**最该翻的那批**恰好挡在外面：上游在这条链之前往 `TitleZH`
		// 里塞了一份日文，翻译就被判成「已经有中文标题了，跳过」。
		// 日志里 `jav translated {"fields":"summary,title"}` 照记不误（那句话只说明
		// **有**字段翻了 —— 简介翻了就算），所以症状是「简介翻好了、标题一个字没动」，
		// 而界面上两栏都还是日文。
		//
		// 现在的判据是「**这部片现在显示的那份标题是不是中文**」——`TitleZH` 非空就取它、
		// 否则取 `Title`（与侧车生成时 `pickTitleZH` 那条链逐字相同，见
		// `titleSourceToTranslate`）：是中文就一个字都不发，不是才拿去翻。
		//
		// 先 `TitleZH` 后 `Title` 不是随手写的：`TitleZH` 是「另存的中文标题」那一栏，
		// 别站补来的中文就在那里（airav / missav 的那些）—— 那是白拿的中文，
		// 优先级高于 JAVDB 的原文。⚠️ 但这也意味着「`TitleZH` 是中文、`Title` 是日文」
		// 时**不翻**：手上的中文已经能读了，拿日文原文去翻只会把它换成机器译文。
		//
		// 代价说清楚：这里**不查来源标记**（`TitleZHSource`）。别站补来的日文
		// 同样会再翻一次，而那种「上游给的日文」在这一栏里是绝大多数 ——
		// 宁可多花一次 AI 额度，也不要漏翻（这正是原来那版的反向错误）。
		if src := titleSourceToTranslate(movie); src != "" {
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

// sidecarTitleZH 返回「该推给侧车的那份中文标题」，没有就返回空串。
//
// 这个判据有三个调用方（`translateAndPersist`、`backfillMovie` 的收尾推送、
// 整批回写 `pushTitleZHBatch`），所以抽出来 —— 两处各写一遍的话，
// 「什么时候推标题」会出现两套答案，而表现正是这次要修的那类症状
// （库里是中文、侧车/界面还是日文）。补缺链那条（`fillMissingFields`）手上没有
// 库记录，走的是同判据的取值形态 `sidecarTitleZHFrom`。
//
// 两条都收：
//   - **AI 刚翻出来的**（来源标记是 `ai`，见 translateSourceAI）；
//   - **别站补来的中文**（dmm / missav / airav / javbus… 自己就给中文标题）。
//
// ⚠️ 第二条是 2026-10-16 补的。原来只认第一条，于是上游给的中文标题永远推不出去：
// 侧车 json 的 `title` 是 nfo `<title>` 的唯一来源，而 nfo 是「存在即跳过」——
// 结果全库有 84 部「侧车 json 的标题是中文、nfo 的 `<title>` 是日文」。
// 真机样本 MNGS-068：库里「超高级中出专门泡泡浴 七濑爱丽丝」，nfo 里
// 「超高級中出し専門ソープ 七瀬アリス」。
//
// sidecarTitleZH 的判据是 `isChinese`，而不是「来源标记是某站」：标记只说明
// **谁写的**，说明不了**写的是中文还是日文**（dmm 两样都给过）。看字最准。
//
// # 与 `LookupTitleZHForSidecar` 的关系（两把尺子，不是重复实现）
//
// 那一条服务的是**另一个场景**：它每份侧车每轮都被扫到（`sidecarSyncOnce` 每小时、
// 海报墙重建/整理每跑一次），所以判据收得更紧（还要求 `title_zh` 与 `title` 不同，
// 或来源是 `ai`/`missav`）—— 见那条的注释里「与 sidecarTitleZH 的分工」。
// 两条的取舍方向不同是有意的：这里宁可多推一次（错在「推了一份能读的中文」），
// 那里宁可少写一次（错在「往侧车里灌了日文」）。
func sidecarTitleZH(m *domain.JavMovie) string {
	if m == nil {
		return ""
	}
	return sidecarTitleZHFrom(m.TitleZH, m.TitleZHSource)
}

// sidecarTitleZHFrom 是上面那条的**取值形态**：给一栏标题 + 它的来源标记，
// 返回「该推给侧车的那一份」，不适用时返回空串。
//
// # 为什么要有这个形态（2026-10-17 补的那条候选链）
//
// 调用点手上未必有一整条库记录：补缺链那一步拿到的是**上游刚给的那份标题**
// （`javdb.NormalizedMovie.TitleZH`，还没跟库合并），而它要问的是
// 「这一次推下去，侧车最后拿到的**是不是还是中文**」—— 判据必须能同时套在
// 「上游新给的」与「库里原有的」两栏上，所以取值与判据要分开。
//
// 判据与 `sidecarTitleZH` **逐字同一个**，两处各写一遍迟早会出现两套答案。
func sidecarTitleZHFrom(titleZH, source string) string {
	zh := strings.TrimSpace(titleZH)
	if zh == "" {
		return ""
	}
	if source == translateSourceAI || isChinese(zh) {
		return zh
	}
	return ""
}

// titleSourceToTranslate 挑出「该拿去翻的标题原文」，返回空串表示不用翻。
//
// # 判据只有一条：**这部片现在显示的那份标题还不是中文**
//
// 「现在显示的那份」= `TitleZH` 非空就取它、否则取 `Title` ——
// 这与侧车/nfo 生成时那条链**逐字相同**（`sidecar.go` 的
// `pickTitleZH(m.TitleZH, m.Title)`）。这一致性是必须的：翻译改的既然就是
// 那一份，判据就必须看那一份。
//
// # 为什么不能「TitleZH 不是中文就退到 Title」
//
// 试过，被单测逮住了：那样在 `TitleZH` 是**中文**、`Title` 是日文时
// （别站已经补来了干净的中文标题）会拿 JAVDB 那份日文去翻，然后把翻出来的
// 东西**盖掉**手上的中文标题 —— 白花一次 AI 额度，还把好的换成了机器译文。
//
// 反过来，`TitleZH` 是**日文**时（dmm / missav / airav / avsox / freejavbt /
// javbus 这些补缺源给的日文就写在这一栏，真机样本 DVAJ-743
// 「ニップルチェーン嵌められ…」、CAWB-033「少女界史上、過去最高イイオンナ…」）
// 翻的正是它 —— 而这一批恰好是原来那句「`TitleZH` 空着才翻」挡在外面的，
// 也就是这次要修的 bug。
//
// # 为什么不做「有没有来源标记」的判断
//
// `TitleZHSource` 记的是**上一次写进这一栏的那个源的名字**
// （`dmm` / `missav` / `ai`…），而「那个源给的是中文还是日文」它表达不了
// （dmm 的标题实测有中文也有日文）。拿它当判据等于用一把量不了这件事的尺子，
// 结果只会是「有的翻了、有的没翻」—— 而**漏翻**正是这次要修的 bug。
// 直接看字，一个字都不用猜。
//
// ⚠️ 拿 `Title` 而不是 `OriginTitle`：`OriginTitle` 是**原样保存的官方原名**，
// 它不该参与翻译（真机上它有额外的版本尾缀，如 DVAJ-743 多出「パンティと生写真セット」）。
// 用它当原文会让译文带上一个莫名其妙的后缀。
//
// # ⚠️ `TitleZHSource == "ai"` 时直接不翻（2026-10-17 补）
//
// 这一栏**已经有一份 AI 译文**了。判据（`isChinese`）说它还不是中文时，绝大多数
// 是「译文里混进了日文」—— 真机量过 8 部，两种成因：
//
//   - 模型把**日文演员名**原样留在译文里（FC2-2890131「…中年教师凉●れむ」）；
//   - 模型压根没翻，回了一句**关于怎么翻的话**
//     （PAI-378「"なお"可以翻译成中文里常见的"另外"…」）。
//
// 这两种情况下重翻都不会变好：第一种每次都会被同一个演员名绊住，
// 第二种换一次随机性也一样 —— 而代价是**每天一次 AI 请求 × 8 部，永远不停**
// （新加的那条「标题没翻就补翻」的后台循环会一直把它们当候选）。
//
// 与 `sidecarTitleZH` 那条**并不矛盾**：那条认「来源是 ai 就直接当可用译文推下去」，
// 判据同为 `isChinese` 只是兜底（别站补来的中文没有 ai 标记）。
// 两处合起来的语义是：**AI 译过就认，不再问第二次**。
func titleSourceToTranslate(m *domain.JavMovie) string {
	if m == nil {
		return ""
	}
	// 已经有 AI 译文了，不再重复问（见上面那段）。
	if m.TitleZHSource == translateSourceAI {
		return ""
	}
	// 与 `pickTitleZH` 同一条链：另存栏优先，空才回落到 JAVDB 那行原文。
	cur := strings.TrimSpace(m.TitleZH)
	if cur == "" {
		cur = strings.TrimSpace(m.Title)
	}
	if cur == "" || isChinese(cur) {
		return ""
	}
	return cur
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
// 判据两条，**同时**满足才算中文：
//
//	① 一个假名都没有（ひらがな / カタカナ 都不许出现）；
//	② 汉字占非空白字符的比例 ≥ 一半。
//
// # 为什么不是「含几个汉字就算」
//
// 日文里大量用汉字（`一ヶ月間の禁欲の果てに彼女の…` 里汉字不少），
// 按「含汉字即中文」会把日文全判成中文、一句都不翻。
// 反过来，日文有**假名**这一显著特征 —— 所以是「汉字占比高 **且** 没有假名」。
//
// 这个组合实测有效：`刺激您五感的三上悠亞頂級自慰協助…`（中文）与
// `S1スリム美女優の豪華共演エモドラマ作！僕の彼女は…`（日文）能被正确分开。
//
// # 为什么假名的闸是「等于 0」而不是「≤ 5%」（2026-10-17 改）
//
// 原来写的是 `kana/total > 0.05 → 日文`，理由是「中文文本里几乎不出现假名」。
// 这个理由**反了**：正因为中文里不出现假名，出现一个就说明它**不是**中文。
// 而「5%」这个余量实际放过的是日文 —— 短标题里一两个假名就顶到 5% 以下，
// 于是 `全編中出し！濃厚母子性交` 这类**妥妥的日文**被判成中文、永不翻译。
// 用户的原话是「日本很多像中文」，指的正是这一族。
//
// 真机量过（16827 部的库）：闸从 5% 收到 0，**40 条标题翻面**
// —— 其中 23 条是真漏翻的日文（改完开始翻译），17 条是「中文 + 日文演员名」
// （会被多翻一次，代价是一次 AI 额度）。宁可多翻一次，也不要永远不翻。
//
// # 为什么「一个假名都没有」不能省（而不是干脆只看汉字占比）
//
// 只看汉字占比，`極上泡姫物語 Vol.156 : 新城由衣`（假名 0、汉字过半）判中文 ——
// 这条**故意**保留，见下面「已知的局限」。
// 但 `S1スリム美女優の豪華共演エモドラマ作！僕の彼女は…` 汉字也过半，
// 靠的就是假名那条把它挡下来。两条缺一不可。
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
//
// # ⚠️ 判假名**必须用 Unicode 的 Script 表，不能按码位区间**
//
// `・` U+30FB（片假名中点）与 `ー` U+30FC（长音符）**落在**片假名区块里，
// 但 Script 是 Common —— 中文标题拿 `・` 当分隔符很常见
// （真机样本 DASS-972「没有剧本・演出・演技，完全依照本能…」）。
// 按 `0x3040–0x30FF` 判会把它们当假名，于是正常的中文标题被判成日文、每天重翻。
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
	// ① 假名一个都不许有（中文文本里不出现假名，出现一个就说明不是中文）。
	if kana > 0 {
		return false
	}
	// ② 汉字要占非空白字符的一半以上（英文/数字占多数时不算中文）。
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
