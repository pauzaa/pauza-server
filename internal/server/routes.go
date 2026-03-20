package server

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IsorilovA/pauza-server/internal/config"
	"github.com/IsorilovA/pauza-server/internal/handler"
	authmw "github.com/IsorilovA/pauza-server/internal/middleware"
)

func mountRoutes(r chi.Router, cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool, deps appDependencies, limiters appLimiters) {
	r.Get("/live", handler.Live(logger))
	r.Get("/ready", handler.Ready(pool, logger))

	photosDir := http.Dir(cfg.PhotoStorageDir)
	r.Get("/photos/*", photosHandler(photosDir))

	r.Get("/docs", docsPageHandler())
	r.Get("/docs/openapi.yaml", docsSpecHandler())

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(limitBody) // defense-in-depth: cap request bodies to 1 MiB by default

		r.Route("/auth", func(r chi.Router) {
			r.Group(func(r chi.Router) {
				r.Use(authmw.RateLimit(limiters.auth, cfg.AuthRateLimit, authmw.IPKey))
				r.Post("/start", deps.authHandler.Start)
				r.Post("/refresh", deps.authHandler.Refresh)
			})

			r.With(
				authmw.RateLimit(limiters.auth, cfg.AuthRateLimit, authmw.IPKey),
				authmw.ExtractAuthEmailKey,
				authmw.RateLimit(limiters.verifyOTP, cfg.VerifyOTPRateLimit, authmw.AuthEmailKey),
			).Post("/verify", deps.authHandler.VerifyOTP)
		})

		r.With(authmw.RateLimit(limiters.webhook, cfg.WebhookRateLimit, authmw.IPKey)).
			Post("/webhooks/revenuecat", deps.webhookHandler.HandleRevenueCat)

		r.Route("/admin", func(r chi.Router) {
			// CORS — must be first so preflight OPTIONS are handled before auth/rate-limit
			r.Use(cors.Handler(cors.Options{
				AllowedOrigins:   cfg.ParseAdminCORSOrigins(),
				AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
				AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Request-Id"},
				MaxAge: 300,
			}))

			r.With(authmw.RateLimit(limiters.admin, cfg.AdminRateLimit, authmw.IPKey)).Post("/login", deps.adminHandler.Login)

			r.Group(func(r chi.Router) {
				r.Use(authmw.AdminJWTAuth(cfg.JWTSecret, logger))
				r.Use(authmw.RateLimit(limiters.admin, cfg.AdminRateLimit, authmw.UserIDKey))

				r.Get("/users", deps.adminHandler.ListUsers)
				r.Get("/users/{id}", deps.adminHandler.GetUserDetail)
				r.Get("/stats", deps.adminHandler.GetPlatformStats)
				r.Get("/stats/user-growth", deps.adminHandler.GetUserGrowth)
				r.Get("/stats/active-users", deps.adminHandler.GetActiveUsers)
				r.Post("/users/{id}/entitlements", deps.adminHandler.ManageEntitlement)
				r.Get("/entitlements", deps.adminHandler.ListEntitlements)
			})
		})

		r.Group(func(r chi.Router) {
			r.Use(authmw.JWTAuth(cfg.JWTSecret, deps.sessionValidator, logger))

			r.Group(func(r chi.Router) {
				r.Use(authmw.RateLimit(limiters.general, cfg.GeneralAPIRateLimit, authmw.UserIDKey))

				r.Post("/auth/logout", deps.authHandler.Logout)

			r.Get("/me", deps.authHandler.GetMe)
				r.Patch("/me", deps.authHandler.UpdateMe)
				r.Get("/me/notification-preferences", deps.authHandler.GetNotificationPreferences)
				r.Patch("/me/notification-preferences", deps.authHandler.UpdateNotificationPreferences)
				r.Get("/me/privacy", deps.authHandler.GetPrivacyPreferences)
				r.Patch("/me/privacy", deps.authHandler.UpdatePrivacyPreferences)
				r.Get("/me/username-available", deps.authHandler.UsernameAvailable)
				r.Post("/me/delete/request", deps.authHandler.DeleteRequest)
				r.Post("/me/delete/confirm", deps.authHandler.DeleteConfirm)
				r.With(limitBodySize(5 << 20)).Post("/me/photo", deps.authHandler.UploadPhoto)
				r.Post("/devices", deps.socialHandler.RegisterDevice)
				r.Post("/devices/unregister", deps.socialHandler.UnregisterDevice)
				r.Get("/friends", deps.socialHandler.ListFriends)
				r.Post("/friends/request", deps.socialHandler.RequestFriend)
				r.Get("/friends/requests/incoming", deps.socialHandler.ListIncomingRequests)
				r.Get("/friends/requests/outgoing", deps.socialHandler.ListOutgoingRequests)
				r.Post("/friends/requests/{id}/accept", deps.socialHandler.AcceptFriend)
				r.Post("/friends/requests/{id}/decline", deps.socialHandler.DeclineFriend)
				r.Post("/friends/requests/{id}/cancel", deps.socialHandler.CancelFriendRequest)
				r.Delete("/friends/{id}", deps.socialHandler.RemoveFriend)
				r.Get("/friends/{id}/stats", deps.socialHandler.FriendStats)
				r.Get("/friends/search", deps.socialHandler.SearchUsers)
				r.Get("/leaderboard/streaks", deps.socialHandler.LeaderboardStreaks)
				r.Get("/leaderboard/focus-time", deps.socialHandler.LeaderboardFocusTime)
			})

			r.With(authmw.RateLimit(limiters.sync, cfg.SyncRateLimit, authmw.UserIDKey)).Post("/sync", deps.syncHandler.Sync)

			if deps.aiHandler != nil {
				r.Route("/ai", func(r chi.Router) {
					r.Use(limitBodySize(5 << 20))
					r.Use(authmw.RateLimit(limiters.ai, cfg.AIRateLimit, authmw.UserIDKey))
					r.Post("/usage-analysis", deps.aiHandler.AnalyzeUsage)
					r.Post("/focus-schedule", deps.aiHandler.SuggestSchedule)
					r.Post("/daily-report", deps.aiHandler.GenerateDailyReport)
					r.Post("/addiction-check", deps.aiHandler.DetectAddiction)
				})
			}
		})
	})
}
