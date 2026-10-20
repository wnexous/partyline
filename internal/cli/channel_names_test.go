package cli

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

func (e *channelEnv) onlineNames(t *testing.T) map[string]bool {
	t.Helper()
	list := testkit.Decode[dto.List[dto.Agent]](e.alice.Get("/api/v1/agents").RequireStatus(http.StatusOK))
	names := map[string]bool{}
	for _, a := range list.Items {
		names[a.Name] = a.Online
	}
	return names
}

// NAME-01, NAME-02 (independent test): two channels started in the same project without
// PARTYLINE_AGENT register as proj and proj-2; the name comes from the git root even when the
// session starts in a subdirectory.
func TestChannelDerivedNamesInSameProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	parent := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", parent)
	repo := filepath.Join(parent, "proj")
	sub := filepath.Join(repo, "internal", "api")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	out, err := exec.Command("git", "init", "-q", repo).CombinedOutput()
	require.NoError(t, err, string(out))

	e := newChannelEnv(t)
	env := map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": e.secret}
	first := startChannel(t, nil, env, repo)
	initialize(first)
	e.agent(t, "proj")
	second := startChannel(t, nil, env, sub)
	initialize(second)
	e.agent(t, "proj-2")
	require.Equal(t, map[string]bool{"proj": true, "proj-2": true}, e.onlineNames(t))
}

// NAME-03: an explicit PARTYLINE_AGENT keeps "newest wins": the second session replaces the
// first and no suffixed agent appears.
func TestChannelExplicitNameReplaces(t *testing.T) {
	e := newChannelEnv(t)
	env := map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": e.secret, "PARTYLINE_AGENT": "proj"}
	first := startChannel(t, nil, env, "/tmp/wd")
	initialize(first)
	e.agent(t, "proj")
	second := startChannel(t, nil, env, "/tmp/wd")
	initialize(second)

	require.Eventually(t, func() bool {
		return strings.Contains(first.stderr.String(), "another session connected as agent proj")
	}, chanWait, 20*time.Millisecond, "the first session is replaced")
	require.Equal(t, map[string]bool{"proj": true}, e.onlineNames(t))
}
