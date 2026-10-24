package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

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

	fromLink := startChannel(t, nil, e.vars(), link)
	initialize(fromLink)
	prof := e.ai(t, target)
	require.Equal(t, []any{"Café Órdenes", "cafe-ordenes"}, []any{prof.DisplayName, prof.Handle})
	require.Equal(t, testMachine, e.machineOf(t, prof.ID), "the channel reports its machine")
	require.Equal(t, []string{target}, e.folders(t), "the symlink is not another folder")

	// The launcher's PARTYLINE_FOLDER wins over the working directory, whatever its spelling.
	fromEnv := startChannel(t, []string{"--tools-only"}, e.vars("PARTYLINE_FOLDER", link+"/"), "/tmp/src/elsewhere")
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
	initialize(startChannel(t, nil, e.vars("HOME", home), work))
	require.Equal(t, "~/src/acme", e.ai(t, work).AI.FolderLabel)
}

// MIG-02: --as names the unlinked identity of an earlier version that the folder adopts: it
// keeps its id and profile and is linked once; a stale name for another folder is ignored.
func TestChannelAsAdoptsTheUnlinkedIdentity(t *testing.T) {
	e := newChannelEnv(t)
	old := e.legacy(t, "old")
	initialize(startChannel(t, []string{"--as", "old"}, e.vars(), "/work/new-home"))
	adopted := e.ai(t, "/work/new-home")
	require.Equal(t, old, adopted.ID)
	require.Equal(t, []any{"old", "old"}, []any{adopted.DisplayName, adopted.Handle}, "the profile is kept")
	require.Equal(t, []string{"/work/new-home"}, e.folders(t))

	initialize(startChannel(t, []string{"--as", "old"}, e.vars(), "/work/another"))
	other := e.ai(t, "/work/another")
	require.NotEqual(t, old, other.ID, "an identity is linked once: the stale name creates a new AI")
	require.Equal(t, []string{"/work/another", "/work/new-home"}, e.folders(t))
}

// --project is accepted and ignored with one line on stderr: an AI is identified by its folder.
func TestChannelIgnoresProject(t *testing.T) {
	e := newChannelEnv(t)
	c := startChannel(t, []string{"--project", "Atlas Videos"}, e.vars(), "/work/acme")
	initialize(c)
	e.ai(t, "/work/acme")
	require.Equal(t, "partyline channel: --project is ignored: an AI is identified by its folder\n", c.stderr.String())
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

	id := sessionIdentity("", std(map[string]string{"HOME": home + "/", "PARTYLINE_HARNESS": " codex ", "PARTYLINE_AGENT": " old-bot "}, target+"/"))
	require.Equal(t, channel.Identity{Machine: "devbox", Folder: target, Home: home, Harness: "codex", Adopt: "old-bot"}, id)

	id = sessionIdentity(" flagged ", std(map[string]string{"PARTYLINE_AGENT": "old-bot", "PARTYLINE_FOLDER": target}, "/ignored"))
	require.Equal(t, channel.Identity{Machine: "devbox", Folder: target, Adopt: "flagged"}, id, "the flag beats PARTYLINE_AGENT and PARTYLINE_FOLDER beats the working directory")

	id = sessionIdentity("", std(map[string]string{"USERPROFILE": home}, target))
	require.Equal(t, home, id.Home, "USERPROFILE is the home folder where HOME is not set")

	unknown := stdio{
		getenv:   func(string) string { return "" },
		getwd:    func() (string, error) { return "", errors.New("no working directory") },
		hostname: func() (string, error) { return "", errors.New("no hostname") },
	}
	id = sessionIdentity("", unknown)
	require.Equal(t, channel.Identity{}, id, "what cannot be told is left empty: the headers say unknown and the server refuses the empty folder")
}
