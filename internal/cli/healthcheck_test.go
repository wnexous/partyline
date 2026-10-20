package cli

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func healthcheckAgainst(t *testing.T, addr string) int {
	t.Helper()
	var out bytes.Buffer
	env := map[string]string{"PARTYLINE_ADDR": addr}
	return execute(context.Background(), []string{"partyline", "healthcheck"}, stdio{
		stdin: strings.NewReader(""), stdout: &out, stderr: &out, getenv: func(k string) string { return env[k] },
	})
}

// OPS-03: `partyline healthcheck` (the container HEALTHCHECK) exits 0 when /healthz answers
// 200 and 1 otherwise.
func TestHealthcheck(t *testing.T) {
	var status int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	require.NoError(t, err)

	status = http.StatusOK
	require.Equal(t, 0, healthcheckAgainst(t, ":"+port), "an empty host means this machine")
	require.Equal(t, 0, healthcheckAgainst(t, "127.0.0.1:"+port))
	require.Equal(t, 0, healthcheckAgainst(t, "0.0.0.0:"+port))

	status = http.StatusServiceUnavailable
	require.Equal(t, 1, healthcheckAgainst(t, ":"+port))

	srv.Close()
	require.Equal(t, 1, healthcheckAgainst(t, ":"+port), "nothing listening")
}
