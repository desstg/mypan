package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"litepan/internal/domain"
)

func ensureServiceReady(w http.ResponseWriter, ready bool) bool {
	if ready {
		return true
	}
	writeErr(w, domain.Errf(domain.CodeNotImplement))
	return false
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return domain.Errorf(domain.CodeValidation, "请求体解析失败：%v", err)
	}
	return nil
}

func parsePathInt64(r *http.Request, name string) (int64, error) {
	raw := chi.URLParam(r, name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, domain.Errorf(domain.CodeValidation, "非法 %s：%s", name, raw)
	}
	return id, nil
}

func parseQueryInt64(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, domain.Errorf(domain.CodeValidation, "非法 %s：%s", name, raw)
	}
	return id, nil
}

func pathID(r *http.Request) (int64, error) {
	return parsePathInt64(r, "id")
}
