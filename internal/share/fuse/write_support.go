//go:build fuse

package fuse

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"litepan/internal/domain"
	"litepan/internal/upload"
)

type stagingFile struct {
	fs.Inode
	b        *backend
	name     string
	tempPath string
	size     int64
	modTime  time.Time
	mu       sync.RWMutex
	// parentID/displayPath 记录上传目标目录，rename 跨目录时同步更新，Flush 以最新值为准。
	parentID    string
	displayPath string
	// uploads 与 taskID 在 Flush 投递上传任务后写入，供改名/删除时联动任务。
	uploads UploadManager
	taskID  string
}

var (
	_ fs.NodeGetattrer = (*stagingFile)(nil)
	_ fs.NodeOpener    = (*stagingFile)(nil)
	_ fs.FileWriter    = (*stagingUploadHandle)(nil)
	_ fs.FileFlusher   = (*stagingUploadHandle)(nil)
	_ fs.FileFsyncer   = (*stagingUploadHandle)(nil)
	_ fs.FileReleaser  = (*stagingUploadHandle)(nil)
	_ fs.FileReader    = (*localStagingReadHandle)(nil)
	_ fs.FileReleaser  = (*localStagingReadHandle)(nil)
)

func (d *cloudDir) Create(ctx context.Context, name string, flags uint32, _ uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	name, errno := normalizeChildName(name)
	if errno != 0 {
		return nil, nil, 0, errno
	}
	if d.b.deps.Uploads == nil {
		return nil, nil, 0, syscall.EROFS
	}
	if _, found, errno := d.lookupChildItemIfExists(ctx, name); errno != 0 {
		return nil, nil, 0, errno
	} else if found {
		return nil, nil, 0, syscall.EEXIST
	}
	tmp, tmpPath, cleanup, untrack, err := createFuseTempFile(d.b.deps.Uploads, name)
	if err != nil {
		return nil, nil, 0, errnoFor(err)
	}
	node := &stagingFile{
		b:           d.b,
		name:        name,
		tempPath:    tmpPath,
		modTime:     time.Now(),
		parentID:    d.item.ID,
		displayPath: d.targetDisplayPath(),
	}
	child := d.NewPersistentInode(ctx, node, fs.StableAttr{
		Ino:  fileIno(d.b.accountID(), "pending:"+tmpPath),
		Mode: fuse.S_IFREG,
	})
	if child == nil {
		cleanup()
		_ = tmp.Close()
		return nil, nil, 0, syscall.EIO
	}
	fillEntryAttr(out, d.b, domain.FileItem{Name: name, Size: 0, ModTime: node.modTime})
	return child, &stagingUploadHandle{
		node:              node,
		uploads:           d.b.deps.Uploads,
		accountID:         d.b.accountID(),
		parentID:          d.item.ID,
		targetDisplayPath: d.targetDisplayPath(),
		fileName:          name,
		tempPath:          tmpPath,
		file:              tmp,
		cleanup:           cleanup,
		untrack:           untrack,
		flags:             flags,
	}, 0, 0
}

func (d *cloudDir) targetDisplayPath() string {
	return strings.Trim(d.Path(nil), "/")
}

func (f *stagingFile) Getattr(_ context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	f.mu.RLock()
	item := domain.FileItem{
		Name:    f.name,
		Size:    f.size,
		ModTime: f.modTime,
	}
	f.mu.RUnlock()
	fillFileAttr(&out.Attr, f.b, item)
	return 0
}

func (f *stagingFile) Open(_ context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if flags&syscall.O_ACCMODE != syscall.O_RDONLY {
		return nil, 0, syscall.EROFS
	}
	f.mu.RLock()
	tempPath := f.tempPath
	f.mu.RUnlock()
	if tempPath == "" {
		return nil, 0, syscall.ENOENT
	}
	file, err := os.Open(tempPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, syscall.ENOENT
		}
		return nil, 0, syscall.EIO
	}
	return &localStagingReadHandle{file: file}, 0, 0
}

func (f *stagingFile) updateSize(size int64) {
	f.mu.Lock()
	if size > f.size {
		f.size = size
	}
	f.modTime = time.Now()
	f.mu.Unlock()
}

func (f *stagingFile) updateFromStat(st os.FileInfo) {
	if st == nil {
		return
	}
	f.mu.Lock()
	f.size = st.Size()
	f.modTime = st.ModTime()
	f.mu.Unlock()
}

type stagingUploadHandle struct {
	node              *stagingFile
	uploads           UploadManager
	accountID         int64
	parentID          string
	targetDisplayPath string
	fileName          string
	tempPath          string
	file              *os.File
	cleanup           func()
	untrack           func()
	flags             uint32
	queued            bool
	released          bool
	mu                sync.Mutex
}

func (h *stagingUploadHandle) Write(_ context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	h.mu.Lock()
	file := h.file
	h.mu.Unlock()
	if file == nil {
		return 0, syscall.EBADF
	}
	n, err := file.WriteAt(data, off)
	if n > 0 && h.node != nil {
		h.node.updateSize(off + int64(n))
	}
	if err != nil {
		return uint32(n), errnoFor(err)
	}
	return uint32(n), 0
}

func (h *stagingUploadHandle) Flush(_ context.Context) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.queued {
		return 0
	}
	if h.file == nil {
		return syscall.EBADF
	}
	if h.flags&syscall.O_ACCMODE == syscall.O_RDONLY {
		return syscall.EBADF
	}
	if err := h.file.Sync(); err != nil {
		return errnoFor(err)
	}
	st, err := h.file.Stat()
	if err != nil {
		return errnoFor(err)
	}
	if h.node != nil {
		h.node.updateFromStat(st)
	}
	// 若在 close 前已发生 rename，以节点当前名为准，避免上传任务使用旧名。
	fileName := h.fileName
	parentID := h.parentID
	displayPath := h.targetDisplayPath
	if h.node != nil {
		h.node.mu.RLock()
		fileName = h.node.name
		parentID = h.node.parentID
		displayPath = h.node.displayPath
		h.node.mu.RUnlock()
	}
	if fileName == "" {
		fileName = h.fileName
	}
	if parentID == "" {
		parentID = h.parentID
	}
	if displayPath == "" {
		displayPath = h.targetDisplayPath
	}
	task, terr := h.uploads.Create(context.Background(), upload.CreateParams{
		AccountID:         h.accountID,
		FileName:          fileName,
		DisplayName:       fileName,
		TargetPath:        parentID,
		TargetDisplayPath: displayPath,
		LocalPath:         h.tempPath,
		TotalBytes:        st.Size(),
		ConflictPolicy:    "overwrite",
	})
	if terr != nil {
		return errnoFor(terr)
	}
	if h.node != nil && task != nil {
		h.node.mu.Lock()
		h.node.uploads = h.uploads
		h.node.taskID = task.TaskID
		h.node.mu.Unlock()
	}
	h.queued = true
	if h.untrack != nil {
		h.untrack()
		h.untrack = nil
	}
	return 0
}

func (h *stagingUploadHandle) Fsync(_ context.Context, _ uint32) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.file == nil {
		return syscall.EBADF
	}
	if err := h.file.Sync(); err != nil {
		return errnoFor(err)
	}
	return 0
}

func (h *stagingUploadHandle) Release(_ context.Context) syscall.Errno {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.released {
		return 0
	}
	h.released = true
	if h.file != nil {
		_ = h.file.Close()
		h.file = nil
	}
	if !h.queued && h.cleanup != nil {
		h.cleanup()
		h.cleanup = nil
	}
	return 0
}

type localStagingReadHandle struct {
	file *os.File
}

func (h *localStagingReadHandle) Read(_ context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if h.file == nil {
		return nil, syscall.EBADF
	}
	n, err := h.file.ReadAt(dest, off)
	if n > 0 {
		return fuse.ReadResultData(dest[:n]), 0
	}
	if err == nil || err == io.EOF {
		return fuse.ReadResultData(nil), 0
	}
	if os.IsNotExist(err) {
		return nil, syscall.ENOENT
	}
	return nil, syscall.EIO
}

func (h *localStagingReadHandle) Release(_ context.Context) syscall.Errno {
	if h.file != nil {
		_ = h.file.Close()
		h.file = nil
	}
	return 0
}

func createFuseTempFile(uploads UploadManager, fileName string) (*os.File, string, func(), func(), error) {
	dir := uploads.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", nil, nil, err
	}
	safeName := filepath.Base(fileName)
	if safeName == "" || safeName == "." {
		safeName = "upload.bin"
	}
	f, err := os.CreateTemp(dir, "fuse_*_"+safeName)
	if err != nil {
		return nil, "", nil, nil, err
	}
	path := filepath.Clean(f.Name())
	untrack := func() {}
	if registry := uploads.TempRegistry(); registry != nil {
		untrack = registry.Track(path)
	}
	cleanup := func() {
		untrack()
		_ = os.Remove(path)
	}
	return f, path, cleanup, untrack, nil
}
