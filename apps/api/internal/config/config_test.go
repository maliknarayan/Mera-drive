package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// validEnv is the minimum set of variables required for a successful Load.
func validEnv() map[string]string {
	key := base64.StdEncoding.EncodeToString(make([]byte, keyLen))
	return map[string]string{
		"GOOGLE_CLIENT_ID":     "client-id",
		"GOOGLE_CLIENT_SECRET": "client-secret",
		"ENCRYPTION_KEY":       key,
		"SESSION_SECRET":       key,
	}
}

func loadWith(t *testing.T, overrides map[string]string) (*Config, error) {
	t.Helper()

	env := validEnv()
	for k, v := range overrides {
		if v == "" {
			delete(env, k)
			continue
		}
		env[k] = v
	}
	// clear anything the host environment might be leaking in
	for _, key := range allKeys {
		t.Setenv(key, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	return Load()
}

var allKeys = []string{
	"SANGAM_ENV", "LOG_LEVEL", "API_PORT", "API_BASE_URL", "APP_BASE_URL",
	"CORS_ORIGINS", "TRUST_PROXY", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET",
	"ENCRYPTION_KEY", "SESSION_SECRET", "COOKIE_DOMAIN", "COOKIE_SECURE",
	"SESSION_TTL", "STORE_DRIVER", "SQLITE_PATH", "RATE_LIMIT_MAX",
	"RATE_LIMIT_WINDOW", "DRIVE_CONCURRENCY", "DRIVE_TIMEOUT",
}

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := loadWith(t, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Env != EnvDevelopment {
		t.Errorf("Env: got %q", cfg.Env)
	}
	if cfg.APIPort != 8080 {
		t.Errorf("APIPort: got %d", cfg.APIPort)
	}
	if cfg.SessionTTL != 30*24*time.Hour {
		t.Errorf("SessionTTL: got %v", cfg.SessionTTL)
	}
	if cfg.DriveConcurrency != 8 {
		t.Errorf("DriveConcurrency: got %d", cfg.DriveConcurrency)
	}
	if cfg.CookieSecure {
		t.Error("CookieSecure should default to false outside production")
	}
	if len(cfg.CORSOrigins) != 1 || cfg.CORSOrigins[0] != "http://localhost:3000" {
		t.Errorf("CORSOrigins: got %v", cfg.CORSOrigins)
	}
	if len(cfg.EncryptionKey) != keyLen {
		t.Errorf("EncryptionKey: got %d bytes", len(cfg.EncryptionKey))
	}
}

func TestGoogleRedirectURLDerivesFromAPIBaseURL(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{"API_BASE_URL": "https://drive.example.com/"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := "https://drive.example.com/api/v1/auth/google/callback"
	if got := cfg.GoogleRedirectURL(); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	cases := map[string]struct {
		overrides map[string]string
		wantIn    string
	}{
		"missing client id": {
			map[string]string{"GOOGLE_CLIENT_ID": ""}, "GOOGLE_CLIENT_ID is required",
		},
		"missing encryption key": {
			map[string]string{"ENCRYPTION_KEY": ""}, "ENCRYPTION_KEY is required",
		},
		"non-base64 key": {
			map[string]string{"ENCRYPTION_KEY": "not base64!"}, "must be base64-encoded",
		},
		"short key": {
			map[string]string{"SESSION_SECRET": base64.StdEncoding.EncodeToString(make([]byte, 16))},
			"must decode to exactly 32 bytes",
		},
		"bad environment": {
			map[string]string{"SANGAM_ENV": "staging"}, "SANGAM_ENV must be",
		},
		"bad log level": {
			map[string]string{"LOG_LEVEL": "verbose"}, "LOG_LEVEL must be",
		},
		"bad port": {
			map[string]string{"API_PORT": "70000"}, "API_PORT must be between",
		},
		"non-numeric port": {
			map[string]string{"API_PORT": "http"}, "API_PORT must be an integer",
		},
		"relative base url": {
			map[string]string{"API_BASE_URL": "/api"}, "must be an absolute URL",
		},
		"wildcard cors": {
			map[string]string{"CORS_ORIGINS": "*"}, "cannot be \"*\"",
		},
		"unknown store driver": {
			map[string]string{"STORE_DRIVER": "postgres"}, "is not implemented",
		},
		"insecure cookies in production": {
			map[string]string{"SANGAM_ENV": "production", "COOKIE_SECURE": "false"},
			"COOKIE_SECURE cannot be false in production",
		},
		"bad duration": {
			map[string]string{"SESSION_TTL": "forever"}, "must be a Go duration",
		},
		"concurrency out of range": {
			map[string]string{"DRIVE_CONCURRENCY": "0"}, "DRIVE_CONCURRENCY must be between",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadWith(t, tc.overrides)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error %q does not mention %q", err, tc.wantIn)
			}
		})
	}
}

func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	_, err := loadWith(t, map[string]string{
		"GOOGLE_CLIENT_ID":     "",
		"GOOGLE_CLIENT_SECRET": "",
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "GOOGLE_CLIENT_ID") ||
		!strings.Contains(err.Error(), "GOOGLE_CLIENT_SECRET") {
		t.Errorf("expected both problems reported, got: %v", err)
	}
}

func TestProductionDefaultsCookieSecure(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{
		"SANGAM_ENV": "production",
		"API_BASE_URL": "https://drive.example.com",
		"APP_BASE_URL": "https://drive.example.com",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.CookieSecure {
		t.Error("CookieSecure should default to true in production")
	}
}
