// Package store owns the SQLite database: connection, migrations, and the
// queries that back authentication.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type DB struct{ *sql.DB }

// Open connects to the SQLite database at path and brings it up to schema.
func Open(ctx context.Context, path string) (*DB, error) {
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"

	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// SQLite takes one writer at a time; a single connection avoids contending
	// with ourselves and makes busy_timeout meaningful.
	sdb.SetMaxOpenConns(1)
	sdb.SetConnMaxLifetime(0)

	if err := sdb.PingContext(ctx); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("ping %s: %w", path, err)
	}

	db := &DB{sdb}
	if err := db.migrate(ctx); err != nil {
		sdb.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) migrate(ctx context.Context) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)

	for _, name := range entries {
		var applied int
		if err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM schema_migrations WHERE name = ?`, name).Scan(&applied); err != nil {
			return err
		}
		if applied > 0 {
			continue
		}

		body, err := migrationsFS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit %s: %w", name, err)
		}
	}
	return nil
}

// Audit records an event. Failures are returned but callers routinely ignore
// them: never fail a real operation because its audit row did not land.
func (db *DB) Audit(ctx context.Context, event, actor, detailJSON string) error {
	if detailJSON == "" {
		detailJSON = "{}"
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO audit_events (id, event, actor, detail_json) VALUES (?, ?, ?, ?)`,
		newID(), event, actor, detailJSON)
	return err
}

// SweepExpired removes rows whose time has passed. Cheap enough to run on a timer.
func (db *DB) SweepExpired(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, q := range []string{
		`DELETE FROM sessions WHERE expires_at < ?`,
		`DELETE FROM webauthn_states WHERE expires_at < ?`,
		`DELETE FROM enrollment_codes WHERE expires_at < ? AND used_at IS NULL`,
	} {
		if _, err := db.ExecContext(ctx, q, now); err != nil {
			return err
		}
	}
	return nil
}
