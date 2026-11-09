package store

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// LF-03: the blob sweep asks the uploads by hash for every blob; the question uses an index, not a scan
// of the table.
func TestUploadsBySHAUseAnIndex(t *testing.T) {
	s := openMigrated(t)
	rows, err := s.write.QueryContext(context.Background(), "EXPLAIN QUERY PLAN SELECT COUNT(*) FROM uploads WHERE sha256 = ?", "abc")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
		plan = append(plan, detail)
	}
	require.NoError(t, rows.Err())
	require.Contains(t, strings.Join(plan, "; "), "INDEX uploads_sha256")
}
