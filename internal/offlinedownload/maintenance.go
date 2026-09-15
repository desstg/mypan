package offlinedownload

import (
	"context"
	"path/filepath"
)

// BuiltinTempRoots 返回当前服务已知的内置离线临时目录快照。
// 配置切换后，仍被历史任务引用的旧目录也会保留在结果中。
func (s *Service) BuiltinTempRoots() []string {
	if s == nil {
		return nil
	}
	roots := s.builtinRootSnapshot()
	for i := range roots {
		roots[i] = filepath.Clean(roots[i])
	}
	return roots
}

// ActiveBuiltinTempPaths 返回离线下载或未完成交棒上传仍在使用的任务目录快照。
func (s *Service) ActiveBuiltinTempPaths(ctx context.Context) []string {
	if s == nil {
		return nil
	}
	active := s.activeBuiltinTempPaths(ctx)
	out := make([]string, 0, len(active))
	for path := range active {
		out = append(out, filepath.Clean(path))
	}
	return out
}
