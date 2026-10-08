package api

import (
	"net/http"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/jav/emby"
	"litepan/internal/strmscrape"
)

// 番号影片的海报墙（辅助工具 → 海报墙，选中「媒体类型 = 番号影片」的任务时走这一套）。
//
// 与 TMDB 那套（strm_scrape.go）共用同一个页面与同一个服务，但数据源完全不同：
// 这里是**扫本地媒体库目录**（`.strm` 主干 + 四个产物的有无），不碰 TMDB、不落库。

// javWallQuery 从 query 里取列表条件。列表与「刷新元数据」共用。
func javWallQuery(r *http.Request) strmscrape.JavWallListQuery {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	// task_id 单独取：它在 handler 里已经解析过了，这里只管列表条件。
	return strmscrape.JavWallListQuery{
		Category: q.Get("category"),
		Keyword:  q.Get("keyword"),
		Sort:     q.Get("sort"),
		Offset:   offset,
		Limit:    limit,
	}
}

func javWallTaskID(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("strm_task_id"))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, domain.Errorf(domain.CodeValidation, "缺少或非法的 strm_task_id")
	}
	return id, nil
}

// listJavWallItems 列卡片。
func (h *Handler) listJavWallItems(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	taskID, err := javWallTaskID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.strmScrape.ListJavWall(r.Context(), taskID, javWallQuery(r), false)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// refreshJavWall 「刷新元数据」：作废快照、重读磁盘、按同一套条件重列。
//
// **它不是重新生成**（那是卡片上的「重刮」）：这里只重新读本地文件，不联网、
// 不重建 nfo/图片，作用是把「刚保存的东西」显示出来（图片 URL 上的 rev 也跟着换）。
func (h *Handler) refreshJavWall(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	taskID, err := javWallTaskID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.strmScrape.ListJavWall(r.Context(), taskID, javWallQuery(r), true)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// listJavWallHiddenDirs 列「整档隐藏的一级目录」候选（勾选界面用）。
//
// strm_task_id **可以不带**（或传 0）：那样就不扫盘，只按分类规则的目标目录列名单 ——
// STRM 设置页那一行走这条（那里手上没有任务）。
func (h *Handler) listJavWallHiddenDirs(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	taskID := javWallOptionalTaskID(r)
	out, err := h.strmScrape.ListJavWallHiddenDirs(r.Context(), taskID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// updateJavWallHiddenDirs 保存名单（勾上 = 隐藏）。
func (h *Handler) updateJavWallHiddenDirs(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var body struct {
		StrmTaskID int64    `json:"strm_task_id"`
		Dirs       []string `json:"dirs"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	taskID := body.StrmTaskID
	if taskID < 0 {
		taskID = 0
	}
	out, err := h.strmScrape.UpdateJavWallHiddenDirs(r.Context(), taskID, body.Dirs)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// javWallOptionalTaskID 取可选的 strm_task_id：缺省 / 0 / 非法一律当「不指定」。
//
// 与 javWallTaskID 的区别是它不报错 —— 「不指定任务」在这些接口上是合法输入。
func javWallOptionalTaskID(r *http.Request) int64 {
	raw := strings.TrimSpace(r.URL.Query().Get("strm_task_id"))
	if raw == "" {
		return 0
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// javWallRef 是「哪一部」那三个字段。
//
// **必须能被外层 body 内嵌**：各 handler 的 body 是 `struct{ javWallRef; 自己的字段 }`，
// 而解码要解**外层**那个整体 —— 解内嵌字段的话，自己的字段（比如 rect）就成了
// 「unknown field」（`decodeJSON` 开了 DisallowUnknownFields），用户看到的是
// 一句「请求体解析失败：json: unknown field "rect"」。这个坑实测踩过。
type javWallRef struct {
	StrmTaskID int64  `json:"strm_task_id"`
	RelDir     string `json:"rel_dir"`
	Stem       string `json:"stem"`
}

// normalize 校验并返回规范化的三元组。
func (ref javWallRef) normalize() (int64, string, string, error) {
	if ref.StrmTaskID <= 0 {
		return 0, "", "", domain.Errorf(domain.CodeValidation, "缺少 strm_task_id")
	}
	return ref.StrmTaskID, ref.RelDir, ref.Stem, nil
}

// getJavWallItem 读单品（编辑器打开时用）。
func (h *Handler) getJavWallItem(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	taskID, err := javWallTaskID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	out, err := h.strmScrape.JavWallItem(r.Context(), taskID, q.Get("rel_dir"), q.Get("stem"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// saveJavWallMeta 保存元数据 → 重写 `<主干>.nfo`。
func (h *Handler) saveJavWallMeta(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var body struct {
		javWallRef
		Meta *emby.MovieMeta `json:"meta"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	taskID, relDir, stem, err := body.javWallRef.normalize()
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.strmScrape.SaveJavWallMeta(r.Context(), taskID, relDir, stem, body.Meta)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// saveJavWallPoster 保存海报裁剪 → 覆盖 `poster.jpg`。
func (h *Handler) saveJavWallPoster(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var body struct {
		javWallRef
		Rect emby.CropRect `json:"rect"`
		// Watermarks 是编辑页上勾的水印 id（4k/8k/leak/sub/umr 的子集，空 = 不贴）。
		// 多条不影响：水印各自贴各自的角，同一个角只会有一个是"选中的那个"。
		Watermarks []string `json:"watermarks"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	taskID, relDir, stem, err := body.javWallRef.normalize()
	if err != nil {
		writeErr(w, err)
		return
	}
	// 窗口值必须是有意义的正数：前端在「框还没量到尺寸」时算出来的会是 0/NaN
	// （NaN 过 JSON 变 null，解 int 就已经报错了），这里再兜一道并给出人话，
	// 免得用户看到的是一句"请求体解析失败"却不知道哪儿不对。
	if body.Rect.W <= 0 || body.Rect.H <= 0 || body.Rect.X < 0 || body.Rect.Y < 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation,
			"裁剪窗口无效（x=%d y=%d w=%d h=%d）：请等图片显示出来、拖动好框之后再点「裁剪」",
			body.Rect.X, body.Rect.Y, body.Rect.W, body.Rect.H))
		return
	}
	out, err := h.strmScrape.SaveJavWallPoster(r.Context(), taskID, relDir, stem, body.Rect, body.Watermarks)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// rebuildJavWallItem 「重刮」：用本地那份 json 重建这一部。
func (h *Handler) rebuildJavWallItem(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var body javWallRef
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	taskID, relDir, stem, err := body.normalize()
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.strmScrape.RebuildJavWallItem(r.Context(), taskID, relDir, stem)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// refreshJavWallItem 只重读这一部（保存后刷一张卡）。
// onlineScrapeJavWall 「在线刮削」：打上游把目录下缺的元数据补齐，写回 json 与 nfo。
//
// 与「刷新元数据」的区别：那一条只重读本地磁盘（不联网）。
func (h *Handler) onlineScrapeJavWall(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var body javWallRef
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	taskID, _, _, err := body.normalize()
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.strmScrape.JavWallOnlineScrape(r.Context(), taskID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// getJavWallOnlineProgress 轮询在线刮削的进度（前端按钮上那个「刮削中…」）。
func (h *Handler) getJavWallOnlineProgress(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	taskID, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("strm_task_id")), 10, 64)
	writeOK(w, h.strmScrape.JavOnlineScrapeProgressOf(taskID))
}

func (h *Handler) refreshJavWallItem(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var body javWallRef
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	taskID, relDir, stem, err := body.normalize()
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.strmScrape.RefreshJavWallItem(r.Context(), taskID, relDir, stem)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// getJavWallItemDetail 读一部的详情（番号影片墙的抽屉）。
//
// 与 getJavWallItem 的区别：那个是**编辑器**的入参（要表单形状），
// 这个是**抽屉**的（演员带头像、剧照一串地址、标签、可播文件）。
func (h *Handler) getJavWallItemDetail(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	taskID, err := javWallTaskID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	out, err := h.strmScrape.JavWallDetail(r.Context(), taskID, q.Get("rel_dir"), q.Get("stem"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}
