package planner

import (
	"litepan/internal/domain"
	"litepan/internal/mediaorganize/moplan"
	"litepan/internal/mediaorganize/rules"
)

func DetectSameWorkDirConflicts(p *Planner) {
	p.detectSameWorkDirConflicts()
}

type BatchEntryForTest struct {
	Item      domain.FileItem
	Ancestors []rules.Ancestor
}

func GroupEntriesForTestExport(p *Planner, entries []BatchEntryForTest) (map[GroupKeyForTest]int, []PendingSkipForTest) {
	internal := make([]batchEntry, len(entries))
	for i, e := range entries {
		internal[i] = batchEntry{item: e.Item, ancestors: e.Ancestors}
	}
	groups, pending := p.groupEntries(internal)
	out := make(map[GroupKeyForTest]int, len(groups))
	for key, items := range groups {
		out[GroupKeyForTest{
			MediaKind: key.mediaKind,
			DirID:     key.dirID,
			DirName:   key.dirName,
			Title:     key.title,
			Year:      key.yearPtr(),
		}] = len(items)
	}
	pendingOut := make([]PendingSkipForTest, len(pending))
	for i, ps := range pending {
		pendingOut[i] = PendingSkipForTest{Reason: ps.reason}
	}
	return out, pendingOut
}

type GroupKeyForTest struct {
	MediaKind string
	DirID     string
	DirName   string
	Title     string
	Year      *int
}

type PendingSkipForTest struct {
	Reason string
}

func (p *Planner) SetActions(actions []moplan.PlanAction) {
	p.actions = append([]moplan.PlanAction(nil), actions...)
}

func (p *Planner) Actions() []moplan.PlanAction {
	return append([]moplan.PlanAction(nil), p.actions...)
}

func ExtensionEnabledForTest(p *Planner, extension string, metadata bool) bool {
	extensions := p.mediaExts
	if metadata {
		extensions = p.metaExts
	}
	_, ok := extensions[extension]
	return ok
}

func (p *Planner) SetScannedDirNames(names map[string]string) {
	for k, v := range names {
		p.scannedDirNames[k] = v
	}
}
