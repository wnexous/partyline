package cli

import (
	"io"
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

// ID-01 (independent test): two channels started in the same project without a name
// register as the temporary agents proj-claude and proj-claude-2 ("Proj · Claude Code" and
// "… #2"); the project comes from the git root even when the session starts in a
// subdirectory.
func TestChannelTemporaryNamesInSameProject(t *testing.T) {
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
	one := e.agent(t, "proj-claude")
	second := startChannel(t, nil, env, sub)
	initialize(second)
	two := e.agent(t, "proj-claude-2")
	require.Equal(t, map[string]bool{"proj-claude": true, "proj-claude-2": true}, e.onlineNames(t))
	require.Equal(t, []string{"Proj · Claude Code", "Proj · Claude Code #2"}, []string{one.DisplayName, two.DisplayName})
	require.Empty(t, first.stderr.String()+second.stderr.String(), "no home warning")
}

// CLI-01: --name and --project reach the server: the session is the fixed agent of that name
// in that project, whatever the working directory.
func TestChannelNameAndProjectFlags(t *testing.T) {
	e := newChannelEnv(t)
	c := startChannel(t, []string{"--name", "ruthe-bot", "--project", "Ruthe Videos"}, e.vars(), "/tmp/src/other")
	initialize(c)
	a := e.agent(t, "ruthe-bot")
	require.Equal(t, []any{dto.AgentKindFixed, "Ruthe Videos", "ruthe-videos", "claude"},
		[]any{a.Kind, a.Project.Name, a.Project.Slug, a.Harness})

	c2 := startChannel(t, []string{"--tools-only"}, e.vars("PARTYLINE_PROJECT", "Notes", "PARTYLINE_HARNESS", "codex"), "/tmp/src/other")
	initialize(c2)
	var got dto.Agent
	require.Eventually(t, func() bool {
		list := testkit.Decode[dto.List[dto.Agent]](e.alice.Get("/api/v1/agents").RequireStatus(http.StatusOK))
		for _, a := range list.Items {
			if a.Name == "notes-codex" {
				got = a
				return true
			}
		}
		return false
	}, chanWait, 20*time.Millisecond, "PARTYLINE_PROJECT and PARTYLINE_HARNESS name the tools-only session")
	require.Equal(t, []any{dto.AgentKindTemporary, "Notes · Codex"}, []any{got.Kind, got.DisplayName})
}

// ID-03: a session started in the home folder (or /) without --project or --name joins
// project "home" and prints one warning line; with --name it does not warn.
func TestChannelWarnsInTheHomeFolder(t *testing.T) {
	e := newChannelEnv(t)
	home := "/home/someone"
	c := startChannel(t, nil, e.vars("HOME", home), home)
	initialize(c)
	a := e.agent(t, "home-claude")
	require.Equal(t, "Home · Claude Code", a.DisplayName)
	require.Equal(t, "partyline channel: started in the home folder; using project \"home\" (pass --project <name> or --name <agent> to choose)\n", c.stderr.String())

	root := startListen(t, e.vars("HOME", home), "/", io.Discard)
	e.agent(t, "home-agent")
	require.Eventually(t, func() bool {
		return root.stderr.String() == "partyline listen: started in the home folder; using project \"home\" (pass --project <name> or --name <agent> to choose)\n"
	}, chanWait, 20*time.Millisecond, "stderr: %s", root.stderr.String())

	named := startChannel(t, []string{"--name", "helper"}, e.vars("HOME", home), home)
	initialize(named)
	h := e.agent(t, "helper")
	require.Equal(t, "home", h.Project.Slug)
	require.Empty(t, named.stderr.String())
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
