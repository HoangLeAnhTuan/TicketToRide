// Package httpapi exposes transport DTOs; no handler can mutate game state.
package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/gin-gonic/gin"

	"ticket-to-ride/internal/auth"
	"ticket-to-ride/internal/game/catalog"
	"ticket-to-ride/internal/room"
)

type Verifier interface {
	auth.TokenVerifier
	Warm(context.Context) error
}

type Server struct {
	router             *gin.Engine
	draining           atomic.Bool
	rooms              *room.Service
	allowedOrigins     []string
	requirePersistence bool
}

type Option func(*Server)

func WithRooms(service *room.Service) Option { return func(s *Server) { s.rooms = service } }
func WithAllowedOrigins(origins []string) Option {
	return func(s *Server) { s.allowedOrigins = append([]string(nil), origins...) }
}
func RequirePersistence() Option { return func(s *Server) { s.requirePersistence = true } }

func New(cat *catalog.Catalog, verifier Verifier, logger *slog.Logger, opts ...Option) (*Server, error) {
	if cat == nil || verifier == nil || logger == nil {
		return nil, fmt.Errorf("catalog, verifier and logger are required")
	}
	s := &Server{router: gin.New()}
	for _, opt := range opts {
		opt(s)
	}
	if err := s.router.SetTrustedProxies(nil); err != nil {
		return nil, fmt.Errorf("disable implicit trusted proxies: %w", err)
	}
	s.router.Use(requestIdentity(), safeRecovery(logger))
	s.router.Use(cors(s.allowedOrigins))
	s.router.GET("/health/live", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	s.router.GET("/health/ready", func(c *gin.Context) {
		// A probe also refreshes expired trust material through the bounded cache.
		if s.draining.Load() || verifier.Warm(c.Request.Context()) != nil || (s.requirePersistence && s.rooms == nil) || (s.rooms != nil && s.rooms.Ping(c.Request.Context()) != nil) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})
	// Expose a fixed allowlist of public board fields, never the ticket catalog.
	mapJSON, err := json.Marshal(cat.SimplifiedMap())
	if err != nil {
		return nil, fmt.Errorf("encode public catalog: %w", err)
	}
	// A representation change must invalidate caches even when the board is unchanged.
	etag := fmt.Sprintf(`"%x"`, sha256.Sum256(mapJSON))
	s.router.GET("/v1/catalog/"+catalog.RulesetVersion, func(c *gin.Context) {
		c.Header("ETag", etag)
		c.Header("Cache-Control", "public, max-age=3600, must-revalidate")
		for _, tag := range strings.Split(c.GetHeader("If-None-Match"), ",") {
			candidate := strings.TrimSpace(tag)
			if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
				c.Status(http.StatusNotModified)
				return
			}
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", mapJSON)
	})
	private := s.router.Group("/v1", auth.Middleware(verifier))
	private.GET("/me", func(c *gin.Context) {
		p, ok := auth.FromContext(c.Request.Context())
		if !ok {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, p)
	})

	s.registerRooms(private, s.rooms)

	return s, nil
}

func (s *Server) Handler() http.Handler { return s.router }
func (s *Server) Drain()                { s.draining.Store(true) }

func requestIdentity() gin.HandlerFunc {
	return func(c *gin.Context) {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		id[6] = (id[6] & 0x0f) | 0x40
		id[8] = (id[8] & 0x3f) | 0x80
		value := fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
		c.Set("request_id", value)
		c.Header("X-Request-ID", value)
		c.Header("X-Content-Type-Options", "nosniff")
		c.Next()
	}
}

func safeRecovery(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recover() != nil {
				// No panic value, request headers, body, or raw URL enters logs.
				logger.Error("HTTP handler panic", "request_id", c.GetString("request_id"), "route", c.FullPath())
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "INTERNAL_ERROR", "message": "An internal error occurred.", "retryable": false}, "request_id": c.GetString("request_id")})
			}
		}()
		c.Next()
	}
}
