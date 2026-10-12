package pansou

import "strings"

// DriverCloudType 把 LitePan 的**驱动类型**映射成盘搜的**网盘类型**。
//
// ⚠️ 盘搜那边是它自己的词表（见 pansou-web 的 diskTypes.ts）：移动云盘叫 `mobile`、
// 天翼叫 `tianyi`，名字与驱动类型对不上。
//
// 映射表**只此一份**（住在这里是因为两个调用方——TG 订阅的自动搜索、
// 后台「搜索转存」页——都要用它，而它们不能互相 import）。
// 前端也不要自己再写一遍：两边各一份必然漂移，表现是
// 「选了某个账号，搜出来还是别人的结果」。
//
// OneDrive / OpenList / WebDAV / 本机存储没有对应的盘搜类型（返回空串），
// 那种账号搜索时只按磁力走。
var driverCloudTypes = map[string]string{
	"115_open":   "115",
	"123_open":   "123",
	"139_cloud":  "mobile",
	"189_cloud":  "tianyi",
	"baidu_open": "baidu",
	"guangya":    "guangya",
	"quark":      "quark",
}

// DriverCloudType 查驱动类型对应的盘搜类型；查不到返回空串。
func DriverCloudType(driverType string) string {
	return driverCloudTypes[trimLower(driverType)]
}

// CloudTypesForDriver 拼出「这个网盘该搜哪些类型」。
//
// 规则：**磁力永远保留 + 该网盘自己的那类**。
//   - 磁力：任何网盘都能靠离线下载（或内置下载器）把它存进去，而且实测盘搜的
//     磁力覆盖最好；
//   - 账号那一类：只有它才可能带分享链，而分享链是「网盘原生」这种存法的主力。
//
// 没有对应盘搜类型的驱动（OneDrive / WebDAV / 本机存储…）只返回 magnet。
//
// 返回逗号分隔的字符串，直接可喂给 Options.CloudTypes。
func CloudTypesForDriver(driverType string) string {
	if own := DriverCloudType(driverType); own != "" {
		return "magnet," + own
	}
	return "magnet"
}

// MergeCloudTypes 把「推导出来的基础类型」与「用户额外填的」合并去重。
//
// 用户填的那份是**追加**而不是覆盖：基础的「磁力 + 目标网盘那一类」永远要在，
// 否则搜出来的东西根本没有对应的存法（表现是匹配历史里堆满「暂不支持投递」）。
// 用户想多搜几类的场景（比如同一批订阅要挑夸克盘上的资源）靠追加满足。
//
// 顺序：先基础、后追加（盘搜按这个顺序拼结果，磁力排最前是刻意的）。
func MergeCloudTypes(base, extra string) string {
	out := make([]string, 0, 4)
	seen := map[string]struct{}{}
	add := func(raw string) {
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			key := strings.ToLower(t)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, t)
		}
	}
	add(base)
	add(extra)
	if len(out) == 0 {
		return DefaultCloudTypes
	}
	return strings.Join(out, ",")
}

func trimLower(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
