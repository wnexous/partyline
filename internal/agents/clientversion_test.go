package agents

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// SESS-63: a Claude Code or pi session is outdated when its client is older than v0.11.0 (the first one
// that sends the harness session id) or sends no id; a harness with no id to send never is.
func TestSessionOutdated(t *testing.T) {
	cases := map[string]struct {
		harness, sessionID, version string
		want                        bool
	}{
		"an old Claude Code client without an id":   {"claude", "", "", true},
		"an old Claude Code client with an id":      {"claude", "sess-a", "v0.10.5", true},
		"a client with no version header at all":    {"claude", "sess-a", "", true},
		"the first version that sends the id":       {"claude", "sess-a", "v0.11.0", false},
		"a newer one":                               {"claude", "sess-a", "v0.13.0", false},
		"a suffix after the version":                {"claude", "sess-a", "v0.11.0-rc1", false},
		"a local build":                             {"claude", "sess-a", "dev", false},
		"an unparsable version is not judged":       {"claude", "sess-a", "yesterday", false},
		"pi without an id":                          {"pi", "", "v0.11.0", true},
		"pi with an id from an old client":          {"pi", "pi-session", "v0.10.9", true},
		"OpenCode, whose harness has no id to send": {"opencode", "", "", false},
		"omp, whose harness has no id to send":      {"omp", "", "v0.10.0", false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, c.want, sessionOutdated(c.harness, c.sessionID, c.version))
		})
	}
}

func TestVersionOlderIgnoresTheLeadingVAndSuffixes(t *testing.T) {
	require.False(t, versionOlder("0.11.0", "v0.11.0"))
	require.False(t, versionOlder("v0.11", "v0.11.0"))
	require.False(t, versionOlder("v0.12.0", "v0.11.0"))
	require.True(t, versionOlder("v0.9.9", "v0.11.0"))
	require.True(t, versionOlder("v0.10.5+meta", "v0.11.0"))
}

// SESS-63: the version header is read leniently: absent or malformed counts as an old client and never
// refuses the connection.
func TestClientVersionFromHeader(t *testing.T) {
	h := http.Header{}
	require.Empty(t, ClientVersionFromHeader(h, dto.HeaderClientVersion))
	require.Empty(t, ClientVersionFromHeader(h, dto.HeaderClientVersion+"-absent"))
	for raw, want := range map[string]string{
		"v1.2.3":         "v1.2.3",
		" 0.13.0 ":       "0.13.0",
		"1.2.3-rc1":      "1.2.3-rc1",
		"abc1234":        "abc1234",
		"dev":            "dev",
		"not-a-version!": "",
	} {
		h.Set(dto.HeaderClientVersion, raw)
		require.Equal(t, want, ClientVersionFromHeader(h, dto.HeaderClientVersion), "header %q", raw)
	}
}
