package pan123open

import (
	"encoding/json"
	"time"

	"litepan/internal/domain"
)

// apiEnvelope 是 123 开放平台统一响应外壳。
type apiEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type listResp struct {
	LastFileID json.Number `json:"lastFileId"`
	FileList   []fileEntry `json:"fileList"`
}

type fileEntry struct {
	FileID        json.Number `json:"fileId"`
	FileID2       json.Number `json:"fileID"`
	Filename      string      `json:"filename"`
	Type          int         `json:"type"` // 0 文件 / 1 文件夹
	Size          int64       `json:"size"`
	Etag          string      `json:"etag"`
	Trashed       int         `json:"trashed"`
	UpdateAt      string      `json:"updateAt"`
	CreateAt      string      `json:"createAt"`
	ParentFileID  json.Number `json:"parentFileId"`
	ParentFileID2 json.Number `json:"parentFileID"`
}

func (e fileEntry) parentID() string {
	if s := e.ParentFileID2.String(); s != "" && s != "0" {
		return s
	}
	return e.ParentFileID.String()
}

func (e fileEntry) entryID() string {
	if s := e.FileID2.String(); s != "" && s != "0" {
		return s
	}
	return e.FileID.String()
}

func (e fileEntry) toFileItem() domain.FileItem {
	item := domain.FileItem{
		ID:     e.entryID(),
		Name:   e.Filename,
		Size:   e.Size,
		IsDir:  e.Type == 1,
		IDKind: domain.IDStable,
	}
	if e.Etag != "" {
		item.Hash = map[domain.HashType]string{domain.HashMD5: e.Etag}
	}
	for _, ts := range []string{e.UpdateAt, e.CreateAt} {
		if t, err := time.ParseInLocation(timeLayout, ts, time.Local); err == nil {
			item.ModTime = t
			break
		}
	}
	return item
}

func parseFileDetail(data json.RawMessage) (fileEntry, error) {
	var e fileEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return fileEntry{}, err
	}
	if e.entryID() != "" {
		return e, nil
	}
	var wrap struct {
		FileInfo fileEntry `json:"fileInfo"`
		File     fileEntry `json:"file"`
	}
	if err := json.Unmarshal(data, &wrap); err != nil {
		return e, nil
	}
	if wrap.FileInfo.entryID() != "" {
		return wrap.FileInfo, nil
	}
	return wrap.File, nil
}
