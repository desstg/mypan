package javrules

import (
	"fmt"
	"regexp"
	"strings"
)

// 本文件是「更好用」那一层：不改动核心算法，只把它的决策过程暴露出来。
//
// 规则编辑最大的问题是**盲改** —— 用户在设置页加一条替换词，没有任何办法验证
// 它到底会不会命中、会不会把名字改坏，只能存下来跑一次真实任务看结果。
// Explain 让设置页能实时试跑，Validate 让写错的正则当场暴露。

// Explain 一个文件名的完整处理结果：识别到什么番号、改了哪些地方、会归到哪个分类。
type Explain struct {
	Name           string   `json:"name"`
	HasCode        bool     `json:"has_code"`
	Code           string   `json:"code"`
	Renamed        string   `json:"renamed"`
	DirName        string   `json:"dir_name"`
	Changed        bool     `json:"changed"`
	Steps          []string `json:"steps"`
	ClassifyRule   string   `json:"classify_rule"`
	ClassifyTarget string   `json:"classify_target"`
	Warnings       []string `json:"warnings"`
}

// ExplainName 试跑单个文件名。
func ExplainName(name string, rules Rules) Explain {
	rules = Normalize(rules)
	tr := &trace{on: true}
	renamed := rename(name, rules, tr)

	dirStem, _ := splitExt(renamed)
	res := Explain{
		Name:     name,
		HasCode:  HasCode(name),
		Code:     ExtractCode(name),
		Renamed:  renamed,
		DirName:  dirStem,
		Changed:  renamed != name,
		Steps:    tr.steps,
		Warnings: tr.warns,
	}
	if res.Steps == nil {
		res.Steps = []string{}
	}

	// 与 planner 保持一致：原名优先、清理名兜底。试跑看到的分类必须就是实际会用的那个。
	idx := ClassifyNameFallback(name, res.DirName, rules.ClassifyRules)
	if idx >= 0 {
		rule := rules.ClassifyRules[idx]
		res.ClassifyRule = rule.Name
		res.ClassifyTarget = rule.TargetName
	}
	if err := checkClassifyPatterns(rules.ClassifyRules); len(err) > 0 {
		res.Warnings = append(res.Warnings, err...)
	}
	if res.Warnings == nil {
		res.Warnings = []string{}
	}
	return res
}

// ExplainBatch 试跑一批文件名（设置页可以粘贴多行一次看结果）。
func ExplainBatch(names []string, rules Rules) []Explain {
	rules = Normalize(rules)
	out := make([]Explain, 0, len(names))
	for _, n := range names {
		if strings.TrimSpace(n) == "" {
			continue
		}
		out = append(out, ExplainName(strings.TrimSpace(n), rules))
	}
	return out
}

// Validate 检查规则本身的毛病，返回人类可读的问题列表（空 = 没问题）。
//
// 保存接口用它做校验：**正则写错必须当场报错**。旧面板对 re.error 是静默吞掉的，
// 结果是一条永远不会命中的分类规则，用户完全看不出来 —— 这是最浪费时间的失败模式。
func Validate(rules Rules) []string {
	var problems []string

	for i, junk := range rules.JunkChars {
		if junk == "" {
			problems = append(problems, fmt.Sprintf("删除字符 第 %d 条是空的，请删掉或填写内容", i+1))
		}
	}

	for i, rp := range rules.ReplaceRules {
		switch {
		case rp.From == "" && rp.To == "":
			problems = append(problems, fmt.Sprintf("替换字符 第 %d 条是空的，请删掉或填写内容", i+1))
		case rp.From == "":
			// strings.ReplaceAll(s, "", to) 会在每个字符之间插入 to，直接把名字撑爆。
			problems = append(problems, fmt.Sprintf("替换字符 第 %d 条的「替换」是空的：空内容会插到每个字符之间，请填写要替换的文本", i+1))
		}
	}

	seenTargets := map[string]string{}
	for i, r := range rules.ClassifyRules {
		label := fmt.Sprintf("分类移动 第 %d 条", i+1)
		name := strings.TrimSpace(r.Name)
		target := strings.TrimSpace(r.TargetName)
		if name == "" {
			problems = append(problems, label+"没有填名称")
		}
		if target == "" {
			problems = append(problems, label+"没有填目标目录")
		}
		if name != "" && target != "" {
			if prev, ok := seenTargets[name]; ok && prev != target {
				// 同名不同目标不算错误，但命中顺序会决定结果，值得提醒。
				problems = append(problems, fmt.Sprintf("分类移动有两条都叫「%s」，但目标目录不同（%s / %s），请区分名称", name, prev, target))
			}
			seenTargets[name] = target
		}

		// 只校验**当前匹配方式**用到的那个字段。另外两种方式的内容是用户切来切去时
		// 留下的草稿，刻意保留着（切回去就能用），不该报成问题。
		switch r.EffectiveMode() {
		case ModeNocode:
			// 兜底规则没有必填项
		case ModePattern:
			if strings.TrimSpace(r.Pattern) == "" {
				problems = append(problems, fmt.Sprintf("%s「%s」选了正则但还没填内容，永远不会命中", label, name))
			} else if _, err := compileClassifyPattern(r.Pattern); err != nil {
				problems = append(problems, fmt.Sprintf("%s「%s」的正则写错了：%v", label, name, err))
			}
		case ModeIncludes:
			if len(cleanStrings(r.Includes)) == 0 {
				problems = append(problems, fmt.Sprintf("%s「%s」选了关键词但还没填内容，永远不会命中", label, name))
			}
		default:
			problems = append(problems, fmt.Sprintf("%s「%s」没有设置任何匹配条件（正则 / 关键词 / 无番号），永远不会命中", label, name))
		}
	}

	return problems
}

func checkClassifyPatterns(rules []ClassifyRule) []string {
	var out []string
	for _, r := range rules {
		if r.Pattern == "" {
			continue
		}
		if _, err := compileClassifyPattern(r.Pattern); err != nil {
			out = append(out, fmt.Sprintf("分类规则「%s」的正则写错了：%v", r.Name, err))
		}
	}
	return out
}

// compileClassifyPattern 与 ClassifyName 的匹配用同一套编译参数（忽略大小写）。
func compileClassifyPattern(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile("(?i)" + pattern)
}
