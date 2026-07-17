// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Günther Mair

//go:build driver_mysql || driver_all

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	mysqldrv "github.com/go-sql-driver/mysql" // registers driver "mysql"; also provides the error type
)

func init() {
	registry["mysql"] = func(dsn string) (Store, error) { return NewMySQL(dsn) }
}

// MySQL stores secrets in a MySQL/MariaDB database via go-sql-driver/mysql.
type MySQL struct {
	db *sql.DB
}

const mysqlSchema = `CREATE TABLE IF NOT EXISTS secrets (
  guid    CHAR(36) NOT NULL PRIMARY KEY,
  secret  MEDIUMTEXT NOT NULL,
  iv      TEXT NOT NULL,
  expires BIGINT NOT NULL,
  KEY idx_secrets_expires (expires)
) ENGINE=InnoDB`

// NewMySQL opens and auto-migrates the database for the given DSN
// (user:password@tcp(host:3306)/oncevault).
func NewMySQL(dsn string) (*MySQL, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	if _, err := db.ExecContext(context.Background(), mysqlSchema); err != nil {
		db.Close()
		return nil, err
	}
	return &MySQL{db: db}, nil
}

// Put stores a secret inside a transaction; MySQL error 1062 (duplicate key)
// maps to ErrDuplicate.
func (s *MySQL) Put(ctx context.Context, guid, secret, iv string, expires int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO secrets (guid, secret, iv, expires) VALUES (?, ?, ?, ?)`,
		guid, secret, iv, expires); err != nil {
		tx.Rollback()
		var me *mysqldrv.MySQLError
		if errors.As(err, &me) && me.Number == 1062 {
			return ErrDuplicate
		}
		return err
	}
	return tx.Commit()
}

// TakeOnce has no DELETE…RETURNING on MySQL, so atomicity comes from a single
// transaction: SELECT…FOR UPDATE locks the row, DELETE removes it, commit.
// The expiry flag is computed by MySQL (UNIX_TIMESTAMP()), never by the Go clock.
// The row is deleted even when expired; rollback on any error.
func (s *MySQL) TakeOnce(ctx context.Context, guid string) (string, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	var secret, iv string
	var expired int64
	err = tx.QueryRowContext(ctx,
		`SELECT secret, iv, (expires <= UNIX_TIMESTAMP()) FROM secrets WHERE guid=? FOR UPDATE`,
		guid).Scan(&secret, &iv, &expired)
	if errors.Is(err, sql.ErrNoRows) {
		tx.Rollback()
		return "", "", ErrNotFound
	}
	if err != nil {
		tx.Rollback()
		return "", "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM secrets WHERE guid=?`, guid); err != nil {
		tx.Rollback()
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	if expired != 0 {
		return "", "", ErrExpired
	}
	return secret, iv, nil
}

// PurgeExpired deletes all rows past expiry (DB-side comparison) and returns the count.
func (s *MySQL) PurgeExpired(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM secrets WHERE expires <= UNIX_TIMESTAMP()`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Vacuum defragments the table. OPTIMIZE TABLE returns a result set, so it is
// queried and drained rather than Exec'd.
func (s *MySQL) Vacuum(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `OPTIMIZE TABLE secrets`)
	if err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return rows.Err()
}

// Close releases the connection pool.
func (s *MySQL) Close() error {
	return s.db.Close()
}
