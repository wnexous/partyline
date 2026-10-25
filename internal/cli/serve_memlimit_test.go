package cli

import (
	"math"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/memlimit"
)

// keepMemoryLimit restores the memory limit of the test process when the test ends: serve sets it for the
// whole process.
func keepMemoryLimit(t *testing.T) {
	t.Helper()
	previous := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(previous) })
}

// OPS-06: serve gives the Go runtime the soft memory limit that PARTYLINE_MEMORY_LIMIT names and logs
// where it came from; off lifts a limit, and says nothing.
func TestServeAppliesTheConfiguredMemoryLimit(t *testing.T) {
	keepMemoryLimit(t)

	srv := startServeWith(t, t.TempDir(), map[string]string{"PARTYLINE_MEMORY_LIMIT": "300MiB"})
	require.Equal(t, int64(300<<20), debug.SetMemoryLimit(-1))
	line := findLog(logLines(t, srv.logs), "memory limit")
	require.NotNil(t, line, "logs: %s", srv.logs.String())
	require.Equal(t, float64(300<<20), line["bytes"])
	require.Equal(t, memlimit.SourceConfig, line["source"])
	require.Equal(t, 0, srv.stop())

	srv = startServeWith(t, t.TempDir(), map[string]string{"PARTYLINE_MEMORY_LIMIT": "off"})
	require.Equal(t, int64(math.MaxInt64), debug.SetMemoryLimit(-1))
	require.Nil(t, findLog(logLines(t, srv.logs), "memory limit"))
	require.Equal(t, 0, srv.stop())
}

// OPS-06: GOMEMLIMIT is the runtime's own variable, already applied when the process started: serve reports
// it and leaves it alone, whatever the container says.
func TestServeLeavesGOMEMLIMITAlone(t *testing.T) {
	keepMemoryLimit(t)
	debug.SetMemoryLimit(200 << 20) // what the runtime did with GOMEMLIMIT=200MiB at its start

	srv := startServeWith(t, t.TempDir(), map[string]string{"GOMEMLIMIT": "200MiB"})

	require.Equal(t, int64(200<<20), debug.SetMemoryLimit(-1))
	line := findLog(logLines(t, srv.logs), "memory limit")
	require.NotNil(t, line, "logs: %s", srv.logs.String())
	require.Equal(t, memlimit.SourceGoMemLimit, line["source"])
	require.Equal(t, 0, srv.stop())
}
