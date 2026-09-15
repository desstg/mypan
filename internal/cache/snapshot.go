package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"litepan/internal/domain"
)

const snapshotSchemaVersion = 1

type snapshotFile struct {
	SchemaVersion int            `json:"schema_version"`
	SavedAt       time.Time      `json:"saved_at"`
	Items         []snapshotItem `json:"items"`
}

type snapshotItem struct {
	Key       string          `json:"key"`
	ExpiresAt time.Time       `json:"expires_at,omitempty"`
	ValueKind string          `json:"value_kind"`
	Value     json.RawMessage `json:"value"`
}

type fileItemDTO struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Size    int64             `json:"size"`
	IsDir   bool              `json:"is_dir"`
	ModTime string            `json:"mod_time,omitempty"`
	Hash    map[string]string `json:"hash,omitempty"`
	Thumb   string            `json:"thumb,omitempty"`
	IDKind  uint8             `json:"id_kind"`
}

func snapshotPath(dir string) string {
	return filepath.Join(dir, "cache_data.json")
}

// SaveSnapshot 将未过期项原子写入磁盘。
func (s *Service) SaveSnapshot(dir string) error {
	if dir == "" {
		return errors.New("snapshot dir required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	now := time.Now()
	items := s.exportSnapshotItems(now)
	payload := snapshotFile{
		SchemaVersion: snapshotSchemaVersion,
		SavedAt:       now,
		Items:         items,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	target := snapshotPath(dir)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// LoadSnapshot 从磁盘恢复未过期项，返回载入条数。
func (s *Service) LoadSnapshot(dir string) (int, error) {
	if dir == "" {
		return 0, nil
	}
	data, err := os.ReadFile(snapshotPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	var file snapshotFile
	if err := json.Unmarshal(data, &file); err != nil {
		return 0, err
	}
	if file.SchemaVersion != snapshotSchemaVersion {
		return 0, fmt.Errorf("unsupported snapshot schema %d", file.SchemaVersion)
	}

	now := time.Now()
	loaded := 0
	for _, it := range file.Items {
		if !it.ExpiresAt.IsZero() && now.After(it.ExpiresAt) {
			continue
		}
		val, err := decodeSnapshotValue(it.ValueKind, it.Value)
		if err != nil {
			continue
		}
		s.restoreEntry(it.Key, val, it.ExpiresAt)
		loaded++
	}
	return loaded, nil
}

func (s *Service) exportSnapshotItems(now time.Time) []snapshotItem {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]snapshotItem, 0, len(s.items))
	for _, el := range s.items {
		en := el.Value.(*entry)
		if !en.expiresAt.IsZero() && now.After(en.expiresAt) {
			continue
		}
		kind, raw, err := encodeSnapshotValue(en.value)
		if err != nil {
			continue
		}
		out = append(out, snapshotItem{
			Key:       en.key,
			ExpiresAt: en.expiresAt,
			ValueKind: kind,
			Value:     raw,
		})
	}
	return out
}

func (s *Service) restoreEntry(key string, val any, expiresAt time.Time) {
	size := entrySize(key, val)
	s.mu.Lock()
	defer s.mu.Unlock()
	if el, ok := s.items[key]; ok {
		en := el.Value.(*entry)
		s.curMem += size - en.size
		en.value, en.size, en.expiresAt = val, size, expiresAt
		s.ll.MoveToFront(el)
		s.ensureCapacity()
		return
	}
	el := s.ll.PushFront(&entry{key: key, value: val, size: size, expiresAt: expiresAt})
	s.items[key] = el
	s.curMem += size
	s.ensureCapacity()
}

func encodeSnapshotValue(v any) (string, json.RawMessage, error) {
	switch t := v.(type) {
	case []domain.FileItem:
		dtos := make([]fileItemDTO, len(t))
		for i := range t {
			dtos[i] = fileItemToDTO(t[i])
		}
		raw, err := json.Marshal(dtos)
		return "file_list", raw, err
	case *domain.FileItem:
		raw, err := json.Marshal(fileItemToDTO(*t))
		return "file_item", raw, err
	case domain.FileItem:
		raw, err := json.Marshal(fileItemToDTO(t))
		return "file_item", raw, err
	default:
		return "", nil, fmt.Errorf("unsupported snapshot value type %T", v)
	}
}

func decodeSnapshotValue(kind string, raw json.RawMessage) (any, error) {
	switch kind {
	case "file_list":
		var dtos []fileItemDTO
		if err := json.Unmarshal(raw, &dtos); err != nil {
			return nil, err
		}
		items := make([]domain.FileItem, len(dtos))
		for i := range dtos {
			items[i] = dtoToFileItem(dtos[i])
		}
		return items, nil
	case "file_item":
		var dto fileItemDTO
		if err := json.Unmarshal(raw, &dto); err != nil {
			return nil, err
		}
		return dtoToFileItem(dto), nil
	default:
		return nil, fmt.Errorf("unsupported snapshot value kind %q", kind)
	}
}

func fileItemToDTO(f domain.FileItem) fileItemDTO {
	hash := make(map[string]string, len(f.Hash))
	for k, v := range f.Hash {
		hash[string(k)] = v
	}
	dto := fileItemDTO{
		ID: f.ID, Name: f.Name, Size: f.Size, IsDir: f.IsDir,
		Hash: hash, Thumb: f.Thumb, IDKind: uint8(f.IDKind),
	}
	if !f.ModTime.IsZero() {
		dto.ModTime = f.ModTime.UTC().Format(time.RFC3339)
	}
	return dto
}

func dtoToFileItem(dto fileItemDTO) domain.FileItem {
	hash := make(map[domain.HashType]string, len(dto.Hash))
	for k, v := range dto.Hash {
		hash[domain.HashType(k)] = v
	}
	item := domain.FileItem{
		ID: dto.ID, Name: dto.Name, Size: dto.Size, IsDir: dto.IsDir,
		Hash: hash, Thumb: dto.Thumb, IDKind: domain.IDKind(dto.IDKind),
	}
	if dto.ModTime != "" {
		if t, err := time.Parse(time.RFC3339, dto.ModTime); err == nil {
			item.ModTime = t
		}
	}
	return item
}
