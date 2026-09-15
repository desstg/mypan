package onedrive

import (
	"strings"
	"time"

	"litepan/internal/domain"
)

type graphItem struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	Size                 int64     `json:"size"`
	Folder               *struct{} `json:"folder"`
	LastModifiedDateTime string    `json:"lastModifiedDateTime"`
	DownloadURL          string    `json:"@microsoft.graph.downloadUrl"`
	ParentReference      struct {
		ID      string `json:"id"`
		DriveID string `json:"driveId"`
	} `json:"parentReference"`
}

func (item graphItem) toFileItem() domain.FileItem {
	return domain.FileItem{
		ID:      strings.TrimSpace(item.ID),
		Name:    strings.TrimSpace(item.Name),
		Size:    item.Size,
		IsDir:   item.Folder != nil,
		ModTime: parseGraphTime(item.LastModifiedDateTime),
		IDKind:  domain.IDStable,
	}
}

func parseGraphTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

type graphList struct {
	Value    []graphItem `json:"value"`
	NextLink string      `json:"@odata.nextLink"`
}

type graphErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type uploadSession struct {
	UploadURL         string   `json:"uploadUrl"`
	NextExpectedRange []string `json:"nextExpectedRanges"`
}
