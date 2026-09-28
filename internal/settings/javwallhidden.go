package settings

import (
	"encoding/json"
	"sort"
	"strings"
)

// 「番号墙隐藏的目录」的值格式（设置键 KeyStrmJavWallHiddenDirs）。
//
// # 为什么是一个 JSON 数组，而不是「;」分隔的列表
//
// 与 JavMetaItems 同一个坑：**空串在本项目里表示「没存过、回落默认」**。
// 而「一个目录都不隐藏」是用户完全可能想要的（把分类规则里的兜底目录也显示出来），
// 用空串表达它就会被读侧当成没存过，用户取消全选、保存、刷新一看还藏着。
// JSON 数组 `[]` 非空串，语义完整。
//
// # 为什么「没存过」不直接给死默认值
//
// 默认隐藏的是**分类规则里那条兜底规则当前的目标目录**（默认叫「未匹配」）。
// 用户在规则编辑器里把兜底目录改名（比如改成「待处理」）后，隐藏的必须跟着改 ——
// 这是改动前写死取末条规则那个行为，升级上来不能变。
// 所以 Default 留空串（= 没存过），由读侧（strmscrape.javWallHiddenDirs）动态回落，
// 一旦用户保存过，就是他当场看到并勾选的那份名单（含当时的名字）。

// ParseJavWallHiddenDirs 解析设置值。
//
// 第二个返回值报告「库里有值吗」：**空串、坏 JSON、`null` 都算没存过**，调用方据此
// 决定要不要回落默认的兜底目录名。`[]`（解析成功但为空）是有效值，返回 ok=true ——
// 这就是「一个目录都不隐藏」与「还没勾过」的分界线。
func ParseJavWallHiddenDirs(raw string) ([]string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, false
	}
	var parsed []string
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil, false
	}
	// JSON `null` 解出来是 nil 切片，与「空数组」不是一回事：前者连数组都没给，
	// 按「没存过」处理才安全（我们的编码器只会写出 [] / ["名"]，写不出 null）。
	if parsed == nil {
		return nil, false
	}
	return normalizeJavWallHiddenDirs(parsed), true
}

// EncodeJavWallHiddenDirs 序列化成规范字符串。
//
// **排序 + 去重**（Normalize 里做）：前端拿前后两个字符串一比就知道「有没有改动」，
// 顺序不稳定会一直误报未保存 —— 与 JavMetaItems 的「字段顺序固定」是同一条理由。
// 空列表编码成 `[]`（合法值），绝不编码成空串（那会被读侧当成没存过）。
func EncodeJavWallHiddenDirs(dirs []string) string {
	clean := normalizeJavWallHiddenDirs(dirs)
	data, err := json.Marshal(clean)
	if err != nil {
		// 只会序列化 []string，到了这里说明 encoding/json 有 bug ——
		// 给 `[]`（= 什么都不藏）比给空串（= 回落默认、可能藏起来）安全。
		return "[]"
	}
	return string(data)
}

// normalizeJavWallHiddenDirs 是设置写入路径上的收口（Spec.normalize）与读取路径的归一：
// 去空白、去空项、去重、排序。保证库里永远不会出现空串或坏 JSON。
func normalizeJavWallHiddenDirs(dirs []string) []string {
	seen := make(map[string]struct{}, len(dirs))
	out := make([]string, 0, len(dirs))
	for _, raw := range dirs {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// normalizeJavWallHiddenDirsValue 是 Spec.normalize 的签名。
func normalizeJavWallHiddenDirsValue(raw string) string {
	dirs, ok := ParseJavWallHiddenDirs(raw)
	if !ok {
		// 没存过 / 坏值：原样留空串，让读侧继续走「动态回落兜底目录」那条路。
		// 这里**不能**填默认值 —— 一填就把它固化成一个死名字了。
		return ""
	}
	return EncodeJavWallHiddenDirs(dirs)
}
