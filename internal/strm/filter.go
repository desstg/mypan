package strm

import (
	"strings"

	"litepan/internal/domain"
)

func parseKeywordRules(text string) []string {
	text = strings.ReplaceAll(text, "；", ";")
	parts := strings.Split(text, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func matchesKeywordRules(name string, rules []string) bool {
	if len(rules) == 0 {
		return false
	}
	candidate := strings.ToLower(name)
	for _, rule := range rules {
		if strings.Contains(candidate, rule) {
			return true
		}
	}
	return false
}

// isQuarantineDir 判断一个目录是不是「冗余目录」（目录整理把同集重复的文件
// 移进去的那个）。
//
// 为什么 STRM 要无条件跳过它：那个目录里的文件是**故意留给人手动删的重复集**，
// 给它生成 .strm 等于把重复集又搬进 Emby，正是用户要消掉的东西。而整理的目标
// 根目录常常就在 STRM 扫描根里面（`…/tmdb影片/电视剧`），靠用户给每个 STRM
// 任务手填排除词不现实 —— 而且名字还能在设置里改。
//
// 判据是**整名相等**（不分大小写），不用 matchesKeywordRules 的包含匹配：
// 「冗余备份」这种正常目录不该被误伤。
func isQuarantineDir(name, quarantineDir string) bool {
	quarantineDir = strings.TrimSpace(quarantineDir)
	if quarantineDir == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(name), quarantineDir)
}

// dirPathExcluded 判断一条**相对目录链**里有没有被排除的目录，有就该文件整个跳过。
//
// 逐目录递归那条路是「遇到被排除的子目录就不往下走」，全量清单模式没有那个动作，
// 只能在拿到每个文件的完整目录链之后自己回溯。两条路必须给出同样的结果，
// 否则「开了排除词、但账号是 115 全量模式」时排除静默失效。
func dirPathExcluded(relDirs, excludeDirs []string, quarantineDir string) bool {
	for _, dir := range relDirs {
		if matchesKeywordRules(dir, excludeDirs) || isQuarantineDir(dir, quarantineDir) {
			return true
		}
	}
	return false
}

func normalizeConflictPolicy(policy string) string {
	switch strings.TrimSpace(strings.ToLower(policy)) {
	case domain.StrmConflictSizeAsc, domain.StrmConflictNameAsc:
		return strings.TrimSpace(strings.ToLower(policy))
	case "quality_then_size":
		return domain.StrmConflictSizeDesc
	default:
		return domain.StrmConflictSizeDesc
	}
}
