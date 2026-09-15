package api

import (
	"net/http/httptest"
	"testing"

	"litepan/internal/domain"
)

func TestShouldSuppressAPIErrorLog(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		remoteAddr string
		err        *domain.AppError
		want       bool
	}{
		{
			name:       "本机上传任务列表401静默",
			path:       "/api/files/upload/tasks",
			remoteAddr: "127.0.0.1:5211",
			err:        domain.Errorf(domain.CodeAdminAuthRequired, "需要管理员权限"),
			want:       true,
		},
		{
			name:       "本机上传任务流401静默",
			path:       "/api/files/upload/tasks/stream",
			remoteAddr: "[::1]:5211",
			err:        domain.Errorf(domain.CodeAdminAuthRequired, "需要管理员权限"),
			want:       true,
		},
		{
			name:       "远端来源不静默",
			path:       "/api/files/upload/tasks",
			remoteAddr: "10.0.0.8:5211",
			err:        domain.Errorf(domain.CodeAdminAuthRequired, "需要管理员权限"),
			want:       false,
		},
		{
			name:       "其他管理员接口不静默",
			path:       "/api/admin/accounts",
			remoteAddr: "127.0.0.1:5211",
			err:        domain.Errorf(domain.CodeAdminAuthRequired, "需要管理员权限"),
			want:       false,
		},
		{
			name:       "其他错误码不静默",
			path:       "/api/files/upload/tasks",
			remoteAddr: "127.0.0.1:5211",
			err:        domain.Errorf(domain.CodeValidation, "参数错误"),
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			req.RemoteAddr = tt.remoteAddr
			if got := shouldSuppressAPIErrorLog(req, tt.err); got != tt.want {
				t.Fatalf("shouldSuppressAPIErrorLog() = %v, want %v", got, tt.want)
			}
		})
	}
}
