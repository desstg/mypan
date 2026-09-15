package guangya

import (
	"encoding/json"
	"strings"
	"time"

	"litepan/internal/domain"
)

type apiEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type listData struct {
	List  []fileEntry `json:"list"`
	Total int         `json:"total"`
}

type fileEntry struct {
	FileID        string `json:"fileId"`
	ParentID      string `json:"parentId"`
	FileName      string `json:"fileName"`
	FileSize      int64  `json:"fileSize"`
	ResType       int    `json:"resType"`
	CTime         int64  `json:"ctime"`
	UTime         int64  `json:"utime"`
	Depth         int    `json:"depth"`
	DirType       int    `json:"dirType"`
	AuditStatus   int    `json:"auditStatus"`
	MD5           string `json:"md5"`
	Gcid          string `json:"gcid"` // 光鸭内容 ID，非 SHA1/MD5，不得写入 FileItem.Hash
	Ext           string `json:"ext"`
	FileType      int    `json:"fileType"`
	MimeType      string `json:"mineType"`
	FullParentIDs string `json:"fullParentIds"`
}

func (e fileEntry) toFileItem() domain.FileItem {
	item := domain.FileItem{
		ID:     e.FileID,
		Name:   e.FileName,
		Size:   e.FileSize,
		IsDir:  e.ResType == 2,
		IDKind: domain.IDStable,
	}
	if e.UTime > 0 {
		item.ModTime = time.Unix(e.UTime, 0)
	}
	if md5 := strings.TrimSpace(e.MD5); md5 != "" {
		item.Hash = map[domain.HashType]string{domain.HashMD5: md5}
	}
	return item
}

type fileDetailData struct {
	FileInfo fileEntry `json:"fileInfo"`
}

type downloadData struct {
	SignedURL        string `json:"signedURL"`
	DownloadURL      string `json:"downloadUrl"`
	URLDuration      int64  `json:"urlDuration"`
	SpeedupSignature string `json:"speedupSignature"`
	RequestID        string `json:"requestId"`
}

func (d downloadData) linkExpiration() time.Duration {
	if d.URLDuration <= 0 {
		return 0
	}
	return time.Duration(d.URLDuration) * time.Second
}

type taskData struct {
	TaskID string `json:"taskId"`
}

type taskStatusData struct {
	Status int `json:"status"`
}

type uploadTokenData struct {
	TaskID          string         `json:"taskId"`
	ObjectPath      string         `json:"objectPath"`
	BucketName      string         `json:"bucketName"`
	EndPoint        string         `json:"endPoint"`
	FullEndPoint    string         `json:"fullEndPoint"`
	AccessKeyID     string         `json:"accessKeyID"`
	SecretAccessKey string         `json:"secretAccessKey"`
	SessionToken    string         `json:"sessionToken"`
	Creds           map[string]any `json:"creds"`
}

func (t *uploadTokenData) normalize() {
	if t == nil {
		return
	}
	if creds, ok := t.Creds["accessKeyID"].(string); ok && t.AccessKeyID == "" {
		t.AccessKeyID = creds
	}
	if creds, ok := t.Creds["secretAccessKey"].(string); ok && t.SecretAccessKey == "" {
		t.SecretAccessKey = creds
	}
	if creds, ok := t.Creds["sessionToken"].(string); ok && t.SessionToken == "" {
		t.SessionToken = creds
	}
	if t.EndPoint == "" {
		t.EndPoint = t.FullEndPoint
	}
}

type uploadTaskInfoData struct {
	FileID string `json:"fileId"`
}

type flashUploadData struct {
	CanFlashUpload bool `json:"canFlashUpload"`
}

type createDirData struct {
	FileID   string `json:"fileId"`
	FileName string `json:"fileName"`
	ResType  int    `json:"resType"`
	CTime    int64  `json:"ctime"`
	UTime    int64  `json:"utime"`
}
