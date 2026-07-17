// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Günther Mair

// Command oncevault runs the OnceVault zero-knowledge secret-sharing service.
//
// Usage: oncevault [-config <path>] [serve|cleanup]
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"oncevault/config"
	"oncevault/server"
	"oncevault/store"
)

// indexHTML is the single-file frontend, embedded at build time and served
// byte-identical at GET /.
//
//go:embed web/index.html
var indexHTML []byte

// faviconICO is the vault-wheel icon (PNG-in-ICO, 16/32/48px), embedded at
// build time and served byte-identical at GET /favicon.ico.
//
//go:embed web/favicon.ico
var faviconICO []byte

func main() {
	os.Exit(run())
}

func run() int {
	configPath := flag.String("config", "config.yaml", "path to the config file (.json, .yaml, or .yml)")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cmd := "serve"
	switch flag.NArg() {
	case 0:
	case 1:
		cmd = flag.Arg(0)
	default:
		fmt.Fprintf(os.Stderr, "usage: %s [-config <path>] [serve|cleanup]\n", os.Args[0])
		return 1
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		return 1
	}

	switch cmd {
	case "serve":
		st, err := store.Open(cfg.DB.Driver, cfg.DB.DSN)
		if err != nil {
			slog.Error("open store failed", "driver", cfg.DB.Driver, "error", err)
			return 1
		}
		return serve(cfg, st)
	case "cleanup":
		st, err := store.Open(cfg.DB.Driver, cfg.DB.DSN)
		if err != nil {
			slog.Error("open store failed", "driver", cfg.DB.Driver, "error", err)
			return 1
		}
		return cleanup(cfg, st)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q: want serve or cleanup\n", cmd)
		return 1
	}
}

// cleanup purges expired secrets and reclaims space, then closes the store.
// On the redis backend both operations are no-ops handled inside the store
// (native TTL already evicts expired keys).
func cleanup(cfg *config.Config, st store.Store) int {
	code := 0
	ctx := context.Background()

	if cfg.DB.Driver == "redis" {
		slog.Info("redis backend: purge and vacuum are no-ops, native TTL handles expiry")
	}

	n, err := st.PurgeExpired(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cleanup: purge expired secrets failed: %v\n", err)
		code = 1
	} else {
		slog.Info("purged expired secrets", "count", n, "driver", cfg.DB.Driver)
	}

	if code == 0 {
		if err := st.Vacuum(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "cleanup: vacuum failed: %v\n", err)
			code = 1
		}
	}

	if err := st.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "cleanup: store close failed: %v\n", err)
		code = 1
	}
	return code
}

// serve runs the HTTP server until SIGINT/SIGTERM, then shuts down gracefully
// and closes the store.
func serve(cfg *config.Config, st store.Store) int {
	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server.New(cfg, st, indexHTML, faviconICO),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       time.Minute,
		WriteTimeout:      time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	slog.Info("oncevault listening", "addr", cfg.Listen, "driver", cfg.DB.Driver)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	code := 0
	select {
	case err := <-errCh:
		slog.Error("server failed", "error", err)
		code = 1
	case sig := <-sigCh:
		slog.Info("shutting down", "signal", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(ctx); err != nil {
			slog.Warn("graceful shutdown incomplete", "error", err)
			code = 1
		}
	}
	if err := st.Close(); err != nil {
		slog.Warn("store close failed", "error", err)
	}
	return code
}
