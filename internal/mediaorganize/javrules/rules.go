package javrules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// ReplaceRule 一条「替换字符」规则：字面替换，不是正则。
// JSON key 保持 115-auto 的 from/to，方便日后直接导入导出 organize_rules.json。
type ReplaceRule struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// 分类规则的三种匹配方式。
const (
	ModeNocode   = "nocode"
	ModePattern  = "pattern"
	ModeIncludes = "includes"
)

// ClassifyRule 一条「分类移动」规则。
//
// 匹配方式是**显式**的（Mode 字段），三选一：nocode / pattern / includes。
//
// 早期版本是从「哪个字段有值」反推匹配方式，结果是切到「正则」就必须清空关键词、
// 切回「关键词」又必须清空正则 —— 用户点一下按钮，之前填的东西就没了，
// 而且正则还没填内容时会被反推回「关键词」，看起来像按钮失灵。现在切换只改 Mode，
// 三种方式的输入内容全部保留，来回切不丢东西。
type ClassifyRule struct {
	Name       string   `json:"name"`
	TargetName string   `json:"target_name"`
	Mode       string   `json:"mode,omitempty"`
	Nocode     bool     `json:"nocode,omitempty"`
	Pattern    string   `json:"pattern,omitempty"`
	Includes   []string `json:"includes,omitempty"`
	Excludes   []string `json:"excludes,omitempty"`
}

// EffectiveMode 返回规则实际的匹配方式。
//
// Mode 为空时按旧规则反推（nocode → pattern → includes），这样升级前存下的规则
// 不用迁移也能继续跑；Normalize 会在下一次读写时把 Mode 补齐成显式值。
func (r ClassifyRule) EffectiveMode() string {
	switch r.Mode {
	case ModeNocode, ModePattern, ModeIncludes:
		return r.Mode
	}
	if r.Nocode {
		return ModeNocode
	}
	if r.Pattern != "" {
		return ModePattern
	}
	if len(r.Includes) > 0 {
		return ModeIncludes
	}
	return ""
}

// Rules 全局番号规则。JSON key 与 115-auto 的 organize_rules.json 一致。
type Rules struct {
	JunkChars     []string       `json:"junk_chars"`
	ReplaceRules  []ReplaceRule  `json:"replace_rules"`
	ClassifyRules []ClassifyRule `json:"classify_rules"`
}

// Normalize 归一化规则：**缺 key 或空列表就用默认值**，不把空规则当成「用户想禁用」——
// 旧面板也是这个语义（文件读不到就回落代码默认），老用户升级上来行为不变。
//
// 注意这与「用户删光了所有删除字符」是冲突的：删光等于回到默认。
// 这是刻意的 —— 少数情况（真想清空）下用户删到只剩一条不可能命中的规则即可。
func Normalize(raw Rules) Rules {
	out := Rules{
		JunkChars:     cleanStrings(raw.JunkChars),
		ReplaceRules:  normalizeReplaceRules(raw.ReplaceRules),
		ClassifyRules: normalizeClassifyRules(raw.ClassifyRules),
	}
	def := Defaults()
	if len(out.JunkChars) == 0 {
		out.JunkChars = def.JunkChars
	}
	if len(out.ReplaceRules) == 0 {
		out.ReplaceRules = def.ReplaceRules
	}
	if len(out.ClassifyRules) == 0 {
		out.ClassifyRules = def.ClassifyRules
	}
	return out
}

// Parse 解析存库的 JSON。空串 / 非法 JSON 都退回默认值，不报错 ——
// 设置项损坏不该让整个整理任务起不来。
func Parse(raw string) Rules {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Defaults()
	}
	var parsed Rules
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return Defaults()
	}
	return Normalize(parsed)
}

// Marshal 序列化用于存库。
func Marshal(r Rules) (string, error) {
	data, err := json.Marshal(Normalize(r))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Fingerprint 返回规则的稳定指纹，用来判断「这份计划是按当前规则生成的吗」。
//
// 规范化之后再做 SHA-256：字段顺序由 struct 定义固定，所以同一份规则永远得到同一个值。
// 计划里存一份指纹，读取时对不上就说明规则改过了 —— 那份计划已作废，应该重新生成。
// 没有这道检查的话，用户改完规则打开预览会看到按**旧规则**生成的计划，
// 表现就是「改了没用」。
func Fingerprint(r Rules) string {
	data, err := json.Marshal(Normalize(r))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func normalizeReplaceRules(in []ReplaceRule) []ReplaceRule {
	out := make([]ReplaceRule, 0, len(in))
	// 匹配不区分大小写，所以 `4K` 和 `4k` 是同一个词 —— 后者永远不可能命中，
	// 留着只会让人以为「加了一条新规则」。保留第一条（真正生效的那条）。
	seen := make(map[string]struct{}, len(in))
	for _, r := range in {
		// 旧面板只接受同时带 from 和 to 的条目（from 可以为空串但不为 nil）。
		if r.From == "" && r.To == "" {
			continue
		}
		key := strings.ToLower(r.From)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ReplaceRule{From: r.From, To: r.To})
	}
	return out
}

func normalizeClassifyRules(in []ClassifyRule) []ClassifyRule {
	out := make([]ClassifyRule, 0, len(in))
	for _, r := range in {
		name := strings.TrimSpace(r.Name)
		target := strings.TrimSpace(r.TargetName)
		if name == "" || target == "" {
			continue // 没名字或没目标的规则是废规则，不落库
		}
		item := ClassifyRule{
			Name:       name,
			TargetName: target,
			Mode:       r.EffectiveMode(),
			Nocode:     r.Nocode,
			Pattern:    strings.TrimSpace(r.Pattern),
			Includes:   cleanStrings(r.Includes),
			Excludes:   cleanStrings(r.Excludes),
		}
		// 把 Mode 与 nocode 同步：Mode=nocode 时 nocode 必须为真（反之亦然），
		// 免得两个字段互相矛盾，读代码的人不知道信哪个。
		item.Nocode = item.Mode == ModeNocode
		out = append(out, item)
	}
	return out
}

func cleanStrings(in []string) []string {
	out := make([]string, 0, len(in))
	// 同样按不区分大小写去重：删除字符也是不区分大小写匹配的。
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		key := strings.ToLower(s)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}
