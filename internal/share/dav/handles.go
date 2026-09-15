package dav

import (
	"context"
	"io"
	"os"

	"litepan/internal/domain"
)

type dirHandle struct {
	info    *nodeInfo
	entries []domain.FileItem
	pos     int
}

func (d *dirHandle) Close() error { return nil }

func (d *dirHandle) Read([]byte) (int, error) { return 0, os.ErrInvalid }

func (d *dirHandle) Write([]byte) (int, error) { return 0, os.ErrPermission }

func (d *dirHandle) Seek(int64, int) (int64, error) { return 0, os.ErrInvalid }

func (d *dirHandle) Readdir(count int) ([]os.FileInfo, error) {
	readAll := count <= 0
	if d.pos >= len(d.entries) {
		if readAll {
			return nil, nil
		}
		return nil, io.EOF
	}
	if readAll {
		count = len(d.entries) - d.pos
	}
	end := d.pos + count
	if end > len(d.entries) {
		end = len(d.entries)
	}
	out := make([]os.FileInfo, 0, end-d.pos)
	for _, it := range d.entries[d.pos:end] {
		out = append(out, itemInfo(it))
	}
	d.pos = end
	if !readAll && d.pos >= len(d.entries) {
		return out, io.EOF
	}
	return out, nil
}

func (d *dirHandle) Stat() (os.FileInfo, error) { return d.info, nil }

type fileHandle struct {
	info *nodeInfo
}

func (f *fileHandle) Close() error { return nil }

func (f *fileHandle) Read([]byte) (int, error) { return 0, io.EOF }

func (f *fileHandle) Write([]byte) (int, error) { return 0, os.ErrPermission }

func (f *fileHandle) Seek(int64, int) (int64, error) { return 0, nil }

func (f *fileHandle) Readdir(int) ([]os.FileInfo, error) { return nil, os.ErrInvalid }

func (f *fileHandle) Stat() (os.FileInfo, error) { return f.info, nil }

type uploadHandle struct {
	fs        *FileSystem
	ctx       context.Context
	accountID int64
	parentID  string
	fileName  string
	tmpPath   string
	file      *os.File
	release   func()
	closed    bool
}

func (u *uploadHandle) Read([]byte) (int, error) { return 0, os.ErrInvalid }

func (u *uploadHandle) Write(p []byte) (int, error) {
	if u.file == nil {
		return 0, os.ErrClosed
	}
	return u.file.Write(p)
}

func (u *uploadHandle) Seek(offset int64, whence int) (int64, error) {
	if u.file == nil {
		return 0, os.ErrClosed
	}
	return u.file.Seek(offset, whence)
}

func (u *uploadHandle) Readdir(int) ([]os.FileInfo, error) { return nil, os.ErrInvalid }

func (u *uploadHandle) Stat() (os.FileInfo, error) {
	if u.file == nil {
		return nil, os.ErrClosed
	}
	st, err := u.file.Stat()
	if err != nil {
		return nil, err
	}
	return &nodeInfo{name: u.fileName, size: st.Size(), mode: 0o644, mod: st.ModTime()}, nil
}

type noopUpload struct{}

func (n *noopUpload) Close() error                       { return nil }
func (n *noopUpload) Read([]byte) (int, error)           { return 0, io.EOF }
func (n *noopUpload) Write(p []byte) (int, error)        { return len(p), nil }
func (n *noopUpload) Seek(int64, int) (int64, error)     { return 0, nil }
func (n *noopUpload) Readdir(int) ([]os.FileInfo, error) { return nil, io.EOF }
func (n *noopUpload) Stat() (os.FileInfo, error) {
	return &nodeInfo{name: ".DS_Store", mode: 0o644}, nil
}
