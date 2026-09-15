package api

import (
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"litepan/internal/backuprestore"
	"litepan/internal/domain"
)

const maxBackupUploadSize = int64(513 * 1024 * 1024)

func (h *Handler) listBackups(w http.ResponseWriter, _ *http.Request) {
	if !ensureServiceReady(w, h.backupRestore != nil) {
		return
	}
	records, err := h.backupRestore.List()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, records)
}

func (h *Handler) createBackup(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.backupRestore != nil) {
		return
	}
	var req backuprestore.CreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	record, err := h.backupRestore.Create(r.Context(), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, Resp{Success: true, Data: record, Message: "备份创建成功"})
}

func (h *Handler) importBackup(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.backupRestore != nil) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBackupUploadSize)
	if err := r.ParseMultipartForm(8 * 1024 * 1024); err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "备份上传失败或文件过大"))
		return
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "请选择备份文件"))
		return
	}
	defer file.Close()
	summary, err := h.backupRestore.Import(r.Context(), header.Filename, r.FormValue("password"), file)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, Resp{Success: true, Data: summary, Message: "备份导入并校验成功"})
}

func (h *Handler) downloadBackup(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.backupRestore != nil) {
		return
	}
	id := chi.URLParam(r, "id")
	file, record, err := h.backupRestore.Open(id)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeErr(w, domain.Wrap(domain.CodeInternal, err))
		return
	}
	created, _ := time.Parse(time.RFC3339Nano, record.CreatedAt)
	filename := "MyPan-backup-" + created.Local().Format("20060102-150405") + ".lpb"
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

func (h *Handler) deleteBackup(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.backupRestore != nil) {
		return
	}
	if err := h.backupRestore.Delete(chi.URLParam(r, "id")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "备份已删除"})
}

func (h *Handler) prepareBackupRestore(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.backupRestore != nil) {
		return
	}
	var req backuprestore.RestoreRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	summary, err := h.backupRestore.PrepareRestore(r.Context(), chi.URLParam(r, "id"), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Data: summary, Message: "备份已校验并准备完成，重启后执行恢复"})
}

func (h *Handler) backupRestoreStatus(w http.ResponseWriter, _ *http.Request) {
	if !ensureServiceReady(w, h.backupRestore != nil) {
		return
	}
	writeOK(w, h.backupRestore.Status())
}

func (h *Handler) cancelPendingRestore(w http.ResponseWriter, _ *http.Request) {
	if !ensureServiceReady(w, h.backupRestore != nil) {
		return
	}
	if err := h.backupRestore.CancelPending(); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "已取消待重启的备份恢复"})
}

func (h *Handler) acknowledgeRestoreStatus(w http.ResponseWriter, _ *http.Request) {
	if !ensureServiceReady(w, h.backupRestore != nil) {
		return
	}
	if err := h.backupRestore.AcknowledgeStatus(); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "恢复结果已确认"})
}

func (h *Handler) restartForRestore(w http.ResponseWriter, _ *http.Request) {
	if !ensureServiceReady(w, h.backupRestore != nil) {
		return
	}
	status := h.backupRestore.Status()
	if status.State != backuprestore.StateWaitingRestart {
		writeErr(w, domain.Errorf(domain.CodeValidation, "当前没有等待重启的恢复"))
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "MyPan 即将优雅退出并执行恢复"})
	go func() {
		time.Sleep(350 * time.Millisecond)
		if err := h.backupRestore.RequestRestart(); err != nil && h.log != nil {
			h.log.Error("发起备份恢复重启失败", "err", fmt.Sprintf("%v", err))
		}
	}()
}
