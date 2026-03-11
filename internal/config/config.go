package config

import (
	"fmt"
	"net"
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
	Port           int    `envconfig:"PORT" default:"8080"`
	LogLevel       string `envconfig:"LOG_LEVEL" default:"info"`
	TrustedProxies string `envconfig:"TRUSTED_PROXIES" default:""`

	// Database
	DatabaseURL string `envconfig:"DATABASE_URL" required:"true"`

	// JWT
	JWTSecret          string        `envconfig:"JWT_SECRET" required:"true"`
	JWTAccessTokenTTL  time.Duration `envconfig:"JWT_ACCESS_TOKEN_TTL" required:"true"`
	JWTRefreshTokenTTL time.Duration `envconfig:"JWT_REFRESH_TOKEN_TTL" required:"true"`

	// SMTP
	SMTPHost      string        `envconfig:"SMTP_HOST" required:"true"`
	SMTPPort      int           `envconfig:"SMTP_PORT" required:"true"`
	SMTPUsername  string        `envconfig:"SMTP_USERNAME" required:"true"`
	SMTPPassword  string        `envconfig:"SMTP_PASSWORD" required:"true"`
	SMTPFrom      string        `envconfig:"SMTP_FROM" required:"true"`
	SMTPTimeout   time.Duration `envconfig:"SMTP_TIMEOUT" default:"30s"`
	SMTPTLSPolicy string        `envconfig:"SMTP_TLS_POLICY" default:"mandatory"`

	// Admin seed (only used by cmd/seed-admin; optional for the server)
	AdminSeedUsername string `envconfig:"ADMIN_SEED_USERNAME"`
	AdminSeedPassword string `envconfig:"ADMIN_SEED_PASSWORD"`

	// RevenueCat
	RevenueCatAPIKey        string `envconfig:"REVENUECAT_API_KEY" required:"true"`
	RevenueCatWebhookSecret string `envconfig:"REVENUECAT_WEBHOOK_SECRET" required:"true"`

	// Firebase (optional; enables push notifications when configured)
	FirebaseServiceAccountJSON string `envconfig:"FIREBASE_SERVICE_ACCOUNT_JSON"`

	// Redis-backed shared rate limiting for the server runtime. Required in all
	// environments so request budgets are enforced consistently.
	RedisURL string `envconfig:"REDIS_URL" required:"true"`

	// Profile photo storage. The backend writes uploads into a
	// deployment-provided local filesystem path and returns URLs rooted at the
	// configured public base URL. A reverse proxy such as Nginx is expected to
	// serve that directory at the matching public path.
	PhotoStorageDir    string `envconfig:"PHOTO_STORAGE_DIR" required:"true"`
	PhotoPublicBaseURL string `envconfig:"PHOTO_PUBLIC_BASE_URL" required:"true"`

	// Rate limiting groups (safe defaults per BACKEND_SPEC §10).
	AuthRateLimit  int           `envconfig:"AUTH_RATE_LIMIT" default:"5"`
	AuthRateWindow time.Duration `envconfig:"AUTH_RATE_WINDOW" default:"1m"`

	VerifyOTPRateLimit  int           `envconfig:"VERIFY_OTP_RATE_LIMIT" default:"3"`
	VerifyOTPRateWindow time.Duration `envconfig:"VERIFY_OTP_RATE_WINDOW" default:"1m"`

	GeneralAPIRateLimit  int           `envconfig:"GENERAL_API_RATE_LIMIT" default:"60"`
	GeneralAPIRateWindow time.Duration `envconfig:"GENERAL_API_RATE_WINDOW" default:"1m"`

	SyncRateLimit  int           `envconfig:"SYNC_RATE_LIMIT" default:"30"`
	SyncRateWindow time.Duration `envconfig:"SYNC_RATE_WINDOW" default:"1m"`

	WebhookRateLimit  int           `envconfig:"WEBHOOK_RATE_LIMIT" default:"100"`
	WebhookRateWindow time.Duration `envconfig:"WEBHOOK_RATE_WINDOW" default:"1m"`

	AdminJWTAccessTokenTTL time.Duration `envconfig:"ADMIN_JWT_ACCESS_TOKEN_TTL" default:"1h"`
	AdminRateLimit         int           `envconfig:"ADMIN_RATE_LIMIT" default:"30"`
	AdminRateWindow        time.Duration `envconfig:"ADMIN_RATE_WINDOW" default:"1m"`

	// Cleanup job
	CleanupInterval    time.Duration `envconfig:"CLEANUP_INTERVAL" default:"1h"`
	OTPRetentionPeriod time.Duration `envconfig:"OTP_RETENTION_PERIOD" default:"24h"`
	// RefreshTokenRevokedRetention uses a legacy name but controls how long
	// expired non-revoked and revoked refresh tokens are kept before the
	// cleanup job deletes them. It is mapped to CleanupConfig.RefreshTokenMaxAge.
	RefreshTokenRevokedRetention time.Duration `envconfig:"REFRESH_TOKEN_REVOKED_RETENTION" default:"168h"`
}

// validLogLevels enumerates accepted LOG_LEVEL values.
var validLogLevels = map[string]bool{
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
}

// validSMTPTLSPolicies enumerates accepted SMTP_TLS_POLICY values.
var validSMTPTLSPolicies = map[string]bool{
	"mandatory":     true,
	"opportunistic": true,
	"none":          true,
}

// minJWTSecretLen is the minimum accepted length for the JWT signing secret.
const minJWTSecretLen = 32

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

	// JWT secret minimum length
	if len(c.JWTSecret) < minJWTSecretLen {
		return fmt.Errorf("JWT_SECRET must be at least %d characters", minJWTSecretLen)
	}

	// JWT TTLs must be positive
	if c.JWTAccessTokenTTL <= 0 {
		return fmt.Errorf("JWT_ACCESS_TOKEN_TTL must be positive, got %s", c.JWTAccessTokenTTL)
	}
	if c.JWTRefreshTokenTTL <= 0 {
		return fmt.Errorf("JWT_REFRESH_TOKEN_TTL must be positive, got %s", c.JWTRefreshTokenTTL)
	}

	// Admin seed: both must be set together; validate only when provided
	hasUser := c.AdminSeedUsername != ""
	hasPass := c.AdminSeedPassword != ""
	if hasUser != hasPass {
		return fmt.Errorf("ADMIN_SEED_USERNAME and ADMIN_SEED_PASSWORD must both be set or both be empty")
	}
	if hasPass {
		if len(c.AdminSeedPassword) < minAdminSeedPasswordLen {
			return fmt.Errorf("ADMIN_SEED_PASSWORD must be at least %d characters", minAdminSeedPasswordLen)
		}
		if len(c.AdminSeedPassword) > 72 {
			return fmt.Errorf("ADMIN_SEED_PASSWORD must not exceed 72 bytes (bcrypt limit)")
		}
	}

	// SMTP timeout must be positive
	if c.SMTPTimeout <= 0 {
		return fmt.Errorf("SMTP_TIMEOUT must be positive, got %s", c.SMTPTimeout)
	}

	// SMTP TLS policy (normalize to lowercase)
	c.SMTPTLSPolicy = strings.ToLower(c.SMTPTLSPolicy)
	if !validSMTPTLSPolicies[c.SMTPTLSPolicy] {
		return fmt.Errorf("SMTP_TLS_POLICY must be one of mandatory, opportunistic, none; got %q", c.SMTPTLSPolicy)
	}

	// Trusted proxies — validate that every entry is a valid IP or CIDR
	if c.TrustedProxies != "" {
		for _, entry := range strings.Split(c.TrustedProxies, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			if strings.Contains(entry, "/") {
				if _, _, err := net.ParseCIDR(entry); err != nil {
					return fmt.Errorf("TRUSTED_PROXIES contains invalid CIDR %q: %w", entry, err)
				}
			} else {
				if net.ParseIP(entry) == nil {
					return fmt.Errorf("TRUSTED_PROXIES contains invalid IP %q", entry)
				}
			}
		}
	}

	// Rate limit values must be positive
	if c.AuthRateLimit <= 0 {
		return fmt.Errorf("AUTH_RATE_LIMIT must be positive, got %d", c.AuthRateLimit)
	}
	if c.AuthRateWindow <= 0 {
		return fmt.Errorf("AUTH_RATE_WINDOW must be positive, got %s", c.AuthRateWindow)
	}
	if c.VerifyOTPRateLimit <= 0 {
		return fmt.Errorf("VERIFY_OTP_RATE_LIMIT must be positive, got %d", c.VerifyOTPRateLimit)
	}
	if c.VerifyOTPRateWindow <= 0 {
		return fmt.Errorf("VERIFY_OTP_RATE_WINDOW must be positive, got %s", c.VerifyOTPRateWindow)
	}
	if c.GeneralAPIRateLimit <= 0 {
		return fmt.Errorf("GENERAL_API_RATE_LIMIT must be positive, got %d", c.GeneralAPIRateLimit)
	}
	if c.GeneralAPIRateWindow <= 0 {
		return fmt.Errorf("GENERAL_API_RATE_WINDOW must be positive, got %s", c.GeneralAPIRateWindow)
	}
	if c.SyncRateLimit <= 0 {
		return fmt.Errorf("SYNC_RATE_LIMIT must be positive, got %d", c.SyncRateLimit)
	}
	if c.SyncRateWindow <= 0 {
		return fmt.Errorf("SYNC_RATE_WINDOW must be positive, got %s", c.SyncRateWindow)
	}
	if c.WebhookRateLimit <= 0 {
		return fmt.Errorf("WEBHOOK_RATE_LIMIT must be positive, got %d", c.WebhookRateLimit)
	}
	if c.WebhookRateWindow <= 0 {
		return fmt.Errorf("WEBHOOK_RATE_WINDOW must be positive, got %s", c.WebhookRateWindow)
	}
	if c.AdminJWTAccessTokenTTL <= 0 {
		return fmt.Errorf("ADMIN_JWT_ACCESS_TOKEN_TTL must be positive, got %s", c.AdminJWTAccessTokenTTL)
	}
	if c.AdminRateLimit <= 0 {
		return fmt.Errorf("ADMIN_RATE_LIMIT must be positive, got %d", c.AdminRateLimit)
	}
	if c.AdminRateWindow <= 0 {
		return fmt.Errorf("ADMIN_RATE_WINDOW must be positive, got %s", c.AdminRateWindow)
	}

	// Cleanup durations must be positive
	if c.CleanupInterval <= 0 {
		return fmt.Errorf("CLEANUP_INTERVAL must be positive, got %s", c.CleanupInterval)
	}
	if c.OTPRetentionPeriod <= 0 {
		return fmt.Errorf("OTP_RETENTION_PERIOD must be positive, got %s", c.OTPRetentionPeriod)
	}
	if c.RefreshTokenRevokedRetention <= 0 {
		return fmt.Errorf("REFRESH_TOKEN_REVOKED_RETENTION must be positive, got %s", c.RefreshTokenRevokedRetention)
	}
	if strings.TrimSpace(c.PhotoStorageDir) == "" {
		return fmt.Errorf("PHOTO_STORAGE_DIR must not be empty")
	}
	if strings.TrimSpace(c.PhotoPublicBaseURL) == "" {
		return fmt.Errorf("PHOTO_PUBLIC_BASE_URL must not be empty")
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

// MigrateConfig holds only the configuration needed by cmd/migrate.
type MigrateConfig struct {
	DatabaseURL string `envconfig:"DATABASE_URL" required:"true"`
}

// LoadMigrate reads the minimal configuration needed to run migrations.
func LoadMigrate() (*MigrateConfig, error) {
	var cfg MigrateConfig
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("loading migrate config: %w", err)
	}
	return &cfg, nil
}

// SeedAdminConfig holds only the configuration needed by cmd/seed-admin.
type SeedAdminConfig struct {
	DatabaseURL       string `envconfig:"DATABASE_URL" required:"true"`
	AdminSeedUsername string `envconfig:"ADMIN_SEED_USERNAME" required:"true"`
	AdminSeedPassword string `envconfig:"ADMIN_SEED_PASSWORD" required:"true"`
}

// validate performs semantic checks on SeedAdminConfig values.
func (c *SeedAdminConfig) validate() error {
	if len(c.AdminSeedPassword) < minAdminSeedPasswordLen {
		return fmt.Errorf("ADMIN_SEED_PASSWORD must be at least %d characters", minAdminSeedPasswordLen)
	}
	if len(c.AdminSeedPassword) > 72 {
		return fmt.Errorf("ADMIN_SEED_PASSWORD must not exceed 72 bytes (bcrypt limit)")
	}
	return nil
}

// LoadSeedAdmin reads the minimal configuration needed to seed an admin user.
func LoadSeedAdmin() (*SeedAdminConfig, error) {
	var cfg SeedAdminConfig
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("loading seed-admin config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating seed-admin config: %w", err)
	}
	return &cfg, nil
}

// ParseTrustedProxies parses the comma-separated TrustedProxies string into
// a slice of *net.IPNet entries. Plain IP addresses are converted to /32
// (IPv4) or /128 (IPv6) single-host CIDRs. An empty TrustedProxies value
// returns a nil slice (trust nobody). This method should only be called on
// a validated Config.
func (c *Config) ParseTrustedProxies() []*net.IPNet {
	if c.TrustedProxies == "" {
		return nil
	}
	var nets []*net.IPNet
	for _, entry := range strings.Split(c.TrustedProxies, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			_, ipNet, _ := net.ParseCIDR(entry) // already validated
			nets = append(nets, ipNet)
		} else {
			ip := net.ParseIP(entry) // already validated
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			nets = append(nets, &net.IPNet{
				IP:   ip,
				Mask: net.CIDRMask(bits, bits),
			})
		}
	}
	return nets
}
