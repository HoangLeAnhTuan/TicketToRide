// Package auth verifies Supabase user access tokens without trusting client identities.
package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"ticket-to-ride/internal/config"
)

var (
	ErrInvalidToken = errors.New("invalid access token")
	ErrExpiredToken = errors.New("expired access token")
	ErrUnavailable  = errors.New("authentication keys unavailable")
	uuidPattern     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

type Principal struct {
	UserID      string    `json:"user_id"`
	IsAnonymous bool      `json:"is_anonymous"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type Options struct {
	Issuer          string
	Audience        string
	JWKSURL         string
	Algorithms      []string
	LegacySecret    string
	CacheTTL        time.Duration
	RefreshInterval time.Duration
	HTTPTimeout     time.Duration
	// Now is injected for deterministic expiry/cache tests. Production uses time.Now.
	Now func() time.Time
}

type Verifier struct {
	parser     *jwt.Parser
	keys       *keyCache
	legacy     []byte
	algorithms []string
}

type accessClaims struct {
	jwt.RegisteredClaims
	Role      string `json:"role"`
	Anonymous bool   `json:"is_anonymous"`
}

func NewVerifier(o Options) (*Verifier, error) {
	if err := config.ValidateEndpoint(o.Issuer); err != nil {
		return nil, fmt.Errorf("issuer: %w", err)
	}
	if o.Audience != "authenticated" || len(o.Algorithms) == 0 {
		return nil, fmt.Errorf("authenticated audience and algorithms are required")
	}
	for i, alg := range o.Algorithms {
		if (alg != "ES256" && alg != "RS256" && alg != "HS256") || slices.Contains(o.Algorithms[:i], alg) {
			return nil, fmt.Errorf("unsupported or duplicate JWT algorithm")
		}
	}
	legacy := slices.Contains(o.Algorithms, "HS256")
	if (legacy && (len(o.Algorithms) != 1 || len(o.LegacySecret) < 32)) || (!legacy && o.LegacySecret != "") {
		return nil, fmt.Errorf("HS256 requires explicit isolated legacy configuration")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	v := &Verifier{algorithms: slices.Clone(o.Algorithms)}
	v.parser = jwt.NewParser(jwt.WithValidMethods(v.algorithms), jwt.WithIssuer(o.Issuer),
		jwt.WithAudience(o.Audience), jwt.WithExpirationRequired(), jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(o.Now), jwt.WithStrictDecoding())
	if legacy {
		v.legacy = []byte(o.LegacySecret)
	} else {
		var err error
		v.keys, err = newKeyCache(o)
		if err != nil {
			return nil, err
		}
	}
	return v, nil
}

// Warm loads initial trust material before the HTTP listener becomes ready.
func (v *Verifier) Warm(ctx context.Context) error {
	if v.keys == nil {
		return nil
	}
	return v.keys.ensure(ctx, "", "")
}

func (v *Verifier) Ready() bool {
	return v.keys == nil || v.keys.ready()
}

func (v *Verifier) Verify(ctx context.Context, raw string) (Principal, error) {
	if len(raw) == 0 || len(raw) > 16*1024 {
		return Principal{}, ErrInvalidToken
	}
	claims := &accessClaims{}
	token, err := v.parser.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		// Never follow a key URL or accept embedded verification material from a JWT.
		for _, name := range []string{"jku", "x5u", "jwk", "crit"} {
			if _, exists := t.Header[name]; exists {
				return nil, ErrInvalidToken
			}
		}
		if v.keys == nil {
			return v.legacy, nil
		}
		kid, ok := t.Header["kid"].(string)
		if !ok || kid == "" || len(kid) > 256 {
			return nil, ErrInvalidToken
		}
		return v.keys.key(ctx, kid, t.Method.Alg())
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrUnavailable):
			return Principal{}, ErrUnavailable
		case errors.Is(err, jwt.ErrTokenExpired) && !errors.Is(err, jwt.ErrTokenSignatureInvalid):
			return Principal{}, ErrExpiredToken
		default:
			return Principal{}, ErrInvalidToken
		}
	}
	if !token.Valid || claims.Role != "authenticated" || !uuidPattern.MatchString(claims.Subject) || claims.Subject == "00000000-0000-0000-0000-000000000000" {
		return Principal{}, ErrInvalidToken
	}
	return Principal{UserID: strings.ToLower(claims.Subject), IsAnonymous: claims.Anonymous, ExpiresAt: claims.ExpiresAt.Time}, nil
}
