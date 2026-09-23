package quality

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

// ———————————————————————— 打分 ————————————————————————

// ResourceScore 是候选资源的主排序键：**[清晰度, 破解, 体积]**，按字典序比较。
//
// 优先级：清晰度（超清 > 高清 > 普通）→ 破解 → 文件大小（越大越好）。
//
// 与源码 resource_score 的差异就是前两位换了个位置。源码是 [破解, 清晰度, 体积]，
// 那意味着一颗 720p 的流出版会压过一颗 2160p 的正片 —— 分辨率是唯一
// 「打开就能看见、无法用别的方式弥补」的维度，而破解与否是内容属性，
// 再高的分辨率也补不回清晰度本身的差距。所以清晰度排第一。
//
// 体积为什么排在最后而不是第二：同一分辨率下体积确实是码率的代理，
// 但跨分辨率时它不成立 —— 一颗 20GB 的 1080p 未必比 8GB 的 2160p 好看。
// 字典序天然把「先比分辨率、同分辨率再比体积」表达清楚了。
//
// 调用方注意：size 未知时应当传 0，不要传一个「猜的」值。未知体积的磁链
// 在这套排序里排在同分辨率同破解状态的最末尾，这是对的 ——
// 宁可推一颗看得见大小的。
func ResourceScore(t Tags, sizeBytes int64) []int64 {
	uncensored := int64(0)
	if t.Uncensored {
		uncensored = 1
	}
	if sizeBytes < 0 {
		sizeBytes = 0
	}
	// 档位用带体积兜底的那一版：角标把 30GB 的片显示成 4K，排序就该按超清档排，
	// 否则会出现「界面上是 4K、实际排在 1080p 后面」这种自相矛盾。
	return []int64{int64(t.ResolutionWithSize(sizeBytes)), uncensored, sizeBytes}
}

// RankKey 是「主排序键 + 同分决胜键」的完整比较键。
//
// 前三位与 ResourceScore 完全一致，后面的决胜项**只在主键完全相同时**才起作用。
// 源码在这个位置只有一个 has_tracker（磁链里有没有 &tr=）然后就是 id 大小 ——
// id 大者胜等于「后入库的赢」，和资源质量毫无关系。
//
// 这里换成一组真实信号，且**不改变任何一条主键的先后**：
// 中字 → 片源 → 编码 → tracker 数量。
//
// 为什么中字放在决胜而不是主键：用户说得很明确「越大越优先」，把中字提到体积
// 前面会让一颗 2GB 中字顶掉一颗 20GB 无字幕，那不是他要的。但体积完全相同
// （或都没解析出体积）时，中字显然比无字幕更该被选中。
func RankKey(t Tags, sizeBytes int64, magnetURI string) []int64 {
	key := ResourceScore(t, sizeBytes)
	subtitle := int64(0)
	if t.Subtitle {
		subtitle = 1
	}
	return append(key,
		subtitle,
		int64(t.Source),
		int64(t.Codec),
		int64(TrackerCount(magnetURI)),
	)
}

// CompareRankKey 按字典序比较两个排序键。返回正数表示 a 更优。
func CompareRankKey(a, b []int64) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] > b[i] {
				return 1
			}
			return -1
		}
	}
	return len(a) - len(b)
}

// ———————————————————————— 磁链里的 tracker ————————————————————————

var reTracker = regexp.MustCompile(`(?i)[?&]tr=([^&]+)`)

// TrackerCount 数一颗磁链里带了几条 tracker。
//
// 源码只判断「有没有 &tr=」，有就 +1。多带几条 tracker 能显著提高
// 冷门资源的可下载性（DHT 找不到 peer 时，tracker 是唯一的路），
// 所以这里按条数计，一条都没有时是 0。
func TrackerCount(magnetURI string) int {
	if !strings.Contains(strings.ToLower(magnetURI), "tr=") {
		return 0
	}
	return len(reTracker.FindAllString(magnetURI, -1))
}

// ———————————————————————— 资源指纹 ————————————————————————

var reBtih = regexp.MustCompile(`(?i)btih:([0-9a-f]{40})`)

// ExtractBtih 从磁链里取出 40 位 btih，取不到返回空串。
func ExtractBtih(magnetURI string) string {
	m := reBtih.FindStringSubmatch(magnetURI)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1])
}

// ResourceFingerprint 给一颗资源算稳定指纹。
//
// 取值顺序 btih → magnet → name，与源码 subscriptions.resource_fingerprint 一致。
//
// 为什么必须有它：幂等键里嵌的就是这个值。**改算法等于改幂等键**，
// 升级后所有推送过的资源都会被当成新资源重推一遍。要改先想清楚。
//
// btih 优先是因为它天然稳定：同一颗种子无论从哪个站抓来、名字被改写成什么样，
// btih 都一样。名称兜底是给上游没给 hash 的情况留的活路。
func ResourceFingerprint(btih, magnetURI, name string) string {
	base := strings.TrimSpace(btih)
	if base == "" {
		base = strings.TrimSpace(magnetURI)
	}
	if base == "" {
		base = strings.TrimSpace(name)
	}
	sum := sha1.Sum([]byte(strings.ToLower(base)))
	return "sha1:" + hex.EncodeToString(sum[:])
}

// MagnetFingerprint 是 jav_magnets 表的主键值。
//
// 有 40 位 btih 就直接用它（可读、可与其它系统对齐），否则退化成 sha1 指纹。
// **绝不返回空串** —— 空串在 SQLite 的 TEXT 主键里会被当成一个正常值，
// 于是所有取不到 hash 的磁链会挤成同一行，互相覆盖。
func MagnetFingerprint(btih, magnetURI, name string) string {
	if h := strings.ToLower(strings.TrimSpace(btih)); h != "" {
		return h
	}
	if h := ExtractBtih(magnetURI); h != "" {
		return h
	}
	return ResourceFingerprint("", magnetURI, name)
}

// ———————————————————————— 幂等键 ————————————————————————

// AutoPushKey 是自动推送（整个订阅挑最优）的幂等键。
//
// 格式 `auto:{sid}:{fp}` 是**持久契约**，不是内部实现细节：
// 它被写进 jav_subscription_push_attempts 并且有唯一索引。
// 改动格式会让升级前推送过的每一颗资源都变成「没推过」，全量重推一遍。
func AutoPushKey(subscriptionID int64, resourceFingerprint string) string {
	return "auto:" + strconv.FormatInt(subscriptionID, 10) + ":" + resourceFingerprint
}

// SubscribeMovieKey 是逐部推送（演员/清单订阅里点某部片）的幂等键。
// 与 AutoPushKey 同理，格式是持久契约。
func SubscribeMovieKey(subscriptionID int64, movieID, resourceFingerprint string) string {
	return "sub:" + strconv.FormatInt(subscriptionID, 10) + ":" + movieID + ":" + resourceFingerprint
}
