package config

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// setRequiredEnvVars sets all required environment variables with test values.
// Returns a cleanup function that unsets them all.
func setRequiredEnvVars(t *testing.T) {
	t.Helper()

	vars := map[string]string{
		"DATABASE_URL":                  "postgres://test:test@localhost:5432/test?sslmode=disable",
		"JWT_SECRET":                    "test-secret-that-is-at-least-32-bytes!",
		"JWT_ACCESS_TOKEN_TTL":          "15m",
		"JWT_REFRESH_TOKEN_TTL":         "720h",
		"SMTP_HOST":                     "smtp.test.com",
		"SMTP_PORT":                     "587",
		"SMTP_USERNAME":                 "testuser",
		"SMTP_PASSWORD":                 "testpass",
		"SMTP_FROM":                     "test@test.com",
		"REVENUECAT_API_KEY":            "rc_test_key",
		"REVENUECAT_WEBHOOK_SECRET":     "rc_test_secret",
		"FIREBASE_SERVICE_ACCOUNT_JSON": "{}",
		"REDIS_URL":                     "redis://localhost:6379/0",
		"PHOTO_STORAGE_DIR":             "./var/photos",
		"PHOTO_PUBLIC_BASE_URL":         "https://api.test/photos",
	}

	for k, v := range vars {
		t.Setenv(k, v)
	}
}

func TestLoad_AllRequiredVarsSet(t *testing.T) {
	setRequiredEnvVars(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if cfg.DatabaseURL != "postgres://test:test@localhost:5432/test?sslmode=disable" {
		t.Errorf("unexpected DatabaseURL: %s", cfg.DatabaseURL)
	}
	if cfg.JWTSecret != "test-secret-that-is-at-least-32-bytes!" {
		t.Errorf("unexpected JWTSecret: %s", cfg.JWTSecret)
	}
	if cfg.JWTAccessTokenTTL != 15*time.Minute {
		t.Errorf("unexpected JWTAccessTokenTTL: %s", cfg.JWTAccessTokenTTL)
	}
	if cfg.JWTRefreshTokenTTL != 720*time.Hour {
		t.Errorf("unexpected JWTRefreshTokenTTL: %s", cfg.JWTRefreshTokenTTL)
	}
	if cfg.SMTPPort != 587 {
		t.Errorf("unexpected SMTPPort: %d", cfg.SMTPPort)
	}
	if cfg.AdminSeedUsername != "" {
		t.Errorf("expected empty AdminSeedUsername, got: %s", cfg.AdminSeedUsername)
	}
}

func TestLoad_Defaults(t *testing.T) {
	setRequiredEnvVars(t)

	// Do NOT set PORT or LOG_LEVEL — they should use defaults

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if cfg.Port != 8080 {
		t.Errorf("expected default Port 8080, got: %d", cfg.Port)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected default LogLevel 'info', got: %s", cfg.LogLevel)
	}
	if cfg.SyncRateLimit != 30 {
		t.Errorf("expected default SyncRateLimit 30, got: %d", cfg.SyncRateLimit)
	}
	if cfg.SyncRateWindow != time.Minute {
		t.Errorf("expected default SyncRateWindow 1m, got: %s", cfg.SyncRateWindow)
	}
	if cfg.AdminJWTAccessTokenTTL != time.Hour {
		t.Errorf("expected default AdminJWTAccessTokenTTL 1h, got: %s", cfg.AdminJWTAccessTokenTTL)
	}
	if cfg.AdminRateLimit != 30 {
		t.Errorf("expected default AdminRateLimit 30, got: %d", cfg.AdminRateLimit)
	}
	if cfg.AdminRateWindow != time.Minute {
		t.Errorf("expected default AdminRateWindow 1m, got: %s", cfg.AdminRateWindow)
	}
}

func TestLoad_CustomPortAndLogLevel(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("PORT", "9090")
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if cfg.Port != 9090 {
		t.Errorf("expected Port 9090, got: %d", cfg.Port)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected LogLevel 'debug', got: %s", cfg.LogLevel)
	}
}

func TestLoad_MissingRequiredVar(t *testing.T) {
	// Set all required vars except DATABASE_URL.
	// Avoid os.Clearenv() since it is process-wide and can cause flaky tests
	// if tests ever run in parallel.
	t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-bytes!")
	t.Setenv("JWT_ACCESS_TOKEN_TTL", "15m")
	t.Setenv("JWT_REFRESH_TOKEN_TTL", "720h")
	t.Setenv("SMTP_HOST", "smtp.test.com")
	t.Setenv("SMTP_PORT", "587")
	t.Setenv("SMTP_USERNAME", "testuser")
	t.Setenv("SMTP_PASSWORD", "testpass")
	t.Setenv("SMTP_FROM", "test@test.com")
	t.Setenv("REVENUECAT_API_KEY", "rc_test_key")
	t.Setenv("REVENUECAT_WEBHOOK_SECRET", "rc_test_secret")
	t.Setenv("FIREBASE_SERVICE_ACCOUNT_JSON", "{}")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("PHOTO_STORAGE_DIR", "./var/photos")
	t.Setenv("PHOTO_PUBLIC_BASE_URL", "https://api.test/photos")

	// Ensure DATABASE_URL is unset even if the developer has it in their shell.
	// t.Setenv cannot clear a variable, so use a save/restore pattern instead.
	prev, hadPrev := os.LookupEnv("DATABASE_URL")
	os.Unsetenv("DATABASE_URL")
	t.Cleanup(func() {
		if hadPrev {
			os.Setenv("DATABASE_URL", prev)
		} else {
			os.Unsetenv("DATABASE_URL")
		}
	})

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing required vars, got nil")
	}
}

func TestLoad_MissingRedisURL(t *testing.T) {
	setRequiredEnvVars(t)

	prev, hadPrev := os.LookupEnv("REDIS_URL")
	os.Unsetenv("REDIS_URL")
	t.Cleanup(func() {
		if hadPrev {
			os.Setenv("REDIS_URL", prev)
		} else {
			os.Unsetenv("REDIS_URL")
		}
	})

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing REDIS_URL, got nil")
	}
	if !strings.Contains(err.Error(), "REDIS_URL") {
		t.Errorf("expected error to mention REDIS_URL, got: %v", err)
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	tests := []struct {
		name string
		port string
	}{
		{"zero", "0"},
		{"negative", "-1"},
		{"too_high", "70000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredEnvVars(t)
			t.Setenv("PORT", tt.port)

			_, err := Load()
			if err == nil {
				t.Fatal("expected error for invalid port, got nil")
			}
			if !strings.Contains(err.Error(), "PORT") {
				t.Errorf("expected error to mention PORT, got: %v", err)
			}
		})
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("LOG_LEVEL", "verbose")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid log level, got nil")
	}
	if !strings.Contains(err.Error(), "LOG_LEVEL") {
		t.Errorf("expected error to mention LOG_LEVEL, got: %v", err)
	}
}

func TestLoad_NonPositiveJWTTTL(t *testing.T) {
	tests := []struct {
		name    string
		envVar  string
		value   string
		mention string
	}{
		{"zero_access", "JWT_ACCESS_TOKEN_TTL", "0s", "JWT_ACCESS_TOKEN_TTL"},
		{"negative_access", "JWT_ACCESS_TOKEN_TTL", "-5m", "JWT_ACCESS_TOKEN_TTL"},
		{"zero_refresh", "JWT_REFRESH_TOKEN_TTL", "0s", "JWT_REFRESH_TOKEN_TTL"},
		{"negative_refresh", "JWT_REFRESH_TOKEN_TTL", "-1h", "JWT_REFRESH_TOKEN_TTL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredEnvVars(t)
			t.Setenv(tt.envVar, tt.value)

			_, err := Load()
			if err == nil {
				t.Fatal("expected error for non-positive TTL, got nil")
			}
			if !strings.Contains(err.Error(), tt.mention) {
				t.Errorf("expected error to mention %s, got: %v", tt.mention, err)
			}
		})
	}
}

func TestLoad_ShortJWTSecret(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("JWT_SECRET", "too-short")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for short JWT secret, got nil")
	}
	if !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Errorf("expected error to mention JWT_SECRET, got: %v", err)
	}
}

func TestLoad_ExactMinJWTSecret(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error for 32-byte JWT secret, got: %v", err)
	}
	if len(cfg.JWTSecret) != 32 {
		t.Errorf("expected JWTSecret length 32, got: %d", len(cfg.JWTSecret))
	}
}

func TestLoad_ShortAdminSeedPassword(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("ADMIN_SEED_USERNAME", "admin")
	t.Setenv("ADMIN_SEED_PASSWORD", "short")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for short admin password, got nil")
	}
	if !strings.Contains(err.Error(), "ADMIN_SEED_PASSWORD") {
		t.Errorf("expected error to mention ADMIN_SEED_PASSWORD, got: %v", err)
	}
}

func TestLoad_TooLongAdminSeedPassword(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("ADMIN_SEED_USERNAME", "admin")
	t.Setenv("ADMIN_SEED_PASSWORD", strings.Repeat("a", 73))

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for admin password exceeding 72 bytes, got nil")
	}
	if !strings.Contains(err.Error(), "ADMIN_SEED_PASSWORD") {
		t.Errorf("expected error to mention ADMIN_SEED_PASSWORD, got: %v", err)
	}
	if !strings.Contains(err.Error(), "72 bytes") {
		t.Errorf("expected error to mention 72 bytes, got: %v", err)
	}
}

func TestLoad_ValidLogLevels(t *testing.T) {
	levels := []string{"debug", "info", "warn", "error"}

	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			setRequiredEnvVars(t)
			t.Setenv("LOG_LEVEL", level)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("expected no error for log level %q, got: %v", level, err)
			}
			if cfg.LogLevel != level {
				t.Errorf("expected LogLevel %q, got: %s", level, cfg.LogLevel)
			}
		})
	}
}

func TestLoad_SMTPTimeoutDefault(t *testing.T) {
	setRequiredEnvVars(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.SMTPTimeout != 30*time.Second {
		t.Errorf("expected default SMTPTimeout 30s, got: %s", cfg.SMTPTimeout)
	}
}

func TestLoad_SMTPTimeoutCustom(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("SMTP_TIMEOUT", "10s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.SMTPTimeout != 10*time.Second {
		t.Errorf("expected SMTPTimeout 10s, got: %s", cfg.SMTPTimeout)
	}
}

func TestLoad_SMTPTimeoutInvalid(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"zero", "0s"},
		{"negative", "-5s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredEnvVars(t)
			t.Setenv("SMTP_TIMEOUT", tt.value)

			_, err := Load()
			if err == nil {
				t.Fatal("expected error for invalid SMTP_TIMEOUT, got nil")
			}
			if !strings.Contains(err.Error(), "SMTP_TIMEOUT") {
				t.Errorf("expected error to mention SMTP_TIMEOUT, got: %v", err)
			}
		})
	}
}

func TestLoad_SMTPTLSPolicyDefault(t *testing.T) {
	setRequiredEnvVars(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.SMTPTLSPolicy != "mandatory" {
		t.Errorf("expected default SMTPTLSPolicy 'mandatory', got: %s", cfg.SMTPTLSPolicy)
	}
}

func TestLoad_SMTPTLSPolicyValid(t *testing.T) {
	policies := []string{"mandatory", "opportunistic", "none", "Mandatory", "OPPORTUNISTIC"}

	for _, policy := range policies {
		t.Run(policy, func(t *testing.T) {
			setRequiredEnvVars(t)
			t.Setenv("SMTP_TLS_POLICY", policy)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("expected no error for TLS policy %q, got: %v", policy, err)
			}
			expected := strings.ToLower(policy)
			if cfg.SMTPTLSPolicy != expected {
				t.Errorf("expected SMTPTLSPolicy %q, got: %s", expected, cfg.SMTPTLSPolicy)
			}
		})
	}
}

func TestLoad_SMTPTLSPolicyInvalid(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("SMTP_TLS_POLICY", "starttls")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid SMTP_TLS_POLICY, got nil")
	}
	if !strings.Contains(err.Error(), "SMTP_TLS_POLICY") {
		t.Errorf("expected error to mention SMTP_TLS_POLICY, got: %v", err)
	}
}

func TestLoad_AdminSeedVarsOptional(t *testing.T) {
	setRequiredEnvVars(t)
	// Neither ADMIN_SEED_USERNAME nor ADMIN_SEED_PASSWORD is set.

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error when admin seed vars are omitted, got: %v", err)
	}
	if cfg.AdminSeedUsername != "" {
		t.Errorf("expected empty AdminSeedUsername, got: %q", cfg.AdminSeedUsername)
	}
	if cfg.AdminSeedPassword != "" {
		t.Errorf("expected empty AdminSeedPassword, got: %q", cfg.AdminSeedPassword)
	}
}

func TestLoad_AdminSeedVarsBothSet(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("ADMIN_SEED_USERNAME", "admin")
	t.Setenv("ADMIN_SEED_PASSWORD", "validpass")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error when both admin seed vars are set, got: %v", err)
	}
	if cfg.AdminSeedUsername != "admin" {
		t.Errorf("expected AdminSeedUsername %q, got: %q", "admin", cfg.AdminSeedUsername)
	}
	if cfg.AdminSeedPassword != "validpass" {
		t.Errorf("expected AdminSeedPassword %q, got: %q", "validpass", cfg.AdminSeedPassword)
	}
}

func TestLoad_AdminSeedVarsPartial(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
	}{
		{"username_only", "admin", ""},
		{"password_only", "", "validpass"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredEnvVars(t)
			if tt.username != "" {
				t.Setenv("ADMIN_SEED_USERNAME", tt.username)
			}
			if tt.password != "" {
				t.Setenv("ADMIN_SEED_PASSWORD", tt.password)
			}

			_, err := Load()
			if err == nil {
				t.Fatal("expected error when only one admin seed var is set, got nil")
			}
			if !strings.Contains(err.Error(), "ADMIN_SEED_USERNAME") || !strings.Contains(err.Error(), "ADMIN_SEED_PASSWORD") {
				t.Errorf("expected error to mention both admin seed vars, got: %v", err)
			}
		})
	}
}

func TestLoad_CleanupDefaults(t *testing.T) {
	setRequiredEnvVars(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.CleanupInterval != time.Hour {
		t.Errorf("expected default CleanupInterval 1h, got: %s", cfg.CleanupInterval)
	}
	if cfg.OTPRetentionPeriod != 24*time.Hour {
		t.Errorf("expected default OTPRetentionPeriod 24h, got: %s", cfg.OTPRetentionPeriod)
	}
	if cfg.RefreshTokenRevokedRetention != 168*time.Hour {
		t.Errorf("expected default RefreshTokenRevokedRetention 168h, got: %s", cfg.RefreshTokenRevokedRetention)
	}
}

func TestLoad_CleanupCustom(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("CLEANUP_INTERVAL", "30m")
	t.Setenv("OTP_RETENTION_PERIOD", "12h")
	t.Setenv("REFRESH_TOKEN_REVOKED_RETENTION", "48h")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.CleanupInterval != 30*time.Minute {
		t.Errorf("expected CleanupInterval 30m, got: %s", cfg.CleanupInterval)
	}
	if cfg.OTPRetentionPeriod != 12*time.Hour {
		t.Errorf("expected OTPRetentionPeriod 12h, got: %s", cfg.OTPRetentionPeriod)
	}
	if cfg.RefreshTokenRevokedRetention != 48*time.Hour {
		t.Errorf("expected RefreshTokenRevokedRetention 48h, got: %s", cfg.RefreshTokenRevokedRetention)
	}
}

func TestLoad_CleanupInvalid(t *testing.T) {
	tests := []struct {
		name    string
		envVar  string
		value   string
		mention string
	}{
		{"zero_interval", "CLEANUP_INTERVAL", "0s", "CLEANUP_INTERVAL"},
		{"negative_interval", "CLEANUP_INTERVAL", "-1h", "CLEANUP_INTERVAL"},
		{"zero_otp_retention", "OTP_RETENTION_PERIOD", "0s", "OTP_RETENTION_PERIOD"},
		{"negative_otp_retention", "OTP_RETENTION_PERIOD", "-1h", "OTP_RETENTION_PERIOD"},
		{"zero_rt_retention", "REFRESH_TOKEN_REVOKED_RETENTION", "0s", "REFRESH_TOKEN_REVOKED_RETENTION"},
		{"negative_rt_retention", "REFRESH_TOKEN_REVOKED_RETENTION", "-1h", "REFRESH_TOKEN_REVOKED_RETENTION"},
		{"zero_sync_rate_limit", "SYNC_RATE_LIMIT", "0", "SYNC_RATE_LIMIT"},
		{"negative_sync_rate_limit", "SYNC_RATE_LIMIT", "-1", "SYNC_RATE_LIMIT"},
		{"zero_sync_rate_window", "SYNC_RATE_WINDOW", "0s", "SYNC_RATE_WINDOW"},
		{"negative_sync_rate_window", "SYNC_RATE_WINDOW", "-1m", "SYNC_RATE_WINDOW"},
		{"zero_admin_jwt_ttl", "ADMIN_JWT_ACCESS_TOKEN_TTL", "0s", "ADMIN_JWT_ACCESS_TOKEN_TTL"},
		{"negative_admin_jwt_ttl", "ADMIN_JWT_ACCESS_TOKEN_TTL", "-5m", "ADMIN_JWT_ACCESS_TOKEN_TTL"},
		{"zero_admin_rate_limit", "ADMIN_RATE_LIMIT", "0", "ADMIN_RATE_LIMIT"},
		{"negative_admin_rate_limit", "ADMIN_RATE_LIMIT", "-1", "ADMIN_RATE_LIMIT"},
		{"zero_admin_rate_window", "ADMIN_RATE_WINDOW", "0s", "ADMIN_RATE_WINDOW"},
		{"negative_admin_rate_window", "ADMIN_RATE_WINDOW", "-1m", "ADMIN_RATE_WINDOW"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredEnvVars(t)
			t.Setenv(tt.envVar, tt.value)

			_, err := Load()
			if err == nil {
				t.Fatalf("expected error for invalid %s, got nil", tt.envVar)
			}
			if !strings.Contains(err.Error(), tt.mention) {
				t.Errorf("expected error to mention %s, got: %v", tt.mention, err)
			}
		})
	}
}

func TestLoad_TrustedProxies_DefaultEmpty(t *testing.T) {
	setRequiredEnvVars(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.TrustedProxies != "" {
		t.Errorf("expected empty TrustedProxies, got: %q", cfg.TrustedProxies)
	}
	nets := cfg.ParseTrustedProxies()
	if nets != nil {
		t.Errorf("expected nil nets for empty TrustedProxies, got: %v", nets)
	}
}

func TestLoad_TrustedProxies_ValidCIDR(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("TRUSTED_PROXIES", "172.16.0.0/12, 10.0.0.0/8")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	nets := cfg.ParseTrustedProxies()
	if len(nets) != 2 {
		t.Fatalf("expected 2 networks, got: %d", len(nets))
	}

	want := []string{"172.16.0.0/12", "10.0.0.0/8"}
	for i, n := range nets {
		if n.String() != want[i] {
			t.Errorf("net[%d] = %q, want %q", i, n.String(), want[i])
		}
	}
}

func TestLoad_TrustedProxies_ValidSingleIP(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("TRUSTED_PROXIES", "10.0.0.1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	nets := cfg.ParseTrustedProxies()
	if len(nets) != 1 {
		t.Fatalf("expected 1 network, got: %d", len(nets))
	}
	if !nets[0].Contains(net.ParseIP("10.0.0.1")) {
		t.Errorf("expected net to contain 10.0.0.1")
	}
	// Single IP should be a /32.
	ones, _ := nets[0].Mask.Size()
	if ones != 32 {
		t.Errorf("expected /32 mask, got /%d", ones)
	}
}

func TestLoad_TrustedProxies_MixedIPAndCIDR(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("TRUSTED_PROXIES", "10.0.0.1, 172.16.0.0/12")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	nets := cfg.ParseTrustedProxies()
	if len(nets) != 2 {
		t.Fatalf("expected 2 networks, got: %d", len(nets))
	}
}

func TestLoad_TrustedProxies_InvalidCIDR(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("TRUSTED_PROXIES", "172.16.0.0/99")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid CIDR, got nil")
	}
	if !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
		t.Errorf("expected error to mention TRUSTED_PROXIES, got: %v", err)
	}
}

func TestLoad_TrustedProxies_InvalidIP(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("TRUSTED_PROXIES", "not-an-ip")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid IP, got nil")
	}
	if !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
		t.Errorf("expected error to mention TRUSTED_PROXIES, got: %v", err)
	}
}

func TestLoad_TrustedProxies_IPv6(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("TRUSTED_PROXIES", "fd00::/8, ::1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	nets := cfg.ParseTrustedProxies()
	if len(nets) != 2 {
		t.Fatalf("expected 2 networks, got: %d", len(nets))
	}
	// ::1 should be /128.
	ones, _ := nets[1].Mask.Size()
	if ones != 128 {
		t.Errorf("expected /128 mask for ::1, got /%d", ones)
	}
}

// ---------------------------------------------------------------------------
// MigrateConfig tests
// ---------------------------------------------------------------------------

func TestLoadMigrate_Success(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost:5432/test?sslmode=disable")

	cfg, err := LoadMigrate()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.DatabaseURL != "postgres://test:test@localhost:5432/test?sslmode=disable" {
		t.Errorf("unexpected DatabaseURL: %s", cfg.DatabaseURL)
	}
}

func TestLoadMigrate_MissingDatabaseURL(t *testing.T) {
	// Ensure DATABASE_URL is unset even if the developer has it in their shell.
	prev, hadPrev := os.LookupEnv("DATABASE_URL")
	os.Unsetenv("DATABASE_URL")
	t.Cleanup(func() {
		if hadPrev {
			os.Setenv("DATABASE_URL", prev)
		} else {
			os.Unsetenv("DATABASE_URL")
		}
	})

	_, err := LoadMigrate()
	if err == nil {
		t.Fatal("expected error for missing DATABASE_URL, got nil")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("expected error to mention DATABASE_URL, got: %v", err)
	}
}

func TestLoadMigrate_DoesNotRequireServerVars(t *testing.T) {
	// Only set DATABASE_URL; no JWT, SMTP, etc.
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost:5432/test?sslmode=disable")

	// Ensure none of the server-only required vars are set.
	for _, key := range []string{
		"JWT_SECRET", "JWT_ACCESS_TOKEN_TTL", "JWT_REFRESH_TOKEN_TTL",
		"SMTP_HOST", "SMTP_PORT", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_FROM",
		"REVENUECAT_API_KEY", "REVENUECAT_WEBHOOK_SECRET",
		"FIREBASE_SERVICE_ACCOUNT_JSON",
	} {
		prev, hadPrev := os.LookupEnv(key)
		os.Unsetenv(key)
		t.Cleanup(func() {
			if hadPrev {
				os.Setenv(key, prev)
			} else {
				os.Unsetenv(key)
			}
		})
	}

	cfg, err := LoadMigrate()
	if err != nil {
		t.Fatalf("expected no error when only DATABASE_URL is set, got: %v", err)
	}
	if cfg.DatabaseURL == "" {
		t.Error("expected non-empty DatabaseURL")
	}
}

// ---------------------------------------------------------------------------
// SeedAdminConfig tests
// ---------------------------------------------------------------------------

// setSeedAdminEnvVars sets the minimal environment for LoadSeedAdmin.
func setSeedAdminEnvVars(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost:5432/test?sslmode=disable")
	t.Setenv("ADMIN_SEED_USERNAME", "admin")
	t.Setenv("ADMIN_SEED_PASSWORD", "validpass")
}

func TestLoadSeedAdmin_Success(t *testing.T) {
	setSeedAdminEnvVars(t)

	cfg, err := LoadSeedAdmin()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.DatabaseURL != "postgres://test:test@localhost:5432/test?sslmode=disable" {
		t.Errorf("unexpected DatabaseURL: %s", cfg.DatabaseURL)
	}
	if cfg.AdminSeedUsername != "admin" {
		t.Errorf("unexpected AdminSeedUsername: %s", cfg.AdminSeedUsername)
	}
	if cfg.AdminSeedPassword != "validpass" {
		t.Errorf("unexpected AdminSeedPassword: %s", cfg.AdminSeedPassword)
	}
}

func TestLoadSeedAdmin_MissingDatabaseURL(t *testing.T) {
	t.Setenv("ADMIN_SEED_USERNAME", "admin")
	t.Setenv("ADMIN_SEED_PASSWORD", "validpass")

	prev, hadPrev := os.LookupEnv("DATABASE_URL")
	os.Unsetenv("DATABASE_URL")
	t.Cleanup(func() {
		if hadPrev {
			os.Setenv("DATABASE_URL", prev)
		} else {
			os.Unsetenv("DATABASE_URL")
		}
	})

	_, err := LoadSeedAdmin()
	if err == nil {
		t.Fatal("expected error for missing DATABASE_URL, got nil")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("expected error to mention DATABASE_URL, got: %v", err)
	}
}

func TestLoadSeedAdmin_MissingUsername(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost:5432/test?sslmode=disable")
	t.Setenv("ADMIN_SEED_PASSWORD", "validpass")

	prev, hadPrev := os.LookupEnv("ADMIN_SEED_USERNAME")
	os.Unsetenv("ADMIN_SEED_USERNAME")
	t.Cleanup(func() {
		if hadPrev {
			os.Setenv("ADMIN_SEED_USERNAME", prev)
		} else {
			os.Unsetenv("ADMIN_SEED_USERNAME")
		}
	})

	_, err := LoadSeedAdmin()
	if err == nil {
		t.Fatal("expected error for missing ADMIN_SEED_USERNAME, got nil")
	}
	if !strings.Contains(err.Error(), "ADMIN_SEED_USERNAME") {
		t.Errorf("expected error to mention ADMIN_SEED_USERNAME, got: %v", err)
	}
}

func TestLoadSeedAdmin_MissingPassword(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test:test@localhost:5432/test?sslmode=disable")
	t.Setenv("ADMIN_SEED_USERNAME", "admin")

	prev, hadPrev := os.LookupEnv("ADMIN_SEED_PASSWORD")
	os.Unsetenv("ADMIN_SEED_PASSWORD")
	t.Cleanup(func() {
		if hadPrev {
			os.Setenv("ADMIN_SEED_PASSWORD", prev)
		} else {
			os.Unsetenv("ADMIN_SEED_PASSWORD")
		}
	})

	_, err := LoadSeedAdmin()
	if err == nil {
		t.Fatal("expected error for missing ADMIN_SEED_PASSWORD, got nil")
	}
	if !strings.Contains(err.Error(), "ADMIN_SEED_PASSWORD") {
		t.Errorf("expected error to mention ADMIN_SEED_PASSWORD, got: %v", err)
	}
}

func TestLoadSeedAdmin_ShortPassword(t *testing.T) {
	setSeedAdminEnvVars(t)
	t.Setenv("ADMIN_SEED_PASSWORD", "short")

	_, err := LoadSeedAdmin()
	if err == nil {
		t.Fatal("expected error for short admin password, got nil")
	}
	if !strings.Contains(err.Error(), "ADMIN_SEED_PASSWORD") {
		t.Errorf("expected error to mention ADMIN_SEED_PASSWORD, got: %v", err)
	}
}

func TestLoadSeedAdmin_TooLongPassword(t *testing.T) {
	setSeedAdminEnvVars(t)
	t.Setenv("ADMIN_SEED_PASSWORD", strings.Repeat("a", 73))

	_, err := LoadSeedAdmin()
	if err == nil {
		t.Fatal("expected error for admin password exceeding 72 bytes, got nil")
	}
	if !strings.Contains(err.Error(), "ADMIN_SEED_PASSWORD") {
		t.Errorf("expected error to mention ADMIN_SEED_PASSWORD, got: %v", err)
	}
	if !strings.Contains(err.Error(), "72 bytes") {
		t.Errorf("expected error to mention 72 bytes, got: %v", err)
	}
}

func TestLoadSeedAdmin_DoesNotRequireServerVars(t *testing.T) {
	setSeedAdminEnvVars(t)

	// Ensure none of the server-only required vars are set.
	for _, key := range []string{
		"JWT_SECRET", "JWT_ACCESS_TOKEN_TTL", "JWT_REFRESH_TOKEN_TTL",
		"SMTP_HOST", "SMTP_PORT", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_FROM",
		"REVENUECAT_API_KEY", "REVENUECAT_WEBHOOK_SECRET",
		"FIREBASE_SERVICE_ACCOUNT_JSON",
	} {
		prev, hadPrev := os.LookupEnv(key)
		os.Unsetenv(key)
		t.Cleanup(func() {
			if hadPrev {
				os.Setenv(key, prev)
			} else {
				os.Unsetenv(key)
			}
		})
	}

	cfg, err := LoadSeedAdmin()
	if err != nil {
		t.Fatalf("expected no error when only seed-admin vars are set, got: %v", err)
	}
	if cfg.DatabaseURL == "" {
		t.Error("expected non-empty DatabaseURL")
	}
	if cfg.AdminSeedUsername == "" {
		t.Error("expected non-empty AdminSeedUsername")
	}
}
