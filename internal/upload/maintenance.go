package upload

import "path/filepath"

// ActiveTempPaths 返回当前上传任务与尚未交棒的 FUSE 写入正在使用的本地临时路径快照。
func (m *Manager) ActiveTempPaths() []string {
	if m == nil {
		return nil
	}
	active := m.activeTempPaths()
	out := make([]string, 0, len(active))
	for path := range active {
		out = append(out, filepath.Clean(path))
	}
	return out
}
