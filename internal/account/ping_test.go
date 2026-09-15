package account

import (
	"errors"
	"strings"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

func TestRetryableConnectionTestError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "网络错误", err: domain.Errorf(domain.CodeDriverError, "dial tcp: i/o timeout"), want: true},
		{name: "上游限流", err: domain.Errf(domain.CodeRateLimited), want: true},
		{name: "上游临时错误", err: domain.Errorf(domain.CodeDriverError, "upstream HTTP 503"), want: true},
		{name: "认证失效", err: domain.Errf(domain.CodeAuthExpired), want: false},
		{name: "权限不足", err: domain.Errf(domain.CodePermissionDenied), want: false},
		{name: "配置错误", err: domain.Errf(domain.CodeValidation), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryableConnectionTestError(tt.err); got != tt.want {
				t.Fatalf("retryableConnectionTestError()=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestConnectionTestErrorKeepsDiagnostics(t *testing.T) {
	first := errors.New("upstream HTTP 503")
	last := domain.Errorf(domain.CodeDriverError, "upstream HTTP 502")
	err := connectionTestError(nilDriver(), "onedrive", last, false, 2, first)
	ae, ok := domain.AsAppError(err)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if !strings.Contains(ae.Message, "网盘接口暂时不可用") {
		t.Fatalf("message=%q", ae.Message)
	}
	if ae.Details["attempts"] != 2 || ae.Details["technical"] != last.Error() || ae.Details["first_attempt"] != first.Error() {
		t.Fatalf("details=%#v", ae.Details)
	}
}

func TestConnectionTestErrorDistinguishesAuthAndNetwork(t *testing.T) {
	authErr := connectionTestError(nilDriver(), "onedrive", domain.Errf(domain.CodeAuthExpired), false, 1, nil)
	if ae, _ := domain.AsAppError(authErr); !strings.Contains(ae.Message, "认证信息无效") {
		t.Fatalf("auth message=%q", ae.Message)
	}
	networkErr := connectionTestError(nilDriver(), "onedrive", domain.Errorf(domain.CodeDriverError, "network is unreachable"), false, 2, nil)
	if ae, _ := domain.AsAppError(networkErr); !strings.Contains(ae.Message, "连接网盘服务失败") {
		t.Fatalf("network message=%q", ae.Message)
	}
}

// nilDriver 仅用于验证公共错误分类；该路径不会调用驱动方法。
func nilDriver() driver.Driver { return nil }
