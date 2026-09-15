package pan115open

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

const fullListPageSize = 1150

// ListAllFiles 使用 cur=0 让服务端递归展开 rootID 下全部文件，分页拉取。
// 该模式不返回文件夹，条目自带 pid，由上层结合 pid→路径 缓存还原目录结构。
// 完整性策略：只以空页作为结束信号，不把 Count 或短页当作可靠终点。115 的 Count 可能因
// 厂商缓存或并发变更暂时偏小；若据此提前停止，会让上层把未扫到的目录误判为已删除。
func (d *Driver) ListAllFiles(ctx context.Context, rootID string) ([]driver.FullListEntry, error) {
	root := d.normalizeParent(rootID)
	return collectFullListPages(ctx, func(ctx context.Context, offset, limit int) (listPageResp, error) {
		query := urlValues(map[string]string{
			"cid":      root,
			"limit":    strconv.Itoa(limit),
			"offset":   strconv.Itoa(offset),
			"show_dir": "0",
			"cur":      "0",
		})
		var page listPageResp
		if err := d.apiCallFull(ctx, http.MethodGet, pathList, query, nil, &page); err != nil {
			return listPageResp{}, err
		}
		return page, nil
	})
}

type fullListPageFetcher func(context.Context, int, int) (listPageResp, error)

// collectFullListPages 独立承载完整性敏感的分页逻辑，便于对短页、错误 Count 和重复页做回归验证。
func collectFullListPages(ctx context.Context, fetch fullListPageFetcher) ([]driver.FullListEntry, error) {
	var entries []driver.FullListEntry
	offset := 0
	seenIDs := make(map[string]struct{})
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := fetch(ctx, offset, fullListPageSize)
		if err != nil {
			return nil, err
		}
		if len(page.Data) == 0 {
			break
		}
		newIDs := 0
		for _, f := range page.Data {
			fileID := strings.TrimSpace(f.entryID())
			if fileID == "" {
				return nil, domain.Errorf(domain.CodeDriverError, "115 全量清单返回了缺少文件 ID 的条目，已停止扫描")
			}
			if _, duplicate := seenIDs[fileID]; duplicate {
				continue
			}
			seenIDs[fileID] = struct{}{}
			newIDs++
			if isTrashed(f) {
				continue
			}
			entries = append(entries, driver.FullListEntry{
				FileID:   fileID,
				ParentID: f.parentID(),
				Name:     f.entryName(),
				Size:     f.entrySize(),
				Sha1:     strings.TrimSpace(f.Sha1),
				PickCode: f.pickCode(),
				MTime:    f.entryMTime(),
			})
		}
		if newIDs == 0 {
			return nil, domain.Errorf(domain.CodeDriverError, "115 全量清单分页重复，已停止扫描以避免使用不完整结果")
		}
		offset += len(page.Data)
	}
	return entries, nil
}

// ResolveDirPath 通过 /open/folder/get_info 拼出目录完整路径。
// 注意：接口返回的 paths 只是“父目录链”（不含目录自身），必须再追加目录自身名称。
func (d *Driver) ResolveDirPath(ctx context.Context, dirID string) (string, error) {
	id := strings.TrimSpace(dirID)
	if id == "" || id == "0" || id == d.rootID() {
		return "", nil
	}
	query := urlValues(map[string]string{"file_id": id})
	var info fileEntry
	if err := d.apiCall(ctx, http.MethodGet, pathFileInfo, query, nil, &info); err != nil {
		return "", err
	}
	if info.entryID() == "" {
		return "", nil
	}
	return buildDirPath(info.Paths, info.entryName()), nil
}

// buildDirPath 把 get_info 的父目录链（不含自身）与目录自身名称拼成完整路径。
// 父链中 file_id 为 0 的根段跳过；结果不含首尾斜杠。
func buildDirPath(paths []dirPathEntry, selfName string) string {
	segs := make([]string, 0, len(paths)+1)
	for _, p := range paths {
		if strings.TrimSpace(p.FileID.String()) == "0" {
			continue
		}
		if name := strings.TrimSpace(p.FileName); name != "" {
			segs = append(segs, name)
		}
	}
	if name := strings.TrimSpace(selfName); name != "" {
		segs = append(segs, name)
	}
	return strings.Join(segs, "/")
}

var (
	_ driver.FullListLister = (*Driver)(nil)
)
