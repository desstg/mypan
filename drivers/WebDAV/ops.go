package webdav

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/studio-b12/gowebdav"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/httpx"
)

const redirectProbeExpiration = time.Minute

func (d *Driver) ResolveDownload(ctx context.Context, req driver.DownloadRequest) (*domain.DownloadInfo, error) {
	c, err := d.ensureClient()
	if err != nil {
		return nil, err
	}
	p := d.normalizePath(req.FileID)
	_ = ctx
	fi, err := c.Stat(p)
	if err != nil {
		return nil, mapError(err)
	}
	if fi.IsDir() {
		return nil, domain.Errorf(domain.CodeValidation, "目录不支持下载")
	}

	url := d.resourceURL(p)
	ua := d.downloadUA(req.UA)
	configuredMode := strings.ToLower(strings.TrimSpace(d.add.DownloadMode))
	if configuredMode == "redirect" {
		if redirectURL, ok := d.resolveAnonymousRedirect(ctx, url, ua); ok {
			return &domain.DownloadInfo{
				URL:        redirectURL,
				Headers:    http.Header{},
				Mode:       domain.DownloadRedirect,
				Expiration: redirectProbeExpiration,
				Size:       fi.Size(),
				FileName:   fi.Name(),
			}, nil
		}
	}
	headers := d.proxyDownloadHeaders(ua)
	return &domain.DownloadInfo{
		URL:        url,
		Headers:    headers,
		Mode:       domain.DownloadProxy,
		Expiration: 8 * time.Hour,
		ForceProxy: true,
		Size:       fi.Size(),
		FileName:   fi.Name(),
	}, nil
}

func basicAuth(user, pw string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pw))
}

func (d *Driver) downloadUA(raw string) string {
	ua := strings.TrimSpace(raw)
	if ua == "" {
		return httpx.DefaultUserAgent
	}
	return ua
}

func (d *Driver) proxyDownloadHeaders(ua string) http.Header {
	return http.Header{
		"Authorization":   []string{"Basic " + basicAuth(d.add.Username, d.add.Password)},
		"User-Agent":      []string{ua},
		"Accept":          []string{"*/*"},
		"Accept-Encoding": []string{"identity"},
		"Connection":      []string{"keep-alive"},
	}
}

func (d *Driver) resolveAnonymousRedirect(ctx context.Context, resourceURL, ua string) (string, bool) {
	client := &http.Client{
		Transport:     buildTransport(d.add),
		Timeout:       secondsOr(d.add.Timeout, defaultTimeout),
		CheckRedirect: webDAVRedirectPolicy,
	}
	redirectURL := d.followRedirectURL(ctx, client, resourceURL, ua, true)
	if redirectURL == "" || redirectURL == resourceURL {
		return "", false
	}
	anonymousURL := d.followRedirectURL(ctx, client, redirectURL, ua, false)
	if anonymousURL == "" {
		return "", false
	}
	return anonymousURL, true
}

func webDAVRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if len(via) == 0 {
		return nil
	}
	prev := via[len(via)-1]
	if prev.URL == nil || req.URL == nil ||
		!strings.EqualFold(prev.URL.Scheme, req.URL.Scheme) ||
		!strings.EqualFold(prev.URL.Host, req.URL.Host) {
		req.Header.Del("Authorization")
		req.Header.Del("Proxy-Authorization")
		req.Header.Del("Cookie")
	}
	return nil
}

func (d *Driver) followRedirectURL(ctx context.Context, client *http.Client, rawURL, ua string, withAuth bool) string {
	if finalURL := d.probeRedirect(ctx, client, http.MethodHead, rawURL, ua, withAuth); finalURL != "" {
		return finalURL
	}
	return d.probeRedirect(ctx, client, http.MethodGet, rawURL, ua, withAuth)
}

func (d *Driver) probeRedirect(ctx context.Context, client *http.Client, method, rawURL, ua string, withAuth bool) string {
	var body io.Reader
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Connection", "keep-alive")
	if method == http.MethodGet {
		req.Header.Set("Range", "bytes=0-0")
	}
	if withAuth {
		req.SetBasicAuth(d.add.Username, d.add.Password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 || resp.Request == nil || resp.Request.URL == nil {
		return ""
	}
	return resp.Request.URL.String()
}

func (d *Driver) CreateFolder(ctx context.Context, parentID, name string) (*domain.FileItem, error) {
	c, err := d.ensureClient()
	if err != nil {
		return nil, err
	}
	folderName := strings.TrimSpace(name)
	if folderName == "" {
		return nil, domain.Errorf(domain.CodeValidation, "文件夹名称不能为空")
	}
	target := d.childPath(parentID, folderName)
	_ = ctx
	// gowebdav 的 Mkdir 会把 405（已存在）改写成 201 误判成功，故先 Stat 探测。
	if _, err := c.Stat(target); err == nil {
		return nil, domain.Errorf(domain.CodeValidation, "目标目录已存在同名文件夹")
	} else if !gowebdav.IsErrNotFound(err) {
		return nil, mapError(err)
	}
	if err := c.Mkdir(target, 0o755); err != nil {
		return nil, mapError(err)
	}
	return &domain.FileItem{
		ID:     target,
		Name:   folderName,
		IsDir:  true,
		IDKind: domain.IDPath,
	}, nil
}

func (d *Driver) DeleteFiles(ctx context.Context, fileIDs []string) error {
	c, err := d.ensureClient()
	if err != nil {
		return err
	}
	for _, id := range fileIDs {
		p := d.normalizePath(id)
		if p == d.rootPath() {
			return domain.Errorf(domain.CodeValidation, "根目录不支持删除")
		}
		_ = ctx
		if err := c.RemoveAll(p); err != nil {
			return mapError(err)
		}
	}
	return nil
}

func (d *Driver) MoveFiles(ctx context.Context, fileIDs []string, targetParentID, _ string) error {
	c, err := d.ensureClient()
	if err != nil {
		return err
	}
	target := d.normalizePath(targetParentID)
	for _, id := range fileIDs {
		src := d.normalizePath(id)
		if src == d.rootPath() {
			return domain.Errorf(domain.CodeValidation, "根目录不支持移动")
		}
		dst := d.childPath(target, baseName(src))
		_ = ctx
		if err := c.Rename(src, dst, true); err != nil {
			return mapError(err)
		}
	}
	return nil
}

func (d *Driver) CopyFiles(ctx context.Context, fileIDs []string, targetParentID string) error {
	c, err := d.ensureClient()
	if err != nil {
		return err
	}
	target := d.normalizePath(targetParentID)
	for _, id := range fileIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		src := d.normalizePath(id)
		if src == d.rootPath() {
			return domain.Errorf(domain.CodeValidation, "根目录不支持复制")
		}
		dst := d.childPath(target, baseName(src))
		if err := d.copyOne(ctx, c, src, dst); err != nil {
			return mapError(err)
		}
	}
	return nil
}

func (d *Driver) copyOne(ctx context.Context, c *gowebdav.Client, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.Copy(src, dst, true); err == nil {
		return nil
	} else if !shouldFallbackCopy(err) {
		return err
	}
	fi, err := c.Stat(src)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return d.copyDirByStream(ctx, c, src, dst)
	}
	return d.copyFileByStream(ctx, c, src, dst, fi.Size())
}

func (d *Driver) copyFileByStream(ctx context.Context, c *gowebdav.Client, src, dst string, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stream, err := c.ReadStream(src)
	if err != nil {
		return err
	}
	defer stream.Close()
	reader := &ctxReader{ctx: ctx, r: stream}
	if err := c.WriteStreamWithLength(dst, reader, size, 0o644); err != nil {
		if err == io.ErrUnexpectedEOF {
			return domain.Errorf(domain.CodeDriverError, "WebDAV 复制中断：%s", src)
		}
		return err
	}
	return nil
}

func (d *Driver) copyDirByStream(ctx context.Context, c *gowebdav.Client, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.ensureRemoteDir(c, dst); err != nil {
		return err
	}
	children, err := c.ReadDir(src)
	if err != nil {
		return err
	}
	for _, child := range children {
		childSrc, ok := webdavPath(child)
		if !ok {
			childSrc = d.childPath(src, child.Name())
		}
		childDst := d.childPath(dst, child.Name())
		if err := ctx.Err(); err != nil {
			return err
		}
		if child.IsDir() {
			if err := d.copyDirByStream(ctx, c, childSrc, childDst); err != nil {
				return err
			}
			continue
		}
		if err := d.copyFileByStream(ctx, c, childSrc, childDst, child.Size()); err != nil {
			return err
		}
	}
	return nil
}

func shouldFallbackCopy(err error) bool {
	return gowebdav.IsErrCode(err, http.StatusMethodNotAllowed) || gowebdav.IsErrCode(err, http.StatusNotImplemented)
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *ctxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	if err != nil {
		return n, err
	}
	if err := r.ctx.Err(); err != nil {
		if n > 0 {
			return n, nil
		}
		return 0, err
	}
	return n, nil
}

func (d *Driver) ensureRemoteDir(c *gowebdav.Client, path string) error {
	if _, err := c.Stat(path); err == nil {
		return nil
	} else if !gowebdav.IsErrNotFound(err) {
		return err
	}
	return c.MkdirAll(path, 0o755)
}

func (d *Driver) RenameFile(ctx context.Context, fileID, newName string) error {
	src := d.normalizePath(fileID)
	name := strings.TrimSpace(newName)
	if src == d.rootPath() {
		return domain.Errorf(domain.CodeValidation, "根目录不支持重命名")
	}
	if name == "" {
		return domain.Errorf(domain.CodeValidation, "新名称不能为空")
	}
	dst := d.childPath(parentPath(src), name)
	c, err := d.ensureClient()
	if err != nil {
		return err
	}
	_ = ctx
	if err := c.Rename(src, dst, true); err != nil {
		return mapError(err)
	}
	return nil
}
