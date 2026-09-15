package api

import (
	"context"
	"net/http"

	"litepan/internal/adminauth"
	"litepan/internal/domain"
)

type adminSessionCtxKey struct{}

func adminSessionFromContext(ctx context.Context) *adminauth.Session {
	sess, _ := ctx.Value(adminSessionCtxKey{}).(*adminauth.Session)
	return sess
}

func (h *Handler) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := h.adminAuth.ReadSession(r)
		if !ok {
			writeErr(w, domain.Errorf(domain.CodeAdminAuthRequired, "需要管理员权限"))
			return
		}
		if err := h.adminAuth.EnsureAdminAccess(r.Context(), r, sess); err != nil {
			writeErr(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminSessionCtxKey{}, sess)))
	})
}

func (h *Handler) requirePublicOrAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, err := h.adminAuth.EnsurePublicOrAdmin(r.Context(), r)
		if err != nil {
			writeErr(w, err)
			return
		}
		if sess != nil {
			r = r.WithContext(context.WithValue(r.Context(), adminSessionCtxKey{}, sess))
		}
		next.ServeHTTP(w, r)
	})
}
