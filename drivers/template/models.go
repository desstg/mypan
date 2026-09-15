package template

import "litepan/internal/domain"

type listData struct {
	Items []fileEntry `json:"items"`
}

type fileEntry struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

func (e fileEntry) toDomain() domain.FileItem {
	return domain.FileItem{
		ID:    e.ID,
		Name:  e.Name,
		Size:  e.Size,
		IsDir: e.IsDir,
	}
}

func mapAPIError(code int, msg string) error {
	switch code {
	case 401:
		return domain.Errorf(domain.CodeAuthExpired, "平台认证失败：%s", msg)
	case 429:
		return domain.Errorf(domain.CodeRateLimited, "平台接口限流：%s", msg)
	case 404:
		return domain.Errf(domain.CodeNotFound)
	default:
		return domain.Errorf(domain.CodeDriverError, "平台 API 错误(%d)：%s", code, msg)
	}
}
