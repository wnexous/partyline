package cli

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

func runAgents(env map[string]string, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = execute(context.Background(), append([]string{"partyline", "agents"}, args...), stdio{
		stdout: &out, stderr: &errOut, getenv: func(k string) string { return env[k] },
	})
	return code, out.String(), errOut.String()
}

// aiOn creates alice's AI of the folder on a machine with its home and harness, and names it
// (display name and handle).
func (e *channelEnv) aiOn(t *testing.T, machine, folder, home, harness, name, handle string) db.Agent {
	t.Helper()
	a, err := e.env.App.Agents.ResolveFolder(context.Background(), e.alice.User.ID,
		agents.FolderID{Machine: machine, Folder: folder, Home: home, Harness: harness}, false)
	require.NoError(t, err)
	p := authn.Principal{Kind: authn.KindUser, UserID: e.alice.User.ID, Role: string(e.alice.User.Role)}
	_, err = e.env.App.Intelligences.Update(context.Background(), p, a.ID, intelligences.ProfileUpdate{DisplayName: &name, Handle: &handle})
	require.NoError(t, err)
	return a
}

// seenAgo records that the AI went offline ago.
func (e *channelEnv) seenAgo(t *testing.T, id string, ago time.Duration) {
	t.Helper()
	at := time.Now().Add(-ago).UnixMilli()
	require.NoError(t, e.env.App.Store.WithTx(context.Background(), func(q *db.Queries) error {
		return q.TouchAgent(context.Background(), db.TouchAgentParams{LastSeenAt: sql.NullInt64{Int64: at, Valid: true}, ID: id})
	}))
}

// TREE-07: `partyline agents` prints the owner's AIs as a machine › folder tree, machines in
// the TREE-03 order, two spaces per level, online markers, tab-aligned columns (name with the
// collapsed path, handle, app, state and folder) and the unlinked identities last.
func TestAgentsPrintsTheMachineTree(t *testing.T) {
	e := newChannelEnv(t)
	atlas := e.aiOn(t, "devbox", "/home/alice/atlas", "/home/alice", "claude", "Atlas", "atlas")
	notes := e.aiOn(t, "devbox", "/home/alice/atlas/notes", "/home/alice", "opencode", "Notes", "notes")
	lab := e.aiOn(t, "devbox", "/home/alice/c/d/lab", "/home/alice", "codex", "Lab", "lab")
	dev := e.aiOn(t, "notebook", "/home/alice", "/home/alice", "claude", "Dev", "dev")
	e.legacy(t, "Old helper")
	e.alice.Put("/api/v1/machines/devbox", dto.SetMachineLabelRequest{Label: "Studio"}).RequireStatus(http.StatusOK)
	e.seenAgo(t, notes.ID, 2*time.Hour)
	e.seenAgo(t, lab.ID, 3*24*time.Hour)
	e.seenAgo(t, dev.ID, 5*24*time.Hour)

	conn, resp := e.alice.WithBearer(e.secret).DialFolder(testkit.Folder{Machine: "devbox", Path: "/home/alice/atlas", Home: "/home/alice", Harness: "claude"})
	require.NotNil(t, conn, "upgrade refused: %d", resp.Status)
	defer conn.Close()
	conn.Welcome(chanWait)
	require.Eventually(t, func() bool { return e.env.App.Presence.Online(atlas.ID) }, chanWait, 20*time.Millisecond)

	code, out, errOut := runAgents(e.vars())
	require.Equal(t, 0, code, errOut)
	require.Equal(t, `Studio (devbox)
  ● Atlas      @atlas  Claude Code  online  ~/atlas
    ○ Notes    @notes  OpenCode     2h ago  ~/atlas/notes
  ○ c/d › Lab  @lab    Codex        3d ago  ~/c/d/lab
notebook
  ○ Dev  @dev  Claude Code  5d ago  ~
Unlinked
  ○ Old helper  @old-helper
`, out)
}

// TREE-07: an owner without AIs reads how to start one.
func TestAgentsEmptyList(t *testing.T) {
	e := newChannelEnv(t)
	code, out, errOut := runAgents(e.vars())
	require.Equal(t, 0, code, errOut)
	require.Equal(t, "No AIs yet: start a session with partyline claude\n", out)
}

// TREE-03: the state column reads online, now, Nm, Nh or Nd; never seen is empty.
func TestRelativeLastSeen(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		ago  time.Duration
		want string
	}{
		{30 * time.Second, "now"},
		{90 * time.Second, "1m ago"},
		{59 * time.Minute, "59m ago"},
		{61 * time.Minute, "1h ago"},
		{23 * time.Hour, "23h ago"},
		{25 * time.Hour, "1d ago"},
		{6 * 24 * time.Hour, "6d ago"},
	} {
		seen := now.Add(-c.ago)
		require.Equal(t, c.want, relativeLastSeen(&seen, now), "seen %s ago", c.ago)
	}
	require.Empty(t, relativeLastSeen(nil, now), "never seen")
}

// TREE-07: without credentials the command explains how to set them; a rejected token is an
// error.
func TestAgentsNeedsCredentials(t *testing.T) {
	code, _, errOut := runAgents(map[string]string{})
	require.Equal(t, 2, code)
	require.Contains(t, errOut, "partyline agents: set PARTYLINE_URL and PARTYLINE_TOKEN")

	e := newChannelEnv(t)
	code, _, errOut = runAgents(map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": "pl_agent_wrong"})
	require.Equal(t, 1, code)
	require.Contains(t, errOut, "partyline agents: the server answered 401")
}
