package file

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"litepan/internal/cache"
	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
	"litepan/internal/settings"
)

// defaultDirTTL 是 settings 不可用（如测试）时的目录缓存兜底时长。
const defaultDirTTL = 30 * time.Minute

// Service 跨驱动文件浏览；读走缓存，写后发 FileMutated。
type Service struct {
	exec     *driverexec.Executor
	cache    *cache.Service
	accounts domain.AccountRepository
	bus      *eventbus.Bus
	settings *settings.Service
	listHits *cache.HitTracker
	log      *slog.Logger
}

func NewService(exec *driverexec.Executor, c *cache.Service, accounts domain.AccountRepository, bus *eventbus.Bus, set *settings.Service, listHits *cache.HitTracker) *Service {
	return &Service{exec: exec, cache: c, accounts: accounts, bus: bus, settings: set, listHits: listHits, log: slog.Default()}
}

// SetLogger 装配期注入 file_op 模块 logger；不调用则回落 slog.Default。
func (s *Service) SetLogger(log *slog.Logger) {
	if log != nil {
		s.log = log
	}
}

// InvalidateDirectoryCaches 清除指定账号的全部目录列表缓存。
func (s *Service) InvalidateDirectoryCaches(accountID int64) {
	if s == nil {
		return
	}
	cache.InvalidateAccountDirKeys(s.cache, accountID)
}

// List 列举某账号下 parentID 目录的子项。forceRefresh 时跳过并失效该目录缓存。
func (s *Service) List(ctx context.Context, accountID int64, parentID string, forceRefresh bool) ([]domain.FileItem, error) {
	parentID = cache.NormalizeDirParentID(parentID)
	if err := s.exec.Check(ctx, accountID); err != nil {
		return nil, err
	}
	if s.cache == nil {
		return s.listFromDriver(ctx, accountID, parentID)
	}

	ttl := s.dirTTL(ctx, accountID)
	if forceRefresh {
		cache.InvalidateDirKeys(s.cache, accountID, parentID)
	}
	if ttl <= 0 || s.cache.DirIsCooling(accountID, parentID) {
		return s.listFromDriver(ctx, accountID, parentID)
	}

	key := cache.DirKey(accountID, parentID)
	items, hit, err := cache.GetOrLoadAs[cache.DirList](ctx, s.cache, key, ttl, func(callCtx context.Context) (cache.DirList, error) {
		if s.cache.DirIsCooling(accountID, parentID) {
			return s.listFromDriver(callCtx, accountID, parentID)
		}
		var list cache.DirList
		lerr := s.exec.Run(callCtx, accountID, func(drv driver.Driver) error {
			got, err := drv.ListFiles(callCtx, parentID)
			if err != nil {
				return err
			}
			list = got
			return nil
		})
		if lerr != nil {
			return nil, lerr
		}
		return list, nil
	})
	if err != nil {
		return nil, err
	}
	if hit {
		s.recordListHit(true)
	} else {
		s.recordListHit(false)
	}
	return items, nil
}

func (s *Service) listFromDriver(ctx context.Context, accountID int64, parentID string) ([]domain.FileItem, error) {
	var items []domain.FileItem
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		got, err := drv.ListFiles(ctx, parentID)
		if err != nil {
			return err
		}
		items = got
		return nil
	})
	if err == nil {
		s.recordListHit(false)
	}
	return items, err
}

// ListAllFiles 走 STRM 清单模式：驱动支持 FullListLister 时一次拉取
// parentID 下全部文件（含子孙目录）；未实现返回 CodeNotImplement。
func (s *Service) ListAllFiles(ctx context.Context, accountID int64, parentID string) ([]driver.FullListEntry, error) {
	if err := s.exec.Check(ctx, accountID); err != nil {
		return nil, err
	}
	var out []driver.FullListEntry
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		cap, ok := drv.(driver.FullListLister)
		if !ok {
			return domain.Errorf(domain.CodeNotImplement, "驱动不支持清单模式")
		}
		got, err := cap.ListAllFiles(ctx, parentID)
		if err != nil {
			return err
		}
		out = got
		return nil
	})
	return out, err
}

func (s *Service) SupportsFullList(ctx context.Context, accountID int64) (bool, error) {
	if err := s.exec.Check(ctx, accountID); err != nil {
		return false, err
	}
	var supported bool
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		_, supported = drv.(driver.FullListLister)
		return nil
	})
	return supported, err
}

// ResolveDirPath 反查目录完整远端路径（清单模式 pid→路径 补漏用）。
func (s *Service) ResolveDirPath(ctx context.Context, accountID int64, dirID string) (string, error) {
	if err := s.exec.Check(ctx, accountID); err != nil {
		return "", err
	}
	var out string
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		cap, ok := drv.(driver.FullListLister)
		if !ok {
			return domain.Errorf(domain.CodeNotImplement, "驱动不支持清单模式")
		}
		got, err := cap.ResolveDirPath(ctx, dirID)
		if err != nil {
			return err
		}
		out = got
		return nil
	})
	return out, err
}

// WarmAccount 挂载/联调前预热账号：强制刷新根目录列表，初始化驱动实例并触发 OAuth 被动续期。
func (s *Service) WarmAccount(ctx context.Context, accountID int64) error {
	_, err := s.List(ctx, accountID, "", true)
	return err
}

// Info 获取单文件信息：驱动未实现该能力时按 NOT_IMPLEMENT 降级。
func (s *Service) Info(ctx context.Context, accountID int64, fileID string) (*domain.FileItem, error) {
	if err := s.exec.Check(ctx, accountID); err != nil {
		return nil, err
	}
	if s.cache == nil {
		return s.infoFromDriver(ctx, accountID, fileID)
	}

	ttl := s.dirTTL(ctx, accountID)
	if ttl <= 0 {
		return s.infoFromDriver(ctx, accountID, fileID)
	}

	key := cache.FileInfoKey(accountID, fileID)
	info, _, err := cache.GetOrLoadAs[cache.FileInfo](ctx, s.cache, key, ttl, func(callCtx context.Context) (cache.FileInfo, error) {
		item, err := s.infoFromDriver(callCtx, accountID, fileID)
		if err != nil {
			return cache.FileInfo{}, err
		}
		return *item, nil
	})
	if err != nil {
		return nil, err
	}
	item := info
	return &item, nil
}

func (s *Service) infoFromDriver(ctx context.Context, accountID int64, fileID string) (*domain.FileItem, error) {
	var item *domain.FileItem
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		g, ok := drv.(driver.InfoGetter)
		if !ok {
			return domain.Errf(domain.CodeNotImplement)
		}
		got, err := g.GetFileInfo(ctx, fileID)
		if err != nil {
			return err
		}
		item = got
		return nil
	})
	return item, err
}

func (s *Service) DeleteFiles(ctx context.Context, accountID int64, fileIDs []string, parentID string) error {
	parentID = cache.NormalizeDirParentID(parentID)
	if err := s.exec.Check(ctx, accountID); err != nil {
		return err
	}
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		deleter, ok := drv.(driver.Deleter)
		if !ok {
			return domain.Errf(domain.CodeNotImplement)
		}
		return deleter.DeleteFiles(ctx, fileIDs)
	})
	if err != nil {
		s.log.Warn("删除文件失败", "account_id", accountID, "count", len(fileIDs), "err", err)
		return err
	}
	if s.cache != nil && parentID != "" {
		cache.InvalidateDirKeys(s.cache, accountID, parentID)
	}
	s.log.Debug("删除文件成功", "account_id", accountID, "count", len(fileIDs), "parent_id", parentID)
	s.publishMutation(ctx, eventbus.FileMutated{AccountID: accountID, Op: "delete", ParentID: parentID, FileIDs: fileIDs})
	return nil
}

func (s *Service) MoveFiles(ctx context.Context, accountID int64, fileIDs []string, targetParentID, sourceParentID string) error {
	targetParentID = cache.NormalizeDirParentID(targetParentID)
	sourceParentID = cache.NormalizeDirParentID(sourceParentID)
	if err := s.exec.Check(ctx, accountID); err != nil {
		return err
	}
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		mover, ok := drv.(driver.Mover)
		if !ok {
			return domain.Errf(domain.CodeNotImplement)
		}
		return mover.MoveFiles(ctx, fileIDs, targetParentID, sourceParentID)
	})
	if err != nil {
		s.log.Warn("移动文件失败", "account_id", accountID, "count", len(fileIDs), "err", err)
		return err
	}
	s.log.Debug("移动文件成功", "account_id", accountID, "count", len(fileIDs), "source", sourceParentID, "target", targetParentID)
	s.publishMutation(ctx, eventbus.FileMutated{
		AccountID:   accountID,
		Op:          "move",
		ParentID:    targetParentID,
		OldParentID: sourceParentID,
		FileIDs:     fileIDs,
	})
	return nil
}

func (s *Service) CopyFiles(ctx context.Context, accountID int64, fileIDs []string, targetParentID string) error {
	targetParentID = cache.NormalizeDirParentID(targetParentID)
	if err := s.exec.Check(ctx, accountID); err != nil {
		return err
	}
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		copier, ok := drv.(driver.Copier)
		if !ok {
			return domain.Errf(domain.CodeNotImplement)
		}
		return copier.CopyFiles(ctx, fileIDs, targetParentID)
	})
	if err != nil {
		s.log.Warn("复制文件失败", "account_id", accountID, "count", len(fileIDs), "err", err)
		return err
	}
	s.log.Debug("复制文件成功", "account_id", accountID, "count", len(fileIDs), "target", targetParentID)
	s.publishMutation(ctx, eventbus.FileMutated{AccountID: accountID, Op: "copy", ParentID: targetParentID})
	return nil
}

func (s *Service) RenameFile(ctx context.Context, accountID int64, fileID, newName, parentID string) error {
	parentID = cache.NormalizeDirParentID(parentID)
	if err := s.exec.Check(ctx, accountID); err != nil {
		return err
	}
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		renamer, ok := drv.(driver.Renamer)
		if !ok {
			return domain.Errf(domain.CodeNotImplement)
		}
		return renamer.RenameFile(ctx, fileID, newName)
	})
	if err != nil {
		s.log.Warn("重命名失败", "account_id", accountID, "file_id", fileID, "new_name", newName, "err", err)
		return err
	}
	s.log.Debug("重命名成功", "account_id", accountID, "file_id", fileID, "new_name", newName)
	s.publishMutation(ctx, eventbus.FileMutated{AccountID: accountID, Op: "rename", ParentID: parentID, FileID: fileID})
	return nil
}

func (s *Service) CreateFolder(ctx context.Context, accountID int64, parentID, name string) (*domain.FileItem, error) {
	parentID = cache.NormalizeDirParentID(parentID)
	if err := s.exec.Check(ctx, accountID); err != nil {
		return nil, err
	}
	var item *domain.FileItem
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		creator, ok := drv.(driver.FolderCreator)
		if !ok {
			return domain.Errf(domain.CodeNotImplement)
		}
		got, err := creator.CreateFolder(ctx, parentID, name)
		if err != nil {
			return err
		}
		item = got
		return nil
	})
	if err != nil {
		s.log.Warn("创建文件夹失败", "account_id", accountID, "name", name, "err", err)
		return nil, err
	}
	s.log.Debug("创建文件夹成功", "account_id", accountID, "name", name, "parent_id", parentID)
	s.publishMutation(ctx, eventbus.FileMutated{
		AccountID: accountID,
		Op:        "create",
		ParentID:  parentID,
		FileID:    item.ID,
		FileName:  item.Name,
		FileSize:  item.Size,
		IsDir:     item.IsDir,
		ModTime:   item.ModTime,
	})
	return item, nil
}

// UploadLocal 从服务器本地临时文件上传到网盘；成功后同步失效父目录缓存并发布 FileMutated。
func (s *Service) UploadLocal(ctx context.Context, accountID int64, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error) {
	if err := s.exec.Check(ctx, accountID); err != nil {
		return nil, err
	}
	var result *driver.LocalUploadResult
	err := s.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		uploader, ok := drv.(driver.LocalUploader)
		if !ok {
			return domain.Errf(domain.CodeNotImplement)
		}
		r, err := uploader.UploadLocalFile(ctx, req)
		if err != nil {
			return err
		}
		result = r
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			s.log.Debug("上传文件已取消", "account_id", accountID, "name", req.FileName)
		} else {
			s.log.Warn("上传文件失败", "account_id", accountID, "name", req.FileName, "err", err)
		}
		return nil, err
	}
	s.log.Debug("上传文件成功", "account_id", accountID, "name", result.FileName, "size", result.Size)
	parentID := cache.NormalizeDirParentID(req.ParentID)
	resolvedParentID := cache.NormalizeDirParentID(result.ParentID)
	if s.cache != nil && resolvedParentID != "" && resolvedParentID != parentID {
		cache.InvalidateDirKeys(s.cache, accountID, resolvedParentID)
	}
	mut := eventbus.FileMutated{
		AccountID: accountID,
		Op:        "create",
		ParentID:  parentID,
		FileID:    result.FileID,
		FileName:  result.FileName,
		FileSize:  result.Size,
	}
	if req.ModTime != nil && !req.ModTime.IsZero() {
		mut.ModTime = *req.ModTime
	}
	s.publishMutation(ctx, mut)
	return result, nil
}

// NotifyCreated 用于绕过 file.Service 写路径完成创建时，补发标准写后失效事件。
// 例如跨盘秒传命中后由驱动直接落文件，需要调用此方法清理目标目录缓存。
func (s *Service) NotifyCreated(ctx context.Context, accountID int64, parentID, fileID, fileName string, fileSize int64, isDir bool) {
	s.publishMutation(ctx, eventbus.FileMutated{
		AccountID: accountID,
		Op:        "create",
		ParentID:  parentID,
		FileID:    fileID,
		FileName:  fileName,
		FileSize:  fileSize,
		IsDir:     isDir,
	})
}

func (s *Service) recordListHit(hit bool) {
	if s.listHits == nil {
		return
	}
	if hit {
		s.listHits.RecordHit()
	} else {
		s.listHits.RecordMiss()
	}
}

// DirCacheTTL 返回目录/WebDAV 路径缓存 TTL。
func (s *Service) DirCacheTTL(ctx context.Context, accountID int64) time.Duration {
	return s.dirTTL(ctx, accountID)
}

// 先同步失效再发事件，避免写后重列命中旧缓存。
func (s *Service) publishMutation(ctx context.Context, e eventbus.FileMutated) {
	e.ParentID = cache.NormalizeDirParentID(e.ParentID)
	e.OldParentID = cache.NormalizeDirParentID(e.OldParentID)
	if s.cache != nil {
		cache.ApplyMutation(s.cache, e)
	}
	if s.bus != nil {
		s.bus.Publish(ctx, e)
	}
}

// TTL：全局关>账号 cache_ttl>全局默认；账号 0=禁用。
func (s *Service) dirTTL(ctx context.Context, accountID int64) time.Duration {
	if s.settings != nil && !s.settings.Bool(settings.KeyCacheEnabled) {
		return 0
	}
	if s.accounts != nil {
		if acc, err := s.accounts.Get(ctx, accountID); err == nil && acc != nil {
			if minutes, ok := parseCacheTTLMinutes(acc.Config); ok {
				if minutes <= 0 {
					return 0
				}
				return time.Duration(minutes) * time.Minute
			}
		}
	}
	return s.globalDirTTL()
}

// globalDirTTL 返回全局默认目录缓存时长；settings 不可用时回落兜底常量。
func (s *Service) globalDirTTL() time.Duration {
	if s.settings == nil {
		return defaultDirTTL
	}
	minutes := s.settings.Int(settings.KeyCacheTTL)
	if minutes <= 0 {
		return 0
	}
	return time.Duration(minutes) * time.Minute
}

func parseCacheTTLMinutes(configJSON string) (int, bool) {
	s := strings.TrimSpace(configJSON)
	if s == "" || s == "{}" {
		return 0, false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return 0, false
	}
	raw, ok := m["cache_ttl"]
	if !ok {
		return 0, false
	}
	var num json.Number
	if err := json.Unmarshal(raw, &num); err == nil {
		if n, err := num.Int64(); err == nil {
			return int(n), true
		}
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		str = strings.TrimSpace(str)
		if str == "" {
			return 0, false
		}
		if n, err := strconv.Atoi(str); err == nil {
			return n, true
		}
	}
	return 0, false
}
