package javrules

// 默认规则表：逐字照搬 115-auto/multipan/organize_rules.py:46-101。
//
// 那份文件本身又是旧面板 web.py 的移植版，且带 parity 测试钉死 ——
// 所以这里**不做任何顺手优化**，改了就会与老用户既有的库对不上。
//
// 注意：那份 Python 里还提到 data/junk_chars.json 是 Docker 时代的旧快照
// （多了 "(" 和 ")"），线上实际生效的是代码默认值，这里照抄的是**代码默认值**。

// DefaultSmallFileMB 小文件删除阈值（MB），对应旧面板 settings.json 的 delete_limit。
const DefaultSmallFileMB = 300

// fallbackJunkChars 广告/水印片段。改名时按列表顺序逐段字面删除（区分大小写）。
var fallbackJunkChars = []string{
	"hhd800.com@", "www.98T.la@", "[98t.tv]", "done-", "[约战竞技场]",
	"4k2.com@", "_GG5", "18bt.net_", "X1080X", "9288.PRO",
	"489155.com@", "@江南@jn998.vip-", "[Thz.la]", "[bbs.yzkof.com]",
	"gg5.co", "4k2.me@", "-nyap2p.com", "jav20s8.com", ".H265", "4k688.com@",
}

// fallbackReplaceRules 替换词。字面替换（非正则）、**不区分大小写**、按列表顺序全局替换。
//
// 相比 115-auto 那份做了两处调整，都是为了配合「不区分大小写」：
//
//  1. **去掉大小写重复项**。原表里 `4kfps` / `4KFPS` / `4kFPS` 折叠后是同一个词，
//     不区分大小写时后两条永远不可能命中，纯属噪音。
//  2. **按长度从长到短排列**。原表把 `4K` 放第一位 —— 区分大小写时没问题，
//     但不区分大小写后 `4k60` 里就含有 `4K`，先命中 `4K` 会把它变成 `-4K60`，
//     再也匹配不到 `4k60`。长的放前面才能让更具体的规则先命中。
//
// 这个顺序是**行为正确性**的一部分，不是审美问题，别随手调。
var fallbackReplaceRules = []ReplaceRule{
	{From: "_restored_prob4", To: "-U"},
	{From: "-restored-prob4", To: "-U"},
	{From: "-restored_prob4", To: "-U"},
	{From: "_restored", To: "-U"},
	{From: "-restored", To: "-U"},
	{From: "4kfps", To: "-4K"},
	{From: "4k60", To: "-4K"},
	{From: "4K", To: "-4K"},
}

// fallbackClassifyRules 分类规则，顺序即优先级，首个命中者胜。
//
// 相比旧面板只去掉了 target_cid（115 专属的目录 ID），改成 target_name ——
// 目标根目录下的相对子目录名，执行时按名找、找不到就建，这样同一套规则在所有网盘上都能用。
var fallbackClassifyRules = []ClassifyRule{
	{
		Name:       "国外",
		TargetName: "国外AV",
		Includes: []string{
			"BLACKED", "BLACKEDRAW", "BRAZZERS", "BRAZZERSEXTRA", "RKPRIME",
			"MILFY", "DIGITALPLAYGROUND", "TUSHY", "BANGBUS", "DAUGHTERSWAP",
			"CUMFIESTA", "TEENPIES", "MILFSOUP", "PENTHOUSEGOLD", "PRIVATE",
			"SEXMEX", "ASSHOLEFEVER", "ASSPARADE", "BBCPIE", "DEEPER",
			"FREEUSEFANTASY", "ILOVEPOV", "JAPANHDV", "IMADEPORN", "BAEB",
			"CLUBSWEETHEARTS", "THEREALWORKOUT",
			// 下面这批是**实测用户库里存在、但名单上没有**的站。点分日期型那条
			// pattern 已经能兜住形状（`BangBros18.19.09.17` 走 pattern 就进国外AV），
			// 这里补的是**形状认不出**的那些 —— 站名后面不带日期段，
			// 或者日期段被别的词隔开（`www.xBay.me - TeenFidelity E385 …`）。
			//
			// 名单来自扫用户库的全部点分日期型番号（55 种片商）+ 磁链名。
			// `BANGBROS18` / `BANGBROSCLIPS` / `BANGPOV` 与 `BANGBUS` 同族但**不互为前缀**
			// （`BANGBROS` 不是 `BANGBUS`），所以逐个列出。
			"VIXEN", "WIFEY", "BSURPRISE", "USEPOV",
			"TEENFIDELITY", "BANANAFEVER", "TEAMSKEET", "BRAZZERSEXXTRA", "TUSHYRAW",
			"BANGBROS18", "BANGBROSCLIPS", "BANGPOV", "PUBLICBANG",
			"BIGTITSATSCHOOL", "BIGTITSATWORK", "BIGTITSROUNDASSES", "BIGWETBUTTS",
			"BIGBUTTSLIKEITBIG", "BIGNATURALS", "BIGTITCREAMPIE", "BABYGOTBOOBS",
			"MOMMYGOTBOOBS", "MILFSLIKEITBIG", "PORNSTARSLIKEITBIG", "TEENSLIKEITBIG",
			"TEENSLOVEHUGECOCKS", "MONSTERSOFCOCK", "TITTYATTACK", "REALWIFESTORIES",
			"MIKEINBRAZIL", "DOCTORADVENTURES", "SNEAKYSEX", "SLAYED", "BADMILFS",
			"BROWNBUNNIES", "FAMILYSTROKES", "GFREVENGE", "AFTERDARK", "WORKMEHARDER",
			"HOTGIRLSGAME", "EXXXTRASMALL", "ZZSERIES",
		},
	},
	{
		Name:       "素人",
		TargetName: "FC2",
		Includes: []string{
			// `FC2-PPV` 与 `FC2-` 是相对上游加的两条，**修 bug 不是顺手优化**。
			//
			// includes 的匹配是纯 `strings.Contains`，不做分隔符归一化，所以上游那份
			// 只写 `FC2PPV` 时：`FC2-PPV-1234567`（FC2 最常见的写法）对不上。
			//
			// 后果不止「漏到别的分类」：`HasCode` 把 `FC2` 当番号，于是这些名字
			// 既不会命中 includes、也不会掉进「无番号」兜底 —— **整类永远不会被分档**，
			// 整理多少次都留在原地（实测：用户库里的 `FC2-4939195` 就是这个状态）。
			//
			// `FC2-` 单独一条是为了**改名后的形态**：目录/文件按番号改名时有可能
			// 只剩下 `FC2-4939195`（实测发生过），只列前两条的话改名之后就再也认不出了。
			// 这个前缀在番号语境里够特异，误伤面很小。
			//
			// 没有改成「匹配前去掉分隔符」：那会让 `MD-` 命中 `MDX-…` 之类，
			// 误伤面比这几条漏网大得多。宁可多列字面量。
			"FC2PPV", "FC2-PPV", "FC2-", "CARIB", "1PON", "PACO", "10MU", "HEYZO-", "LUXU-",
			"SIRO-", "GANA-", "PEEP-", "DEBZ-",
		},
	},
	{
		Name:       "国内",
		TargetName: "国产AV",
		Includes: []string{
			"MD-", "MDX-", "MDSJ-", "MDSR-", "MDHT-", "MAN-", "XB-", "XJX-",
			"JDSY-", "RAS-", "QQCM-", "AIMD-",
		},
	},
	{
		// 欧美点分型：`<片商>.<日期>`（`TeenFidelity.19.09.17` / `BangBros18.19.09.17`）。
		//
		// **为什么必须有这条 pattern**：这类名字的番号形状本身就认得出片商，
		// 但站名表永远列不全 —— 实测用户库里有 **55 种**点分日期型片商，
		// 「国外」的 includes 只覆盖 12 种。没有这条兜底，剩下的（`BangBros18`、
		// `TeenFidelity`、`Vixen` 改名前的形态…）会掉进「无番号」被塞进
		// 「国产无番号」，那是**错的归类**。
		//
		// 放在「国内」之后、「日本」之前：日式番号（`ABP-123`）与国内站
		// （`MD-0123`）先被更具体的规则吃掉，轮不到这里。
		//
		// 锚定 `^` + 片商名是**纯字母**，这两条把误伤面压到零 ——
		// 实测拿它扫用户库的 62 个「国产无番号」+ 101 个「日本AV」+ 201 个「FC2」
		// 目录，**一个都不命中**；而 86 个「国外AV」目录命中 82 个
		// （剩下 4 个是 Vixen/Wifey 那批，靠 includes 命中）。
		//
		// 不锚定、或允许片商名含数字，都会把 `1080p.x264` 这类误收进来，
		// 而且 `BangBros18`（片商名带数字的真实站）就得靠 includes 兜。
		Name:       "欧美日期型",
		TargetName: "国外AV",
		Pattern:    `^[A-Za-z]+[._-](\d{4}|\d{2})[._-]\d{2}[._-]\d{2}`,
	},
	{
		Name:       "日本",
		TargetName: "日本AV",
		Pattern:    `^[A-Za-z]{2,6}-\d{2,5}`,
	},
	{
		Name: "无番号",
		// 都没匹配到时的兜底目录。
		//
		// 叫「无匹配」而不是「国产无番号」：这条规则捕获的是**规则表没命中**的东西，
		// 而那不等于「国产且没番号」—— 一个没写番号的欧美片也会落到这里，
		// 放进「国产无番号」是错的归类。名字改成中性的，用户要按自己的口径
		// 再分就在规则编辑器里改（target_name 本来就可编辑）。
		TargetName: "无匹配",
		Nocode:     true,
	},
}

// Defaults 返回一份默认规则的深拷贝。
//
// 必须是拷贝：调用方会直接改返回值（设置页的「恢复默认」、规则编辑器），
// 共享同一份切片会让「改了一条规则」污染掉整个进程的默认值。
func Defaults() Rules {
	return Rules{
		JunkChars:     append([]string(nil), fallbackJunkChars...),
		ReplaceRules:  cloneReplaceRules(fallbackReplaceRules),
		ClassifyRules: cloneClassifyRules(fallbackClassifyRules),
	}
}

func cloneReplaceRules(in []ReplaceRule) []ReplaceRule {
	if in == nil {
		return nil
	}
	out := make([]ReplaceRule, len(in))
	copy(out, in)
	return out
}

func cloneClassifyRules(in []ClassifyRule) []ClassifyRule {
	if in == nil {
		return nil
	}
	out := make([]ClassifyRule, len(in))
	for i, r := range in {
		out[i] = r
		out[i].Includes = append([]string(nil), r.Includes...)
		out[i].Excludes = append([]string(nil), r.Excludes...)
	}
	return out
}
