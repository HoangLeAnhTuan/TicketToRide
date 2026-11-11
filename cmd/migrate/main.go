package main

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"ticket-to-ride/migrations"
	"time"
)

func main() {
	url := os.Getenv("DATABASE_MIGRATION_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_MIGRATION_URL is required; use a deployment credential, not the runtime login")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid migration database configuration")
		os.Exit(1)
	}
	defer pool.Close()
	if err := migrations.Up(ctx, pool); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("backend migrations are current")
}
