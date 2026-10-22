package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// fakeProc writes a /proc-like tree: for each pid its stat line (parent ppid) and, when env is
// not nil, its environ block (NUL-separated). A nil env leaves environ out, like a process
// whose environment cannot be read.
type fakeProc struct {
	t    *testing.T
	root string
}

func newFakeProc(t *testing.T) fakeProc { return fakeProc{t: t, root: t.TempDir()} }

func (f fakeProc) add(pid, ppid int, comm string, env []string) {
	f.t.Helper()
	dir := filepath.Join(f.root, strconv.Itoa(pid))
	require.NoError(f.t, os.MkdirAll(dir, 0o755))
	stat := strconv.Itoa(pid) + " (" + comm + ") S " + strconv.Itoa(ppid) + " 1 1 0 -1 4194560"
	require.NoError(f.t, os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644))
	if env != nil {
		block := strings.Join(env, "\x00") + "\x00"
		require.NoError(f.t, os.WriteFile(filepath.Join(dir, "environ"), []byte(block), 0o600))
	}
}

// AID-07: only the four launcher variables are taken from an environment block; empty values
// and every other variable (URL, token, PATH, the v0.3 session and project ones …) are left out.
func TestLauncherEnvironKeepsOnlyLauncherVariables(t *testing.T) {
	block := []byte("PATH=/usr/bin\x00PARTYLINE_FOLDER=/work/my notes\x00PARTYLINE_TOKEN=pl_agent_secret\x00" +
		"PARTYLINE_URL=https://pl.example\x00PARTYLINE_HARNESS=codex\x00PARTYLINE_TAKEOVER=1\x00PARTYLINE_SESSION=pl-old\x00" +
		"PARTYLINE_PROJECT=Old\x00PARTYLINE_AGENT=\x00NOEQUALS\x00PARTYLINE_FOLDERX=no\x00")
	require.Equal(t, map[string]string{
		"PARTYLINE_FOLDER": "/work/my notes", "PARTYLINE_HARNESS": "codex", "PARTYLINE_TAKEOVER": "1",
	}, launcherEnviron(block))
	require.Empty(t, launcherEnviron(nil))
}

// AID-07: the variables come from the nearest ancestor whose environment has PARTYLINE_FOLDER:
// ancestors without it, or whose environment cannot be read, are passed over; the walk stops
// after maxAncestors processes and at pid 1.
func TestAncestorLauncherEnvNearestWins(t *testing.T) {
	proc := newFakeProc(t)
	launched := []string{"PARTYLINE_FOLDER=/work/near", "PARTYLINE_HARNESS=codex", "PARTYLINE_AGENT=old-bot", "PARTYLINE_TOKEN=x"}
	proc.add(40, 30, "codex", []string{"HOME=/home/a", "PATH=/usr/bin"}) // MCP server parent: stripped
	proc.add(30, 20, "node (wrapper)", nil)                              // unreadable environment
	proc.add(20, 10, "node", launched)
	proc.add(10, 1, "bash", []string{"PARTYLINE_FOLDER=/work/outer", "PARTYLINE_HARNESS=claude"})

	require.Equal(t, map[string]string{"PARTYLINE_FOLDER": "/work/near", "PARTYLINE_HARNESS": "codex", "PARTYLINE_AGENT": "old-bot"},
		ancestorLauncherEnv(proc.root, 40), "the nearest launched ancestor wins, past a stripped and an unreadable one")
	require.Equal(t, map[string]string{"PARTYLINE_FOLDER": "/work/outer", "PARTYLINE_HARNESS": "claude"},
		ancestorLauncherEnv(proc.root, 10))

	deep := newFakeProc(t)
	deep.add(50, 49, "a", []string{"PATH=/usr/bin"})
	deep.add(49, 48, "b", []string{"PATH=/usr/bin"})
	deep.add(48, 47, "c", []string{"PATH=/usr/bin"})
	deep.add(47, 46, "d", []string{"PATH=/usr/bin"})
	deep.add(46, 1, "e", launched)
	require.Nil(t, ancestorLauncherEnv(deep.root, 50), "at most maxAncestors processes are read")
	require.NotNil(t, ancestorLauncherEnv(deep.root, 49))

	none := newFakeProc(t)
	none.add(60, 1, "codex", []string{"PATH=/usr/bin", "PARTYLINE_FOLDER="})
	require.Nil(t, ancestorLauncherEnv(none.root, 60), "no ancestor with a folder")
	require.Nil(t, ancestorLauncherEnv(filepath.Join(none.root, "missing"), 60), "no /proc (macOS, Windows)")
}

// AID-07: on Linux the real /proc is read: a child started with the launcher variables gives
// them back, and the parent pid comes from /proc/<pid>/stat.
func TestAncestorLauncherEnvReadsRealProc(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc is Linux only")
	}
	sleep, err := exec.LookPath("sleep")
	require.NoError(t, err)
	cmd := exec.Command(sleep, "30")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "PARTYLINE_FOLDER=/work/child", "PARTYLINE_HARNESS=gemini", "PARTYLINE_TOKEN=pl_agent_secret"}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	want := map[string]string{"PARTYLINE_FOLDER": "/work/child", "PARTYLINE_HARNESS": "gemini"}
	// Until the child has exec'd, /proc shows the environment it inherited.
	require.Eventually(t, func() bool {
		return reflect.DeepEqual(want, ancestorLauncherEnv("/proc", cmd.Process.Pid))
	}, chanWait, 10*time.Millisecond)
	require.Equal(t, os.Getppid(), parentPID("/proc", os.Getpid()))
}

// AID-07: the process's own variables win; the ancestor's fill only the launcher variables that
// are missing, and only when PARTYLINE_FOLDER itself is missing.
func TestWithLauncherEnv(t *testing.T) {
	ancestor := map[string]string{"PARTYLINE_FOLDER": "/work/anc", "PARTYLINE_HARNESS": "codex", "PARTYLINE_TAKEOVER": "1", "PARTYLINE_AGENT": "anc-bot"}
	std := func(own map[string]string) stdio {
		return stdio{getenv: func(k string) string { return own[k] }, launcherEnv: func() map[string]string { return ancestor }}
	}

	got := withLauncherEnv(std(map[string]string{"PARTYLINE_AGENT": "own-bot", "PARTYLINE_URL": "http://x"}))
	require.Equal(t, []string{"/work/anc", "codex", "1", "own-bot", "http://x", ""},
		[]string{got.getenv("PARTYLINE_FOLDER"), got.getenv("PARTYLINE_HARNESS"), got.getenv("PARTYLINE_TAKEOVER"),
			got.getenv("PARTYLINE_AGENT"), got.getenv("PARTYLINE_URL"), got.getenv("PARTYLINE_TOKEN")})

	got = withLauncherEnv(std(map[string]string{"PARTYLINE_FOLDER": "/work/own"}))
	require.Equal(t, []string{"/work/own", "", ""},
		[]string{got.getenv("PARTYLINE_FOLDER"), got.getenv("PARTYLINE_HARNESS"), got.getenv("PARTYLINE_TAKEOVER")},
		"a process that has its folder takes nothing from an ancestor")

	got = withLauncherEnv(stdio{getenv: func(string) string { return "" }})
	require.Empty(t, got.getenv("PARTYLINE_FOLDER"), "no ancestor lookup")
}

// AID-01, AID-07: Codex starts its MCP servers with a filtered environment, so the tools-only
// channel of a `partyline codex` session lacks PARTYLINE_FOLDER and PARTYLINE_HARNESS, and its
// working directory is not the session's folder. It takes them from the launched ancestor:
// its AI is the one of the session's folder with the session's harness, and the session's Stop
// hook (environment intact, or stripped too and recovered the same way) acts as that same AI.
// Two such sessions keep two AIs and nothing else is created.
func TestStrippedToolsOnlyChannelRecoversLauncherVariables(t *testing.T) {
	e := newChannelEnv(t)
	launched := func(folder string) map[string]string {
		return map[string]string{"PARTYLINE_FOLDER": folder, "PARTYLINE_HARNESS": "codex"}
	}
	full := func(folder string) map[string]string {
		env := e.vars()
		for k, v := range launched(folder) {
			env[k] = v
		}
		return env
	}
	call := func(c *mcpClient) {
		t.Helper()
		c.send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "list_rooms", "arguments": map[string]any{}}})
		require.Equal(t, false, c.next()["result"].(map[string]any)["isError"])
	}

	one := startChannelWith(t, []string{"--tools-only"}, e.vars(), "/tmp/src/other", launched("/work/one"))
	initialize(one)
	call(one)
	two := startChannelWith(t, []string{"--tools-only"}, e.vars(), "/tmp/src/other", launched("/work/two"))
	initialize(two)
	call(two)

	first := e.registered(t, "/work/one")
	require.Equal(t, "codex", first.AI.App, "the harness comes from the launched ancestor")
	room1 := e.room(t, dto.CreateRoomRequest{Name: "One"}, first.ID)
	msg1 := e.post(t, room1.ID, "for the first session")
	room2 := e.room(t, dto.CreateRoomRequest{Name: "Two"}, e.registered(t, "/work/two").ID)
	msg2 := e.post(t, room2.ID, "for the second session")

	_, out, errOut := runHook("codex-stop", `{"cwd":"/work/elsewhere"}`, full("/work/one"))
	require.Contains(t, out, `message_id=\"`+msg1.ID+`\"`, "the first session's hook acts as its tools' AI: %s", errOut)
	require.NotContains(t, out, msg2.ID)
	_, out, errOut = runHookWith("codex-stop", `{"cwd":"/work/elsewhere"}`, e.vars(), launched("/work/two"))
	require.Contains(t, out, `message_id=\"`+msg2.ID+`\"`, "a stripped hook recovers its session too: %s", errOut)
	require.NotContains(t, out, msg1.ID)

	require.Equal(t, []string{"/work/one", "/work/two"}, e.folders(t), "one AI per session folder")
}

// Without a launched ancestor a stripped tools-only channel falls back on its own working
// directory.
func TestStrippedToolsOnlyChannelWithoutAncestorUsesItsWorkingDirectory(t *testing.T) {
	e := newChannelEnv(t)
	c := startChannelWith(t, []string{"--tools-only"}, e.vars(), "/work/plain", map[string]string{})
	initialize(c)
	c.send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "list_rooms", "arguments": map[string]any{}}})
	require.Equal(t, false, c.next()["result"].(map[string]any)["isError"])
	require.Equal(t, []string{"/work/plain"}, e.folders(t))
	require.Equal(t, "agent", e.registered(t, "/work/plain").AI.App, "and its harness is unknown")
}
