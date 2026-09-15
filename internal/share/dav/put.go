package dav

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

type uploadPlan struct {
	accountID int64
	parentID  string
	fileName  string
	existed   bool
	noop      bool
}

func (fs *FileSystem) planUpload(ctx context.Context, webPath string, exclusive bool) (*uploadPlan, error) {
	parsed := ParseWebDAVPath(webPath)
	if parsed.AccountName == "" || len(parsed.RelParts) == 0 {
		return nil, os.ErrPermission
	}
	if isMacOSMetadataPath(append([]string{parsed.AccountName}, parsed.RelParts...)) {
		return &uploadPlan{noop: true}, nil
	}
	acc, err := fs.resolver.accountByName(ctx, parsed.AccountName)
	if err != nil {
		return nil, err
	}
	fileName := parsed.RelParts[len(parsed.RelParts)-1]
	parentParts := parsed.RelParts[:len(parsed.RelParts)-1]
	parentID := "0"
	if len(parentParts) > 0 {
		parentItem, _, err := fs.resolver.resolveUnderAccount(ctx, acc.ID, parentParts)
		if err != nil {
			return nil, err
		}
		if !parentItem.IsDir {
			return nil, os.ErrInvalid
		}
		parentID = parentItem.ID
	}
	existed := false
	if cur, _, err := fs.resolver.resolveUnderAccount(ctx, acc.ID, parsed.RelParts); err == nil {
		if cur.IsDir {
			return nil, errUploadToCollection
		}
		existed = true
		if exclusive {
			return nil, os.ErrExist
		}
	}
	return &uploadPlan{
		accountID: acc.ID,
		parentID:  parentID,
		fileName:  fileName,
		existed:   existed,
	}, nil
}

var errUploadToCollection = errors.New("cannot overwrite a collection with PUT")

func (s *Server) servePut(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	webPath := resourcePath(r)
	exclusive := r.Header.Get("If-None-Match") == "*"

	plan, err := s.fs.planUpload(ctx, webPath, exclusive)
	if err != nil {
		writeUploadErr(w, err)
		return
	}
	if plan.noop {
		w.WriteHeader(http.StatusCreated)
		return
	}

	tmp, tmpPath, release, err := createWebDAVTempFile(s.fs.dataDir, plan.fileName, s.fs.tempRegistry)
	if err != nil {
		s.log.Warn("webdav put temp file", "path", webPath, "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer release()

	if _, err := io.Copy(tmp, r.Body); err != nil {
		_ = tmp.Close()
		s.log.Warn("webdav put read body", "path", webPath, "err", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	if err := tmp.Close(); err != nil {
		s.log.Warn("webdav put close temp", "path", webPath, "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	info, err := os.Stat(tmpPath)
	if err != nil {
		s.log.Warn("webdav put stat temp", "path", webPath, "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	parsed := ParseWebDAVPath(webPath)
	if info.Size() == 0 {
		if _, staging := stripWebDAVStagingSuffix(plan.fileName); staging {
			s.fs.resolver.rememberFile(ctx, plan.accountID, parsed.RelParts, plan.parentID, domain.FileItem{
				Name: plan.fileName,
				Size: 0,
			})
		}
		if plan.existed {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusCreated)
		}
		return
	}

	req := driver.LocalUploadRequest{
		LocalPath:      tmpPath,
		FileName:       plan.fileName,
		ParentID:       plan.parentID,
		ConflictPolicy: "overwrite",
	}
	if times, ok := uploadTimesFromContext(ctx); ok {
		req.ModTime = times.ModTime
		req.CreateTime = times.CreateTime
	}
	result, err := s.fs.files.UploadLocal(ctx, plan.accountID, req)
	if err != nil {
		s.log.Warn("webdav put upload", "path", webPath, "account", plan.accountID, "err", err)
		writeUploadErr(w, err)
		return
	}
	item := domain.FileItem{
		ID:     result.FileID,
		Name:   result.FileName,
		Size:   result.Size,
		IDKind: domain.IDStable,
	}
	if times, ok := uploadTimesFromContext(ctx); ok && times.ModTime != nil && !times.ModTime.IsZero() {
		item.ModTime = *times.ModTime
	} else {
		item.ModTime = time.Now()
	}
	s.fs.resolver.rememberFile(ctx, plan.accountID, parsed.RelParts, plan.parentID, item)
	w.Header().Set("ETag", stableFileETag(item))
	if plan.existed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func writeUploadErr(w http.ResponseWriter, err error) {
	if errors.Is(err, errUploadToCollection) {
		http.Error(w, err.Error(), http.StatusMethodNotAllowed)
		return
	}
	if errors.Is(err, os.ErrPermission) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if errors.Is(err, os.ErrExist) {
		http.Error(w, "File already exists", http.StatusPreconditionFailed)
		return
	}
	if errors.Is(err, os.ErrInvalid) {
		http.Error(w, "Parent path is not a collection", http.StatusConflict)
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
	http.Error(w, "Upload failed", http.StatusConflict)
}
