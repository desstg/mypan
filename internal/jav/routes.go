package jav

import (
	"strings"

	"litepan/internal/mediaorganize/javrules"
)

// 本文件是「番号刮削按类型路由」的那一层。
//
// # 它解决什么问题
//
// 补缺链（synopsis.Enrich）原来是一条**所有片子共用**的硬编码站点链
// （missav → jav321 → caribbeancom → javbus，见 service.go 的 enrichers）。
// 而番号是分档的，每档的好站在别处：FC2 只有 FC2 站有，欧美只有欧美站有。
// 真机数据：FC2 那 1426 部在这条链上**必然全空**（missav 的 slug 对
// `fc2ppv-*` 直接 404，其余几家也没有 FC2 数据）。
//
// 所以照 mdc-ng 的「类型路由默认值」表把刮削改成**按档位走不同的有序站点链**。
//
// # ⚠️⚠️ 这里的顺序是「补缺优先级」，不是「主源优先级」
//
// **JAVDB 永远是唯一的主源**：封面 / 标题 / 番号 / 评分 / `has_cnsub` 全部挂在
// 它那一份上，订阅排序（quality.ResourceScore = 清晰度/破解/体积）、洗版判定、
// 评分口径（javdbScoreMax，5 分制）也都依赖它。
//
// 补缺链的语义是「**只在当前为空的项上填一个值**」（synopsis.Enrich 按 Missing
// 逐项判），所以就算把 dmm 排在有码第一位，它**也不可能**抢走 JAVDB 的标题或封面
// —— 它只能补 JAVDB 没给的那些空位。
//
// 把 mdc-ng 表里排第一的站点当成「新主源」（= 让它参与入库、参与评分）是**错的**，
// 会同时踩三件事：
//  1. 评分从 5 分制变成那家的口径，整库排序偏（见 sidecar_fields.go 的 javdbScoreMax）；
//  2. 封面与 javdb_url 的生成分家（那些由站基址设置项拼出来）；
//  3. `jav_movies.id` 是 JAVDB 的 id，换主源等于换主键。
//
// 要改这张表的顺序，理由必须是「哪家补得更准/更快」，**不是「谁更权威」**。
// 另外：mdc-ng 表里的 `javdb` 因我们是主源而**从补缺链里去掉**（它已经给过了）；
// 表里其余站点的**相对顺序尊重 mdc-ng**，我们把自家久经实测的源接在它们后面。

// JavRoute 是一个「刮削档位」。
//
// 与 javrules 的归档分类**不是一回事**：那份规则表决定的是**媒体库目录结构**
// （`欧美/无码/国产/有码/未匹配`），加一档就会让「整理」把片子搬进新目录。
// 这里是**刮削**问题 —— 只决定「去哪几个站补元数据」，不动任何文件。
// 所以两边各判各的，别把这里的档位往 javrules 的规则表里塞。
type JavRoute string

const (
	RouteCensored   JavRoute = "censored"   // 有码
	RouteUncensored JavRoute = "uncensored" // 无码
	RouteFC2        JavRoute = "fc2"        // FC2
	RouteChinese    JavRoute = "chinese"    // 国产
	RouteAmateur    JavRoute = "amateur"    // 素人
	RouteWestern    JavRoute = "western"    // 欧美
	RouteAnime      JavRoute = "anime"      // 动漫（**当前无判定入口**，见 RouteOf）
	// RouteDefault 是「判不出档位」时的兜底。
	//
	// 它的站点链**与加路由之前逐字相同**，所以老调用方（后台回写、hydrate）
	// 不传档位时行为不变 —— 这是这次改造的回归锚。
	RouteDefault JavRoute = "default"
)

// RouteInput 是路由判定需要的全部线索。
//
// 用结构体而不是一长串位置参数：这些线索来自三个不同的地方（番号来自侧车/nfo、
// 目录名来自海报墙扫盘、type 来自 JAVDB），位置参数到了调用点没人看得懂哪格是什么。
type RouteInput struct {
	// Number 是**裸番号**（不带质量后缀）。这是最可信的一条线索 ——
	// 番号形状本身就定死了一批类型（`FC2-*` / `091926-001` / `Tushy.2026.09.20`）。
	Number string
	// Station 是站名线索（`javrules.MatchStation` 的产出，如 `CARIB`），可为空。
	//
	// 为什么需要它：日期序号型番号（`091926-001`）**本身不带任何站名信息**，
	// 只有靠改名时补上的站名后缀才认得出它属于哪个素人站（见 javrules/station.go）。
	Station string
	// Category 是海报墙的一级目录名，可为空。
	//
	// ⚠️ **只当线索，不当依据**：它是**用户可改**的自由文本（规则编辑器里
	// `TargetName` 随时能改），拿它当唯一依据的话，用户把「有码」改名成「日本」
	// 就会让整条路由静默降级 —— 而「填了没用也不报错」正是这个项目反复在修的病。
	// 它的正确用途只有一个：形状与 type 都给不出答案时的**提示**。
	Category string
	// MovieType 是 JAVDB 的 type（`0` 有码 / `1` 无码 / `2` 欧美 / `3` FC2），可为空。
	//
	// ⚠️ **它排在番号形状之后**，因为它在「新片」这一场景下不可信：
	// `upsertSummaries` 的插入分支传的 `raw_json` 是空串，而 `type` 列的 SQL 是
	// `CASE WHEN excluded.raw_json <> '' THEN excluded.type ELSE jav_movies.type END`
	// —— 也就是**只有抓过详情的片子 type 才会被更新**。新片第一次被点开时，
	// type 可能是兜底值 `'0'`，照它路由会把无码片送进有码链。
	// 形状优先正好躲开这个坑。
	MovieType string
}

// RouteOf 判出这一部该走哪个档位。
//
// 判据顺序即优先级，**形状优先、type 次之、目录名只作提示**（理由见 RouteInput
// 各字段的说明）。每一步都只回答「是/不是」，先命中的赢。
//
// ⚠️ **动漫（RouteAnime）当前没有判定入口**：默认规则表里没有动漫这一档，
// 番号形状也认不出它（实测：纯番号喂 `javrules.ClassifyName` 判不出动漫）。
// 所以 RouteAnime **是一条死档** —— 表里留着它是为了与 mdc-ng 那张默认表对齐
// （可审计），以及等以后有了「动漫番号形状 / 站名线索」时接进来。
// 这是有意的：留一个没人命中的档位，好过删掉之后下一个人不知道它曾经存在。
func RouteOf(in RouteInput) JavRoute {
	number := strings.TrimSpace(in.Number)

	// ① FC2：番号前缀就写着。这是最硬的判据，连带质量后缀的形态都认
	//    （`FC2-PPV-4983582` / `FC2-4939195`）。
	//    与 javdb.MovieTypeOf 的兜底**同一条判据**（那边也是「type 为空则按 FC2 前缀」）。
	if isFC2Number(number) {
		return RouteFC2
	}
	// ② 欧美点分日期型（`Tushy.2026.09.20` / `RKPrime.26.09.21`）。
	//    形状自带片商，与磁盘怎么摆无关。走 javrules 的现成判据，不另写正则。
	if javrules.IsWesternCode(number) {
		return RouteWestern
	}
	// ③ type 兜底（形状给不出答案时才看它）。FC2 与欧美这两档在形状上已经判过，
	//    这里再判一次是为了兜住「形状认不出、但 type 明确」的形态。
	if t := strings.TrimSpace(in.MovieType); t != "" {
		switch t {
		case "3":
			return RouteFC2
		case "2":
			return RouteWestern
		}
	}
	// ④ 素人：日期序号型 + 站名线索（`091926-001` + `CARIB`）。
	//    日期序号型本身不带站名，只有靠后缀才认得出 —— 而它恰恰是
	//    「什么规则都命中不了」的那一类（见 javrules/station.go 的说明）。
	if station := strings.ToUpper(strings.TrimSpace(in.Station)); station != "" && isAmateurStation(station) {
		return RouteAmateur
	}
	// ⑤ 国产：「大写字母直接接数字」的形状（`XKG147` / `MD0309` / `MGL0002`）。
	//
	//    ⚠️⚠️ **必须排在 type 之前**（2026-10-15 真机实测修的一处错判）。
	//
	//    起初这一步排在「type==1 → 无码」之后，理由是「type 是上游给的事实」。
	//    拿真库一跑就露馅了：**JAVDB 把国产片大面积标成 type=1（无码）** ——
	//    16811 部里有 1120 部是国产形状，其中 **1115 部 type=1**、只有 5 部 type 为空。
	//    于是那 1115 部麻豆系（`MD0309` / `MDSR0005-1` / `MGL0002` / `BLX0002`…）
	//    **全被送进了无码链**，而国产链上那三家（madou / xiao_huang_shu / freejavbt）
	//    一部都没轮到。表现是「国产片永远补不到国产站的料」，且不报错。
	//
	//    形状优先在这里是安全的：`[A-Z]{2,8}\d{2,6}` 这个形状在日式番号里**不存在**
	//    （日式片商名后面必须跟 `-`，见 javrules/defaults.go 那条「国产·形状」的实测依据：
	//    拿用户库 15007 个番号验过，救回 355 个、**抢走 0 个**）。
	if javrules.LooksLikeCNNumber(number) {
		return RouteChinese
	}
	// ⑥ 无码：type 说是，或者「日期序号型但没站名线索」（那多半是无码站的片子）。
	if strings.TrimSpace(in.MovieType) == "1" {
		return RouteUncensored
	}
	if javrules.IsDateSeqCode(number) {
		return RouteUncensored
	}
	// ⑦ 有码：走到这一步还能被认作**番号**的，就只剩日式有码了 ——
	//    FC2（①）、欧美点分（②）、日期序号型（⑤）、国产形状（⑥）都已经分流过，
	//    而 `HasCode` 认的另外两条（FC2 / 日期序号）也在这里面。
	//
	//    ⚠️ 这一条**不能要求 `MovieType == "0"`**：在线刮削那侧拿不到 type
	//    （见 RouteInput.MovieType 的说明），传进来的永远是空串。若只认 type，
	//    `SSIS-001` 这种最标准的日式番号会掉进兜底链 —— 实测就是这么暴露的。
	if javrules.HasCode(number) {
		return RouteCensored
	}
	if strings.TrimSpace(in.MovieType) == "0" {
		return RouteCensored
	}
	// ⑧ 目录名提示（**最后一道**）：形状与 type 都没说话时，看看海报墙那个一级
	//    目录叫什么。用户改过名就命中不了 —— 那是可接受的，因为它本来就只是提示，
	//    而「命中不了」退到默认链，与加路由之前的行为一致。
	if r, ok := categoryRoutes[strings.TrimSpace(in.Category)]; ok {
		return r
	}
	// ⑨ 兜底：默认链（与加路由之前逐字相同）。
	return RouteDefault
}

// isFC2Number 报告番号是不是 FC2 系。
//
// 判据是**前缀 FC2**（`FC2-4939195` / `FC2-PPV-4983582` / `FC2PPV1234567` 三种
// 写法都认）。不复用 javrules.HasCode：那个是「名字里含不含番号」的宽判据
// （`reCodeFC2` 是 `(?i)FC2`，只要名字里出现 `FC2` 就算真），拿它判「是不是 FC2 档」
// 会把 `XXX-123 FC2版` 这种也吞进来。这里要的是「番号**以** FC2 开头」。
func isFC2Number(number string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(number)), "FC2")
}

// isAmateurStation 报告站名是不是「素人 / 无码站」。
//
// 名单来自 javrules 默认规则表里「无码和素人」那条 includes —— 但**只取素人站**，
// 把 FC2 那几条（`FC2PPV` / `FC2-PPV` / `FC2-`）排除掉：FC2 有它自己的一档，
// 在 ① 就判走了。
//
// ⚠️ javrules.StationTokens **取不到这几个**：`isStationShape` 会把带尾连字符的
// token（`HEYZO-` / `SIRO-` / `LUXU-` / `GANA-` / `PEEP-` / `DEBZ-`）过滤掉
// （它只收纯字母数字）。所以素人这一档的 token 名单在这里**自己维护一份**，
// 由 AmateurStationTokens 暴露给扫盘那侧拼进 MatchStation 的 token 表。
func isAmateurStation(token string) bool {
	for _, t := range amateurStationTokens {
		if token == t {
			return true
		}
	}
	return false
}

// amateurStationTokens 是素人站的站名 token（大写）。
//
// 与 javrules 默认规则表「无码和素人」那条 includes 同源，但去掉 FC2 系
// （FC2 单独一档），并补上 MatchStation 能用到的形态（不带尾连字符）。
var amateurStationTokens = []string{
	"CARIB", "1PON", "PACO", "10MU", "HEYZO", "LUXU", "SIRO", "GANA", "PEEP", "DEBZ",
}

// AmateurStationTokens 返回素人站的站名 token（给扫盘那侧拼 MatchStation 用）。
//
// 为什么需要它：`javrules.StationTokens(rules)` 返回的是**规则表 includes 里**的
// token，而素人站那几条带尾连字符的（`HEYZO-`）会被 `isStationShape` 过滤掉 ——
// 少这一份的话，`MatchStation` 手上**根本没有 `HEYZO` 这个词**，
// `HEYZO-3950` / `200GANA-2931` 这类番号就认不出站名（判据本身认得出，
// 见 javrules.MatchStation 的判据 C，但 token 表里没有它也是白搭）。
//
// ⚠️ 别把这句话读成「MatchStation 认不出紧贴番号前缀的 token」—— 那是 2026-10-16
// 之前的旧行为，已经改了；这里缺的只是**词表**。
func AmateurStationTokens() []string {
	out := make([]string, len(amateurStationTokens))
	copy(out, amateurStationTokens)
	return out
}

// isChineseNumber 报告番号是不是国产。
//
// 两条判据，任一命中即可（与 javrules 默认规则表的「国产」三条规则同源）：
//   - 用户填的厂牌表命中（`CNBrandsFromRules` 的产出）；
//   - 「大写字母直接接数字」的形状（`XKG147` / `RS034`）—— 日式番号的片商名后面
//     **必须跟连字符**（`ABP-123`），所以这个形状本身就够判（见 javrules/defaults.go
//     那条「国产·形状」规则的实测依据）。
func isChineseNumber(number string, brands []string) bool {
	number = strings.TrimSpace(number)
	if number == "" {
		return false
	}
	// 形状那条：与 javrules 的 cnShape 同一个形状（`^[A-Z]{2,8}\d{2,6}(-\d+)*$`）。
	// 走它而不是自己写正则 —— javrules 已经把「拿用户库 15007 个番号验过、
	// 抢走 0 个」这件事做完了。
	if javrules.LooksLikeCNNumber(number) {
		return true
	}
	// 厂牌表那条：`MD-0292` 这种带连字符的厂牌番号形状认不出，得靠厂牌。
	// 复用 HasCodeWithBrands 的国产分支（它内部就是 cnBrandRegexesFor(brands).solid）。
	if len(brands) > 0 && javrules.HasCodeWithBrands(number, brands) {
		// HasCodeWithBrands 是个宽判据（日式番号也返回真），所以要**再确认一次**
		// 它命中的是不是国产那条分支：去掉厂牌再判一次，若那时为假，说明命中的
		// 正是厂牌分支。
		if !javrules.HasCodeWithBrands(number, nil) {
			return true
		}
	}
	return false
}

// routeSiteTable 是「档位 → 有序站点链」。
//
// # 这份表的出处（2026-10-15，**不是照图 7 抄的**）
//
// 图 7 那张「类型路由默认值」是 mdc-ng 的**旧文档**（7 档、每档 3~4 家）。
// 实际运行实例（v1.36.0，`http://192.168.31.4:9208/providers` 的「优先级设置(全局)」）
// 是**6 档、每档 4~13 家，而且没有「动漫」档**。下面按运行实例来 ——
// 并已被反编译核实（v1.36.0 二进制里只有 6 个
// `default_provider_priority_{jav_censored,jav_uncensored,jav_amateur,jav_fc2,cn,ea}`，
// **没有 janime**，所以图 7 那条「动漫 getchu→dmm→javdb」在 v1.36.0 已不存在）。
//
// # 怎么从 mdc-ng 的表翻译过来的
//
//  1. mdc-ng 的表**含它自己的所有源**（有码一档列了 13 家）；我们只有其中 5 家，
//     本次再加 5 家。所以这里是**过滤后的投影**，被过滤掉的那些（Avbase / Avmoo /
//     Hbox_jp / Javlibrary / Mgstage / Mmtv / Madouqu / Madou / Xiao_huang_shu /
//     Fc2_hub / 7mmtv）在 mdc-ng 表里的**相对位置**保留在注释里，以后加站时按位插入。
//  2. `Javdb` 从 mdc-ng 的每一条里**去掉**了 —— 它是我们的**主源**，已经给过数据
//     （见文件头那段警告：主源不参与补缺）。
//  3. mdc-ng 表里 **Miss_av / Freejavbt / Airav_io 几乎出现在每一条的尾部** ——
//     那是它的「广覆盖兜底三家」。我们照抄这个位置（放尾部）。
//
// # 顺序即补缺优先级
//
// 「首个非空胜」由 `synopsis.Enrich` 实现（按 `Missing` 逐项判），
// 与 mdc-ng 那句「最终结果根据优先级进行选取」是同一个语义。
var routeSiteTable = map[JavRoute][]string{
	// 有码。mdc-ng: Dmm, Mgstage, Javlibrary, Avbase, Hbox_jp, [Javdb], Javbus,
	// Jav321, Avmoo, Mmtv, Airav_io, Freejavbt, Miss_av
	RouteCensored: {"dmm", "javbus", "jav321", "airav", "freejavbt", "missav"},
	// 无码。mdc-ng: Carib, Avbase, Javbus, [Javdb], Avsox, Mmtv, Airav_io, Freejavbt, Miss_av
	// ⚠️ Carib（caribbeancom）在 mdc-ng 里排**第一**，但本项目的注释记着它只认
	// 日期序号型（`^\d{6}-\d{2,4}$`），`SIRO-` / `HEYZO-` 这类字母番号的无码它一个都没有
	// —— 所以本项目的实测把 javbus 放前面更稳（javbus 有这些字母番号）。
	RouteUncensored: {"javbus", "avsox", "caribbeancom", "airav", "freejavbt", "missav"},
	// FC2。mdc-ng: Fc2, Fc2_hub, [Javdb], Avsox, Mmtv, Airav_io, Freejavbt, Miss_av
	RouteFC2: {"fc2", "avsox", "airav", "freejavbt", "missav"},
	// 素人。mdc-ng: Mgstage, Carib, Javlibrary, Avsox, Avmoo, Javbus, [Javdb],
	// Jav321, Mmtv, Airav_io, Freejavbt, Miss_av
	RouteAmateur: {"caribbeancom", "avsox", "javbus", "jav321", "airav", "freejavbt", "missav"},
	// 国产。mdc-ng: Madouqu, Madou, Xiao_huang_shu, Mmtv —— 已实现 madouqu。
	//
	// ⚠️ 这一档**没有权威源**（不像有码有 javdb）：真机实测那 1140 部国产片里
	// 99% 缺演员、99% 缺简介、100% 缺导演 —— **必须靠几家拼**。这正是 mdc-ng
	// 要搞字段级优先级的原因，也是它国产链清一色全是国产站（别的档都有个权威源压阵）。
	// madou 与 xiao_huang_shu 待实现；airav / freejavbt / missav 是通用兜底。
	// ⚠️ madouqu 只填 中文片名 / 演员 / 封面 / 发行日期，**绝不填简介**（那是站点样板话）。
	RouteChinese: {"madouqu", "airav", "freejavbt", "missav"},
	// 欧美。mdc-ng: ThePornDB（**只有一家**）。
	// 它要 API key，没有 key 时只剩兜底三家。
	RouteWestern: {"theporndb", "freejavbt", "missav"},
	// 动漫：**mdc-ng v1.36.0 没有这一档**（图 7 那份旧文档有，但运行实例与二进制都没有）。
	// 保留它是因为图 7 明确列过，且以后可能加回来；但**当前没有任何输入能路由到它**
	// （见 RouteOf 的说明），所以链是空的也无所谓 —— 这里仍然给一条兜底，
	// 免得万一有人手填 RouteAnime 时拿到空链（空链 = 静默不补）。
	RouteAnime: {"dmm", "missav"},
	// 兜底：**与加路由之前逐字相同**的那条链（service.go 的 enrichers 原样）。
	// 这是回归锚 —— 判不出档位时行为必须与改造前一致。
	RouteDefault: {"missav", "jav321", "caribbeancom", "javbus"},
}

// RouteSites 返回某个档位的站点链（**未过滤**是否已实现）。
//
// 过滤交给调用方：只有 service.go 的 siteConstructors 知道哪些站真的存在，
// 而把这个知识复制到这里会让两处迟早分家（表现是「表里有、实际跳过」却不报错）。
func RouteSites(r JavRoute) []string {
	sites, ok := routeSiteTable[r]
	if !ok {
		return nil
	}
	out := make([]string, len(sites))
	copy(out, sites)
	return out
}

// AllRoutes 返回所有档位（顺序稳定，便于测试与遍历）。
func AllRoutes() []JavRoute {
	return []JavRoute{
		RouteCensored, RouteUncensored, RouteFC2, RouteChinese,
		RouteAmateur, RouteWestern, RouteAnime, RouteDefault,
	}
}

// categoryRoutes 是「海报墙一级目录名 → 档位」的**默认**映射（RouteOf 的第 ⑧ 步）。
//
// ⚠️ 这些字面量是 javrules 默认规则表里的 `TargetName`，而**用户随时能改**
// （规则编辑器里改「目标目录名」）。所以它只配当最后一道提示：
// 用户改了名 → 这里命中不了 → 退到默认链，与加路由之前一致，不会走错大类。
//
// 为什么不在这里做「名字无关」的映射：那要读用户的规则表（I/O + settings 依赖），
// 而 RouteOf 是纯函数。想要那条的话，由调用方把**用户当前的 TargetName** 翻译成
// 这里能认的名字再传进来（`javrules.ClassifyRules[i].Name` 是规则自己的名字，
// 默认表里它与 TargetName 同源）。
var categoryRoutes = map[string]JavRoute{
	"有码":  RouteCensored,
	"无码":  RouteUncensored,
	"欧美":  RouteWestern,
	"国产":  RouteChinese,
	"未匹配": RouteDefault, // 兜底目录：判不出，走默认链
}
