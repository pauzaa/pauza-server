package config

import (
	"fmt"

	"github.com/kelseyhightower/envconfig"
)

// Config holds all configuration for the application.
// All environment variables from the spec are declared here.
// Variables not yet consumed by the current phase are still required
// to ensure the deployment environment is fully configured.
type Config struct {
	// Server
	Port     int    `envconfig:"PORT" default:"8080"`
	LogLevel string `envconfig:"LOG_LEVEL" default:"info"`

	// Database
	DatabaseURL string `envconfig:"DATABASE_URL" required:"true"`

	// JWT
	JWTSecret          string `envconfig:"JWT_SECRET" required:"true"`
	JWTAccessTokenTTL  string `envconfig:"JWT_ACCESS_TOKEN_TTL" required:"true"`
	JWTRefreshTokenTTL string `envconfig:"JWT_REFRESH_TOKEN_TTL" required:"true"`

	// SMTP
	SMTPHost     string `envconfig:"SMTP_HOST" required:"true"`
	SMTPPort     int    `envconfig:"SMTP_PORT" required:"true"`
	SMTPUsername string `envconfig:"SMTP_USERNAME" required:"true"`
	SMTPPassword string `envconfig:"SMTP_PASSWORD" required:"true"`
	SMTPFrom     string `envconfig:"SMTP_FROM" required:"true"`

	// Admin seed
	AdminSeedUsername string `envconfig:"ADMIN_SEED_USERNAME" required:"true"`
	AdminSeedPassword string `envconfig:"ADMIN_SEED_PASSWORD" required:"true"`

	// RevenueCat
	RevenueCatAPIKey        string `envconfig:"REVENUECAT_API_KEY" required:"true"`
	RevenueCatWebhookSecret string `envconfig:"REVENUECAT_WEBHOOK_SECRET" required:"true"`

	// Firebase
	FirebaseServiceAccountJSON string `envconfig:"FIREBASE_SERVICE_ACCOUNT_JSON" required:"true"`

	// Student verification
	StudentVerificationProvider string `envconfig:"STUDENT_VERIFICATION_PROVIDER" required:"true"`
	StudentVerificationAPIKey   string `envconfig:"STUDENT_VERIFICATION_API_KEY" required:"true"`
}

// Load reads configuration from environment variables and returns
// a validated Config. Returns an error if any required variable is missing.
func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	return &cfg, nil
}
