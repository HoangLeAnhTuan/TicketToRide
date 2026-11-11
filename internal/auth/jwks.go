package auth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"slices"
	"sync"
	"time"

	"ticket-to-ride/internal/config"
)

type verificationKey struct {
	algorithm string
	value     any
}

// A single shared refresh bounds requests even when many unknown kids arrive.
// Published key maps are immutable. No network I/O is performed under mu.
type keyCache struct {
	mu          sync.RWMutex
	keys        map[string]verificationKey
	expires     time.Time
	nextRefresh time.Time
	refreshing  chan struct{}
	url         string
	client      *http.Client
	ttl         time.Duration
	interval    time.Duration
	now         func() time.Time
	algorithms  []string
}

func newKeyCache(o Options) (*keyCache, error) {
	if err := config.ValidateEndpoint(o.JWKSURL); err != nil {
		return nil, fmt.Errorf("JWKS endpoint: %w", err)
	}
	if o.CacheTTL <= 0 || o.CacheTTL > 10*time.Minute || o.RefreshInterval <= 0 || o.RefreshInterval > o.CacheTTL || o.HTTPTimeout <= 0 || o.HTTPTimeout > 30*time.Second {
		return nil, fmt.Errorf("invalid bounded JWKS cache or timeout configuration")
	}
	return &keyCache{
		url: o.JWKSURL, ttl: o.CacheTTL, interval: o.RefreshInterval, now: o.Now,
		algorithms: slices.Clone(o.Algorithms),
		client: &http.Client{Timeout: o.HTTPTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}, nil
}

func (k *keyCache) ready() bool {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return len(k.keys) > 0 && k.now().Before(k.expires)
}

func (k *keyCache) key(ctx context.Context, kid, algorithm string) (any, error) {
	if err := k.ensure(ctx, kid, algorithm); err != nil {
		return nil, err
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	key, ok := k.keys[kid]
	if !k.now().Before(k.expires) {
		return nil, ErrUnavailable
	}
	if !ok || key.algorithm != algorithm {
		return nil, ErrInvalidToken
	}
	return key.value, nil
}

func (k *keyCache) ensure(ctx context.Context, kid, algorithm string) error {
	for {
		k.mu.Lock()
		now := k.now()
		fresh := len(k.keys) > 0 && now.Before(k.expires)
		key, found := k.keys[kid]
		if fresh && (kid == "" || (found && key.algorithm == algorithm)) {
			k.mu.Unlock()
			return nil
		}
		if fresh && found && key.algorithm != algorithm {
			k.mu.Unlock()
			return ErrInvalidToken
		}
		if done := k.refreshing; done != nil {
			k.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return ErrUnavailable
			}
		}
		if now.Before(k.nextRefresh) {
			k.mu.Unlock()
			if fresh {
				return ErrInvalidToken
			}
			return ErrUnavailable
		}
		done := make(chan struct{})
		k.refreshing = done
		k.nextRefresh = now.Add(k.interval)
		k.mu.Unlock()

		keys, err := k.fetch(ctx)
		k.mu.Lock()
		if err == nil {
			k.keys = keys
			k.expires = k.now().Add(k.ttl)
		}
		k.nextRefresh = k.now().Add(k.interval)
		k.refreshing = nil
		close(done)
		k.mu.Unlock()
		if err != nil {
			return ErrUnavailable
		}
		// Recheck once through the normal cache path; a missing kid is throttled.
	}
}

func (k *keyCache) fetch(ctx context.Context) (map[string]verificationKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := k.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS fetch failed")
	}
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return nil, fmt.Errorf("invalid JWKS response size")
	}
	var document struct {
		Keys []jwk `json:"keys"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF || len(document.Keys) == 0 || len(document.Keys) > 32 {
		return nil, fmt.Errorf("invalid JWKS document")
	}
	keys := make(map[string]verificationKey)
	seen := make(map[string]bool)
	for _, entry := range document.Keys {
		if entry.Kid == "" || len(entry.Kid) > 256 || seen[entry.Kid] {
			return nil, fmt.Errorf("invalid or duplicate JWKS kid")
		}
		seen[entry.Kid] = true
		if entry.Use != "" && entry.Use != "sig" {
			continue
		}
		if len(entry.KeyOps) > 0 && !slices.Contains(entry.KeyOps, "verify") {
			continue
		}
		key, err := entry.publicKey()
		if err != nil {
			return nil, err
		}
		if key.value != nil && slices.Contains(k.algorithms, key.algorithm) {
			keys[entry.Kid] = key
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no supported signing keys")
	}
	return keys, nil
}

type jwk struct {
	Kid    string   `json:"kid"`
	Kty    string   `json:"kty"`
	Alg    string   `json:"alg"`
	Use    string   `json:"use"`
	KeyOps []string `json:"key_ops"`
	Crv    string   `json:"crv"`
	X      string   `json:"x"`
	Y      string   `json:"y"`
	N      string   `json:"n"`
	E      string   `json:"e"`
}

func (j jwk) publicKey() (verificationKey, error) {
	decode := base64.RawURLEncoding.Strict().DecodeString
	switch j.Kty {
	case "EC":
		if j.Crv != "P-256" || (j.Alg != "" && j.Alg != "ES256") {
			return verificationKey{}, nil
		}
		x, xerr := decode(j.X)
		y, yerr := decode(j.Y)
		if xerr != nil || yerr != nil || len(x) != 32 || len(y) != 32 {
			return verificationKey{}, fmt.Errorf("invalid EC signing key")
		}
		pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
			return verificationKey{}, fmt.Errorf("EC point not on curve")
		}
		return verificationKey{algorithm: "ES256", value: pub}, nil
	case "RSA":
		if j.Alg != "" && j.Alg != "RS256" {
			return verificationKey{}, nil
		}
		n, nerr := decode(j.N)
		e, eerr := decode(j.E)
		modulus := new(big.Int).SetBytes(n)
		if nerr != nil || eerr != nil || modulus.BitLen() < 2048 || modulus.BitLen() > 8192 || len(e) == 0 || len(e) > 4 {
			return verificationKey{}, fmt.Errorf("invalid RSA signing key")
		}
		exponent := new(big.Int).SetBytes(e).Int64()
		if exponent < 3 || exponent > 1<<31-1 || exponent%2 == 0 {
			return verificationKey{}, fmt.Errorf("invalid RSA exponent")
		}
		return verificationKey{algorithm: "RS256", value: &rsa.PublicKey{N: modulus, E: int(exponent)}}, nil
	default:
		return verificationKey{}, nil
	}
}
