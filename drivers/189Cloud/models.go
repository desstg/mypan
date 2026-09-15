package cloud189

import (
	"encoding/json"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

type oauthRefreshResp struct {
	ResCode      json.RawMessage `json:"res_code"`
	ResMessage   string          `json:"res_message"`
	AccessToken  string          `json:"accessToken"`
	AccessToken2 string          `json:"access_token"`
	RefreshToken string          `json:"refreshToken"`
	ExpiresIn    json.Number     `json:"expiresIn"`
	ExpiresIn2   json.Number     `json:"expires_in"`
	Expires      json.Number     `json:"expires"`
}

type sessionResp struct {
	ResCode             json.RawMessage `json:"res_code"`
	ResMessage          string          `json:"res_message"`
	AccessToken         string          `json:"accessToken"`
	SessionKey          string          `json:"sessionKey"`
	SessionSecret       string          `json:"sessionSecret"`
	FamilySessionKey    string          `json:"familySessionKey"`
	FamilySessionSecret string          `json:"familySessionSecret"`
	LoginName           string          `json:"loginName"`
	RefreshToken        string          `json:"refreshToken"`
}

type familyListResp struct {
	FamilyInfoResp []familyInfo `json:"familyInfoResp"`
}

type familyInfo struct {
	FamilyID   flexString `json:"familyId"`
	RemarkName string     `json:"remarkName"`
	UseFlag    int        `json:"useFlag"`
}

type listResp struct {
	ResCode    json.RawMessage `json:"res_code"`
	ResMessage string          `json:"res_message"`
	Code       string          `json:"code"`
	Message    string          `json:"message"`
	FileListAO struct {
		FolderList []fileEntry `json:"folderList"`
		FileList   []fileEntry `json:"fileList"`
	} `json:"fileListAO"`
}

type fileInfoResp struct {
	ResCode         json.RawMessage `json:"res_code"`
	ResMessage      string          `json:"res_message"`
	ID              json.Number     `json:"id"`
	Name            string          `json:"name"`
	Size            json.Number     `json:"size"`
	ParentID        json.Number     `json:"parentId"`
	FilePath        string          `json:"filePath"`
	MD5             string          `json:"md5"`
	Icon            json.RawMessage `json:"icon"`
	LastOpTime      any             `json:"lastOpTime"`
	LastOpTimeStr   string          `json:"lastOpTimeStr"`
	CreateDate      any             `json:"createDate"`
	FileDownloadURL string          `json:"fileDownloadUrl"`
	File            *fileInfoResp   `json:"file"`
	FileData        *fileInfoResp   `json:"fileData"`
	UserFile        *fileInfoResp   `json:"userFile"`
}

func (r fileInfoResp) size() int64 {
	v, _ := r.Size.Int64()
	return v
}

func (r *fileInfoResp) contentMD5() string {
	if r == nil {
		return ""
	}
	for _, value := range []string{r.MD5, nested189MD5(r.File), nested189MD5(r.FileData), nested189MD5(r.UserFile)} {
		if md5 := driver.NormalizeTransferHash("md5", value); md5 != "" {
			return md5
		}
	}
	return ""
}

func nested189MD5(info *fileInfoResp) string {
	if info == nil {
		return ""
	}
	return info.MD5
}

type fileEntry struct {
	ID             json.Number     `json:"id"`
	FileID         json.Number     `json:"fileId"`
	FolderID       json.Number     `json:"folderId"`
	Name           string          `json:"name"`
	FileName       string          `json:"fileName"`
	FolderName     string          `json:"folderName"`
	Size           json.Number     `json:"size"`
	ParentID       json.Number     `json:"parentId"`
	ParentFolderID json.Number     `json:"parentFolderId"`
	LastOpTime     any             `json:"lastOpTime"`
	LastOpDate     any             `json:"lastOpDate"`
	ModifyDate     any             `json:"modifyDate"`
	CreateDate     any             `json:"createDate"`
	CreateTime     any             `json:"createTime"`
	MD5            string          `json:"md5"`
	Icon           json.RawMessage `json:"icon"`
	isDir          bool
}

func (e fileEntry) entryID() string {
	for _, n := range []json.Number{e.ID, e.FileID, e.FolderID} {
		if s := n.String(); s != "" {
			return s
		}
	}
	return ""
}

func (e fileEntry) entryName() string {
	for _, s := range []string{e.Name, e.FileName, e.FolderName} {
		if strings.TrimSpace(s) != "" {
			return normalize189Name(s)
		}
	}
	return ""
}

func normalize189Name(raw string) string {
	return strings.ReplaceAll(strings.TrimSpace(raw), `\'`, `'`)
}

func (e fileEntry) size() int64 {
	if e.isDir {
		return 0
	}
	v, _ := e.Size.Int64()
	return v
}

func (e fileEntry) toFileItem() domain.FileItem {
	item := domain.FileItem{
		ID:      e.entryID(),
		Name:    e.entryName(),
		Size:    e.size(),
		IsDir:   e.isDir,
		ModTime: parse189Time(firstNonNil(e.LastOpTime, e.LastOpDate, e.ModifyDate, e.CreateDate, e.CreateTime)),
		IDKind:  domain.IDStable,
	}
	if md5 := strings.TrimSpace(e.MD5); md5 != "" {
		item.Hash = map[domain.HashType]string{domain.HashMD5: strings.ToLower(md5)}
	}
	if thumb := parseThumb(e.Icon); thumb != "" {
		item.Thumb = thumb
	}
	return item
}

func firstNonNil(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func parseThumb(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var icon struct {
		SmallURL  string `json:"smallUrl"`
		MediumURL string `json:"mediumUrl"`
		Max600    string `json:"max600"`
	}
	if err := json.Unmarshal(raw, &icon); err != nil {
		return ""
	}
	for _, s := range []string{icon.SmallURL, icon.MediumURL, icon.Max600} {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func parse189Time(v any) time.Time {
	switch x := v.(type) {
	case nil:
		return time.Time{}
	case float64:
		return parseEpoch189(int64(x))
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return parseEpoch189(n)
		}
	case string:
		text := strings.TrimSpace(x)
		if text == "" {
			return time.Time{}
		}
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", text, time.FixedZone("CST", 8*3600)); err == nil {
			return t.UTC()
		}
		if t, err := time.ParseInLocation("Jan 02, 2006 03:04:05 PM", text, time.FixedZone("CST", 8*3600)); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func parseEpoch189(v int64) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	if v > 1e12 {
		return time.UnixMilli(v)
	}
	return time.Unix(v, 0)
}
