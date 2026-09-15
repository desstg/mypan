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
