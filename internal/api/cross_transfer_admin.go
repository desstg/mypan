package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"litepan/internal/crosstransfer"
	"litepan/internal/domain"
)

func (h *Handler) crossTransferRoutes(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.crossTransfer != nil) {
		return
	}
	routes := crosstransfer.BuildRoutes()
	writeOK(w, routes)
}

type crossTransferScanReq struct {
	SourceAccountID   int64                     `json:"source_account_id"`
	SourceParentID    string                    `json:"source_parent_id"`
	Method            string                    `json:"method"`
	SourceDisplayPath string                    `json:"source_display_path"`
	Sources           []crossTransferScanSource `json:"sources"`
}

type crossTransferScanSource struct {
	ParentID    string   `json:"parent_id"`
	DisplayPath string   `json:"display_path"`
	AncestorIDs []string `json:"ancestor_ids"`
}

func (r crossTransferScanReq) roots() []crosstransfer.ScanRoot {
	if len(r.Sources) == 0 {
		return []crosstransfer.ScanRoot{{
			ParentID:    r.SourceParentID,
			DisplayPath: r.SourceDisplayPath,
		}}
	}
	roots := make([]crosstransfer.ScanRoot, 0, len(r.Sources))
	for _, source := range r.Sources {
		roots = append(roots, crosstransfer.ScanRoot{
			ParentID:    source.ParentID,
			DisplayPath: source.DisplayPath,
			AncestorIDs: source.AncestorIDs,
		})
	}
	return roots
}

func (h *Handler) crossTransferScan(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.crossTransfer != nil) {
		return
	}
	var req crossTransferScanReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.crossTransfer.ScanSources(r.Context(), req.SourceAccountID, req.roots(), req.Method)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (h *Handler) crossTransferScanStream(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.crossTransfer != nil) {
		return
	}
	var req crossTransferScanReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	h.streamCrossTransferNDJSON(w, r, func(emit func(crosstransfer.StreamEvent) error) error {
		return h.crossTransfer.ScanSourcesStream(
			r.Context(),
			req.SourceAccountID,
			req.roots(),
			req.Method,
			emit,
		)
	})
}

type crossTransferProbeFile struct {
	SourceFileID string `json:"source_file_id"`
	RelPath      string `json:"rel_path"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	Hash         string `json:"hash"`
}

type crossTransferProbeReq struct {
	SourceAccountID int64                    `json:"source_account_id"`
	TargetAccountID int64                    `json:"target_account_id"`
	TargetParentID  string                   `json:"target_parent_id"`
	Method          string                   `json:"method"`
	Files           []crossTransferProbeFile `json:"files"`
}

func (h *Handler) crossTransferProbe(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.crossTransfer != nil) {
		return
	}
	var req crossTransferProbeReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	h.streamCrossTransferNDJSON(w, r, func(emit func(crosstransfer.StreamEvent) error) error {
		files := make([]crosstransfer.TransferFile, 0, len(req.Files))
		for _, f := range req.Files {
			files = append(files, crosstransfer.TransferFile{
				SourceFileID: f.SourceFileID,
				RelPath:      f.RelPath,
				Name:         f.Name,
				Size:         f.Size,
				Hash:         f.Hash,
			})
		}
		return h.crossTransfer.ProbeStream(r.Context(), req.SourceAccountID, req.TargetAccountID, req.TargetParentID, req.Method, files, emit)
	})
}

type crossTransferTransferFile struct {
	SourceFileID string `json:"source_file_id"`
	RelPath      string `json:"rel_path"`
	RelDir       string `json:"rel_dir"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	Hash         string `json:"hash"`
}

type crossTransferExecuteReq struct {
	SourceAccountID   int64                       `json:"source_account_id"`
	SourceAccountName string                      `json:"source_account_name"`
	SourceDriverType  string                      `json:"source_driver_type"`
	TargetAccountID   int64                       `json:"target_account_id"`
	TargetAccountName string                      `json:"target_account_name"`
	TargetDriverType  string                      `json:"target_driver_type"`
	TargetParentID    string                      `json:"target_parent_id"`
	TargetDisplayPath string                      `json:"target_display_path"`
	Method            string                      `json:"method"`
	Files             []crossTransferTransferFile `json:"files"`
	Conflict          string                      `json:"conflict"`
	Fallback          bool                        `json:"fallback"`
}

func (h *Handler) crossTransferExecute(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.crossTransfer != nil) {
		return
	}
	var req crossTransferExecuteReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	files := make([]crosstransfer.TransferFile, 0, len(req.Files))
	for _, f := range req.Files {
		files = append(files, crosstransfer.TransferFile{
			SourceFileID: f.SourceFileID,
			RelPath:      f.RelPath,
			RelDir:       f.RelDir,
			Name:         f.Name,
			Size:         f.Size,
			Hash:         f.Hash,
		})
	}
	h.streamCrossTransferNDJSON(w, r, func(emit func(crosstransfer.StreamEvent) error) error {
		return h.crossTransfer.ExecuteStream(r.Context(), crosstransfer.ExecuteInput{
			SourceAccountID:   req.SourceAccountID,
			SourceAccountName: req.SourceAccountName,
			SourceDriverType:  req.SourceDriverType,
			TargetAccountID:   req.TargetAccountID,
			TargetAccountName: req.TargetAccountName,
			TargetDriverType:  req.TargetDriverType,
			TargetParentID:    req.TargetParentID,
			TargetDisplayPath: req.TargetDisplayPath,
			MethodID:          req.Method,
			Files:             files,
			Conflict:          req.Conflict,
			Fallback:          req.Fallback,
		}, emit)
	})
}

func (h *Handler) streamCrossTransferNDJSON(w http.ResponseWriter, r *http.Request, fn func(func(crosstransfer.StreamEvent) error) error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, domain.Errorf(domain.CodeInternal, "不支持流式响应"))
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	writeLine := func(event crosstransfer.StreamEvent) error {
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "%s\n", raw); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	if err := fn(writeLine); err != nil {
		_ = writeLine(crosstransfer.StreamEvent{"event": "error", "message": err.Error()})
	}
}

type crossTransferPlainEnqueueReq struct {
	SourceAccountID   int64                     `json:"source_account_id"`
	SourceAccountName string                    `json:"source_account_name"`
	SourceDriverType  string                    `json:"source_driver_type"`
	TargetAccountID   int64                     `json:"target_account_id"`
	TargetAccountName string                    `json:"target_account_name"`
	TargetDriverType  string                    `json:"target_driver_type"`
	TargetParentID    string                    `json:"target_parent_id"`
	TargetDisplayPath string                    `json:"target_display_path"`
	Sources           []crossTransferScanSource `json:"sources"`
	Conflict          string                    `json:"conflict"`
}

// crossTransferPlainEnqueue 跨盘普传：服务端枚举源目录后直接创建持久化 relay 任务，
// 入队即返回 JSON 汇总（非 NDJSON 流式），浏览器关闭不影响任务执行。
func (h *Handler) crossTransferPlainEnqueue(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.crossTransfer != nil) {
		return
	}
	var req crossTransferPlainEnqueueReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	sources := make([]crosstransfer.ScanRoot, 0, len(req.Sources))
	for _, src := range req.Sources {
		sources = append(sources, crosstransfer.ScanRoot{
			ParentID:    src.ParentID,
			DisplayPath: src.DisplayPath,
			AncestorIDs: src.AncestorIDs,
		})
	}
	result, err := h.crossTransfer.EnqueuePlain(r.Context(), crosstransfer.EnqueuePlainInput{
		SourceAccountID:   req.SourceAccountID,
		SourceAccountName: req.SourceAccountName,
		SourceDriverType:  req.SourceDriverType,
		TargetAccountID:   req.TargetAccountID,
		TargetAccountName: req.TargetAccountName,
		TargetDriverType:  req.TargetDriverType,
		TargetParentID:    req.TargetParentID,
		TargetDisplayPath: req.TargetDisplayPath,
		Sources:           sources,
		Conflict:          req.Conflict,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}
