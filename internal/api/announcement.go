package api

import (
	"net/http"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/settings"
)

type markAnnouncementReadRequest struct {
	NoticeVersion string `json:"notice_version"`
}

// getAnnouncement 返回当前公告。未配置（enabled=false）或拉取失败/无内容时 item 为 null，
// 前端据此不弹窗；本接口本身不报错，保证公告不可用时后台无感。
func (h *Handler) getAnnouncement(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.announcement != nil) {
		return
	}
	enabled := h.announcement.Enabled()
	if !enabled {
		writeOK(w, map[string]any{"enabled": false, "item": nil, "read": false})
		return
	}
	item, err := h.announcement.Fetch(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	if item == nil {
		// 拉取失败是**静默降级**的（见 announcement 包注释）：前端只会看到
		// 「暂无公告」，和「本来就还没发公告」长得一模一样。实测群晖那台
		// 就是靠翻代码才定位到「是拉不到远端」。这里补一条日志，
		// 不改前端行为，但下次再出这种事日志里一眼可见。
		//
		// 地址一起记下来：绝大多数失败原因都在那个地址本身
		// （默认的 raw.githubusercontent.com 在国内常常不可达）。
		h.log.Warn("后台公告拉取失败，本次无内容可展示",
			"url", h.announcement.URL())
	}
	read := false
	if item != nil {
		read = isAnnouncementVersionRead(h.settings, item.Version)
	}
	writeOK(w, map[string]any{"enabled": true, "item": item, "read": read})
}

func isAnnouncementVersionRead(svc *settings.Service, version string) bool {
	version = strings.TrimSpace(version)
	return svc != nil && version != "" && strings.TrimSpace(svc.String(settings.KeyAnnouncementReadVersion)) == version
}

// markAnnouncementRead 将用户实际关闭的公告版本写入现有 settings 表。
// 版本号由当前已展示内容携带，避免远端公告更新时误将未读新版本标记为已读。
func (h *Handler) markAnnouncementRead(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.settings != nil) {
		return
	}
	var in markAnnouncementReadRequest
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	version := strings.TrimSpace(in.NoticeVersion)
	if version == "" {
		writeErr(w, domain.Errorf(domain.CodeValidation, "公告版本不能为空"))
		return
	}
	if len(version) > 256 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "公告版本过长"))
		return
	}
	if isAnnouncementVersionRead(h.settings, version) {
		writeOK(w, map[string]string{"notice_version": version})
		return
	}
	if err := h.settings.UpdateSilent(r.Context(), map[string]string{
		settings.KeyAnnouncementReadVersion: version,
	}); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]string{"notice_version": version})
}
