package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/mediaorganize/tmdb"
	"litepan/internal/tgsubscribe"
)

// tgSubscribeReady 是各 handler 的统一前置检查。
func (h *Handler) tgSubscribeReady(w http.ResponseWriter) bool {
	return ensureServiceReady(w, h.tgSubscribe != nil)
}

// ————————————————————— 热门推荐（TMDB） —————————————————————

func (h *Handler) tgDiscover(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	q := r.URL.Query()
	mediaType := strings.TrimSpace(q.Get("type"))
	if mediaType == "" {
		mediaType = domain.TGMediaTypeMovie
	}
	page, _ := strconv.Atoi(strings.TrimSpace(q.Get("page")))

	params := tmdb.DiscoverParams{
		Page:          page,
		OriginCountry: strings.TrimSpace(q.Get("country")),
		SortBy:        strings.TrimSpace(q.Get("sort")),
	}
	if raw := strings.TrimSpace(q.Get("year")); raw != "" {
		year, err := strconv.Atoi(raw)
		if err != nil {
			writeErr(w, domain.Errorf(domain.CodeValidation, "年份需为整数"))
			return
		}
		params.Year = year
	}
	for _, raw := range strings.Split(q.Get("genres"), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			continue
		}
		params.GenreIDs = append(params.GenreIDs, id)
	}

	payload, err := h.tgSubscribe.Discover(r.Context(), mediaType, params)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "获取热门推荐成功", Data: payload})
}

func (h *Handler) tgSearch(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("q"))
	if query == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "请输入搜索关键词"))
		return
	}
	var year *int
	if raw := strings.TrimSpace(q.Get("year")); raw != "" {
		y, err := strconv.Atoi(raw)
		if err != nil {
			writeErr(w, domain.Errorf(domain.CodeValidation, "年份需为整数"))
			return
		}
		year = &y
	}
	mediaType := strings.TrimSpace(q.Get("type"))
	if mediaType == "" {
		mediaType = "auto"
	}
	results, err := h.tgSubscribe.Search(r.Context(), query, year, mediaType)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, results)
}

func (h *Handler) tgGenres(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	mediaType := strings.TrimSpace(r.URL.Query().Get("type"))
	if mediaType == "" {
		mediaType = domain.TGMediaTypeMovie
	}
	payload, err := h.tgSubscribe.Genres(r.Context(), mediaType)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, payload)
}

func (h *Handler) tgDetail(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	q := r.URL.Query()
	id := strings.TrimSpace(q.Get("id"))
	if id == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "缺少 id"))
		return
	}
	mediaType := strings.TrimSpace(q.Get("type"))
	if mediaType == "" {
		mediaType = domain.TGMediaTypeMovie
	}
	payload, err := h.tgSubscribe.Detail(r.Context(), id, mediaType)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, payload)
}

// tgPoster 是海报代理。
//
// image.tmdb.org 在国内浏览器经常打不开，直连等于海报墙全白 —— 所以图片一律
// 经后端转发，并带磁盘缓存扛住海报墙一次 20 张的并发。
func (h *Handler) tgPoster(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	q := r.URL.Query()
	data, contentType, err := h.tgSubscribe.FetchPoster(r.Context(), q.Get("path"), q.Get("size"))
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}

// ————————————————————— 配置 —————————————————————

func (h *Handler) getTGSubscribeConfig(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	writeOK(w, h.tgSubscribe.ConfigView(r.Context()))
}

func (h *Handler) updateTGSubscribeConfig(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	var req tgsubscribe.ConfigInput
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.tgSubscribe.UpdateConfig(r.Context(), req); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{
		Success: true,
		Message: "配置已保存",
		Data:    h.tgSubscribe.ConfigView(r.Context()),
	})
}

func (h *Handler) testTGSubscribeBot(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	botName, err := h.tgSubscribe.TestBot(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{
		Success: true,
		Message: "连接成功：" + botName,
		Data:    map[string]string{"bot_name": botName},
	})
}

// ————————————————————— 频道 —————————————————————

func (h *Handler) listTGChannels(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	rows, err := h.tgSubscribe.ListChannels(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, rows)
}

func (h *Handler) createTGChannel(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	var req tgsubscribe.ChannelInput
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.tgSubscribe.CreateChannel(r.Context(), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "频道已添加", Data: view})
}

func (h *Handler) updateTGChannel(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var req tgsubscribe.ChannelInput
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.tgSubscribe.UpdateChannel(r.Context(), id, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "频道已更新", Data: view})
}

func (h *Handler) deleteTGChannel(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.tgSubscribe.DeleteChannel(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "频道已删除"})
}

func (h *Handler) testTGChannel(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	probe, err := h.tgSubscribe.TestChannel(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "频道校验通过", Data: probe})
}

// ————————————————————— 订阅 —————————————————————

func (h *Handler) listTGSubscriptions(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	rows, err := h.tgSubscribe.ListSubscriptions(r.Context(), strings.TrimSpace(r.URL.Query().Get("status")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, rows)
}

func (h *Handler) getTGSubscriptionByTMDB(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	q := r.URL.Query()
	view, err := h.tgSubscribe.GetSubscriptionByTMDB(r.Context(), q.Get("tmdb_id"), q.Get("type"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, view)
}

// tgSubscriptionDetail 返回订阅详情（含已收集集数）。
func (h *Handler) getTGSubscription(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.tgSubscribe.GetSubscription(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, view)
}

func (h *Handler) createTGSubscription(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	var req tgsubscribe.SubscriptionInput
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.tgSubscribe.CreateSubscription(r.Context(), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "订阅成功，正在为你追更", Data: view})
}

func (h *Handler) updateTGSubscription(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var req tgsubscribe.SubscriptionInput
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.tgSubscribe.UpdateSubscription(r.Context(), id, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "订阅已更新", Data: view})
}

func (h *Handler) deleteTGSubscription(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.tgSubscribe.DeleteSubscription(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "订阅已取消"})
}

type tgSubscriptionStatusReq struct {
	Status string `json:"status"`
}

func (h *Handler) setTGSubscriptionStatus(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var req tgSubscriptionStatusReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.tgSubscribe.SetSubscriptionStatus(r.Context(), id, req.Status); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "订阅状态已更新"})
}

func (h *Handler) resetTGSubscription(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	removed, err := h.tgSubscribe.ResetSubscription(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{
		Success: true,
		Message: "已重置订阅，清除了 " + strconv.FormatInt(removed, 10) + " 条已收集记录",
		Data:    map[string]int64{"removed": removed},
	})
}

func (h *Handler) listTGSubscriptionEpisodes(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := h.tgSubscribe.ListEpisodes(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, rows)
}

// ————————————————————— 画质方案 —————————————————————

func (h *Handler) listTGQualityProfiles(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	rows, err := h.tgSubscribe.ListQualityProfiles(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, rows)
}

func (h *Handler) createTGQualityProfile(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	var req tgsubscribe.QualityProfileInput
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	id, err := h.tgSubscribe.CreateQualityProfile(r.Context(), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{
		Success: true,
		Message: "画质方案已创建",
		Data:    map[string]int64{"id": id},
	})
}

func (h *Handler) updateTGQualityProfile(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var req tgsubscribe.QualityProfileInput
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.tgSubscribe.UpdateQualityProfile(r.Context(), id, req); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "画质方案已保存"})
}

func (h *Handler) deleteTGQualityProfile(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.tgSubscribe.DeleteQualityProfile(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "画质方案已删除"})
}

type tgQualityPreviewReq struct {
	RawName   string `json:"raw_name"`
	ProfileID int64  `json:"quality_profile_id"`
}

// previewTGQuality 是匹配算法的调试器：粘一段发布名，返回解析结果 + 画质判定 + 候选得分。
// 没有它，用户调画质规则只能靠猜。
func (h *Handler) previewTGQuality(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	var req tgQualityPreviewReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.tgSubscribe.PreviewQuality(r.Context(), req.RawName, req.ProfileID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

// ————————————————————— 匹配历史 —————————————————————

func (h *Handler) listTGRecords(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	q := r.URL.Query()
	filter := tgsubscribe.RecordFilter{
		Status:  strings.TrimSpace(q.Get("status")),
		Keyword: strings.TrimSpace(q.Get("keyword")),
	}
	filter.Limit = queryIntDefault(r, "limit", 50)
	filter.Offset = queryIntDefault(r, "offset", 0)
	if raw := strings.TrimSpace(q.Get("subscription_id")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			filter.SubscriptionID = v
		}
	}
	if raw := strings.TrimSpace(q.Get("channel_id")); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			filter.ChannelID = v
		}
	}

	rows, total, err := h.tgSubscribe.ListRecords(r.Context(), filter)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"items": rows, "total": total})
}

func (h *Handler) getTGRecord(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.tgSubscribe.GetRecord(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, view)
}

type tgManualPushReq struct {
	SubscriptionID int64 `json:"subscription_id"`
}

// pushTGRecord 手动推送。待确认 / 未匹配 / 推送失败的兜底入口 ——
// ambiguous 记录的处理方式就是让用户点一下，而不是系统去赌。
func (h *Handler) pushTGRecord(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var req tgManualPushReq
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeErr(w, err)
			return
		}
	}
	view, err := h.tgSubscribe.ManualPush(r.Context(), id, req.SubscriptionID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "已提交推送", Data: view})
}

func (h *Handler) ignoreTGRecord(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.tgSubscribe.IgnoreRecord(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "已忽略这条记录"})
}

func (h *Handler) clearTGRecords(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	before := time.Now()
	if raw := strings.TrimSpace(r.URL.Query().Get("before")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeErr(w, domain.Errorf(domain.CodeValidation, "before 需为 RFC3339 时间"))
			return
		}
		before = parsed
	}
	n, err := h.tgSubscribe.ClearRecords(r.Context(), before)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{
		Success: true,
		Message: "已清理 " + strconv.FormatInt(n, 10) + " 条历史记录",
		Data:    map[string]int64{"removed": n},
	})
}

func (h *Handler) getTGSubscribeStats(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	writeOK(w, h.tgSubscribe.Stats(r.Context()))
}

// providerSummary 让订阅详情弹窗能提示「这个网盘会不会降级」。
func (h *Handler) getTGProviderSummary(w http.ResponseWriter, r *http.Request) {
	if !h.tgSubscribeReady(w) {
		return
	}
	accountID, err := parseQueryInt64(r, "account_id")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]string{
		"provider": h.tgSubscribe.ProviderSummary(r.Context(), accountID),
	})
}

func queryIntDefault(r *http.Request, name string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}
