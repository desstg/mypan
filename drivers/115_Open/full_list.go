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
		// 目录已经被删/不可见。返回 NotFound 而不是 ("", nil)：
		// 空路径会被上层当成「在根目录」，于是把整份 STRM 写到根上去。
		return "", domain.Errf(domain.CodeNotFound)
	}
	path, ok := buildDirPath(info.Paths, info.entryName(), d.rootID())
	if !ok {
		// 父链里找不到账号的挂载根：说明这个目录不在本账号的根之下（权限/缓存异常）。
		// 拼出来的路径会缺前几层，宁可报错也不要写出错位的 STRM。
		return "", domain.Errorf(domain.CodeDriverError, "115 目录不在账号挂载根下，已停止扫描")
	}
	return path, nil
}

// buildDirPath 把 get_info 的父目录链（不含自身）与目录自身名称拼成完整路径。
//
// rootID 用来定位「账号挂载根」这一层：父链里可能有根以外的段（例如移动后的残留），
// 只有碰到 rootID 之后的段才算属于本账号的相对路径。rootID 为 "0" 时按整条链算
// （见 foundRoot 的初始化）。返回 ok=false 表示父链里根本没出现挂载根。
func buildDirPath(paths []dirPathEntry, selfName, rootID string) (string, bool) {
	segs := make([]string, 0, len(paths)+1)
	// rootID 为 "0" 表示账号根就是 115 的根，此时整条父链都属于本账号。
	foundRoot := rootID == "0"
	for _, p := range paths {
		id := strings.TrimSpace(p.FileID.String())
		if id == rootID {
			// 遇到挂载根：它自己不算路径段，且**清空**之前收集的段
			// （那些在根之上，不属于本账号）。
			foundRoot = true
			segs = segs[:0]
			continue
		}
		if id == "0" || !foundRoot {
			continue
		}
		if name := pathSegmentName(p.FileName); name != "" {
			segs = append(segs, name)
		}
	}
	if !foundRoot {
		return "", false
	}
	if name := pathSegmentName(selfName); name != "" {
		segs = append(segs, name)
	}
	return strings.Join(segs, "/"), true
}

// pathSegmentName 把目录名里会被误当成层级的分隔符替换掉。
//
// 115 **允许**目录名里出现 "/"。直接拿名字拼路径，一个目录就会被读成好几层：
// STRM 增强扫描会往这些并不存在的层级里写 .strm，随后又把正确位置的那份当重复删掉。
// 换成下划线是保真度最高的做法 —— 名字仍然可读，且不会再被当成路径分隔符。
func pathSegmentName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "/", "_")
	return strings.ReplaceAll(name, "\\", "_")
}

var (
	_ driver.FullListLister = (*Driver)(nil)
)
