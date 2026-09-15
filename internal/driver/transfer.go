package driver

import (
	"context"
	"errors"
	"strings"

	"litepan/internal/domain"
)

type RapidUploadRequest struct {
	ParentID  string
	FileName  string
	Method    string
	Hash      string
	Size      int64
	Duplicate int
}

type RapidUploadResult struct {
	Reuse    bool
	FileID   string
	ParentID string
	Message  string
}

type RapidUploader interface {
	RapidUploadByHash(ctx context.Context, req RapidUploadRequest) (*RapidUploadResult, error)
}

type RapidUploadProber interface {
	SupportsRapidUploadProbe(method string) bool
	ProbeRapidUploadByHash(ctx context.Context, req RapidUploadRequest) (*RapidUploadResult, error)
}

type rapidProbeTerminalError struct{ err error }

func (e *rapidProbeTerminalError) Error() string { return e.err.Error() }
func (e *rapidProbeTerminalError) Unwrap() error { return e.err }

func StopRapidProbe(err error) error {
	if err == nil || IsRapidProbeTerminal(err) {
		return err
	}
	return &rapidProbeTerminalError{err: err}
}

func IsRapidProbeTerminal(err error) bool {
	var target *rapidProbeTerminalError
	return errors.As(err, &target)
}

type TransferHashResolver interface {
	ResolveTransferHash(ctx context.Context, item *domain.FileItem, method string, allowStream bool) (string, error)
}

func NormalizeTransferHash(method, value string) string {
	text := strings.ToLower(strings.TrimSpace(value))
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "md5":
		if len(text) != 32 {
			return ""
		}
		for _, ch := range text {
			if ch < '0' || (ch > '9' && ch < 'a') || ch > 'f' {
				return ""
			}
		}
		return text
	case "sha1":
		if len(text) != 40 {
			return ""
		}
		for _, ch := range text {
			if ch < '0' || (ch > '9' && ch < 'a') || ch > 'f' {
				return ""
			}
		}
		return text
	default:
		return ""
	}
}

func HashFromItem(item *domain.FileItem, method string) string {
	if item == nil || item.Hash == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "md5":
		return NormalizeTransferHash("md5", item.Hash[domain.HashMD5])
	case "sha1":
		return NormalizeTransferHash("sha1", item.Hash[domain.HashSHA1])
	default:
		return ""
	}
}
