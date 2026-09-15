package localfs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/driver/uploadutil"
)

const uploadChunkSize = 1024 * 1024

type localUploadResumeCtx struct {
	parentID      string
	requestedName string
	targetPath    string
	fileSize      int64
	uploadedBytes int64
}

func (d *Driver) resolveDir(parentID string) (string, error) {
	root := filepath.Clean(d.root())
	if parentID == "" || parentID == "0" || parentID == "/" {
		return root, nil
	}
	target := filepath.Clean(parentID)
	if err := d.ensureWithinRoot(target); err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", domain.Wrap(domain.CodeNotFound, err)
	}
	if !info.IsDir() {
		return "", domain.Errorf(domain.CodeValidation, "目标不是目录")
	}
	return target, nil
}

func (d *Driver) resolveEntry(fileID string) (string, error) {
	root := filepath.Clean(d.root())
	if fileID == "" || fileID == "0" {
		return root, nil
	}
	target := filepath.Clean(fileID)
	if err := d.ensureWithinRoot(target); err != nil {
		return "", err
	}
	return target, nil
}

func (d *Driver) ensureWithinRoot(p string) error {
	root := filepath.Clean(d.root())
	clean, err := filepath.Abs(filepath.Clean(p))
	if err != nil || !isSubPath(root, clean) {
		return domain.Errorf(domain.CodeValidation, "路径超出根目录")
	}
	realRoot := d.rootReal
	if realRoot == "" {
		realRoot, err = filepath.EvalSymlinks(root)
		if err != nil {
			return domain.Wrap(domain.CodeValidation, err)
		}
	}
	realTarget, err := filepath.EvalSymlinks(clean)
	if err != nil {
		if !os.IsNotExist(err) {
			return domain.Wrap(domain.CodeDriverError, err)
		}
		realParent, parentErr := filepath.EvalSymlinks(filepath.Dir(clean))
		if parentErr != nil {
			if os.IsNotExist(parentErr) {
				return domain.Wrap(domain.CodeNotFound, parentErr)
			}
			return domain.Wrap(domain.CodeDriverError, parentErr)
		}
		realTarget = filepath.Join(realParent, filepath.Base(clean))
	}
	if !isSubPath(realRoot, realTarget) {
		return domain.Errorf(domain.CodeValidation, "路径超出根目录")
	}
	return nil
}

func isSubPath(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func validateEntryName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed == "." || trimmed == ".." {
		return "", domain.Errorf(domain.CodeValidation, "文件名无效")
	}
	if strings.ContainsAny(trimmed, `/\`) {
		return "", domain.Errorf(domain.CodeValidation, "文件名无效")
	}
	return trimmed, nil
}

func suffixForUnique(target string) string {
	dir := filepath.Dir(target)
	base := filepath.Base(target)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; i < 1000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, 999, ext))
}

func (d *Driver) CreateFolder(ctx context.Context, parentID, name string) (*domain.FileItem, error) {
	_ = ctx
	parent, err := d.resolveDir(parentID)
	if err != nil {
		return nil, err
	}
	folderName, err := validateEntryName(name)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(parent, folderName)
	if _, err := os.Stat(target); err == nil {
		return nil, domain.Errorf(domain.CodeValidation, "当前目录已存在同名文件夹")
	} else if !os.IsNotExist(err) {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	return &domain.FileItem{
		ID:      target,
		Name:    info.Name(),
		IsDir:   true,
		ModTime: info.ModTime(),
		IDKind:  domain.IDPath,
	}, nil
}

func (d *Driver) DeleteFiles(ctx context.Context, fileIDs []string) error {
	_ = ctx
	root := filepath.Clean(d.root())
	for _, id := range fileIDs {
		target, err := d.resolveEntry(id)
		if err != nil {
			return err
		}
		if target == root {
			return domain.Errorf(domain.CodeValidation, "不能删除存储根目录")
		}
		if err := os.RemoveAll(target); err != nil {
			return domain.Wrap(domain.CodeDriverError, err)
		}
	}
	return nil
}

func (d *Driver) UploadLocalFile(ctx context.Context, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	parent, err := d.resolveDir(req.ParentID)
	if err != nil {
		return nil, err
	}
	name, err := validateEntryName(req.FileName)
	if err != nil {
		return nil, err
	}
	srcPath := req.LocalPath
	srcInfo, err := os.Stat(srcPath)
	if err != nil || !srcInfo.Mode().IsRegular() {
		return nil, domain.Errorf(domain.CodeValidation, "本地源文件不存在")
	}
	total := srcInfo.Size()

	resume := normalizeLocalUploadResumeState(req.ResumeState, parent, name, total)
	dst := filepath.Join(parent, name)
	policy := strings.ToLower(strings.TrimSpace(req.ConflictPolicy))
	if policy == "" {
		policy = "overwrite"
	}
	if resume != nil {
		dst = resume.targetPath
	} else if _, err := os.Stat(dst); err == nil {
		switch policy {
		case "skip":
			return &driver.LocalUploadResult{
				FileID:   dst,
				ParentID: parent,
				FileName: name,
				Size:     total,
				Message:  "已跳过同名文件",
				Skipped:  true,
			}, nil
		case "rename":
			dst = suffixForUnique(dst)
		}
	} else if !os.IsNotExist(err) {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}

	tmp := dst + ".part"
	offset := int64(0)
	if resume != nil {
		if info, err := os.Stat(tmp); err == nil && info.Mode().IsRegular() && info.Size() >= 0 && info.Size() <= total {
			offset = info.Size()
		} else {
			resume = nil
		}
	}
	if resume == nil {
		resume = &localUploadResumeCtx{parentID: parent, requestedName: name, targetPath: dst, fileSize: total}
	}
	resume.uploadedBytes = offset
	persistLocalUploadResumeState(req.OnResumeState, resume)
	if offset > 0 {
		uploadutil.NotifyProgress(req.OnProgress, offset, total, "正在继续写入本地存储")
	}
	if err := copyFileWithProgress(ctx, srcPath, tmp, offset, total, "正在写入本地存储", req.OnProgress, func(uploaded int64) {
		resume.uploadedBytes = uploaded
		persistLocalUploadResumeState(req.OnResumeState, resume)
	}); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	if req.ModTime != nil && !req.ModTime.IsZero() {
		atime := time.Now()
		if st, statErr := os.Stat(dst); statErr == nil {
			atime = st.ModTime()
		}
		if err := os.Chtimes(dst, atime, req.ModTime.UTC()); err != nil {
			return nil, domain.Wrap(domain.CodeDriverError, err)
		}
	}
	info, err := os.Stat(dst)
	if err != nil {
		return nil, domain.Wrap(domain.CodeDriverError, err)
	}
	if req.OnProgress != nil {
		req.OnProgress(total, total, "正在写入本地存储")
	}
	return &driver.LocalUploadResult{
		FileID:   dst,
		ParentID: parent,
		FileName: info.Name(),
		Size:     info.Size(),
		Message:  "上传成功",
	}, nil
}

func normalizeLocalUploadResumeState(state map[string]any, parentID, requestedName string, fileSize int64) *localUploadResumeCtx {
	if len(state) == 0 || uploadutil.AnyString(state["parent_id"]) != parentID ||
		uploadutil.AnyString(state["requested_name"]) != requestedName {
		return nil
	}
	resumeSize, ok := uploadutil.MapInt64(state["file_size"])
	targetPath := filepath.Clean(uploadutil.AnyString(state["target_path"]))
	if !ok || resumeSize != fileSize || filepath.Dir(targetPath) != parentID {
		return nil
	}
	return &localUploadResumeCtx{
		parentID:      parentID,
		requestedName: requestedName,
		targetPath:    targetPath,
		fileSize:      fileSize,
		uploadedBytes: uploadutil.ResumeStateUploadedBytes(state),
	}
}

func persistLocalUploadResumeState(onState driver.UploadStateCallback, resume *localUploadResumeCtx) {
	if onState == nil || resume == nil {
		return
	}
	progress := int(resume.uploadedBytes * 100 / uploadutil.Max64(resume.fileSize, 1))
	if resume.uploadedBytes < resume.fileSize && progress > 99 {
		progress = 99
	}
	onState(map[string]any{
		"parent_id":      resume.parentID,
		"requested_name": resume.requestedName,
		"target_path":    resume.targetPath,
		"file_size":      resume.fileSize,
		"uploaded_bytes": resume.uploadedBytes,
		"progress":       progress,
	})
}

func copyFileWithProgress(ctx context.Context, src, dst string, offset, total int64, msg string, onProgress driver.UploadProgress, onCheckpoint func(int64)) error {
	in, err := os.Open(src)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	defer in.Close()

	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	}
	out, err := os.OpenFile(dst, flags, 0o644)
	if err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	defer func() {
		_ = out.Close()
	}()
	if _, err := in.Seek(offset, io.SeekStart); err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}
	if _, err := out.Seek(offset, io.SeekStart); err != nil {
		return domain.Wrap(domain.CodeDriverError, err)
	}

	buf := make([]byte, uploadChunkSize)
	uploaded := offset
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, readErr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return domain.Wrap(domain.CodeDriverError, werr)
			}
			uploaded += int64(n)
			if onCheckpoint != nil {
				onCheckpoint(uploaded)
			}
			if onProgress != nil {
				onProgress(uploaded, total, msg)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return domain.Wrap(domain.CodeDriverError, readErr)
		}
	}
	return nil
}
