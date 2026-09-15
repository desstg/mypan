package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"litepan/internal/domain"
	"litepan/internal/upload"
)

type batchDeleteUploadTasksReq struct {
	TaskIDs            []string `json:"task_ids"`
	DeleteUploadedFile bool     `json:"delete_uploaded_file"`
	DeleteBatchRoots   bool     `json:"delete_batch_roots"`
}

type batchControlUploadTasksReq struct {
	TaskIDs []string `json:"task_ids"`
}

func (h *Handler) createUploadTask(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		if errors.Is(r.Context().Err(), context.Canceled) || strings.Contains(strings.ToLower(err.Error()), "context canceled") {
			return
		}
		writeErr(w, translateUploadFormParseError(err))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	accountID, err := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	if err != nil || accountID <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 account_id"))
		return
	}
	targetPath := r.FormValue("path")
	conflictPolicy := r.FormValue("conflict_policy")
	if conflictPolicy == "" {
		conflictPolicy = "overwrite"
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "缺少上传文件"))
		return
	}
	defer file.Close()

	tempPath, total, err := saveUploadTemp(h.uploads.TempDir(), file, header.Filename)
	if err != nil {
		if errors.Is(r.Context().Err(), context.Canceled) || strings.Contains(strings.ToLower(err.Error()), "context canceled") {
			return
		}
		writeErr(w, err)
		return
	}
	if total < 0 {
		_ = os.Remove(tempPath)
		writeErr(w, domain.Errorf(domain.CodeValidation, "上传文件大小非法"))
		return
	}

	displayName := strings.TrimSpace(r.FormValue("display_name"))
	fileName := header.Filename
	if fileName == "" {
		fileName = filepath.Base(tempPath)
	}

	task, err := h.uploads.Create(r.Context(), upload.CreateParams{
		ClientTaskID:      r.FormValue("client_task_id"),
		BatchID:           r.FormValue("batch_id"),
		BatchName:         r.FormValue("batch_name"),
		BatchRootID:       r.FormValue("batch_root_id"),
		BatchRootParentID: r.FormValue("batch_root_parent_id"),
		BatchRootOwned:    strings.EqualFold(strings.TrimSpace(r.FormValue("batch_root_owned")), "true"),
		AccountID:         accountID,
		FileName:          fileName,
		DisplayName:       displayName,
		RelPath:           r.FormValue("rel_path"),
		RelDir:            r.FormValue("rel_dir"),
		TargetPath:        targetPath,
		TargetDisplayPath: r.FormValue("target_display_path"),
		LocalPath:         tempPath,
		TotalBytes:        total,
		ConflictPolicy:    conflictPolicy,
	})
	if err != nil {
		_ = os.Remove(tempPath)
		if errors.Is(r.Context().Err(), context.Canceled) || strings.Contains(strings.ToLower(err.Error()), "context canceled") {
			return
		}
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "上传任务已创建", Data: task})
}

func translateUploadFormParseError(err error) error {
	if err == nil {
		return domain.Errorf(domain.CodeValidation, "解析上传表单失败")
	}
	lower := strings.ToLower(err.Error())
	if errors.Is(err, syscall.ENOSPC) || strings.Contains(lower, "no space left on device") {
		return domain.Errorf(domain.CodeInternal, "服务器临时目录空间不足，请清理磁盘后重试")
	}
	return domain.Errorf(domain.CodeValidation, "解析上传表单失败: %v", err)
}

func saveUploadTemp(dir string, src io.Reader, name string) (string, int64, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, domain.Wrap(domain.CodeInternal, err)
	}
	ext := filepath.Ext(name)
	path := filepath.Join(dir, fmt.Sprintf("%d%s", time.Now().UnixNano(), ext))
	out, err := os.Create(path)
	if err != nil {
		if errors.Is(err, syscall.ENOSPC) || strings.Contains(strings.ToLower(err.Error()), "no space left on device") {
			return "", 0, domain.Errorf(domain.CodeInternal, "服务器上传缓存目录空间不足，请清理磁盘后重试")
		}
		return "", 0, domain.Wrap(domain.CodeInternal, err)
	}
	defer out.Close()
	n, err := io.Copy(out, src)
	if err != nil {
		_ = os.Remove(path)
		if errors.Is(err, syscall.ENOSPC) || strings.Contains(strings.ToLower(err.Error()), "no space left on device") {
			return "", 0, domain.Errorf(domain.CodeInternal, "服务器上传缓存目录空间不足，请清理磁盘后重试")
		}
		return "", 0, domain.Wrap(domain.CodeInternal, err)
	}
	return path, n, nil
}

func (h *Handler) listUploadTasks(w http.ResponseWriter, r *http.Request) {
	var accountID int64
	if raw := r.URL.Query().Get("account_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeErr(w, domain.Errorf(domain.CodeValidation, "非法 account_id"))
			return
		}
		accountID = id
	}
	tasks := h.uploads.List(r.Context(), accountID)
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "获取上传任务成功", Data: tasks})
}

func (h *Handler) streamUploadTasks(w http.ResponseWriter, r *http.Request) {
	s, err := newSSEWriter(w)
	if err != nil {
		writeErr(w, err)
		return
	}
	ch := h.uploads.Subscribe()
	defer h.uploads.Unsubscribe(ch)
	streamSSEByteMessages(r, s, "tasks", h.uploads.SnapshotPayload(), ch)
}

func (h *Handler) getUploadTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	task, ok := h.uploads.Get(r.Context(), taskID)
	if !ok {
		writeErr(w, domain.Errorf(domain.CodeNotFound, "上传任务不存在"))
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "获取上传任务成功", Data: task})
}

func (h *Handler) pauseUploadTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	task, ok := h.uploads.Pause(r.Context(), taskID)
	if !ok {
		writeErr(w, domain.Errorf(domain.CodeNotFound, "上传任务不存在"))
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "上传任务已暂停", Data: task})
}

func (h *Handler) resumeUploadTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	task, ok := h.uploads.Resume(r.Context(), taskID)
	if !ok {
		writeErr(w, domain.Errorf(domain.CodeNotFound, "上传任务不存在"))
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "上传任务已继续", Data: task})
}

func (h *Handler) deleteUploadTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	deleteFile := r.URL.Query().Get("delete_uploaded_file") == "true"
	found, err := h.uploads.Delete(r.Context(), taskID, deleteFile)
	if !found {
		writeErr(w, domain.Errorf(domain.CodeNotFound, "上传任务不存在"))
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "上传任务已删除"})
}

func (h *Handler) batchDeleteUploadTasks(w http.ResponseWriter, r *http.Request) {
	var req batchDeleteUploadTasksReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	result := h.uploads.BatchDelete(r.Context(), req.TaskIDs, req.DeleteUploadedFile, req.DeleteBatchRoots)
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "批量删除上传任务成功", Data: result})
}

func (h *Handler) batchPauseUploadTasks(w http.ResponseWriter, r *http.Request) {
	var req batchControlUploadTasksReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	result := h.uploads.BatchPause(r.Context(), req.TaskIDs)
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "批量暂停上传任务成功", Data: result})
}
