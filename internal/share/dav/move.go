package dav

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	"litepan/internal/domain"
)

var errInvalidDestination = errors.New("invalid destination")

func (s *Server) serveMove(w http.ResponseWriter, r *http.Request) {
	src := resourcePath(r)
	dst, err := parseMoveDestination(r)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	if dst == src {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	ctx := r.Context()
	if s.stagingMoveAlreadyDone(ctx, src, dst) {
		w.WriteHeader(http.StatusCreated)
		return
	}

	overwrite := r.Header.Get("Overwrite") == "T"
	created := false
	if _, err := s.fs.Stat(ctx, dst); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			writeMoveErr(w, err)
			return
		}
		created = true
	} else if overwrite {
		if err := s.fs.RemoveAll(ctx, dst); err != nil {
			s.log.Warn("webdav move overwrite delete", "dst", dst, "err", err)
			writeMoveErr(w, err)
			return
		}
	} else {
		http.Error(w, "File already exists", http.StatusPreconditionFailed)
		return
	}

	if err := s.fs.Rename(ctx, src, dst); err != nil {
		s.log.Warn("webdav move", "src", src, "dst", dst, "err", err)
		writeMoveErr(w, err)
		return
	}
	if created {
		w.WriteHeader(http.StatusCreated)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseMoveDestination(r *http.Request) (string, error) {
	hdr := strings.TrimSpace(r.Header.Get("Destination"))
	if hdr == "" {
		return "", errInvalidDestination
	}
	u, err := url.Parse(hdr)
	if err != nil {
		return "", errInvalidDestination
	}
	if u.Host != "" && !strings.EqualFold(u.Host, r.Host) {
		return "", errInvalidDestination
	}
	dst := stripMountPrefix(u.Path)
	if dst == "" || dst == "/" {
		return "", errInvalidDestination
	}
	return dst, nil
}

func writeMoveErr(w http.ResponseWriter, err error) {
	if errors.Is(err, os.ErrPermission) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if ae, ok := domain.AsAppError(err); ok {
		http.Error(w, ae.Message, ae.HTTPStatus())
		return
	}
	if os.IsNotExist(err) {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	http.Error(w, "Move failed", http.StatusForbidden)
}

func (s *Server) stagingMoveAlreadyDone(ctx context.Context, src, dst string) bool {
	srcParsed := ParseWebDAVPath(src)
	dstParsed := ParseWebDAVPath(dst)
	if srcParsed.AccountName == "" || dstParsed.AccountName == "" {
		return false
	}
	if !strings.EqualFold(srcParsed.AccountName, dstParsed.AccountName) {
		return false
	}
	if !isStagingMoveToCanonical(srcParsed.RelParts, dstParsed.RelParts) {
		return false
	}
	_, srcErr := s.fs.Stat(ctx, src)
	if srcErr == nil {
		return false
	}
	if !errors.Is(srcErr, os.ErrNotExist) {
		return false
	}
	_, dstErr := s.fs.Stat(ctx, dst)
	return dstErr == nil
}
