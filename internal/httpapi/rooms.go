package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"ticket-to-ride/internal/auth"
	"ticket-to-ride/internal/room"
)

func cors(origins []string) gin.HandlerFunc {
	allowed := map[string]bool{}
	for _, origin := range origins {
		allowed[origin] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			c.Header("Vary", "Origin")
			if !allowed[origin] {
				roomError(c, room.Failure("FORBIDDEN"))
				return
			}
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Expose-Headers", "ETag, X-Request-ID, Retry-After")
			if c.Request.Method == http.MethodOptions {
				c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
				c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, If-None-Match")
				c.Header("Access-Control-Max-Age", "600")
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
		}
		c.Next()
	}
}

type bucket struct {
	until time.Time
	count int
}
type limiter struct {
	mu      sync.Mutex
	buckets map[string]bucket
	cleanup time.Time
}

func (l *limiter) allow(key string, limit int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if !now.Before(l.cleanup) {
		for k, b := range l.buckets {
			if !b.until.After(now) {
				delete(l.buckets, k)
			}
		}
		l.cleanup = now.Add(time.Minute)
	}
	b, exists := l.buckets[key]
	if !exists && len(l.buckets) >= 10000 {
		return false
	}
	if !b.until.After(now) {
		b = bucket{until: now.Add(time.Minute)}
	}
	if b.count >= limit {
		return false
	}
	b.count++
	l.buckets[key] = b
	return true
}
func (s *Server) registerRooms(group *gin.RouterGroup, service *room.Service) {
	l := &limiter{buckets: map[string]bucket{}}
	routes := group.Group("/rooms")
	routes.Use(func(c *gin.Context) {
		if service == nil || s.draining.Load() {
			roomError(c, room.Failure("PERSISTENCE_UNAVAILABLE"))
			return
		}
		p, ok := auth.FromContext(c.Request.Context())
		if !ok {
			roomError(c, room.Failure("UNAUTHENTICATED"))
			return
		}
		if !l.allow("user:"+p.UserID, 120) || !l.allow("ip:"+c.ClientIP(), 300) {
			roomError(c, room.Failure("RATE_LIMITED"))
			return
		}
		if c.Request.Method == "POST" && (c.FullPath() == "/v1/rooms" || strings.HasSuffix(c.FullPath(), "/join")) {
			if !l.allow("password:"+c.ClientIP(), 5) || !l.allow("password-user:"+p.UserID, 10) {
				roomError(c, room.Failure("RATE_LIMITED"))
				return
			}
		}
		c.Header("Cache-Control", "no-store")
		c.Next()
	})
	routes.GET("", func(c *gin.Context) {
		if status := c.Query("status"); status != "" && status != "WAITING" {
			roomError(c, room.Failure("INVALID_MESSAGE"))
			return
		}
		limit := 50
		if value := c.Query("limit"); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil {
				roomError(c, room.Failure("INVALID_MESSAGE"))
				return
			}
			limit = n
		}
		page, err := service.List(c.Request.Context(), c.Query("cursor"), limit)
		if err != nil {
			roomError(c, err)
			return
		}
		c.JSON(200, page)
	})
	routes.GET("/by-code/:code", func(c *gin.Context) {
		r, err := service.ByCode(c.Request.Context(), c.Param("code"))
		if err != nil {
			roomError(c, err)
			return
		}
		c.JSON(200, r)
	})
	routes.POST("", func(c *gin.Context) {
		var body struct {
			RoomID      string `json:"room_id"`
			Name        string `json:"name"`
			MaxPlayers  int    `json:"max_players"`
			Password    string `json:"password"`
			DisplayName string `json:"display_name"`
		}
		if !bodyJSON(c, &body) {
			return
		}
		cmd, ok := clientCommand(c, "CREATE", strings.ToLower(body.RoomID))
		if !ok {
			return
		}
		cmd.Name = body.Name
		cmd.MaxPlayers = body.MaxPlayers
		cmd.Password = body.Password
		cmd.DisplayName = body.DisplayName
		replyCommand(c, service, cmd, 201)
	})
	routes.POST("/:id/join", func(c *gin.Context) {
		var body struct {
			Password    string `json:"password"`
			DisplayName string `json:"display_name"`
		}
		if !bodyJSON(c, &body) {
			return
		}
		cmd, ok := clientCommand(c, "JOIN", strings.ToLower(c.Param("id")))
		if !ok {
			return
		}
		cmd.Password = body.Password
		cmd.DisplayName = body.DisplayName
		replyCommand(c, service, cmd, 200)
	})
	routes.POST("/:id/leave", func(c *gin.Context) {
		var empty struct{}
		if !bodyJSON(c, &empty) {
			return
		}
		cmd, ok := clientCommand(c, "LEAVE", strings.ToLower(c.Param("id")))
		if !ok {
			return
		}
		replyCommand(c, service, cmd, 200)
	})
	routes.PATCH("/:id/player", func(c *gin.Context) {
		var body struct {
			ExpectedVersion int64   `json:"expected_version"`
			Color           *string `json:"color"`
			Ready           *bool   `json:"is_ready"`
			DisplayName     *string `json:"display_name"`
		}
		if !bodyJSON(c, &body) {
			return
		}
		count := 0
		kind := ""
		if body.Color != nil {
			count++
			kind = "SET_COLOR"
		}
		if body.Ready != nil {
			count++
			kind = "SET_READY"
		}
		if body.DisplayName != nil {
			count++
			kind = "SET_DISPLAY_NAME"
		}
		if count != 1 || body.ExpectedVersion < 1 {
			roomError(c, room.Failure("INVALID_MESSAGE"))
			return
		}
		cmd, ok := clientCommand(c, kind, strings.ToLower(c.Param("id")))
		if !ok {
			return
		}
		cmd.ExpectedVersion = body.ExpectedVersion
		if body.Color != nil {
			cmd.Color = *body.Color
		}
		if body.Ready != nil {
			cmd.Ready = *body.Ready
		}
		if body.DisplayName != nil {
			cmd.DisplayName = *body.DisplayName
		}
		replyCommand(c, service, cmd, 200)
	})
	routes.GET("/:id/state", func(c *gin.Context) {
		p, _ := auth.FromContext(c.Request.Context())
		v, err := service.View(c.Request.Context(), strings.ToLower(c.Param("id")), p.UserID)
		if err != nil {
			roomError(c, err)
			return
		}
		c.JSON(200, v)
	})
	routes.GET("/:id/commands/:request", func(c *gin.Context) {
		p, _ := auth.FromContext(c.Request.Context())
		r, err := service.Receipt(c.Request.Context(), strings.ToLower(c.Param("id")), p.UserID, strings.ToLower(c.Param("request")))
		if err != nil {
			roomError(c, err)
			return
		}
		c.JSON(200, r)
	})
	routes.POST("/:id/ws-ticket", func(c *gin.Context) {
		var empty struct{}
		if !bodyJSON(c, &empty) {
			return
		}
		p, _ := auth.FromContext(c.Request.Context())
		ticket, err := service.IssueTicket(c.Request.Context(), strings.ToLower(c.Param("id")), p.UserID, p.ExpiresAt)
		if err != nil {
			roomError(c, err)
			return
		}
		c.JSON(200, ticket)
	})
}
func clientCommand(c *gin.Context, kind, id string) (room.Command, bool) {
	p, _ := auth.FromContext(c.Request.Context())
	key := strings.ToLower(strings.TrimSpace(c.GetHeader("Idempotency-Key")))
	if !room.ValidID(key) || !room.ValidID(id) {
		roomError(c, room.Failure("INVALID_MESSAGE"))
		return room.Command{}, false
	}
	return room.Command{Kind: kind, RoomID: id, UserID: p.UserID, RequestID: key, ExpiresAt: p.ExpiresAt}, true
}
func replyCommand(c *gin.Context, s *room.Service, cmd room.Command, status int) {
	out, err := s.Do(c.Request.Context(), cmd)
	if err != nil {
		roomError(c, err)
		return
	}
	if out.Receipt != nil && out.Receipt.Duplicate {
		status = 200
	}
	c.JSON(status, out)
}
func bodyJSON(c *gin.Context, dst any) bool {
	if c.ContentType() != "application/json" {
		roomError(c, room.Failure("INVALID_MESSAGE"))
		return false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil || d.Decode(new(any)) != io.EOF {
		roomError(c, room.Failure("INVALID_MESSAGE"))
		return false
	}
	return true
}
func roomError(c *gin.Context, err error) {
	var e *room.Error
	if !errors.As(err, &e) {
		e = room.Failure("PERSISTENCE_UNAVAILABLE")
	}
	status := 409
	switch e.Code {
	case "INVALID_MESSAGE", "INVALID_CONFIGURATION":
		status = 400
	case "TOKEN_EXPIRED", "UNAUTHENTICATED":
		status = 401
	case "NOT_MEMBER", "FORBIDDEN", "INVALID_PASSWORD":
		status = 403
	case "ROOM_NOT_FOUND":
		status = 404
	case "RATE_LIMITED":
		status = 429
	case "SERVER_BUSY", "PERSISTENCE_UNAVAILABLE", "OWNER_LOST", "COMMIT_UNCERTAIN", "INVALID_SNAPSHOT", "INCOMPATIBLE_SNAPSHOT":
		status = 503
	}
	if status == 429 || status == 503 {
		c.Header("Retry-After", "10")
	}
	c.Header("Cache-Control", "no-store")
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": e.Code, "message": e.Message, "retryable": e.Retryable || status == 429 || status == 503}, "request_id": c.GetString("request_id")})
}
