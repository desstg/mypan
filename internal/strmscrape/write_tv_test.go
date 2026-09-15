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
