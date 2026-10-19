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

// Migrate applies the embedded migrations that are not yet recorded in schema_migrations.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.write.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	files, err := migrationFiles()
	if err != nil {
		return err
	}
	for _, name := range files {
		if err := s.applyMigration(ctx, name); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, name string) error {
	version := strings.TrimSuffix(name, ".sql")
	body, err := migrationsFS.ReadFile("migrations/" + name)
	if err != nil {
		return err
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var applied int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).Scan(&applied); err != nil {
		return err
	}
	if applied > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
		version, time.Now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

func migrationFiles() ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}
