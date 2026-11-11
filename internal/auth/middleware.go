package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const PrincipalKey = "auth.principal"

type TokenVerifier interface {
	Verify(context.Context, string) (Principal, error)
}

type principalContextKey struct{}

// FromContext keeps application code independent of Gin.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalContextKey{}).(Principal)
	return p, ok
}

// Middleware validates the Authorization Bearer token on requests and attaches
// the verified Principal to the Gin context.
func Middleware(v TokenVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		headers := c.Request.Header.Values("Authorization")
		if len(headers) != 1 {
			reject(c, http.StatusUnauthorized, "UNAUTHENTICATED", "A bearer access token is required.", false)
			return
		}

		parts := strings.Fields(headers[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			reject(c, http.StatusUnauthorized, "UNAUTHENTICATED", "A bearer access token is required.", false)
			return
		}

		token := parts[1]
		principal, err := v.Verify(c.Request.Context(), token)
		if err != nil {
			switch {
			case errors.Is(err, ErrExpiredToken):
				reject(c, http.StatusUnauthorized, "TOKEN_EXPIRED", "Refresh your access token and retry.", false)
			case errors.Is(err, ErrUnavailable):
				c.Header("Retry-After", "10")
				reject(c, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "Authentication is temporarily unavailable.", true)
			default:
				reject(c, http.StatusUnauthorized, "UNAUTHENTICATED", "The access token is invalid.", false)
			}
			return
		}

		c.Set(PrincipalKey, principal)
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), principalContextKey{}, principal))
		c.Next()
	}
}

func reject(c *gin.Context, status int, code, message string, retryable bool) {
	c.Header("Cache-Control", "no-store")
	if status == http.StatusUnauthorized {
		c.Header("WWW-Authenticate", `Bearer realm="ticket-to-ride"`)
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message, "retryable": retryable}, "request_id": c.GetString("request_id")})
}

// GetPrincipal retrieves the authenticated Principal from the Gin context if present.
func GetPrincipal(c *gin.Context) (Principal, bool) {
	val, exists := c.Get(PrincipalKey)
	if !exists {
		return Principal{}, false
	}
	p, ok := val.(Principal)
	return p, ok
}

// MustGetPrincipal returns the Principal from the Gin context or panics if not authenticated.
func MustGetPrincipal(c *gin.Context) Principal {
	p, ok := GetPrincipal(c)
	if !ok {
		panic("auth: principal missing from context")
	}
	return p
}
