package service

import (
	"log/slog"
	"time"

	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/repository"
)

type AuthService struct {
	pool               repository.Pool
	repo               repository.AuthRepository
	mailer             mail.Sender
	jwtSecret          string
	jwtAccessTokenTTL  time.Duration
	jwtRefreshTokenTTL time.Duration
	logger             *slog.Logger
}

const cleanupTimeout = 5 * time.Second

func NewAuthService(
	pool repository.Pool,
	repo repository.AuthRepository,
	mailer mail.Sender,
	jwtSecret string,
	jwtAccessTokenTTL time.Duration,
	jwtRefreshTokenTTL time.Duration,
	logger *slog.Logger,
) *AuthService {
	return &AuthService{
		pool:               pool,
		repo:               repo,
		mailer:             mailer,
		jwtSecret:          jwtSecret,
		jwtAccessTokenTTL:  jwtAccessTokenTTL,
		jwtRefreshTokenTTL: jwtRefreshTokenTTL,
		logger:             logger,
	}
}
