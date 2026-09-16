package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient(ClientOptions{Token: "test-token", APIHost: srv.URL})
}

func TestClientParsesSuccessfulResult(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/bottest-token/getMe") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":42,"is_bot":true,"username":"mybot"}}`))
	})
	me, err := c.GetMe(context.Background())
	if err != nil {
		t.Fatalf("getMe: %v", err)
	}
	if me.ID != 42 || me.Username != "mybot" || !me.IsBot {
		t.Fatalf("unexpected user: %+v", me)
	}
}

// Webhook 一旦设置，getUpdates 会返回 409。必须能把它识别出来，
// 否则用户只会看到一个「服务内部错误」，不知道该去 deleteWebhook。
func TestClientTranslatesConflictIntoActionableError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":409,"description":"Conflict: can't use getUpdates method while webhook is active"}`))
	})
	_, err := c.GetUpdates(context.Background(), 1, 0, 10)
	if err == nil {
		t.Fatal("expected an error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if !apiErr.IsConflict() {
		t.Fatalf("expected conflict, got code %d", apiErr.Code)
	}
	if !strings.Contains(apiErr.Description, "webhook") {
		t.Fatalf("description should be preserved, got %q", apiErr.Description)
	}
}

func TestClientSurfacesRetryAfter(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`))
	})
	_, err := c.GetUpdates(context.Background(), 1, 0, 10)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.IsRetryAfter() || apiErr.RetryAfter != 7 {
		t.Fatalf("expected retry_after=7, got %#v (err %v)", apiErr, err)
	}
}

func TestClientRejectsNonJSONBody(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>bad gateway</html>`))
	})
	_, err := c.GetMe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "不是合法 JSON") {
		t.Fatalf("expected a readable parse error, got %v", err)
	}
}

// 长轮询必须带上 offset / timeout / limit，并且只要频道帖子。
func TestGetUpdatesQueryParams(t *testing.T) {
	var got map[string]string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = map[string]string{}
		for k := range r.URL.Query() {
			got[k] = r.URL.Query().Get(k)
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":11,"channel_post":{"message_id":1,"chat":{"id":-100,"type":"channel"}}}]}`))
	})
	updates, err := c.GetUpdates(context.Background(), -1, 30, 100)
	if err != nil {
		t.Fatalf("getUpdates: %v", err)
	}
	if got["offset"] != "-1" || got["timeout"] != "30" || got["limit"] != "100" {
		t.Fatalf("unexpected query: %+v", got)
	}
	var allowed []string
	if err := json.Unmarshal([]byte(got["allowed_updates"]), &allowed); err != nil {
		t.Fatalf("allowed_updates should be a JSON array: %v", err)
	}
	for _, want := range []string{"channel_post", "edited_channel_post"} {
		found := false
		for _, a := range allowed {
			if a == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("allowed_updates missing %s: %v", want, allowed)
		}
	}
	if len(updates) != 1 || updates[0].UpdateID != 11 || updates[0].Post() == nil {
		t.Fatalf("unexpected updates: %+v", updates)
	}
}

func TestClientRequiresToken(t *testing.T) {
	c := NewClient(ClientOptions{APIHost: "https://example.invalid"})
	if c.Ready() {
		t.Fatal("client without token should not be ready")
	}
	if _, err := c.GetMe(context.Background()); err == nil {
		t.Fatal("expected an error when the token is missing")
	}
}

func TestClientUsesConfiguredAPIHost(t *testing.T) {
	var path string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1}}`))
	})
	if _, err := c.GetChat(context.Background(), "@somechannel"); err != nil {
		t.Fatalf("getChat: %v", err)
	}
	if want := "/bottest-token/getChat"; path != want {
		t.Fatalf("expected %s, got %s", want, path)
	}
}

func TestClientDefaultsToOfficialHost(t *testing.T) {
	c := NewClient(ClientOptions{Token: "x"})
	if got := c.methodURL("getMe"); got != "https://api.telegram.org/botx/getMe" {
		t.Fatalf("unexpected default URL: %s", got)
	}
}

func TestClientTrimsTrailingSlashFromHost(t *testing.T) {
	c := NewClient(ClientOptions{Token: "x", APIHost: "https://tg.example.com/"})
	if got := c.methodURL("getMe"); got != "https://tg.example.com/botx/getMe" {
		t.Fatalf("unexpected URL: %s", got)
	}
}

func TestForbiddenClassification(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden: bot is not a member of the channel chat"}`))
	})
	_, err := c.GetChat(context.Background(), "@x")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.IsForbidden() {
		t.Fatalf("expected forbidden, got %v", err)
	}
}

func TestUnauthorizedClassification(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
	})
	_, err := c.GetMe(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.IsUnauthorized() {
		t.Fatalf("expected unauthorized, got %v", err)
	}
	if !strings.Contains(apiErr.Error(), "401") {
		t.Fatalf("error text should carry the code: %s", apiErr.Error())
	}
	_ = fmt.Sprintf("%v", apiErr)
}
