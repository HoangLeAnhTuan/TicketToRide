package config_test

import (
	"strings"
	"testing"
	"time"

	"ticket-to-ride/internal/config"
)

func TestLoad(t *testing.T) {
	t.Parallel()

	validEnv := map[string]string{
		"SUPABASE_URL": "https://example.supabase.co",
	}

	lookup := func(env map[string]string) func(string) string {
		return func(k string) string {
			return env[k]
		}
	}

	t.Run("valid default config", func(t *testing.T) {
		t.Parallel()
		cfg, err := config.Load(lookup(validEnv))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.HTTPAddr != "127.0.0.1:8080" {
			t.Errorf("expected 127.0.0.1:8080, got %s", cfg.HTTPAddr)
		}
		if cfg.Issuer != "https://example.supabase.co/auth/v1" {
			t.Errorf("expected https://example.supabase.co/auth/v1, got %s", cfg.Issuer)
		}
		if cfg.JWKSURL != "https://example.supabase.co/auth/v1/.well-known/jwks.json" {
			t.Errorf("expected https://example.supabase.co/auth/v1/.well-known/jwks.json, got %s", cfg.JWKSURL)
		}
		if cfg.Audience != "authenticated" {
			t.Errorf("expected authenticated, got %s", cfg.Audience)
		}
		if len(cfg.Algorithms) != 2 || cfg.Algorithms[0] != "ES256" || cfg.Algorithms[1] != "RS256" {
			t.Errorf("unexpected algorithms: %v", cfg.Algorithms)
		}
		if cfg.JWKSCacheTTL != 5*time.Minute {
			t.Errorf("expected 5m, got %v", cfg.JWKSCacheTTL)
		}
		if cfg.RefreshInterval != 10*time.Second {
			t.Errorf("expected 10s, got %v", cfg.RefreshInterval)
		}
	})

	t.Run("missing supabase URL and issuer", func(t *testing.T) {
		t.Parallel()
		_, err := config.Load(lookup(map[string]string{}))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("invalid HTTP addr", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			"SUPABASE_URL": "https://example.supabase.co",
			"HTTP_ADDR":    "not-a-host-port",
		}
		_, err := config.Load(lookup(env))
		if err == nil || !strings.Contains(err.Error(), "HTTP_ADDR") {
			t.Fatalf("expected HTTP_ADDR error, got %v", err)
		}
	})

	t.Run("insecure non-loopback HTTP endpoint rejected", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			"SUPABASE_URL": "http://insecure.example.com",
		}
		_, err := config.Load(lookup(env))
		if err == nil || !strings.Contains(err.Error(), "HTTPS is required") {
			t.Fatalf("expected HTTPS required error, got %v", err)
		}
	})

	t.Run("loopback HTTP allowed for local dev", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			"SUPABASE_URL": "http://127.0.0.1:54321",
		}
		cfg, err := config.Load(lookup(env))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Issuer != "http://127.0.0.1:54321/auth/v1" {
			t.Errorf("unexpected issuer: %s", cfg.Issuer)
		}
	})

	t.Run("legacy HS256 valid", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			"SUPABASE_URL":                 "https://example.supabase.co",
			"SUPABASE_ENABLE_LEGACY_HS256": "true",
			"SUPABASE_JWT_ALGORITHMS":      "HS256",
			"SUPABASE_LEGACY_JWT_SECRET":   "secret-must-be-at-least-32-bytes-long!",
		}
		cfg, err := config.Load(lookup(env))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.LegacySecret != "secret-must-be-at-least-32-bytes-long!" {
			t.Errorf("unexpected legacy secret: %s", cfg.LegacySecret)
		}
	})

	t.Run("legacy HS256 rejected if secret too short", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			"SUPABASE_URL":                 "https://example.supabase.co",
			"SUPABASE_ENABLE_LEGACY_HS256": "true",
			"SUPABASE_JWT_ALGORITHMS":      "HS256",
			"SUPABASE_LEGACY_JWT_SECRET":   "short",
		}
		_, err := config.Load(lookup(env))
		if err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
			t.Fatalf("expected secret length error, got %v", err)
		}
	})

	t.Run("legacy HS256 rejected without explicit flag", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			"SUPABASE_URL":            "https://example.supabase.co",
			"SUPABASE_JWT_ALGORITHMS": "HS256",
		}
		_, err := config.Load(lookup(env))
		if err == nil || !strings.Contains(err.Error(), "legacy HS256 must be explicitly enabled") {
			t.Fatalf("expected legacy explicit error, got %v", err)
		}
	})

	t.Run("duration validation and bounds", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			"SUPABASE_URL":          "https://example.supabase.co",
			"JWKS_CACHE_TTL":        "1s",
			"JWKS_REFRESH_INTERVAL": "5s",
		}
		_, err := config.Load(lookup(env))
		if err == nil || !strings.Contains(err.Error(), "JWKS_REFRESH_INTERVAL must not exceed JWKS_CACHE_TTL") {
			t.Fatalf("expected interval > ttl error, got %v", err)
		}
	})
}
