package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testUser = "e5843d15-181c-45a2-b3fe-7506dbcc43aa"

type authFixture struct {
	server   *httptest.Server
	verifier *Verifier
	private  *ecdsa.PrivateKey
	clock    atomic.Int64
	requests atomic.Int32
	mu       sync.RWMutex
	keys     []jwk
	status   int
}

func newFixture(t *testing.T) *authFixture {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &authFixture{private: private, keys: []jwk{ecJWK("first", private)}}
	f.clock.Store(time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC).UnixNano())
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		f.mu.RLock()
		defer f.mu.RUnlock()
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"keys": f.keys}); err != nil {
			t.Errorf("encode JWKS: %v", err)
		}
	}))
	t.Cleanup(f.server.Close)
	v, err := NewVerifier(f.options())
	if err != nil {
		t.Fatal(err)
	}
	f.verifier = v
	if err := v.Warm(context.Background()); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *authFixture) now() time.Time { return time.Unix(0, f.clock.Load()).UTC() }
func (f *authFixture) options() Options {
	return Options{Issuer: f.server.URL + "/auth/v1", Audience: "authenticated", JWKSURL: f.server.URL, Algorithms: []string{"ES256", "RS256"}, CacheTTL: 5 * time.Minute, RefreshInterval: 10 * time.Second, HTTPTimeout: 2 * time.Second, Now: f.now}
}
func (f *authFixture) claims() jwt.MapClaims {
	return jwt.MapClaims{"iss": f.server.URL + "/auth/v1", "aud": "authenticated", "sub": testUser, "role": "authenticated", "exp": f.now().Add(time.Hour).Unix(), "nbf": f.now().Add(-time.Second).Unix(), "is_anonymous": false}
}
func ecJWK(kid string, key *ecdsa.PrivateKey) jwk {
	return jwk{Kid: kid, Kty: "EC", Alg: "ES256", Use: "sig", Crv: "P-256", X: base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))), Y: base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32)))}
}
func sign(t *testing.T, method jwt.SigningMethod, key any, claims jwt.MapClaims, kid string, headers map[string]any) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	for k, v := range headers {
		token.Header[k] = v
	}
	raw, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestVerifiedIdentityNormalization(t *testing.T) {
	f := newFixture(t)
	for _, guest := range []bool{false, true} {
		claims := f.claims()
		claims["is_anonymous"] = guest
		claims["sub"] = strings.ToUpper(testUser)
		p, err := f.verifier.Verify(context.Background(), sign(t, jwt.SigningMethodES256, f.private, claims, "first", nil))
		if err != nil || p.UserID != testUser || p.IsAnonymous != guest || !p.ExpiresAt.Equal(f.now().Add(time.Hour)) {
			t.Fatalf("principal: %+v, %v", p, err)
		}
	}
	if f.requests.Load() != 1 {
		t.Fatal("cache unnecessarily refreshed")
	}
}

func TestRejectUntrustedTokenForms(t *testing.T) {
	f := newFixture(t)
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		change  func(jwt.MapClaims)
		method  jwt.SigningMethod
		key     any
		kid     string
		headers map[string]any
		want    error
	}{
		{name: "missing exp", change: func(c jwt.MapClaims) { delete(c, "exp") }},
		{name: "missing aud", change: func(c jwt.MapClaims) { delete(c, "aud") }},
		{name: "missing issuer", change: func(c jwt.MapClaims) { delete(c, "iss") }},
		{name: "missing sub", change: func(c jwt.MapClaims) { delete(c, "sub") }},
		{name: "missing role", change: func(c jwt.MapClaims) { delete(c, "role") }},
		{name: "service API token", change: func(c jwt.MapClaims) { c["role"] = "service_role" }},
		{name: "future nbf", change: func(c jwt.MapClaims) { c["nbf"] = f.now().Add(time.Minute).Unix() }},
		{name: "expired", change: func(c jwt.MapClaims) { c["exp"] = f.now().Add(-time.Minute).Unix() }, want: ErrExpiredToken},
		{name: "wrong signature", key: other},
		{name: "none", method: jwt.SigningMethodNone, key: jwt.UnsafeAllowNoneSignatureType},
		{name: "HMAC confusion", method: jwt.SigningMethodHS256, key: []byte("public-key-as-secret")},
		{name: "missing kid", kid: "-"},
		{name: "unknown kid", kid: "untrusted"},
		{name: "key URL", headers: map[string]any{"jku": "http://attacker.invalid/jwks"}},
		{name: "certificate URL", headers: map[string]any{"x5u": "http://attacker.invalid/key"}},
		{name: "embedded key", headers: map[string]any{"jwk": ecJWK("first", other)}},
		{name: "critical header", headers: map[string]any{"crit": []string{"b64"}}},
		{name: "bad anonymous claim", change: func(c jwt.MapClaims) { c["is_anonymous"] = "true" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims := f.claims()
			if tc.change != nil {
				tc.change(claims)
			}
			method := tc.method
			if method == nil {
				method = jwt.SigningMethodES256
			}
			key := tc.key
			if key == nil {
				key = f.private
			}
			kid := tc.kid
			if kid == "" {
				kid = "first"
			}
			if kid == "-" {
				kid = ""
			}
			want := tc.want
			if want == nil {
				want = ErrInvalidToken
			}
			if _, err := f.verifier.Verify(context.Background(), sign(t, method, key, claims, kid, tc.headers)); !errors.Is(err, want) {
				t.Fatalf("got %v; want %v", err, want)
			}
		})
	}
	for _, raw := range []string{"", "not.a.jwt", strings.Repeat("x", 16*1024+1)} {
		if _, err := f.verifier.Verify(context.Background(), raw); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("malformed token: %v", err)
		}
	}
}

func TestConcurrentRefreshAndUnknownKeyThrottling(t *testing.T) {
	f := newFixture(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.keys = []jwk{ecJWK("rotated", key)}
	f.mu.Unlock()
	f.clock.Add(int64(11 * time.Second))
	raw := sign(t, jwt.SigningMethodES256, key, f.claims(), "rotated", nil)
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.verifier.Verify(context.Background(), raw); err != nil {
				t.Errorf("rotated key: %v", err)
			}
		}()
	}
	wg.Wait()
	if f.requests.Load() != 2 {
		t.Fatalf("refresh not single flight: %d", f.requests.Load())
	}
	unknown := sign(t, jwt.SigningMethodES256, key, f.claims(), "never-trusted", nil)
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.verifier.Verify(context.Background(), unknown); !errors.Is(err, ErrInvalidToken) {
				t.Errorf("unknown key: %v", err)
			}
		}()
	}
	wg.Wait()
	if f.requests.Load() != 2 {
		t.Fatal("unknown-kid flood caused network requests")
	}
	wrongAlg := sign(t, jwt.SigningMethodES256, key, f.claims(), "rotated", nil)
	if _, err := f.verifier.keys.key(context.Background(), "rotated", "RS256"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("key algorithm not bound: %v", err)
	}
	if _, err := f.verifier.Verify(context.Background(), wrongAlg); err != nil {
		t.Fatal(err)
	}
	old := sign(t, jwt.SigningMethodES256, f.private, f.claims(), "first", nil)
	if _, err := f.verifier.Verify(context.Background(), old); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("retired key accepted: %v", err)
	}
}

func TestExpiredTrustFailsClosedAndRecovers(t *testing.T) {
	f := newFixture(t)
	raw := sign(t, jwt.SigningMethodES256, f.private, f.claims(), "first", nil)
	f.mu.Lock()
	f.status = 503
	f.mu.Unlock()
	f.clock.Add(int64(6 * time.Minute))
	for range 10 {
		if _, err := f.verifier.Verify(context.Background(), raw); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("stale trust accepted: %v", err)
		}
	}
	if f.verifier.Ready() || f.requests.Load() != 2 {
		t.Fatal("expiry/backoff was not enforced")
	}
	f.mu.Lock()
	f.status = 0
	f.mu.Unlock()
	f.clock.Add(int64(11 * time.Second))
	if _, err := f.verifier.Verify(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
}

func TestCancelledWaiterDoesNotAbortSharedRefresh(t *testing.T) {
	f := newFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"keys": []jwk{ecJWK("first", f.private)}}); err != nil {
			t.Errorf("JWKS: %v", err)
		}
	}))
	defer server.Close()
	o := f.options()
	o.JWKSURL = server.URL
	v, err := NewVerifier(o)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- v.Warm(ctx) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("refresh never started")
	}
	stopped, stop := context.WithCancel(context.Background())
	stop()
	raw := sign(t, jwt.SigningMethodES256, f.private, f.claims(), "first", nil)
	_, verifyErr := v.Verify(stopped, raw)
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("waiter cancelled shared refresh: %v", err)
	}
	if !errors.Is(verifyErr, ErrUnavailable) {
		t.Fatalf("cancelled waiter: %v", verifyErr)
	}
	if _, err := v.Verify(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedJWKSAndRedirectsFailClosed(t *testing.T) {
	f := newFixture(t)
	valid := ecJWK("first", f.private)
	duplicate, err := json.Marshal(map[string]any{"keys": []jwk{valid, valid}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		redirect   bool
	}{
		{"duplicate kid", string(duplicate), false}, {"empty", `{"keys":[]}`, false},
		{"bad point", `{"keys":[{"kid":"bad","kty":"EC","crv":"P-256","x":"bad","y":"bad"}]}`, false},
		{"trailing", string(duplicate) + `{}`, false}, {"oversized", strings.Repeat(" ", (1<<20)+1), false}, {name: "redirect", redirect: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.redirect {
					http.Redirect(w, r, f.server.URL, 302)
					return
				}
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Errorf("write fixture: %v", err)
				}
			}))
			defer server.Close()
			o := f.options()
			o.JWKSURL = server.URL
			v, err := NewVerifier(o)
			if err != nil {
				t.Fatal(err)
			}
			if err := v.Warm(context.Background()); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("untrusted document accepted: %v", err)
			}
		})
	}
}

func FuzzVerifyDoesNotPanic(f *testing.F) {
	v, err := NewVerifier(Options{Issuer: "https://example.supabase.co/auth/v1", Audience: "authenticated", Algorithms: []string{"HS256"}, LegacySecret: strings.Repeat("s", 32)})
	if err != nil {
		f.Fatal(err)
	}
	f.Add("not-a-token")
	f.Add("a.b.c")
	f.Add("")
	f.Fuzz(func(t *testing.T, raw string) { _, _ = v.Verify(context.Background(), raw) })
}
