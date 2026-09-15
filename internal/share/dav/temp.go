package dav

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"litepan/internal/upload"
)

func createWebDAVTempFile(dataDir, fileName string, registry *upload.TempRegistry) (*os.File, string, func(), error) {
	dir := upload.TempDir(dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", nil, err
	}
	safeName := filepath.Base(fileName)
	if safeName == "" || safeName == "." {
		safeName = "upload.bin"
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, "", nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("webdav_%s_%s", hex.EncodeToString(id[:]), safeName))
	f, err := os.Create(path)
	if err != nil {
		return nil, "", nil, err
	}
	untrack := func() {}
	if registry != nil {
		untrack = registry.Track(path)
	}
	release := func() {
		untrack()
		_ = os.Remove(path)
	}
	return f, path, release, nil
}
