// Package database owns Nightloch's SQLite connection and schema migrations.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// ErrMigrationsPending means the schema is older than this binary.
var ErrMigrationsPending = errors.New("database migrations are pending; run `nightloch migrate`")

// Open opens a serialized SQLite connection with the runtime safety pragmas.
func Open(path string) (*sql.DB, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// A single connection makes write transactions and rate-limit increments
	// deterministic. Nightloch is intentionally a single-process control plane.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure sqlite (%s): %w", pragma, err)
		}
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if path != ":memory:" {
		if err := os.Chmod(path, 0o600); err != nil {
			db.Close()
			return nil, fmt.Errorf("protect sqlite database: %w", err)
		}
	}
	return db, nil
}

// Migrate applies every forward-only schema migration in order.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	if err := checkSchemaVersion(ctx, db, false); err != nil && !errors.Is(err, ErrMigrationsPending) {
		return err
	}

	for index, migration := range migrations {
		version := index + 1
		var applied int
		err := db.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE version = ?", version).Scan(&applied)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read migration %d: %w", version, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, migration); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES (?, unixepoch())", version); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", version, err)
		}
	}
	return checkSchemaVersion(ctx, db, true)
}

// CheckCurrent refuses to serve with a missing, old, or unexpectedly new schema.
func CheckCurrent(ctx context.Context, db *sql.DB) error {
	return checkSchemaVersion(ctx, db, true)
}

func checkSchemaVersion(ctx context.Context, db *sql.DB, requireCurrent bool) error {
	var version, count int
	err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0), COUNT(*) FROM schema_migrations").Scan(&version, &count)
	if err != nil {
		return ErrMigrationsPending
	}
	if version > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than binary version %d", version, len(migrations))
	}
	if count != version {
		return fmt.Errorf("database migration history is incomplete (max version %d, applied count %d)", version, count)
	}
	if requireCurrent && version < len(migrations) {
		return ErrMigrationsPending
	}
	return nil
}

// Writable verifies that SQLite can execute a write transaction without
// changing durable state. It is the database component of /health.
func Writable(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin health transaction: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE health_probe SET touched_at = unixepoch() WHERE id = 1"); err != nil {
		tx.Rollback()
		return fmt.Errorf("write health probe: %w", err)
	}
	if err := tx.Rollback(); err != nil {
		return fmt.Errorf("roll back health transaction: %w", err)
	}
	return nil
}

// Snapshot writes a transactionally consistent SQLite copy to a new file.
// It refuses to overwrite any existing filesystem entry.
func Snapshot(ctx context.Context, db *sql.DB, destination string) error {
	destination = filepath.Clean(destination)
	if destination == "." || destination == string(filepath.Separator) {
		return errors.New("backup destination must be a file path")
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("backup destination already exists: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect backup destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", destination); err != nil {
		// The path did not exist before this call. Remove only a partial file
		// created by the failed VACUUM, if SQLite left one behind.
		_ = os.Remove(destination)
		return fmt.Errorf("create SQLite snapshot: %w", err)
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		return fmt.Errorf("protect SQLite snapshot: %w", err)
	}
	return nil
}
