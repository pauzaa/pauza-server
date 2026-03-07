package config

import (
	"fmt"
	"strings"
	"time"

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
	JWTSecret          string        `envconfig:"JWT_SECRET" required:"true"`
	JWTAccessTokenTTL  time.Duration `envconfig:"JWT_ACCESS_TOKEN_TTL" required:"true"`
	JWTRefreshTokenTTL time.Duration `envconfig:"JWT_REFRESH_TOKEN_TTL" required:"true"`

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

// validLogLevels enumerates accepted LOG_LEVEL values.
var validLogLevels = map[string]bool{
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
}

// validStudentVerificationProviders enumerates known provider names.
var validStudentVerificationProviders = map[string]bool{
	"sheerid": true,
	"unidays": true,
}

// minAdminSeedPasswordLen is the minimum accepted length for the admin seed password.
const minAdminSeedPasswordLen = 8

// validate performs semantic checks on configuration values that envconfig
// cannot enforce via struct tags alone.
func (c *Config) validate() error {
	// Port range
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("PORT must be between 1 and 65535, got %d", c.Port)
	}

	// Log level
	if !validLogLevels[strings.ToLower(c.LogLevel)] {
		return fmt.Errorf("LOG_LEVEL must be one of debug, info, warn, error; got %q", c.LogLevel)
	}

	// JWT TTLs must be positive
	if c.JWTAccessTokenTTL <= 0 {
		return fmt.Errorf("JWT_ACCESS_TOKEN_TTL must be positive, got %s", c.JWTAccessTokenTTL)
	}
	if c.JWTRefreshTokenTTL <= 0 {
		return fmt.Errorf("JWT_REFRESH_TOKEN_TTL must be positive, got %s", c.JWTRefreshTokenTTL)
	}

	// Admin seed password length and bcrypt limit
	if len(c.AdminSeedPassword) < minAdminSeedPasswordLen {
		return fmt.Errorf("ADMIN_SEED_PASSWORD must be at least %d characters", minAdminSeedPasswordLen)
	}
	if len(c.AdminSeedPassword) > 72 {
		return fmt.Errorf("ADMIN_SEED_PASSWORD must not exceed 72 bytes (bcrypt limit)")
	}

	// Student verification provider
	if !validStudentVerificationProviders[strings.ToLower(c.StudentVerificationProvider)] {
		return fmt.Errorf("STUDENT_VERIFICATION_PROVIDER must be one of sheerid, unidays; got %q", c.StudentVerificationProvider)
	}

	return nil
}

// Load reads configuration from environment variables and returns
// a validated Config. Returns an error if any required variable is missing
// or if semantic validation fails.
func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}
	return &cfg, nil
}
