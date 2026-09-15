package app

import (
	"context"
	"fmt"

	"litepan/internal/cacheretention"
	"litepan/internal/domain"
	"litepan/internal/favorites"
	"litepan/internal/fusemount"
	"litepan/internal/fusereadcache"
	"litepan/internal/mediaorganize"
	"litepan/internal/offlinedownload"
	"litepan/internal/quarktv"
	"litepan/internal/strm"
	"litepan/internal/upload"
)

type accountLifecycle struct {
	fuse      *fusemount.Service
	readCache *fusereadcache.Service
	strm      *strm.Coordinator
	strmSvc   *strm.Service
	retention *cacheretention.Coordinator
	media     *mediaorganize.Service
	favorites *favorites.Service
	offline   *offlinedownload.Service
	uploads   *upload.Manager
	quarktv   *quarktv.Service
}

func (a accountLifecycle) OnAccountDisabled(ctx context.Context, accountID int64) {
	if accountID <= 0 {
		return
	}
	if a.strm != nil {
		_, _ = a.strm.PauseByAccount(ctx, accountID, domain.PauseReasonAccountDisabled, "关联的账号已禁用")
	}
	if a.retention != nil {
		_, _ = a.retention.PauseByAccount(ctx, accountID, domain.PauseReasonAccountDisabled, "关联的账号已禁用")
	}
}

func (a accountLifecycle) OnAccountEnabled(ctx context.Context, accountID int64) {
	if accountID <= 0 {
		return
	}
	if a.strm != nil {
		_, _ = a.strm.ResumeByAccount(ctx, accountID)
	}
	if a.retention != nil {
		_, _ = a.retention.ResumeByAccount(ctx, accountID)
	}
}

func (a accountLifecycle) OnAccountDeleted(ctx context.Context, accountID int64) error {
	if accountID <= 0 {
		return nil
	}
	if a.fuse != nil {
		if err := a.fuse.OnAccountDeleted(ctx, accountID); err != nil {
			return err
		}
	}
	if a.readCache != nil {
		if err := a.readCache.InvalidateAccount(ctx, accountID); err != nil {
			return fmt.Errorf("清理 FUSE 读缓存失败: %w", err)
		}
	}
	if a.strm != nil {
		if _, err := a.strm.RemoveTasksByAccount(ctx, accountID); err != nil {
			return fmt.Errorf("清理 STRM 任务失败: %w", err)
		}
	}
	if a.strmSvc != nil {
		if _, err := a.strmSvc.ClearDirCache(ctx, accountID); err != nil {
			return fmt.Errorf("清理 STRM 路径映射表失败: %w", err)
		}
	}
	if a.retention != nil {
		if _, err := a.retention.RemoveTasksByAccount(ctx, accountID); err != nil {
			return fmt.Errorf("清理缓存保持任务失败: %w", err)
		}
	}
	if a.media != nil {
		if _, err := a.media.RemoveTasksByAccount(ctx, accountID); err != nil {
			return fmt.Errorf("清理媒体整理任务失败: %w", err)
		}
	}
	if a.favorites != nil {
		if err := a.favorites.Delete(ctx, accountID); err != nil {
			return fmt.Errorf("清理收藏夹失败: %w", err)
		}
	}
	if a.offline != nil {
		if _, err := a.offline.RemoveTasksByAccount(ctx, accountID); err != nil {
			return fmt.Errorf("清理离线下载任务失败: %w", err)
		}
	}
	if a.uploads != nil {
		if _, err := a.uploads.RemoveTasksByAccount(ctx, accountID); err != nil {
			return fmt.Errorf("清理上传任务失败: %w", err)
		}
	}
	if a.quarktv != nil {
		if err := a.quarktv.DeleteBinding(ctx, accountID); err != nil {
			return fmt.Errorf("清理夸克 TV 绑定失败: %w", err)
		}
	}
	return nil
}
