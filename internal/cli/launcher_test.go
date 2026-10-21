package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/harness"
	"github.com/wnexous/partyline/internal/testkit"
)

// launchMachine is a machine for `partyline <harness>`: a fake HOME, the programs on PATH, the
// harnesses Partyline is registered in and the environment the launcher passes on.
type launchMachine struct {
	home    string
	onPath  map[string]bool
	codex   bool // `codex mcp get partyline` finds the server
	environ []string
}

func newLaunchMachine(t *testing.T) *launchMachine {
	t.Helper()
	home := t.TempDir()
	return &launchMachine{home: home, onPath: map[string]bool{}, environ: []string{
		"HOME=" + home, "PATH=/usr/local/bin:/usr/bin", "PARTYLINE_SESSION=inherited", "LANG=C.UTF-8",
	}}
}

const launchBinary = "/opt/bin/partyline"

// register records Partyline in harness id the way setup leaves it.
func (m *launchMachine) register(t *testing.T, id string) {
	t.Helper()
	write := func(rel, data string) {
		path := filepath.Join(m.home, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	}
	switch id {
	case "claude":
		write(".claude.json", `{"mcpServers":{"partyline":{"command":"`+launchBinary+`","args":["channel"]}}}`)
	case "gemini":
		write(".gemini/settings.json", `{"mcpServers":{"partyline":{"command":"`+launchBinary+`","args":["channel","--tools-only"]}}}`)
	case "codex":
		m.codex = true
	default: // the file harnesses write their own entry
		env := harness.Env{Home: m.home, Binary: launchBinary, Version: "dev", Getenv: func(string) string { return "" }}
		for _, r := range harness.All(env) {
			if r.ID() == id {
				require.NoError(t, r.Apply(context.Background()))
			}
		}
	}
}

type launchRun struct {
	code   int
	stderr string
	execed bool
	path   string
	argv   []string
	env    []string
}

func (m *launchMachine) run(t *testing.T, args ...string) launchRun {
	t.Helper()
	var r launchRun
	var errOut bytes.Buffer
	vars := map[string]string{"HOME": m.home}
	r.code = execute(context.Background(), append([]string{"partyline"}, args...), stdio{
		stdin: bytes.NewReader(nil), stdout: &bytes.Buffer{}, stderr: &errOut,
		getenv:     func(k string) string { return vars[k] },
		executable: func() (string, error) { return launchBinary, nil },
		lookPath: func(name string) (string, error) {
			if m.onPath[name] {
				return "/usr/local/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		runHarness: func(_ context.Context, name string, a ...string) ([]byte, error) {
			line := name + " " + strings.Join(a, " ")
			if line != "codex mcp get partyline --json" {
				t.Errorf("unexpected harness CLI call: %s", line)
				return nil, errors.New("unexpected")
			}
			if m.codex {
				return []byte(`{"name":"partyline","transport":{"type":"stdio","command":"` + launchBinary + `"}}`), nil
			}
			return []byte("Error: No MCP server named 'partyline' found."), errors.New("exit status 1")
		},
		environ: func() []string { return append([]string(nil), m.environ...) },
		exec: func(path string, argv, env []string) (int, error) {
			r.execed, r.path, r.argv, r.env = true, path, argv, env
			return 7, nil
		},
	})
	r.stderr = errOut.String()
	return r
}

// sessionID is the PARTYLINE_SESSION a launcher passed on; it fails unless there is exactly one.
func (r launchRun) sessionID(t *testing.T) string {
	t.Helper()
	var ids []string
	for _, kv := range r.env {
		if v, ok := strings.CutPrefix(kv, "PARTYLINE_SESSION="); ok {
			ids = append(ids, v)
		}
	}
	require.Len(t, ids, 1, "env: %v", r.env)
	return ids[0]
}

// A session key that listen and the tools-only channel accept as a file name.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// HAR-03: every launcher (and Claude's aliases) execs the harness binary found on PATH with
// the user's arguments unchanged (Claude first gets the development-channels flag), returns
// its exit code, and passes this environment on with a fresh PARTYLINE_SESSION in place of
// the inherited one and PARTYLINE_HARNESS set to the harness id (CLI-01). A registered harness starts without a warning.
func TestLaunchersExecTheHarness(t *testing.T) {
	user := []string{"--resume", "abc", "-p", "hi there", "--", "--help"}
	cases := []struct {
		command, binary string
		argv            []string
	}{
		{"claude", "claude", []string{"claude", "--dangerously-load-development-channels", "server:partyline", "--resume", "abc", "-p", "hi there", "--", "--help"}},
		{"claudecode", "claude", []string{"claude", "--dangerously-load-development-channels", "server:partyline", "--resume", "abc", "-p", "hi there", "--", "--help"}},
		{"claude-code", "claude", []string{"claude", "--dangerously-load-development-channels", "server:partyline", "--resume", "abc", "-p", "hi there", "--", "--help"}},
		{"opencode", "opencode", []string{"opencode", "--resume", "abc", "-p", "hi there", "--", "--help"}},
		{"pi", "pi", []string{"pi", "--resume", "abc", "-p", "hi there", "--", "--help"}},
		{"omp", "omp", []string{"omp", "--resume", "abc", "-p", "hi there", "--", "--help"}},
		{"codex", "codex", []string{"codex", "--resume", "abc", "-p", "hi there", "--", "--help"}},
		{"gemini", "gemini", []string{"gemini", "--resume", "abc", "-p", "hi there", "--", "--help"}},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			m := newLaunchMachine(t)
			m.onPath[c.binary] = true
			m.register(t, c.binary)

			r := m.run(t, append([]string{c.command}, user...)...)
			require.Equal(t, 7, r.code, r.stderr)
			require.Empty(t, r.stderr)
			require.True(t, r.execed)
			require.Equal(t, "/usr/local/bin/"+c.binary, r.path)
			require.Equal(t, c.argv, r.argv)

			id := r.sessionID(t)
			require.Regexp(t, sessionIDPattern, id)
			require.NotEqual(t, "inherited", id)
			require.False(t, seen[id], "every launch gets a fresh session id")
			seen[id] = true
			harnessID := c.binary
			require.Equal(t, []string{"HOME=" + m.home, "PATH=/usr/local/bin:/usr/bin", "LANG=C.UTF-8", "PARTYLINE_SESSION=" + id, "PARTYLINE_HARNESS=" + harnessID}, r.env)
		})
	}
}

// HAR-03: without the harness binary on PATH the launcher exits 127 with the install hint
// and runs nothing.
func TestLauncherWithoutTheHarness(t *testing.T) {
	cases := map[string]string{
		"claude":      "partyline claude: Claude Code is not installed (install it, then run partyline setup)\n",
		"claude-code": "partyline claude: Claude Code is not installed (install it, then run partyline setup)\n",
		"opencode":    "partyline opencode: OpenCode is not installed (install it, then run partyline setup)\n",
		"pi":          "partyline pi: pi is not installed (install it, then run partyline setup)\n",
		"omp":         "partyline omp: oh-my-pi (omp) is not installed (install it, then run partyline setup)\n",
		"codex":       "partyline codex: Codex CLI is not installed (install it, then run partyline setup)\n",
		"gemini":      "partyline gemini: Gemini CLI is not installed (install it, then run partyline setup)\n",
	}
	for command, want := range cases {
		t.Run(command, func(t *testing.T) {
			m := newLaunchMachine(t)
			m.onPath["partyline"] = true
			r := m.run(t, command, "--version")
			require.Equal(t, 127, r.code)
			require.Equal(t, want, r.stderr)
			require.False(t, r.execed, "nothing runs without the harness")
		})
	}
}

// HAR-03: when Partyline is not registered in the harness the launcher prints one warning
// naming `partyline setup --harness <id>` and starts the harness anyway.
func TestLauncherWarnsWhenNotRegistered(t *testing.T) {
	cases := map[string]string{
		"claude":   "partyline claude: Partyline is not registered in Claude Code; run partyline setup --harness claude. Starting anyway.\n",
		"opencode": "partyline opencode: Partyline is not registered in OpenCode; run partyline setup --harness opencode. Starting anyway.\n",
		"pi":       "partyline pi: Partyline is not registered in pi; run partyline setup --harness pi. Starting anyway.\n",
		"omp":      "partyline omp: Partyline is not registered in oh-my-pi (omp); run partyline setup --harness omp. Starting anyway.\n",
		"codex":    "partyline codex: Partyline is not registered in Codex CLI; run partyline setup --harness codex. Starting anyway.\n",
		"gemini":   "partyline gemini: Partyline is not registered in Gemini CLI; run partyline setup --harness gemini. Starting anyway.\n",
	}
	for id, want := range cases {
		t.Run(id, func(t *testing.T) {
			m := newLaunchMachine(t)
			m.onPath[id] = true
			r := m.run(t, id)
			require.Equal(t, want, r.stderr)
			require.True(t, r.execed, "the harness starts anyway")
			require.Equal(t, 7, r.code)
		})
	}
}

// HAR-03: the session id of one launch lets that session's listen and tools-only channel
// share the derived name, and two launches in the same folder never share it: the second
// session is proj-2 and its tools act as proj-2, the first stays proj.
func TestLaunchedSessionsInOneFolderKeepTheirNames(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	m := newLaunchMachine(t)
	m.onPath["pi"] = true
	m.register(t, "pi")
	first, second := m.run(t, "pi").sessionID(t), m.run(t, "pi").sessionID(t)
	require.NotEqual(t, first, second)

	e := newChannelEnv(t)
	wd := "/tmp/src/proj"
	listen1 := startListen(t, e.vars("PARTYLINE_SESSION", first, "PARTYLINE_HARNESS", "pi"), wd, nil)
	proj := e.agent(t, "proj-pi")
	listen2 := startListen(t, e.vars("PARTYLINE_SESSION", second, "PARTYLINE_HARNESS", "pi"), wd, nil)
	proj2 := e.agent(t, "proj-pi-2")
	for _, id := range []string{first, second} {
		path := filepath.Join(os.TempDir(), "partyline", ".sessions", id)
		require.Eventually(t, func() bool { _, err := os.Stat(path); return err == nil }, chanWait, 20*time.Millisecond, "session file %s", path)
	}
	room := e.room(t, dto.CreateRoomRequest{Name: "Shared"}, proj.ID, proj2.ID)

	post := func(session string, id int, text string) {
		t.Helper()
		c := startChannel(t, []string{"--tools-only"}, e.vars("PARTYLINE_SESSION", session, "PARTYLINE_HARNESS", "pi"), wd)
		initialize(c)
		c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{
			"name": "send_message", "arguments": map[string]any{"room": "Shared", "text": text},
		}})
		r := c.next()
		require.Equal(t, false, r["result"].(map[string]any)["isError"], "result: %v", r)
	}
	post(first, 7, "from the first session")
	post(second, 8, "from the second session")

	page := testkit.Decode[dto.Page[dto.Message]](e.alice.Get("/api/v1/rooms/" + room.ID + "/messages").RequireStatus(http.StatusOK))
	authors := map[string]string{}
	for _, msg := range page.Items {
		authors[msg.Text] = msg.AuthorID
	}
	require.Equal(t, proj.ID, authors["from the first session"])
	require.Equal(t, proj2.ID, authors["from the second session"])

	require.NoError(t, listen1.in.Close())
	require.NoError(t, listen2.in.Close())
	require.Equal(t, 0, listen1.exit())
	require.Equal(t, 0, listen2.exit())
}

// CLI-01: leading --name and --project (also --name=<v>) are taken off the harness's
// arguments and passed on as PARTYLINE_AGENT and PARTYLINE_PROJECT, replacing inherited
// values; later arguments that look the same go to the harness unchanged. A flag without a
// value exits 2 and runs nothing.
func TestLauncherPassesNameAndProject(t *testing.T) {
	m := newLaunchMachine(t)
	m.environ = append(m.environ, "PARTYLINE_AGENT=inherited", "PARTYLINE_HARNESS=pi")
	m.onPath["claude"] = true
	m.register(t, "claude")

	r := m.run(t, "claude", "--name", "ruthe-bot", "--project=Ruthe", "--resume", "--name", "x")
	require.Equal(t, 7, r.code, r.stderr)
	require.Equal(t, []string{"claude", "--dangerously-load-development-channels", "server:partyline", "--resume", "--name", "x"}, r.argv)
	id := r.sessionID(t)
	require.Equal(t, []string{"HOME=" + m.home, "PATH=/usr/local/bin:/usr/bin", "LANG=C.UTF-8",
		"PARTYLINE_SESSION=" + id, "PARTYLINE_HARNESS=claude", "PARTYLINE_AGENT=ruthe-bot", "PARTYLINE_PROJECT=Ruthe"}, r.env)

	r = m.run(t, "claude", "--project")
	require.Equal(t, 2, r.code)
	require.False(t, r.execed)
	require.Equal(t, "partyline claude: --project needs a value\n", r.stderr)
}

// ID-03: a launch from the home folder without --project or --name warns once on stderr and
// starts; with either flag it does not warn.
func TestLauncherWarnsInTheHomeFolder(t *testing.T) {
	m := newLaunchMachine(t)
	m.onPath["claude"] = true
	m.register(t, "claude")
	t.Chdir(m.home)

	r := m.run(t, "claude")
	require.Equal(t, 7, r.code)
	require.True(t, r.execed)
	require.Equal(t, "partyline claude: started in the home folder; using project \"home\" (pass --project <name> or --name <agent> to choose)\n", r.stderr)
	for _, args := range [][]string{{"claude", "--project", "notes"}, {"claude", "--name", "helper"}} {
		r = m.run(t, args...)
		require.Equal(t, 7, r.code)
		require.Empty(t, r.stderr, "%v", args)
	}
}

// HAR-03: `partyline --help` (and -h, help) prints the usage with every launcher on stdout.
func TestHelpListsTheLaunchers(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "help"} {
		t.Run(flag, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := execute(context.Background(), []string{"partyline", flag}, stdio{stdout: &out, stderr: &errOut, getenv: func(string) string { return "" }})
			require.Equal(t, 0, code)
			require.Empty(t, errOut.String())
			require.Contains(t, out.String(), "Start a session with Partyline (the arguments go to the harness unchanged):\n"+
				"  claude [args...]       Claude Code with the Partyline channel loaded (also: claudecode, claude-code)\n"+
				"  opencode [args...]     OpenCode\n"+
				"  pi [args...]           pi\n"+
				"  omp [args...]          oh-my-pi\n"+
				"  codex [args...]        Codex CLI\n"+
				"  gemini [args...]       Gemini CLI\n")
		})
	}
}
