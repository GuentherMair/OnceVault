// Package store provides the storage backends for OnceVault secrets.
// This file is the frozen contract (docs/PLAN_STEP_0.md §0.3) — do not modify.
package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
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

// registry holds the constructors contributed by whichever driver files were
// compiled in via build tags (driver_sqlite / driver_mysql / driver_postgres /
// driver_redis, or driver_all for the full set).
var registry = map[string]func(string) (Store, error){}

// Open returns the Store for the configured driver name. The driver must have
// been enabled at build time via the corresponding build tag; otherwise an
// "unknown db driver" error is returned.
func Open(driver, dsn string) (Store, error) {
	b, ok := registry[driver]
	if !ok {
		return nil, fmt.Errorf("unknown db driver %q (built: %s)", driver, builtDrivers())
	}
	return b(dsn)
}

// builtDrivers returns the sorted list of drivers enabled at build time, for
// the error message above.
func builtDrivers() string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "<none — rebuild with -tags driver_all or -tags driver_<name>>"
	}
	return fmt.Sprintf("%v", names)
}
