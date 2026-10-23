package executor_test

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/executor"
	"litepan/internal/mediaorganize/moplan"
)

// recordingOverwriteFS 记下 DeleteFiles 真的删了哪些 ID，并且**真的删掉**
// （别的测试里的 recordingDeleteFS 是一删就报错，用来证明「不该删」）。
type recordingOverwriteFS struct {
	mockExecFS
	deleted []string
}

func (m *recordingOverwriteFS) DeleteFiles(_ context.Context, _ int64, fileIDs []string, parentID string) error {
	m.deleted = append(m.deleted, fileIDs...)
	idSet := map[string]struct{}{}
	for _, id := range fileIDs {
		idSet[id] = struct{}{}
	}
	keep := make([]domain.FileItem, 0, len(m.dirs[parentID]))
	for _, item := range m.dirs[parentID] {
		if _, ok := idSet[item.ID]; ok {
			continue
		}
		keep = append(keep, item)
	}
	m.dirs[parentID] = keep
	return nil
}

// 同一集在目标目录里堆了好几份历史版本时，洗版要把它们**都**删掉，
// 只留下新进来的那一份 —— 否则「集数不重复」这条今天修完明天又冒出来。
//
// 元数据走一遍 JSON 往返（计划要存库 / 过前端预览），所以 []string 会变成
// []any —— 这条也得认。
func TestOverwriteDeletesAllHistoricalCopiesOfSameEpisode(t *testing.T) {
	for _, roundTrip := range []bool{false, true} {
		fs := &recordingOverwriteFS{mockExecFS: mockExecFS{dirs: map[string][]domain.FileItem{
			"root": {{ID: "d1", Name: "Season 01", IsDir: true}},
			"d1": {
				{ID: "old1", Name: "剧 S01E01 [1080p].mkv"},
				{ID: "old2", Name: "剧 S01E01 [720p].mkv"},
			},
		}}}
		plan := &moplan.Plan{
			TaskID: "t1",
			Actions: []moplan.PlanAction{{
				ID:             "a1",
				Kind:           moplan.ActionKindRelocate,
				SourceID:       "new",
				SourceName:     "剧 S01E01 [2160p].mkv",
				SourceParentID: "src",
				TargetParentID: "d1",
				TargetName:     "剧 S01E01 [2160p].mkv",
				Metadata: map[string]any{
					"_resolved_target_parent_id": "d1",
					"_overwrite_target_id":       "old1",
					"_overwrite_target_ids":      []string{"old1", "old2"},
				},
			}},
		}
		if roundTrip {
			data, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			if plan, err = moplan.Parse(data); err != nil {
				t.Fatal(err)
			}
		}
		fs.dirs["src"] = []domain.FileItem{{ID: "new", Name: "剧 S01E01 [2160p].mkv"}}
		ex := executor.New(context.Background(), fs, plan, 1, true, func(string) {}, nil)
		if _, err := ex.Apply(); err != nil {
			t.Fatal(err)
		}
		sort.Strings(fs.deleted)
		want := []string{"old1", "old2"}
		if len(fs.deleted) != len(want) {
			t.Fatalf("roundTrip=%v deleted=%v, want %v", roundTrip, fs.deleted, want)
		}
		for i := range want {
			if fs.deleted[i] != want[i] {
				t.Fatalf("roundTrip=%v deleted=%v, want %v", roundTrip, fs.deleted, want)
			}
		}
	}
}
