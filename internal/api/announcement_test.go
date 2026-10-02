package api

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"litepan/internal/announcement"
	"litepan/internal/settings"
)

type announcementConfigRepo struct {
	values map[string]string
	sets   int
}

func (r *announcementConfigRepo) Get(_ context.Context, key string) (string, bool, error) {
	value, ok := r.values[key]
	return value, ok, nil
}

func (r *announcementConfigRepo) Set(_ context.Context, key, value string) error {
	r.sets++
	r.values[key] = value
	return nil
}

func (r *announcementConfigRepo) All(context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func TestMarkAnnouncementReadPersistsVersion(t *testing.T) {
	repo := &announcementConfigRepo{values: map[string]string{}}
	settingsSvc, err := settings.New(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	var logOutput strings.Builder
	settingsSvc.SetLogger(slog.New(slog.NewTextHandler(&logOutput, nil)))
	handler := &Handler{settings: settingsSvc}

	request := httptest.NewRequest(http.MethodPost, "/api/admin/announcement/read", bytes.NewBufferString(`{"notice_version":" 2026-08-24-1 "}`))
	response := httptest.NewRecorder()
	handler.markAnnouncementRead(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := settingsSvc.String(settings.KeyAnnouncementReadVersion); got != "2026-08-24-1" {
		t.Fatalf("已读版本=%q", got)
	}
	if !isAnnouncementVersionRead(settingsSvc, "2026-08-24-1") {
		t.Fatal("相同公告版本应判定为已读")
	}
	reloaded, err := settings.New(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !isAnnouncementVersionRead(reloaded, "2026-08-24-1") {
		t.Fatal("重新加载 settings 后应保留已读版本")
	}
	if isAnnouncementVersionRead(settingsSvc, "2026-08-24-2") {
		t.Fatal("新公告版本不应判定为已读")
	}
	if logOutput.Len() != 0 {
		t.Fatalf("公告已读状态不应产生设置日志: %s", logOutput.String())
	}

	repeatRequest := httptest.NewRequest(http.MethodPost, "/api/admin/announcement/read", bytes.NewBufferString(`{"notice_version":"2026-08-24-1"}`))
	repeatResponse := httptest.NewRecorder()
	handler.markAnnouncementRead(repeatResponse, repeatRequest)
	if repeatResponse.Code != http.StatusOK {
		t.Fatalf("重复标记 status=%d body=%s", repeatResponse.Code, repeatResponse.Body.String())
	}
	if repo.sets != 1 {
		t.Fatalf("相同公告版本不应重复写入，sets=%d", repo.sets)
	}
}

func TestMarkAnnouncementReadRejectsEmptyVersion(t *testing.T) {
	repo := &announcementConfigRepo{values: map[string]string{}}
	settingsSvc, err := settings.New(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{settings: settingsSvc}

	request := httptest.NewRequest(http.MethodPost, "/api/admin/announcement/read", bytes.NewBufferString(`{"notice_version":" "}`))
	response := httptest.NewRecorder()
	handler.markAnnouncementRead(response, request)

	if response.Code == http.StatusOK {
		t.Fatalf("空公告版本应拒绝: body=%s", response.Body.String())
	}
}

// 拉不到公告时**必须在日志里留痕**：前端那边「拉取失败」和「本来就还没发公告」
// 长得一模一样（都是「暂无公告」），实测群晖那台就是靠翻代码才定位到。
func TestGetAnnouncementLogsWhenFetchFails(t *testing.T) {
	// 一个必然拉不到的地址：端口上没人监听。
	unreachable := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := unreachable.URL
	unreachable.Close()

	var logOutput strings.Builder
	handler := &Handler{
		announcement: announcement.NewDynamic(func() string { return url }),
		log:          slog.New(slog.NewTextHandler(&logOutput, nil)),
	}

	request := httptest.NewRequest(http.MethodGet, "/api/admin/announcement", nil)
	response := httptest.NewRecorder()
	handler.getAnnouncement(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	logged := logOutput.String()
	if !strings.Contains(logged, "拉取失败") || !strings.Contains(logged, url) {
		t.Fatalf("拉取失败应当留下带地址的日志，实际：%q", logged)
	}
}
