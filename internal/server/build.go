package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/IsorilovA/pauza-server/internal/config"
	"github.com/IsorilovA/pauza-server/internal/handler"
	"github.com/IsorilovA/pauza-server/internal/mail"
	authmw "github.com/IsorilovA/pauza-server/internal/middleware"
	"github.com/IsorilovA/pauza-server/internal/photostore"
	"github.com/IsorilovA/pauza-server/internal/push"
	"github.com/IsorilovA/pauza-server/internal/ratelimit"
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

type appLimiters struct {
	auth      ratelimit.Limiter
	verifyOTP ratelimit.Limiter
	general   ratelimit.Limiter
	sync      ratelimit.Limiter
	webhook   ratelimit.Limiter
	admin     ratelimit.Limiter
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
	return socialModule{handler: handler.NewSocialHandler(service.NewSocialService(pool, socialRepo, pushSender, logger))}
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
		handler:         handler.NewAdminHandler(adminService, logger),
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

func buildLimiters(cfg *config.Config, logger *slog.Logger, redisClient *redis.Client) (appLimiters, func()) {
	newLimiter := func(prefix string, rate int, window time.Duration) ratelimit.Limiter {
		inner := ratelimit.NewRedisLimiter(redisClient, rate, window, ratelimit.WithPrefix("rl:"+prefix+":"))
		return ratelimit.NewFailOpen(inner, logger)
	}

	limiters := appLimiters{
		auth:      newLimiter("auth", cfg.AuthRateLimit, cfg.AuthRateWindow),
		verifyOTP: newLimiter("verify-otp", cfg.VerifyOTPRateLimit, cfg.VerifyOTPRateWindow),
		general:   newLimiter("general-api", cfg.GeneralAPIRateLimit, cfg.GeneralAPIRateWindow),
		sync:      newLimiter("sync", cfg.SyncRateLimit, cfg.SyncRateWindow),
		webhook:   newLimiter("webhook", cfg.WebhookRateLimit, cfg.WebhookRateWindow),
		admin:     newLimiter("admin", cfg.AdminRateLimit, cfg.AdminRateWindow),
	}

	cleanup := func() {
		limiters.auth.Stop()
		limiters.verifyOTP.Stop()
		limiters.general.Stop()
		limiters.sync.Stop()
		limiters.webhook.Stop()
		limiters.admin.Stop()
	}

	return limiters, cleanup
}

func mountRoutes(r chi.Router, cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool, deps appDependencies, limiters appLimiters) {
	r.Get("/live", handler.Live(logger))
	r.Get("/ready", handler.Ready(pool, logger))

	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			r.With(authmw.RateLimit(limiters.auth, cfg.AuthRateLimit, authmw.IPKey)).
				Post("/start", deps.authHandler.Start)
			r.With(authmw.RateLimit(limiters.auth, cfg.AuthRateLimit, authmw.IPKey)).
				Post("/refresh", deps.authHandler.Refresh)
			r.With(
				authmw.RateLimit(limiters.auth, cfg.AuthRateLimit, authmw.IPKey),
				authmw.RateLimit(limiters.verifyOTP, cfg.VerifyOTPRateLimit, authmw.EmailKey),
			).
				Post("/verify", deps.authHandler.VerifyOTP)
		})

		r.With(authmw.RateLimit(limiters.webhook, cfg.WebhookRateLimit, authmw.IPKey)).
			Post("/webhooks/revenuecat", deps.webhookHandler.HandleRevenueCat)

		r.Route("/admin", func(r chi.Router) {
			r.With(authmw.RateLimit(limiters.admin, cfg.AdminRateLimit, authmw.IPKey)).
				Post("/login", deps.adminHandler.Login)

			r.Group(func(r chi.Router) {
				r.Use(authmw.AdminJWTAuth(cfg.JWTSecret, logger))
				r.Use(authmw.RateLimit(limiters.admin, cfg.AdminRateLimit, authmw.UserIDKey))

				r.Get("/users", deps.adminHandler.ListUsers)
				r.Get("/users/{id}", deps.adminHandler.GetUserDetail)
				r.Get("/stats", deps.adminHandler.GetPlatformStats)
				r.Post("/users/{id}/entitlements", deps.adminHandler.ManageEntitlement)
				r.Get("/entitlements", deps.adminHandler.ListEntitlements)
			})
		})

		r.Group(func(r chi.Router) {
			r.Use(authmw.JWTAuth(cfg.JWTSecret, logger))

			r.Group(func(r chi.Router) {
				r.Use(authmw.RateLimit(limiters.general, cfg.GeneralAPIRateLimit, authmw.UserIDKey))

				r.Get("/me", deps.authHandler.GetMe)
				r.Patch("/me", deps.authHandler.UpdateMe)
				r.Get("/me/notification-preferences", deps.authHandler.GetNotificationPreferences)
				r.Patch("/me/notification-preferences", deps.authHandler.UpdateNotificationPreferences)
				r.Get("/me/privacy", deps.authHandler.GetPrivacyPreferences)
				r.Patch("/me/privacy", deps.authHandler.UpdatePrivacyPreferences)
				r.Get("/me/username-available", deps.authHandler.UsernameAvailable)
				r.Post("/me/delete/request", deps.authHandler.DeleteRequest)
				r.Post("/me/delete/confirm", deps.authHandler.DeleteConfirm)
				r.Post("/me/photo", deps.authHandler.UploadPhoto)
				r.Post("/devices", deps.socialHandler.RegisterDevice)
				r.Post("/devices/unregister", deps.socialHandler.UnregisterDevice)
				r.Get("/friends", deps.socialHandler.ListFriends)
				r.Post("/friends/request", deps.socialHandler.RequestFriend)
				r.Get("/friends/requests/incoming", deps.socialHandler.ListIncomingRequests)
				r.Get("/friends/requests/outgoing", deps.socialHandler.ListOutgoingRequests)
				r.Post("/friends/requests/{id}/accept", deps.socialHandler.AcceptFriend)
				r.Post("/friends/requests/{id}/decline", deps.socialHandler.DeclineFriend)
				r.Delete("/friends/{id}", deps.socialHandler.RemoveFriend)
				r.Get("/friends/{id}/stats", deps.socialHandler.FriendStats)
				r.Get("/friends/search", deps.socialHandler.SearchUsers)
				r.Get("/leaderboard/streaks", deps.socialHandler.LeaderboardStreaks)
				r.Get("/leaderboard/focus-time", deps.socialHandler.LeaderboardFocusTime)
			})

			r.With(authmw.RateLimit(limiters.sync, cfg.SyncRateLimit, authmw.UserIDKey)).Post("/sync", deps.syncHandler.Sync)
		})
	})
}

func newHTTPServer(cfg *config.Config, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}
