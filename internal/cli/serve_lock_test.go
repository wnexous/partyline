package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/datalock"
)

// OPS-05: a second `partyline serve` on a data directory that a running server uses exits 1 at once,
// naming the directory and what to do, without listening or touching the first server; `partyline
// admin`, which does not run a server, still works against it; and once the first server stopped the
// directory is free again.
func TestServeRefusesADataDirectoryAnotherServerUses(t *testing.T) {
	dir := t.TempDir()
	first := startServe(t, dir)

	env := map[string]string{"PARTYLINE_ADDR": "127.0.0.1:0", "PARTYLINE_DATA_DIR": dir, "PARTYLINE_LOG_FORMAT": "json"}
	logs := &syncBuffer{}
	// The timeout only keeps a broken lock, which would let this server run, from hanging the test.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	code := execute(ctx, []string{"partyline", "serve"}, stdio{
		stdin: strings.NewReader(""), stdout: logs, stderr: logs, getenv: func(k string) string { return env[k] },
	})

	require.Equal(t, 1, code, "logs: %s", logs.String())
	failed := findLog(logLines(t, logs), "start failed")
	require.NotNil(t, failed, "logs: %s", logs.String())
	require.Contains(t, failed["err"], dir)
	require.Contains(t, failed["err"], "already in use by another partyline process")
	require.Contains(t, failed["err"], datalock.FileName)
	require.Nil(t, findLog(logLines(t, logs), msgListening), "the second server did not listen")

	require.Equal(t, http.StatusOK, first.do(t, http.MethodGet, "/healthz", "", "", nil).StatusCode, "the first server is untouched")

	code, out := runWithStdin(t, dir, "carols-password-1\n", "admin", "create-user", "-username", "carol")
	require.Equal(t, 0, code, "admin works against a running server: %s", out)

	require.Equal(t, 0, first.stop())
	second := startServe(t, dir)
	require.Equal(t, 0, second.stop())
}
