package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	sqlite3 "modernc.org/sqlite" // registers driver "sqlite"; also provides the error type
)

// SQLite stores secrets in a single-file (or in-memory) SQLite database via the
// CGO-free modernc.org/sqlite driver.
type SQLite struct {
	db *sql.DB
}

const sqliteSchema = `CREATE TABLE IF NOT EXISTS secrets (
  guid    TEXT PRIMARY KEY,
  secret  TEXT NOT NULL,
  iv      TEXT NOT NULL,
  expires INTEGER NOT NULL
)`

// NewSQLite opens and auto-migrates the database at dsn (a file path or ":memory:").
func NewSQLite(dsn string) (*SQLite, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Single connection: serializes writers (avoids SQLITE_BUSY on the file DB)
	// and keeps an in-memory database alive for the process lifetime.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx := context.Background()
	stmts := []string{sqliteSchema,
		`CREATE INDEX IF NOT EXISTS idx_secrets_expires ON secrets(expires)`}
	if !sqliteIsMemoryDSN(dsn) {
		stmts = append([]string{
			`PRAGMA journal_mode=WAL`,
			`PRAGMA busy_timeout=5000`,
		}, stmts...)
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &SQLite{db: db}, nil
}

func sqliteIsMemoryDSN(dsn string) bool {
	return strings.Contains(dsn, ":memory:") || strings.Contains(dsn, "mode=memory")
}

// Put stores a secret inside a transaction; a guid collision maps to ErrDuplicate.
func (s *SQLite) Put(ctx context.Context, guid, secret, iv string, expires int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO secrets (guid, secret, iv, expires) VALUES (?, ?, ?, ?)`,
		guid, secret, iv, expires); err != nil {
		tx.Rollback()
		var se *sqlite3.Error
		if errors.As(err, &se) && se.Code()&0xff == 19 { // SQLITE_CONSTRAINT
			return ErrDuplicate
		}
		return err
	}
	return tx.Commit()
}

// TakeOnce deletes the row and returns its content in one statement; the expiry
// flag is computed by SQLite itself (unixepoch()), never by the Go clock.
func (s *SQLite) TakeOnce(ctx context.Context, guid string) (string, string, error) {
	var secret, iv string
	var expired int64
	err := s.db.QueryRowContext(ctx,
		`DELETE FROM secrets WHERE guid=? RETURNING secret, iv, (expires <= unixepoch())`,
		guid).Scan(&secret, &iv, &expired)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	if expired != 0 {
		return "", "", ErrExpired
	}
	return secret, iv, nil
}

// PurgeExpired deletes all rows past expiry (DB-side comparison) and returns the count.
func (s *SQLite) PurgeExpired(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM secrets WHERE expires <= unixepoch()`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Vacuum reclaims file space freed by deleted rows.
func (s *SQLite) Vacuum(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `VACUUM`)
	return err
}

// Close releases the database handle.
func (s *SQLite) Close() error {
	return s.db.Close()
}
