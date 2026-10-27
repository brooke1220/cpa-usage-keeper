package app

import (
	"context"
	"fmt"
	"io"

	"cpa-usage-keeper/internal/api"
	"cpa-usage-keeper/internal/auth"
	"cpa-usage-keeper/internal/config"
	"cpa-usage-keeper/internal/ingest"
	"cpa-usage-keeper/internal/oidc"
	"cpa-usage-keeper/internal/poller"
	"cpa-usage-keeper/internal/pricing"
	"cpa-usage-keeper/internal/ranking"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/service"
	"cpa-usage-keeper/internal/workhours"
	webui "cpa-usage-keeper/web"
	"gorm.io/gorm"
)

// newPushReceiver 加载设置页维护的推送来源和开关；两种装配都创建它，接收路由始终存在，由开关决定是否收数。
func newPushReceiver(ctx context.Context, db *gorm.DB) (*ingest.Receiver, error) {
	sources, err := ingest.NewSources(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("load push sources: %w", err)
	}
	return ingest.New(db, sources)
}

// buildPushReadyApp 在没有配置 CPA 时只装配本地服务：不创建任何 CPA 客户端，只接收推送。
func buildPushReadyApp(cfg config.Config, db, readDB *gorm.DB, logCloser io.Closer, ctx context.Context) (*App, error) {
	receiver, err := newPushReceiver(ctx, db)
	if err != nil {
		return nil, err
	}
	recent, err := newUsageRecentEventCache(db, repository.UsageRecentEventCacheOptions{})
	if err != nil {
		return nil, err
	}
	prepared := false
	defer func() {
		if !prepared {
			recent.Close()
		}
	}()
	snapshot, err := repository.LoadPricingSnapshot(ctx, db)
	if err != nil {
		return nil, err
	}
	catalog := pricing.NewCatalog(snapshot)
	gate := service.NewCostReadGate()
	aggregation := poller.NewUsageAggregationRunner(db)
	syncService := service.NewSyncServiceWithOptions(db, service.SyncServiceOptions{PricingCatalog: catalog, RecentUsageEvents: recent, UsageAggregationNotifier: aggregation, UsageRawRetentionDays: cfg.UsageRawRetentionDays})
	process := poller.NewRedisProcessRunner(syncService)
	status := poller.NewRedisPoller(nil, process)
	workClock, err := workhours.Load(ctx, db)
	if err != nil {
		return nil, err
	}
	keyTime, err := service.NewAPIKeyTimeUsageService(db, service.APIKeyTimeUsageOptions{Clock: workClock})
	if err != nil {
		return nil, err
	}
	localRanking, err := ranking.NewLocalRankingService(db, ranking.LocalRankingServiceOptions{})
	if err != nil {
		return nil, err
	}
	rankingRunner, err := ranking.NewLocalRankingRunner(localRanking)
	if err != nil {
		return nil, err
	}
	prices := service.NewPricingServiceWithRecalculation(db, catalog, service.PricingRecalculationDependencies{LifecycleCtx: ctx, Sync: syncService, Aggregation: aggregation, CostReadGate: gate, Recent: recent})
	authConfig := appAuthConfig(cfg)
	if cfg.OIDC.Enabled {
		client, err := oidc.NewClient(cfg.OIDC, nil)
		if err != nil {
			return nil, err
		}
		authConfig.OIDC = client
	}
	store := auth.NewGormSessionStore(db)
	if _, err := store.RevokeInvalidOIDCSessions(auth.OIDCSessionPolicy{Enabled: cfg.AuthEnabled && cfg.OIDC.Enabled, Issuer: cfg.OIDC.Issuer, ClientID: cfg.OIDC.ClientID, AllowedSubjects: cfg.OIDC.AllowedSubjects}); err != nil {
		return nil, err
	}
	sessions := auth.NewSessionManager(cfg.AuthSessionTTL)
	if cfg.AuthEnabled {
		sessions = auth.NewPersistentSessionManager(cfg.AuthSessionTTL, store)
	}
	var backups *DatabaseBackupRunner
	if cfg.BackupEnabled {
		sqlDB, err := db.DB()
		if err != nil {
			return nil, fmt.Errorf("open push backup writer: %w", err)
		}
		storage := newDatabaseBackupStore(sqlDB, cfg.BackupDir)
		backups = NewDatabaseBackupRunner(storage, storage, cfg.BackupInterval, cfg.BackupRetentionDays)
	}
	keys := service.NewCPAAPIKeyService(db)
	usage := service.NewUsageServiceWithOptions(db, service.UsageServiceOptions{RecentUsage: recent, PricingCatalog: catalog})
	ready := &App{Config: &cfg, DB: db, ReadDB: readDB, Poller: status, RedisProcess: process, UsageAggregation: aggregation, LocalRanking: rankingRunner, Maintenance: NewStorageCleanupRunner(syncService), BackupMaintenance: backups, RecentUsageCache: recent, CostReadGate: gate, PricingCatalog: catalog, PricingService: prices, LogCloser: logCloser}
	ready.Router = api.NewRouter(webui.Static, status, usage, prices, authConfig, api.NewAuthHandler(authConfig, sessions), cfg.AppBasePath, api.OptionalProviders{CostReadGate: gate, CPAAPIKeys: keys, APIKeyTimeUsage: keyTime, LocalRanking: localRanking, WorkHours: workhours.NewService(db, workClock), Ingest: receiver, Status: api.StatusRouteConfig{IngestMode: cfg.IngestMode(), Ingest: receiver}})
	prepared = true
	return ready, nil
}
