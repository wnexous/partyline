package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/testkit"
)

// ONB-03: GET /install.sh serves the installer to anyone, with this instance's URL as the
// default for setup.
func TestInstallScriptServedWithInstanceURL(t *testing.T) {
	env := testkit.New(t)
	resp := env.Anon().Get("/install.sh").RequireStatus(http.StatusOK)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	body := string(resp.Body)
	require.True(t, strings.HasPrefix(body, "#!/bin/sh\n"), "a shell script")
	require.Contains(t, body, "DEFAULT_URL='"+env.URL+"'\n")
	require.NotContains(t, body, "__PARTYLINE_URL__")
}
