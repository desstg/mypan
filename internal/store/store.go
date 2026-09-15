package store

import "litepan/internal/domain"

// Store 聚合各仓储实现，作为上层注入的统一入口。
type Store struct {
	DB                  *DB
	Accounts            domain.AccountRepository
	AuthStates          domain.AuthStateRepository
	Configs             domain.ConfigRepository
	Notifications       domain.NotificationRepository
	ApiKeys             domain.ApiKeyRepository
	StrmTasks           domain.StrmTaskRepository
	StrmBranches        domain.StrmBranchRepository
	StrmDirCache        domain.StrmDirCacheRepository
	UploadTasks         domain.UploadTaskRepository
	OfflineDownloads    domain.OfflineDownloadTaskRepository
	MediaOrganizeTasks  domain.MediaOrganizeTaskRepository
	FuseMounts          domain.FuseMountRepository
	CacheRetentionTasks domain.CacheRetentionTaskRepository
	AutomationRules     domain.AutomationRuleRepository
	AutomationRuns      domain.AutomationRunRepository
	QuarkTVBindings     domain.QuarkTVBindingRepository
}

// New 基于已打开的 DB 构造仓储集合。
func New(db *DB) *Store {
	return &Store{
		DB:                  db,
		Accounts:            &accountRepo{db: db},
		AuthStates:          &authStateRepo{db: db},
		Configs:             &configRepo{db: db},
		Notifications:       &notificationRepo{db: db},
		ApiKeys:             &apiKeyRepo{db: db},
		StrmTasks:           &strmTaskRepo{db: db},
		StrmBranches:        &strmBranchRepo{db: db},
		StrmDirCache:        &strmDirCacheRepo{db: db},
		UploadTasks:         &uploadTaskRepo{db: db},
		OfflineDownloads:    &offlineDownloadTaskRepo{db: db},
		MediaOrganizeTasks:  &mediaOrganizeTaskRepo{db: db},
		FuseMounts:          &fuseMountRepo{db: db},
		CacheRetentionTasks: &cacheRetentionRepo{db: db},
		AutomationRules:     &automationRuleRepo{db: db},
		AutomationRuns:      &automationRunRepo{db: db},
		QuarkTVBindings:     &quarktvBindingRepo{db: db},
	}
}
