package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"litepan/internal/strmscrape"
)

// 演员**自定义头像**的三条路由（POST / DELETE / GET）在 HTTP 这一层的钉子。
//
// 服务层的用例（strmscrape/javwall_actor_avatar_test.go）钉的是「缩放 / 落盘 / 不逃出
// 目录」；这里钉的是**接缝**上那几件容易错的事：
//   - multipart 字段名必须是 `file`（前端 `form.append("file", …)`）；
//   - `key` 走 query（名字里可能有空格 / 斜杠，不能当路径段）；
//   - GET 回的 Content-Type 是 `image/jpeg` 且能被浏览器当图渲染；
//   - 删除之后 GET 是 404（不是 500，也不是一张 0 字节的图）；
//   - 非图片字节回**校验错**（400，且不留文件）。

func newActorAvatarFixture(t *testing.T) *Handler {
	t.Helper()
	svc := strmscrape.New(strmscrape.Options{
		StrmDir: t.TempDir(),
		DataDir: t.TempDir(),
	})
	return &Handler{strmScrape: svc}
}

// avatarPNG 造一张能过校验的小图。
func avatarPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: 30, G: 160, B: 220, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("造 PNG 失败：%v", err)
	}
	return buf.Bytes()
}

// uploadAvatarRequest 拼一条 multipart 上传请求（字段名与前端同源）。
func uploadAvatarRequest(t *testing.T, key string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "avatar.png")
	if err != nil {
		t.Fatalf("建 multipart 失败：%v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("写 multipart 失败：%v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("收 multipart 失败：%v", err)
	}
	target := "/jav-wall/actor/avatar?key=" + url.QueryEscape(key)
	req := httptest.NewRequest(http.MethodPost, target, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

type avatarEnvelope struct {
	Success bool                          `json:"success"`
	Data    strmscrape.JavWallActorAvatar `json:"data"`
	Message string                        `json:"message"`
}

func TestJavActorAvatarUploadThenServeThenDelete(t *testing.T) {
	h := newActorAvatarFixture(t)
	// 名字带空格与斜杠：这正是「key 走 query 而不是路径段」的理由。
	const key = "name:某某 / 测试"

	rec := httptest.NewRecorder()
	h.saveJavWallActorAvatar(rec, uploadAvatarRequest(t, key, avatarPNG(t, 300, 450)))
	if rec.Code != http.StatusOK {
		t.Fatalf("上传 status = %d，body=%s", rec.Code, rec.Body.String())
	}
	var env avatarEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("解 JSON 失败：%v，body=%s", err, rec.Body.String())
	}
	if !env.Success {
		t.Fatalf("上传 success=false：%s", rec.Body.String())
	}
	// ⚠️ 必须是**服务端**地址：前端拿到它就直接当 src 用，并且会核对这个前缀。
	if !strings.HasPrefix(env.Data.AvatarURL, "/api/admin/strm-scrape/jav-wall/actor/avatar?") {
		t.Fatalf("返回的地址不是服务端地址：%q", env.Data.AvatarURL)
	}
	if !strings.Contains(env.Data.AvatarURL, "v=") {
		t.Fatalf("地址里应带 mtime 版本（换图后不吃旧缓存）：%q", env.Data.AvatarURL)
	}

	// GET：真的能当图渲染出来（前端那一格就是 <img src>）。
	getRec := httptest.NewRecorder()
	h.getJavWallActorAvatar(getRec, httptest.NewRequest(http.MethodGet, env.Data.AvatarURL, nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("取图 status = %d", getRec.Code)
	}
	if ct := getRec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("Content-Type = %q，应为 image/jpeg", ct)
	}
	if _, format, err := image.Decode(bytes.NewReader(getRec.Body.Bytes())); err != nil || format != "jpeg" {
		t.Fatalf("回的不是可解码的 JPEG：format=%q err=%v", format, err)
	}

	// DELETE：幂等成功，且之后 GET 是 404。
	delRec := httptest.NewRecorder()
	h.deleteJavWallActorAvatar(delRec, httptest.NewRequest(http.MethodDelete, "/jav-wall/actor/avatar?key="+url.QueryEscape(key), nil))
	if delRec.Code != http.StatusOK {
		t.Fatalf("删除 status = %d，body=%s", delRec.Code, delRec.Body.String())
	}
	getAfter := httptest.NewRecorder()
	h.getJavWallActorAvatar(getAfter, httptest.NewRequest(http.MethodGet, env.Data.AvatarURL, nil))
	if getAfter.Code != http.StatusNotFound {
		t.Fatalf("删除之后取图应为 404，实际 %d", getAfter.Code)
	}
	// 再删一次也不该报错（前端「恢复默认」可能重复点）。
	delAgain := httptest.NewRecorder()
	h.deleteJavWallActorAvatar(delAgain, httptest.NewRequest(http.MethodDelete, "/jav-wall/actor/avatar?key="+url.QueryEscape(key), nil))
	if delAgain.Code != http.StatusOK {
		t.Fatalf("重复删除应仍回 200，实际 %d", delAgain.Code)
	}
}

func TestJavActorAvatarUploadRejectsBadInput(t *testing.T) {
	h := newActorAvatarFixture(t)

	t.Run("非图片字节", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.saveJavWallActorAvatar(rec, uploadAvatarRequest(t, "k1", []byte("这不是图片")))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("非图片应回 400，实际 %d：%s", rec.Code, rec.Body.String())
		}
		getRec := httptest.NewRecorder()
		h.getJavWallActorAvatar(getRec, httptest.NewRequest(http.MethodGet, "/jav-wall/actor/avatar?key=k1", nil))
		if getRec.Code != http.StatusNotFound {
			t.Fatalf("校验失败后不该留下文件，取图应 404，实际 %d", getRec.Code)
		}
	})

	t.Run("缺 key", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.saveJavWallActorAvatar(rec, uploadAvatarRequest(t, "", avatarPNG(t, 50, 50)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("缺 key 应回 400，实际 %d：%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("缺文件字段", func(t *testing.T) {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		_ = mw.WriteField("note", "没有 file 字段")
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/jav-wall/actor/avatar?key=k2", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		h.saveJavWallActorAvatar(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("缺 file 字段应回 400，实际 %d：%s", rec.Code, rec.Body.String())
		}
	})
}

// 没上传过的 key 取图 → 404（不是空 200，也不是 500）。
func TestJavActorAvatarGetMissingIs404(t *testing.T) {
	h := newActorAvatarFixture(t)
	rec := httptest.NewRecorder()
	h.getJavWallActorAvatar(rec, httptest.NewRequest(http.MethodGet, "/jav-wall/actor/avatar?key=nobody", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("没上传过的 key 应 404，实际 %d", rec.Code)
	}
}
