// Package memory supplies a transactional test adapter; production always uses PostgreSQL.
package memory

import (
	"context"
	"slices"
	"strings"
	"sync"
	"ticket-to-ride/internal/room"
	"time"
)

type Store struct {
	mu       sync.Mutex
	records  map[string]room.Record
	receipts map[string]room.Receipt
	now      func() time.Time
}

func New(now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{records: map[string]room.Record{}, receipts: map[string]room.Receipt{}, now: now}
}
func clone(r room.Record) room.Record       { r.State = r.State.Clone(); return r }
func key(id, user, request string) string   { return id + "/" + user + "/" + request }
func (s *Store) Ping(context.Context) error { return nil }
func (s *Store) Acquire(_ context.Context, id, instance string) (room.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return r, room.Failure("ROOM_NOT_FOUND")
	}
	if r.Owner.Instance != "" && r.Owner.Instance != instance && r.Owner.Until.After(s.now()) {
		return r, room.Failure("OWNER_LOST")
	}
	r = clone(r)
	r.Owner = room.Owner{Instance: instance, Epoch: r.Owner.Epoch + 1, Until: s.now().Add(30 * time.Second)}
	changed := false
	for id, p := range r.State.Players {
		if p.IsConnected || p.IsReady {
			p.IsConnected = false
			p.IsReady = false
			r.State.Players[id] = p
			changed = true
		}
	}
	if changed {
		r.State.StateVersion++
	}
	s.records[id] = clone(r)
	return r, nil
}
func (s *Store) Create(_ context.Context, r room.Record, receipt room.Receipt) (room.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[r.State.Room.ID]; exists {
		return r, room.Failure("CONFLICT")
	}
	for _, other := range s.records {
		if other.State.Room.Code == r.State.Room.Code {
			return r, room.Failure("CODE_COLLISION")
		}
	}
	r.Owner.Epoch = 1
	r.Owner.Until = s.now().Add(30 * time.Second)
	s.records[r.State.Room.ID] = clone(r)
	s.receipts[key(r.State.Room.ID, receipt.UserID, receipt.Ack.RequestID)] = receipt
	return clone(r), nil
}
func (s *Store) Commit(_ context.Context, j room.Commit) (room.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.records[j.Record.State.Room.ID]
	if !ok {
		return room.Record{}, room.Failure("ROOM_NOT_FOUND")
	}
	if !s.owned(current.Owner, j.Record.Owner) {
		return room.Record{}, room.Failure("OWNER_LOST")
	}
	k := key(j.Record.State.Room.ID, j.Receipt.UserID, j.Receipt.Ack.RequestID)
	if old, ok := s.receipts[k]; ok {
		if old.Hash != j.Receipt.Hash {
			return room.Record{}, room.Failure("REQUEST_ID_REUSED")
		}
		return clone(current), nil
	}
	if current.State.StateVersion != j.BaseVersion || j.Record.State.StateVersion < j.BaseVersion || j.Record.State.StateVersion > j.BaseVersion+1 {
		return room.Record{}, room.Failure("STALE_STATE")
	}
	s.records[j.Record.State.Room.ID] = clone(j.Record)
	s.receipts[k] = j.Receipt
	return clone(j.Record), nil
}
func (s *Store) Resolve(_ context.Context, j room.Commit) (room.Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[j.Record.State.Room.ID]
	if !ok {
		return r, false, room.Failure("ROOM_NOT_FOUND")
	}
	if !s.owned(r.Owner, j.Record.Owner) {
		return r, false, room.Failure("OWNER_LOST")
	}
	if receipt, ok := s.receipts[key(r.State.Room.ID, j.Receipt.UserID, j.Receipt.Ack.RequestID)]; ok {
		if receipt.Hash != j.Receipt.Hash {
			return r, false, room.Failure("REQUEST_ID_REUSED")
		}
		return clone(r), true, nil
	}
	if r.State.StateVersion != j.BaseVersion {
		return r, false, room.Failure("STALE_STATE")
	}
	return clone(r), false, nil
}
func (s *Store) Receipt(_ context.Context, id, user, request string) (room.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.receipts[key(id, user, request)]
	if !ok {
		return r, room.Failure("ROOM_NOT_FOUND")
	}
	return r, nil
}
func (s *Store) owned(a, b room.Owner) bool {
	return a.Instance == b.Instance && a.Epoch == b.Epoch && a.Until.After(s.now())
}
func (s *Store) Renew(_ context.Context, id string, owner room.Owner) (room.Owner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok || !s.owned(r.Owner, owner) {
		return owner, room.Failure("OWNER_LOST")
	}
	owner.Until = s.now().Add(30 * time.Second)
	r.Owner = owner
	s.records[id] = r
	return owner, nil
}
func (s *Store) Release(_ context.Context, id string, owner room.Owner) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if ok && r.Owner.Instance == owner.Instance && r.Owner.Epoch == owner.Epoch {
		r.Owner.Instance = ""
		r.Owner.Until = time.Time{}
		s.records[id] = r
	}
	return nil
}
func summary(r room.Record) room.Summary {
	m := r.State.Room
	return room.Summary{PublicRoom: room.PublicRoom{ID: m.ID, Code: m.Code, Name: m.Name, HostID: m.HostID, MaxPlayers: m.MaxPlayers, Status: m.Status, HasPassword: m.HasPassword}, PlayerCount: len(r.State.Players), CreatedAt: m.CreatedAt}
}
func (s *Store) List(_ context.Context, cursor *room.Cursor, limit int) ([]room.Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := []room.Summary{}
	for _, r := range s.records {
		if r.State.Room.ClosedAt != nil {
			continue
		}
		m := r.State.Room
		if cursor != nil && (m.CreatedAt.After(cursor.CreatedAt) || (m.CreatedAt.Equal(cursor.CreatedAt) && m.ID >= cursor.ID)) {
			continue
		}
		rows = append(rows, summary(r))
	}
	slices.SortFunc(rows, func(a, b room.Summary) int {
		if n := b.CreatedAt.Compare(a.CreatedAt); n != 0 {
			return n
		}
		return strings.Compare(b.ID, a.ID)
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}
func (s *Store) ByCode(_ context.Context, code string) (room.Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.records {
		if r.State.Room.Code == code && r.State.Room.ClosedAt == nil {
			return summary(r), nil
		}
	}
	return room.Summary{}, room.Failure("ROOM_NOT_FOUND")
}
