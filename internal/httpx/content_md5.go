package httpx

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const contentMD5ProbeSize = 256 * 1024

// ProbeContentMD5 从下载链接或响应头获取整文件 MD5。
func ProbeContentMD5(ctx context.Context, client *http.Client, rawURL string, headers http.Header) (string, error) {
	if md5 := contentMD5FromURL(rawURL); md5 != "" {
		return md5, nil
	}
	if client == nil {
		client = http.DefaultClient
	}

	hadResponse := false
	var lastErr error
	if md5, responded, err := requestContentMD5(ctx, client, http.MethodHead, rawURL, headers); md5 != "" {
		return md5, nil
	} else {
		hadResponse = hadResponse || responded
		lastErr = err
	}

	for attempt := 0; attempt < 2; attempt++ {
		rangeHeaders := cloneHTTPHeader(headers)
		rangeHeaders.Set("Range", "bytes=0-262143")
		md5, responded, err := requestContentMD5(ctx, client, http.MethodGet, rawURL, rangeHeaders)
		if md5 != "" {
			return md5, nil
		}
		hadResponse = hadResponse || responded
		if err != nil {
			lastErr = err
		}
		if attempt == 0 {
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", ctx.Err()
			case <-timer.C:
			}
		}
	}
	if !hadResponse && lastErr != nil {
		return "", lastErr
	}
	return "", nil
}

func requestContentMD5(ctx context.Context, client *http.Client, method, rawURL string, headers http.Header) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return "", false, err
	}
	req.Header = cloneHTTPHeader(headers)
	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	if method == http.MethodGet && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent) {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, contentMD5ProbeSize+1))
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", true, nil
	}
	if md5 := contentMD5FromHeader(resp.Header); md5 != "" {
		return md5, true, nil
	}
	if resp.Request != nil && resp.Request.URL != nil {
		return contentMD5FromURL(resp.Request.URL.String()), true, nil
	}
	return "", true, nil
}

func contentMD5FromHeader(header http.Header) string {
	for _, key := range []string{"Content-MD5", "X-Bs-Meta-Md5", "X-Bce-Content-Md5", "ETag"} {
		if md5 := normalizeContentMD5(header.Get(key)); md5 != "" {
			return md5
		}
	}
	return ""
}

func contentMD5FromURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	for key, values := range parsed.Query() {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "md5", "content-md5", "content_md5", "x-bce-content-md5":
			for _, value := range values {
				if md5 := normalizeContentMD5(value); md5 != "" {
					return md5
				}
			}
		}
	}
	return ""
}

func normalizeContentMD5(raw string) string {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(value, "W/")
	value = strings.Trim(value, "\"'")
	if len(value) == 32 {
		lower := strings.ToLower(value)
		if _, err := hex.DecodeString(lower); err == nil {
			return lower
		}
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil && len(decoded) == 16 {
		return hex.EncodeToString(decoded)
	}
	return ""
}

func cloneHTTPHeader(header http.Header) http.Header {
	cloned := make(http.Header, len(header))
	for key, values := range header {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}
