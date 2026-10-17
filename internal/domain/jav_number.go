package domain

import "strings"

// 番号的**书写形态**问题（2026-10-17 真机）。
//
// # 治的是什么
//
// 同一个番号在三个地方写法不同：
//
//   - 上游（JAVDB / missav 那一众）：`MAD-016`、`BLX-0072` —— 带连字符；
//   - 库（`jav_movies.number`）：`MAD016`、`BLX0072` —— 不带（1719 条，全是国产）；
//   - 刮削目标（磁盘 `.strm` 主干）：可能两者之一。
//
// 而 `GetByNumber` 是**全等**匹配（只大小写不敏感）。于是真机上 10 部片里有 7 部
// 在「把中文标题写进侧车」那一步被记成 `库里查不到这一部` 而**静默跳过** ——
// 库里明明有这一部，标题就在那里。
//
// # 为什么归一化放在 domain，而不是 SQL 里
//
// SQL 侧也能写（`replace(lower(number),'-','') = ?`），但那是**全表扫描**：
// 表达式上没有任何索引可用，而库有 1.6 万行，`GET /api/jav/...` 这类读路径
// 不该为它买单。归一到「候选形态」再让 `GetByNumber` 走索引更合适 ——
// 一个番号最多试 2~3 个形态（见 NumberLookupForms）。
//
// ⚠️ **别把这条归一化用在「写库」上**：库里的形态是上游给的、并且被
// `javrules.NormalizeCNHyphen` 那一套整理规则认着（见记忆 litepan-jav-cn-no-hyphen：
// 磁盘与库的规范形态分别是 `MGL-0002` 与 `MGL0002`）。这里只解决**读取时找不到**。
const (
	// numberSeparators 是要**忽略**的番号分隔符。
	//
	// 与 jav 包 catalog.go 里那个（未导出的）normalizeNumber 同源。刻意含 `_`：
	// 上游偶尔写 `SSIS_001`。不含 `.`：欧美那档的点分日期（`vixen.26.09.19`）里
	// 的点是番号本体的一部分，去掉会与另一部片撞上。
	numberSeparators = "-_"
)

// NormalizeNumberKey 把一个番号归一成**比较键**：大写、去分隔符与空白。
//
//	"MAD-016" / "mad016" / "MAD_016" → "MAD016"
//
// 用户眼里它们本来就是同一个番号。**只用于比较**，不要拿它写盘。
func NormalizeNumberKey(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	v = strings.NewReplacer("-", "", "_", "", " ", "", "\t", "").Replace(v)
	return v
}

// NumberLookupForms 返回「查库时可以依次试的那几个形态」，**第一个就是原样子**。
//
// 顺序是「最可能命中的在前」：
//
//  1. 原样（绝大多数番号是全等存的，这一步就命中）；
//  2. 若原样带连字符 → 去掉第一个连字符（`MAD-016` → `MAD016`）。
//     这是真机上 1719 条国产番号的存法，也是「刮削目标是带连字符、库里不带」那 7 部
//     的形态。
//
// # 为什么不枚举全部切分
//
// 同一批番号里确实存在「去掉连字符后撞车」的情形（2026-10-17 全库实测 3 组：
// `T-38052`/`T38-052`、`T28549` 那一对、`GRCH-279-2`/`GRCH-2792`）。如果这里
// 枚举所有「往哪儿插连字符」的组合，那 3 组就会被**随机**挑中一条 —— 数据没错、
// 但可能不是这一部。所以反过来做：只试「原样」与「**库里那种降级形态**」两种，
// 命中即止，其余交错形态交给调用方「查不到就算了」。
//
// 反过来的方向（原样不带连字符、库里有带连字符那几组）**刻意不做**：
// 那要往任意位置插连字符，误配面比「删一个连字符」大一个量级，
// 而「库里是加连字符形态」这件事在 2026-10-17 全库是 0 条。
func NumberLookupForms(number string) []string {
	number = strings.TrimSpace(number)
	if number == "" {
		return nil
	}
	out := []string{number}
	// 只去掉**第一个**连字符：`MDSR0006-1` → `MDSR-0006-1` 那一类是「厂牌后接数字」
	// 的降级形态，而 `A-123-456` 这种多段的，第一个连字符去掉之后才是国产那套写法。
	if i := strings.IndexByte(number, '-'); i > 0 {
		loose := number[:i] + number[i+1:]
		// 与「原样」真的一样（`-` 后面又是 `-` 之类）就没有第二种形态可言。
		//
		// ⚠️ 判据必须是**原样字符串**比较，不能拿归一后的键比 —— 去掉连字符本来就
		// 不改变键，那样写这第二种形态永远进不来（第一版就栽在这，测试立刻红了）。
		if loose != number {
			out = append(out, loose)
		}
	}
	return out
}

// SameNumber 报告两个番号是不是同一部片（忽略大小写与分隔符）。
//
// 给「上游给的番号 vs 磁盘上的番号」这类**不查库**的比较用；
// 查库那条路走 NumberLookupForms。
func SameNumber(a, b string) bool {
	ka, kb := NormalizeNumberKey(a), NormalizeNumberKey(b)
	return ka != "" && ka == kb
}
