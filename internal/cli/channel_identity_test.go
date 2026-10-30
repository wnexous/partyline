package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel"
)

// canonicalDir creates dir under a fresh temp dir and returns its canonical path.
func canonicalDir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(append([]string{t.TempDir()}, parts...)...)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	return resolved
}

// AID-07: a process sends the canonical folder: a symlinked working directory and a
// PARTYLINE_FOLDER with a trailing slash both reach the server as the resolved absolute path,
// and the folder (spaces, non-ASCII) names the AI.
func TestChannelSendsTheCanonicalFolder(t *testing.T) {
	e := newChannelEnv(t)
	target := canonicalDir(t, "the lab", "Café Órdenes")
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(target, link))

	fromLink := startChannel(t, nil, e.launched("claude"), link)
	initialize(fromLink)
	prof := e.ai(t, target)
	require.Equal(t, []any{"Café Órdenes", "cafe-ordenes"}, []any{prof.DisplayName, prof.Handle})
	require.Equal(t, testMachine, e.machineOf(t, prof.ID), "the channel reports its machine")
	require.Equal(t, []string{target}, e.folders(t), "the symlink is not another folder")

	// The launcher's PARTYLINE_FOLDER wins over the working directory, whatever its spelling.
	fromEnv := startChannel(t, []string{"--tools-only"}, e.launched("claude", "PARTYLINE_FOLDER", link+"/"), "/tmp/src/elsewhere")
	initialize(fromEnv)
	fromEnv.send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "list_rooms", "arguments": map[string]any{}}})
	require.Equal(t, false, fromEnv.next()["result"].(map[string]any)["isError"])
	require.Equal(t, []string{target}, e.folders(t), "a trailing-slash PARTYLINE_FOLDER through a symlink is the same folder")
	require.Equal(t, prof.ID, e.registered(t, target).ID)
}

// The home folder travels too: the tree shows the AI's folder as "~/…" under it (TREE-02).
func TestChannelSendsTheHomeFolder(t *testing.T) {
	e := newChannelEnv(t)
	home := canonicalDir(t, "home", "alice")
	work := filepath.Join(home, "src", "acme")
	require.NoError(t, os.MkdirAll(work, 0o755))
	initialize(startChannel(t, nil, e.launched("claude", "HOME", home), work))
	require.Equal(t, "~/src/acme", e.ai(t, work).AI.FolderLabel)
}

// AID-07, CA-04: `partyline setup` and `partyline runner` name the machine they get approved for;
// inside a container that is the AI's machine (PARTYLINE_MACHINE), never the container's hostname.
func TestMachineNameUsesPARTYLINE_MACHINE(t *testing.T) {
	std := func(env map[string]string, hostname func() (string, error)) stdio {
		return stdio{getenv: func(k string) string { return env[k] }, hostname: hostname}
	}
	require.Equal(t, "container:pl-a1b2c3d4e5f6", machineName(std(
		map[string]string{"PARTYLINE_MACHINE": " container:pl-a1b2c3d4e5f6 "},
		func() (string, error) { return "f38b3aaa1710", nil })), "the AI's machine wins over the container's hostname")
	require.Equal(t, "devbox", machineName(std(map[string]string{}, func() (string, error) { return " devbox\n", nil })))
	require.Equal(t, "devbox", machineName(std(map[string]string{"PARTYLINE_MACHINE": "  "}, func() (string, error) { return "devbox", nil })),
		"a blank PARTYLINE_MACHINE falls back to the hostname")
	require.Equal(t, "partyline", machineName(std(map[string]string{}, func() (string, error) { return "", errors.New("no hostname") })))
}

// CA-04: the session inside a container is the AI that was created for it. The agent image hands the
// CLI the AI's machine and folder (PARTYLINE_MACHINE, PARTYLINE_FOLDER), so the session joins that AI
// even though the container's own hostname and working directory are different, and no second AI
// appears (the prod bug: the session joined an AI named after the container's hostname).
func TestContainerSessionJoinsTheCreatedAI(t *testing.T) {
	e := newChannelEnv(t)
	const machine, folder = "container:pl-a1b2c3d4e5f6", agents.ContainerRepoFolder
	created, err := e.env.App.Agents.ResolveFolder(context.Background(), e.alice.User.ID,
		agents.FolderID{Machine: machine, Folder: folder, Harness: "claude"}, false)
	require.NoError(t, err)

	env := e.launched("claude", "PARTYLINE_MACHINE", machine, "PARTYLINE_FOLDER", folder)
	initialize(startChannel(t, nil, env, "/src/elsewhere"))
	prof := e.ai(t, folder)
	require.Equal(t, created.ID, prof.ID, "the session is the AI of the container, not another one")
	require.Equal(t, machine, e.machineOf(t, prof.ID), "the AI keeps its machine, not the container's hostname")
	require.Equal(t, []string{folder}, e.folders(t), "no other AI was created")
}

// CA-04: the pushed session (`partyline listen`, which the pi and OpenCode adapters run) joins the
// same container AI from the same two variables, and receives its messages.
func TestListenUsesTheContainerIdentity(t *testing.T) {
	e := newChannelEnv(t)
	const machine, folder = "container:pl-0f0e0d0c0b0a", agents.ContainerRootFolder
	created, err := e.env.App.Agents.ResolveFolder(context.Background(), e.alice.User.ID,
		agents.FolderID{Machine: machine, Folder: folder, Harness: "pi"}, false)
	require.NoError(t, err)

	p := startListen(t, e.vars("PARTYLINE_HARNESS", "pi", "PARTYLINE_MACHINE", machine, "PARTYLINE_FOLDER", folder), "/src/elsewhere", nil)
	online := e.ai(t, folder)
	require.Equal(t, created.ID, online.ID, "listen is the AI of the container")
	room := e.room(t, dto.CreateRoomRequest{Name: "Box"}, online.ID)
	msg := e.post(t, room.ID, "hello from the box")
	require.Equal(t, msg.ID, p.line()["message_id"], "the container AI receives its messages")
	require.Equal(t, []string{folder}, e.folders(t), "no other AI was created")
}

// MIG-02: --as names the unlinked identity of an earlier version that the folder adopts: it
// keeps its id and profile and is linked once; a stale name for another folder is ignored.
func TestChannelAsAdoptsTheUnlinkedIdentity(t *testing.T) {
	e := newChannelEnv(t)
	old := e.legacy(t, "old")
	initialize(startChannel(t, []string{"--as", "old"}, e.launched("claude"), "/work/new-home"))
	adopted := e.ai(t, "/work/new-home")
	require.Equal(t, old, adopted.ID)
	require.Equal(t, []any{"old", "old"}, []any{adopted.DisplayName, adopted.Handle}, "the profile is kept")
	require.Equal(t, []string{"/work/new-home"}, e.folders(t))

	initialize(startChannel(t, []string{"--as", "old"}, e.launched("claude"), "/work/another"))
	other := e.ai(t, "/work/another")
	require.NotEqual(t, old, other.ID, "an identity is linked once: the stale name creates a new AI")
	require.Equal(t, []string{"/work/another", "/work/new-home"}, e.folders(t))
}

// --project is accepted and ignored with one line on stderr: an AI is identified by its folder.
func TestChannelIgnoresProject(t *testing.T) {
	e := newChannelEnv(t)
	c := startChannel(t, []string{"--project", "Atlas Videos"}, e.launched("claude", "PARTYLINE_HARNESS_SESSION", "sess-project-01"), "/work/acme")
	initialize(c)
	e.ai(t, "/work/acme")
	require.Equal(t, "partyline channel: --project is ignored: an AI is identified by its folder\n", c.stderr.String(), "a session with its harness session id is current and says nothing else")
}

// AID-07: the identity a process gathers: the canonical folder (PARTYLINE_FOLDER, else the
// working directory), the canonical home folder, the hostname, PARTYLINE_HARNESS and the
// identity to adopt (the flag, else PARTYLINE_AGENT).
func TestSessionIdentity(t *testing.T) {
	target := canonicalDir(t, "work", "acme")
	home := canonicalDir(t, "home")
	host := func() (string, error) { return " devbox \n", nil }
	std := func(env map[string]string, wd string) stdio {
		return stdio{
			getenv: func(k string) string { return env[k] }, hostname: host,
			getwd: func() (string, error) { return wd, nil },
		}
	}

	id := sessionIdentity("", std(map[string]string{"HOME": home + "/", "PARTYLINE_HARNESS": " pi ", "PARTYLINE_AGENT": " old-bot "}, target+"/"))
	require.Equal(t, channel.Identity{Machine: "devbox", Folder: target, Home: home, Harness: "pi", Adopt: "old-bot"}, id)

	id = sessionIdentity(" flagged ", std(map[string]string{"PARTYLINE_AGENT": "old-bot", "PARTYLINE_FOLDER": target}, "/ignored"))
	require.Equal(t, channel.Identity{Machine: "devbox", Folder: target, Adopt: "flagged"}, id, "the flag beats PARTYLINE_AGENT and PARTYLINE_FOLDER beats the working directory")

	id = sessionIdentity("", std(map[string]string{"USERPROFILE": home}, target))
	require.Equal(t, home, id.Home, "USERPROFILE is the home folder where HOME is not set")

	// CA-04: inside a container the image names the machine of the AI it runs, because the
	// container's own hostname changes with every container; it wins over the hostname.
	id = sessionIdentity("", std(map[string]string{"PARTYLINE_MACHINE": " container:pl-a1b2c3d4e5f6 ", "PARTYLINE_FOLDER": target}, "/ignored"))
	require.Equal(t, channel.Identity{Machine: "container:pl-a1b2c3d4e5f6", Folder: target}, id, "PARTYLINE_MACHINE wins over the hostname")

	// AD-020: what the session asked for comes from the PARTYLINE_LAUNCH_* variables the launcher set, trimmed ...
	id = sessionIdentity("", std(map[string]string{
		"PARTYLINE_FOLDER": target, "PARTYLINE_LAUNCH_ORG": " acme ", "PARTYLINE_LAUNCH_POSITION": " Lead developer ", "PARTYLINE_LAUNCH_PARENT": " @lead ",
	}, "/ignored"))
	require.Equal(t, channel.Identity{Machine: "devbox", Folder: target, Org: "acme", Position: "Lead developer", Parent: "@lead"}, id)
	// ... and only from there: the user-facing variables are the launcher's input, not the session's, and an empty
	// or blank launch variable asks for nothing (L-035)
	id = sessionIdentity("", std(map[string]string{
		"PARTYLINE_FOLDER": target, "PARTYLINE_ORG": "acme", "PARTYLINE_POSITION": "Worktrees", "PARTYLINE_PARENT": "@lead",
		"PARTYLINE_LAUNCH_ORG": "  ", "PARTYLINE_LAUNCH_POSITION": "",
	}, "/ignored"))
	require.Equal(t, channel.Identity{Machine: "devbox", Folder: target}, id)

	unknown := stdio{
		getenv:   func(string) string { return "" },
		getwd:    func() (string, error) { return "", errors.New("no working directory") },
		hostname: func() (string, error) { return "", errors.New("no hostname") },
	}
	id = sessionIdentity("", unknown)
	require.Equal(t, channel.Identity{}, id, "what cannot be told is left empty: the headers say unknown and the server refuses the empty folder")
}
