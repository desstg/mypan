package dav

import (
	"context"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/webdav"

	"litepan/internal/cache"
	"litepan/internal/domain"
	"litepan/internal/file"
	"litepan/internal/playback"
	"litepan/internal/settings"
	"litepan/internal/upload"
	"litepan/pkg/security"
)

const (
	configAdminUsername = "admin_username"
	configAdminPassword = "admin_password"
	configWebDAVEnabled = "webdav_enabled"
	mountPrefix         = "/dav"
)

type Deps struct {
	Logs         *slog.Logger
	Files        *file.Service
	Playback     *playback.Service
	Accounts     domain.AccountRepository
	Configs      domain.ConfigRepository
	Cache        *cache.Service
	Settings     *settings.Service
	DataDir      string
	TempRegistry *upload.TempRegistry
}

type Server struct {
	log      *slog.Logger
	resolver *Resolver
	playback *playback.Service
	fs       *FileSystem
	handler  *webdav.Handler
	configs  domain.ConfigRepository
	wc       *webdavCache
}

type webDAVHandlerError struct {
	err error
}

type webDAVHandlerErrorKey struct{}

func New(d Deps) *Server {
	log := d.Logs
	if log == nil {
		log = slog.Default()
	}
	wc := newWebDAVCache(d.Cache, d.Settings, d.Files)
	resolver := NewResolver(d.Files, d.Accounts, wc)
	tmpDir := strings.TrimSpace(d.DataDir)
	if tmpDir == "" {
		tmpDir = os.TempDir()
	}
	fs := &FileSystem{
		resolver:     resolver,
		files:        d.Files,
		dataDir:      tmpDir,
		tempRegistry: d.TempRegistry,
		log:          log,
	}
	h := &webdav.Handler{
		Prefix:     mountPrefix,
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if captured, ok := r.Context().Value(webDAVHandlerErrorKey{}).(*webDAVHandlerError); ok {
				captured.err = err
			}
			if err != nil && log.Enabled(r.Context(), slog.LevelDebug) {
				log.Debug("webdav", "method", r.Method, "path", r.URL.Path, "err", err)
			}
		},
	}
	return &Server{
		log:      log,
		resolver: resolver,
		playback: d.Playback,
		fs:       fs,
		handler:  h,
		configs:  d.Configs,
		wc:       wc,
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodOptions && !s.webdavEnabled(r.Context()) {
		http.Error(w, "WebDAV 服务已关闭", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodOptions && !s.credentialsConfigured(r.Context()) {
		http.Error(w, "WebDAV 未启用：请先在管理后台完成管理员密码设置", http.StatusServiceUnavailable)
		return
	}
	if !s.authenticate(w, r) {
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if s.serveRead(w, r) {
			return
		}
	}
	if r.Method == http.MethodPut {
		r = r.WithContext(contextWithUploadTimes(r.Context(), r.Header))
		s.servePut(w, r)
		return
	}
	if r.Method == "MOVE" {
		s.serveMove(w, r)
		return
	}

	webPath := resourcePath(r)
	if r.Method == "PROPFIND" && s.tryServeCachedPropfind(w, r, webPath) {
		return
	}

	var captured *webDAVHandlerError
	if r.Method == "PROPFIND" {
		captured = &webDAVHandlerError{}
		r = r.WithContext(context.WithValue(r.Context(), webDAVHandlerErrorKey{}, captured))
	}

	if s.useBufferedHandler(r.Method) {
		brw := newBufferedResponseWriter()
		s.handler.ServeHTTP(brw, r)
		if r.Method == "PROPFIND" {
			normalizePropfindErrorResponse(brw, captured.err)
			s.storePropfindCache(r, webPath, brw.statusCode, brw.body)
		}
		_, _ = brw.writeTo(w)
		return
	}

	s.handler.ServeHTTP(w, r)
}

func normalizePropfindErrorResponse(w *bufferedResponseWriter, err error) {
	if w == nil || w.statusCode != http.StatusMethodNotAllowed {
		return
	}
	status, message := webDAVErrorResponse(err)
	w.statusCode = status
	w.body = []byte(message)
}

func webDAVErrorResponse(err error) (int, string) {
	if ae, ok := domain.AsAppError(err); ok {
		status := ae.HTTPStatus()
		message := strings.TrimSpace(ae.Message)
		if message == "" {
			message = http.StatusText(status)
		}
		return status, message
	}
	if os.IsNotExist(err) {
		return http.StatusNotFound, http.StatusText(http.StatusNotFound)
	}
	if os.IsPermission(err) {
		return http.StatusForbidden, http.StatusText(http.StatusForbidden)
	}
	return http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError)
}

func (s *Server) propfindAccountID(ctx context.Context, webPath string) int64 {
	parsed := ParseWebDAVPath(webPath)
	if parsed.AccountName == "" {
		return 0
	}
	acc, err := s.resolver.accountByName(ctx, parsed.AccountName)
	if err != nil || acc == nil {
		return 0
	}
	return acc.ID
}

func (s *Server) tryServeCachedPropfind(w http.ResponseWriter, r *http.Request, webPath string) bool {
	wc := s.wc
	if wc == nil {
		return false
	}
	accountID := s.propfindAccountID(r.Context(), webPath)
	if accountID == 0 {
		return false
	}
	body, ok := wc.getPropfind(accountID, webPath, wc.propfindDepth(r.Header.Get("Depth")))
	if !ok {
		return false
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = w.Write(body)
	return true
}

func (s *Server) storePropfindCache(r *http.Request, webPath string, status int, body []byte) {
	wc := s.wc
	if wc == nil || status != http.StatusMultiStatus || len(body) == 0 {
		return
	}
	accountID := s.propfindAccountID(r.Context(), webPath)
	if accountID == 0 {
		return
	}
	wc.setPropfind(r.Context(), accountID, webPath, wc.propfindDepth(r.Header.Get("Depth")), body)
}

func (s *Server) useBufferedHandler(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost:
		return false
	default:
		return true
	}
}

func (s *Server) credentialsConfigured(ctx context.Context) bool {
	if s.configs == nil {
		return false
	}
	v, ok, err := s.configs.Get(ctx, configAdminPassword)
	if err != nil || !ok {
		return false
	}
	pass := strings.TrimSpace(v)
	if pass == "" {
		return false
	}
	return strings.HasPrefix(pass, "pbkdf2:") || strings.HasPrefix(pass, "scrypt:")
}

func (s *Server) webdavEnabled(ctx context.Context) bool {
	if s.configs == nil {
		return true
	}
	v, ok, err := s.configs.Get(ctx, configWebDAVEnabled)
	if err != nil || !ok {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodOptions {
		return true
	}
	user, pass, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="MyPan WebDAV"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	if !s.checkCredentials(r.Context(), user, pass) {
		w.Header().Set("WWW-Authenticate", `Basic realm="MyPan WebDAV"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func (s *Server) checkCredentials(ctx context.Context, username, password string) bool {
	storedUser := "admin"
	storedPass := ""
	if s.configs != nil {
		if v, ok, _ := s.configs.Get(ctx, configAdminUsername); ok && strings.TrimSpace(v) != "" {
			storedUser = strings.TrimSpace(v)
		}
		if v, ok, _ := s.configs.Get(ctx, configAdminPassword); ok {
			storedPass = strings.TrimSpace(v)
		}
	}
	if username != storedUser {
		return false
	}
	if strings.HasPrefix(storedPass, "pbkdf2:") || strings.HasPrefix(storedPass, "scrypt:") {
		return security.CheckPasswordHash(storedPass, password)
	}
	return false
}

func (s *Server) serveRead(w http.ResponseWriter, r *http.Request) bool {
	reqPath := resourcePath(r)
	node, err := s.resolver.Resolve(r.Context(), reqPath)
	if err != nil {
		return false
	}
	if node.IsRoot || node.IsAccount || node.Item.IsDir {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Type", "httpd/unix-directory")
			w.WriteHeader(http.StatusOK)
			return true
		}
		s.serveDirListing(w, r, node, reqPath)
		return true
	}
	if r.Method == http.MethodHead {
		writeFileHead(w, node)
		return true
	}
	req := playback.Request{AccountID: node.Account.ID, FileID: node.Item.ID}
	intent := playback.Intent{FileName: node.Item.Name, WebDAV: true}
	if err := s.playback.ServeHTTP(w, r, req, intent); err != nil {
		writeDomainErr(w, err)
	}
	return true
}

func writeFileHead(w http.ResponseWriter, node *Node) {
	item := node.Item
	etag := stableFileETag(item)
	if item.IsDir {
		w.Header().Set("Content-Type", "httpd/unix-directory")
	} else {
		ctype := mime.TypeByExtension(path.Ext(item.Name))
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ctype)
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("ETag", etag)
	if item.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(item.Size, 10))
	}
	if !item.ModTime.IsZero() {
		w.Header().Set("Last-Modified", item.ModTime.UTC().Format(http.TimeFormat))
	}
	w.WriteHeader(http.StatusOK)
}

func stableFileETag(item domain.FileItem) string {
	mod := item.ModTime
	if mod.IsZero() {
		mod = time.Unix(0, 0)
	}
	return fmt.Sprintf(`"%s-%d-%d"`, item.ID, item.Size, mod.Unix())
}

func resourcePath(r *http.Request) string {
	return stripMountPrefix(r.URL.Path)
}

func stripMountPrefix(urlPath string) string {
	p := strings.TrimPrefix(urlPath, mountPrefix)
	if p == "" || p == "/" {
		return "/"
	}
	return cleanWebPath(p)
}

func cleanWebPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return pathClean(p)
}

func writeDomainErr(w http.ResponseWriter, err error) {
	if ae, ok := domain.AsAppError(err); ok {
		http.Error(w, ae.Message, ae.HTTPStatus())
		return
	}
	if os.IsNotExist(err) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

var _ http.Handler = (*Server)(nil)
