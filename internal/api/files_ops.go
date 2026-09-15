package api

import (
	"net/http"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/file"
)

type deleteFilesReq struct {
	AccountID int64    `json:"account_id"`
	FileIDs   []string `json:"file_ids"`
	ParentID  string   `json:"parent_id"`
}

type transferFilesReq struct {
	AccountID      int64    `json:"account_id"`
	FileIDs        []string `json:"file_ids"`
	TargetParentID string   `json:"target_parent_id"`
	SourceParentID string   `json:"source_parent_id"`
}

func (h *Handler) deleteFiles(w http.ResponseWriter, r *http.Request) {
	var req deleteFilesReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.AccountID <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 account_id"))
		return
	}
	if len(req.FileIDs) == 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "file_ids 不能为空"))
		return
	}
	if err := h.files.DeleteFiles(r.Context(), req.AccountID, req.FileIDs, req.ParentID); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{
		Success: true,
		Message: "已删除到回收站 " + strconv.Itoa(len(req.FileIDs)) + " 个项目",
		Data: map[string]any{
			"file_ids": req.FileIDs,
		},
	})
}

func (h *Handler) moveFiles(w http.ResponseWriter, r *http.Request) {
	var req transferFilesReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.AccountID <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 account_id"))
		return
	}
	if len(req.FileIDs) == 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "file_ids 不能为空"))
		return
	}
	if err := h.files.MoveFiles(r.Context(), req.AccountID, req.FileIDs, req.TargetParentID, req.SourceParentID); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{
		Success: true,
		Message: "已移动 " + strconv.Itoa(len(req.FileIDs)) + " 个项目",
		Data: map[string]any{
			"file_ids":         req.FileIDs,
			"target_parent_id": req.TargetParentID,
		},
	})
}

func (h *Handler) copyFiles(w http.ResponseWriter, r *http.Request) {
	var req transferFilesReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.AccountID <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 account_id"))
		return
	}
	if len(req.FileIDs) == 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "file_ids 不能为空"))
		return
	}
	if err := h.files.CopyFiles(r.Context(), req.AccountID, req.FileIDs, req.TargetParentID); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{
		Success: true,
		Message: "已复制 " + strconv.Itoa(len(req.FileIDs)) + " 个项目",
		Data: map[string]any{
			"file_ids":         req.FileIDs,
			"target_parent_id": req.TargetParentID,
		},
	})
}

type renameFileReq struct {
	AccountID int64  `json:"account_id"`
	FileID    string `json:"file_id"`
	NewName   string `json:"new_name"`
	ParentID  string `json:"parent_id"`
}

type nameAlignPreviewReq struct {
	AccountID    int64  `json:"account_id"`
	ParentID     string `json:"parent_id"`
	TargetFileID string `json:"target_file_id"`
	SampleFileID string `json:"sample_file_id"`
}

type nameAlignApplyReq struct {
	AccountID       int64    `json:"account_id"`
	ParentID        string   `json:"parent_id"`
	TargetFileID    string   `json:"target_file_id"`
	SampleFileID    string   `json:"sample_file_id"`
	SelectedFileIDs []string `json:"selected_file_ids"`
}

type createFolderReq struct {
	AccountID int64  `json:"account_id"`
	ParentID  string `json:"parent_id"`
	Name      string `json:"name"`
}

func (h *Handler) renameFile(w http.ResponseWriter, r *http.Request) {
	var req renameFileReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.AccountID <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 account_id"))
		return
	}
	if strings.TrimSpace(req.FileID) == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "file_id 不能为空"))
		return
	}
	if strings.TrimSpace(req.NewName) == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "new_name 不能为空"))
		return
	}
	if err := h.files.RenameFile(r.Context(), req.AccountID, req.FileID, req.NewName, req.ParentID); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{
		Success: true,
		Message: "重命名成功",
		Data: map[string]any{
			"file_id":  req.FileID,
			"new_name": req.NewName,
		},
	})
}

func (h *Handler) previewNameAlign(w http.ResponseWriter, r *http.Request) {
	var req nameAlignPreviewReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.AccountID <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 account_id"))
		return
	}
	if strings.TrimSpace(req.TargetFileID) == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "target_file_id 不能为空"))
		return
	}
	preview, err := h.files.PreviewNameAlign(r.Context(), file.NameAlignPreviewInput{
		AccountID:    req.AccountID,
		ParentID:     req.ParentID,
		TargetFileID: req.TargetFileID,
		SampleFileID: req.SampleFileID,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{
		Success: true,
		Message: "命名对齐预览生成成功",
		Data:    preview,
	})
}

func (h *Handler) applyNameAlign(w http.ResponseWriter, r *http.Request) {
	var req nameAlignApplyReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.AccountID <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 account_id"))
		return
	}
	if strings.TrimSpace(req.TargetFileID) == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "target_file_id 不能为空"))
		return
	}
	if len(req.SelectedFileIDs) == 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "selected_file_ids 不能为空"))
		return
	}
	result, err := h.files.ApplyNameAlign(r.Context(), file.NameAlignApplyInput{
		AccountID:       req.AccountID,
		ParentID:        req.ParentID,
		TargetFileID:    req.TargetFileID,
		SampleFileID:    req.SampleFileID,
		SelectedFileIDs: req.SelectedFileIDs,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{
		Success: true,
		Message: "命名对齐执行成功",
		Data:    result,
	})
}

func (h *Handler) createFolder(w http.ResponseWriter, r *http.Request) {
	var req createFolderReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.AccountID <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 account_id"))
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "name 不能为空"))
		return
	}
	item, err := h.files.CreateFolder(r.Context(), req.AccountID, req.ParentID, req.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{
		Success: true,
		Message: "文件夹 '" + req.Name + "' 创建成功",
		Data: map[string]any{
			"folder_id":   item.ID,
			"folder_name": item.Name,
			"parent_id":   req.ParentID,
		},
	})
}
