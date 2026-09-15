package dav

import (
	"context"
	"mime"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/net/webdav"

	"litepan/internal/domain"
)

type nodeInfo struct {
	name string
	size int64
	mode os.FileMode
	dir  bool
	mod  time.Time
}

func (n *nodeInfo) Name() string       { return n.name }
func (n *nodeInfo) Size() int64        { return n.size }
func (n *nodeInfo) Mode() os.FileMode  { return n.mode }
func (n *nodeInfo) ModTime() time.Time { return n.mod }
func (n *nodeInfo) IsDir() bool        { return n.dir }
func (n *nodeInfo) Sys() any           { return nil }

func (n *nodeInfo) ContentType(ctx context.Context) (string, error) {
	if n.dir {
		return "", webdav.ErrNotImplemented
	}
	if ctype := mime.TypeByExtension(filepath.Ext(n.name)); ctype != "" {
		return ctype, nil
	}
	return "application/octet-stream", nil
}

func fileInfoFromNode(node *Node) *nodeInfo {
	mode := os.FileMode(0o644)
	dir := node.IsRoot || node.IsAccount || node.Item.IsDir
	if dir {
		mode = os.ModeDir | 0o755
	}
	name := node.Item.Name
	if node.IsRoot {
		name = "/"
	}
	mod := node.Item.ModTime
	if mod.IsZero() {
		mod = time.Now().UTC()
	}
	return &nodeInfo{name: name, size: node.Item.Size, mode: mode, dir: dir, mod: mod}
}

func itemInfo(it domain.FileItem) *nodeInfo {
	mode := os.FileMode(0o644)
	if it.IsDir {
		mode = os.ModeDir | 0o755
	}
	mod := it.ModTime
	if mod.IsZero() {
		mod = time.Now().UTC()
	}
	return &nodeInfo{name: it.Name, size: it.Size, mode: mode, dir: it.IsDir, mod: mod}
}
