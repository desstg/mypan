package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"litepan/internal/automation"
	"litepan/internal/domain"
)

type automationValidateDTO struct {
	Actions []automation.RuleAction `json:"actions"`
}

func (h *Handler) listAutomationRules(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.automation != nil) {
		return
	}
	data, err := h.automation.ListRules(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}

func (h *Handler) createAutomationRule(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.automation != nil) {
		return
	}
	var in automation.RuleInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	data, err := h.automation.CreateRule(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}

func (h *Handler) updateAutomationRule(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.automation != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in automation.RuleInput
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	data, err := h.automation.UpdateRule(r.Context(), id, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}

func (h *Handler) deleteAutomationRule(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.automation != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.automation.DeleteRule(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"id": id})
}

func (h *Handler) toggleAutomationRule(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.automation != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	data, err := h.automation.ToggleRule(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}

func (h *Handler) runAutomationRule(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.automation != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	data, err := h.automation.RunAsync(r.Context(), id, "manual")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}

func (h *Handler) validateAutomationRule(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.automation != nil) {
		return
	}
	var in automationValidateDTO
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	data, err := h.automation.ValidateRule(r.Context(), in.Actions)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}

func (h *Handler) listAutomationRuns(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.automation != nil) {
		return
	}
	var (
		ruleID int64
		err    error
	)
	if raw := r.URL.Query().Get("rule_id"); raw != "" {
		ruleID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeErr(w, domain.Errorf(domain.CodeValidation, "非法 rule_id：%s", raw))
			return
		}
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		got, convErr := strconv.Atoi(raw)
		if convErr != nil {
			writeErr(w, domain.Errorf(domain.CodeValidation, "非法 limit：%s", raw))
			return
		}
		limit = got
	}
	data, err := h.automation.ListRuns(r.Context(), ruleID, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}

func (h *Handler) clearAutomationRuns(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.automation != nil) {
		return
	}
	deleted, err := h.automation.ClearRuns(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"deleted": deleted})
}

func (h *Handler) automationOptions(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.automation != nil) {
		return
	}
	data, err := h.automation.ListOptions(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}

func (h *Handler) automationWebhook(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.automation != nil) {
		return
	}
	event, err := decodeWebhookEvent(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	data, err := h.automation.TriggerWebhook(r.Context(), automationAuthHeader(r), event)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, data)
}

func decodeWebhookEvent(r *http.Request) (automation.WebhookEvent, error) {
	var payload map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		return automation.WebhookEvent{}, domain.Errorf(domain.CodeValidation, "请求体解析失败：%v", err)
	}
	var event automation.WebhookEvent
	if raw, ok := payload["event"]; ok {
		if err := json.Unmarshal(raw, &event.Event); err != nil {
			return automation.WebhookEvent{}, domain.Errorf(domain.CodeValidation, "event 字段解析失败：%v", err)
		}
	}
	if raw, ok := payload["source"]; ok {
		if err := json.Unmarshal(raw, &event.Source); err != nil {
			return automation.WebhookEvent{}, domain.Errorf(domain.CodeValidation, "source 字段解析失败：%v", err)
		}
	}
	if raw, ok := payload["path"]; ok {
		if err := json.Unmarshal(raw, &event.Path); err != nil {
			return automation.WebhookEvent{}, domain.Errorf(domain.CodeValidation, "path 字段解析失败：%v", err)
		}
	}
	if raw, ok := payload["delayTime"]; ok {
		_ = json.Unmarshal(raw, &event.DelayTime)
	}
	return event, nil
}

func automationAuthHeader(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth != "" {
		return auth
	}
	if key := r.Header.Get("X-API-Key"); key != "" {
		return "Bearer " + key
	}
	return ""
}
