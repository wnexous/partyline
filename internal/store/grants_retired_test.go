package store

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ORG-02, AD-016 (T51): nothing reads grants any more. Sharing is organization membership, so the grants a
// v0.5 installation holds mean nothing after the upgrade, and the only code that still names the table is what
// has to: the migrations that created, rebuilt, emptied and drop it, the upgrade hook that turns its rows into
// memberships (OUPG-01), and the tests of those. The scan reads every Go and SQL file under internal/, so a
// query or a service added later that reads or writes grants (or presets, or sharing) fails it. Reword a
// comment that only says the word.
func TestOnlyTheMigrationsAndTheBackfillNameGrants(t *testing.T) {
	retired := regexp.MustCompile(`grants|Sharing|SetVisibility`)
	internal, err := filepath.Abs("..")
	require.NoError(t, err)

	// What may name the retired code, as paths under internal/: the schema, the hook that reads the old rows and
	// the tests and fixtures that build an older database to upgrade it.
	allowed := func(rel string) bool {
		switch {
		case strings.HasPrefix(rel, "store/migrations/"), strings.HasPrefix(rel, "store/testdata/"):
			return true
		case rel == "store/organizations_backfill.go":
			return true
		case strings.HasPrefix(rel, "store/") && strings.HasSuffix(rel, "_test.go"):
			return true
		case rel == "api/upgrade_test.go": // upgrades a v0.5 database that holds grants (T53)
			return true
		}
		return false
	}

	scanned, allowedSeen := 0, 0
	var found []string
	require.NoError(t, filepath.WalkDir(internal, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(internal, path)
		require.NoError(t, err)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "webui" { // the embedded web app is built output, not code of the server
				return filepath.SkipDir
			}
			return nil
		}
		if ext := filepath.Ext(path); ext != ".go" && ext != ".sql" {
			return nil
		}
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		scanned++
		if !retired.Match(data) {
			return nil
		}
		if allowed(rel) {
			allowedSeen++
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			if retired.MatchString(line) {
				found = append(found, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	}))

	require.Greater(t, scanned, 300, "the scan did not walk internal/")
	require.NotZero(t, allowedSeen, "the migrations and the backfill still name the table: the scan sees them")
	require.Empty(t, found, "code outside the migrations and the backfill still names grants, sharing or presets")
}
