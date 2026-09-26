package javplanner

import (
	"sort"
	"strings"

	"litepan/internal/jav/quality"
	"litepan/internal/mediaorganize/javrules"
)

// 侧车（`<番号>[-U|-C|-UC][-4K].json`）的识别，以及「标准番号命名」。
//
// 侧车由 internal/jav/sidecar.go 在离线下载完成时写进资源所在的那一层目录。
// 本包**只读它的文件名，不读它的内容**：
//
//   - 番号与三个质量标记全都写在名字里（`SSIS-444-UC-4K.json`），而 List 已经
//     把名字带回来了 —— 一次计划几十个作品，逐个把 json 从网盘读下来是几十次
//     「解析直链 + 下载」，全压在生成预览这一步上；
//   - 每次网络读取都是一个会失败的环节，读不到就整目录回落到旧命名，而番号
//     明明就写在文件名上。
//
// 名字由 quality.BuildJavFileName 拼、这里用 quality.ParseJavFileName 拆 ——
// 同一份实现的两个方向。两边各写一份的话，将来一边改了（加个 -HD 之类），
// 另一边不认，就成了「文件明明在那儿但认不出来」的静默失效。
//
// 为什么要侧车而不是从视频名猜：视频名里往往什么都没有（实测那颗种子的视频
// 叫 `manko.fun.mp4`），而侧车里的番号与标记是推送时就判定好的、与界面上那颗
// 磁链的角标同源。硬猜只能得到 `MANKO.FUN` 这种东西。

// sidecarExt 是侧车的扩展名（小写、不含点）。
const sidecarExt = "json"

// sidecarEntry 是一个目录里认出来的那份侧车。
type sidecarEntry struct {
	FileID string // 侧车文件自己的 ID（移入时要带上它）
	Name   string // 侧车文件名，形如 <番号>-UC-4K.json（日期序号型会带上站名）
	Number string
	Marks  quality.Marks

	// Station 是从**同目录的视频名、目录名或侧车名自己**里捞出来的站名
	// （CARIB / 1PON / …），只有日期序号型番号才可能非空。见 resolveStations。
	//
	// 为什么要有它：`091926-001` 这种番号本身不带站名，而它是唯一没有 pattern
	// 兜底的番号形态 —— 字母番号改名后仍命中「有码」那条 `^[A-Za-z]{2,6}-[A-Za-z]?\d{2,5}`，
	// 日期序号型则什么规则都命中不了，会**静默**留在原地（不报错、Skipped 里也没有）。
	// 补上站名（`091926-001-CARIB`）之后，「素人」规则的 includes 就命中得了。
	Station string

	// brands 是**用户填的国产厂牌**（分类规则里来的，见 javrules.CNBrandsFromRules）。
	//
	// 与 Station 一样**固化在 entry 上**，而不是每个出口现算一遍：三个出口
	// （视频名 / 目录名 / 侧车自己的去处）必须给出同一个答案，各算各的迟早走岔。
	// 它是「无连字符的国产番号要不要补成带连字符」的依据。
	brands []string
}

// WorkNumber 是「视频名、作品目录名与侧车名该用的番号」。
//
// 日期序号型且捞到站名时拼成 `091926-001-CARIB`，否则就是 Number 本身
// （字母番号、以及没带站名的日期序号型 —— 后者保持既有行为不变）。
//
// **国内厂牌的番号在这里补上连字符**（`MGL0002` → `MGL-0002`）。
//
// 为什么放在这一层而不是 `quality.BuildJavFileName`：后者是**写侧车与整理共用**
// 的出口，而「哪些前缀算国内厂牌」是整理侧的业务知识（那份厂牌表就住在
// `javrules`，是为了分类规则而存在的），塞进纯命名包不合它的定位。
//
// 放在这一层的代价：写侧车时（`internal/jav/sidecar.go`）名字仍是 `MGL0002.json`
// 而整理会把它改成 `MGL-0002.json` —— 多产生一条改名动作才收敛。这是可接受的：
// 那条动作幂等（第二轮 `WorkNumber()` 已是带连字符的形态，算出来同名、不产生动作），
// 而反过来把它放进 `quality` 会让那个包开始认识业务规则。
//
// **所有出口都必须读它**，不能各读各的：视频名、作品目录名、侧车自己的去处
// 三处给出不同答案，就会出现「按 A 建目录、按 B 把目录改名」这种自相矛盾的计划
// （stages.go 的 dirNameFor 注释就是为这件事写的），更糟的是侧车会与它的视频分家。
//
// 侧车名也跟着它走（用户要求 `091926-001.json` 与视频一起变成
// `091926-001-CARIB.json`）：名字是「这一层有哪些作品」的索引，视频改了名而
// json 没改，下一轮就得靠猜。质量标记（-U/-C/-UC/-4K）仍由 Marks 单独缀在后面。
func (e sidecarEntry) WorkNumber() string {
	if e.Station == "" {
		// 国内厂牌补连字符；其余番号（日式字母番号、FC2、日期序号型）原样返回 ——
		// `NormalizeCNHyphenWithBrands` 只认那份厂牌表（代码里的 + 用户填的），
		// 不匹配就原样给回来。
		number, _ := javrules.NormalizeCNHyphenWithBrands(e.Number, e.brands)
		return number
	}
	return e.Number + "-" + e.Station
}

// sidecarWorkName 是侧车文件「本次计划跑完后」该叫什么。
//
// 与视频名同一套拼法（quality.BuildJavFileName），只是 ext 固定 `.json` 且不带
// cd 编号 —— 一份侧车对应一颗资源，分片是视频层面的事。
// 与原名的比较交给调用方：名字一样时不该产生动作（幂等）。
func sidecarWorkName(entry sidecarEntry) string {
	return quality.BuildJavFileName(entry.WorkNumber(), entry.Marks, 0, ".json")
}

// sidecarIndex 是「目录 ID → 该层认出的侧车」的索引。
//
// **一个目录可以有多份**：手动推送不建子目录（SubfolderMode: none），所以
// 「下载目录」天然会平铺积累多部片 —— 一个目录里两份 json、两个视频是常态，
// 不是异常。早先按「一目录一份、取第一份」写是错的：那会把两部片串成一部
// （两个视频都用第一份的番号命名，还被编成 -cd1/-cd2，看着像是有意的）。
//
// 带上 dirs 保序：动作 ID 是按处理顺序递增的（nextID），靠 map 迭代会让
// 同一棵树两次生成出不同的动作编号 —— 预览与实际执行、以及测试都没法比对。
type sidecarIndex struct {
	byDir map[string][]sidecarEntry
	dirs  []string
}

// all 返回该目录认出的全部侧车（按扫描顺序）。
func (ix sidecarIndex) all(dirID string) []sidecarEntry { return ix.byDir[dirID] }

// sole 在该目录**恰好只有一份**侧车时返回它。
//
// 这一条是种子目录的常态：`MOIL-001/` 里就一份 json，于是它管这个目录里的
// 所有视频 —— 也正因为有它，`manko.fun.mp4` 这种名字里没有番号的视频才认得出来。
func (ix sidecarIndex) sole(dirID string) (sidecarEntry, bool) {
	list := ix.byDir[dirID]
	if len(list) != 1 {
		return sidecarEntry{}, false
	}
	return list[0], true
}

// isContainer 报告该目录是不是「容器」：认得出两份以上侧车。
//
// 容器目录**不能整体改名**，也不能把所有视频塞进同一个番号目录 —— 它装的
// 是好几部作品，每部要各自建自己的 `<番号>/`。
func (ix sidecarIndex) isContainer(dirID string) bool { return len(ix.byDir[dirID]) > 1 }

// loadSidecars 扫出这一棵树里所有认得出的侧车，按所在目录归位。
//
// 按**目录**归而不是按文件名跟视频配对：侧车与它描述的视频必然同层（写侧车时
// 就是这么落点的 —— 见 internal/jav/sidecar.go 的定层逻辑），而 ScannedFile 只有
// ID/Name/ParentID/Size、没有路径，配对只能靠 ParentID。
//
// 认不出的 json **不是错误**，只是「这份 json 与我们无关」—— 网盘上还有各种
// 顺手带上的 json，一个个报错只会把日志淹掉。
func (b *builder) loadSidecars() sidecarIndex {
	ix := sidecarIndex{byDir: map[string][]sidecarEntry{}}
	// 同一目录、同一番号的重复侧车只留第一份（重推换了质量时新旧并排躺着的那种）。
	seen := map[string]map[string]struct{}{}
	for _, f := range b.tree.Files {
		if extOf(f.Name) != sidecarExt {
			continue
		}
		number, marks, ok := parseSidecarName(f.Name, b.cnBrands)
		if !ok {
			continue
		}
		key := strings.ToUpper(number)
		if seen[f.ParentID] == nil {
			seen[f.ParentID] = map[string]struct{}{}
		}
		if _, dup := seen[f.ParentID][key]; dup {
			// 重推一颗质量不同的磁链时文件名会变，`overwrite` 就不生效，
			// 于是新老两份并排躺着。取先扫到的那个，**不自动删旧的** ——
			// 删文件不可逆，而且旧的那份可能正是用户想要的。
			b.planner.log("[番号匹配] 目录里有同一番号的多份侧车，只用了先扫到的：" + f.Name)
			continue
		}
		seen[f.ParentID][key] = struct{}{}
		if len(ix.byDir[f.ParentID]) == 0 {
			ix.dirs = append(ix.dirs, f.ParentID)
		}
		ix.byDir[f.ParentID] = append(ix.byDir[f.ParentID], sidecarEntry{
			FileID: f.ID, Name: f.Name, Number: number, Marks: marks,
			brands: b.cnBrands,
		})
	}
	if len(ix.dirs) > 0 {
		b.plan.Diagnostics["sidecars"] = len(ix.dirs)
	}
	return ix
}

// resolveStations 给日期序号型的侧车补上站名（就地写回 sidecarIndex）。
//
// 站名只可能从**文件名**里捞 —— 本包不读侧车内容（见文件头注释），而 JAVDB 的
// `number` 字段本来就不含站名（`carib` 是 `maker_name`「カリビアンコム」）。
// 实测：用户库 88 个日期序号型目录**全部**带站名后缀，且只有四个
// —— CARIB / 1PON / PACO / 10MU，都在「素人」规则的 includes 里。
//
// 为什么固化在 entry 上、而不是每个出口现算一遍：三个出口（视频名 / 目录名 /
// 侧车自己的去处）必须给出同一个答案，各算各的迟早走岔。而且固化之后
// dirNameFor / folderForVideo / buildName 都只是读一个字段，不必各自持有 tokens。
//
// **幂等性**：站名每次都从**当前**文件名重新捞。整理跑过一轮之后视频已叫
// `091926-001-CARIB.mp4`，名字里仍然带着 CARIB，于是第二轮算出同一个答案、
// 不产生任何动作。若改成「只在第一轮捞、之后记下来」，第二轮就会把它改回去。
func (b *builder) resolveStations() {
	tokens := javrules.StationTokens(b.planner.rules.ClassifyRules)
	if len(tokens) == 0 {
		return
	}
	for _, dirID := range b.sidecars.dirs {
		list := b.sidecars.byDir[dirID]
		for i := range list {
			entry := &list[i]
			// ① 侧车名自己就带着站名（上一轮改过的形态 `091926-001-CARIB.json`）。
			//    最权威，直接拆 —— 而且**必须**拆：不拆的话 Number 会是
			//    `091926-001-CARIB`，容器目录里 ownerFor 拿视频名抽出的
			//    `091926-001` 去比它比不中，整部片会被判成「认不出番号、保持原样」。
			if base, site := javrules.SplitStationSuffix(entry.Number, tokens); site != "" {
				entry.Number, entry.Station = base, site
				continue
			}
			// ② 侧车名是纯番号 → 从同目录的视频名 / 目录名里捞。
			//    字母番号不参与（它自带字母，改名后照样命中「日本」）。
			if !javrules.IsDateSeqCode(entry.Number) {
				continue
			}
			entry.Station = b.stationFor(dirID, *entry, tokens)
		}
	}
}

// stationFor 在一份侧车所属的视频名里找站名；一个都没找到、且该目录只有这一份
// 侧车时，再拿目录名兜一次。
//
// 遍历的是**当前**文件名（阶段 2 改名之前读，第二轮读到的则是上一轮改过的名字，
// 两轮都认得出 —— 见 resolveStations 的幂等性说明）。
func (b *builder) stationFor(dirID string, entry sidecarEntry, tokens []string) string {
	// 一个目录可以有多份侧车（手动推送不建子目录 → 下载目录会平铺积累多部片），
	// 所以要先认归属，别拿别的片的文件名去配这一份。
	for _, f := range b.tree.ChildrenOf(dirID) {
		if b.isSidecarFile(f) || !isVideoExt(f.Name, b.videoExts) {
			continue
		}
		owner, ok := b.ownerFor(dirID, f)
		if !ok || owner.FileID != entry.FileID {
			continue
		}
		if site := javrules.MatchStation(f.Name, entry.Number, tokens); site != "" {
			return site
		}
	}
	// 目录名兜底：实测种子目录会叫 `030323-001-carib`，而目录里的视频名可能
	// 什么都没带（`manko.fun.mp4` 那种）。只在**唯一一份侧车**时才用目录名 ——
	// 容器目录（多份侧车）的目录名不属于任何一部片。
	if _, sole := b.sidecars.sole(dirID); !sole {
		return ""
	}
	if dir := b.tree.FindDir(dirID); dir != nil {
		return javrules.MatchStation(dir.Name, entry.Number, tokens)
	}
	return ""
}

// ownerFor 找出这个视频该按哪份侧车命名，找不到返回 false。
//
// 三条判据，顺序固定：
//
//  1. **该目录只有一份侧车 → 就是它。** 种子目录的常态（一份 json 管一层），
//     也让「视频名里没有番号」的那种仍然认得出来（`manko.fun.mp4`）。
//  2. **多份 → 从视频文件名里抽番号来配。** 发布组的名字再乱也基本带着番号：
//     实测 `www.98T.la@ABF-179@BVPP1XdfBVPP4X(STD)_apo8_iris2_watermusk.mp4`
//     抽出 `ABF-179`（走 javrules.ExtractCode，与设置页试跑、与整理同一个识别）。
//  3. **抽不出番号 → 没有归属。** 目录里有两份以上 json 时，没有任何信息能判定
//     哪份属于它 —— 这不是实现问题，是信息本身不存在。这时**一律不动它**，
//     由调用方记进 Skipped 让用户在预览里看见。宁可漏整理一部，
//     也不要靠猜把两部片串成一部。
func (b *builder) ownerFor(dirID string, f ScannedFile) (sidecarEntry, bool) {
	if entry, ok := b.sidecars.sole(dirID); ok {
		return entry, true
	}
	code := javrules.ExtractCodeWithBrands(f.Name, b.cnBrands)
	if strings.TrimSpace(code) == "" {
		return sidecarEntry{}, false
	}
	for _, entry := range b.sidecars.all(dirID) {
		if sameNumber(entry.Number, code) {
			return entry, true
		}
	}
	return sidecarEntry{}, false
}

// sameNumber 比较两个番号是否指同一部作品。
//
// 先按原样忽略大小写比；不中再比「去掉分隔符与空格」的形式 —— 番号在不同地方
// 的分隔符写法不稳定（`ABF-179` / `ABF179` / `abf_179`），发布组与上游各写各的。
// 只去分隔符、不做别的归一化：再宽松就会把 `ABF-17-9` 当成 `ABF-179` 了。
func sameNumber(a, b string) bool {
	norm := func(s string) string {
		s = strings.ToUpper(strings.TrimSpace(s))
		return strings.NewReplacer("-", "", "_", "", " ", "").Replace(s)
	}
	na, nb := norm(a), norm(b)
	return na != "" && na == nb
}

// parseSidecarName 从文件名认出侧车，返回番号与标记。
//
// 两道闸：
//  1. 必须是 `*.json`，且主名能按标准命名机械地拆开（quality.ParseJavFileName）；
//  2. 拆出来的番号必须**真的像番号**（javrules.HasCode）。
//
// 第二道不能省。机械拆分对 `4K`、`notes`、`config` 这种名字同样会「拆得开」
// （它们确实就是「没有标记的主名」），而网盘上顺带带着的配置 json 满地都是 ——
// 少这道闸，一个 `readme-4K.json` 就会被当成侧车，拿 `readme` 去改视频名、建目录。
//
// 判据复用 javrules.HasCodeWithBrands 而不是在这里另写一套：那是这个项目里唯一
// 权威的番号识别，设置页的试跑预览走的也是它。两套判据迟早分家，而分家的表现是
// 「同一份文件在预览里认得出、整理时不认」。
//
// `brands` 是用户填的国产厂牌：不带上它的话，用户在设置页新加一个国产厂牌之后，
// **分类**按规则命中进了「国产」，而推送写出的 `ZZBRAND0001.json` 却不被当成侧车
// ——视频不改名、json 也没有任何动作搬它，落在源目录里没人管。这就是
// `MDCN` / `MDL` 当初漏掉的那个病。
func parseSidecarName(name string, brands []string) (string, quality.Marks, bool) {
	if extOf(name) != sidecarExt {
		return "", quality.Marks{}, false
	}
	number, marks, ok := quality.ParseJavFileName(stemOf(name))
	if !ok || !javrules.HasCodeWithBrands(number, brands) {
		return "", quality.Marks{}, false
	}
	return number, marks, true
}

// isSidecarFile 报告这个文件是不是「本目录的某一份侧车」。
//
// 用遍历而不是查一份：一个目录可以有多份侧车（容器目录），每一份都要被认出来 ——
// 它们都不改名、都要跟着各自的视频进各自的番号目录。
func (b *builder) isSidecarFile(f ScannedFile) bool {
	for _, e := range b.sidecars.all(f.ParentID) {
		if e.FileID == f.ID {
			return true
		}
	}
	return false
}

// isPartCandidate 报告这个视频要不要参与 cd 编号。
//
// 判据是**体积**：小视频（广告、样片）不是分片。阈值复用「小文件阈值」，
// 但它**与「清理小文件」开关无关** —— 那个开关决定删不删，不决定「算不算分片」。
// 实测那颗 MOIL-001 的种子：正片 358MB + 一个 11MB 的广告 mp4，靠这一条分开。
//
// 阈值 <= 0 时恒为真（全部参与）—— 那是「我没设阈值」，不是「什么都别编号」。
func (b *builder) isPartCandidate(f ScannedFile) bool {
	mb := b.planner.cfg.SmallFileMB
	if mb <= 0 {
		return true
	}
	return f.Size >= int64(mb)*1024*1024
}

// sortBySizeDesc 按体积降序（大的在前），同体积按名字固定 —— 编号顺序必须可复现。
//
// 两边共用同一条规则：写侧车时定不下编号（一份侧车对应一颗资源），但整理时
// 每次都要重新算，所以只要规则确定，重复整理同一棵树得到的名字就一致。
func sortBySizeDesc(files []ScannedFile) {
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].Size != files[j].Size {
			return files[i].Size > files[j].Size
		}
		return files[i].Name < files[j].Name
	})
}

// buildName 按标准番号命名给一个视频算新名字。
//
// 只是把 quality 那套转一下手：ext 从原文件名原样取（**保持大小写**）。
// 番号用 WorkNumber()（日期序号型会带上站名，见那里）。
// 番号为空返回空串，调用方据此回落到既有的清理式命名。
func buildName(entry sidecarEntry, cd int, originalName string) string {
	return quality.BuildJavFileName(entry.WorkNumber(), entry.Marks, cd, extWithDot(originalName))
}

// extWithDot 取带点、**保持原样大小写**的扩展名；没有扩展名时返回空串。
//
// 与 scan.go 的 extOf 是两件事：那个小写、不含点，用于查扩展名集合；
// 这个要原样拼回文件名里。
func extWithDot(name string) string {
	stem := stemOf(name)
	if len(stem) >= len(name) {
		return ""
	}
	return name[len(stem):]
}
