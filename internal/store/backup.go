package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultBackupsKeep is how many pre-migration backups stay in the backup directory when
// nothing else is configured: the newest five.
const DefaultBackupsKeep = 5

// BackupsDirName is the directory, inside the data directory, of the pre-migration backups.
const BackupsDirName = "backups"

// MigrationBackupOptions configures the copy of the database Migrate writes before it applies
// pending migrations (OPS-07). Open enables it, keeping the newest DefaultBackupsKeep copies;
// Skip turns it off and Keep 0 keeps every copy.
type MigrationBackupOptions struct {
	// Skip turns the backup off: the operator's PARTYLINE_SKIP_MIGRATION_BACKUP=1.
	Skip bool
	// Keep is how many backups stay in the directory after a successful new one; 0 keeps all.
	Keep int
	// Dir is where the backups go; empty means <database directory>/backups.
	Dir string
	// Logger receives the one line of a written backup and the warnings of the retention;
	// nil discards them.
	Logger *slog.Logger
	// Now returns the current time, used in the backup file name; nil means time.Now.
	Now func() time.Time
}

// backupBeforeMigrate writes a consistent copy of the database when at least one migration is
// pending and the database already has data (schema_migrations holds a version). A failure stops
// the migration: the operator keeps the old database instead of an untested upgrade.
func (s *Store) backupBeforeMigrate(ctx context.Context, fsys fs.FS) error {
	names, err := migrationFiles(fsys)
	if err != nil {
		return err
	}
	applied, err := appliedVersions(ctx, s.write)
	if err != nil {
		return err
	}
	pending := pendingVersions(names, applied)
	if len(pending) == 0 || len(applied) == 0 {
		// A fresh database has nothing to lose, and an up-to-date one runs no migration.
		return nil
	}
	from, to := latestVersion(applied), pending[len(pending)-1]
	if s.backup.Skip {
		s.backupLogger().Warn("migration backup skipped by PARTYLINE_SKIP_MIGRATION_BACKUP",
			"from", from, "to", to)
		return nil
	}
	if err := s.checkBackupSpace(); err != nil {
		return err
	}
	dir := s.backupDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return backupFailure(fmt.Errorf("create the backup directory: %w", err))
	}
	if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- a directory needs the execute bit, and only the server reads it
		return backupFailure(fmt.Errorf("restrict the backup directory %s: %w", dir, err))
	}
	path := filepath.Join(dir, backupName(from, to, s.backupNow()))
	if err := s.vacuumInto(ctx, path); err != nil {
		_ = os.Remove(path)
		return backupFailure(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = os.Remove(path)
		return backupFailure(fmt.Errorf("restrict the backup file %s: %w", path, err))
	}
	s.backupLogger().Info("migration backup written", "from", from, "to", to, "path", path)
	s.pruneBackups(dir, path)
	return nil
}

// vacuumInto writes a consistent copy of the database to path. VACUUM INTO reads one snapshot,
// so the WAL's committed changes are part of the copy.
func (s *Store) vacuumInto(ctx context.Context, path string) error {
	if _, err := s.write.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return err
	}
	return nil
}

// checkBackupSpace refuses the backup when the data directory cannot hold a copy of the
// database with room to spare: the copy needs its size, and the migration may need as much again.
func (s *Store) checkBackupSpace() error {
	size, err := databaseSize(s.path)
	if err != nil {
		return err
	}
	free, known := freeSpace(filepath.Dir(s.path))
	if !known {
		// The platform has no portable free-space call; the copy either fits or fails clearly.
		return nil
	}
	if need := 2 * size; free < need {
		return backupFailure(fmt.Errorf("not enough free space in %s: %s free, the backup needs %s",
			filepath.Dir(s.path), humanBytes(free), humanBytes(need)))
	}
	return nil
}

// backupFailure wraps a failed backup with the way out: the operator who accepts the risk (or
// restored a snapshot already) migrates without one.
func backupFailure(err error) error {
	return fmt.Errorf("back up the database before migrating (set PARTYLINE_SKIP_MIGRATION_BACKUP=1 "+
		"to migrate without a backup): %w", err)
}

// pruneBackups deletes the backups older than the newest Keep of dir, never the one just written
// (newest), even when the clock went backwards. It runs only after a successful new backup, so a
// failed one never costs an old copy. A failure to delete one is a warning: the new backup is what
// matters, and the server starts.
func (s *Store) pruneBackups(dir, newest string) {
	keep := s.backup.Keep
	if keep <= 0 {
		return
	}
	matches, err := filepath.Glob(filepath.Join(dir, "partyline-*.db"))
	if err != nil {
		s.backupLogger().Warn("migration backups: list failed", "dir", dir, "err", err)
		return
	}
	if len(matches) <= keep {
		return
	}
	sort.Slice(matches, func(i, j int) bool {
		// The name starts with the version, so only the moment orders backups taken from
		// different versions.
		if a, b := backupStamp(matches[i]), backupStamp(matches[j]); !a.Equal(b) {
			return a.After(b)
		}
		return matches[i] > matches[j]
	})
	for _, path := range matches[keep:] {
		if path == newest {
			continue
		}
		if err := os.Remove(path); err != nil {
			s.backupLogger().Warn("migration backups: could not delete an old backup", "path", path, "err", err)
		}
	}
}

// backupDir is where the backups go: the configured directory, else <database directory>/backups.
func (s *Store) backupDir() string {
	if s.backup.Dir != "" {
		return s.backup.Dir
	}
	return filepath.Join(filepath.Dir(s.path), BackupsDirName)
}

func (s *Store) backupLogger() *slog.Logger {
	if s.backup.Logger != nil {
		return s.backup.Logger
	}
	return slog.New(slog.DiscardHandler)
}

func (s *Store) backupNow() time.Time {
	if s.backup.Now != nil {
		return s.backup.Now()
	}
	return time.Now()
}

// backupName names one backup after the version it leaves and the version it reaches, plus the
// UTC moment. The moment is fixed-width, so retention can order the copies by when they were taken.
func backupName(from, to string, now time.Time) string {
	return fmt.Sprintf("partyline-%s-to-%s-%s.db", from, to, now.UTC().Format("20060102T150405.000000000Z"))
}

// backupStamp returns the UTC moment in a backup's name, or the zero time when the name is not
// one of ours.
func backupStamp(path string) time.Time {
	name := strings.TrimSuffix(filepath.Base(path), ".db")
	i := strings.LastIndex(name, "-")
	if i < 0 {
		return time.Time{}
	}
	stamp, err := time.Parse("20060102T150405.000000000Z", name[i+1:])
	if err != nil {
		return time.Time{}
	}
	return stamp
}

// pendingVersions returns the versions of names (migration file names) not in applied, in order.
func pendingVersions(names []string, applied map[string]bool) []string {
	var pending []string
	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		if !applied[version] {
			pending = append(pending, version)
		}
	}
	return pending
}

// latestVersion returns the highest applied version; the zero-padded file names sort like numbers.
func latestVersion(applied map[string]bool) string {
	var latest string
	for version := range applied {
		if version > latest {
			latest = version
		}
	}
	return latest
}

// databaseSize is the size of the database file and its WAL: the bytes a copy has to hold.
func databaseSize(path string) (int64, error) {
	var total int64
	for _, suffix := range []string{"", "-wal"} {
		info, err := os.Stat(path + suffix)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("measure the database: %w", err)
		}
		total += info.Size()
	}
	return total, nil
}

// humanBytes renders a size for an error message, in the units the configuration uses.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
