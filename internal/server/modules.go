package server

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IsorilovA/pauza-server/internal/config"
	"github.com/IsorilovA/pauza-server/internal/handler"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/photostore"
	"github.com/IsorilovA/pauza-server/internal/push"
	"github.com/IsorilovA/pauza-server/internal/repository"
	"github.com/IsorilovA/pauza-server/internal/revenuecat"
	"github.com/IsorilovA/pauza-server/internal/service"
)

type appDependencies struct {
	authHandler    *handler.AuthHandler
	syncHandler    *handler.SyncHandler
	socialHandler  *handler.SocialHandler
	adminHandler   *handler.AdminHandler
	webhookHandler *handler.WebhookHandler
}

func buildDependencies(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool, mailer mail.Sender, pushSender push.Sender) appDependencies {
	authModule := buildAuthModule(cfg, logger, pool, mailer)
	syncModule := buildSyncModule(logger, pool, authModule.authRepo)
	socialModule := buildSocialModule(logger, pool, authModule.authRepo, pushSender)
	adminModule := buildAdminModule(cfg, logger, pool)
	webhookModule := buildWebhookModule(cfg, logger, pool, authModule.authRepo, adminModule.adminRepo, adminModule.entitlementRepo)

	return appDependencies{
		authHandler:    authModule.handler,
		syncHandler:    syncModule.handler,
		socialHandler:  socialModule.handler,
		adminHandler:   adminModule.handler,
		webhookHandler: webhookModule.handler,
	}
}

type authModule struct {
	handler  *handler.AuthHandler
	authRepo repository.AuthRepository
}

func buildAuthModule(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool, mailer mail.Sender) authModule {
	authRepo := repository.NewPgxAuthRepository()
	authService := service.NewAuthService(
		pool, authRepo, mailer, cfg.JWTSecret,
		cfg.JWTAccessTokenTTL, cfg.JWTRefreshTokenTTL, logger,
	)
	photoStore := photostore.NewFileStore(cfg.PhotoStorageDir, cfg.PhotoPublicBaseURL)
	return authModule{
		handler:  handler.NewAuthHandlerWithPhotoStore(authService, photoStore, logger),
		authRepo: authRepo,
	}
}

type syncModule struct {
	handler *handler.SyncHandler
}

func buildSyncModule(logger *slog.Logger, pool *pgxpool.Pool, authRepo repository.AuthRepository) syncModule {
	syncRepo := repository.NewPgxSyncRepository()
	syncService := service.NewSyncService(pool, syncRepo, authRepo, logger)
	return syncModule{handler: handler.NewSyncHandlerWithLogger(syncService, logger)}
}

type socialModule struct {
	handler *handler.SocialHandler
}

func buildSocialModule(logger *slog.Logger, pool *pgxpool.Pool, authRepo repository.AuthRepository, pushSender push.Sender) socialModule {
	socialRepo := repository.NewSocialRepository()
	if pushSender == nil {
		pushSender = push.NewNoopSender(logger)
	}
	pushSender = push.NewPreferenceSender(pool, authRepo, pushSender, logger)
	socialService := service.NewSocialService(pool, socialRepo, pushSender, logger)
	return socialModule{handler: handler.NewSocialHandler(socialService, socialService, socialService)}
}

type adminModule struct {
	handler         *handler.AdminHandler
	adminRepo       repository.AdminRepository
	entitlementRepo repository.EntitlementRepository
}

func buildAdminModule(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool) adminModule {
	adminRepo := repository.NewPgxAdminRepository()
	entitlementRepo := repository.NewPgxEntitlementRepository()
	adminService := service.NewAdminService(pool, adminRepo, entitlementRepo, cfg.JWTSecret, cfg.AdminJWTAccessTokenTTL, logger)
	return adminModule{
		handler:         handler.NewAdminHandler(adminService, adminService, adminService, adminService, logger),
		adminRepo:       adminRepo,
		entitlementRepo: entitlementRepo,
	}
}

type webhookModule struct {
	handler *handler.WebhookHandler
}

func buildWebhookModule(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool, authRepo repository.AuthRepository, adminRepo repository.AdminRepository, entitlementRepo repository.EntitlementRepository) webhookModule {
	rcClient := revenuecat.NewClient(cfg.RevenueCatAPIKey)
	webhookService := service.NewWebhookService(pool, entitlementRepo, rcClient, authRepo, logger, service.WithOverrideChecker(adminRepo))
	return webhookModule{handler: handler.NewWebhookHandler(webhookService, cfg.RevenueCatWebhookSecret, logger)}
}
