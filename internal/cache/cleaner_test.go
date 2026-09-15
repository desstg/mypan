package cache

import (
	"testing"
	"time"

	"litepan/internal/eventbus"
)

func TestMutationFileIDsUsesBatchAndFallsBackToSingleID(t *testing.T) {
	tests := []struct {
		name  string
		event eventbus.FileMutated
		want  []string
	}{
		{
			name:  "batch takes precedence",
			event: eventbus.FileMutated{FileID: "single", FileIDs: []string{"a", "b"}},
			want:  []string{"a", "b"},
		},
		{
			name:  "single fallback",
			event: eventbus.FileMutated{FileID: "single"},
			want:  []string{"single"},
		},
		{name: "empty event", event: eventbus.FileMutated{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mutationFileIDs(tt.event)
			if len(got) != len(tt.want) {
				t.Fatalf("IDs = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("IDs = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestApplyMutationInvalidatesMovedFilesAndBothDirectories(t *testing.T) {
	cache := NewService(Options{MaxItems: 32})
	t.Cleanup(cache.Close)
	const accountID int64 = 7
	const oldParent = "old-parent"
	const newParent = "new-parent"
	fileIDs := []string{"file-a", "file-b"}

	cache.Set(DirKey(accountID, oldParent), "old", time.Hour)
	cache.Set(DirKey(accountID, newParent), "new", time.Hour)
	for _, fileID := range fileIDs {
		cache.Set(FileInfoKey(accountID, fileID), fileID, time.Hour)
		cache.Set(DownloadURLKey(accountID, fileID, "test-agent"), fileID, time.Hour)
	}

	ApplyMutation(cache, eventbus.FileMutated{
		AccountID:   accountID,
		Op:          "move",
		ParentID:    newParent,
		OldParentID: oldParent,
		FileIDs:     fileIDs,
	})

	for _, key := range []string{DirKey(accountID, oldParent), DirKey(accountID, newParent)} {
		if _, ok := cache.Get(key); ok {
			t.Fatalf("directory cache %q was not invalidated", key)
		}
	}
	for _, fileID := range fileIDs {
		if _, ok := cache.Get(FileInfoKey(accountID, fileID)); ok {
			t.Fatalf("file info %q was not invalidated", fileID)
		}
		if _, ok := cache.Get(DownloadURLKey(accountID, fileID, "test-agent")); ok {
			t.Fatalf("download URL %q was not invalidated", fileID)
		}
	}
	if !cache.DirIsCooling(accountID, oldParent) || !cache.DirIsCooling(accountID, newParent) {
		t.Fatal("both source and target directories must enter cooling")
	}
}

func TestInvalidateAccountIncludesWebDAVCaches(t *testing.T) {
	cache := NewService(Options{MaxItems: 16})
	t.Cleanup(cache.Close)
	const accountID int64 = 7
	keys := []string{
		DirKey(accountID, "root"),
		FileInfoKey(accountID, "file"),
		DownloadURLKey(accountID, "file", "ua"),
		PathMapKey(accountID, "/movie"),
		WebDAVMetaKey(accountID, "PROPFIND|/movie|depth=1"),
	}
	for _, key := range keys {
		cache.Set(key, "cached", time.Hour)
	}
	otherAccountKey := PathMapKey(8, "/movie")
	cache.Set(otherAccountKey, "keep", time.Hour)

	cache.InvalidateAccount(accountID)

	for _, key := range keys {
		if _, ok := cache.Get(key); ok {
			t.Fatalf("account cache %q was not invalidated", key)
		}
	}
	if _, ok := cache.Get(otherAccountKey); !ok {
		t.Fatal("another account cache must be preserved")
	}
}
