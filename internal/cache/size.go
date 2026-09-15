package cache

import (
	"encoding/json"

	"litepan/internal/domain"
)

const (
	entryStructOverhead = int64(48)
	cacheHeapFudgeNum   = 110
	cacheHeapFudgeDen   = 100
)

// entrySize 在写入时估算单条缓存占用：payload + key/entry 壳子，再乘堆上浮系数。
func entrySize(key string, val any) int64 {
	payload := estimatePayload(val)
	base := payload + entryStructOverhead + int64(len(key))
	return base * cacheHeapFudgeNum / cacheHeapFudgeDen
}

func estimatePayload(v any) int64 {
	switch t := v.(type) {
	case []domain.FileItem:
		return fileListPayload(t)
	case *domain.FileItem:
		if t == nil {
			return 0
		}
		return fileItemPayload(*t)
	case domain.FileItem:
		return fileItemPayload(t)
	case *domain.DownloadInfo:
		if t == nil {
			return 0
		}
		return downloadInfoPayload(*t)
	case domain.DownloadInfo:
		return downloadInfoPayload(t)
	case PathMapEntry:
		return pathMapPayload(t)
	case []byte:
		return byteSlicePayload(t)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return 256
		}
		return int64(len(raw))
	}
}

func fileListPayload(list []domain.FileItem) int64 {
	if len(list) == 0 {
		return 2
	}
	_, raw, err := encodeSnapshotValue(list)
	if err != nil {
		return int64(len(list)) * 128
	}
	return int64(len(raw))
}

func fileItemPayload(f domain.FileItem) int64 {
	_, raw, err := encodeSnapshotValue(f)
	if err != nil {
		return 128
	}
	return int64(len(raw))
}

func pathMapPayload(ent PathMapEntry) int64 {
	raw, err := json.Marshal(struct {
		Item     fileItemDTO `json:"item"`
		ParentID string      `json:"parent_id"`
	}{
		Item:     fileItemToDTO(ent.Item),
		ParentID: ent.ParentID,
	})
	if err != nil {
		return fileItemPayload(ent.Item) + int64(len(ent.ParentID)) + 32
	}
	return int64(len(raw))
}

func downloadInfoPayload(info domain.DownloadInfo) int64 {
	raw, err := json.Marshal(info)
	if err != nil {
		return int64(len(info.URL)) + 128
	}
	return int64(len(raw))
}

func byteSlicePayload(b []byte) int64 {
	return 24 + int64(len(b))
}
