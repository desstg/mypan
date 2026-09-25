package javbus

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMagnetsByCodeNotFoundIsSentinel 404 要能被识别成「这个番号这儿没有页面」。
//
// 这一条是给调用方用的：无码 / 欧美 / FC2 三档的番号在 JAVBUS **必然** 404
// （它是日式有码站的库），有码那档也常有漏网的。若 404 与「网络挂了」在错误里
// 长得一样，调用方就只能二选一：要么每一部片都记一条 warn（日志被正常状态刷满），
// 要么把真的故障也一起吞掉。
func TestMagnetsByCodeNotFoundIsSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.MagnetsByCode(context.Background(), "SZL028")
	if err == nil {
		t.Fatal("404 时应当报错")
	}
	if !errors.Is(err, ErrCodeNotFound) {
		t.Errorf("404 应当包成 ErrCodeNotFound，got %v", err)
	}
	if !strings.Contains(err.Error(), "SZL028") {
		t.Errorf("错误里应当带上番号，方便排查: %v", err)
	}
}

// TestMagnetsByCodeOtherErrorsAreNotNotFound 别的失败**不能**被当成「没这个番号」。
//
// 500 / 被墙 / 结构变了都是「这次没成」，不是「这儿没有」—— 混为一谈的话，
// 上游一挂，界面上会显示成「这部片没有磁链」，而真正的原因（网络）永远看不到。
func TestMagnetsByCodeOtherErrorsAreNotNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.MagnetsByCode(context.Background(), "SSIS-001")
	if err == nil {
		t.Fatal("403 时应当报错")
	}
	if errors.Is(err, ErrCodeNotFound) {
		t.Errorf("403 不该被当成「没有这个番号」: %v", err)
	}
}
