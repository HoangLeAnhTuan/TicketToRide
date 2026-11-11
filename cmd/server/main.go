package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"ticket-to-ride/internal/auth"
	"ticket-to-ride/internal/config"
	"ticket-to-ride/internal/game/catalog"
	"ticket-to-ride/internal/httpapi"
	"ticket-to-ride/internal/repository/postgres"
	"ticket-to-ride/internal/room"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	gin.SetMode(gin.ReleaseMode)
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	cat, err := catalog.LoadDefault()
	if err != nil {
		return fmt.Errorf("catalog initialization: %w", err)
	}
	verifier, err := auth.NewVerifier(auth.Options{
		Issuer: cfg.Issuer, Audience: cfg.Audience, JWKSURL: cfg.JWKSURL, Algorithms: cfg.Algorithms,
		LegacySecret: cfg.LegacySecret, CacheTTL: cfg.JWKSCacheTTL, RefreshInterval: cfg.RefreshInterval, HTTPTimeout: cfg.AuthHTTPTimeout,
	})
	if err != nil {
		return fmt.Errorf("authentication initialization: %w", err)
	}
	if err := verifier.Warm(ctx); err != nil {
		return fmt.Errorf("authentication initialization: %w", err)
	}
	var lobby *room.Service
	if cfg.DatabaseURL != "" {
		limited, cancel := context.WithTimeout(ctx, 5*time.Second)
		store, openErr := postgres.Open(limited, cfg.DatabaseURL, catalog.RulesetVersion, cat.Manifest().SHA256, int32(cfg.DatabaseMaxConns))
		cancel()
		if openErr != nil {
			return openErr
		}
		defer store.Close()
		instance, idErr := room.NewID()
		if idErr != nil {
			return fmt.Errorf("instance identity unavailable")
		}
		lobby, err = room.NewService(store, room.Options{Ruleset: catalog.RulesetVersion, Hash: cat.Manifest().SHA256, Instance: instance, Workers: cfg.RoomWorkers, Queue: cfg.RoomQueue, MaxRooms: cfg.MaxRooms, BcryptCost: cfg.BcryptCost, OperationTimeout: 5 * time.Second})
		if err != nil {
			return err
		}
		defer func() {
			drain, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
			defer cancel()
			if err := lobby.Close(drain); err != nil {
				logger.Error("room drain failed")
			}
		}()
	} else {
		logger.Warn("room APIs unavailable until DATABASE_URL is configured")
	}
	api, err := httpapi.New(cat, verifier, logger, httpapi.WithRooms(lobby), httpapi.WithAllowedOrigins(cfg.AllowedOrigins), httpapi.RequirePersistence())
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr: cfg.HTTPAddr, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10,
	}
	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	logger.Info("HTTP server started", "address", listener.Addr().String(), "ruleset", catalog.RulesetVersion, "catalog_hash", cat.Manifest().SHA256)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
		api.Drain()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			closeErr := server.Close()
			return errors.Join(fmt.Errorf("graceful shutdown: %w", err), closeErr)
		}
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve during shutdown: %w", err)
		}
		return nil
	}
}
