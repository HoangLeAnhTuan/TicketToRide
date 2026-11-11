package auth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"ticket-to-ride/internal/auth"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func generateECKey(kid string) (*ecdsa.PrivateKey, string, map[string]any) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	x := key.PublicKey.X.Bytes()
	y := key.PublicKey.Y.Bytes()
	// Pad if needed to 32 bytes
	if len(x) < 32 {
		pad := make([]byte, 32-len(x))
		x = append(pad, x...)
	}
	if len(y) < 32 {
		pad := make([]byte, 32-len(y))
		y = append(pad, y...)
	}

	jwk := map[string]any{
		"kty": "EC",
		"crv": "P-256",
		"x":   base64.RawURLEncoding.EncodeToString(x),
		"y":   base64.RawURLEncoding.EncodeToString(y),
		"kid": kid,
		"alg": "ES256",
		"use": "sig",
	}
	return key, kid, jwk
}

func generateRSAKey() (*rsa.PrivateKey, string, map[string]any) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	kid := "test-rsa-kid-1"
	n := key.PublicKey.N.Bytes()
	e := []byte{1, 0, 1} // 65537

	jwk := map[string]any{
		"kty": "RSA",
		"n":   base64.RawURLEncoding.EncodeToString(n),
		"e":   base64.RawURLEncoding.EncodeToString(e),
		"kid": kid,
		"alg": "RS256",
		"use": "sig",
	}
	return key, kid, jwk
}

func makeToken(signingKey any, method jwt.SigningMethod, kid, issuer, aud, sub, role string, isAnon bool, exp time.Time) string {
	claims := jwt.MapClaims{
		"iss":          issuer,
		"aud":          aud,
		"sub":          sub,
		"role":         role,
		"is_anonymous": isAnon,
		"exp":          exp.Unix(),
		"iat":          exp.Add(-time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(method, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	s, err := token.SignedString(signingKey)
	if err != nil {
		panic(err)
	}
	return s
}

func TestVerifierAndMiddleware(t *testing.T) {
	t.Parallel()

	ecKey, ecKid, ecJWK := generateECKey("test-ec-kid-1")
	rsaKey, rsaKid, rsaJWK := generateRSAKey()

	var handlerMu sync.RWMutex
	var jwksHandler http.HandlerFunc = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{ecJWK, rsaJWK},
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/v1/.well-known/jwks.json" {
			handlerMu.RLock()
			defer handlerMu.RUnlock()
			jwksHandler(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	issuer := server.URL + "/auth/v1"
	now := time.Now()

	opts := auth.Options{
		Issuer:          issuer,
		Audience:        "authenticated",
		JWKSURL:         issuer + "/.well-known/jwks.json",
		Algorithms:      []string{"ES256", "RS256"},
		CacheTTL:        5 * time.Minute,
		RefreshInterval: 5 * time.Second,
		HTTPTimeout:     2 * time.Second,
		Now:             func() time.Time { return now },
	}

	verifier, err := auth.NewVerifier(opts)
	if err != nil {
		t.Fatalf("failed to create verifier: %v", err)
	}

	// Warm the cache
	if err := verifier.Warm(context.Background()); err != nil {
		t.Fatalf("failed to warm verifier: %v", err)
	}
	if !verifier.Ready() {
		t.Fatal("expected verifier to be ready after warm")
	}

	validUserID := "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"

	t.Run("valid ES256 email user token", func(t *testing.T) {
		tok := makeToken(ecKey, jwt.SigningMethodES256, ecKid, issuer, "authenticated", validUserID, "authenticated", false, now.Add(time.Hour))
		p, err := verifier.Verify(context.Background(), tok)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.UserID != validUserID || p.IsAnonymous || !p.ExpiresAt.Equal(now.Add(time.Hour).Truncate(time.Second)) {
			t.Fatalf("unexpected principal: %+v", p)
		}
	})

	t.Run("valid RS256 anonymous guest token", func(t *testing.T) {
		tok := makeToken(rsaKey, jwt.SigningMethodRS256, rsaKid, issuer, "authenticated", validUserID, "authenticated", true, now.Add(time.Hour))
		p, err := verifier.Verify(context.Background(), tok)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.UserID != validUserID || !p.IsAnonymous {
			t.Fatalf("unexpected principal: %+v", p)
		}
	})

	t.Run("expired token returns ErrExpiredToken", func(t *testing.T) {
		tok := makeToken(ecKey, jwt.SigningMethodES256, ecKid, issuer, "authenticated", validUserID, "authenticated", false, now.Add(-time.Minute))
		_, err := verifier.Verify(context.Background(), tok)
		if err != auth.ErrExpiredToken {
			t.Fatalf("expected ErrExpiredToken, got %v", err)
		}
	})

	t.Run("wrong issuer rejected", func(t *testing.T) {
		tok := makeToken(ecKey, jwt.SigningMethodES256, ecKid, "https://wrong-project.supabase.co/auth/v1", "authenticated", validUserID, "authenticated", false, now.Add(time.Hour))
		_, err := verifier.Verify(context.Background(), tok)
		if err != auth.ErrInvalidToken {
			t.Fatalf("expected ErrInvalidToken, got %v", err)
		}
	})

	t.Run("wrong audience rejected", func(t *testing.T) {
		tok := makeToken(ecKey, jwt.SigningMethodES256, ecKid, issuer, "anon", validUserID, "authenticated", false, now.Add(time.Hour))
		_, err := verifier.Verify(context.Background(), tok)
		if err != auth.ErrInvalidToken {
			t.Fatalf("expected ErrInvalidToken, got %v", err)
		}
	})

	t.Run("wrong role rejected", func(t *testing.T) {
		tok := makeToken(ecKey, jwt.SigningMethodES256, ecKid, issuer, "authenticated", validUserID, "anon", false, now.Add(time.Hour))
		_, err := verifier.Verify(context.Background(), tok)
		if err != auth.ErrInvalidToken {
			t.Fatalf("expected ErrInvalidToken, got %v", err)
		}
	})

	t.Run("nil UUID subject rejected", func(t *testing.T) {
		tok := makeToken(ecKey, jwt.SigningMethodES256, ecKid, issuer, "authenticated", "00000000-0000-0000-0000-000000000000", "authenticated", false, now.Add(time.Hour))
		_, err := verifier.Verify(context.Background(), tok)
		if err != auth.ErrInvalidToken {
			t.Fatalf("expected ErrInvalidToken, got %v", err)
		}
	})

	t.Run("non-UUID subject rejected", func(t *testing.T) {
		tok := makeToken(ecKey, jwt.SigningMethodES256, ecKid, issuer, "authenticated", "admin_user", "authenticated", false, now.Add(time.Hour))
		_, err := verifier.Verify(context.Background(), tok)
		if err != auth.ErrInvalidToken {
			t.Fatalf("expected ErrInvalidToken, got %v", err)
		}
	})

	t.Run("unknown kid triggers fetch or failure", func(t *testing.T) {
		tok := makeToken(ecKey, jwt.SigningMethodES256, "unknown-kid", issuer, "authenticated", validUserID, "authenticated", false, now.Add(time.Hour))
		_, err := verifier.Verify(context.Background(), tok)
		if err != auth.ErrInvalidToken {
			t.Fatalf("expected ErrInvalidToken, got %v", err)
		}
	})

	t.Run("forged signature rejected", func(t *testing.T) {
		otherKey, _, _ := generateECKey("forged-key")
		tok := makeToken(otherKey, jwt.SigningMethodES256, ecKid, issuer, "authenticated", validUserID, "authenticated", false, now.Add(time.Hour))
		_, err := verifier.Verify(context.Background(), tok)
		if err != auth.ErrInvalidToken {
			t.Fatalf("expected ErrInvalidToken, got %v", err)
		}
	})

	// Test Gin Middleware integration
	router := gin.New()
	router.Use(auth.Middleware(verifier))
	router.GET("/test-protected", func(c *gin.Context) {
		p := auth.MustGetPrincipal(c)
		contextPrincipal, ok := auth.FromContext(c.Request.Context())
		if !ok || contextPrincipal != p {
			t.Error("Gin and standard context identities diverged")
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"user_id":      p.UserID,
			"is_anonymous": p.IsAnonymous,
		})
	})

	t.Run("middleware accepts valid token", func(t *testing.T) {
		tok := makeToken(ecKey, jwt.SigningMethodES256, ecKid, issuer, "authenticated", validUserID, "authenticated", false, now.Add(time.Hour))
		req := httptest.NewRequest("GET", "/test-protected", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), validUserID) {
			t.Fatalf("expected body to contain user ID: %s", w.Body.String())
		}
	})

	t.Run("middleware rejects missing header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test-protected", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}
	})

	t.Run("legacy HS256 verifier", func(t *testing.T) {
		legacySecret := "very-long-legacy-secret-for-hs256-testing-must-be-32-bytes"
		hOpts := auth.Options{
			Issuer:       issuer,
			Audience:     "authenticated",
			Algorithms:   []string{"HS256"},
			LegacySecret: legacySecret,
			Now:          func() time.Time { return now },
		}
		hVerifier, err := auth.NewVerifier(hOpts)
		if err != nil {
			t.Fatalf("failed to create HS256 verifier: %v", err)
		}
		tok := makeToken([]byte(legacySecret), jwt.SigningMethodHS256, "", issuer, "authenticated", validUserID, "authenticated", false, now.Add(time.Hour))
		p, err := hVerifier.Verify(context.Background(), tok)
		if err != nil {
			t.Fatalf("unexpected HS256 verification error: %v", err)
		}
		if p.UserID != validUserID {
			t.Fatalf("unexpected user ID: %s", p.UserID)
		}
	})

	t.Run("key rotation handles new keys on unknown kid after refresh interval", func(t *testing.T) {
		newECKey, newECKid, newECJWK := generateECKey("test-ec-kid-rotated-2")
		// Advance mock clock past refresh interval so throttle allows refresh
		now = now.Add(10 * time.Second)
		// Update JWKS handler to return both old and new keys
		handlerMu.Lock()
		jwksHandler = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]any{ecJWK, rsaJWK, newECJWK},
			})
		}
		handlerMu.Unlock()
		// Token signed with new key
		tok := makeToken(newECKey, jwt.SigningMethodES256, newECKid, issuer, "authenticated", validUserID, "authenticated", false, now.Add(time.Hour))
		p, err := verifier.Verify(context.Background(), tok)
		if err != nil {
			t.Fatalf("unexpected error verifying rotated key token: %v", err)
		}
		if p.UserID != validUserID {
			t.Fatalf("unexpected user ID: %s", p.UserID)
		}
	})
}
