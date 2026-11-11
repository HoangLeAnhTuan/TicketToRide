package migrations

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io/fs"
	"sort"
	"strings"
)

// Up is deliberately invoked by deployment tooling, never by the HTTP server.
func Up(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := fs.Glob(Files, "*.up.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)
	for _, name := range entries {
		raw, err := Files.ReadFile(name)
		if err != nil {
			return err
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(raw))
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("migration transaction unavailable")
		}
		_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(174846,2); CREATE SCHEMA IF NOT EXISTS ttr_private; REVOKE ALL ON SCHEMA ttr_private FROM PUBLIC; CREATE TABLE IF NOT EXISTS ttr_private.schema_migrations(name text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`)
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration registry unavailable")
		}
		var previous string
		err = tx.QueryRow(ctx, `SELECT checksum FROM ttr_private.schema_migrations WHERE name=$1`, name).Scan(&previous)
		if err == nil {
			_ = tx.Rollback(ctx)
			if previous != checksum {
				return fmt.Errorf("migration checksum changed: %s", name)
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration registry read failed")
		}
		// Exactly these files are embedded; outer transaction includes its durable marker.
		body := strings.TrimSpace(string(raw))
		body = strings.TrimPrefix(body, "BEGIN;")
		body = strings.TrimSuffix(strings.TrimSpace(body), "COMMIT;")
		if _, err = tx.Exec(ctx, body); err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO ttr_private.schema_migrations(name,checksum) VALUES($1,$2)`, name, checksum)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration failed: %s", name)
		}
		if err = tx.Commit(ctx); err != nil {
			return fmt.Errorf("migration commit must be resolved before retry: %s", name)
		}
	}
	return nil
}
