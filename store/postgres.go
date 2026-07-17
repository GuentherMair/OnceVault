//go:build driver_postgres || driver_all

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // registers driver "pgx"
)

func init() {
	registry["postgres"] = func(dsn string) (Store, error) { return NewPostgres(dsn) }
}

// Postgres stores secrets in a PostgreSQL database via pgx's database/sql driver.
type Postgres struct {
	db *sql.DB
}

const postgresSchema = `CREATE TABLE IF NOT EXISTS secrets (
  guid    CHAR(36) PRIMARY KEY,
  secret  TEXT NOT NULL,
  iv      TEXT NOT NULL,
  expires BIGINT NOT NULL
)`

// NewPostgres opens and auto-migrates the database for the given DSN
// (postgres://user:password@host:5432/oncevault).
func NewPostgres(dsn string) (*Postgres, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	ctx := context.Background()
	for _, stmt := range []string{postgresSchema,
		`CREATE INDEX IF NOT EXISTS idx_secrets_expires ON secrets(expires)`} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Postgres{db: db}, nil
}

// Put stores a secret inside a transaction; SQLSTATE 23505 (unique violation)
// maps to ErrDuplicate.
func (s *Postgres) Put(ctx context.Context, guid, secret, iv string, expires int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO secrets (guid, secret, iv, expires) VALUES ($1, $2, $3, $4)`,
		guid, secret, iv, expires); err != nil {
		tx.Rollback()
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrDuplicate
		}
		return err
	}
	return tx.Commit()
}

// TakeOnce deletes the row and returns its content in one statement; the expiry
// flag is computed by PostgreSQL (now()), never by the Go clock.
func (s *Postgres) TakeOnce(ctx context.Context, guid string) (string, string, error) {
	var secret, iv string
	var expired bool
	err := s.db.QueryRowContext(ctx,
		`DELETE FROM secrets WHERE guid=$1 RETURNING secret, iv, (expires <= EXTRACT(EPOCH FROM now())::bigint)`,
		guid).Scan(&secret, &iv, &expired)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	if expired {
		return "", "", ErrExpired
	}
	return secret, iv, nil
}

// PurgeExpired deletes all rows past expiry (DB-side comparison) and returns the count.
func (s *Postgres) PurgeExpired(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM secrets WHERE expires <= EXTRACT(EPOCH FROM now())::bigint`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Vacuum reclaims dead tuples. Issued argument-free so pgx uses the simple query
// protocol — VACUUM cannot run as a prepared statement or inside a transaction.
func (s *Postgres) Vacuum(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `VACUUM secrets`)
	return err
}

// Close releases the connection pool.
func (s *Postgres) Close() error {
	return s.db.Close()
}
