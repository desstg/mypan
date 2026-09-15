package dav

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type uploadTimesCtxKey struct{}

// UploadTimes 从 WebDAV PUT 请求头解析出的本地文件时间，供上传链路保留 mtime。
type UploadTimes struct {
	ModTime    *time.Time
	CreateTime *time.Time
}

func contextWithUploadTimes(ctx context.Context, h http.Header) context.Context {
	mod, create := parseUploadTimes(h)
	if mod == nil && create == nil {
		return ctx
	}
	return context.WithValue(ctx, uploadTimesCtxKey{}, UploadTimes{
		ModTime:    mod,
		CreateTime: create,
	})
}

func uploadTimesFromContext(ctx context.Context) (UploadTimes, bool) {
	v, ok := ctx.Value(uploadTimesCtxKey{}).(UploadTimes)
	return v, ok
}

func parseUploadTimes(h http.Header) (mod, create *time.Time) {
	if t, ok := parseHeaderUnixTime(h.Get("X-OC-Mtime")); ok {
		mod = &t
	} else if lm := h.Get("Last-Modified"); lm != "" {
		if t, err := http.ParseTime(lm); err == nil {
			mod = &t
		}
	}
	if t, ok := parseHeaderUnixTime(h.Get("X-OC-Ctime")); ok {
		create = &t
	} else if mod != nil {
		t := *mod
		create = &t
	}
	return mod, create
}

func parseHeaderUnixTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	sec, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(sec, 0), true
}
