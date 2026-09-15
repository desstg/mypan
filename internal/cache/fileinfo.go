package cache

import "litepan/internal/domain"

func FileInfoFromCache(raw any) (FileInfo, bool) {
	switch v := raw.(type) {
	case FileInfo:
		return v, true
	case *domain.FileItem:
		if v == nil {
			return FileInfo{}, false
		}
		return *v, true
	default:
		return FileInfo{}, false
	}
}

func coerceFileInfo[T any](raw any) (T, bool) {
	var zero T
	if _, ok := any(zero).(FileInfo); !ok {
		return zero, false
	}
	item, ok := FileInfoFromCache(raw)
	if !ok {
		return zero, false
	}
	out, ok := any(item).(T)
	return out, ok
}
