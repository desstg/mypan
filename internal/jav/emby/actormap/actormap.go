// Package actormap 把「同一个演员的多个名字」归并成一个统一名。
//
// 数据是同目录的 `mapping_actor.xml`（`go:embed` 进二进制，随镜像一起发），
// 结构见文件头的说明：
//
//	<a zh_cn="AIKA" zh_tw="AIKA" jp="AIKA"
//	   keyword=",AIKA,優木あいか,平岡歩,…," href="https://javdb.com/actors/z0GW"/>
//
// `keyword` 是**别名表**（逗号分隔、首尾各带一个逗号），用它去匹配刮削到的
// 演员名；`zh_cn` / `zh_tw` 是统一后的输出名。
//
// # 用在哪
//
// **只用在写 nfo 那一步**（`emby.MovieMetaFromSidecar`）：把 `<actor><name>`、
// `<set>`、`<genre>` 里的演员名换成统一名。数据库、界面、演员订阅**都不动** ——
// 那几处要的是「上游给的原始名字」，改了会让演员页与订阅目标对不上。
//
// # 索引表里没有的演员怎么办
//
// **原样返回**（用户明确要求：索引表没有的演员，程序不能出错，回到现在的行为）。
// 所以这张表是一层**可选的增强**，不是必过的关卡 —— 缺条目、缺语言字段、
// 甚至整个文件读不出来，都只是「少了一层归并」，nfo 照旧生成。
package actormap

import (
	_ "embed"
	"encoding/xml"
	"os"
	"sort"
	"strings"
	"sync"
)

// xmlActor 是 <a> 元素。属性名与 XML 一一对应。
type xmlActor struct {
	ZhCN    string `xml:"zh_cn,attr"`
	ZhTW    string `xml:"zh_tw,attr"`
	Keyword string `xml:"keyword,attr"`
	// Href 是演员在 JAVDB 的主页（`https://javdb.com/actors/z0GW`）。
	//
	// 表里 8000 多条几乎都填了它。**我们不打开那个页面**（要登录，302 → /login），
	// 只从里面取那个短 id —— 头像地址就是由它拼出来的：
	// `https://c0.jdbstatic.com/avatars/<id 前两位小写>/<id>.jpg`。
	// 见 strmscrape 的演员资料层。
	Href string `xml:"href,attr"`
}

type xmlRoot struct {
	Actors []xmlActor `xml:"a"`
}

// Table 是「别名 → 统一名」的查表。
//
// 零值不可用（map 是 nil），请用 Parse / Default 取。
type Table struct {
	byAlias map[string]string
	// byUnified 反查：统一名 → 别名列表（保序、**不含统一名本身**）。
	// 演员详情页要显示「别名」，靠的就是它。
	byUnified map[string][]string
	// byJavdbID 反查：统一名 → JAVDB 演员短 id（`z0GW` 这种）。
	//
	// 表里的 `href` 就带这个 id，而 JAVDB 的头像地址可以直接由它拼出来
	// （见 javdbActorAvatarURL）。**不打开那个页面** —— 它要登录。
	byJavdbID map[string]string
	// unifiedEntries 数「每个统一名在表里有几条**条目**」（不是几个别名）。
	//
	// 实测有 75 个统一名被两条条目共用（`真木今日子` 就有 qgpP 与 Y3MD 两条）——
	// 那是**同名不同人**，统一名当不了「同一个人」的判据。见 Canonical。
	unifiedEntries map[string]int
	entries        int
}

// Aliases 返回某个统一名对应的别名（保序、不含统一名本身）。
//
// 查不到（没这条、或统一名写法对不上）返回 nil —— 调用方照常显示即可。
func (t *Table) Aliases(unified string) []string {
	if t == nil || t.byUnified == nil {
		return nil
	}
	return t.byUnified[strings.TrimSpace(unified)]
}

// JavdbID 返回某个统一名对应的 JAVDB 演员短 id（`z0GW`）。
//
// 查不到返回空串。id 只用来拼头像地址，**不用来打开** JAVDB 的演员页
// （那个页面要登录，匿名访问会 302 到 /login）。
func (t *Table) JavdbID(unified string) string {
	if t == nil || t.byJavdbID == nil {
		return ""
	}
	return t.byJavdbID[strings.TrimSpace(unified)]
}

// Lookup 查这个名字在表里归到哪一条。ok 为假时调用方应原样使用输入名。
//
// 与 Resolve 的区别是它把「表里没有」与「表里有、统一名恰好等于原名」分开 ——
// 后者 `Resolve` 同样返回原名，但界面上可以据此知道「这条是索引表认识的」。
func (t *Table) Lookup(name string) (string, bool) {
	if t == nil || t.byAlias == nil {
		return "", false
	}
	unified, ok := t.byAlias[strings.TrimSpace(name)]
	return unified, ok
}

// Len 返回条目数（不是别名数）—— 给日志与测试看。
func (t *Table) Len() int {
	if t == nil {
		return 0
	}
	return t.entries
}

// Canonical 返回这个名字可以**当身份用**的那个写法：表里认得、且归到的统一名
// 在表里只有一条条目时给统一名，否则原样返回（去掉首尾空白）。
//
// # 与 Resolve 的区别（这个区别是分页聚合的前提）
//
// `Resolve` 回答的是「**显示**成什么」，它宁可把「同名不同人」也并到一个漂亮名字上；
// `Canonical` 回答的是「**这两个名字是不是同一个人**」，它必须承认自己不确定。
// 表里实测有 75 个统一名被两条条目共用（`真木今日子` = qgpP 与 Y3MD，
// `白井ナナ` = K4gKv 与 d4zD0），拿统一名当聚合键会把两位不同的人焊成一位 ——
// 那比「一件演员两张卡」更糟：详情里会出现**别人**参演的片。
//
// 所以这里对共用统一名的条目**主动放弃**归并能力（返回原名），
// 它们的卡仍然各自独立 —— 与改造前的行为完全一致。
//
// 别名表本身不用这个判据：那 75 个统一名各自的别名几乎没有交集，
// 同一个别名落到哪一条由 Parse 的裁决规则定死了。
func (t *Table) Canonical(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || t == nil || t.byAlias == nil {
		return name
	}
	unified, ok := t.byAlias[name]
	if !ok || unified == "" {
		return name
	}
	if t.unifiedEntries[unified] > 1 {
		return name
	}
	return unified
}

// Resolve 把一个演员名换成统一名。
//
// **查不到就原样返回**（用户明确要求）。空串也原样返回 —— 调用方常常拿它
// 直接往 nfo 里写，返回空串会把演员抹掉。
//
// 匹配是**精确**的（不做大小写折叠、不做前缀/子串）：`keyword` 那种
// 「首尾带逗号」的写法本来就是为精确匹配设计的，而演员名里同名不同人
// 的情况真实存在（表里有 207 个别名指向多个条目），放宽匹配只会把两个人
// 并成一个。
func (t *Table) Resolve(name string) string {
	if t == nil || t.byAlias == nil {
		return name
	}
	key := strings.TrimSpace(name)
	if key == "" {
		return name
	}
	if unified, ok := t.byAlias[key]; ok {
		return unified
	}
	return name
}

// Parse 解析索引表。解析失败返回 error（调用方决定要不要降级）。
//
// # 冲突怎么裁决
//
// 同一个别名可能出现在多个条目里（实测 207 个，如 `ASUKA` 同时是
// `ASUKA` 与 `Asuka` 两个条目的别名）。裁决规则由用户定：
//
//  1. **原文完全相等的优先** —— 别名与该条目的输出名逐字相同时，
//     它就是「本人」，比「别人的别名」可信；
//  2. 否则**先出现的优先**（XML 里靠前的那个）。
//
// 规则写死在这里而不是留成参数：两种裁决在「同名不同人」上给出的答案
// 不同，而同一份 nfo 在不同机器上写成不同名字是最难查的一类问题。
func Parse(data []byte) (*Table, error) {
	var root xmlRoot
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	t := &Table{
		byAlias:        make(map[string]string, len(root.Actors)*2),
		byUnified:      make(map[string][]string, len(root.Actors)),
		byJavdbID:      make(map[string]string, len(root.Actors)),
		unifiedEntries: make(map[string]int, len(root.Actors)),
	}
	// href 单独记一份：同一统一名可能有多条（极少见），先出现的赢 ——
	// 与别名裁决的「先出现优先」同一口径，免得两份表各按各的规则。
	for _, a := range root.Actors {
		if unified := outputName(a); unified != "" {
			if id := javdbIDFromHref(a.Href); id != "" {
				if _, dup := t.byJavdbID[unified]; !dup {
					t.byJavdbID[unified] = id
				}
			}
		}
	}
	// firstSeen 记「这个别名是不是第一次见」，配合 exact 的升级实现
	// 「原文相等优先、其次先出现」—— 见下面循环里的两条分支。
	exact := make(map[string]bool, len(root.Actors)*2)
	for _, a := range root.Actors {
		unified := outputName(a)
		if unified == "" {
			continue
		}
		t.entries++
		t.unifiedEntries[unified]++
		for _, alias := range splitKeywords(a.Keyword) {
			if _, seen := t.byAlias[alias]; seen {
				// 已有一条。只有当「新条目是原文相等」而「旧的那条不是」时才顶掉
				// —— 这正是「原文相等优先」；两条都相等或都不相等时保持先出现的。
				if alias == unified && !exact[alias] {
					t.byAlias[alias] = unified
					exact[alias] = true
				}
				continue
			}
			t.byAlias[alias] = unified
			exact[alias] = alias == unified
		}
	}
	// 反查表要**在裁决之后**建：别名被别人抢走的那一条不该再列出来，
	// 否则演员详情里会显示一个其实指向别人的名字。
	t.buildUnifiedIndex()
	return t, nil
}

// buildUnifiedIndex 按最终裁决结果重建「统一名 → 别名」。
//
// 别名本身等于统一名时不列（那是「本人」，不是别名）；同一个别名只归一处
// （`byAlias` 已经裁决过），所以这里不会重复。
func (t *Table) buildUnifiedIndex() {
	if t.byUnified == nil {
		t.byUnified = make(map[string][]string, t.entries)
	}
	for alias, unified := range t.byAlias {
		if alias == unified {
			continue
		}
		t.byUnified[unified] = append(t.byUnified[unified], alias)
	}
	// map 遍历是无序的 —— 排一下，让「别名」那行的顺序在每次打开时都一样。
	for unified := range t.byUnified {
		sort.Strings(t.byUnified[unified])
	}
}

// outputName 取这个条目要输出的统一名：**zh_cn 优先，没有就 zh_tw**（用户定的）。
//
// 两个都空时返回空串 —— 那种条目整条跳过，而不是拿 `jp` 顶上：
// 用户没说要 jp，而 `jp` 在表里常是罗马音/假名（`リリー・ハート`），
// 与库里的中文标题风格不一致。宁可跳过（那部片的演员名原样保留）。
func outputName(a xmlActor) string {
	if s := strings.TrimSpace(a.ZhCN); s != "" {
		return s
	}
	return strings.TrimSpace(a.ZhTW)
}

// javdbIDFromHref 从 `https://javdb.com/actors/z0GW` 里取 `z0GW`。
//
// 不引 net/url 也不做严格解析：这是随二进制发的数据，形态就这一种；
// 拿到别的东西（空串、别人家的链接）时返回空串，调用方当作「没有」。
// id 的字符集实测是大小写字母 + 数字。
func javdbIDFromHref(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	const marker = "/actors/"
	idx := strings.LastIndex(href, marker)
	if idx < 0 {
		return ""
	}
	id := strings.Trim(strings.TrimSpace(href[idx+len(marker):]), "/")
	if id == "" || strings.ContainsAny(id, "/?&# ") {
		return ""
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	return id
}

// splitKeywords 拆 `,A,B,C,` 形式的别名表。//
// 首尾那两个逗号是原表的写法（也为精确匹配服务），拆的时候当空段丢掉。
// 顺带按 `,` 切分而不是按子串找 —— 别名里本身可能含空格、点、连字符。
func splitKeywords(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ————————————————————— 默认表 —————————————————————

//go:embed mapping_actor.xml
var builtinXML []byte

var (
	mu sync.RWMutex
	// current 是**当前生效**的表：默认是内置那份，LoadOverride 成功后换成覆盖的那份。
	current *Table
	// overridePath 记「已经尝试过哪条路径」，避免每次调用都 stat 一次盘。
	overridePath string
)

// Default 返回内置索引表（随二进制发的那份）。
//
// 解析失败返回 nil —— 调用方（Resolve 系列）对 nil 是安全的，
// 表现就是「这层归并没生效」，而不是把整条 nfo 生成链路带崩。
func Default() *Table {
	mu.RLock()
	if current != nil {
		t := current
		mu.RUnlock()
		return t
	}
	mu.RUnlock()

	mu.Lock()
	defer mu.Unlock()
	if current != nil {
		return current
	}
	t, err := Parse(builtinXML)
	if err != nil {
		// 内置数据解析不了 = 打包出了问题，但没有日志通道（本包不依赖
		// 调用方的 logger），只能退化成 nil。测试里有一条会钉住它不为 nil。
		return nil
	}
	current = t
	return current
}

// LoadOverride 尝试用**用户自己那份**索引表替换内置表（仿水印图标那套做法）。
//
// `path` 是 `data/mapping_actor.xml` 这类路径。三种情况：
//
//   - 文件不存在 → **保持内置表**（这是绝大多数部署的常态，不算错误）；
//   - 读得到但解析失败 → **保持内置表**并返回 error，让调用方记一条日志
//     （静默退回内置会让用户以为「我改了那份文件」，实际没生效）；
//   - 读得到且解析成功 → 换成它。
//
// 为什么放在 data/ 而不是只留内置那份：用户要能自己补条目（新演员、自家叫法），
// 而不必重新打包。内置那份保证**新装/新镜像开箱可用**。
//
// 幂等：同一路径重复调用只解析一次；换路径会重新解析。
func LoadOverride(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	mu.Lock()
	if overridePath == path && current != nil {
		mu.Unlock()
		return nil // 这个路径已经处理过了
	}
	overridePath = path
	mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 文件不在 = 用内置那份，这不是错误。
			return nil
		}
		return err
	}
	t, err := Parse(data)
	if err != nil {
		return err
	}
	mu.Lock()
	current = t
	mu.Unlock()
	return nil
}

// ResetToBuiltin 丢掉覆盖表、退回内置那份。给测试与「恢复默认」用。
func ResetToBuiltin() {
	mu.Lock()
	current = nil
	overridePath = ""
	mu.Unlock()
}
