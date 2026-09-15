package pan115open

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

type Driver struct {
	driver.AuthRefreshControl
	add       Addition
	client    *http.Client
	oauthBase string

	intervalGate driver.RequestIntervalGate
	persist      driver.AuthPersistFunc

	mu      sync.Mutex
	token   string
	refresh string

	pickMu sync.RWMutex
	pickBy map[string]string
}

var config = driver.Config{
	Name:                   "115_open",
	DisplayName:            "115网盘Open",
	Description:            "115网盘官方API接入，支持文件管理、上传下载等功能",
	CardTags:               []string{"官方授权", "OAuth", "支持302", "SHA1"},
	SortOrder:              2,
	AuthLabel:              "OAuth",
	CardColor:              "#22A7F0",
	CardLogo:               "/logos/115.png",
	DefaultRoot:            "0",
	AuthType:               driver.AuthToken,
	SupportsAccountProfile: true,
	OAuthName:              "115网盘Open",
	TokenLifetime:          2 * time.Hour,
	RefreshAdvance:         15 * time.Minute,
	ProvideHashes:          []string{"sha1"},
}

func New() driver.Driver { return &Driver{} }

func init() { driver.Register(New) }

func (d *Driver) Config() driver.Config { return config }

func (d *Driver) GetAddition() any { return &d.add }

func (d *Driver) SetAuthCredentials(creds domain.AuthCredentials) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.token = strings.TrimSpace(creds.AccessToken)
	d.refresh = strings.TrimSpace(creds.RefreshToken)
}

func (d *Driver) SetAuthPersister(fn driver.AuthPersistFunc) { d.persist = fn }

func (d *Driver) SetOAuthServer(baseURL string) { d.oauthBase = baseURL }

func (d *Driver) SetRequestIntervalGate(gate driver.RequestIntervalGate) { d.intervalGate = gate }

func (d *Driver) Init(ctx context.Context) error {
	if d.client == nil {
		d.client = httpx.NewClient(httpx.ClientOptions{Timeout: 30 * time.Second})
	}
	d.mu.Lock()
	token := d.token
	refresh := d.refresh
	d.mu.Unlock()
	if token == "" && refresh == "" {
		token = strings.TrimSpace(d.add.AccessToken)
		refresh = strings.TrimSpace(d.add.RefreshToken)
	}
	if token == "" && refresh == "" {
		return domain.Errorf(domain.CodeValidation, "access_token 与 refresh_token 不能都为空")
	}
	d.mu.Lock()
	d.token = token
	d.refresh = refresh
	d.mu.Unlock()
	if strings.TrimSpace(token) == "" {
		if _, err := d.doRefresh(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) Drop(context.Context) error {
	httpx.CloseClient(d.client)
	return nil
}

func (d *Driver) Ping(ctx context.Context) error {
	query := urlValues(map[string]string{
		"cid":      d.rootID(),
		"limit":    "1",
		"offset":   "0",
		"show_dir": "1",
	})
	var page listPageResp
	return d.apiCallFull(ctx, http.MethodGet, pathList, query, nil, &page)
}

func listPageLimit(pageNum int, remaining int64) int {
	if remaining <= 0 {
		return 0
	}
	cap := listPageFollow
	switch pageNum {
	case 1:
		cap = listPageFirst
	case 2:
		cap = listPageSecond
	}
	if int64(cap) > remaining {
		return int(remaining)
	}
	return cap
}

func (d *Driver) ListFiles(ctx context.Context, parentID string) ([]domain.FileItem, error) {
	parent := d.normalizeParent(parentID)
	var items []domain.FileItem
	offset := 0
	fetched := 0
	totalCount := int64(0)
	pageNum := 0

	for {
		pageNum++
		limit := listPageFirst
		if pageNum > 1 {
			remaining := totalCount - int64(fetched)
			if totalCount > 0 && remaining <= 0 {
				break
			}
			if totalCount > 0 {
				limit = listPageLimit(pageNum, remaining)
			} else {
				limit = listPageLimit(pageNum, int64(listPageFollow))
			}
			if limit <= 0 {
				break
			}
		}

		query := urlValues(map[string]string{
			"cid":      parent,
			"limit":    strconv.Itoa(limit),
			"offset":   strconv.Itoa(offset),
			"show_dir": "1",
		})
		var page listPageResp
		if err := d.apiCallFull(ctx, http.MethodGet, pathList, query, nil, &page); err != nil {
			return nil, err
		}
		if pageNum == 1 {
			totalCount = page.Count
		}
		if len(page.Data) == 0 {
			break
		}
		for _, f := range page.Data {
			if isTrashed(f) {
				continue
			}
			d.rememberPickCode(f)
			items = append(items, fileToItem(f))
		}
		fetched += len(page.Data)
		if totalCount > 0 && int64(fetched) >= totalCount {
			break
		}
		if len(page.Data) < limit {
			break
		}
		offset += len(page.Data)
	}
	return items, nil
}

func (d *Driver) GetFileInfo(ctx context.Context, fileID string) (*domain.FileItem, error) {
	id := strings.TrimSpace(fileID)
	root := d.rootID()
	if id == "" || id == "0" || id == root {
		return &domain.FileItem{
			ID:     root,
			Name:   "根目录",
			IsDir:  true,
			IDKind: domain.IDStable,
		}, nil
	}
	query := urlValues(map[string]string{"file_id": id})
	var info fileEntry
	if err := d.apiCall(ctx, http.MethodGet, pathFileInfo, query, nil, &info); err != nil {
		return nil, err
	}
	if info.entryID() == "" {
		return nil, domain.Errf(domain.CodeNotFound)
	}
	d.rememberPickCode(info)
	item := fileToItem(info)
	return &item, nil
}

var (
	_ driver.Driver                   = (*Driver)(nil)
	_ driver.InfoGetter               = (*Driver)(nil)
	_ driver.Downloader               = (*Driver)(nil)
	_ driver.Deleter                  = (*Driver)(nil)
	_ driver.Mover                    = (*Driver)(nil)
	_ driver.Copier                   = (*Driver)(nil)
	_ driver.Renamer                  = (*Driver)(nil)
	_ driver.FolderCreator            = (*Driver)(nil)
	_ driver.AuthRefresher            = (*Driver)(nil)
	_ driver.AuthCredentialConsumer   = (*Driver)(nil)
	_ driver.AuthPersistConsumer      = (*Driver)(nil)
	_ driver.OAuthConsumer            = (*Driver)(nil)
	_ driver.RequestIntervalConsumer  = (*Driver)(nil)
	_ driver.LocalUploader            = (*Driver)(nil)
	_ driver.OfflineDownloadProvider  = (*Driver)(nil)
	_ driver.OfflineURLDownloader     = (*Driver)(nil)
	_ driver.OfflineTaskRefresher     = (*Driver)(nil)
	_ driver.OfflineTaskDeleter       = (*Driver)(nil)
	_ driver.OfflineTorrentDownloader = (*Driver)(nil)
)
