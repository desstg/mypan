package api

import (
	"net/http"

	"litepan/internal/domain"
	"litepan/internal/searchtransfer"
)

// 「搜索转存」页的接口。四件事：
//   - site：探活盘搜站点（装没装内置盘搜、地址通不通）；
//   - search：关键词搜索，结果按网盘类型分组交给前端做 tab；
//   - save-capabilities：所选账号能收哪些 scheme（界面据此置灰）；
//   - save-share：115 分享链的手工转存（磁力/ed2k/http 等走既有的
//     /files/offline-download/urls，不在这里重复一遍）。

type searchTransferSearchReq struct {
	Keyword string `json:"keyword"`
	// CloudTypes / Plugins / Channels 是**按次**参数，不落库。
	CloudTypes []string `json:"cloud_types"`
	Plugins    []string `json:"plugins"`
	Channels   []string `json:"channels"`
	// BaseURL / Token 留空则用设置里那一份（与 TG 订阅的网盘搜索同一个键）。
	BaseURL string `json:"base_url"`
	Token   string `json:"token"`
}

type saveShareReq struct {
	AccountID   int64  `json:"account_id"`
	ShareURL    string `json:"share_url"`
	Password    string `json:"password"`
	ParentID    string `json:"target_parent_id"`
	DisplayPath string `json:"target_display_path"`
	// ShareName 是搜索结果的标题（note），只为转存记录留档。
	ShareName string `json:"share_name"`
}

// transferRecordsReq 是转存记录列表的查询参数（走 query）。
type transferRecordItem struct {
	ID          int64   `json:"id"`
	Kind        string  `json:"kind"`
	Link        string  `json:"link"`
	Password    string  `json:"password"`
	ShareName   string  `json:"share_name"`
	FileName    string  `json:"file_name"`
	Status      string  `json:"status"`
	Note        string  `json:"note"`
	Source      string  `json:"source"`
	AccountName string  `json:"account_name"`
	CreatedAt   float64 `json:"created_at"`
	UpdatedAt   float64 `json:"updated_at"`
}

type deleteTransferRecordsReq struct {
	IDs []int64 `json:"ids"`
}

type saveLinksReq struct {
	AccountID    int64    `json:"account_id"`
	URLs         []string `json:"urls"`
	ParentID     string   `json:"target_parent_id"`
	DisplayPath  string   `json:"target_display_path"`
	ProviderKind string   `json:"provider_kind"`
}

type saveLinkItemReq struct {
	URL       string `json:"url"`
	Password  string `json:"password"`
	ShareName string `json:"share_name"`
}

type saveLinksBatchReq struct {
	AccountID    int64             `json:"account_id"`
	Items        []saveLinkItemReq `json:"items"`
	ParentID     string            `json:"target_parent_id"`
	DisplayPath  string            `json:"target_display_path"`
	ProviderKind string            `json:"provider_kind"`
	// Source 标明这一批从哪一页发起（search / manual），进转存记录。
	Source string `json:"source"`
}

// searchTransferReady 是这一组接口的统一就绪判断。
//
// 服务没接上时回 503 而不是让 handler 里 nil 崩掉 —— 与 h.tgSubscribeReady 同款。
func (h *Handler) searchTransferReady(w http.ResponseWriter) bool {
	if h.searchTransfer == nil || !h.searchTransfer.Ready() {
		writeErr(w, domain.Errorf(domain.CodeNotImplement, "搜索转存服务未就绪"))
		return false
	}
	return true
}

// searchTransferSite 探活盘搜站点。失败也是 200 —— 「站点不可用」是要展示的状态，
// 不是一次请求错误（前端据此显示「安装内置盘搜或填写地址」的提示）。
func (h *Handler) searchTransferSite(w http.ResponseWriter, r *http.Request) {
	if !h.searchTransferReady(w) {
		return
	}
	site := h.searchTransfer.Site(r.Context(), r.URL.Query().Get("base_url"), r.URL.Query().Get("token"))
	writeOK(w, site)
}

func (h *Handler) searchTransferSearch(w http.ResponseWriter, r *http.Request) {
	if !h.searchTransferReady(w) {
		return
	}
	var req searchTransferSearchReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	res, err := h.searchTransfer.Search(r.Context(), searchtransfer.SearchRequest{
		Keyword:    req.Keyword,
		CloudTypes: req.CloudTypes,
		Plugins:    req.Plugins,
		Channels:   req.Channels,
		BaseURL:    req.BaseURL,
		Token:      req.Token,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, res)
}

func (h *Handler) searchTransferCapabilities(w http.ResponseWriter, r *http.Request) {
	if !h.searchTransferReady(w) {
		return
	}
	accountID, err := parseQueryInt64(r, "account_id")
	if err != nil {
		writeErr(w, err)
		return
	}
	caps, err := h.searchTransfer.Capabilities(r.Context(), accountID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, caps)
}

func (h *Handler) saveShareToAccount(w http.ResponseWriter, r *http.Request) {
	if !h.searchTransferReady(w) {
		return
	}
	var req saveShareReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	res, err := h.searchTransfer.SaveShare(r.Context(), searchtransfer.SaveShareRequest{
		AccountID:   req.AccountID,
		ShareURL:    req.ShareURL,
		Password:    req.Password,
		ParentID:    req.ParentID,
		DisplayPath: req.DisplayPath,
		Source:      domain.TransferSourceSearch,
		ShareName:   req.ShareName,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, res)
}

// searchTransferSaveLinks 是「转存」tab 的批量提交入口。
//
// 与搜索页逐条调 save-share / offline-download/urls 不同：这里一次拿一整批，
// 由服务端按行判类型分派，并**逐条落转存记录** —— 用户在这一页看到的是
// 一张完整的去向清单，而不是一串 toast。
func (h *Handler) searchTransferSaveLinks(w http.ResponseWriter, r *http.Request) {
	if !h.searchTransferReady(w) {
		return
	}
	var req saveLinksBatchReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	items := make([]searchtransfer.SaveLinkRequest, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, searchtransfer.SaveLinkRequest{
			AccountID:    req.AccountID,
			URL:          item.URL,
			Password:     item.Password,
			ShareName:    item.ShareName,
			ParentID:     req.ParentID,
			DisplayPath:  req.DisplayPath,
			ProviderKind: req.ProviderKind,
		})
	}
	res, err := h.searchTransfer.SaveLinks(r.Context(), items, req.Source)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, res)
}

// transferRecords 读一页转存记录。
func (h *Handler) transferRecords(w http.ResponseWriter, r *http.Request) {
	if !h.searchTransferReady(w) {
		return
	}
	page, _ := parseQueryInt64(r, "page")
	pageSize, _ := parseQueryInt64(r, "page_size")
	res, err := h.searchTransfer.Records(r.Context(), searchtransfer.RecordFilter{
		Kind:   r.URL.Query().Get("kind"),
		Status: r.URL.Query().Get("status"),
	}, int(page), int(pageSize))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, res)
}

// deleteTransferRecords 删记录。**不动网盘里的文件**。
func (h *Handler) deleteTransferRecords(w http.ResponseWriter, r *http.Request) {
	if !h.searchTransferReady(w) {
		return
	}
	var req deleteTransferRecordsReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	n, err := h.searchTransfer.DeleteRecords(r.Context(), req.IDs)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"deleted": n})
}
