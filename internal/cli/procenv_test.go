package cli

import (
	"context"
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

	"github.com/wnexous/partyline/internal/agents"
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

// ID-01 (T17): only the four launcher variables are taken from an environment block; empty
// values and every other variable (URL, token, PATH …) are left out.
func TestLauncherEnvironKeepsOnlyLauncherVariables(t *testing.T) {
	block := []byte("PATH=/usr/bin\x00PARTYLINE_SESSION=pl-0123456789abcdef\x00PARTYLINE_TOKEN=pl_agent_secret\x00" +
		"PARTYLINE_URL=https://pl.example\x00PARTYLINE_HARNESS=codex\x00PARTYLINE_PROJECT=My Notes\x00" +
		"PARTYLINE_AGENT=\x00NOEQUALS\x00PARTYLINE_SESSIONX=no\x00")
	require.Equal(t, map[string]string{
		"PARTYLINE_SESSION": "pl-0123456789abcdef", "PARTYLINE_HARNESS": "codex", "PARTYLINE_PROJECT": "My Notes",
	}, launcherEnviron(block))
	require.Empty(t, launcherEnviron(nil))
}

// ID-01 (T17): the variables come from the nearest ancestor whose environment has
// PARTYLINE_SESSION: ancestors without it, or whose environment cannot be read, are passed
// over; the walk stops after maxAncestors processes and at pid 1.
func TestAncestorLauncherEnvNearestWins(t *testing.T) {
	proc := newFakeProc(t)
	launched := []string{"PARTYLINE_SESSION=pl-near", "PARTYLINE_HARNESS=codex", "PARTYLINE_PROJECT=Notes", "PARTYLINE_TOKEN=x"}
	proc.add(40, 30, "codex", []string{"HOME=/home/a", "PATH=/usr/bin"}) // MCP server parent: stripped
	proc.add(30, 20, "node (wrapper)", nil)                              // unreadable environment
	proc.add(20, 10, "node", launched)
	proc.add(10, 1, "bash", []string{"PARTYLINE_SESSION=pl-outer", "PARTYLINE_HARNESS=claude"})

	require.Equal(t, map[string]string{"PARTYLINE_SESSION": "pl-near", "PARTYLINE_HARNESS": "codex", "PARTYLINE_PROJECT": "Notes"},
		ancestorLauncherEnv(proc.root, 40), "the nearest launched ancestor wins, past a stripped and an unreadable one")
	require.Equal(t, map[string]string{"PARTYLINE_SESSION": "pl-outer", "PARTYLINE_HARNESS": "claude"},
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
	none.add(60, 1, "codex", []string{"PATH=/usr/bin", "PARTYLINE_SESSION="})
	require.Nil(t, ancestorLauncherEnv(none.root, 60), "no ancestor with a session")
	require.Nil(t, ancestorLauncherEnv(filepath.Join(none.root, "missing"), 60), "no /proc (macOS, Windows)")
}

// ID-01 (T17): on Linux the real /proc is read: a child started with the launcher variables
// gives them back, and the parent pid comes from /proc/<pid>/stat.
func TestAncestorLauncherEnvReadsRealProc(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc is Linux only")
	}
	sleep, err := exec.LookPath("sleep")
	require.NoError(t, err)
	cmd := exec.Command(sleep, "30")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "PARTYLINE_SESSION=pl-child", "PARTYLINE_HARNESS=gemini", "PARTYLINE_TOKEN=pl_agent_secret"}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	want := map[string]string{"PARTYLINE_SESSION": "pl-child", "PARTYLINE_HARNESS": "gemini"}
	// Until the child has exec'd, /proc shows the environment it inherited.
	require.Eventually(t, func() bool {
		return reflect.DeepEqual(want, ancestorLauncherEnv("/proc", cmd.Process.Pid))
	}, chanWait, 10*time.Millisecond)
	require.Equal(t, os.Getppid(), parentPID("/proc", os.Getpid()))
}

// ID-01 (T17): the process's own variables win; the ancestor's fill only the launcher
// variables that are missing, and only when PARTYLINE_SESSION itself is missing.
func TestWithLauncherEnv(t *testing.T) {
	ancestor := map[string]string{"PARTYLINE_SESSION": "pl-anc", "PARTYLINE_HARNESS": "codex", "PARTYLINE_PROJECT": "Notes", "PARTYLINE_AGENT": "anc-bot"}
	std := func(own map[string]string) stdio {
		return stdio{getenv: func(k string) string { return own[k] }, launcherEnv: func() map[string]string { return ancestor }}
	}

	got := withLauncherEnv(std(map[string]string{"PARTYLINE_PROJECT": "Own", "PARTYLINE_URL": "http://x"}))
	require.Equal(t, []string{"pl-anc", "codex", "Own", "anc-bot", "http://x", ""},
		[]string{got.getenv("PARTYLINE_SESSION"), got.getenv("PARTYLINE_HARNESS"), got.getenv("PARTYLINE_PROJECT"),
			got.getenv("PARTYLINE_AGENT"), got.getenv("PARTYLINE_URL"), got.getenv("PARTYLINE_TOKEN")})

	got = withLauncherEnv(std(map[string]string{"PARTYLINE_SESSION": "pl-own"}))
	require.Equal(t, []string{"pl-own", "", ""},
		[]string{got.getenv("PARTYLINE_SESSION"), got.getenv("PARTYLINE_HARNESS"), got.getenv("PARTYLINE_PROJECT")},
		"a process that has its session takes nothing from an ancestor")

	got = withLauncherEnv(stdio{getenv: func(string) string { return "" }})
	require.Empty(t, got.getenv("PARTYLINE_SESSION"), "no ancestor lookup")
}

// ID-01 (T17): Codex starts its MCP servers with a filtered environment, so the tools-only
// channel of a `partyline codex` session lacks PARTYLINE_SESSION, PARTYLINE_HARNESS and
// PARTYLINE_PROJECT. It takes them from the launched ancestor: its agent carries the
// session's project and harness, and the session's Stop hook (environment intact, or
// stripped too and recovered the same way) acts as that same agent. Two such sessions keep
// two agents and nothing else is registered.
func TestStrippedToolsOnlyChannelRecoversLauncherVariables(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()) // the tools-only channel looks for listen's session file
	e := newChannelEnv(t)
	launched := func(key string) map[string]string {
		return map[string]string{"PARTYLINE_SESSION": key, "PARTYLINE_HARNESS": "codex", "PARTYLINE_PROJECT": "Notes"}
	}
	full := func(key string) map[string]string {
		env := e.vars()
		for k, v := range launched(key) {
			env[k] = v
		}
		return env
	}
	registered := func(name string) {
		t.Helper()
		require.Eventually(t, func() bool { _, ok := e.onlineNames(t)[name]; return ok }, chanWait, 20*time.Millisecond, name)
	}

	one := startChannelWith(t, []string{"--tools-only"}, e.vars(), "/tmp/src/other", launched("pl-one"))
	initialize(one)
	registered("notes-codex")
	two := startChannelWith(t, []string{"--tools-only"}, e.vars(), "/tmp/src/other", launched("pl-two"))
	initialize(two)
	registered("notes-codex-2")

	first := e.agentNamed(t, "notes-codex")
	require.Equal(t, []string{"Notes · Codex", "codex", "Notes"}, []string{first.DisplayName, first.Harness, first.Project.Name},
		"harness and project come from the launched ancestor")
	room1 := e.room(t, dto.CreateRoomRequest{Name: "One"}, first.ID)
	msg1 := e.post(t, room1.ID, "for the first session")
	room2 := e.room(t, dto.CreateRoomRequest{Name: "Two"}, e.agentNamed(t, "notes-codex-2").ID)
	msg2 := e.post(t, room2.ID, "for the second session")

	_, out, errOut := runHook("codex-stop", `{"cwd":"/work/elsewhere"}`, full("pl-one"))
	require.Contains(t, out, `message_id=\"`+msg1.ID+`\"`, "the first session's hook acts as its tools' agent: %s", errOut)
	require.NotContains(t, out, msg2.ID)
	_, out, errOut = runHookWith("codex-stop", `{"cwd":"/work/elsewhere"}`, e.vars(), launched("pl-two"))
	require.Contains(t, out, `message_id=\"`+msg2.ID+`\"`, "a stripped hook recovers its session too: %s", errOut)
	require.NotContains(t, out, msg1.ID)

	require.Equal(t, map[string]bool{"notes-codex": false, "notes-codex-2": false}, e.onlineNames(t), "one agent per session")
}

// ID-01 (T17): without a launched ancestor the stripped tools-only channel keeps the old rule:
// its parent process id is the session key and the harness is unknown.
func TestStrippedToolsOnlyChannelWithoutAncestorUsesParentPID(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	e := newChannelEnv(t)
	c := startChannelWith(t, []string{"--tools-only"}, e.vars(), "/work/plain", map[string]string{})
	initialize(c)
	require.Eventually(t, func() bool { _, ok := e.onlineNames(t)["plain-agent"]; return ok }, chanWait, 20*time.Millisecond)

	ctx := context.Background()
	same, err := e.env.App.Agents.Register(ctx, e.alice.User.ID, agents.Identity{Project: "plain", Session: strconv.Itoa(os.Getppid())})
	require.NoError(t, err)
	require.Equal(t, "plain-agent", same.Agent.Name, "the parent pid key holds the channel's agent")
	other, err := e.env.App.Agents.Register(ctx, e.alice.User.ID, agents.Identity{Project: "plain", Session: "pl-other"})
	require.NoError(t, err)
	require.Equal(t, "plain-agent-2", other.Agent.Name, "another key does not get it")
}
