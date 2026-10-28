package strmscrape

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type stubImageDownloader struct {
	data []byte
	err  error
}

func (d stubImageDownloader) DownloadImage(context.Context, string, string) ([]byte, error) {
	return d.data, d.err
}

func TestWriteOptionalArtworkSkipsDownloadFailure(t *testing.T) {
	var logs bytes.Buffer
	svc := &Service{log: slog.New(slog.NewTextHandler(&logs, nil))}
	out := filepath.Join(t.TempDir(), "episode-thumb.jpg")

	written, err := svc.writeOptionalArtwork(context.Background(), stubImageDownloader{err: fmt.Errorf("图片 404")}, "/missing.jpg", out, "S01E275 缩略图")
	if err != nil {
		t.Fatalf("可选图片下载失败不应中断刮削，err=%v", err)
	}
	if written {
		t.Fatal("下载失败不应报告已写入")
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("下载失败后不应生成图片，err=%v", err)
	}
	if text := logs.String(); !strings.Contains(text, "可选图片下载失败") || !strings.Contains(text, "S01E275 缩略图") {
		t.Fatalf("未记录可选图片警告：%s", text)
	}
}

func TestWriteOptionalArtworkPreservesWriteFailure(t *testing.T) {
	root := t.TempDir()
	notDir := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := &Service{}
	_, err := svc.writeOptionalArtwork(context.Background(), stubImageDownloader{data: []byte("image")}, "/ok.jpg", filepath.Join(notDir, "thumb.jpg"), "S01E275 缩略图")
	if err == nil || !strings.Contains(err.Error(), "写入S01E275 缩略图") {
		t.Fatalf("本地写入失败必须保留，err=%v", err)
	}
}

func TestWriteOptionalArtworkPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc := &Service{}
	_, err := svc.writeOptionalArtwork(ctx, stubImageDownloader{err: fmt.Errorf("请求失败")}, "/cancel.jpg", filepath.Join(t.TempDir(), "thumb.jpg"), "S01E275 缩略图")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("任务取消不能被降级为警告，err=%v", err)
	}
}

// TMDB 的 poster_path / still_path 为 null 时，anyString 曾经把它变成字面量 "<nil>"，
// 于是「没有图」被当成「图在 <nil> 这个路径上」去下载，整部作品判「刮削失败」。
func TestAnyStringMapsJSONNullToEmpty(t *testing.T) {
	if got := anyString(nil); got != "" {
		t.Fatalf("anyString(nil) = %q, want 空串", got)
	}
	if got := anyString("poster.jpg"); got != "poster.jpg" {
		t.Fatalf("anyString(string) = %q", got)
	}
	if got := anyString(float64(7)); got != "7" {
		t.Fatalf("anyString(number) = %q", got)
	}
}

func TestBlankImagePathRejectsNilLiteral(t *testing.T) {
	for _, in := range []string{"", "   ", "<nil>"} {
		if !blankImagePath(in) {
			t.Fatalf("blankImagePath(%q) = false, want true", in)
		}
	}
	if blankImagePath("/abc.jpg") {
		t.Fatal("真实路径不应被判为空")
	}
}

// 没有图时必须**完全不发请求**：以前这里会带着 "<nil>" 打一次图床，
// 下载失败又把整部作品拖成「刮削失败」。
func TestArtworkWritersSkipMissingImagesWithoutDownloading(t *testing.T) {
	root := t.TempDir()
	svc := &Service{log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}
	counter := &countingImageDownloader{}

	if written, err := svc.writeOptionalArtwork(context.Background(), counter, "<nil>", filepath.Join(root, "a.jpg"), "海报"); err != nil || written {
		t.Fatalf("writeOptionalArtwork = (%v, %v), want (false, nil)", written, err)
	}
	if written, err := svc.writeArtworkSize(context.Background(), counter, "", "w1280", filepath.Join(root, "b.jpg"), "背景图"); err != nil || written {
		t.Fatalf("writeArtworkSize = (%v, %v), want (false, nil)", written, err)
	}
	if counter.calls != 0 {
		t.Fatalf("没有图时不该发下载请求，实际发了 %d 次", counter.calls)
	}
}

type countingImageDownloader struct{ calls int }

func (d *countingImageDownloader) DownloadImage(context.Context, string, string) ([]byte, error) {
	d.calls++
	return nil, fmt.Errorf("不该被调用")
}
