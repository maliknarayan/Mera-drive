// Package config loads and validates runtime configuration from the environment.
//
// Configuration is read exactly once at startup. A missing or malformed value is
// a fatal error rather than a runtime surprise: an operator should learn about a
// bad ENCRYPTION_KEY when the process refuses to boot, not when the first user
// tries to connect an account.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment identifies the deployment mode.
type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvProduction  Environment = "production"
)

// IsProduction reports whether stricter defaults should apply.
func (e Environment) IsProduction() bool { return e == EnvProduction }

// keyLen is the required raw byte length of ENCRYPTION_KEY and SESSION_SECRET.
const keyLen = 32

// Config is the fully validated application configuration.
type Config struct {
	Env      Environment
	LogLevel string

	APIPort     int
	APIBaseURL  string
	AppBaseURL  string
	CORSOrigins []string
	TrustProxy  bool

	GoogleClientID     string
	GoogleClientSecret string

	EncryptionKey []byte
	SessionSecret []byte

	CookieDomain string
	CookieSecure bool
	SessionTTL   time.Duration

	StoreDriver string
	SQLitePath  string

	RateLimitMax    int
	RateLimitWindow time.Duration

	DriveConcurrency int
	DriveTimeout     time.Duration
}

// GoogleRedirectURL is the OAuth callback Google must be configured to allow.
// Derived from APIBaseURL so operators only configure one origin.
func (c *Config) GoogleRedirectURL() string {
	return strings.TrimRight(c.APIBaseURL, "/") + "/api/v1/auth/google/callback"
}

// Load reads configuration from the process environment.
func Load() (*Config, error) {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	cfg := &Config{
		Env:              Environment(str("SANGAM_ENV", string(EnvDevelopment))),
		LogLevel:         strings.ToLower(str("LOG_LEVEL", "info")),
		APIBaseURL:       str("API_BASE_URL", "http://localhost:8080"),
		AppBaseURL:       str("APP_BASE_URL", "http://localhost:3000"),
		CookieDomain:     str("COOKIE_DOMAIN", ""),
		StoreDriver:      strings.ToLower(str("STORE_DRIVER", "sqlite")),
		SQLitePath:       str("SQLITE_PATH", "./data/sangamdrive.db"),
		GoogleClientID:   str("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: str("GOOGLE_CLIENT_SECRET", ""),
	}

	if cfg.Env != EnvDevelopment && cfg.Env != EnvProduction {
		fail("SANGAM_ENV must be %q or %q, got %q", EnvDevelopment, EnvProduction, cfg.Env)
	}
	if !validLogLevels[cfg.LogLevel] {
		fail("LOG_LEVEL must be one of debug|info|warn|error, got %q", cfg.LogLevel)
	}

	var err error
	if cfg.APIPort, err = intVal("API_PORT", 8080); err != nil {
		errs = append(errs, err)
	} else if cfg.APIPort < 1 || cfg.APIPort > 65535 {
		fail("API_PORT must be between 1 and 65535, got %d", cfg.APIPort)
	}

	if err := requireAbsURL("API_BASE_URL", cfg.APIBaseURL); err != nil {
		errs = append(errs, err)
	}
	if err := requireAbsURL("APP_BASE_URL", cfg.AppBaseURL); err != nil {
		errs = append(errs, err)
	}

	cfg.CORSOrigins = csv(str("CORS_ORIGINS", cfg.AppBaseURL))
	if len(cfg.CORSOrigins) == 0 {
		fail("CORS_ORIGINS must list at least one origin")
	}
	for _, o := range cfg.CORSOrigins {
		if o == "*" {
			fail("CORS_ORIGINS cannot be \"*\" because the API uses credentialed requests")
			break
		}
		if err := requireAbsURL("CORS_ORIGINS", o); err != nil {
			errs = append(errs, err)
		}
	}

	if cfg.TrustProxy, err = boolVal("TRUST_PROXY", false); err != nil {
		errs = append(errs, err)
	}

	if cfg.GoogleClientID == "" {
		fail("GOOGLE_CLIENT_ID is required")
	}
	if cfg.GoogleClientSecret == "" {
		fail("GOOGLE_CLIENT_SECRET is required")
	}

	if cfg.EncryptionKey, err = keyVal("ENCRYPTION_KEY"); err != nil {
		errs = append(errs, err)
	}
	if cfg.SessionSecret, err = keyVal("SESSION_SECRET"); err != nil {
		errs = append(errs, err)
	}

	if cfg.CookieSecure, err = boolVal("COOKIE_SECURE", cfg.Env.IsProduction()); err != nil {
		errs = append(errs, err)
	}
	if cfg.Env.IsProduction() && !cfg.CookieSecure {
		fail("COOKIE_SECURE cannot be false in production")
	}

	if cfg.SessionTTL, err = durationVal("SESSION_TTL", 30*24*time.Hour); err != nil {
		errs = append(errs, err)
	} else if cfg.SessionTTL <= 0 {
		fail("SESSION_TTL must be positive")
	}

	if cfg.StoreDriver != "sqlite" {
		fail("STORE_DRIVER %q is not implemented; only \"sqlite\" is available", cfg.StoreDriver)
	}
	if cfg.SQLitePath == "" {
		fail("SQLITE_PATH is required when STORE_DRIVER=sqlite")
	}

	if cfg.RateLimitMax, err = intVal("RATE_LIMIT_MAX", 300); err != nil {
		errs = append(errs, err)
	} else if cfg.RateLimitMax < 1 {
		fail("RATE_LIMIT_MAX must be at least 1")
	}
	if cfg.RateLimitWindow, err = durationVal("RATE_LIMIT_WINDOW", time.Minute); err != nil {
		errs = append(errs, err)
	} else if cfg.RateLimitWindow <= 0 {
		fail("RATE_LIMIT_WINDOW must be positive")
	}

	if cfg.DriveConcurrency, err = intVal("DRIVE_CONCURRENCY", 8); err != nil {
		errs = append(errs, err)
	} else if cfg.DriveConcurrency < 1 || cfg.DriveConcurrency > 64 {
		fail("DRIVE_CONCURRENCY must be between 1 and 64, got %d", cfg.DriveConcurrency)
	}
	if cfg.DriveTimeout, err = durationVal("DRIVE_TIMEOUT", 30*time.Second); err != nil {
		errs = append(errs, err)
	} else if cfg.DriveTimeout <= 0 {
		fail("DRIVE_TIMEOUT must be positive")
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid configuration:\n  %w", errors.Join(errs...))
	}
	return cfg, nil
}

var validLogLevels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

func str(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func intVal(key string, def int) (int, error) {
	raw := str(key, "")
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q", key, raw)
	}
	return n, nil
}

func boolVal(key string, def bool) (bool, error) {
	raw := str(key, "")
	if raw == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean, got %q", key, raw)
	}
	return b, nil
}

func durationVal(key string, def time.Duration) (time.Duration, error) {
	raw := str(key, "")
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration such as 30s or 720h, got %q", key, raw)
	}
	return d, nil
}

// keyVal decodes a base64 secret and enforces the exact key length.
func keyVal(key string) ([]byte, error) {
	raw := str(key, "")
	if raw == "" {
		return nil, fmt.Errorf("%s is required; generate one with: openssl rand -base64 32", key)
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be base64-encoded: %w", key, err)
	}
	if len(b) != keyLen {
		return nil, fmt.Errorf("%s must decode to exactly %d bytes, got %d", key, keyLen, len(b))
	}
	return b, nil
}

func csv(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, strings.TrimRight(p, "/"))
		}
	}
	return out
}

func requireAbsURL(key, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%s must be an absolute URL such as https://drive.example.com, got %q", key, raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s must use http or https, got %q", key, u.Scheme)
	}
	return nil
}
