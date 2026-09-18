package tgsubscribe

import (
	"net/url"
	"sort"
	"strconv"
	"strings"

	"litepan/internal/tgsubscribe/preview"
	"litepan/internal/tgsubscribe/telegram"
)

// 频道体检。
//
// 存在的理由：网页预览抓取有一整类「静默失败」——频道照常发帖、插件照常记账，
// 但一条资源都抽不出来。实测两个真实频道各是一种典型：
//
//	QukanMovie      按钮一大堆，但下载链接要点进 @xxx_bot 私聊机器人才拿得到
//	oneonefivewpfx  正文 40 条链接全指向第三方中转站 re0.me
//
// 老实现只在「内联按钮数为 0」时提示「可能是用点击复制按钮发资源」——
// 对前者漏报（按钮很多，不告警），对后者误报（真实原因跟复制按钮无关）。
// 现在改成：对最近一页帖子跑一遍**真实抽取器**，把结果按类型数出来，
// 再按实际命中的模式给出互斥的结论。
//
// 计数只统计、不落库 —— 体检是只读操作。

// ProbeResourceCount 是体检报告里的一行：某种资源类型在最近一页里命中了多少。
type ProbeResourceCount struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Count int    `json:"count"`
	// Deliverable 表示当前版本**在类型层面**能不能把它投递出去。
	// 「抓到了但投不了」和「压根抓不到」是两种完全不同的处置，前端要能分开显示。
	//
	// ⚠️ 只说类型，不说账号：体检按频道做，没有账号上下文。
	// 115 分享就属于「类型支持、但要账号配了网页 Cookie 才真的能转」，
	// 这一层由 probeWarnings 用文字交代，不在这里下结论。
	Deliverable bool `json:"deliverable"`
}

// probeKindOrder 是报告里资源类型的固定展示顺序（与 newRegistry 的优先级一致）。
var probeKindOrder = []string{KindMagnet, KindED2K, KindHTTP, KindShare115, KindShareQuark}

// probeScan 是对一页帖子跑一遍抽取的汇总。
type probeScan struct {
	// posts 是这一页的帖子数，只用来拼提示文案。
	posts  int
	counts map[string]int
	// hosts 是正文链接里「既不是网盘分享域、也不是 Telegram 自己的域」的域名。
	// 剩下这些基本就是中转站、推广站、详情页 —— 抓不到资源的原因通常就在这儿。
	hosts map[string]struct{}
	// botLinks 是内联按钮里指向 <xxx>_bot 深链的数量。
	botLinks int
	// discarded 是「抽到了资源，但发布名不像影视资源」因而不会被落库的帖子数。
	// 不数它的话，用户会看到「识别到 16 条 115 分享」却一条历史都没有，更困惑。
	discarded int
}

// scanPage 对一页帖子跑真实抽取器，汇总各类资源的命中情况。
func (s *Service) scanPage(page *preview.Page) probeScan {
	return s.scanPosts(page.Posts)
}

// scanPosts 是 scanPage 的实现体。
//
// 单独抽出来是因为除了「最近一页」，还有第二个入口需要同一份统计：
// 频道内关键词搜索（subscribe.go 的 SearchHistory）。两边必须用同一套判据，
// 否则搜索报告里的数字与体检报告里的对不上，用户没法互相对照。
func (s *Service) scanPosts(posts []preview.Post) probeScan {
	scan := probeScan{counts: map[string]int{}, hosts: map[string]struct{}{}}
	scan.posts = len(posts)
	for i := range posts {
		msg := posts[i].Message
		scan.collectHosts(msg)
		scan.botLinks += botDeepLinkCount(msg)

		refs := s.registry.Extract(msg)
		if len(refs) == 0 {
			continue
		}
		for _, ref := range refs {
			scan.counts[ref.Kind]++
		}
		// 与 handler 里那道 LooksLikeRelease 门槛用同一个判据 —— 这里数出来的
		// 「被丢弃」必须和真正落库时的行为一致，否则这个数字会误导人。
		usable := false
		for _, ref := range refs {
			if ParseReleaseName(resourceFromRef(ref, msg).DisplayName).LooksLikeRelease() {
				usable = true
				break
			}
		}
		if !usable {
			scan.discarded++
		}
	}
	return scan
}

// collectHosts 收集正文 text_link 里指向第三方站点的域名。
func (s *probeScan) collectHosts(msg *telegram.Message) {
	for _, ent := range msg.Entities {
		if ent.Type != "text_link" || strings.TrimSpace(ent.URL) == "" {
			continue
		}
		u, err := url.Parse(strings.TrimSpace(ent.URL))
		if err != nil {
			continue
		}
		host := strings.ToLower(u.Hostname())
		if host == "" || telegram.IsShareHost(host) || telegram.IsNoiseHost(host) {
			continue
		}
		s.hosts[host] = struct{}{}
	}
}

// total 是抽到的资源总数。
func (s probeScan) total() int {
	n := 0
	for _, c := range s.counts {
		n += c
	}
	return n
}

// shares 是分享类资源的命中数（两类分享链之和）。
func (s probeScan) shares() int {
	return s.counts[KindShare115] + s.counts[KindShareQuark]
}

// topHosts 返回排好序的前 n 个第三方域名。
//
// 排序是为了输出稳定：map 的顺序随机，不排的话同一个频道每次体检的提示文案都可能不同。
func (s probeScan) topHosts(n int) []string {
	out := make([]string, 0, len(s.hosts))
	for h := range s.hosts {
		out = append(out, h)
	}
	sort.Strings(out)
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// counts 把命中情况转成给前端的有序列表（固定顺序、含中文名与可投递性）。
func (s *Service) probeResourceCounts(scan probeScan) []ProbeResourceCount {
	out := make([]ProbeResourceCount, 0, len(probeKindOrder))
	for _, kind := range probeKindOrder {
		out = append(out, ProbeResourceCount{
			Kind:        kind,
			Label:       labelKind(kind),
			Count:       scan.counts[kind],
			Deliverable: s.delivererFor(kind) != nil,
		})
	}
	return out
}

// probeWarnings 按实际命中的模式给出互斥的结论。
//
// 顺序即优先级：先解释「一条都没抽到」的三种典型成因，再解释「抽到了但投不了」。
func (s *Service) probeWarnings(scan probeScan, urlButtonCount int) []string {
	posts := strconv.Itoa(scan.posts)
	total := scan.total()

	if total == 0 {
		switch {
		case scan.botLinks > 0:
			return []string{"最近 " + posts + " 条帖子里有 " + strconv.Itoa(scan.botLinks) +
				" 个指向机器人（@xxx_bot）的按钮或深链。实际下载地址要点进去私聊机器人才拿得到，" +
				"网页预览抓不到 —— 建议换一个把下载链接直接写在正文或做成链接按钮的频道。"}
		case len(scan.hosts) > 0:
			return []string{"最近 " + posts + " 条帖子的正文链接都指向第三方站点（" +
				strings.Join(scan.topHosts(3), "、") + "），网页预览抓不到实际下载地址。" +
				"这类频道通常要靠中转站或机器人再跳一次，无法订阅。"}
		case urlButtonCount == 0:
			return []string{"最近 " + posts + " 条帖子里没有任何可抓取的下载链接。" +
				"如果这个频道用「点击复制」按钮发资源，网页预览不渲染这类按钮，抓不到 —— 建议换一个频道。"}
		default:
			return []string{"最近 " + posts + " 条帖子里的按钮都指向频道互推或其它页面，" +
				"没有一处是下载地址 —— 这个频道抓不到内容。"}
		}
	}

	var deliverable, undeliverable []string
	for _, item := range s.probeResourceCounts(scan) {
		if item.Count == 0 {
			continue
		}
		line := item.Label + " " + strconv.Itoa(item.Count)
		if item.Deliverable {
			deliverable = append(deliverable, line)
		} else {
			undeliverable = append(undeliverable, line)
		}
	}

	var warnings []string
	// 「识别到了但推不出去」这条**总是**要说，不管同不同时存在能投递的类型。
	// 实测 QukanMovie 就是这种混合情况：16 条资源里 15 条是投不了的分享链，
	// 只有 1 条 ed2k 能推。只报「有能推的」会让用户以为自己没漏东西。
	if len(undeliverable) > 0 {
		line := "最近 " + posts + " 条帖子里识别到 " + strings.Join(undeliverable, "、") +
			"，当前版本只能识别、不能投递 —— 它们会以「暂不支持投递」出现在匹配历史里。"
		if len(deliverable) == 0 {
			line = "最近 " + posts + " 条帖子里没有可投递的资源。" + line
		}
		warnings = append(warnings, line)
	}
	// 115 分享链的前置条件必须单独说：体检是**按频道**做的，没有账号上下文，
	// 判断不了「这个账号配没配 Cookie」，所以只能把前提讲清楚而不是下结论。
	// 不讲的话用户会看到「115 分享 15」却没有一条进历史，然后去翻日志。
	if scan.counts[KindShare115] > 0 {
		warnings = append(warnings, "其中 115 分享链需要目标账号配置「网页 Cookie」才能自动转存"+
			"（115 的分享转存只有网页接口能做，开放平台令牌不行）；没配的账号上这些记录会以「暂不支持投递」出现。")
	}
	if scan.discarded > 0 {
		warnings = append(warnings, "另有 "+strconv.Itoa(scan.discarded)+
			" 条帖子抽到了资源，但发布名里看不出片名或画质，不会进入匹配历史。"+
			"如果这些正是你要的资源，说明该频道的命名方式不适配当前的解析规则。")
	}
	return warnings
}

// botDeepLinkCount 数一条消息里指向机器人的按钮。
//
// 放在这里而不是 preview 包：解析层只该产出结构事实（按钮有哪些 URL），
// 「什么样的 URL 算机器人深链」是产品判断。
func botDeepLinkCount(msg *telegram.Message) int {
	if msg == nil || msg.ReplyMarkup == nil {
		return 0
	}
	n := 0
	for _, row := range msg.ReplyMarkup.InlineKeyboard {
		for _, btn := range row {
			if isBotDeepLink(btn.URL) {
				n++
			}
		}
	}
	return n
}

// isBotDeepLink 识别「点进去找机器人要链接」的按钮。
//
// 两个判据，任一命中即可：
//   - 用户名以 `_bot` 结尾（tougao115guaguale_bot?start=qtrans_3836）；
//   - URL 带 `?start=` —— 这是 Telegram 机器人深链的标准参数（频道链接不用它）。
//     实测频道里就有名字不带 `_bot` 后缀的机器人（jisou2?start=a_912917457），
//     只看后缀会漏掉它们。
func isBotDeepLink(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host != "t.me" && host != "telegram.me" {
		return false
	}
	seg := strings.Trim(u.Path, "/")
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	if strings.HasSuffix(strings.ToLower(seg), "_bot") {
		return true
	}
	return u.Query().Has("start")
}
