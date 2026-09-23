package api

import (
	"net/http"
	"strconv"
)

// javImage 代理并解码上游图片。
//
// 前端所有番号封面都走这里，不是可有可无的一层 —— 见 internal/jav/image.go
// 顶部那段：上游的图是 XOR 混淆的、Content-Type 也不对，直连显示不出来。
//
// 走管理员会话：这个端点在 requireAdmin 组内，而 <img> 请求会带上同源的 cookie，
// 所以页面上直接引用即可。白名单在 Service 里做（防 SSRF）。
func (h *Handler) javImage(w http.ResponseWriter, r *http.Request) {
	if !h.javReady(w) {
		return
	}
	raw := r.URL.Query().Get("url")
	data, contentType, err := h.jav.FetchImage(r.Context(), raw)
	if err != nil {
		writeErr(w, err)
		return
	}

	// 上游图片是不可变的（URL 带内容指纹），缓存一天。
	// 不缓存的话每翻一页都要重新拉几十张几百 KB 的图。
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(86400))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}
