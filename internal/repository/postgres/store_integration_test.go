package postgres_test

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"ticket-to-ride/internal/game/catalog"
	"ticket-to-ride/internal/repository/postgres"
	"ticket-to-ride/internal/room"
	"ticket-to-ride/migrations"
)

func uuid(t *testing.T) string {
	t.Helper()
	id, err := room.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func errCode(t *testing.T, err error, want string) {
	t.Helper()
	var e *room.Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("got %v, want %s", err, want)
	}
}

func TestPostgresTransactionsRolesAndFencing(t *testing.T) {
	dsn := os.Getenv("TTR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TTR_TEST_DATABASE_URL to a disposable loopback PostgreSQL administrator connection")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	ip := net.ParseIP(cfg.ConnConfig.Host)
	if cfg.ConnConfig.Host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		t.Fatal("integration tests require a loopback server; remote databases are never modified")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	name := "ttr_test_" + strings.ReplaceAll(uuid(t), "-", "")
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	childCfg := cfg.Copy()
	childCfg.ConnConfig.Database = name
	owner, err := pgxpool.NewWithConfig(ctx, childCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		owner.Close()
		clean, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, err := admin.Exec(clean, `DROP DATABASE `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`)
		admin.Close()
		if err != nil {
			t.Error("could not remove the uniquely created test database")
		}
	})
	_, err = owner.Exec(ctx, `DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='anon') THEN CREATE ROLE anon NOLOGIN; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='authenticated') THEN CREATE ROLE authenticated NOLOGIN; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='service_role') THEN CREATE ROLE service_role NOLOGIN BYPASSRLS; END IF;
 END $$; CREATE SCHEMA auth; CREATE TABLE auth.users(id uuid PRIMARY KEY);`)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, owner); err != nil {
		t.Fatalf("migration replay: %v", err)
	}
	cat, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	runtimeCfg := childCfg.Copy()
	runtimeCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE ttr_backend`)
		return err
	}
	runtimeCfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, runtimeCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := postgres.New(pool, catalog.RulesetVersion, cat.Manifest().SHA256)
	if err := store.Ping(ctx); err != nil {
		t.Fatalf("runtime role rejected: %v", err)
	}
	users := []string{}
	for range 12 {
		user := uuid(t)
		users = append(users, user)
		if _, err := owner.Exec(ctx, `INSERT INTO auth.users(id) VALUES($1)`, user); err != nil {
			t.Fatal(err)
		}
	}
	instance := uuid(t)
	service, err := room.NewService(store, room.Options{Ruleset: catalog.RulesetVersion, Hash: cat.Manifest().SHA256, Instance: instance, Workers: 4, Queue: 64, MaxRooms: 50, BcryptCost: 8, OperationTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := service.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	create := room.Command{Kind: "CREATE", RoomID: uuid(t), UserID: users[0], RequestID: uuid(t), Name: "Integration room", DisplayName: "Host", MaxPlayers: 3, Password: "private-test-password"}
	first, err := service.Do(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	var hash string
	var version int64
	if err := owner.QueryRow(ctx, `SELECT g.state_json,r.password_hash,r.state_version FROM public.game_snapshots g JOIN public.rooms r ON r.id=g.room_id WHERE r.id=$1`, create.RoomID).Scan(&raw, &hash, &version); err != nil {
		t.Fatal(err)
	}
	if !room.VerifyPassword(hash, create.Password) || version != 1 || strings.Contains(string(raw), create.Password) || strings.Contains(string(raw), "password_hash") {
		t.Fatal("password or snapshot storage is incorrect")
	}
	if replay, err := service.Do(ctx, create); err != nil || !replay.Receipt.Duplicate {
		t.Fatalf("durable create replay: %v", err)
	}
	bad := room.Command{Kind: "JOIN", RoomID: create.RoomID, UserID: users[1], RequestID: uuid(t), DisplayName: "Guest", Password: "wrong"}
	_, err = service.Do(ctx, bad)
	errCode(t, err, "INVALID_PASSWORD")
	var wg sync.WaitGroup
	var wins atomic.Int32
	for _, user := range users[1:10] {
		cmd := room.Command{Kind: "JOIN", RoomID: create.RoomID, UserID: user, RequestID: uuid(t), DisplayName: "Guest", Password: create.Password}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.Do(ctx, cmd)
			if err == nil {
				wins.Add(1)
			} else {
				var e *room.Error
				if !errors.As(err, &e) || e.Code != "ROOM_FULL" {
					t.Errorf("concurrent SQL join: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 2 {
		t.Fatalf("overfilled room: %d successful joins", wins.Load())
	}
	view, err := service.View(ctx, create.RoomID, users[0])
	if err != nil || len(view.Players) != 3 || view.StateVersion != 3 {
		t.Fatalf("inconsistent roster: %+v %v", view, err)
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	record, err := store.Acquire(ctx, create.RoomID, uuid(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"anon", "authenticated", "service_role"} {
		tx, err := owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE `+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, `SELECT state_json FROM public.game_snapshots`)
		var p *pgconn.PgError
		if !errors.As(err, &p) || p.Code != "42501" {
			t.Errorf("%s can access private snapshots: %v", role, err)
		}
		_ = tx.Rollback(ctx)
	}
	var secured int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM pg_class WHERE oid IN ('public.rooms'::regclass,'public.room_players'::regclass,'public.game_snapshots'::regclass,'public.room_commands'::regclass) AND relrowsecurity`).Scan(&secured); err != nil || secured != 4 {
		t.Fatal("RLS is not enabled on all authoritative tables")
	}
	// Force a foreign-key error after metadata updates and roster deletion; every write must roll back.
	next := record
	next.State = record.State.Clone()
	next.State.StateVersion++
	next.State.Room.Name = "must roll back"
	missing := uuid(t)
	next.State.Players[missing] = room.Player{UserID: missing, DisplayName: "No Auth row", Seat: 4, JoinedAt: time.Now().UTC().Truncate(time.Microsecond)}
	// Keep cardinality valid by replacing a non-host player's seat.
	for id := range next.State.Players {
		if id != missing && id != users[0] {
			delete(next.State.Players, id)
			break
		}
	}
	receipt := room.Receipt{UserID: users[0], Hash: strings.Repeat("b", 64), Ack: room.Ack{RequestID: uuid(t), CommandType: "JOIN", CommittedVersion: next.State.StateVersion}}
	_, err = store.Commit(ctx, room.Commit{Record: next, BaseVersion: record.State.StateVersion, Receipt: receipt})
	errCode(t, err, "INVALID_IDENTITY")
	var nameAfter string
	var versionAfter int64
	if err := owner.QueryRow(ctx, `SELECT name,state_version FROM public.rooms WHERE id=$1`, create.RoomID).Scan(&nameAfter, &versionAfter); err != nil {
		t.Fatal(err)
	}
	if nameAfter != record.State.Room.Name || versionAfter != record.State.StateVersion {
		t.Fatal("failed transaction partially updated aggregate")
	}
	_, err = store.Receipt(ctx, create.RoomID, users[0], receipt.Ack.RequestID)
	errCode(t, err, "ROOM_NOT_FOUND")
	// Resolve a committed candidate under the same lock that fences ownership transfer.
	next = record
	next.State = record.State.Clone()
	next.State.StateVersion++
	next.State.Room.Name = "committed once"
	receipt.Ack.RequestID = uuid(t)
	receipt.Ack.CommittedVersion = next.State.StateVersion
	job := room.Commit{Record: next, BaseVersion: record.State.StateVersion, Receipt: receipt}
	if _, err := store.Commit(ctx, job); err != nil {
		t.Fatal(err)
	}
	resolved, found, err := store.Resolve(ctx, job)
	if err != nil || !found || resolved.State.StateVersion != next.State.StateVersion {
		t.Fatalf("committed result could not be resolved: %v", err)
	}
	if _, err := owner.Exec(ctx, `UPDATE public.rooms SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, create.RoomID); err != nil {
		t.Fatal(err)
	}
	_, err = store.Renew(ctx, create.RoomID, record.Owner)
	errCode(t, err, "OWNER_LOST")
	_, err = store.Commit(ctx, job)
	errCode(t, err, "OWNER_LOST")
	if _, err := store.Acquire(ctx, create.RoomID, uuid(t)); err != nil {
		t.Fatal(err)
	}
	_, err = store.Acquire(ctx, create.RoomID, record.Owner.Instance)
	errCode(t, err, "OWNER_LOST")
	if first.State.Room.Code == "" {
		t.Fatal("room code missing")
	}
	for _, name := range []string{"000002_backend_access.down.sql", "000001_core.down.sql"} {
		raw, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Exec(ctx, string(raw)); err != nil {
			t.Fatalf("rollback script %s failed: %v", name, err)
		}
	}
}
