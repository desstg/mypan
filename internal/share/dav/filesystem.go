package dav

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"golang.org/x/net/webdav"

	"litepan/internal/file"
	"litepan/internal/upload"
)

type FileSystem struct {
	resolver     *Resolver
	files        *file.Service
	dataDir      string
	tempRegistry *upload.TempRegistry
	log          *slog.Logger
}

func (fs *FileSystem) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	_ = perm
	parsed := ParseWebDAVPath(name)
	if parsed.AccountName == "" {
		return os.ErrPermission
	}
	if isMacOSMetadataPath(append([]string{parsed.AccountName}, parsed.RelParts...)) {
		return nil
	}
	if len(parsed.RelParts) == 0 {
		return os.ErrExist
	}
	folderName := parsed.RelParts[len(parsed.RelParts)-1]
	parentParts := parsed.RelParts[:len(parsed.RelParts)-1]
	acc, err := fs.resolver.accountByName(ctx, parsed.AccountName)
	if err != nil {
		return err
	}
	parentID := "0"
	if len(parentParts) > 0 {
		parentItem, _, err := fs.resolver.resolveUnderAccount(ctx, acc.ID, parentParts)
		if err != nil {
			return err
		}
		if !parentItem.IsDir {
			return os.ErrInvalid
		}
		parentID = parentItem.ID
	}
	if _, err := fs.files.CreateFolder(ctx, acc.ID, parentID, folderName); err != nil {
		return err
	}
	return nil
}

func (fs *FileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	_ = perm
	write := flag&os.O_WRONLY != 0 || flag&(os.O_RDWR) != 0
	if write {
		return fs.openUpload(ctx, name, flag)
	}
	node, err := fs.resolver.Resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	if node.IsRoot || node.IsAccount || node.Item.IsDir {
		items, err := fs.resolver.ListChildren(ctx, node)
		if err != nil {
			return nil, err
		}
		return &dirHandle{info: fileInfoFromNode(node), entries: items}, nil
	}
	return &fileHandle{info: fileInfoFromNode(node)}, nil
}

func (fs *FileSystem) RemoveAll(ctx context.Context, name string) error {
	parsed := ParseWebDAVPath(name)
	if parsed.AccountName == "" {
		return os.ErrPermission
	}
	if isMacOSMetadataPath(append([]string{parsed.AccountName}, parsed.RelParts...)) {
		return nil
	}
	node, err := fs.resolver.Resolve(ctx, name)
	if err != nil {
		return err
	}
	if node.IsAccount {
		return os.ErrPermission
	}
	return fs.files.DeleteFiles(ctx, node.Account.ID, []string{node.Item.ID}, node.ParentID)
}

func (fs *FileSystem) Rename(ctx context.Context, oldName, newName string) error {
	oldParsed := ParseWebDAVPath(oldName)
	newParsed := ParseWebDAVPath(newName)
	if oldParsed.AccountName == "" || newParsed.AccountName == "" {
		return os.ErrPermission
	}
	if !strings.EqualFold(oldParsed.AccountName, newParsed.AccountName) {
		return os.ErrPermission
	}
	if len(newParsed.RelParts) == 0 {
		return os.ErrInvalid
	}
	src, err := fs.resolver.Resolve(ctx, oldName)
	if err != nil {
		return err
	}
	if src.IsAccount {
		return os.ErrPermission
	}
	dstName := newParsed.RelParts[len(newParsed.RelParts)-1]
	dstParentParts := newParsed.RelParts[:len(newParsed.RelParts)-1]
	targetParentID := "0"
	if len(dstParentParts) > 0 {
		dstParent, _, err := fs.resolver.resolveUnderAccount(ctx, src.Account.ID, dstParentParts)
		if err != nil {
			return err
		}
		if !dstParent.IsDir {
			return os.ErrInvalid
		}
		targetParentID = dstParent.ID
	}
	sourceParentID := src.ParentID
	if sourceParentID == targetParentID {
		return fs.files.RenameFile(ctx, src.Account.ID, src.Item.ID, dstName, sourceParentID)
	}
	if err := fs.files.MoveFiles(ctx, src.Account.ID, []string{src.Item.ID}, targetParentID, sourceParentID); err != nil {
		return err
	}
	if dstName != src.Item.Name {
		return fs.files.RenameFile(ctx, src.Account.ID, src.Item.ID, dstName, targetParentID)
	}
	return nil
}

func (fs *FileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	parsed := ParseWebDAVPath(name)
	if parsed.AccountName != "" && len(parsed.RelParts) > 0 {
		if isMacOSMetadataPath(append([]string{parsed.AccountName}, parsed.RelParts...)) {
			return &nodeInfo{name: parsed.RelParts[len(parsed.RelParts)-1], mode: os.ModeDir | 0o755, dir: true}, nil
		}
	}
	node, err := fs.resolver.Resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	return fileInfoFromNode(node), nil
}

var _ webdav.FileSystem = (*FileSystem)(nil)
