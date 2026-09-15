package adminauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/store"
	"litepan/pkg/security"
)

func newTestAuth(t *testing.T) (*Service, domain.ConfigRepository, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(db)
	_ = st.Configs.Set(ctx, KeyAdminUsername, "admin")
	_ = st.Configs.Set(ctx, KeyAdminPassword, security.HashPassword("changed-secret"))
	return New(st.Configs, []byte("test-secret-key-min-16b"), nil), st.Configs, ctx
}

func newBareTestAuth(t *testing.T) (*Service, domain.ConfigRepository, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(db)
	return New(st.Configs, []byte("test-secret-key-min-16b"), nil), st.Configs, ctx
}

func TestDefaultAdminPasswordIsInitializedAsHashAndMustChange(t *testing.T) {
	svc, configs, ctx := newBareTestAuth(t)

	username, storedPassword := svc.adminCredentials(ctx)
	if username != defaultAdminUsername {
		t.Fatalf("username = %q, want %q", username, defaultAdminUsername)
	}
	if !security.IsPasswordHash(storedPassword) {
		t.Fatalf("default password should be stored as hash, got %q", storedPassword)
	}
	if !security.VerifyAdminPassword(storedPassword, defaultAdminPassword) {
		t.Fatal("hashed default password should verify admin password")
	}
	persisted, ok, err := configs.Get(ctx, KeyAdminPassword)
	if err != nil {
		t.Fatalf("get persisted password: %v", err)
	}
	if !ok || persisted != storedPassword {
		t.Fatalf("persisted password = %q, ok=%v, want stored hash %q", persisted, ok, storedPassword)
	}

	state := svc.credentialState(ctx)
	if !state.MustChangePassword {
		t.Fatal("default admin should still require password change")
	}
	if state.PasswordChangeReason != "default_credentials" {
		t.Fatalf("reason = %q, want default_credentials", state.PasswordChangeReason)
	}
}

func TestPublicIndexIsDisabledByDefault(t *testing.T) {
	svc, configs, ctx := newBareTestAuth(t)
	if svc.publicIndexEnabled(ctx) {
		t.Fatal("public index should be disabled by default")
	}
	if err := configs.Set(ctx, KeyPublicIndexEnabled, "true"); err != nil {
		t.Fatalf("enable public index: %v", err)
	}
	if !svc.publicIndexEnabled(ctx) {
		t.Fatal("saved public index setting should override the default")
	}
}

func TestReadSessionExpiresAfterTimeout(t *testing.T) {
	svc, configs, ctx := newTestAuth(t)
	_ = configs.Set(ctx, KeySessionTimeout, "1800")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := svc.WriteSession(rec, req, Session{IsAdmin: true, Username: "admin"}, false); err != nil {
		t.Fatalf("write session: %v", err)
	}
	cookie := rec.Result().Cookies()[0]

	checkReq := httptest.NewRequest(http.MethodGet, "/", nil)
	checkReq.AddCookie(cookie)
	if _, ok := svc.ReadSession(checkReq); !ok {
		t.Fatal("session should be valid immediately after login")
	}

	sess, ok := svc.ReadSession(checkReq)
	if !ok || sess == nil {
		t.Fatal("expected session payload")
	}
	sess.CreatedAt = time.Now().Add(-31 * time.Minute).Format(time.RFC3339)
	rec2 := httptest.NewRecorder()
	if err := svc.WriteSession(rec2, checkReq, *sess, false); err != nil {
		t.Fatalf("rewrite session: %v", err)
	}

	expiredReq := httptest.NewRequest(http.MethodGet, "/", nil)
	expiredReq.AddCookie(rec2.Result().Cookies()[0])
	if _, ok := svc.ReadSession(expiredReq); ok {
		t.Fatal("session should expire after configured timeout")
	}
}

func TestUpdateCredentialsPreservesSessionCreatedAt(t *testing.T) {
	svc, configs, ctx := newTestAuth(t)
	_ = configs.Set(ctx, KeySessionTimeout, "1800")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	createdAt := time.Now().Add(-20 * time.Minute).Format(time.RFC3339)
	if err := svc.WriteSession(rec, req, Session{
		IsAdmin:   true,
		Username:  "admin",
		CreatedAt: createdAt,
	}, false); err != nil {
		t.Fatalf("write session: %v", err)
	}
	cookie := rec.Result().Cookies()[0]

	updateReq := httptest.NewRequest(http.MethodPost, "/api/admin/update-credentials", nil)
	updateReq.AddCookie(cookie)
	sess, ok := svc.ReadSession(updateReq)
	if !ok {
		t.Fatal("expected active session before update")
	}
	timeout := 0.5
	updateRec := httptest.NewRecorder()
	if err := svc.UpdateCredentials(ctx, updateReq, updateRec, UpdateCredentialsRequest{
		AdminUsername:  "admin",
		SessionTimeout: &timeout,
	}, sess); err != nil {
		t.Fatalf("update credentials: %v", err)
	}

	afterReq := httptest.NewRequest(http.MethodGet, "/", nil)
	afterReq.AddCookie(updateRec.Result().Cookies()[0])
	got, ok := svc.ReadSession(afterReq)
	if !ok || got == nil {
		t.Fatal("session should remain valid after settings save")
	}
	if got.CreatedAt != createdAt {
		t.Fatalf("CreatedAt changed after settings save: got %q want %q", got.CreatedAt, createdAt)
	}
}

func TestUpdateCredentialsClearsMustChangePassword(t *testing.T) {
	svc, _, ctx := newTestAuth(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := svc.WriteSession(rec, req, Session{
		IsAdmin:              true,
		Username:             "admin",
		MustChangePassword:   true,
		PasswordChangeReason: "default_credentials",
	}, false); err != nil {
		t.Fatalf("write session: %v", err)
	}
	cookie := rec.Result().Cookies()[0]

	updateReq := httptest.NewRequest(http.MethodPost, "/api/admin/update-credentials", nil)
	updateReq.AddCookie(cookie)
	sess, ok := svc.ReadSession(updateReq)
	if !ok {
		t.Fatal("expected active session before update")
	}
	updateRec := httptest.NewRecorder()
	if err := svc.UpdateCredentials(ctx, updateReq, updateRec, UpdateCredentialsRequest{
		AdminUsername: "admin",
		AdminPassword: "new-secret",
	}, sess); err != nil {
		t.Fatalf("update credentials: %v", err)
	}

	afterReq := httptest.NewRequest(http.MethodGet, "/", nil)
	afterReq.AddCookie(updateRec.Result().Cookies()[0])
	got, ok := svc.ReadSession(afterReq)
	if !ok || got == nil {
		t.Fatal("session should remain valid after password change")
	}
	if got.MustChangePassword {
		t.Fatal("session must_change_password should be cleared after password upgrade")
	}
	st := svc.Status(ctx, afterReq)
	if st.MustChangePassword {
		t.Fatal("status must_change_password should be false after password upgrade")
	}
}

func TestUpdateCredentialsValidationFailureDoesNotPersistAnySetting(t *testing.T) {
	svc, configs, ctx := newTestAuth(t)
	if err := configs.Set(ctx, KeyPublicIndexEnabled, "true"); err != nil {
		t.Fatalf("set public index: %v", err)
	}

	publicIndexEnabled := false
	invalidConcurrency := 6
	err := svc.UpdateCredentials(
		ctx,
		httptest.NewRequest(http.MethodPost, "/api/admin/update-credentials", nil),
		httptest.NewRecorder(),
		UpdateCredentialsRequest{
			AdminUsername:         "changed_admin",
			PublicIndexEnabled:    &publicIndexEnabled,
			UploadTaskConcurrency: &invalidConcurrency,
		},
		nil,
	)
	if err == nil {
		t.Fatal("expected invalid concurrency to reject the whole update")
	}

	username, ok, getErr := configs.Get(ctx, KeyAdminUsername)
	if getErr != nil {
		t.Fatalf("get username: %v", getErr)
	}
	if !ok || username != "admin" {
		t.Fatalf("username changed after rejected update: got %q, ok=%v", username, ok)
	}
	publicIndex, ok, getErr := configs.Get(ctx, KeyPublicIndexEnabled)
	if getErr != nil {
		t.Fatalf("get public index: %v", getErr)
	}
	if !ok || publicIndex != "true" {
		t.Fatalf("public index changed after rejected update: got %q, ok=%v", publicIndex, ok)
	}
}

func TestDefaultCredentialsMayOnlyUseBootstrapRestoreEndpoints(t *testing.T) {
	svc, _, ctx := newBareTestAuth(t)
	sess := &Session{
		IsAdmin:              true,
		Username:             "admin",
		MustChangePassword:   true,
		PasswordChangeReason: "default_credentials",
	}

	allowed := []string{
		"/api/admin/backups/import",
		"/api/admin/backups/11111111-1111-1111-1111-111111111111/restore",
		"/api/admin/backups/restart",
	}
	for _, path := range allowed {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		if err := svc.EnsureAdminAccess(ctx, req, sess); err != nil {
			t.Fatalf("bootstrap restore path %q rejected: %v", path, err)
		}
	}

	blocked := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/admin/backups/"},
		{http.MethodPost, "/api/admin/backups/"},
		{http.MethodDelete, "/api/admin/backups/pending"},
		{http.MethodPost, "/api/admin/accounts"},
		{http.MethodGet, "/api/admin/backups/import"},
	}
	for _, item := range blocked {
		req := httptest.NewRequest(item.method, item.path, nil)
		if err := svc.EnsureAdminAccess(ctx, req, sess); err == nil {
			t.Fatalf("locked default session unexpectedly accessed %s %s", item.method, item.path)
		}
	}
}

func TestTemporaryPasswordCannotUseBootstrapRestoreEndpoints(t *testing.T) {
	svc, _, ctx := newTestAuth(t)
	sess := &Session{
		IsAdmin:              true,
		Username:             "admin",
		MustChangePassword:   true,
		PasswordChangeReason: "temporary_password",
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/backups/import", nil)
	if err := svc.EnsureAdminAccess(ctx, req, sess); err == nil {
		t.Fatal("temporary password session must not use bootstrap restore endpoints")
	}
}
