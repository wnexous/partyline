// Package store owns the SQLite database: connection pools, migrations and transactions.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/wnexous/partyline/internal/store/db"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const pragmas = "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"

// Store holds a read pool (4 connections) and a single-connection write pool.
type Store struct {
	read  *sql.DB
	write *sql.DB
	rq    *db.Queries
}

// Open opens the database file at path. Call Migrate before using it.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?" + pragmas
	write, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open write pool: %w", err)
	}
	write.SetMaxOpenConns(1)
	// The write pool connects first so the file and WAL mode exist before readers open it.
	if err := write.Ping(); err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	read, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("open read pool: %w", err)
	}
	read.SetMaxOpenConns(4)
	return &Store{read: read, write: write, rq: db.New(read)}, nil
}

// Close closes both pools.
func (s *Store) Close() error {
	rerr := s.read.Close()
	if err := s.write.Close(); err != nil {
		return err
	}
	return rerr
}

// R returns queries bound to the read pool.
func (s *Store) R() *db.Queries { return s.rq }

// WithTx runs fn inside a write transaction (BEGIN IMMEDIATE). It commits when fn
// returns nil and rolls back otherwise.
func (s *Store) WithTx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := fn(db.New(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ReadTx runs fn inside a read-only transaction on the read pool. Every query in fn reads
// the same snapshot (WAL): writes committed meanwhile are not seen.
func (s *Store) ReadTx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return fn(db.New(tx))
}

// Hook is the Go step of a migration. It runs in the migration transaction right after the
// SQL file of the same version and must use raw SQL only: sqlc queries follow the latest
// schema, not the schema of its migration.
type Hook func(ctx context.Context, tx *sql.Tx) error

// hooks maps a migration version (the file name without ".sql") to its Go step.
var hooks = map[string]Hook{
	"0015_intelligences": backfillIntelligences,
}

// migrations holds the embedded migration files at its root.
var migrations = func() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(err)
	}
	return sub
}()

// Migrate applies every embedded migration not yet recorded in schema_migrations, with their
// Go hooks, in one write transaction (MIG-05, AD-012): any error rolls all of them back.
func (s *Store) Migrate(ctx context.Context) error {
	return s.migrate(ctx, migrations, hooks)
}

// MigrateBefore applies the embedded migrations older than version (e.g. "0015"), in one
// transaction like Migrate. Upgrade tests use it to build databases of an older schema.
func (s *Store) MigrateBefore(ctx context.Context, version string) error {
	return s.migrate(ctx, olderThan{FS: migrations, version: version}, hooks)
}

// olderThan lists only the files of FS whose names sort before version.
type olderThan struct {
	fs.FS
	version string
}

func (o olderThan) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(o.FS, name)
	if err != nil {
		return nil, err
	}
	var kept []fs.DirEntry
	for _, e := range entries {
		if e.Name() < o.version {
			kept = append(kept, e)
		}
	}
	return kept, nil
}

// migrate applies the pending *.sql files of fsys in name order, each followed by its hook,
// records each version and commits once.
func (s *Store) migrate(ctx context.Context, fsys fs.FS, hooks map[string]Hook) error {
	names, err := migrationFiles(fsys)
	if err != nil {
		return err
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migrations: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	applied, err := appliedVersions(ctx, tx)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		if applied[version] {
			continue
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if hook := hooks[version]; hook != nil {
			if err := hook(ctx, tx); err != nil {
				return fmt.Errorf("migration %s hook: %w", name, err)
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
			version, now); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

func appliedVersions(ctx context.Context, tx *sql.Tx) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	applied := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// migrationFiles lists the *.sql files at the root of fsys in name order.
func migrationFiles(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
