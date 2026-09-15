package cloud139

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
)

type apiEnvelope struct {
	Success *bool           `json:"success"`
	Code    flexString      `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type routePolicyData struct {
	RoutePolicyList []routePolicyItem `json:"routePolicyList"`
}

type routePolicyItem struct {
	ModName  string `json:"modName"`
	HTTPSURL string `json:"httpsUrl"`
	HTTPURL  string `json:"httpUrl"`
}

type listData struct {
	Items          []fileEntry `json:"items"`
	NextPageCursor string      `json:"nextPageCursor"`
}

type fileEntry struct {
	FileID        flexString       `json:"fileId"`
	CatalogID     flexString       `json:"catalogId"`
	Name          string           `json:"name"`
	CatalogName   string           `json:"catalogName"`
	Size          json.Number      `json:"size"`
	Type          string           `json:"type"`
	CreatedAt     any              `json:"createdAt"`
	UpdatedAt     any              `json:"updatedAt"`
	ThumbnailURLs []thumbnailEntry `json:"thumbnailUrls"`
	ThumbnailURL  string           `json:"thumbnailURL"`
}

type thumbnailEntry struct {
	URL string `json:"url"`
}

func (e fileEntry) toFileItem() domain.FileItem {
	id := e.FileID.String()
	if id == "" {
		id = e.CatalogID.String()
	}
	name := strings.TrimSpace(e.Name)
	if name == "" {
		name = strings.TrimSpace(e.CatalogName)
	}
	size, _ := e.Size.Int64()
	thumb := strings.TrimSpace(e.ThumbnailURL)
	if thumb == "" {
		for _, entry := range e.ThumbnailURLs {
			if strings.TrimSpace(entry.URL) != "" {
				thumb = strings.TrimSpace(entry.URL)
				break
			}
		}
	}
	return domain.FileItem{
		ID:      id,
		Name:    name,
		Size:    size,
		IsDir:   strings.EqualFold(strings.TrimSpace(e.Type), "folder"),
		ModTime: parseCloudTime(e.UpdatedAt),
		Thumb:   thumb,
		IDKind:  domain.IDStable,
	}
}

func parseCloudTime(raw any) time.Time {
	switch value := raw.(type) {
	case nil:
		return time.Time{}
	case float64:
		return time.UnixMilli(int64(value))
	case json.Number:
		if millis, err := value.Int64(); err == nil {
			return time.UnixMilli(millis)
		}
	case string:
		value = strings.TrimSpace(value)
		if value == "" {
			return time.Time{}
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
			if parsed, err := time.Parse(layout, value); err == nil {
				return parsed
			}
		}
		if millis, err := strconv.ParseInt(value, 10, 64); err == nil {
			return time.UnixMilli(millis)
		}
	}
	return time.Time{}
}

type downloadData struct {
	CDNURL   string      `json:"cdnUrl"`
	URL      string      `json:"url"`
	FileName string      `json:"fileName"`
	Size     json.Number `json:"size"`
}

type createFolderData struct {
	FileID flexString `json:"fileId"`
	Name   string     `json:"name"`
}

type uploadPartSpec struct {
	ParallelHashCtx struct {
		PartOffset int64 `json:"partOffset"`
	} `json:"parallelHashCtx"`
	PartNumber int   `json:"partNumber"`
	PartSize   int64 `json:"partSize"`
}

type uploadPartURL struct {
	PartNumber int    `json:"partNumber"`
	UploadURL  string `json:"uploadUrl"`
}

type uploadCreateData struct {
	FileID      flexString      `json:"fileId"`
	FileName    string          `json:"fileName"`
	UploadID    string          `json:"uploadId"`
	PartInfos   []uploadPartURL `json:"partInfos"`
	RapidUpload bool            `json:"rapidUpload"`
	Exist       bool            `json:"exist"`
}

type uploadURLsData struct {
	PartInfos []uploadPartURL `json:"partInfos"`
}

type refreshTokenResponse struct {
	Return      string `xml:"return"`
	Token       string `xml:"token"`
	AccessToken string `xml:"accessToken"`
	ExpireTime  string `xml:"expiretime"`
	Desc        string `xml:"desc"`
}
