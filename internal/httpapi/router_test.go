package httpapi

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"ticket-to-ride/internal/auth"
	"ticket-to-ride/internal/game/catalog"
)

func TestHTTPWithRealJWTVerification(t *testing.T) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := map[string]any{"kid": "test-key", "kty": "EC", "crv": "P-256", "alg": "ES256", "use": "sig", "x": base64.RawURLEncoding.EncodeToString(private.X.FillBytes(make([]byte, 32))), "y": base64.RawURLEncoding.EncodeToString(private.Y.FillBytes(make([]byte, 32)))}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{"keys": []any{key}}); err != nil {
			t.Errorf("JWKS: %v", err)
		}
	}))
	defer jwks.Close()
	v, err := auth.NewVerifier(auth.Options{Issuer: jwks.URL + "/auth/v1", Audience: "authenticated", JWKSURL: jwks.URL, Algorithms: []string{"ES256"}, CacheTTL: time.Minute, RefreshInterval: time.Second, HTTPTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Warm(context.Background()); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	api, err := New(cat, v, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	get := func(path, token, etag string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := resp.Body.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
		})
		return resp
	}
	for _, path := range []string{"/health/live", "/health/ready"} {
		if resp := get(path, "", ""); resp.StatusCode != 200 {
			t.Fatalf("%s returned %d", path, resp.StatusCode)
		}
	}
	resp := get("/v1/catalog/europe-core-v1", "", "")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || strings.Contains(string(body), `"tickets"`) || strings.Contains(string(body), `"points"`) {
		t.Fatalf("unsafe catalog response: %d", resp.StatusCode)
	}
	var board struct {
		Cities []catalog.City  `json:"cities"`
		Routes []catalog.Route `json:"routes"`
		Rules  map[string]bool `json:"rules"`
	}
	if err := json.Unmarshal(body, &board); err != nil {
		t.Fatal(err)
	}
	if len(board.Cities) != 47 || len(board.Routes) != 101 || len(board.Rules) != 3 || board.Rules["tunnels"] || board.Rules["ferries"] || board.Rules["stations"] {
		t.Fatal("incorrect public ruleset")
	}
	wantMap, err := json.Marshal(cat.SimplifiedMap())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, wantMap) {
		t.Fatal("HTTP catalog diverges from the canonical public projection")
	}
	if resp.Header.Get("ETag") != fmt.Sprintf(`"%x"`, sha256.Sum256(body)) {
		t.Fatal("ETag does not identify the response bytes")
	}
	if cached := get("/v1/catalog/europe-core-v1", "", `W/`+resp.Header.Get("ETag")); cached.StatusCode != 304 {
		t.Fatal("ETag not honored")
	}
	for _, token := range []string{"", "garbage"} {
		resp := get("/v1/me", token, "")
		if resp.StatusCode != 401 || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("invalid auth response %d", resp.StatusCode)
		}
	}
	for _, headers := range [][]string{{"Bearer"}, {"Basic credentials"}, {"Bearer one extra"}, {"Bearer one", "Bearer two"}} {
		req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
		for _, value := range headers {
			req.Header.Add("Authorization", value)
		}
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("malformed Authorization accepted: %v", headers)
		}
		var envelope struct {
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.RequestID == "" || envelope.RequestID != w.Header().Get("X-Request-ID") {
			t.Fatal("error lacks consistent request identity")
		}
	}
	for _, anonymous := range []bool{false, true} {
		claims := jwt.MapClaims{"iss": jwks.URL + "/auth/v1", "aud": "authenticated", "sub": "27f3f2d3-cf58-481a-8d0a-87d2ff3cd52b", "exp": time.Now().Add(time.Hour).Unix(), "role": "authenticated", "is_anonymous": anonymous}
		token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
		token.Header["kid"] = "test-key"
		raw, err := token.SignedString(private)
		if err != nil {
			t.Fatal(err)
		}
		resp := get("/v1/me", raw, "")
		if resp.StatusCode != 200 {
			t.Fatalf("authenticated request failed: %d", resp.StatusCode)
		}
		var p auth.Principal
		if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		if p.UserID != claims["sub"] || p.IsAnonymous != anonymous {
			t.Fatalf("wrong principal: %+v", p)
		}
		if strings.Contains(logs.String(), raw) {
			t.Fatal("access token leaked to logs")
		}
	}
	api.Drain()
	if resp := get("/health/ready", "", ""); resp.StatusCode != 503 {
		t.Fatal("draining server remains ready")
	}
	if resp := get("/health/live", "", ""); resp.StatusCode != 200 {
		t.Fatal("draining server not live")
	}
}

type failingVerifier struct{}

func (failingVerifier) Warm(context.Context) error { return auth.ErrUnavailable }
func (failingVerifier) Verify(context.Context, string) (auth.Principal, error) {
	return auth.Principal{}, auth.ErrUnavailable
}

func TestUnavailableAuthIsNotAnUnauthorizedUser(t *testing.T) {
	cat, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	api, err := New(cat, failingVerifier{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/health/ready", "/v1/me"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer token")
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, req)
		if w.Code != 503 {
			t.Fatalf("%s = %d", path, w.Code)
		}
	}
}

func TestRecoveryDoesNotLogSensitivePanic(t *testing.T) {
	cat, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	api, err := New(cat, failingVerifier{}, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	api.router.GET("/panic", func(_ *gin.Context) { panic("secret-token-in-panic") })
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/panic?password=secret-password", nil))
	if w.Code != 500 || strings.Contains(logs.String(), "secret-") || strings.Contains(w.Body.String(), "secret-") {
		t.Fatal("panic details leaked")
	}
}
