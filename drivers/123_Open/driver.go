package pan123open

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
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
	add          Addition
	client       *http.Client
	uploadClient *http.Client

	oauthBase string

	intervalGate driver.RequestIntervalGate
	persist      driver.AuthPersistFunc

	mu             sync.Mutex
	token          string
	refresh        string
	offlineMissing map[string]int
}

func (d *Driver) SetAuthCredentials(creds domain.AuthCredentials) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.token = strings.TrimSpace(creds.AccessToken)
	d.refresh = strings.TrimSpace(creds.RefreshToken)
}

func (d *Driver) SetAuthPersister(fn driver.AuthPersistFunc) { d.persist = fn }

func (d *Driver) SetOAuthServer(baseURL string) { d.oauthBase = baseURL }

func (d *Driver) SetRequestIntervalGate(gate driver.RequestIntervalGate) { d.intervalGate = gate }

var config = driver.Config{
	Name:                   "123_open",
	DisplayName:            "123云盘 Open",
	Description:            "123云盘官方开放 API 接入",
	CardTags:               []string{"官方授权", "OAuth", "支持302", "支持秒传"},
	SortOrder:              4,
	AuthLabel:              "OAuth",
	CardColor:              "#2563eb",
	CardLogo:               "/logos/123.png",
	DefaultRoot:            "0",
	AuthType:               driver.AuthToken,
	SupportsAccountProfile: true,
	OAuthName:              "123云盘Open",
	TokenLifetime:          30 * 24 * time.Hour,
	RefreshAdvance:         10 * time.Hour,
	ProvideHashes:          []string{"md5"},
	RapidUploadHashes:      []string{"sha1", "md5"},
	UploadConflictPolicies: []string{"rename", "overwrite"},
}

func New() driver.Driver { return &Driver{} }

func init() { driver.Register(New) }

func (d *Driver) Config() driver.Config { return config }

func (d *Driver) GetAddition() any { return &d.add }

func (d *Driver) Init(ctx context.Context) error {
	if d.client == nil {
		d.client = httpx.NewClient(httpx.ClientOptions{Timeout: 30 * time.Second})
	}
	if d.uploadClient == nil {
		d.uploadClient = httpx.NewClient(httpx.ClientOptions{
			Timeout:            180 * time.Second,
			DisableCompression: true,
			DisableKeepAlives:  true,
		})
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
	if d.token == "" {
		if _, err := d.doRefresh(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) Drop(context.Context) error {
	httpx.CloseClient(d.client)
	httpx.CloseClient(d.uploadClient)
	return nil
}

func (d *Driver) Ping(ctx context.Context) error {
	return d.apiCall(ctx, http.MethodGet, pathUserInfo, nil, nil, nil)
}

func (d *Driver) ExplainConnectionError(technical string, saving bool) string {
	prefix := "添加失败"
	if saving {
		prefix = "保存失败"
	}
	lower := strings.ToLower(technical)
	switch {
	case strings.Contains(technical, "安全验证") ||
		strings.Contains(technical, "安全风险") ||
		(strings.Contains(technical, "验证") && strings.Contains(technical, "123")):
		return prefix + "：123 云盘要求进行安全验证，请在同一网络下登录 123 网页版后重试"
	case strings.Contains(lower, "client_id") || strings.Contains(lower, "client_secret"):
		return prefix + "，请检查 Client ID 和 Client Secret 是否正确"
	default:
		return ""
	}
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
	params := url.Values{}
	params.Set("fileID", id)
	var raw json.RawMessage
	if err := d.apiCall(ctx, http.MethodGet, pathFileDetail, params, nil, &raw); err != nil {
		return nil, err
	}
	entry, err := parseFileDetail(raw)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	if entry.entryID() == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "文件详情响应缺少 fileID")
	}
	if entry.Trashed != 0 {
		return nil, domain.Errf(domain.CodeNotFound)
	}
	item := entry.toFileItem()
	return &item, nil
}

// ListFiles 列举目录子项（分页 + 过滤回收站）。
func (d *Driver) ListFiles(ctx context.Context, parentID string) ([]domain.FileItem, error) {
	parent := d.normalizeParent(parentID)
	var items []domain.FileItem
	lastFileID := ""

	for {
		params := url.Values{}
		params.Set("parentFileId", parent)
		params.Set("limit", strconv.Itoa(listLimit))
		if lastFileID != "" && lastFileID != "-1" {
			params.Set("lastFileId", lastFileID)
		}

		var resp listResp
		if err := d.apiCall(ctx, http.MethodGet, pathList, params, nil, &resp); err != nil {
			return nil, err
		}
		for _, e := range resp.FileList {
			if e.Trashed != 0 {
				continue
			}
			items = append(items, e.toFileItem())
		}

		next := resp.LastFileID.String()
		if next == "" || next == "-1" || next == lastFileID {
			break
		}
		lastFileID = next
	}
	return items, nil
}

// 能力断言：明确声明本驱动实现了哪些可选能力（编译期校验 + 文档作用）。
var (
	_ driver.Driver                   = (*Driver)(nil)
	_ driver.InfoGetter               = (*Driver)(nil)
	_ driver.Downloader               = (*Driver)(nil)
	_ driver.Deleter                  = (*Driver)(nil)
	_ driver.Mover                    = (*Driver)(nil)
	_ driver.Copier                   = (*Driver)(nil)
	_ driver.Renamer                  = (*Driver)(nil)
	_ driver.FolderCreator            = (*Driver)(nil)
	_ driver.LocalUploader            = (*Driver)(nil)
	_ driver.OAuthConsumer            = (*Driver)(nil)
	_ driver.AuthRefresher            = (*Driver)(nil)
	_ driver.AuthCredentialConsumer   = (*Driver)(nil)
	_ driver.AuthPersistConsumer      = (*Driver)(nil)
	_ driver.ConnectionErrorExplainer = (*Driver)(nil)
	_ driver.RequestIntervalConsumer  = (*Driver)(nil)
	_ driver.RapidUploader            = (*Driver)(nil)
	_ driver.OfflineDownloadProvider  = (*Driver)(nil)
	_ driver.OfflineURLDownloader     = (*Driver)(nil)
	_ driver.OfflineTaskRefresher     = (*Driver)(nil)
)
