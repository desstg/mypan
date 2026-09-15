package playback

import (
	"context"
	"net/http"

	"litepan/internal/cache"
	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
)

type Service struct {
	exec        *driverexec.Executor
	cache       *cache.Service
	clientHTTP1 *http.Client
	clientH2    *http.Client
	rangeLimits accountRangeLimiter
	resolveHook DownloadResolverHook
}

// DownloadResolverHook 允许外部插件在驱动解析前接管下载直链。
// 返回 handled=true 时使用返回的 DownloadInfo；handled=false 时回落驱动默认解析。
// playback 为 true 表示本次解析用于“播放/流式”（可接受转码直链），false 表示字节级读取（必须源文件）。
type DownloadResolverHook func(ctx context.Context, accountID int64, driverType, fileID, ua string, playback bool) (*domain.DownloadInfo, bool, error)

func NewService(exec *driverexec.Executor, c *cache.Service) *Service {
	return &Service{
		exec:        exec,
		cache:       c,
		clientHTTP1: &http.Client{Transport: newUpstreamTransport(false), CheckRedirect: stripRedirectReferer},
		clientH2:    &http.Client{Transport: newUpstreamTransport(true), CheckRedirect: stripRedirectReferer},
	}
}

// SetDownloadResolverHook 注入下载解析接管钩子，仅在服务启动前调用一次。
func (s *Service) SetDownloadResolverHook(h DownloadResolverHook) {
	s.resolveHook = h
}

func stripRedirectReferer(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	prev := via[len(via)-1]
	if prev.URL.Host != req.URL.Host || prev.URL.Scheme != req.URL.Scheme {
		req.Header.Del("Referer")
	}
	return nil
}

type Request struct {
	AccountID int64
	FileID    string
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request, req Request, intent Intent) error {
	if err := s.exec.Check(r.Context(), req.AccountID); err != nil {
		return err
	}
	ua := r.UserAgent()
	res, err := s.Resolve(r.Context(), req.AccountID, req.FileID, ua, false, intent.allowsPlaybackResolve())
	if err != nil {
		return err
	}
	if res.File.IsDir {
		return domain.Errorf(domain.CodeValidation, "不能下载目录")
	}
	action := PickAction(res.Mode, res.Link, intent)
	if action == ActionRedirect {
		writeRedirect(w, r, res, intent)
		return nil
	}
	name := intent.FileName
	if name == "" {
		name = res.File.Name
	}
	return s.serveStream(w, r, req, res, name, ua, intent)
}

func (s *Service) Resolve(ctx context.Context, accountID int64, fileID, ua string, refresh, playback bool) (Resolved, error) {
	if s.cache == nil {
		return s.resolveFresh(ctx, accountID, fileID, ua, playback)
	}

	key := cache.DownloadURLKey(accountID, fileID, resolveCacheVariant(ua, playback))
	if refresh {
		s.cache.InvalidateKey(key)
	} else if res, ok := cache.GetAs[Resolved](s.cache, key); ok {
		return res, nil
	}

	res, err := cache.CoalesceAs[Resolved](ctx, s.cache, key, func(callCtx context.Context) (Resolved, error) {
		if !refresh {
			if cached, ok := cache.GetAs[Resolved](s.cache, key); ok {
				return cached, nil
			}
		}
		fresh, err := s.resolveFresh(callCtx, accountID, fileID, ua, playback)
		if err != nil {
			return Resolved{}, err
		}
		ttl := fresh.Link.Expiration
		if ttl <= 0 {
			ttl = defaultLinkTTL
		}
		cache.SetAs(s.cache, key, fresh, ttl)
		return fresh, nil
	})
	if err != nil {
		return Resolved{}, err
	}
	return res, nil
}

// resolveCacheVariant 将播放直链与原始文件直链隔离，避免同一 UA 的缓存串用。
func resolveCacheVariant(ua string, playback bool) string {
	if playback {
		return ua + "\x00playback"
	}
	return ua + "\x00original"
}

func (s *Service) resolveFresh(ctx context.Context, accountID int64, fileID, ua string, playback bool) (Resolved, error) {
	var res Resolved
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		file := domain.FileItem{ID: fileID}
		var link *domain.DownloadInfo
		if s.resolveHook != nil {
			if info, handled, err := s.resolveHook(ctx, accountID, drv.Config().Name, fileID, ua, playback); handled {
				if err != nil {
					return err
				}
				link = info
			}
		}
		if link == nil {
			dl, err := driverexec.Require[driver.Downloader](drv)
			if err != nil {
				return err
			}
			got, err := dl.ResolveDownload(ctx, driver.DownloadRequest{FileID: fileID, UA: ua})
			if err != nil {
				return err
			}
			link = got
		}
		if link.URL == "" && link.LocalPath == "" && !link.ForceProxy {
			return domain.Errorf(domain.CodeDriverError, "驱动未返回下载地址")
		}
		if file.Size <= 0 && link.Size > 0 {
			file.Size = link.Size
		}
		if file.Name == "" && link.FileName != "" {
			file.Name = link.FileName
		}
		if file.Name == "" || file.Size <= 0 {
			if info, ok := drv.(driver.InfoGetter); ok {
				got, err := info.GetFileInfo(ctx, fileID)
				if err != nil {
					return err
				}
				file = *got
				if file.Size <= 0 && link.Size > 0 {
					file.Size = link.Size
				}
				if file.Name == "" && link.FileName != "" {
					file.Name = link.FileName
				}
			}
		}
		mode := link.Mode
		if mode == domain.DownloadRedirect && link.ForceProxy {
			mode = domain.DownloadProxy
		}
		res = Resolved{File: file, Link: *link, Mode: mode}
		return nil
	})
	return res, err
}

func (s *Service) InvalidateAccount(accountID int64) {
	if s.cache != nil {
		s.cache.InvalidateAccountType(accountID, cache.TypeDownloadURL)
	}
}

func (s *Service) InvalidateAll() {
	if s.cache != nil {
		s.cache.InvalidatePrefix(string(cache.TypeDownloadURL) + ":")
	}
}
