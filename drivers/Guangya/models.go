package guangya

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/pkg/strutil"
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

// ——————————————————— 分享链（见 share.go） ———————————————————

// sharePageData 是 get_share_page_files_list 的 data。
type sharePageData struct {
	List  []shareFileEntry `json:"list"`
	Total int              `json:"total"`
}

// shareFileEntry 是分享里的一条条目。
//
// ⚠️ **分享侧的字段名与自己的文件列表不是同一套**（`fileEntry` 那套 fileId /
// fileName / resType 在这里解出来是空的，真机 2026-10-12 实测）。所以这里为
// 每个字段留了几个候选名，`pick` 取第一个非空的。
// 真机跑通之后应当把没命中的候选删掉，只留实测确认的那个 —— 留着会让
// 「接口又改字段名」时的排查比现在更难（哪个才是真的？）。
type shareFileEntry struct {
	FileID   string `json:"fileId"`
	FileID2  string `json:"fid"`
	FileID3  string `json:"id"`
	FileName string `json:"fileName"`
	Name     string `json:"name"`
	ResType  int    `json:"resType"`
	FileType int    `json:"fileType"`
	Type     int    `json:"type"`
	IsDir    bool   `json:"isDir"`
	Dir      bool   `json:"dir"`
	FileSize int64  `json:"fileSize"`
	Size     int64  `json:"size"`
	// raw 保留原始对象，供排查用（字段名全没命中时，打印它就知道服务端叫什么）。
	raw map[string]any
}

func (e *shareFileEntry) UnmarshalJSON(data []byte) error {
	// 先原样收下来，再按候选名逐个取 —— 这样加字段不用改两次。
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	e.raw = m
	e.FileID = pickString(m, "fileId", "fid", "id", "file_id")
	e.FileName = pickString(m, "fileName", "name", "file_name", "title")
	e.ResType = int(pickNumber(m, "resType", "fileType", "type"))
	e.FileSize = int64(pickNumber(m, "fileSize", "size"))
	e.IsDir = pickBool(m, "isDir", "dir") || e.ResType == 2 || e.Type == 2
	return nil
}

func (e shareFileEntry) id() string        { return strings.TrimSpace(e.FileID) }
func (e shareFileEntry) name() string      { return strings.TrimSpace(e.FileName) }
func (e shareFileEntry) isDirectory() bool { return e.IsDir }

// pickString / pickNumber / pickBool 从原始对象里按候选键名逐个取。
func pickString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case json.Number:
			return v.String()
		case float64:
			return strconv.FormatInt(int64(v), 10)
		}
	}
	return ""
}

func pickNumber(m map[string]any, keys ...string) float64 {
	for _, k := range keys {
		switch v := m[k].(type) {
		case float64:
			return v
		case json.Number:
			if f, err := v.Float64(); err == nil {
				return f
			}
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				return f
			}
		}
	}
	return 0
}

func pickBool(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if b, ok := m[k].(bool); ok {
			return b
		}
	}
	return false
}

// fields 把原始对象的键名排好序返回 —— 字段全没命中时用它打日志，
// 一眼就能看出服务端实际用的是什么名字。
func (e shareFileEntry) fields() []string {
	out := make([]string, 0, len(e.raw))
	for k := range e.raw {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// describe 把原始对象按「键=值」打出来（值截断），用于排查。
func (e shareFileEntry) describe() string {
	parts := make([]string, 0, len(e.raw))
	for _, k := range e.fields() {
		parts = append(parts, fmt.Sprintf("%s=%v", k, e.raw[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// shareAccessTokenData 是 get_share_access_token 的 data。
//
// ⚠️ 响应里的具体字段名是**推断**的（第三方封装只写「获取分享链接的访问令牌」，
// 没给响应结构）。所以这里用 flexibleString 收几种可能的名字，
// 拿到空值时由调用方回一句「接口结构可能已变」，而不是静默失败。
type shareAccessTokenData struct {
	AccessToken  flexString `json:"accessToken"`
	AccessToken2 flexString `json:"access_token"`
	Token        flexString `json:"token"`
}

func (s shareAccessTokenData) value() string {
	for _, v := range []string{s.AccessToken.String(), s.AccessToken2.String(), s.Token.String()} {
		if v != "" {
			return v
		}
	}
	return ""
}

// shareSummaryData 是 get_share_summary 的 data。
//
// 同一条说明：字段名按常见的分享概要结构推断，多写几个候选名。
type shareSummaryData struct {
	ShareID   flexString `json:"shareId"`
	Title     string     `json:"title"`
	Name      string     `json:"name"`
	FileName  string     `json:"fileName"`
	FileCount int        `json:"fileCount"`
	FileNum   int        `json:"fileNum"`
	FileSize  int64      `json:"fileSize"`
	TotalSize int64      `json:"totalSize"`
	Size      int64      `json:"size"`
}

func (s shareSummaryData) displayTitle() string {
	return strings.TrimSpace(strutil.FirstNonEmpty(s.Title, s.Name, s.FileName))
}

func (s shareSummaryData) count() int {
	if s.FileCount > 0 {
		return s.FileCount
	}
	return s.FileNum
}

func (s shareSummaryData) totalSize() int64 {
	if s.FileSize > 0 {
		return s.FileSize
	}
	if s.TotalSize > 0 {
		return s.TotalSize
	}
	return s.Size
}
