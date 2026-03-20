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
	RevenueCatV2SecretKey   string `envconfig:"REVENUECAT_V2_SECRET_KEY"`
	RevenueCatProjectID     string `envconfig:"REVENUECAT_PROJECT_ID"`

	// Firebase (optional; enables push notifications when configured)
	FirebaseServiceAccountJSON string `envconfig:"FIREBASE_SERVICE_ACCOUNT_JSON"`

	// Redis-backed shared rate limiting for the server runtime. Required in all
	// environments so request budgets are enforced consistently.
	RedisURL string `envconfig:"REDIS_URL" required:"true"`

	// Profile photo storage. The backend writes uploads into a
	// deployment-provided local filesystem path and serves them directly at the
	// configured public base URL via the /photos/* route.
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
	AdminCORSOrigins       string        `envconfig:"ADMIN_CORS_ORIGINS" default:"http://localhost:5173"`

	// AI analysis (optional; endpoints are disabled when AIProvider is empty)
	AIProvider   string        `envconfig:"AI_PROVIDER"`
	OpenAIAPIKey string        `envconfig:"OPENAI_API_KEY"`
	GeminiAPIKey string        `envconfig:"GEMINI_API_KEY"`
	AIModel      string        `envconfig:"AI_MODEL"`
	AIRateLimit  int           `envconfig:"AI_RATE_LIMIT" default:"10"`
	AIRateWindow time.Duration `envconfig:"AI_RATE_WINDOW" default:"1h"`
	AITimeout    time.Duration `envconfig:"AI_TIMEOUT" default:"60s"`

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
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("PORT must be between 1 and 65535, got %d", c.Port)
	}
	if len(c.JWTSecret) < minJWTSecretLen {
		return fmt.Errorf("JWT_SECRET must be at least %d characters", minJWTSecretLen)
	}
	if err := validateEnum("LOG_LEVEL", c.LogLevel, validLogLevels, []string{"debug", "info", "warn", "error"}); err != nil {
		return err
	}
	if err := validatePositiveDurationFields([]durationField{
		{name: "JWT_ACCESS_TOKEN_TTL", value: c.JWTAccessTokenTTL},
		{name: "JWT_REFRESH_TOKEN_TTL", value: c.JWTRefreshTokenTTL},
		{name: "SMTP_TIMEOUT", value: c.SMTPTimeout},
		{name: "AUTH_RATE_WINDOW", value: c.AuthRateWindow},
		{name: "VERIFY_OTP_RATE_WINDOW", value: c.VerifyOTPRateWindow},
		{name: "GENERAL_API_RATE_WINDOW", value: c.GeneralAPIRateWindow},
		{name: "SYNC_RATE_WINDOW", value: c.SyncRateWindow},
		{name: "WEBHOOK_RATE_WINDOW", value: c.WebhookRateWindow},
		{name: "ADMIN_JWT_ACCESS_TOKEN_TTL", value: c.AdminJWTAccessTokenTTL},
		{name: "ADMIN_RATE_WINDOW", value: c.AdminRateWindow},
		{name: "CLEANUP_INTERVAL", value: c.CleanupInterval},
		{name: "OTP_RETENTION_PERIOD", value: c.OTPRetentionPeriod},
		{name: "REFRESH_TOKEN_REVOKED_RETENTION", value: c.RefreshTokenRevokedRetention},
	}); err != nil {
		return err
	}
	if err := validatePositiveIntFields([]intField{
		{name: "AUTH_RATE_LIMIT", value: c.AuthRateLimit},
		{name: "VERIFY_OTP_RATE_LIMIT", value: c.VerifyOTPRateLimit},
		{name: "GENERAL_API_RATE_LIMIT", value: c.GeneralAPIRateLimit},
		{name: "SYNC_RATE_LIMIT", value: c.SyncRateLimit},
		{name: "WEBHOOK_RATE_LIMIT", value: c.WebhookRateLimit},
		{name: "ADMIN_RATE_LIMIT", value: c.AdminRateLimit},
	}); err != nil {
		return err
	}

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

	c.SMTPTLSPolicy = strings.ToLower(c.SMTPTLSPolicy)
	if err := validateEnum("SMTP_TLS_POLICY", c.SMTPTLSPolicy, validSMTPTLSPolicies, []string{"mandatory", "opportunistic", "none"}); err != nil {
		return err
	}
	if err := validateTrustedProxies(c.TrustedProxies); err != nil {
		return err
	}
	if err := validateNonEmptyTrimmed("PHOTO_STORAGE_DIR", c.PhotoStorageDir); err != nil {
		return err
	}
	if err := validateNonEmptyTrimmed("PHOTO_PUBLIC_BASE_URL", c.PhotoPublicBaseURL); err != nil {
		return err
	}

	if err := c.validateAI(); err != nil {
		return err
	}

	return nil
}

type durationField struct {
	name  string
	value time.Duration
}

type intField struct {
	name  string
	value int
}

func validatePositiveDurationFields(fields []durationField) error {
	for _, field := range fields {
		if err := validatePositiveDuration(field.name, field.value); err != nil {
			return err
		}
	}
	return nil
}

func validatePositiveDuration(name string, value time.Duration) error {
	if value <= 0 {
		return fmt.Errorf("%s must be positive, got %s", name, value)
	}
	return nil
}

func validatePositiveIntFields(fields []intField) error {
	for _, field := range fields {
		if err := validatePositiveInt(field.name, field.value); err != nil {
			return err
		}
	}
	return nil
}

func validatePositiveInt(name string, value int) error {
	if value <= 0 {
		return fmt.Errorf("%s must be positive, got %d", name, value)
	}
	return nil
}

func validateNonEmptyTrimmed(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	return nil
}

func validateEnum(name, value string, allowed map[string]bool, ordered []string) error {
	if !allowed[strings.ToLower(value)] {
		return fmt.Errorf("%s must be one of %s; got %q", name, enumValues(ordered), value)
	}
	return nil
}

func enumValues(ordered []string) string {
	return strings.Join(ordered, ", ")
}

var validAIProviders = map[string]bool{
	"openai": true,
	"gemini": true,
}

func (c *Config) validateAI() error {
	if c.AIProvider == "" {
		return nil
	}
	c.AIProvider = strings.ToLower(c.AIProvider)
	if err := validateEnum("AI_PROVIDER", c.AIProvider, validAIProviders, []string{"openai", "gemini"}); err != nil {
		return err
	}
	switch c.AIProvider {
	case "openai":
		if strings.TrimSpace(c.OpenAIAPIKey) == "" {
			return fmt.Errorf("OPENAI_API_KEY is required when AI_PROVIDER is openai")
		}
	case "gemini":
		if strings.TrimSpace(c.GeminiAPIKey) == "" {
			return fmt.Errorf("GEMINI_API_KEY is required when AI_PROVIDER is gemini")
		}
	}
	if c.AIRateLimit <= 0 {
		return fmt.Errorf("AI_RATE_LIMIT must be positive, got %d", c.AIRateLimit)
	}
	if c.AIRateWindow <= 0 {
		return fmt.Errorf("AI_RATE_WINDOW must be positive, got %s", c.AIRateWindow)
	}
	if c.AITimeout <= 0 {
		return fmt.Errorf("AI_TIMEOUT must be positive, got %s", c.AITimeout)
	}
	return nil
}

func validateTrustedProxies(value string) error {
	if value == "" {
		return nil
	}
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				return fmt.Errorf("TRUSTED_PROXIES contains invalid CIDR %q: %w", entry, err)
			}
			continue
		}
		if net.ParseIP(entry) == nil {
			return fmt.Errorf("TRUSTED_PROXIES contains invalid IP %q", entry)
		}
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

// AIEnabled reports whether AI analysis endpoints should be mounted.
func (c *Config) AIEnabled() bool {
	return c.AIProvider != ""
}

// AIAPIKey returns the API key for the configured AI provider.
func (c *Config) AIAPIKey() string {
	switch c.AIProvider {
	case "openai":
		return c.OpenAIAPIKey
	case "gemini":
		return c.GeminiAPIKey
	default:
		return ""
	}
}

// ParseAdminCORSOrigins parses the comma-separated AdminCORSOrigins string
// into a slice of origin strings suitable for the CORS middleware.
func (c *Config) ParseAdminCORSOrigins() []string {
	raw := strings.TrimSpace(c.AdminCORSOrigins)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			origins = append(origins, trimmed)
		}
	}
	return origins
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
