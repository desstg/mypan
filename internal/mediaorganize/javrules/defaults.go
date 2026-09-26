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
// **这份默认表已经偏离 115-auto 的出厂值了**（原来那份在
// `115-auto/multipan/organize_rules.py` 的 FALLBACK_CLASSIFY_RULES，5 条）。
// 偏离逐条有据，都是实测踩出来的，不是顺手优化：
//
//  1. **多了「欧美日期型」这条 pattern**（原表 5 条 → 6 条）。原表只有
//     includes，而站名表永远列不全 —— 实测用户库里有 55 种点分日期型片商，
//     includes 只覆盖 12 种，剩下的会掉进兜底被塞进「国产无番号」，那是错的归类。
//  2. **「素人」的 includes 补了 `FC2-PPV` 与 `FC2-`**（原表只有 `FC2PPV`）。
//     includes 是纯 strings.Contains，`FC2-PPV-1234567` 这种最常见的写法对不上；
//     而 `HasCode` 认 FC2，于是这些名字**既不入 includes 也不入兜底**，
//     整类永远不会被分档。
//  3. **「国外」的 includes 从 27 条补到 69 条**（实测用户库的片商名单）。
//  4. **「日本」的 pattern 放宽成「连字符后可选一个字母」**，与 names.go 的
//     reCodeAlphaNum 同源（`MKD-S03` 这类番号原来整体认不出）。
//  5. **规则名与目标目录名换成中性口径**：国外→欧美、素人→无码和素人、
//     国内→国产、日本→有码、无番号→未匹配。兜底那条的目标尤其不能叫
//     「国产无番号」—— 它捕获的是**规则表没命中**的东西，而那不等于
//     「国产且没番号」，一部没写番号的欧美片落进去就是错的归类。
//
// 目标目录名（target_name）在目标根目录下按名找、找不到就建，所以这套口径
// 决定的是**媒体库里的目录结构**：`欧美 / 无码 / 国产 / 有码 / 未匹配`。
// 用户要按自己的口径分，在设置页的规则编辑器里改（两个名字都可编辑）。
var fallbackClassifyRules = []ClassifyRule{
	{
		Name:       "欧美",
		TargetName: "欧美",
		Includes: []string{
			"BLACKED", "BLACKEDRAW", "BRAZZERS", "BRAZZERSEXTRA", "RKPRIME",
			"MILFY", "DIGITALPLAYGROUND", "TUSHY", "BANGBUS", "DAUGHTERSWAP",
			"CUMFIESTA", "TEENPIES", "MILFSOUP", "PENTHOUSEGOLD", "PRIVATE",
			"SEXMEX", "ASSHOLEFEVER", "ASSPARADE", "BBCPIE", "DEEPER",
			"FREEUSEFANTASY", "ILOVEPOV", "JAPANHDV", "IMADEPORN", "BAEB",
			"CLUBSWEETHEARTS", "THEREALWORKOUT",
			// 下面这批是**实测用户库里存在、但上游名单上没有**的站。点分日期型那条
			// pattern 已经能兜住形状（`BangBros18.19.09.17` 走 pattern 就进欧美），
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
		// 无码与素人**合成一档**：这两类在媒体库里的归置需求是一样的
		// （都不是日式有码片），而分开两档只会让目录变碎。
		Name:       "无码和素人",
		TargetName: "无码",
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
		// 欧美点分型：`<片商>.<日期>`（`TeenFidelity.19.09.17` / `BangBros18.19.09.17`）。
		//
		// **为什么必须有这条 pattern**：这类名字的番号形状本身就认得出片商，
		// 但站名表永远列不全 —— 实测用户库里有 **55 种**点分日期型片商，
		// 「欧美」的 includes 只覆盖 12 种。没有这条兜底，剩下的（`BangBros18`、
		// `TeenFidelity`、`Vixen` 改名前的形态…）会掉进兜底被塞进
		// 「国产无番号」，那是**错的归类**。
		//
		// 放在「国产」之后、「有码」之前：日式番号（`ABP-123`）与国内站
		// （`MD-0123`）先被更具体的规则吃掉，轮不到这里。
		//
		// 锚定 `^` + 片商名是**纯字母**，这两条把误伤面压到零 ——
		// 实测拿它扫用户库的 62 个「未匹配」+ 101 个「有码」+ 201 个「无码」
		// 目录（当时的目录名还是旧口径「国产无番号 / 日本AV / FC2 / 国外AV」），
		// **一个都不命中**；而 86 个「欧美」目录命中 82 个
		// （剩下 4 个是 Vixen/Wifey 那批，靠 includes 命中）。
		//
		// 不锚定、或允许片商名含数字，都会把 `1080p.x264` 这类误收进来，
		// 而且 `BangBros18`（片商名带数字的真实站）就得靠 includes 兜。
		Name:       "欧美日期型",
		TargetName: "欧美",
		Pattern:    `^[A-Za-z]+[._-](\d{4}|\d{2})[._-]\d{2}[._-]\d{2}`,
	},
	{
		// 国内站番号的**无连字符**形态（`MD0292` / `MDX0020` / `JDSY008`）。
		//
		// 为什么单列一条 pattern 而不是塞进下面那条的 includes：includes 是
		// **纯 strings.Contains，不做分隔符归一化**（照搬 115-auto 的行为），
		// 而下面那 12 条前缀**全部带尾连字符** —— `MD0292` 里没有 `MD-`，
		// 一个都不命中。实测用户库里 46 个国内番号栽在这上面（`MD` 22 个、
		// `MDSR` 9 个、`MDX` 7 个、`JDSY` 6 个…），整理后全落进兜底分类。
		//
		// 前缀后面允许一个可选分隔符（`[-_]?`），所以 `MD-0123` 与 `MD0292`
		// 两种形态都吃 —— 但**下面那条 includes 必须保留**：它的子串匹配
		// 顺手覆盖了别的形态，实测拿掉它会让 88 个已在「国产」的番号改判
		// （`AMD-315` / `CEMD-163` / `SMD-115` 这类**日本**番号也是靠子串
		// 命中进来的 —— 那是既有行为，与 115-auto 一致，本次不动）。
		//
		// 左边要求「开头或非字母数字」：`【麻】MD0292胁迫调教…` 这种带标题
		// 前缀的名字也要认得出 —— 而 `MD0292` 这类番号 `HasCode` 为假，
		// 改名不会动它，目录名就是整条标题，分类只能吃原名。
		//
		// 不放宽 HasCode：那会让 289 个「字母直接接数字」的名字都算番号
		// （`n0417` / `crazyasia00414` 那种），代价与收益不相称。
		Name:       "国产·无连字符",
		TargetName: "国产",
		Pattern: `(?:^|[^A-Za-z0-9])(?:` +
			// 上游那 12 条 includes 的前缀
			`MD|MDX|MDSJ|MDSR|MDHT|MAN|XB|XJX|JDSY|RAS|QQCM|AIMD|` +
			// 下面这批是**实测用户库「国产AV」目录里出现、而上面那 12 条没覆盖**的
			// 厂牌（麻豆的 MDCM/MDAG/MDWP、大象的 DA、以及 MGL/MSD/SZL/BLX…）。
			// 逐个拿全库 8166 个番号验过：按这条 pattern 的形状匹配，
			// **一个都没抢走**现在落在「有码/无码/欧美」的番号 —— `MM` 不会命中
			// `MMB-045`、`NI` 不会命中 `NIMA-011`、`DA` 不会命中 `DAJ-017`，
			// 因为前缀后面必须**直接**跟分隔符或数字。
			`MDCM|MDAG|MDWP|MDHG|MDHS|MAD|MGL|MSD|SZL|BLX|BLXC|MCY|NHAV|` +
			`EMTC|EMX|MFK|MPG|MNSC|WMM|MTVQ|MM|NI|PME|FX|GX|PH|TZ|DA|` +
			`MDCN|MDL|PMC|MB|PMS|GDCM|PM|CZ|MHG|MT|PC|PMA|AAP|AAVV|CP` +
			`)[-_]?\d`,
	},
	{
		Name:       "国产",
		TargetName: "国产",
		Includes: []string{
			"MD-", "MDX-", "MDSJ-", "MDSR-", "MDHT-", "MAN-", "XB-", "XJX-",
			"JDSY-", "RAS-", "QQCM-", "AIMD-",
		},
	},
	{
		Name:       "有码",
		TargetName: "有码",
		// 与 names.go 的 reCodeAlphaNum **同源**：那边放宽了「连字符后可选一个字母」
		// （`MKD-S03`），这里必须跟着放，否则会出现最难查的那种不一致 ——
		// `HasCode` 说「这是番号」（于是改名、认侧车都按番号走），
		// 分类却一个 pattern 都命中不了，最后落进兜底的「未匹配」。
		// 两处一起改才不会分家；改任一处时记得对一下。
		Pattern: `^[A-Za-z]{2,6}-[A-Za-z]?\d{2,5}`,
	},
	{
		// 都没匹配到时的兜底目录。
		//
		// 叫「未匹配」而不是「国产无番号」：这条规则捕获的是**规则表没命中**的东西，
		// 而那不等于「国产且没番号」—— 一个没写番号的欧美片也会落到这里，
		// 放进「国产无番号」是错的归类。名字用中性的，用户要按自己的口径
		// 再分就在规则编辑器里改（target_name 本来就可编辑）。
		Name:       "未匹配",
		TargetName: "未匹配",
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
