package mediaorganize

import (
	"testing"
)

// 这一组用例钉住一条真机事故：番号联动在「只碰到一两部片」的那些天里必失败。
//
// 现场（群晖 2026-10-28）：整理范围里只有 1 部片（SNOS-438），它正常移走了，
// 另有一条「侧车永不删除」的计划期跳过。摘要算成 total=3 / skipped=2 /
// abnormal_skipped=2 → 风险比例 66.67% 超过 30% 的闸，自动化第二步
// （prev_success 的 STRM）被跳过，看起来像「联动坏了」。
//
// 根因：侧车保留是**设计如此**，但 isNormalSkip 只认一张文案表，
// 「已保留（侧车永不删除）」不在表里，于是被算成「需关注」。

func plannedSkip(reason string) map[string]any {
	return map[string]any{"name": "", "reason": reason}
}

func TestSummarizePlanCountsSidecarKeptAsNormal(t *testing.T) {
	t.Run("带 normal 标记的计划期跳过不计入异常", func(t *testing.T) {
		plan := &Plan{
			Actions: []PlanAction{
				{Kind: ActionKindRelocate, Status: "done", SourceParentID: "a", TargetParentID: "b"},
			},
			Skipped: []map[string]any{{
				"name":   "",
				"reason": "小文件阈值内的 1 个元数据 json 已保留（侧车永不删除）",
				"normal": true,
			}},
		}
		summary := summarizePlan(plan, false)
		if got := summary["normal_skipped"]; got != 1 {
			t.Fatalf("侧车保留应计为正常跳过，实际 normal_skipped=%v summary=%v", got, summary)
		}
		if got := summary["abnormal_skipped"]; got != 0 {
			t.Fatalf("侧车保留不应计入异常，实际 abnormal_skipped=%v", got)
		}
		// 复算自动化那侧的比例：分母 = total - normal_skipped = 2 - 1 = 1，
		// 分子 = failed + abnormal = 0 → 0%，不再顶爆 30% 的闸。
		total := summary["total"].(int)
		normal := summary["normal_skipped"].(int)
		abnormal := summary["abnormal_skipped"].(int)
		riskTotal := total - normal
		if riskTotal <= 0 {
			t.Fatalf("分母算错：total=%d normal=%d", total, normal)
		}
		risk := float64(abnormal) / float64(riskTotal) * 100
		if risk > 0 {
			t.Fatalf("侧车保留场景的风险比例应为 0%%，实际 %.2f%%", risk)
		}
	})

	t.Run("不删除的文件类型同样按 normal 处理", func(t *testing.T) {
		plan := &Plan{
			Actions: []PlanAction{
				{Kind: ActionKindRelocate, Status: "done", SourceParentID: "a", TargetParentID: "b"},
			},
			Skipped: []map[string]any{{
				"name":   "",
				"reason": "小文件阈值内的 2 个文件因命中「不删除的文件类型」而保留",
				"normal": true,
			}},
		}
		summary := summarizePlan(plan, false)
		if got := summary["normal_skipped"]; got != 1 {
			t.Fatalf("「不删除的文件类型」保留应计为正常跳过，实际 %v", got)
		}
	})

	// 该算异常的一条都不能被放过：认不出番号是**无法判断归属**，必须留在异常里，
	// 否则闸门就形同虚设。
	t.Run("认不出番号的跳过仍计入异常", func(t *testing.T) {
		plan := &Plan{
			Actions: []PlanAction{
				{Kind: ActionKindRelocate, Status: "done", SourceParentID: "a", TargetParentID: "b"},
			},
			Skipped: []map[string]any{plannedSkip(
				"该目录有多份侧车，但这个名字里认不出番号 —— 无法判断它属于哪一份，保持原样")},
		}
		summary := summarizePlan(plan, false)
		if got := summary["abnormal_skipped"]; got != 1 {
			t.Fatalf("认不出番号应计入异常，实际 abnormal_skipped=%v", got)
		}
		if got := summary["normal_skipped"]; got != 0 {
			t.Fatalf("认不出番号不应计为正常跳过，实际 %v", got)
		}
	})

	// 老计划（没有 normal 标记）继续走文案兜底，行为不回归。
	t.Run("老计划的文案兜底仍然生效", func(t *testing.T) {
		plan := &Plan{
			Skipped: []map[string]any{plannedSkip("文件已自动并入目标目录")},
		}
		summary := summarizePlan(plan, false)
		if got := summary["normal_skipped"]; got != 1 {
			t.Fatalf("文案兜底应把「已并入」认成正常跳过，实际 %v", got)
		}
	})
}

// isNormalPlannedSkip 的标记优先于文案：即便文案里带着异常味的关键词，
// 只要规划器明确标了 normal 就按正常算。
func TestIsNormalPlannedSkipPrefersFlag(t *testing.T) {
	if !isNormalPlannedSkip(map[string]any{"reason": "随便什么文案", "normal": true}) {
		t.Fatal("normal=true 应直接判为正常跳过")
	}
	if isNormalPlannedSkip(map[string]any{"reason": "随便什么文案", "normal": false}) {
		t.Fatal("normal=false 且文案无标记时不应判为正常跳过")
	}
}
