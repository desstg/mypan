package pan115open

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"litepan/internal/domain"
)

type listPageResp struct {
	Count int64       `json:"count"`
	Data  []fileEntry `json:"data"`
}

type dirPathEntry struct {
	FileID   flexString `json:"file_id"`
	FileName string     `json:"file_name"`
}

type fileEntry struct {
	Fid          string         `json:"fid"`
	FileID       string         `json:"file_id"`
	Fn           string         `json:"fn"`
	FileName     string         `json:"file_name"`
	Fc           flexString     `json:"fc"`
	FileCategory flexString     `json:"file_category"`
	Aid          flexString     `json:"aid"`
	Pid          string         `json:"pid"`
	Cid          string         `json:"cid"`
	ParentID     string         `json:"parent_id"`
	Pc           string         `json:"pc"`
	PickCode     string         `json:"pick_code"`
	Pickcode     string         `json:"pickcode"`
	Code         string         `json:"code"`
	Sha1         string         `json:"sha1"`
	SizeByte     flexNumber     `json:"size_byte"`
	S            flexNumber     `json:"s"`
	FS           flexNumber     `json:"fs"`
	Size         flexNumber     `json:"size"`
	Thumb        string         `json:"thumb"`
	Thumbnail    string         `json:"thumbnail"`
	T            flexNumber     `json:"t"`
	Upt          flexNumber     `json:"upt"`
	Uetime       flexNumber     `json:"uet"`
	Uppt         flexNumber     `json:"uppt"`
	Utime        string         `json:"utime"`
	Ptime        string         `json:"ptime"`
	Paths        []dirPathEntry `json:"paths"`
}

type mkdirResp struct {
	Cid      string `json:"cid"`
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
}

type recycleListResp struct {
	Count  json.Number            `json:"count"`
	Offset json.Number            `json:"offset"`
	Limit  json.Number            `json:"limit"`
	RbPass json.Number            `json:"rb_pass"`
	Files  map[string]recycleFile `json:"-"`
}

type recycleFile struct {
	ID         string      `json:"id"`
	FileName   string      `json:"file_name"`
	Dtime      json.Number `json:"dtime"`
	DeleteTime json.Number `json:"delete_time"`
}

func (e fileEntry) entryID() string {
	if s := strings.TrimSpace(e.Fid); s != "" {
		return s
	}
	return strings.TrimSpace(e.FileID)
}

func (e fileEntry) entryName() string {
	if s := strings.TrimSpace(e.Fn); s != "" {
		return s
	}
	return strings.TrimSpace(e.FileName)
}

func (e fileEntry) isDirectory() bool {
	if s := e.FileCategory.String(); s != "" {
		return s == "0"
	}
	return e.Fc.String() == "0"
}

func (e fileEntry) entrySize() int64 {
	for _, n := range []flexNumber{e.FS, e.SizeByte, e.S, e.Size} {
		if v := n.int64(); v > 0 {
			return v
		}
	}
	return 0
}

func (e fileEntry) pickCode() string {
	for _, s := range []string{e.Pc, e.PickCode, e.Pickcode, e.Code} {
		if v := strings.TrimSpace(s); v != "" {
			return v
		}
	}
	return ""
}

func (e fileEntry) parentID() string {
	for _, s := range []string{e.Pid, e.ParentID, e.Cid} {
		if v := strings.TrimSpace(s); v != "" {
			return v
		}
	}
	return ""
}

func (e fileEntry) entryMTime() int64 {
	if v := e.Uppt.int64(); v > 0 {
		return v
	}
	if v := e.Upt.int64(); v > 0 {
		return v
	}
	return 0
}

func (e *fileEntry) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '[' {
		var arr []fileEntry
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		if len(arr) == 0 {
			return domain.Errf(domain.CodeNotFound)
		}
		*e = arr[0]
		return nil
	}
	type alias fileEntry
	return json.Unmarshal(data, (*alias)(e))
}

func fileToItem(e fileEntry) domain.FileItem {
	item := domain.FileItem{
		ID:     e.entryID(),
		Name:   e.entryName(),
		Size:   e.entrySize(),
		IsDir:  e.isDirectory(),
		IDKind: domain.IDStable,
	}
	if sha1 := strings.TrimSpace(e.Sha1); sha1 != "" {
		item.Hash = map[domain.HashType]string{domain.HashSHA1: sha1}
	}
	thumb := strings.TrimSpace(e.Thumb)
	if thumb == "" {
		thumb = strings.TrimSpace(e.Thumbnail)
	}
	if thumb != "" {
		item.Thumb = thumb
	}
	if ts := entryModTime(e); !ts.IsZero() {
		item.ModTime = ts
	}
	return item
}

func entryModTime(e fileEntry) time.Time {
	for _, n := range []flexNumber{e.Upt, e.T, e.Uetime, e.Uppt} {
		if v := n.int64(); v > 0 {
			return time.Unix(v, 0)
		}
	}
	for _, raw := range []string{e.Utime, e.Ptime} {
		if ts := parseTimeText(raw); !ts.IsZero() {
			return ts
		}
	}
	return time.Time{}
}

func parseTimeText(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" {
		return time.Time{}
	}
	if ts, err := time.ParseInLocation("2006-01-02 15:04:05", raw, time.Local); err == nil {
		return ts
	}
	return time.Time{}
}

func isTrashed(e fileEntry) bool {
	switch e.Aid.String() {
	case "7", "120":
		return true
	default:
		return false
	}
}

func (r *recycleListResp) UnmarshalJSON(data []byte) error {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	payload := data
	if len(bytes.TrimSpace(envelope.Data)) > 0 {
		payload = envelope.Data
	}
	return r.parseRecyclePayload(payload)
}

func (r *recycleListResp) parseRecyclePayload(payload []byte) error {
	var meta struct {
		Count  json.Number `json:"count"`
		Offset json.Number `json:"offset"`
		Limit  json.Number `json:"limit"`
		RbPass json.Number `json:"rb_pass"`
	}
	if err := json.Unmarshal(payload, &meta); err != nil {
		return err
	}
	r.Count = meta.Count
	r.Offset = meta.Offset
	r.Limit = meta.Limit
	r.RbPass = meta.RbPass

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil {
		return err
	}
	skip := map[string]struct{}{
		"count": {}, "offset": {}, "limit": {}, "rb_pass": {},
	}
	files := make(map[string]recycleFile)
	for key, val := range raw {
		if _, ok := skip[key]; ok {
			continue
		}
		var f recycleFile
		if json.Unmarshal(val, &f) != nil {
			continue
		}
		files[key] = f
	}
	r.Files = files
	return nil
}

func recycleDeleteTime(f recycleFile) int64 {
	for _, n := range []json.Number{f.DeleteTime, f.Dtime} {
		if s := strings.TrimSpace(n.String()); s != "" && s != "0" {
			if v, err := n.Int64(); err == nil {
				return v
			}
		}
	}
	return 0
}
