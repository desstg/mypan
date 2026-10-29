package api

import (
	"io"
	"net/http"
	"os"
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

// javWallSourceRef 从 query 里读数据源选择器（`local_dir_id` 优先，其次 `strm_task_id`）。
//
// 两个都不给时报错 —— 墙必须有数据源才能列东西。
func javWallSourceRef(r *http.Request) (strmscrape.WallSourceRef, error) {
	ref := strmscrape.WallSourceRef{
		StrmTaskID: qInt64(r, "strm_task_id"),
		LocalDirID: strings.TrimSpace(r.URL.Query().Get("local_dir_id")),
	}
	if ref.StrmTaskID <= 0 && ref.LocalDirID == "" {
		return ref, domain.Errorf(domain.CodeValidation, "缺少 strm_task_id 或 local_dir_id")
	}
	return ref, nil
}

// javWallOptionalSourceRef 同上去掉「必须给一个」的校验：给不出就返回零值
// （读侧按「没有数据源」处理，比如「整档隐藏」在 STRM 设置页那一行）。
func javWallOptionalSourceRef(r *http.Request) strmscrape.WallSourceRef {
	ref := strmscrape.WallSourceRef{
		StrmTaskID: qInt64(r, "strm_task_id"),
		LocalDirID: strings.TrimSpace(r.URL.Query().Get("local_dir_id")),
	}
	if ref.StrmTaskID <= 0 {
		ref.StrmTaskID = 0
	}
	return ref
}

// qInt64 读一个 int64 query 参数（缺失 / 非法都当 0）。
func qInt64(r *http.Request, key string) int64 {
	id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get(key)), 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// listJavWallItems 列卡片。
func (h *Handler) listJavWallItems(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	srcRef, err := javWallSourceRef(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.strmScrape.ListJavWall(r.Context(), srcRef, javWallQuery(r), false)
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
	srcRef, err := javWallSourceRef(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.strmScrape.ListJavWall(r.Context(), srcRef, javWallQuery(r), true)
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
	out, err := h.strmScrape.ListJavWallHiddenDirs(r.Context(), javWallOptionalSourceRef(r))
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
		LocalDirID string   `json:"local_dir_id,omitempty"`
		Dirs       []string `json:"dirs"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	srcRef := strmscrape.WallSourceRef{StrmTaskID: body.StrmTaskID, LocalDirID: strings.TrimSpace(body.LocalDirID)}
	if srcRef.StrmTaskID < 0 {
		srcRef.StrmTaskID = 0
	}
	out, err := h.strmScrape.UpdateJavWallHiddenDirs(r.Context(), srcRef, body.Dirs)
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
	StrmTaskID int64 `json:"strm_task_id"`
	// LocalDirID 非空时优先（挂上来的本地目录源）。
	LocalDirID string `json:"local_dir_id,omitempty"`
	RelDir     string `json:"rel_dir"`
	Stem       string `json:"stem"`
}

// normalize 校验并返回 (数据源, relDir, stem)。
func (ref javWallRef) normalize() (strmscrape.WallSourceRef, string, string, error) {
	src := strmscrape.WallSourceRef{StrmTaskID: ref.StrmTaskID, LocalDirID: strings.TrimSpace(ref.LocalDirID)}
	if src.StrmTaskID <= 0 && src.LocalDirID == "" {
		return src, "", "", domain.Errorf(domain.CodeValidation, "缺少 strm_task_id 或 local_dir_id")
	}
	return src, ref.RelDir, ref.Stem, nil
}

// getJavWallItem 读单品（编辑器打开时用）。
func (h *Handler) getJavWallItem(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	srcRef, err := javWallSourceRef(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	out, err := h.strmScrape.JavWallItem(r.Context(), srcRef, q.Get("rel_dir"), q.Get("stem"))
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
	srcRef, relDir, stem, err := body.javWallRef.normalize()
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.strmScrape.SaveJavWallMeta(r.Context(), srcRef, relDir, stem, body.Meta)
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
		// Watermarks 是编辑页上勾的水印 id（4k/8k/leak/sub/umr/ltx 的子集，空 = 不贴）。
		// 多条不影响：水印各自贴各自的角，同一个角只会有一个是"选中的那个"
		// （umr 与 ltx 共用右下角，前端那组单选保证不会同时传）。
		Watermarks []string `json:"watermarks"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	srcRef, relDir, stem, err := body.javWallRef.normalize()
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
	out, err := h.strmScrape.SaveJavWallPoster(r.Context(), srcRef, relDir, stem, body.Rect, body.Watermarks)
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
	srcRef, relDir, stem, err := body.normalize()
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.strmScrape.RebuildJavWallItem(r.Context(), srcRef, relDir, stem)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// onlineScrapeJavWall 「在线刮削」：打上游把缺的元数据补齐，写回 json 与 nfo。
//
// 与「刷新元数据」的区别：那一条只重读本地磁盘（不联网）。
//
// # items 的三态（批量选择）
//
//   - **不带这个键**（老前端 / curl）→ 整根，老行为逐字不变；
//   - **带了、非空** → 只刮点名的这几部；
//   - **带了、空数组** → **校验错误**，绝不静默退化成整根 ——
//     用户明明说「一部都没选」，服务端却把整个库跑一遍，那是十几分钟的白等。
//
// # category（整分类，2026-10-19 加的）
//
//   - **不带这个键**（`nil`）→ 与以前逐字相同；
//   - **带了**（含空串 —— 空串是**根目录**那一档，合法值）→ 刮**整个分类**。
//
// ⚠️ 与 `items` 同时给时，`items` 的角色**从「要刮的」变成「要排除的」**：
// 「这个分类里除了这几部以外都要」。理由是前端手上只有当前那一页 —— 分类的完整
// 成员只有服务端快照里有，所以「取消」只能靠排除表达（见 JavTargetMode）。
//
// ⚠️ 上面那条「空数组 = 校验错误」只适用于**没有** `category` 的时候：
// 带 category 时空数组是合法输入（= 整个分类都要，没有排除项）——
// 前端在「全部取消」时就会发 `items: [], category: "欧美"`，那只是把这一档清空，
// 不是「一部都没选」。硬报 400 会让「取消整个分类」这个动作在服务端炸掉。
func (h *Handler) onlineScrapeJavWall(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	// ⚠️ items / category **必须内嵌在外层**：解内嵌字段的话它们会变成 unknown field
	// （见 javWallRef 上方那段说明）。
	var body struct {
		javWallRef
		Items *[]strmscrape.JavWallScrapeItem `json:"items"`
		// Category 是整分类的目标（指针：空串合法，见上面那段 ⚠️）。
		Category *string `json:"category"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	srcRef, _, _, err := body.javWallRef.normalize()
	if err != nil {
		writeErr(w, err)
		return
	}
	var items []strmscrape.JavWallScrapeItem
	if body.Items != nil {
		items = *body.Items
		// 没有 category 时的空数组才是错的（见上面那段 ⚠️）。
		if len(items) == 0 && body.Category == nil {
			writeErr(w, domain.Errorf(domain.CodeValidation, "没有选中任何一部"))
			return
		}
		if len(items) > strmscrape.MaxJavWallScrapeItems {
			writeErr(w, domain.Errorf(domain.CodeValidation,
				"一次最多刮削 %d 部，当前选中 %d 部", strmscrape.MaxJavWallScrapeItems, len(items)))
			return
		}
	}
	// **后台任务**：立刻返回进度，活在后头跑（前端轮询进度那条接口）。
	//
	// 原来是同步跑完再返回 —— 108 部要十几分钟，群晖那层反代等不了就回 502
	// （实测跑到 20 部左右）。见 StartJavWallOnlineScrape 的说明。
	out, err := h.strmScrape.StartJavWallOnlineScrape(r.Context(), srcRef, items, body.Category)
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
	writeOK(w, h.strmScrape.JavOnlineScrapeProgressByRef(javWallOptionalSourceRef(r)))
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
	srcRef, relDir, stem, err := body.normalize()
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.strmScrape.RefreshJavWallItem(r.Context(), srcRef, relDir, stem)
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
	srcRef, err := javWallSourceRef(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	out, err := h.strmScrape.JavWallDetail(r.Context(), srcRef, q.Get("rel_dir"), q.Get("stem"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// previewJavWallDelete 「删除」前的**会删掉什么**。不写任何东西。
//
// 单独一步而不是让前端自己拼文案：删什么由服务端算（哪些文件属于这一部、
// 目录会不会一起删、网盘那边删的是哪个路径），前端只负责展示与二次确认。
// 前端拼的话，两边的判据迟早会漂 —— 而这一漂就是「提示说删 3 个文件、
// 实际删了 12 个」这种事故。
func (h *Handler) previewJavWallDelete(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var body javWallRef
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	srcRef, relDir, stem, err := body.normalize()
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.strmScrape.PreviewJavWallDelete(r.Context(), srcRef, relDir, stem)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// deleteJavWallItem 「删除」：本地文件 + 目录（空了才删）+ 网盘那一份目录。
//
// ⚠️ 这个接口**会真的删东西**（本地不可恢复、网盘进回收站）。前端必须先走
// preview 那条、把清单摆给用户、拿到确认之后才调这里 —— 服务端不重复弹窗，
// 但也**不额外做「确认令牌」那一层**：这是后台管理接口，调用方就是管理员自己。
func (h *Handler) deleteJavWallItem(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	var body javWallRef
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	srcRef, relDir, stem, err := body.normalize()
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.strmScrape.DeleteJavWallItem(r.Context(), srcRef, relDir, stem)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// listJavWallActors 演员墙的一页。
//
// 与其它 jav-wall 接口**不同**：它**不带数据源**。演员墙要的是「所有番号墙上的演员」，
// 不是当前选中那一面 —— 多一面墙就少一半人，用户打开它就是为了看全集。
//
// `include_male=1` 才列男演员：默认不列（女优占绝大多数，男优混进来只冲稀列表）。
//
// `anchor=<key>` 是「点名」：不管关键词与男优口径，这一位一定在结果里。演员详情
// 里点别名就是走它 —— 那些别名常常来自 avbase / JAVDB，墙上的索引里没有这个写法。
func (h *Handler) listJavWallActors(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	includeMale := q.Get("include_male") == "1" || q.Get("include_male") == "true"
	out := h.strmScrape.ListJavWallActors(r.Context(), q.Get("keyword"), page, pageSize, includeMale, q.Get("anchor"))
	writeOK(w, out)
}

// getJavWallActorDetail 一位演员 + 他参演的墙上影片。
//
// `key` 是列表里原样带回来的那个（有上游 id 就是 id，否则 `name:<名字>`）——
// 走 query 而不是路径段，因为名字里可能有空格与斜杠。
//
// 响应里还可能带 `actor.tmdb`：TMDB 那边的同名档案（简介 / 生日 / 别名）。
// 那是**补充**，查不到就没有这个字段。
func (h *Handler) getJavWallActorDetail(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	out, err := h.strmScrape.JavWallActorDetail(r.Context(), r.URL.Query().Get("key"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// getJavWallActorTMDB 一位演员的 TMDB 补充档案（简介 / 生日 / 出生地 / 别名）。
//
// **单独一条路**，与详情分开：TMDB 可能很慢、也可能不通，而「这位演员演过墙上的
// 哪几部」不该陪着它一起等。查不到时 `data` 是 null（不是错误）。
func (h *Handler) getJavWallActorTMDB(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	writeOK(w, h.strmScrape.ActorTMDB(r.Context(), r.URL.Query().Get("key")))
}

// getJavWallActorProfile 一位演员的**资料档案**（生日 / 出身 / 三围 / 头像 / 别名）。
//
// 与 TMDB 那条一样是**独立的补充请求**，理由也相同：它要打外站（avbase / JAVDB），
// 慢和不通都是常态，不该拖着「名字 + 参演影片」一起等。查不到时 `data` 是 null。
//
// 前端打开演员详情时并行发这一条，回来就补上面板；没回来也就少一段资料。
func (h *Handler) getJavWallActorProfile(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	writeOK(w, h.strmScrape.ActorProfile(r.Context(), r.URL.Query().Get("key")))
}

// getJavWallActorPhoto 代理 TMDB 人像（`actor.tmdb.profile_path`）。
//
// 与 TG 订阅的海报代理同一条理由：image.tmdb.org 在国内浏览器经常打不开，
// 所以演员详情页的人像一律经后端转发（后端带全局代理设置）。
// ⚠️ 只接受以 `/` 开头的 TMDB 路径（`normalizeActorPhotoSize` 限定尺寸）——
// 这个参数不能变成一个「替服务器打任意地址」的入口。
func (h *Handler) getJavWallActorPhoto(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	q := r.URL.Query()
	data, contentType, err := h.strmScrape.FetchJavActorPhoto(r.Context(), q.Get("path"), q.Get("size"))
	if err != nil {
		writeErr(w, err)
		return
	}
	// TMDB 的图不可变（路径带内容哈希），缓存一天。
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}

// 演员的**自定义头像**：用户在演员详情页自己传一张，盖过侧车 / 资料层那只。
//
// 三条路由共用 `/jav-wall/actor/avatar`，用方法与参数区分：
//
//	POST   表单字段 file + query key  → 存（返回新的地址）
//	DELETE query key                  → 删（回到侧车 / 资料层那张）
//	GET    query key                  → 取图（image/jpeg）
//
// ⚠️ 上传的是**用户当时那台设备上的**文件，但服务端会把它重新编码后落盘
// （见 strmscrape.SaveJavActorAvatar），之后与原文件、原设备无关 ——
// 换手机 / 换电脑 / 清空相册都不影响显示。

// saveJavWallActorAvatar 上传一张自定义头像。
func (h *Handler) saveJavWallActorAvatar(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	max := strmscrape.JavActorAvatarMaxBytes
	// 两道闸：MaxBytesReader 挡住超大请求体（连解析都不做），读完再核一次实际字节数
	// （multipart 的边界、头部也算进 body，所以给 1 MB 余量）。
	r.Body = http.MaxBytesReader(w, r.Body, max+(1<<20))
	if err := r.ParseMultipartForm(max); err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "读取上传的图片失败（可能超过 %d MB）", max>>20))
		return
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		key = strings.TrimSpace(r.FormValue("key"))
	}
	if key == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "缺少演员标识"))
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "请选择要上传的图片"))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "读取上传的图片失败"))
		return
	}
	if int64(len(data)) > max {
		writeErr(w, domain.Errorf(domain.CodeValidation, "图片超过 %d MB 上限", max>>20))
		return
	}
	out, err := h.strmScrape.SaveJavActorAvatar(r.Context(), key, data)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, out)
}

// deleteJavWallActorAvatar 删掉自定义头像（回到侧车 / 资料层那张）。
//
// 幂等：本来就没有也不算错 —— 前端点「恢复默认」时不该看到一个报错。
func (h *Handler) deleteJavWallActorAvatar(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	writeOK(w, h.strmScrape.DeleteJavActorAvatar(r.URL.Query().Get("key")))
}

// getJavWallActorAvatar 取自定义头像的字节。
//
// 地址里的 `v=<mtime>` 让换图之后浏览器拿新的（见 actorAvatarURL），所以这里可以
// 放心缓存一天。
func (h *Handler) getJavWallActorAvatar(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmScrape != nil) {
		return
	}
	path, ok := h.strmScrape.ResolveJavActorAvatarFile(r.URL.Query().Get("key"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// 存的时候一律是重新编码的 JPEG（见 normalizeActorAvatar），类型是确定的。
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}
