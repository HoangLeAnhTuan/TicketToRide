package room

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ticket-to-ride/internal/persistence"
)

type Options struct {
	Ruleset, Hash, Instance              string
	Workers, Queue, MaxRooms, BcryptCost int
	OperationTimeout                     time.Duration
	Now                                  func() time.Time
}
type Service struct {
	store   Store
	opts    Options
	pool    *persistence.Pool
	mu      sync.RWMutex
	actors  map[string]*actor
	closed  atomic.Bool
	tickets *Tickets
}
type Outcome struct {
	State   *View `json:"state,omitempty"`
	Receipt *Ack  `json:"receipt,omitempty"`
}
type request struct {
	command Command
	view    bool
	reply   chan answer
}
type answer struct {
	out Outcome
	err error
}
type workResult struct {
	record     *Record
	ack        *Ack
	generation string
	kind, user string
}
type actor struct {
	service     *Service
	id          string
	ctx         context.Context
	cancel      context.CancelFunc
	in          chan request
	done        chan struct{}
	results     chan persistence.Result
	record      *Record
	generations map[string]string
}

func NewService(store Store, o Options) (*Service, error) {
	if store == nil || o.Ruleset == "" || len(o.Hash) != 64 || !ValidID(o.Instance) || o.Workers < 1 || o.Queue < 1 || o.MaxRooms < 1 || o.BcryptCost < 8 || o.BcryptCost > 12 || o.OperationTimeout <= 0 || o.OperationTimeout > 5*time.Second {
		return nil, Failure("INVALID_CONFIGURATION")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{store: store, opts: o, pool: persistence.New(o.Workers, o.Queue), actors: map[string]*actor{}, tickets: NewTickets(4096, o.Now)}, nil
}
func (s *Service) Ping(ctx context.Context) error { return s.store.Ping(ctx) }
func (s *Service) Do(ctx context.Context, c Command) (Outcome, error) {
	if !ValidID(c.RoomID) || !ValidID(c.UserID) || !ValidID(c.RequestID) {
		return Outcome{}, Failure("INVALID_MESSAGE")
	}
	if len(c.Password) > 72 {
		return Outcome{}, Failure("INVALID_MESSAGE")
	}
	if (c.Kind == "SET_COLOR" || c.Kind == "SET_READY" || c.Kind == "SET_DISPLAY_NAME") && c.ExpectedVersion < 1 {
		return Outcome{}, Failure("INVALID_MESSAGE")
	}
	if (c.Kind == "ATTACH" || c.Kind == "DETACH") && !ValidID(c.Generation) {
		return Outcome{}, Failure("INVALID_MESSAGE")
	}
	if c.Kind == "CREATE" {
		var err error
		c.Name, err = Text(c.Name, 80)
		if err != nil {
			return Outcome{}, err
		}
		c.DisplayName, err = Text(c.DisplayName, 32)
		if err != nil {
			return Outcome{}, err
		}
		if c.MaxPlayers < 2 || c.MaxPlayers > 5 {
			return Outcome{}, Failure("INVALID_MESSAGE")
		}
	}
	if c.Kind == "JOIN" || c.Kind == "SET_DISPLAY_NAME" {
		var err error
		c.DisplayName, err = Text(c.DisplayName, 32)
		if err != nil {
			return Outcome{}, err
		}
	}
	return s.dispatch(ctx, request{command: c, reply: make(chan answer, 1)})
}
func (s *Service) View(ctx context.Context, id, user string) (View, error) {
	if !ValidID(id) || !ValidID(user) {
		return View{}, Failure("INVALID_MESSAGE")
	}
	out, err := s.dispatch(ctx, request{command: Command{RoomID: id, UserID: user}, view: true, reply: make(chan answer, 1)})
	if err != nil {
		return View{}, err
	}
	return *out.State, nil
}
func (s *Service) dispatch(ctx context.Context, r request) (Outcome, error) {
	if s.closed.Load() {
		return Outcome{}, Failure("PERSISTENCE_UNAVAILABLE")
	}
	s.mu.Lock()
	if s.closed.Load() {
		s.mu.Unlock()
		return Outcome{}, Failure("PERSISTENCE_UNAVAILABLE")
	}
	a := s.actors[r.command.RoomID]
	if a == nil {
		if len(s.actors) >= s.opts.MaxRooms {
			s.mu.Unlock()
			return Outcome{}, Failure("SERVER_BUSY")
		}
		root, cancel := context.WithCancel(context.Background())
		a = &actor{service: s, id: r.command.RoomID, ctx: root, cancel: cancel, in: make(chan request, 128), results: make(chan persistence.Result, 1), done: make(chan struct{}), generations: map[string]string{}}
		s.actors[a.id] = a
		go a.run()
	}
	s.mu.Unlock()
	select {
	case a.in <- r:
	case <-ctx.Done():
		return Outcome{}, ctx.Err()
	case <-a.done:
		return Outcome{}, Failure("OWNER_LOST")
	default:
		return Outcome{}, Failure("SERVER_BUSY")
	}
	select {
	case response := <-r.reply:
		return response.out, response.err
	case <-ctx.Done():
		return Outcome{}, ctx.Err()
	case <-a.done:
		select {
		case response := <-r.reply:
			return response.out, response.err
		default:
			return Outcome{}, Failure("OWNER_LOST")
		}
	}
}
func (a *actor) run() {
	s := a.service
	defer close(a.done)
	defer a.cancel()
	defer func() {
		s.mu.Lock()
		if s.actors[a.id] == a {
			delete(s.actors, a.id)
		}
		s.mu.Unlock()
	}()
	timer := time.NewTicker(10 * time.Second)
	defer timer.Stop()
	queue := []request{}
	var pending *request
	renewing := false
	needRenew := false
	respond := func(r request, out Outcome, err error) { r.reply <- answer{out, err} }
	defer func() {
		if pending != nil {
			respond(*pending, Outcome{}, Failure("PERSISTENCE_UNAVAILABLE"))
		}
		for _, r := range queue {
			respond(r, Outcome{}, Failure("PERSISTENCE_UNAVAILABLE"))
		}
	}()
	for {
		if pending == nil && !renewing {
			if needRenew && a.record != nil {
				owner := a.record.Owner
				renewing = true
				needRenew = false
				if !s.pool.Submit(persistence.Job{Context: a.ctx, Done: a.results, Run: func(ctx context.Context) (any, error) {
					limited, cancel := context.WithTimeout(ctx, s.opts.OperationTimeout)
					defer cancel()
					return s.store.Renew(limited, a.id, owner)
				}}) {
					return
				}
			} else if len(queue) > 0 {
				r := queue[0]
				queue = queue[1:]
				if r.command.Kind == "DETACH" && a.generations[r.command.UserID] != r.command.Generation {
					respond(r, Outcome{}, nil)
					continue
				}
				var base *Record
				if a.record != nil {
					v := *a.record
					v.State = v.State.Clone()
					base = &v
				}
				pending = &r
				if !s.pool.Submit(persistence.Job{Context: a.ctx, Done: a.results, Run: func(ctx context.Context) (any, error) { return s.execute(ctx, base, r) }}) {
					respond(r, Outcome{}, Failure("SERVER_BUSY"))
					pending = nil
				}
			}
		}
		select {
		case <-a.ctx.Done():
			return
		case <-timer.C:
			needRenew = true
		case r := <-a.in:
			if len(queue) >= 64 {
				respond(r, Outcome{}, Failure("SERVER_BUSY"))
			} else {
				queue = append(queue, r)
			}
		case result := <-a.results:
			if renewing {
				renewing = false
				if result.Err != nil {
					return
				}
				owner := result.Value.(Owner)
				if a.record != nil {
					a.record.Owner = owner
				}
				continue
			}
			if pending == nil {
				continue
			}
			r := *pending
			pending = nil
			if result.Err != nil {
				respond(r, Outcome{}, result.Err)
				if fatal(result.Err) || (a.record == nil && code(result.Err, "ROOM_NOT_FOUND")) {
					return
				}
				continue
			}
			v := result.Value.(workResult)
			a.record = v.record
			if v.kind == "ATTACH" {
				a.generations[v.user] = v.generation
			} else if v.kind == "DETACH" {
				delete(a.generations, v.user)
			}
			out := Outcome{Receipt: v.ack}
			if a.record != nil && a.record.State.Room.ClosedAt == nil {
				view, err := a.record.State.Project(r.command.UserID)
				if err == nil {
					out.State = &view
				} else if r.view {
					respond(r, Outcome{}, err)
					continue
				}
			}
			respond(r, out, nil)
		}
	}
}
func fatal(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Code == "COMMIT_UNCERTAIN" || e.Code == "OWNER_LOST" || e.Code == "PERSISTENCE_UNAVAILABLE" || e.Code == "INVALID_SNAPSHOT" || e.Code == "INCOMPATIBLE_SNAPSHOT")
}

func (s *Service) execute(root context.Context, base *Record, r request) (any, error) {
	ctx, cancel := context.WithTimeout(root, s.opts.OperationTimeout)
	defer cancel()
	c := r.command
	if !c.ExpiresAt.IsZero() && !c.ExpiresAt.After(s.opts.Now()) {
		return nil, Failure("TOKEN_EXPIRED")
	}
	if base == nil {
		loaded, err := s.store.Acquire(ctx, c.RoomID, s.opts.Instance)
		if err == nil {
			base = &loaded
		} else if c.Kind != "CREATE" || !code(err, "ROOM_NOT_FOUND") {
			return nil, err
		}
	}
	if r.view {
		if _, err := base.State.Project(c.UserID); err != nil {
			return nil, err
		}
		return workResult{record: base}, nil
	}
	if base != nil {
		old, err := s.store.Receipt(ctx, c.RoomID, c.UserID, c.RequestID)
		if err == nil {
			if old.Hash != c.Hash() {
				return nil, Failure("REQUEST_ID_REUSED")
			}
			if c.Kind == "CREATE" && !VerifyPassword(base.PasswordHash, c.Password) {
				return nil, Failure("REQUEST_ID_REUSED")
			}
			ack := old.Ack
			ack.Duplicate = true
			return workResult{record: base, ack: &ack}, nil
		}
		if !code(err, "ROOM_NOT_FOUND") {
			return nil, err
		}
	}
	if c.Kind == "CREATE" {
		if base != nil {
			return nil, Failure("CONFLICT")
		}
		return s.create(ctx, root, c)
	}
	if c.Kind == "JOIN" {
		if _, member := base.State.Players[c.UserID]; !member && !VerifyPassword(base.PasswordHash, c.Password) {
			return nil, Failure("INVALID_PASSWORD")
		}
	}
	now := s.opts.Now().UTC().Truncate(time.Microsecond)
	candidate, err := apply(base.State, c, now)
	if err != nil {
		return nil, err
	}
	if err := candidate.Validate(s.opts.Ruleset, s.opts.Hash); err != nil {
		return nil, err
	}
	next := *base
	next.State = candidate
	receipt := Receipt{UserID: c.UserID, Hash: c.Hash(), Ack: Ack{RequestID: c.RequestID, CommandType: c.Kind, CommittedVersion: candidate.StateVersion}}
	job := Commit{Record: next, BaseVersion: base.State.StateVersion, Receipt: receipt}
	stored, err := s.store.Commit(ctx, job)
	if errors.Is(err, ErrUncertain) {
		cancel()
		fresh, stop := context.WithTimeout(root, s.opts.OperationTimeout)
		defer stop()
		resolved, found, resolveErr := s.store.Resolve(fresh, job)
		if resolveErr != nil {
			return nil, ErrUncertain
		}
		if found {
			stored = resolved
			err = nil
		} else {
			stored, err = s.store.Commit(fresh, job)
		}
	}
	if err != nil {
		return nil, err
	}
	return workResult{record: &stored, ack: &receipt.Ack, kind: c.Kind, user: c.UserID, generation: c.Generation}, nil
}
func (s *Service) create(ctx, root context.Context, c Command) (any, error) {
	var hash string
	if c.Password != "" {
		encoded, err := hashPassword(c.Password, s.opts.BcryptCost)
		if err != nil {
			return nil, Failure("INVALID_MESSAGE")
		}
		hash = encoded
	}
	now := s.opts.Now().UTC().Truncate(time.Microsecond)
	for range 10 {
		codeValue, err := GenerateRoomCode()
		if err != nil {
			return nil, Failure("PERSISTENCE_UNAVAILABLE")
		}
		r := Record{State: State{SchemaVersion: 1, RulesetVersion: s.opts.Ruleset, CatalogHash: s.opts.Hash, StateVersion: 1, Room: Meta{ID: c.RoomID, Code: codeValue, Name: c.Name, HostID: c.UserID, MaxPlayers: c.MaxPlayers, Status: "WAITING", HasPassword: hash != "", PasswordVersion: 1, CreatedAt: now}, Players: map[string]Player{c.UserID: {UserID: c.UserID, DisplayName: c.DisplayName, Seat: 0, JoinedAt: now}}}, PasswordHash: hash, Owner: Owner{Instance: s.opts.Instance, Epoch: 1}}
		receipt := Receipt{UserID: c.UserID, Hash: c.Hash(), Ack: Ack{RequestID: c.RequestID, CommandType: c.Kind, CommittedVersion: 1}}
		stored, err := s.store.Create(ctx, r, receipt)
		if code(err, "CODE_COLLISION") {
			continue
		}
		if errors.Is(err, ErrUncertain) {
			fresh, stop := context.WithTimeout(root, s.opts.OperationTimeout)
			loaded, e := s.store.Acquire(fresh, c.RoomID, s.opts.Instance)
			if e == nil {
				existing, e := s.store.Receipt(fresh, c.RoomID, c.UserID, c.RequestID)
				if e == nil && existing.Hash == receipt.Hash && VerifyPassword(loaded.PasswordHash, c.Password) {
					stored = loaded
					err = nil
				}
			}
			stop()
		}
		if err != nil {
			return nil, err
		}
		return workResult{record: &stored, ack: &receipt.Ack}, nil
	}
	return nil, Failure("SERVER_BUSY")
}
func (s *Service) List(ctx context.Context, cursor string, limit int) (Page, error) {
	if limit < 1 || limit > 50 {
		return Page{}, Failure("INVALID_MESSAGE")
	}
	var before *Cursor
	if cursor != "" {
		if len(cursor) > 256 {
			return Page{}, Failure("INVALID_MESSAGE")
		}
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return Page{}, Failure("INVALID_MESSAGE")
		}
		var c Cursor
		if json.Unmarshal(raw, &c) != nil || c.CreatedAt.IsZero() || !ValidID(c.ID) {
			return Page{}, Failure("INVALID_MESSAGE")
		}
		before = &c
	}
	rows, err := s.store.List(ctx, before, limit+1)
	if err != nil {
		return Page{}, err
	}
	page := Page{Rooms: rows}
	if len(rows) > limit {
		page.Rooms = rows[:limit]
		last := page.Rooms[limit-1]
		raw, _ := json.Marshal(Cursor{last.CreatedAt, last.ID})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}
func (s *Service) ByCode(ctx context.Context, value string) (Summary, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 6 {
		return Summary{}, Failure("INVALID_MESSAGE")
	}
	return s.store.ByCode(ctx, value)
}
func (s *Service) Receipt(ctx context.Context, id, user, request string) (Ack, error) {
	if !ValidID(id) || !ValidID(user) || !ValidID(request) {
		return Ack{}, Failure("INVALID_MESSAGE")
	}
	r, err := s.store.Receipt(ctx, id, user, request)
	return r.Ack, err
}
func (s *Service) Close(ctx context.Context) error {
	defer s.pool.Close()
	s.closed.Store(true)
	s.mu.RLock()
	actors := make([]*actor, 0, len(s.actors))
	for _, a := range s.actors {
		actors = append(actors, a)
	}
	s.mu.RUnlock()
	// HTTP drains first. Actor cancellation then stops admission and waits for bounded jobs.
	for _, a := range actors {
		a.cancel()
	}
	for _, a := range actors {
		select {
		case <-a.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, a := range actors {
		if a.record != nil {
			_ = s.store.Release(ctx, a.id, a.record.Owner)
		}
	}
	return nil
}
