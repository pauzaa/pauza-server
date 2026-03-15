package service

import (
	"log/slog"
	"time"

	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/repository"
)

type authUserRepository interface {
	repository.UserRepository
}

type authOTPRepository interface {
	repository.OTPRepository
}

type authRefreshTokenRepository interface {
	repository.RefreshTokenRepository
}

type authEntitlementRepository interface {
	repository.EntitlementSnapshotRepository
}

type authSessionRepository interface {
	repository.SessionRepository
}

type AuthService struct {
	pool               repository.Pool
	users              authUserRepository
	otps               authOTPRepository
	refreshTokens      authRefreshTokenRepository
	sessions           authSessionRepository
	entitlements       authEntitlementRepository
	mailer             mail.Sender
	jwtSecret          string
	jwtAccessTokenTTL  time.Duration
	jwtRefreshTokenTTL time.Duration
	logger             *slog.Logger
}

const cleanupTimeout = 5 * time.Second

func NewAuthService(
	pool repository.Pool,
	users authUserRepository,
	otps authOTPRepository,
	refreshTokens authRefreshTokenRepository,
	sessions authSessionRepository,
	entitlements authEntitlementRepository,
	mailer mail.Sender,
	jwtSecret string,
	jwtAccessTokenTTL time.Duration,
	jwtRefreshTokenTTL time.Duration,
	logger *slog.Logger,
) *AuthService {
	return &AuthService{
		pool:               pool,
		users:              users,
		otps:               otps,
		refreshTokens:      refreshTokens,
		sessions:           sessions,
		entitlements:       entitlements,
		mailer:             mailer,
		jwtSecret:          jwtSecret,
		jwtAccessTokenTTL:  jwtAccessTokenTTL,
		jwtRefreshTokenTTL: jwtRefreshTokenTTL,
		logger:             logger,
	}
}
