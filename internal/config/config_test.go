package config

import (
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
		"JWT_SECRET":                    "test-secret",
		"JWT_ACCESS_TOKEN_TTL":          "15m",
		"JWT_REFRESH_TOKEN_TTL":         "720h",
		"SMTP_HOST":                     "smtp.test.com",
		"SMTP_PORT":                     "587",
		"SMTP_USERNAME":                 "testuser",
		"SMTP_PASSWORD":                 "testpass",
		"SMTP_FROM":                     "test@test.com",
		"ADMIN_SEED_USERNAME":           "admin",
		"ADMIN_SEED_PASSWORD":           "adminpass",
		"REVENUECAT_API_KEY":            "rc_test_key",
		"REVENUECAT_WEBHOOK_SECRET":     "rc_test_secret",
		"FIREBASE_SERVICE_ACCOUNT_JSON": "{}",
		"STUDENT_VERIFICATION_PROVIDER": "sheerid",
		"STUDENT_VERIFICATION_API_KEY":  "sv_test_key",
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
	if cfg.JWTSecret != "test-secret" {
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
	if cfg.AdminSeedUsername != "admin" {
		t.Errorf("unexpected AdminSeedUsername: %s", cfg.AdminSeedUsername)
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
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_ACCESS_TOKEN_TTL", "15m")
	t.Setenv("JWT_REFRESH_TOKEN_TTL", "720h")
	t.Setenv("SMTP_HOST", "smtp.test.com")
	t.Setenv("SMTP_PORT", "587")
	t.Setenv("SMTP_USERNAME", "testuser")
	t.Setenv("SMTP_PASSWORD", "testpass")
	t.Setenv("SMTP_FROM", "test@test.com")
	t.Setenv("ADMIN_SEED_USERNAME", "admin")
	t.Setenv("ADMIN_SEED_PASSWORD", "adminpass")
	t.Setenv("REVENUECAT_API_KEY", "rc_test_key")
	t.Setenv("REVENUECAT_WEBHOOK_SECRET", "rc_test_secret")
	t.Setenv("FIREBASE_SERVICE_ACCOUNT_JSON", "{}")
	t.Setenv("STUDENT_VERIFICATION_PROVIDER", "sheerid")
	t.Setenv("STUDENT_VERIFICATION_API_KEY", "sv_test_key")

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

func TestLoad_ShortAdminSeedPassword(t *testing.T) {
	setRequiredEnvVars(t)
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

func TestLoad_InvalidStudentVerificationProvider(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("STUDENT_VERIFICATION_PROVIDER", "unknown_provider")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), "STUDENT_VERIFICATION_PROVIDER") {
		t.Errorf("expected error to mention STUDENT_VERIFICATION_PROVIDER, got: %v", err)
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
