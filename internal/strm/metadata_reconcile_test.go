package strm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
)

type metadataFilesStub struct {
	remote         map[string][]domain.FileItem
	uploads        []driver.LocalUploadRequest
	mutationMarked bool
}

func (s *metadataFilesStub) List(_ context.Context, _ int64, parentID string, _ bool) ([]domain.FileItem, error) {
	return append([]domain.FileItem(nil), s.remote[parentID]...), nil
}

func (s *metadataFilesStub) UploadLocal(ctx context.Context, _ int64, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	s.mutationMarked = isMetadataSyncMutation(ctx)
	s.uploads = append(s.uploads, req)
	info, err := os.Stat(req.LocalPath)
	if err != nil {
		return nil, err
	}
	return &driver.LocalUploadResult{
		FileID:   "uploaded-" + req.FileName,
		ParentID: req.ParentID,
		FileName: req.FileName,
		Size:     info.Size(),
	}, nil
}

func TestMetadataSyncModesWithSimulatedFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("cloud metadata"))
	}))
	defer server.Close()

	tests := []struct {
		name          string
		mode          string
		wantLocal     bool
		wantUploaded  int64
		wantDeleted   int64
		wantLastPhase string
	}{
		{
			name:          "本地元数据补缺只从网盘补缺",
			mode:          MetadataSyncLocalPrimary,
			wantLocal:     true,
			wantLastPhase: ScanPhaseMetadata,
		},
		{
			name:          "网盘元数据为主下载并清理本地多余文件",
			mode:          MetadataSyncCloudPrimary,
			wantLocal:     false,
			wantDeleted:   1,
			wantLastPhase: ScanPhaseMetadataCleanup,
		},
		{
			name:          "本地与云端互补下载并上传双方缺失文件",
			mode:          MetadataSyncBidirectional,
			wantLocal:     true,
			wantUploaded:  1,
			wantLastPhase: ScanPhaseMetadataUpload,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			localDir := filepath.Join(root, "任务", "电影")
			if err := os.MkdirAll(localDir, 0o755); err != nil {
				t.Fatal(err)
			}
			localOnly := filepath.Join(localDir, "local.nfo")
			if err := os.WriteFile(localOnly, []byte("local metadata"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(localDir, "shared.jpg"), []byte("shared"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(localDir, "movie.strm"), []byte("play url"), 0o644); err != nil {
				t.Fatal(err)
			}

			files := &metadataFilesStub{remote: map[string][]domain.FileItem{
				"remote-movie": {
					{ID: "cloud", Name: "cloud.nfo", Size: 14},
					{ID: "shared", Name: "shared.jpg", Size: 6},
				},
			}}
			var phases []string
			result, err := syncMetadata(t.Context(), metadataSyncRequest{
				AccountID:    1,
				Root:         root,
				OutputFolder: "任务",
				Mode:         tt.mode,
				Extensions:   map[string]struct{}{"nfo": {}, "jpg": {}},
				MaxSizeBytes: 10 << 20,
				RemoteItems: []metadataItem{
					{fileID: "cloud", fileName: "cloud.nfo", relPath: filepath.Join("任务", "电影", "cloud.nfo")},
					{fileID: "shared", fileName: "shared.jpg", relPath: filepath.Join("任务", "电影", "shared.jpg")},
				},
				Directories: map[string]metadataDirectory{
					dirKey([]string{"电影"}): {parentID: "remote-movie", relDirs: []string{"电影"}},
				},
				Files:    files,
				Playback: &metadataResolverStub{baseURL: server.URL},
				OnProgress: func(update ScanProgressUpdate) {
					if update.Phase != "" {
						phases = append(phases, update.Phase)
					}
				},
			})
			if err != nil {
				t.Fatalf("同步失败: %v", err)
			}
			if result.Downloaded != 1 {
				t.Fatalf("下载数量=%d，期望=1", result.Downloaded)
			}
			if result.Uploaded != tt.wantUploaded || result.Deleted != tt.wantDeleted {
				t.Fatalf("结果=%+v，期望上传=%d、删除=%d", result, tt.wantUploaded, tt.wantDeleted)
			}
			if _, err := os.Stat(filepath.Join(localDir, "cloud.nfo")); err != nil {
				t.Fatalf("云端缺失元数据未补到本地: %v", err)
			}
			_, localErr := os.Stat(localOnly)
			if (localErr == nil) != tt.wantLocal {
				t.Fatalf("本地独有元数据存在=%v，期望=%v", localErr == nil, tt.wantLocal)
			}
			if _, err := os.Stat(filepath.Join(localDir, "movie.strm")); err != nil {
				t.Fatalf("同步不应处理 STRM 文件: %v", err)
			}
			if tt.wantUploaded > 0 {
				if len(files.uploads) != 1 || files.uploads[0].FileName != "local.nfo" {
					t.Fatalf("上传请求=%+v，期望只上传 local.nfo", files.uploads)
				}
				if !files.mutationMarked {
					t.Fatal("元数据反向上传必须标记为内部事件，避免再次触发 STRM 扫描")
				}
			}
			if len(phases) == 0 || phases[len(phases)-1] != tt.wantLastPhase {
				t.Fatalf("进度阶段=%v，最后阶段期望=%q", phases, tt.wantLastPhase)
			}
		})
	}
}

func TestCloudPrimarySkipsCleanupWhenCloudDownloadIsIncomplete(t *testing.T) {
	root := t.TempDir()
	localDir := filepath.Join(root, "任务", "电影")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	localOnly := filepath.Join(localDir, "local.nfo")
	if err := os.WriteFile(localOnly, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := syncMetadata(t.Context(), metadataSyncRequest{
		Root:         root,
		OutputFolder: "任务",
		Mode:         MetadataSyncCloudPrimary,
		Extensions:   map[string]struct{}{"nfo": {}},
		RemoteItems: []metadataItem{
			{fileID: "missing", relPath: filepath.Join("任务", "电影", "cloud.nfo")},
		},
		Directories: map[string]metadataDirectory{
			dirKey([]string{"电影"}): {parentID: "remote-movie", relDirs: []string{"电影"}},
		},
	})
	if err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	if result.Deleted != 0 {
		t.Fatalf("云端下载未完成时不应清理本地，实际删除=%d", result.Deleted)
	}
	if _, err := os.Stat(localOnly); err != nil {
		t.Fatalf("云端下载未完成时本地文件应保留: %v", err)
	}
}

func TestMetadataUploadMutationDoesNotWakeScanner(t *testing.T) {
	svc := NewService(ServiceOptions{})
	svc.OnFileMutated(withMetadataSyncMutation(t.Context()), eventbus.FileMutated{
		AccountID: 9,
		Op:        "create",
	})
	if svc.dirtyAccounts[9] {
		t.Fatal("STRM 自己上传的元数据不应再次唤醒扫描")
	}

	svc.OnFileMutated(t.Context(), eventbus.FileMutated{
		AccountID: 9,
		Op:        "create",
	})
	if !svc.dirtyAccounts[9] {
		t.Fatal("普通文件变更仍应唤醒扫描")
	}
}

// TestBuildMetadataSyncPlanKeepsJavArtifacts 番号任务生成的 nfo / 图片不参与解算。
//
// 这条挡的是一个**静默且昂贵**的循环：我们生成的 `<主干>.nfo` / `poster.jpg` /
// `thumb.jpg` / `fanart.jpg` 扩展名全在元数据表里、而且永远不在网盘上 ——
// 没有守卫的话，cloud_primary 每一轮扫描都删一次、生成器再写一次
// （每轮重拉一遍全部剧照），bidirectional 则会把海报往网盘上传。
func TestBuildMetadataSyncPlanKeepsJavArtifacts(t *testing.T) {
	root := t.TempDir()
	localDir := filepath.Join(root, "任务", "电影")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 一部片 + 本程序生成的那一套
	for _, name := range []string{"MOIL-001-UC-4K.strm", "MOIL-001-UC-4K.nfo", "poster.jpg", "thumb.jpg", "fanart.jpg"} {
		if err := os.WriteFile(filepath.Join(localDir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 用户自己塞的、网盘上也没有的元数据（该被清掉 / 上传）
	if err := os.WriteFile(filepath.Join(localDir, "user-notes.nfo"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	planFor := func(guard bool, mode string) metadataSyncPlan {
		t.Helper()
		plan, err := buildMetadataSyncPlan(t.Context(), metadataSyncRequest{
			Root:         root,
			OutputFolder: "任务",
			Mode:         mode,
			Extensions:   map[string]struct{}{"nfo": {}, "jpg": {}},
			MaxSizeBytes: 10 << 20,
			Directories: map[string]metadataDirectory{
				"电影": {parentID: "remote", relDirs: []string{"电影"}},
			},
			JavArtifactGuard: guard,
		})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	names := func(items []localMetadataItem) []string {
		out := make([]string, 0, len(items))
		for _, it := range items {
			out = append(out, it.fileName)
		}
		return out
	}

	// 番号任务 + cloud_primary：只删用户那份，本程序生成的一个都不动
	deletes := names(planFor(true, MetadataSyncCloudPrimary).deletes)
	if len(deletes) != 1 || deletes[0] != "user-notes.nfo" {
		t.Errorf("应当只删 user-notes.nfo，got %v", deletes)
	}
	// 番号任务 + bidirectional：同理不上传
	uploads := names(planFor(true, MetadataSyncBidirectional).uploads)
	if len(uploads) != 1 || uploads[0] != "user-notes.nfo" {
		t.Errorf("应当只上传 user-notes.nfo，got %v", uploads)
	}
	// **没开守卫时照旧全删** —— 证明守卫是按任务类型生效的，不是无条件放行
	// （刮削那套写进来的海报走的是这条路，那是独立的既有行为）。
	if got := names(planFor(false, MetadataSyncCloudPrimary).deletes); len(got) != 5 {
		t.Errorf("非番号任务应当照旧清理（5 个），got %v", got)
	}
}
