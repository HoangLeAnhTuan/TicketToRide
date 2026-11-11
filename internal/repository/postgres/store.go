// Package postgres persists each room aggregate and command receipt in one fenced transaction.
package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticket-to-ride/internal/room"
)

type Store struct {
	pool          *pgxpool.Pool
	ruleset, hash string
}

func New(pool *pgxpool.Pool, ruleset, hash string) *Store {
	return &Store{pool: pool, ruleset: ruleset, hash: hash}
}

func Open(ctx context.Context, dsn, ruleset, hash string, max int32) (*Store, error) {
	if max < 2 || max > 100 {
		return nil, fmt.Errorf("database pool size must be between 2 and 100")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid database configuration")
	}
	ip := net.ParseIP(cfg.ConnConfig.Host)
	local := cfg.ConnConfig.Host == "localhost" || (ip != nil && ip.IsLoopback())
	if !local && (cfg.ConnConfig.TLSConfig == nil || cfg.ConnConfig.TLSConfig.InsecureSkipVerify) {
		return nil, fmt.Errorf("database TLS certificate verification is required")
	}
	if !local {
		for _, f := range cfg.ConnConfig.Fallbacks {
			if f.TLSConfig == nil || f.TLSConfig.InsecureSkipVerify {
				return nil, fmt.Errorf("insecure database fallback is forbidden")
			}
		}
	}
	cfg.MaxConns = max
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "2000"
	cfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "5000"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("database pool initialization failed")
	}
	s := New(pool, ruleset, hash)
	if err := s.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database schema or runtime role is unavailable")
	}
	return s, nil
}
func (s *Store) Close() { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error {
	var allowed bool
	err := s.pool.QueryRow(ctx, `SELECT NOT r.rolsuper AND NOT r.rolbypassrls
 AND pg_has_role(current_user,'ttr_backend','member')
 AND NOT EXISTS(SELECT 1 FROM pg_class WHERE oid IN ('public.rooms'::regclass,'public.room_players'::regclass,'public.game_snapshots'::regclass,'public.room_commands'::regclass) AND pg_has_role(current_user,relowner,'USAGE'))
 FROM pg_roles r WHERE rolname=current_user`).Scan(&allowed)
	if err != nil || !allowed {
		return room.Failure("PERSISTENCE_UNAVAILABLE")
	}
	_, err = s.pool.Exec(ctx, `SELECT closed_at FROM public.rooms LIMIT 0`)
	if err != nil {
		return safe(err)
	}
	var versions int
	err = s.pool.QueryRow(ctx, `SELECT count(*) FROM ttr_private.schema_migrations WHERE name IN ('000001_core.up.sql','000002_backend_access.up.sql')`).Scan(&versions)
	if err != nil || versions != 2 {
		return room.Failure("PERSISTENCE_UNAVAILABLE")
	}
	return nil
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func (s *Store) load(ctx context.Context, q queryer, id string, lock bool) (room.Record, error) {
	if lock {
		var locked string
		if err := q.QueryRow(ctx, `SELECT id::text FROM public.rooms WHERE id=$1 FOR UPDATE`, id).Scan(&locked); err != nil {
			return room.Record{}, safe(err)
		}
	}
	sql := `SELECT r.id::text,r.room_code,r.name,coalesce(r.host_id::text,''),r.max_players,r.status,r.password_hash,r.password_version,r.created_at,r.closed_at,coalesce(r.owner_instance::text,''),r.owner_epoch,r.lease_until,r.state_version,g.state_json,g.state_version,g.schema_version,g.ruleset_version,g.catalog_hash,g.turn_number
 FROM public.rooms r LEFT JOIN public.game_snapshots g ON g.room_id=r.id WHERE r.id=$1`
	var r room.Record
	var normal room.Meta
	var hash *string
	var until *time.Time
	var raw []byte
	var version int64
	var snapshotVersion, turn int64
	var schema int
	var ruleset, catalogHash string
	err := q.QueryRow(ctx, sql, id).Scan(&normal.ID, &normal.Code, &normal.Name, &normal.HostID, &normal.MaxPlayers, &normal.Status, &hash, &normal.PasswordVersion, &normal.CreatedAt, &normal.ClosedAt, &r.Owner.Instance, &r.Owner.Epoch, &until, &version, &raw, &snapshotVersion, &schema, &ruleset, &catalogHash, &turn)
	if err != nil {
		return r, safe(err)
	}
	if hash != nil {
		r.PasswordHash = *hash
		normal.HasPassword = true
	}
	if until != nil {
		r.Owner.Until = until.UTC()
	}
	normal.CreatedAt = normal.CreatedAt.UTC()
	if normal.ClosedAt != nil {
		v := normal.ClosedAt.UTC()
		normal.ClosedAt = &v
	}
	state, err := room.Decode(raw, s.ruleset, s.hash)
	if err != nil {
		return r, err
	}
	a, _ := json.Marshal(normal)
	b, _ := json.Marshal(state.Room)
	if !bytes.Equal(a, b) || version != state.StateVersion || snapshotVersion != state.StateVersion || schema != state.SchemaVersion || ruleset != state.RulesetVersion || catalogHash != state.CatalogHash || turn != 0 {
		return r, room.Failure("INVALID_SNAPSHOT")
	}
	rows, err := q.Query(ctx, `SELECT user_id::text,display_name,coalesce(color,''),seat,joined_at,is_ready,is_connected FROM public.room_players WHERE room_id=$1`, id)
	if err != nil {
		return r, safe(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var p room.Player
		if err := rows.Scan(&p.UserID, &p.DisplayName, &p.Color, &p.Seat, &p.JoinedAt, &p.IsReady, &p.IsConnected); err != nil {
			return r, safe(err)
		}
		p.JoinedAt = p.JoinedAt.UTC()
		expected, ok := state.Players[p.UserID]
		if !ok || expected != p {
			return r, room.Failure("INVALID_SNAPSHOT")
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return r, safe(err)
	}
	if count != len(state.Players) {
		return r, room.Failure("INVALID_SNAPSHOT")
	}
	r.State = state
	return r, nil
}
func (s *Store) Acquire(ctx context.Context, id, instance string) (room.Record, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return room.Record{}, safe(err)
	}
	defer rollback(tx)
	r, err := s.load(ctx, tx, id, true)
	if err != nil {
		return r, err
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return r, safe(err)
	}
	if r.Owner.Instance != "" && r.Owner.Instance != instance && r.Owner.Until.After(now) {
		return r, room.Failure("OWNER_LOST")
	}
	err = tx.QueryRow(ctx, `UPDATE public.rooms SET owner_instance=$2,owner_epoch=owner_epoch+1,lease_until=clock_timestamp()+interval '30 seconds' WHERE id=$1 RETURNING owner_epoch,lease_until`, id, instance).Scan(&r.Owner.Epoch, &r.Owner.Until)
	if err != nil {
		return r, safe(err)
	}
	r.Owner.Instance = instance
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
		if err := s.write(ctx, tx, r); err != nil {
			return r, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return r, room.ErrUncertain
	}
	return r, nil
}
func (s *Store) Create(ctx context.Context, r room.Record, receipt room.Receipt) (room.Record, error) {
	if err := r.State.Validate(s.ruleset, s.hash); err != nil {
		return r, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, safe(err)
	}
	defer rollback(tx)
	m := r.State.Room
	err = tx.QueryRow(ctx, `INSERT INTO public.rooms(id,room_code,name,password_hash,host_id,max_players,status,state_version,password_version,created_at,owner_instance,owner_epoch,lease_until)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,1,clock_timestamp()+interval '30 seconds') RETURNING owner_epoch,lease_until`, m.ID, m.Code, m.Name, nullable(r.PasswordHash), m.HostID, m.MaxPlayers, m.Status, r.State.StateVersion, m.PasswordVersion, m.CreatedAt, r.Owner.Instance).Scan(&r.Owner.Epoch, &r.Owner.Until)
	if err != nil {
		return r, safe(err)
	}
	if err := s.write(ctx, tx, r); err != nil {
		return r, err
	}
	if err := insertReceipt(ctx, tx, m.ID, receipt); err != nil {
		return r, err
	}
	if err := tx.Commit(ctx); err != nil {
		return r, room.ErrUncertain
	}
	return r, nil
}
func (s *Store) Commit(ctx context.Context, j room.Commit) (room.Record, error) {
	r := j.Record
	if err := r.State.Validate(s.ruleset, s.hash); err != nil {
		return r, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r, safe(err)
	}
	defer rollback(tx)
	current, err := s.load(ctx, tx, r.State.Room.ID, true)
	if err != nil {
		return r, err
	}
	if err := fence(ctx, tx, current.Owner, r.Owner); err != nil {
		return r, err
	}
	old, err := getReceipt(ctx, tx, r.State.Room.ID, j.Receipt.UserID, j.Receipt.Ack.RequestID)
	if err == nil {
		if old.Hash != j.Receipt.Hash {
			return r, room.Failure("REQUEST_ID_REUSED")
		}
		return current, nil
	}
	if !isCode(err, "ROOM_NOT_FOUND") {
		return r, err
	}
	if current.State.StateVersion != j.BaseVersion || r.State.StateVersion < j.BaseVersion || r.State.StateVersion > j.BaseVersion+1 {
		return r, room.Failure("STALE_STATE")
	}
	if r.State.StateVersion > j.BaseVersion {
		if err := s.write(ctx, tx, r); err != nil {
			return r, err
		}
	}
	if err := insertReceipt(ctx, tx, r.State.Room.ID, j.Receipt); err != nil {
		return r, err
	}
	if err := tx.Commit(ctx); err != nil {
		return r, room.ErrUncertain
	}
	return r, nil
}
func (s *Store) Resolve(ctx context.Context, j room.Commit) (room.Record, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return room.Record{}, false, safe(err)
	}
	defer rollback(tx)
	r, err := s.load(ctx, tx, j.Record.State.Room.ID, true)
	if err != nil {
		return r, false, err
	}
	if err := fence(ctx, tx, r.Owner, j.Record.Owner); err != nil {
		return r, false, err
	}
	receipt, err := getReceipt(ctx, tx, r.State.Room.ID, j.Receipt.UserID, j.Receipt.Ack.RequestID)
	if err == nil {
		if receipt.Hash != j.Receipt.Hash {
			return r, false, room.Failure("REQUEST_ID_REUSED")
		}
		return r, true, nil
	}
	if !isCode(err, "ROOM_NOT_FOUND") {
		return r, false, err
	}
	if r.State.StateVersion != j.BaseVersion {
		return r, false, room.Failure("STALE_STATE")
	}
	return r, false, nil
}
func fence(ctx context.Context, q queryer, current, expected room.Owner) error {
	var now time.Time
	if err := q.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return safe(err)
	}
	if current.Instance != expected.Instance || current.Epoch != expected.Epoch || !current.Until.After(now) {
		return room.Failure("OWNER_LOST")
	}
	return nil
}
func (s *Store) write(ctx context.Context, q queryer, r room.Record) error {
	m := r.State.Room
	_, err := q.Exec(ctx, `UPDATE public.rooms SET name=$2,host_id=$3,max_players=$4,status=$5,state_version=$6,closed_at=$7,updated_at=clock_timestamp() WHERE id=$1`, m.ID, m.Name, nullable(m.HostID), m.MaxPlayers, m.Status, r.State.StateVersion, m.ClosedAt)
	if err != nil {
		return safe(err)
	}
	if _, err := q.Exec(ctx, `DELETE FROM public.room_players WHERE room_id=$1`, m.ID); err != nil {
		return safe(err)
	}
	for _, p := range r.State.Players {
		_, err := q.Exec(ctx, `INSERT INTO public.room_players(room_id,user_id,display_name,color,is_ready,is_connected,joined_at,seat) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, m.ID, p.UserID, p.DisplayName, nullable(p.Color), p.IsReady, p.IsConnected, p.JoinedAt, p.Seat)
		if err != nil {
			return safe(err)
		}
	}
	raw, err := json.Marshal(r.State)
	if err != nil {
		return room.Failure("INVALID_SNAPSHOT")
	}
	tag, err := q.Exec(ctx, `INSERT INTO public.game_snapshots(room_id,turn_number,state_version,schema_version,ruleset_version,catalog_hash,state_json)
 VALUES($1,0,$2,$3,$4,$5,$6) ON CONFLICT(room_id) DO UPDATE SET state_version=excluded.state_version,schema_version=excluded.schema_version,ruleset_version=excluded.ruleset_version,catalog_hash=excluded.catalog_hash,state_json=excluded.state_json,updated_at=clock_timestamp()
 WHERE public.game_snapshots.state_version<excluded.state_version`, m.ID, r.State.StateVersion, r.State.SchemaVersion, r.State.RulesetVersion, r.State.CatalogHash, raw)
	if err != nil {
		return safe(err)
	}
	if tag.RowsAffected() != 1 {
		return room.Failure("STALE_STATE")
	}
	return nil
}
func insertReceipt(ctx context.Context, q queryer, id string, r room.Receipt) error {
	raw, err := json.Marshal(r.Ack)
	if err != nil {
		return room.Failure("INVALID_MESSAGE")
	}
	_, err = q.Exec(ctx, `INSERT INTO public.room_commands(room_id,user_id,request_id,command_hash,command_type,state_version,response_json) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, r.UserID, r.Ack.RequestID, r.Hash, r.Ack.CommandType, r.Ack.CommittedVersion, raw)
	return safe(err)
}
func getReceipt(ctx context.Context, q queryer, id, user, request string) (room.Receipt, error) {
	r := room.Receipt{UserID: user}
	var raw []byte
	err := q.QueryRow(ctx, `SELECT command_hash,response_json FROM public.room_commands WHERE room_id=$1 AND user_id=$2 AND request_id=$3`, id, user, request).Scan(&r.Hash, &raw)
	if err != nil {
		return r, safe(err)
	}
	if err := json.Unmarshal(raw, &r.Ack); err != nil {
		return r, room.Failure("INVALID_SNAPSHOT")
	}
	return r, nil
}
func (s *Store) Receipt(ctx context.Context, id, user, request string) (room.Receipt, error) {
	return getReceipt(ctx, s.pool, id, user, request)
}
func (s *Store) Renew(ctx context.Context, id string, o room.Owner) (room.Owner, error) {
	err := s.pool.QueryRow(ctx, `UPDATE public.rooms SET lease_until=clock_timestamp()+interval '30 seconds' WHERE id=$1 AND owner_instance=$2 AND owner_epoch=$3 AND lease_until>clock_timestamp() RETURNING lease_until`, id, o.Instance, o.Epoch).Scan(&o.Until)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, room.Failure("OWNER_LOST")
	}
	return o, safe(err)
}
func (s *Store) Release(ctx context.Context, id string, o room.Owner) error {
	_, err := s.pool.Exec(ctx, `UPDATE public.rooms SET owner_instance=NULL,lease_until=NULL WHERE id=$1 AND owner_instance=$2 AND owner_epoch=$3`, id, o.Instance, o.Epoch)
	return safe(err)
}
func (s *Store) List(ctx context.Context, cursor *room.Cursor, limit int) ([]room.Summary, error) {
	var before *time.Time
	var id *string
	if cursor != nil {
		before = &cursor.CreatedAt
		id = &cursor.ID
	}
	rows, err := s.pool.Query(ctx, `SELECT r.id::text,r.room_code,r.name,r.host_id::text,r.max_players,r.status,(r.password_hash IS NOT NULL),r.created_at,(SELECT count(*) FROM public.room_players p WHERE p.room_id=r.id)
 FROM public.rooms r WHERE r.closed_at IS NULL AND r.status='WAITING' AND ($1::timestamptz IS NULL OR (r.created_at,r.id)<($1,$2::uuid)) ORDER BY r.created_at DESC,r.id DESC LIMIT $3`, before, id, limit)
	if err != nil {
		return nil, safe(err)
	}
	defer rows.Close()
	result := []room.Summary{}
	for rows.Next() {
		var r room.Summary
		if err := rows.Scan(&r.ID, &r.Code, &r.Name, &r.HostID, &r.MaxPlayers, &r.Status, &r.HasPassword, &r.CreatedAt, &r.PlayerCount); err != nil {
			return nil, safe(err)
		}
		result = append(result, r)
	}
	return result, safe(rows.Err())
}
func (s *Store) ByCode(ctx context.Context, code string) (room.Summary, error) {
	var r room.Summary
	err := s.pool.QueryRow(ctx, `SELECT r.id::text,r.room_code,r.name,r.host_id::text,r.max_players,r.status,(r.password_hash IS NOT NULL),r.created_at,(SELECT count(*) FROM public.room_players p WHERE p.room_id=r.id) FROM public.rooms r WHERE r.room_code=$1 AND r.closed_at IS NULL`, code).Scan(&r.ID, &r.Code, &r.Name, &r.HostID, &r.MaxPlayers, &r.Status, &r.HasPassword, &r.CreatedAt, &r.PlayerCount)
	return r, safe(err)
}
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
func isCode(err error, code string) bool {
	var e *room.Error
	return errors.As(err, &e) && e.Code == code
}
func safe(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return room.Failure("ROOM_NOT_FOUND")
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		switch p.Code {
		case "23505":
			if p.ConstraintName == "rooms_room_code_key" {
				return room.Failure("CODE_COLLISION")
			}
			return room.Failure("CONFLICT")
		case "23503":
			return room.Failure("INVALID_IDENTITY")
		}
	}
	return room.Failure("PERSISTENCE_UNAVAILABLE")
}
