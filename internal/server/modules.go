package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IsorilovA/pauza-server/internal/ai"
	"github.com/IsorilovA/pauza-server/internal/config"
	"github.com/IsorilovA/pauza-server/internal/handler"
	"github.com/IsorilovA/pauza-server/internal/mail"
	authmw "github.com/IsorilovA/pauza-server/internal/middleware"
	"github.com/IsorilovA/pauza-server/internal/photostore"
	"github.com/IsorilovA/pauza-server/internal/push"
	"github.com/IsorilovA/pauza-server/internal/repository"
	"github.com/IsorilovA/pauza-server/internal/revenuecat"
	"github.com/IsorilovA/pauza-server/internal/service"
)

type appDependencies struct {
	authHandler      *handler.AuthHandler
	syncHandler      *handler.SyncHandler
	socialHandler    *handler.SocialHandler
	adminHandler     *handler.AdminHandler
	webhookHandler   *handler.WebhookHandler
	aiHandler        *handler.AIHandler // nil when AI_PROVIDER is not configured
	sessionValidator authmw.SessionValidator
}

// sessionValidator implements middleware.SessionValidator using the auth repository.
type sessionValidator struct {
	repo repository.SessionRepository
	pool repository.Pool
}

func (v *sessionValidator) ValidateSession(ctx context.Context, sessionID string) error {
	if v.pool == nil {
		// No database connection available (e.g. in unit tests); skip validation.
		return nil
	}
	sess, err := v.repo.GetSessionByID(ctx, v.pool, sessionID)
	if err != nil {
		return err
	}
	if sess.Revoked {
		return fmt.Errorf("session revoked")
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		return fmt.Errorf("session expired")
	}
	return nil
}

func buildDependencies(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool, mailer mail.Sender, pushSender push.Sender, aiProvider ai.Provider) appDependencies {
	authRepo := repository.NewPgxAuthRepository()
	adminRepo := repository.NewPgxAdminRepository()
	entitlementRepo := repository.NewPgxEntitlementRepository()

	authService := service.NewAuthService(
		pool,
		authRepo,
		authRepo,
		authRepo,
		authRepo, // sessions
		authRepo,
		mailer,
		cfg.JWTSecret,
		cfg.JWTAccessTokenTTL,
		cfg.JWTRefreshTokenTTL,
		logger,
	)
	authHandler := handler.NewAuthHandler(
		authService,
		photostore.NewFileStore(cfg.PhotoStorageDir, cfg.PhotoPublicBaseURL),
		logger,
	)

	syncService := service.NewSyncService(pool, repository.NewPgxSyncRepository(repository.NewSocialRepository()), authRepo, logger)
	syncHandler := handler.NewSyncHandler(syncService, logger)

	if pushSender == nil {
		pushSender = push.NewNoopSender(logger)
	}
	pushSender = push.NewPreferenceSender(pool, authRepo, pushSender, logger)
	socialService := service.NewSocialService(pool, repository.NewSocialRepository(), pushSender, logger)
	socialHandler := handler.NewSocialHandler(socialService, logger)

	adminService := service.NewAdminService(pool, adminRepo, cfg.JWTSecret, cfg.AdminJWTAccessTokenTTL, logger)
	adminHandler := handler.NewAdminHandler(adminService, logger)

	rcClient := revenuecat.NewClient(cfg.RevenueCatAPIKey)
	webhookService := service.NewWebhookService(pool, entitlementRepo, rcClient, authRepo, logger, service.WithOverrideChecker(adminRepo))
	webhookHandler := handler.NewWebhookHandler(webhookService, cfg.RevenueCatWebhookSecret, logger)

	var aiHandler *handler.AIHandler
	if aiProvider != nil {
		socialRepo := repository.NewSocialRepository()
		aiService := service.NewAIService(aiProvider, pool, socialRepo, logger, cfg.AITimeout)
		aiHandler = handler.NewAIHandler(aiService, logger)
	}

	var sv *sessionValidator
	if pool != nil {
		sv = &sessionValidator{repo: authRepo, pool: pool}
	} else {
		sv = &sessionValidator{repo: authRepo}
	}

	return appDependencies{
		authHandler:      authHandler,
		syncHandler:      syncHandler,
		socialHandler:    socialHandler,
		adminHandler:     adminHandler,
		webhookHandler:   webhookHandler,
		aiHandler:        aiHandler,
		sessionValidator: sv,
	}
}
