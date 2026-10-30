package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// openStore opens the database at path and closes it at cleanup.
func openStore(t *testing.T, path string, opts ...Option) *Store {
	t.Helper()
	s, err := Open(path, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// backupPaths lists the pre-migration backups of a data directory.
func backupPaths(t *testing.T, dataDir string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dataDir, BackupsDirName, "partyline-*.db"))
	require.NoError(t, err)
	return paths
}

// openBackup opens a backup read-only, so the test never changes it.
func openBackup(t *testing.T, path string) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	return raw
}

// migrateToLatestMinusOne leaves a database on the second-to-last schema, with exactly one
// pending migration, and returns the version it has and the one the next Migrate reaches.
func migrateToLatestMinusOne(t *testing.T, s *Store) (from, to string) {
	t.Helper()
	files, err := migrationFiles(migrations)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(files), 2)
	to = strings.TrimSuffix(files[len(files)-1], ".sql")
	from = strings.TrimSuffix(files[len(files)-2], ".sql")
	require.NoError(t, s.MigrateBefore(context.Background(), to))
	return from, to
}

// OPS-07: a database with data and a pending migration is copied before the migration runs; the
// copy opens, has the old version and the same rows, and is private to the server.
func TestMigrateBacksUpADatabaseWithData(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	s := openStore(t, filepath.Join(dataDir, "partyline.db"))
	from, to := migrateToLatestMinusOne(t, s)
	_, err := s.write.ExecContext(ctx,
		`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('u-alice', 'alice', 'x', 'member', 1)`)
	require.NoError(t, err)

	require.NoError(t, s.Migrate(ctx))

	paths := backupPaths(t, dataDir)
	require.Len(t, paths, 1)
	require.True(t, strings.HasPrefix(filepath.Base(paths[0]), "partyline-"+from+"-to-"+to+"-"), paths[0])
	info, err := os.Stat(paths[0])
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the backup is private")
	dirInfo, err := os.Stat(filepath.Join(dataDir, BackupsDirName))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm(), "the backup directory is private")

	backup := openBackup(t, paths[0])
	var version string
	require.NoError(t, backup.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&version))
	require.Equal(t, from, version, "the copy has the schema the migration started from")
	var username string
	require.NoError(t, backup.QueryRowContext(ctx, "SELECT username FROM users WHERE id = 'u-alice'").Scan(&username))
	require.Equal(t, "alice", username, "the copy has the rows the migration started from")
	var integrity string
	require.NoError(t, backup.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity))
	require.Equal(t, "ok", integrity)

	// The live database went on to the new version.
	var live string
	require.NoError(t, s.write.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&live))
	require.Equal(t, to, live)
}

// OPS-07: a fresh database has nothing to lose, and an up-to-date one runs no migration, so
// neither writes a backup.
func TestMigrateWithoutDataOrWithoutPendingWritesNoBackup(t *testing.T) {
	ctx := context.Background()

	t.Run("fresh database", func(t *testing.T) {
		dataDir := t.TempDir()
		s := openStore(t, filepath.Join(dataDir, "partyline.db"))
		require.NoError(t, s.Migrate(ctx))
		require.Empty(t, backupPaths(t, dataDir))
	})

	t.Run("nothing pending", func(t *testing.T) {
		dataDir := t.TempDir()
		s := openStore(t, filepath.Join(dataDir, "partyline.db"))
		require.NoError(t, s.Migrate(ctx))
		_, err := s.write.ExecContext(ctx,
			`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('u-alice', 'alice', 'x', 'member', 1)`)
		require.NoError(t, err)
		require.NoError(t, s.Migrate(ctx))
		require.Empty(t, backupPaths(t, dataDir))
	})
}

// OPS-07: a backup that cannot be written stops the migration; the operator keeps the old
// database and an error that names the way out.
func TestMigrateStopsWhenTheBackupFails(t *testing.T) {
	ctx := context.Background()

	t.Run("the backup directory cannot be created", func(t *testing.T) {
		dataDir := t.TempDir()
		s := openStore(t, filepath.Join(dataDir, "partyline.db"))
		from, _ := migrateToLatestMinusOne(t, s)
		// A regular file where the directory goes makes the creation fail whatever the user is.
		require.NoError(t, os.WriteFile(filepath.Join(dataDir, BackupsDirName), []byte("not a directory"), 0o600))

		err := s.Migrate(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "PARTYLINE_SKIP_MIGRATION_BACKUP")
		applied := versions(t, s)
		require.Equal(t, from, applied[len(applied)-1], "the pending migration did not run")
		require.Empty(t, backupPaths(t, dataDir))
	})

	t.Run("the copy cannot be written", func(t *testing.T) {
		dataDir := t.TempDir()
		now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		s := openStore(t, filepath.Join(dataDir, "partyline.db"),
			WithMigrationBackup(MigrationBackupOptions{Keep: 5, Now: func() time.Time { return now }}))
		from, to := migrateToLatestMinusOne(t, s)
		// An empty directory where VACUUM INTO would write makes the copy fail.
		require.NoError(t, os.MkdirAll(filepath.Join(dataDir, BackupsDirName, backupName(from, to, now)), 0o700))

		err := s.Migrate(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "PARTYLINE_SKIP_MIGRATION_BACKUP")
		applied := versions(t, s)
		require.Equal(t, from, applied[len(applied)-1], "the pending migration did not run")
		require.NotContains(t, applied, to)
	})
}

// OPS-07: retention keeps the newest 5 backups and deletes the older ones only after a new
// backup succeeded.
func TestMigrateBackupRetentionKeepsTheNewestFive(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := openStore(t, filepath.Join(dataDir, "partyline.db"),
		WithMigrationBackup(MigrationBackupOptions{Keep: DefaultBackupsKeep, Now: func() time.Time { return now }}))
	from, to := migrateToLatestMinusOne(t, s)

	backupsDir := filepath.Join(dataDir, BackupsDirName)
	require.NoError(t, os.MkdirAll(backupsDir, 0o700))
	// The names sort after the new backup, so only the moment of each can order them.
	var old []string
	for i := 1; i <= 5; i++ {
		name := filepath.Join(backupsDir, "partyline-9999-to-9999-2020010"+string(rune('0'+i))+"T000000.000000000Z.db")
		require.NoError(t, os.WriteFile(name, []byte("old"), 0o600))
		old = append(old, name)
	}

	require.NoError(t, s.Migrate(ctx))

	paths := backupPaths(t, dataDir)
	require.Len(t, paths, DefaultBackupsKeep)
	require.NoFileExists(t, old[0], "the oldest backup went")
	for _, kept := range old[1:] {
		require.FileExists(t, kept)
	}
	require.FileExists(t, filepath.Join(backupsDir, backupName(from, to, now)), "the new backup stays")

	// Keep 0 keeps every backup.
	t.Run("keep all", func(t *testing.T) {
		s := &Store{backup: MigrationBackupOptions{Keep: 0}}
		dir := t.TempDir()
		for i := 0; i < 7; i++ {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "partyline-0001-to-0002-2020010"+
				string(rune('1'+i))+"T000000.000000000Z.db"), []byte("old"), 0o600))
		}
		s.pruneBackups(dir, "")
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 7)
	})
}

// OPS-07: PARTYLINE_SKIP_MIGRATION_BACKUP=1 (here its store option) migrates without a backup.
func TestMigrateBackupCanBeSkipped(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	s := openStore(t, filepath.Join(dataDir, "partyline.db"),
		WithMigrationBackup(MigrationBackupOptions{Skip: true}))
	_, to := migrateToLatestMinusOne(t, s)
	require.NoError(t, s.Migrate(ctx))
	require.Empty(t, backupPaths(t, dataDir))
	applied := versions(t, s)
	require.Equal(t, to, applied[len(applied)-1], "the migration ran")
}
