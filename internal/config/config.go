// Package config validates the server's environment at startup.
package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr         string
	Issuer           string
	Audience         string
	JWKSURL          string
	Algorithms       []string
	LegacySecret     string
	JWKSCacheTTL     time.Duration
	RefreshInterval  time.Duration
	AuthHTTPTimeout  time.Duration
	ShutdownTimeout  time.Duration
	DatabaseURL      string
	DatabaseMaxConns int
	RoomWorkers      int
	RoomQueue        int
	MaxRooms         int
	BcryptCost       int
	AllowedOrigins   []string
}

// Load accepts an environment lookup so tests never change process-wide state.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		HTTPAddr: value(getenv, "HTTP_ADDR", "127.0.0.1:8080"),
		Audience: value(getenv, "SUPABASE_JWT_AUDIENCE", "authenticated"),
	}
	_, port, err := net.SplitHostPort(c.HTTPAddr)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || portNumber < 1 || portNumber > 65535 {
		return Config{}, fmt.Errorf("HTTP_ADDR must be host:port")
	}
	base := strings.TrimRight(strings.TrimSpace(getenv("SUPABASE_URL")), "/")
	c.Issuer = value(getenv, "SUPABASE_JWT_ISSUER", base+"/auth/v1")
	if base == "" && strings.TrimSpace(getenv("SUPABASE_JWT_ISSUER")) == "" {
		return Config{}, fmt.Errorf("SUPABASE_URL or SUPABASE_JWT_ISSUER is required")
	}
	if err := ValidateEndpoint(c.Issuer); err != nil {
		return Config{}, fmt.Errorf("SUPABASE_JWT_ISSUER: %w", err)
	}
	c.JWKSURL = value(getenv, "SUPABASE_JWKS_URL", strings.TrimRight(c.Issuer, "/")+"/.well-known/jwks.json")
	if err := ValidateEndpoint(c.JWKSURL); err != nil {
		return Config{}, fmt.Errorf("SUPABASE_JWKS_URL: %w", err)
	}
	if c.Audience != "authenticated" {
		return Config{}, fmt.Errorf("SUPABASE_JWT_AUDIENCE must be authenticated")
	}
	legacy, err := strconv.ParseBool(value(getenv, "SUPABASE_ENABLE_LEGACY_HS256", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("SUPABASE_ENABLE_LEGACY_HS256 must be true or false")
	}
	seen := make(map[string]bool)
	for _, entry := range strings.Split(value(getenv, "SUPABASE_JWT_ALGORITHMS", "ES256,RS256"), ",") {
		alg := strings.TrimSpace(entry)
		if seen[alg] || (alg != "ES256" && alg != "RS256" && alg != "HS256") {
			return Config{}, fmt.Errorf("SUPABASE_JWT_ALGORITHMS contains an unsupported or duplicate algorithm")
		}
		seen[alg] = true
		c.Algorithms = append(c.Algorithms, alg)
	}
	secret := getenv("SUPABASE_LEGACY_JWT_SECRET")
	if legacy {
		if len(c.Algorithms) != 1 || c.Algorithms[0] != "HS256" || len(secret) < 32 {
			return Config{}, fmt.Errorf("legacy mode requires HS256 only and a secret of at least 32 bytes")
		}
		c.LegacySecret = secret
	} else if seen["HS256"] || secret != "" {
		return Config{}, fmt.Errorf("legacy HS256 must be explicitly enabled separately from asymmetric verification")
	}
	for _, field := range []struct {
		name     string
		fallback string
		maximum  time.Duration
		dst      *time.Duration
	}{
		{"JWKS_CACHE_TTL", "5m", 10 * time.Minute, &c.JWKSCacheTTL},
		{"JWKS_REFRESH_INTERVAL", "10s", time.Minute, &c.RefreshInterval},
		{"AUTH_HTTP_TIMEOUT", "5s", 30 * time.Second, &c.AuthHTTPTimeout},
		{"SHUTDOWN_TIMEOUT", "15s", time.Minute, &c.ShutdownTimeout},
	} {
		d, err := time.ParseDuration(value(getenv, field.name, field.fallback))
		if err != nil || d <= 0 || d > field.maximum {
			return Config{}, fmt.Errorf("%s must be a positive duration at most %s", field.name, field.maximum)
		}
		*field.dst = d
	}
	if c.RefreshInterval > c.JWKSCacheTTL {
		return Config{}, fmt.Errorf("JWKS_REFRESH_INTERVAL must not exceed JWKS_CACHE_TTL")
	}
	c.DatabaseURL = strings.TrimSpace(getenv("DATABASE_URL"))
	for _, field := range []struct {
		name, fallback string
		min, max       int
		dst            *int
	}{
		{"DATABASE_MAX_CONNS", "16", 2, 100, &c.DatabaseMaxConns},
		{"ROOM_WORKERS", "8", 1, 32, &c.RoomWorkers},
		{"ROOM_JOB_QUEUE", "256", 1, 4096, &c.RoomQueue},
		{"MAX_ACTIVE_ROOMS", "1000", 1, 10000, &c.MaxRooms},
		{"BCRYPT_COST", "10", 8, 12, &c.BcryptCost},
	} {
		n, err := strconv.Atoi(value(getenv, field.name, field.fallback))
		if err != nil || n < field.min || n > field.max {
			return Config{}, fmt.Errorf("%s is outside its allowed bounds", field.name)
		}
		*field.dst = n
	}
	for _, origin := range strings.Split(value(getenv, "ALLOWED_ORIGINS", "http://localhost:5173,http://127.0.0.1:5173"), ",") {
		origin = strings.TrimSpace(origin)
		u, err := url.Parse(origin)
		if err != nil || ValidateEndpoint(origin) != nil || u.Path != "" {
			return Config{}, fmt.Errorf("ALLOWED_ORIGINS must contain exact HTTPS origins or loopback development origins")
		}
		c.AllowedOrigins = append(c.AllowedOrigins, origin)
	}
	return c, nil
}

func value(getenv func(string) string, name, fallback string) string {
	if s := strings.TrimSpace(getenv(name)); s != "" {
		return s
	}
	return fallback
}

// ValidateEndpoint allows plaintext HTTP only on literal loopback hosts for local development.
func ValidateEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("must be an absolute URL without credentials, query, or fragment")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return fmt.Errorf("HTTPS is required except on loopback")
}
