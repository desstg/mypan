package api

import (
	"net/http"
	"strings"
)

// javUserActivity 记一笔「用户正在用番号功能」，给后台三个 sweep 循环避让用
// （reviewSweep / magnetSweep / summaryBackfill，判据见 internal/jav/loops.go 的
// userActivityWindow）。
//
// # 为什么在 API 层打点，而不是让 javdb 客户端自己认「谁发的请求」
//
// 三个后台循环**不经过 HTTP handler**，所以「handler 被调到」这件事天然等价于
// 「用户在操作」—— 不需要在客户端里塞 context 标记去区分发送方，也就不会出现
// 「标记漏了一处 → 闸门静默失效」那种坑。
//
// 这条判据的来历：2026-09-30 之前三个循环用的是 `javdb.Client.LastUsedAt`
// （由**所有**上游请求推动，含后台自己的），实测避让**一次都没生效**
// （日志里 `paused (user active)` 零条，而后台从 0 点到 9 点每小时都在跑）。
//
// # 为什么必须放过 `?local=1`
//
// `GET /movies/{id}?local=1` 是**本地读**（Service.DetailLocal，只查库），
// 而详情抽屉每 3 秒轮询它一次（web/src/components/admin/JavMovieDrawer.vue 的
// DETAIL_POLL_MS = 3000，最多 20 跳），推送状态那条更久（PUSH_POLL_MS = 30s，
// 有在途磁链时会一直续）。那些轮询**不代表用户在操作** —— 人可能开着抽屉去吃饭了。
// 无条件打点的话，用户开着详情页不动，后台补缺就**永远**跑不起来
// （每 3 秒被推后一次，5 分钟的窗口永远不会过期）。
//
// 判据按**查询参数**而不是路由：同一个 `GET /movies/{id}` 带不带 `local=1`
// 是两条完全不同的路（一条读库、一条打上游），路由层分不开。
func (h *Handler) javUserActivity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.jav != nil && !isJavLocalRead(r) {
			h.jav.TouchUserActivity()
		}
		next.ServeHTTP(w, r)
	})
}

// isJavLocalRead 报告这次请求是不是「只读本地」的那种轮询。
//
// 前端只在这两个端点上带 `local=1`，且都来自抽屉里那两条定时轮询
// （见 JavMovieDrawer 的 DETAIL_POLL_MS / PUSH_POLL_MS 与 loadMagnets）。
//
// 判据是「GET + local=1」，不看路径 —— 这个参数在番号这一组里只有这两处使用，
// 而**漏放过一处的代价是后台永远不跑**（每 3 秒推后一次，5 分钟的窗口永不过期），
// 比误放过几处的代价大得多。将来若有人拿 `local=1` 表示别的意思，
// 那应该换个参数名，而不是让这条闸门去猜。
//
// # 为什么「番号海报墙」不在打点范围
//
// `/api/admin/strm-scrape/jav-wall/*`（StrmJavWall.vue 会调）走的是 strmscrape
// 那一组路由，**不在 `/jav` 这棵树下**，所以不会被这里打点。这是对的，不是漏挂：
// 它调的是 `strm.JavImageFetcher` → `jav.FetchImage`，**图片代理**走的是另一个
// 无配额客户端（Service.imageClient），不抢 javdb 那条 `minInterval` 限流通道。
// 给它打点只会让「有人在看海报墙」被误判成「有人在搜片」。
func isJavLocalRead(r *http.Request) bool {
	if r == nil || r.Method != http.MethodGet {
		return false
	}
	return strings.TrimSpace(r.URL.Query().Get("local")) == "1"
}
