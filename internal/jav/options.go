package jav

import (
	"context"
	"log/slog"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
	"litepan/internal/offlinedownload"
	"litepan/internal/settings"
)

// Options 是番号模块的依赖集合。
//
// 依赖以**仓储接口 + 服务**的形式注入，不直接吃 *store.Store ——
// 测试可以只塞内存库里的几个仓储，不必把整个 Store 拖进来。
type Options struct {
	Movies     domain.JavMovieRepository
	Magnets    domain.JavMagnetRepository
	Reviews    domain.JavReviewRepository
	Subs       domain.JavSubscriptionRepository
	Runs       domain.JavRunRepository
	Candidates domain.JavCandidateRepository
	Attempts   domain.JavPushAttemptRepository
	Blacklist  domain.JavBlacklistRepository
	Follows    domain.JavFollowRepository
	Skips      domain.JavSkipRepository
	Lists      domain.JavListMovieRepository
	Servers    domain.JavMediaServerRepository
	Library    domain.JavLibraryRepository
	Records    domain.JavPushRecordRepository

	Settings *settings.Service
	Bus      *eventbus.Bus
	Log      *slog.Logger

	// Offline 是磁力投递的落点。为空时推送整体不可用，但订阅检查照常能跑 ——
	// 「先看匹配得对不对、先别推」是这套系统里最常见的用法。
	//
	// 类型是窄接口而不是 *offlinedownload.Service，只为让测试注入桩：
	// 假实现只要十来行，而真实实现天然满足它。
	Offline OfflinePusher

	// Folders 用来确保推送目标下的子目录存在，以及给推送成功的资源写元数据侧车。
	// 为空时不做子目录（资源直接落在目标目录里）、也不写侧车。
	Folders FolderStore
}

// FolderStore 是 file.Service 的一个窄切面：建目录 / 列目录 / 查条目 / 上传小文件。
//
// 类型是窄接口而不是 *file.Service，只为让测试注入桩 —— 与 Offline 同一个理由。
// *file.Service 天然满足它，接线处一行都不用改。
type FolderStore interface {
	CreateFolder(ctx context.Context, accountID int64, parentID, name string) (*domain.FileItem, error)
	List(ctx context.Context, accountID int64, parentID string, forceRefresh bool) ([]domain.FileItem, error)
	Info(ctx context.Context, accountID int64, fileID string) (*domain.FileItem, error)
	UploadLocal(ctx context.Context, accountID int64, req driver.LocalUploadRequest) (*driver.LocalUploadResult, error)
}

// OfflinePusher 是离线下载服务的一个很窄的切面：探能力 + 提交。
//
// *offlinedownload.Service 天然满足它。
type OfflinePusher interface {
	Capabilities(ctx context.Context, accountID int64) (offlinedownload.Capabilities, error)
	AddURLs(ctx context.Context, p offlinedownload.AddURLParams) ([]offlinedownload.Task, error)
}
