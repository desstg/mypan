package favorites

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeleteRemovesOnlyTargetAccount(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	svc := newTestFavoritesService(dir)
	ctx := context.Background()
	for _, accountID := range []int64{11, 22} {
		if _, err := svc.Put(ctx, accountID, AccountState{
			Open: true,
			Items: []Item{{
				ID:   "folder",
				Name: "收藏目录",
				Crumbs: []Crumb{{
					ID:   "root",
					Name: "根目录",
				}},
			}},
		}); err != nil {
			t.Fatalf("保存账号 %d 收藏失败: %v", accountID, err)
		}
	}

	if err := svc.Delete(ctx, 11); err != nil {
		t.Fatalf("删除账号收藏失败: %v", err)
	}
	deleted, err := svc.Get(ctx, 11)
	if err != nil {
		t.Fatalf("读取已删除账号收藏失败: %v", err)
	}
	if deleted.Open || len(deleted.Items) != 0 {
		t.Fatalf("目标账号收藏未清空: %#v", deleted)
	}
	kept, err := svc.Get(ctx, 22)
	if err != nil {
		t.Fatalf("读取其他账号收藏失败: %v", err)
	}
	if !kept.Open || len(kept.Items) != 1 || kept.Items[0].Name != "收藏目录" {
		t.Fatalf("其他账号收藏被误改: %#v", kept)
	}
}

func TestDeleteMissingAccountDoesNotRewriteFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	svc := newTestFavoritesService(dir)
	ctx := context.Background()
	if _, err := svc.Put(ctx, 11, AccountState{Open: true}); err != nil {
		t.Fatalf("保存收藏状态失败: %v", err)
	}
	path := filepath.Join(dir, fileName)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("读取收藏文件信息失败: %v", err)
	}

	if err := svc.Delete(ctx, 999); err != nil {
		t.Fatalf("删除不存在账号的收藏应幂等成功: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("再次读取收藏文件信息失败: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatalf("不存在的账号不应触发收藏文件重写")
	}
}

func TestGetMovesCorruptedFavoritesFileAndReturnsError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	svc := newTestFavoritesService(dir)
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{bad json"), 0o644); err != nil {
		t.Fatalf("写入损坏收藏夹文件失败: %v", err)
	}

	_, err := svc.Get(context.Background(), 1)
	if err == nil {
		t.Fatalf("期望返回损坏错误")
	}
	if !strings.Contains(err.Error(), "已损坏") {
		t.Fatalf("错误信息未指出文件损坏: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, fileName)); !os.IsNotExist(statErr) {
		t.Fatalf("损坏原文件应被转移，实际 stat err=%v", statErr)
	}
	matches, globErr := filepath.Glob(filepath.Join(dir, fileName+".corrupt-*"))
	if globErr != nil {
		t.Fatalf("查找损坏备份失败: %v", globErr)
	}
	if len(matches) != 1 {
		t.Fatalf("期望生成 1 个损坏备份，实际 %d 个: %#v", len(matches), matches)
	}
	raw, readErr := os.ReadFile(matches[0])
	if readErr != nil {
		t.Fatalf("读取损坏备份失败: %v", readErr)
	}
	if string(raw) != "{bad json" {
		t.Fatalf("损坏备份内容不匹配: %q", string(raw))
	}
}

func TestPutDoesNotSilentlyOverwriteCorruptedFavoritesFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	svc := newTestFavoritesService(dir)
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{bad json"), 0o644); err != nil {
		t.Fatalf("写入损坏收藏夹文件失败: %v", err)
	}

	_, err := svc.Put(context.Background(), 1, AccountState{
		Open: true,
		Items: []Item{{
			ID:   "1",
			Name: "电影",
			Crumbs: []Crumb{{
				ID:   "root",
				Name: "根目录",
			}},
		}},
	})
	if err == nil {
		t.Fatalf("期望保存时返回损坏错误")
	}
	if _, statErr := os.Stat(filepath.Join(dir, fileName)); !os.IsNotExist(statErr) {
		t.Fatalf("损坏原文件应已被转移且当前不应生成新文件，stat err=%v", statErr)
	}
	matches, globErr := filepath.Glob(filepath.Join(dir, fileName+".corrupt-*"))
	if globErr != nil {
		t.Fatalf("查找损坏备份失败: %v", globErr)
	}
	if len(matches) != 1 {
		t.Fatalf("期望保留 1 份损坏备份，实际 %d 个: %#v", len(matches), matches)
	}
}

func newTestFavoritesService(dir string) *Service {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewService(filepath.Join(dir, "litepan.db"), logger)
}
