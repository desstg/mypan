package javplanner

import (
	"context"
	"strconv"
	"strings"
	"time"

	"litepan/internal/mediaorganize/rules"
)

// ScannedTree 是一次扫描的结果。files / dirs 都带 ParentID，方便后续按目录分组。
type ScannedTree struct {
	Files     []ScannedFile
	Dirs      []ScannedDir
	Truncated bool
	DirCount  int
}

// ScannedFile 扫描到的文件。
type ScannedFile struct {
	ID       string
	Name     string
	ParentID string
	Size     int64
}

// ScannedDir 扫描到的目录。
type ScannedDir struct {
	ID       string
	Name     string
	ParentID string
}

func (t *ScannedTree) ChildrenOf(parentID string) []ScannedFile {
	out := make([]ScannedFile, 0, 8)
	for _, f := range t.Files {
		if f.ParentID == parentID {
			out = append(out, f)
		}
	}
	return out
}

func (t *ScannedTree) SubdirsOf(parentID string) []ScannedDir {
	out := make([]ScannedDir, 0, 4)
	for _, d := range t.Dirs {
		if d.ParentID == parentID {
			out = append(out, d)
		}
	}
	return out
}

func (t *ScannedTree) FindDir(dirID string) *ScannedDir {
	for i := range t.Dirs {
		if t.Dirs[i].ID == dirID {
			return &t.Dirs[i]
		}
	}
	return nil
}

// AncestorIDs 返回 dirID 自身及它所有祖先的 ID（沿扫描树向上走）。
//
// 用来保护分类目标根：目标根可能是待整理目录**里面**的一个子目录（整理目录=库根，
// 是很常见的配法），它和它的祖先都不该被本任务改名或搬走。
// 路径型网盘上改了祖先的路径，目标根当场失效。
func (t *ScannedTree) AncestorIDs(dirID string) map[string]struct{} {
	out := map[string]struct{}{}
	if strings.TrimSpace(dirID) == "" {
		return out
	}
	parentOf := make(map[string]string, len(t.Dirs))
	for _, d := range t.Dirs {
		parentOf[d.ID] = d.ParentID
	}
	seen := map[string]struct{}{}
	cur := dirID
	for cur != "" {
		if _, dup := seen[cur]; dup {
			break // 目录树成环（异常数据）时保底，不能死循环
		}
		seen[cur] = struct{}{}
		out[cur] = struct{}{}
		cur = parentOf[cur]
	}
	return out
}

// scanTree 递归扫描源目录，收集**文件和目录**（不像 STRM 那样只留文件）。
//
// 两件事和现有 STRM 扫描保持一致：目录之间等待 API 间隔（避免触发网盘风控）、
// 认 ctx 取消。上限命中后立刻停止并在 Truncated 上记一笔 —— 静默截断会让用户
// 拿到一份「看起来完整」的残缺计划，比明确报错更糟。
func scanTree(
	ctx context.Context,
	files FileService,
	accountID int64,
	rootID string,
	apiIntervalMS int,
	maxDirs int,
	log LogFunc,
) (*ScannedTree, error) {
	tree := &ScannedTree{}
	if maxDirs <= 0 {
		maxDirs = defaultMaxDirs
	}
	interval := time.Duration(max(0, apiIntervalMS)) * time.Millisecond

	stack := []string{rootID}
	seen := map[string]struct{}{rootID: {}}

	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if tree.DirCount >= maxDirs {
			tree.Truncated = true
			log("[番号匹配] 扫描目录数达到上限 " + strconv.Itoa(maxDirs) + "，计划不完整")
			return tree, nil
		}
		parentID := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		tree.DirCount++

		items, err := files.List(ctx, accountID, parentID, false)
		if err != nil {
			// 单个目录列举失败不该让整个任务挂掉：记录下来继续扫其余分支，
			// 用户能在日志里看到是哪一层出了问题。
			log("[番号匹配] 列目录失败 " + parentID + "：" + err.Error() + "（跳过该目录）")
			continue
		}
		if interval > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(interval):
			}
		}
		for _, it := range items {
			name := strings.TrimSpace(it.Name)
			if name == "" {
				continue
			}
			if it.IsDir {
				tree.Dirs = append(tree.Dirs, ScannedDir{ID: it.ID, Name: name, ParentID: parentID})
				if _, dup := seen[it.ID]; !dup {
					seen[it.ID] = struct{}{}
					stack = append(stack, it.ID)
				}
				continue
			}
			tree.Files = append(tree.Files, ScannedFile{
				ID: it.ID, Name: name, ParentID: parentID, Size: it.Size,
			})
		}
	}
	return tree, nil
}

// extOf 取小写扩展名（不含点）。没有扩展名返回空串。
func extOf(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx < 0 || idx == len(name)-1 {
		return ""
	}
	return strings.ToLower(name[idx+1:])
}

// stemOf 取不含扩展名的部分。
func stemOf(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx <= 0 {
		return name
	}
	return name[:idx]
}

// splitExtList 把「分号分隔的扩展名列表」拆成集合，统一小写、去掉前导点。
func splitExtList(raw string, fallback string) map[string]struct{} {
	src := raw
	if strings.TrimSpace(src) == "" {
		src = fallback
	}
	out := map[string]struct{}{}
	for _, part := range strings.FieldsFunc(src, func(r rune) bool {
		return r == ';' || r == ',' || r == '|' || r == ' ' || r == '\n' || r == '\t'
	}) {
		ext := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), ".")))
		if ext != "" {
			out[ext] = struct{}{}
		}
	}
	return out
}

// metadataExtSet 元数据扩展名的默认值。与 115-auto 的 META_EXTS 逐字节相同 ——
// 这不是巧合，是刻意对齐过的，所以不需要新加一个设置项。
var defaultMetadataExtensions = rules.DefaultMetadataExtensions

func isVideoExt(name string, videoExts map[string]struct{}) bool {
	ext := extOf(name)
	if ext == "" {
		return false
	}
	_, ok := videoExts[ext]
	return ok
}

func isMetaExt(name string, metaExts map[string]struct{}) bool {
	ext := extOf(name)
	if ext == "" {
		return false
	}
	_, ok := metaExts[ext]
	return ok
}
