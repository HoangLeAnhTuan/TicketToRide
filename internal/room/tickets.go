package room

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"time"
)

type Admission struct {
	UserID, RoomID string
	ExpiresAt      time.Time
}
type Ticket struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Protocol  string    `json:"protocol"`
}
type Tickets struct {
	mu    sync.Mutex
	items map[[32]byte]Admission
	max   int
	now   func() time.Time
}

func NewTickets(max int, now func() time.Time) *Tickets {
	return &Tickets{items: map[[32]byte]Admission{}, max: max, now: now}
}
func (t *Tickets) issue(user, id string, expiry time.Time) (Ticket, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for key, a := range t.items {
		if !a.ExpiresAt.After(now) {
			delete(t.items, key)
		}
	}
	if !expiry.After(now) {
		return Ticket{}, Failure("TOKEN_EXPIRED")
	}
	if len(t.items) >= t.max {
		return Ticket{}, Failure("SERVER_BUSY")
	}
	until := now.Add(30 * time.Second)
	if expiry.Before(until) {
		until = expiry
	}
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return Ticket{}, Failure("SERVER_BUSY")
	}
	token := base64.RawURLEncoding.EncodeToString(value[:])
	t.items[sha256.Sum256([]byte(token))] = Admission{user, id, until}
	return Ticket{token, until, "ttr.v1"}, nil
}
func (t *Tickets) Consume(token, id string) (Admission, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(token) > 128 {
		return Admission{}, Failure("UNAUTHENTICATED")
	}
	key := sha256.Sum256([]byte(token))
	a, ok := t.items[key]
	delete(t.items, key)
	if !ok || a.RoomID != id || !a.ExpiresAt.After(t.now()) {
		return Admission{}, Failure("UNAUTHENTICATED")
	}
	return a, nil
}
func (s *Service) IssueTicket(ctx context.Context, id, user string, expiry time.Time) (Ticket, error) {
	if _, err := s.View(ctx, id, user); err != nil {
		return Ticket{}, err
	}
	return s.tickets.issue(user, id, expiry)
}

// ConsumeTicket is for the Phase 3 upgrader; it must recheck membership on Attach.
func (s *Service) ConsumeTicket(token, id string) (Admission, error) {
	return s.tickets.Consume(token, id)
}
