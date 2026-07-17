// Package store provides the storage backends for OnceVault secrets.
// This file is the frozen contract (docs/PLAN_STEP_0.md §0.3) — do not modify.
package store

import (
	"context"
	"errors"
	"fmt"
)

// Sentinel errors every Store implementation must map its driver errors onto.
var (
	ErrNotFound  = errors.New("secret not found")
	ErrExpired   = errors.New("secret expired")
	ErrDuplicate = errors.New("guid already exists")
)

// Store is the storage contract for OnceVault secrets.
type Store interface {
	// Put stores a secret; expires is UTC epoch seconds. Returns ErrDuplicate on guid collision.
	Put(ctx context.Context, guid, secret, iv string, expires int64) error
	// TakeOnce atomically fetches and deletes a secret. The expiry comparison must
	// happen inside the database engine, never in Go. Returns ErrExpired for a row
	// past its expiry (row deleted regardless) and ErrNotFound when no row exists.
	TakeOnce(ctx context.Context, guid string) (secret, iv string, err error)
	// PurgeExpired deletes all rows with expires <= now (DB-side comparison).
	PurgeExpired(ctx context.Context) (int64, error)
	// Vacuum reclaims space where the backend supports it; no-op otherwise.
	Vacuum(ctx context.Context) error
	Close() error
}

// Open returns the Store for the configured driver ("sqlite", "redis", "mysql", "postgres").
func Open(driver, dsn string) (Store, error) {
	switch driver {
	case "sqlite":
		return NewSQLite(dsn)
	case "redis":
		return NewRedis(dsn)
	case "mysql":
		return NewMySQL(dsn)
	case "postgres":
		return NewPostgres(dsn)
	default:
		return nil, fmt.Errorf("unknown db driver %q", driver)
	}
}
