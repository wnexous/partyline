package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel"
	"github.com/wnexous/partyline/internal/cliconfig"
	"github.com/wnexous/partyline/internal/harness"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/users"
)

// launchMachine is a machine for `partyline <harness>`: a fake HOME, the programs on PATH, the
// harnesses Partyline is registered in, the folder the launcher runs in and the environment it
// passes on. getenv holds the variables the launcher process reads (HOME always, otherwise
// m.environ is only what the harness inherits).
type launchMachine struct {
	home     string
	hostname string
	cwd      string
	onPath   map[string]bool
	codex    bool // `codex mcp get partyline` finds the server
	environ  []string
	getenv   map[string]string
	// input is an answer a launch must never read any more (SESS-16); terminals lists the file
	// descriptors that are terminals (stdin is 0, stderr is 2).
	input     string
	terminals map[int]bool
	// harnessExit is the exit code the fake harness returns (nil: 7).
	harnessExit *int
	// now is the launcher's clock (nil: time.Now); its location formats the session's time.
	now func() time.Time
	// sessionHTTP reaches GET /agent/session (nil: the launcher's own client).
	sessionHTTP *http.Client
	// pid is the launcher's process id (the pid a pi or OpenCode session's live id is tied to).
	pid int
	// afterExec runs inside the exec hook, before it returns (the takeover test dials as the
	// harness's channel from there).
	afterExec func(env []string)
}

func newLaunchMachine(t *testing.T) *launchMachine {
	t.Helper()
	home := t.TempDir()
	return &launchMachine{home: home, hostname: "testbox", cwd: "/work/acme", pid: 4242, onPath: map[string]bool{}, environ: []string{
		"HOME=" + home, "PATH=/usr/local/bin:/usr/bin", "PARTYLINE_FOLDER=/inherited/folder", "LANG=C.UTF-8",
	}}
}

// saveConfig writes the credentials `partyline <harness>` resolves when no variable names an
// instance.
func (m *launchMachine) saveConfig(t *testing.T, url, token string) {
	t.Helper()
	path, err := cliconfig.Path(func(k string) string {
		if k == "HOME" {
			return m.home
		}
		return ""
	})
	require.NoError(t, err)
	require.NoError(t, cliconfig.Save(path, cliconfig.Config{URL: url, Token: token}))
}

const launchToken = "pl_agent_launcher_test"

// answerSession asks GET /agent/session from the launcher with the given state and returns the
// client to reach it; ask asserts the request, when it is not nil.
func (m *launchMachine) answerSession(t *testing.T, state dto.SessionState, ask func(r *http.Request)) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/agent/session", r.URL.Path)
		require.Equal(t, "Bearer "+launchToken, r.Header.Get("Authorization"))
		if ask != nil {
			ask(r)
		}
		require.NoError(t, json.NewEncoder(w).Encode(state))
	}))
	t.Cleanup(srv.Close)
	m.saveConfig(t, srv.URL, launchToken)
	m.sessionHTTP = srv.Client()
}

// sessionState is an online AI of the folder.
func sessionState(name, app string, since time.Time) dto.SessionState {
	return dto.SessionState{
		Exists: true, Online: true, App: app, ConnectedAt: &since,
		Intelligence: &dto.IntelligenceRef{Kind: dto.IntelligenceKindAI, DisplayName: name},
	}
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
	input := io.Reader(bytes.NewReader(nil))
	if m.input != "" {
		input = strings.NewReader(m.input)
	}
	hostname := m.hostname
	r.code = execute(context.Background(), append([]string{"partyline"}, args...), stdio{
		stdin: input, stdout: &bytes.Buffer{}, stderr: &errOut,
		getenv: func(k string) string {
			if v, ok := m.getenv[k]; ok {
				return v
			}
			return vars[k]
		},
		getwd:      func() (string, error) { return m.cwd, nil },
		hostname:   func() (string, error) { return hostname, nil },
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
		environ:     func() []string { return append([]string(nil), m.environ...) },
		pid:         func() int { return m.pid },
		isTerminal:  func(fd int) bool { return m.terminals[fd] },
		now:         m.now,
		sessionHTTP: m.sessionHTTP,
		exec: func(path string, argv, env []string) (int, error) {
			r.execed, r.path, r.argv, r.env = true, path, argv, env
			if m.afterExec != nil {
				m.afterExec(env)
			}
			if m.harnessExit != nil {
				return *m.harnessExit, nil
			}
			return 7, nil
		},
	})
	r.stderr = errOut.String()
	return r
}

// HAR-03: every launcher (and Claude's aliases) execs the harness binary found on PATH with
// the user's arguments unchanged (Claude first gets the development-channels flag), returns
// its exit code, and passes this environment on with PARTYLINE_FOLDER set to the folder it runs
// in in place of the inherited one and PARTYLINE_HARNESS set to the harness id (CLI-01,
// AID-07). A registered harness starts without a warning.
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

			// The harnesses that get their messages from a listen process share one live id with their tools (SESS-45).
			env := slices.DeleteFunc(slices.Clone(r.env), func(kv string) bool { return strings.HasPrefix(kv, liveEnvVar+"=") })
			require.Equal(t, []string{"HOME=" + m.home, "PATH=/usr/local/bin:/usr/bin", "LANG=C.UTF-8", "PARTYLINE_FOLDER=/work/acme", "PARTYLINE_HARNESS=" + c.binary}, env)
			lives := envValues(r.env, liveEnvVar)
			if c.binary == "pi" || c.binary == "opencode" {
				require.Len(t, lives, 1)
				pid, id, ok := parseLive(lives[0])
				require.True(t, ok, lives[0])
				require.Equal(t, m.pid, pid, "the id belongs to the process exec keeps")
				require.Regexp(t, `^[A-Za-z0-9_-]{22}$`, id)
			} else {
				require.Empty(t, lives)
			}
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

// AID-07: the launcher passes on the canonical folder it runs in: absolute, symlinks resolved,
// no trailing separator, whatever the working directory looked like.
func TestLauncherPassesTheCanonicalFolder(t *testing.T) {
	m := newLaunchMachine(t)
	m.onPath["claude"] = true
	m.register(t, "claude")
	target := filepath.Join(t.TempDir(), "the lab", "Café")
	require.NoError(t, os.MkdirAll(target, 0o755))
	resolved, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(target, link))

	for _, cwd := range []string{link, link + "/", target + "/."} {
		m.cwd = cwd
		r := m.run(t, "claude")
		require.Equal(t, 7, r.code, r.stderr)
		require.Contains(t, r.env, "PARTYLINE_FOLDER="+resolved, "working directory %s", cwd)
		require.NotContains(t, r.env, "PARTYLINE_FOLDER=/inherited/folder")
	}
}

// MIG-02, CLI-01: leading --as (also --name, the older spelling, and --as=<v>) is taken off
// the harness's arguments and passed on as PARTYLINE_AGENT, replacing an inherited value; later
// arguments that look the same go to the harness unchanged. --project is accepted and ignored
// with one line on stderr. A flag without a value exits 2 and runs nothing.
func TestLauncherPassesTheIdentityToAdopt(t *testing.T) {
	m := newLaunchMachine(t)
	m.environ = append(m.environ, "PARTYLINE_AGENT=inherited", "PARTYLINE_HARNESS=pi")
	m.onPath["claude"] = true
	m.register(t, "claude")
	base := []string{"HOME=" + m.home, "PATH=/usr/local/bin:/usr/bin", "LANG=C.UTF-8", "PARTYLINE_FOLDER=/work/acme", "PARTYLINE_HARNESS=claude"}

	for _, flags := range [][]string{{"--as", "atlas-bot"}, {"--name", "atlas-bot"}, {"--as=atlas-bot"}} {
		r := m.run(t, append(append([]string{"claude"}, flags...), "--resume", "--as", "x")...)
		require.Equal(t, 7, r.code, r.stderr)
		require.Empty(t, r.stderr, "%v", flags)
		require.Equal(t, []string{"claude", "--dangerously-load-development-channels", "server:partyline", "--resume", "--as", "x"}, r.argv, "%v", flags)
		require.Equal(t, append(append([]string(nil), base...), "PARTYLINE_AGENT=atlas-bot"), r.env, "%v", flags)
	}

	r := m.run(t, "claude", "--project=Atlas", "--resume")
	require.Equal(t, 7, r.code, r.stderr)
	require.Equal(t, "partyline claude: --project is ignored: an AI is identified by its folder\n", r.stderr)
	require.Equal(t, []string{"claude", "--dangerously-load-development-channels", "server:partyline", "--resume"}, r.argv)
	require.Equal(t, []string{"HOME=" + m.home, "PATH=/usr/local/bin:/usr/bin", "LANG=C.UTF-8", "PARTYLINE_AGENT=inherited", "PARTYLINE_FOLDER=/work/acme", "PARTYLINE_HARNESS=claude"}, r.env,
		"an inherited PARTYLINE_AGENT stays and no project is passed on")

	for _, flag := range []string{"--as", "--name", "--project"} {
		r = m.run(t, "claude", flag)
		require.Equal(t, 2, r.code, flag)
		require.False(t, r.execed, flag)
		require.Equal(t, "partyline claude: "+flag+" needs a value\n", r.stderr)
	}
}

// Assumption "Home folder or /": a launch from the home folder is an ordinary launch, with no
// warning and the home folder as its folder.
func TestLauncherDoesNotWarnInTheHomeFolder(t *testing.T) {
	m := newLaunchMachine(t)
	m.onPath["claude"] = true
	m.register(t, "claude")
	home, err := filepath.EvalSymlinks(m.home)
	require.NoError(t, err)
	for _, cwd := range []string{m.home, "/"} {
		m.cwd = cwd
		r := m.run(t, "claude")
		require.Equal(t, 7, r.code)
		require.True(t, r.execed)
		require.Empty(t, r.stderr, "working directory %s", cwd)
	}
	m.cwd = m.home
	require.Contains(t, m.run(t, "claude").env, "PARTYLINE_FOLDER="+home)
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

// SESS-16: a live AI no longer makes the launcher ask. Whatever is ready on stdin and whichever file
// descriptors are terminals, the harness starts as another session with no takeover; the channel
// announces the session it became (SESS-15).
func TestLauncherStartsWithoutAskingOnATerminal(t *testing.T) {
	since := time.Date(2026, 10, 21, 12, 30, 0, 0, time.UTC)
	for name, answer := range map[string]string{
		"yes": "y\n", "no": "n\n", "no word": "no\n", "enter": "\n", "eof": "", "other": "sure\n",
	} {
		t.Run(name, func(t *testing.T) {
			m := newLaunchMachine(t)
			m.onPath["claude"] = true
			m.register(t, "claude")
			m.input = answer
			m.terminals = map[int]bool{0: true, 2: true}
			m.answerSession(t, sessionState("Atlas", "claude", since), func(_ *http.Request) {
				t.Error("a plain launch asks the server nothing")
			})

			r := m.run(t, "claude")
			require.Equal(t, 7, r.code, r.stderr)
			require.True(t, r.execed, "stdin is never read for a takeover answer")
			require.Empty(t, r.stderr, "the launcher neither prompts nor reports the live session")
			require.NotContains(t, r.env, "PARTYLINE_TAKEOVER=1")
		})
	}
}

// SESS-16: the second `partyline claude` in the folder of a live AI starts, with no takeover and no
// prompt, and the AI keeps both sessions (the channel announces the label it got, SESS-15).
func TestLauncherStartsASecondSessionInOneFolder(t *testing.T) {
	start := time.Date(2026, 10, 21, 12, 30, 0, 0, time.UTC)
	env := testkit.New(t, testkit.WithClock(func() time.Time { return start }))
	alice := env.NewUser("alice")
	_, secret, err := env.App.Users.CreateToken(context.Background(), alice.User.ID, users.TokenInput{Kind: dto.TokenKindAgent, Name: "laptop"})
	require.NoError(t, err)
	folder := testkit.Folder{Machine: "testbox", Path: "/work/acme", Harness: "claude"}
	holder, resp := alice.WithBearer(secret).DialFolder(folder)
	require.NotNil(t, holder, "upgrade refused: %d %s", resp.Status, resp.Body)
	holder.Welcome(chanWait)

	m := newLaunchMachine(t)
	m.onPath["claude"] = true
	m.register(t, "claude")
	m.input = "n\n"
	m.terminals = map[int]bool{0: true, 2: true}
	m.saveConfig(t, env.URL, secret)
	code := 0
	m.harnessExit = &code

	r := m.run(t, "claude")
	require.Equal(t, 0, r.code, r.stderr)
	require.True(t, r.execed, "a second launch in the same folder starts")
	require.Empty(t, r.stderr, "no prompt and no exit 1")
	require.NotContains(t, r.env, "PARTYLINE_TAKEOVER=1")

	second, resp := alice.WithBearer(secret).DialFolder(folder)
	require.NotNil(t, second, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Equal(t, "claude-2", second.Welcome(chanWait).Session.Label, "the second session is parallel")
	require.NoError(t, holder.Closed(100*time.Millisecond), "the principal session is left alone")
}

// SESS-16: without a terminal on stdin and stderr there is nothing to skip either: the harness starts
// with no takeover, exactly as on a terminal.
func TestLauncherDoesNotAskWithoutATerminal(t *testing.T) {
	since := time.Date(2026, 10, 21, 12, 30, 0, 0, time.UTC)
	for name, terminals := range map[string]map[int]bool{
		"none": nil, "stdin only": {0: true}, "stderr only": {2: true},
	} {
		t.Run(name, func(t *testing.T) {
			m := newLaunchMachine(t)
			m.onPath["claude"] = true
			m.register(t, "claude")
			m.terminals = terminals
			m.answerSession(t, sessionState("Atlas", "claude", since), func(_ *http.Request) {
				t.Error("the launcher asked without a terminal")
			})

			r := m.run(t, "claude")
			require.Equal(t, 7, r.code, r.stderr)
			require.Empty(t, r.stderr)
			require.True(t, r.execed)
			require.NotContains(t, r.env, "PARTYLINE_TAKEOVER=1")
		})
	}
}

// SESS-16: an identity without a live session starts without a prompt, even on a terminal.
func TestLauncherDoesNotAskWhenTheIdentityIsFree(t *testing.T) {
	m := newLaunchMachine(t)
	m.onPath["claude"] = true
	m.register(t, "claude")
	m.terminals = map[int]bool{0: true, 2: true}
	m.answerSession(t, dto.SessionState{}, nil)

	r := m.run(t, "claude")
	require.Equal(t, 7, r.code, r.stderr)
	require.Empty(t, r.stderr, "no prompt for a free identity")
	require.True(t, r.execed)
	require.NotContains(t, r.env, "PARTYLINE_TAKEOVER=1")
}

// SESS-13, SESS-16: --take-over (anywhere among the leading flags) and PARTYLINE_TAKEOVER=1 are
// the whole consent: the launcher takes over without asking and strips the flag from the harness's
// arguments.
func TestLauncherTakesOverWithoutAsking(t *testing.T) {
	since := time.Date(2026, 10, 21, 12, 30, 0, 0, time.UTC)
	for name, setup := range map[string]func(m *launchMachine){
		"flag": func(m *launchMachine) { m.getenv = map[string]string{} },
		"env":  func(m *launchMachine) { m.getenv = map[string]string{"PARTYLINE_TAKEOVER": "1"} },
	} {
		t.Run(name, func(t *testing.T) {
			m := newLaunchMachine(t)
			setup(m)
			m.onPath["claude"] = true
			m.register(t, "claude")
			m.terminals = map[int]bool{0: true, 2: true}
			m.answerSession(t, sessionState("Atlas", "claude", since), func(_ *http.Request) {
				t.Error("the launcher asked although takeover was given")
			})
			args := []string{"claude", "--as", "atlas-bot", "--resume"}
			if name == "flag" {
				args = []string{"claude", "--as", "atlas-bot", "--take-over", "--resume"}
			}
			r := m.run(t, args...)
			require.Equal(t, 7, r.code, r.stderr)
			require.Empty(t, r.stderr, "no prompt")
			require.Equal(t, []string{"claude", "--dangerously-load-development-channels", "server:partyline", "--resume"}, r.argv)
			require.Contains(t, r.env, "PARTYLINE_TAKEOVER=1")
			require.Contains(t, r.env, "PARTYLINE_AGENT=atlas-bot")
		})
	}
}

// SESS-16: a plain launch asks the server nothing, so an unreachable one changes nothing.
func TestLauncherStartsWithoutAskingWhenTheServerIsUnreachable(t *testing.T) {
	m := newLaunchMachine(t)
	m.onPath["claude"] = true
	m.register(t, "claude")
	m.terminals = map[int]bool{0: true, 2: true}
	m.input = "y\n"
	m.saveConfig(t, "http://127.0.0.1:1", launchToken)

	r := m.run(t, "claude")
	require.Equal(t, 7, r.code, r.stderr)
	require.Empty(t, r.stderr)
	require.True(t, r.execed)
	require.NotContains(t, r.env, "PARTYLINE_TAKEOVER=1")
}

// ORG-05: a placement check that does not answer within 2 seconds never holds the person up: the
// harness starts and the session reports the problem itself.
func TestLauncherGivesUpOnASlowServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // the launcher gave up
	}))
	t.Cleanup(srv.Close)
	m := newLaunchMachine(t)
	m.onPath["claude"] = true
	m.register(t, "claude")
	m.terminals = map[int]bool{0: true, 2: true}
	m.input = "y\n"
	m.saveConfig(t, srv.URL, launchToken)

	started := time.Now()
	r := m.run(t, "claude", "--org", "acme")
	elapsed := time.Since(started)
	require.Equal(t, 7, r.code, r.stderr)
	require.Empty(t, r.stderr)
	require.True(t, r.execed)
	require.NotContains(t, r.env, "PARTYLINE_TAKEOVER=1")
	require.GreaterOrEqual(t, elapsed, 1900*time.Millisecond, "the launcher waited for the 2 s timeout")
	require.Less(t, elapsed, 10*time.Second, "the launcher did not wait much longer than 2 s")
}

// Spec issue 6: Codex and Gemini open no live session, so their launchers never take over; they
// print one informational line when the AI is live elsewhere and start.
func TestToolsOnlyLaunchersAnnounceTheLiveSession(t *testing.T) {
	since := time.Date(2026, 10, 21, 12, 30, 0, 0, time.UTC)
	const want = "Atlas is also open in another session (Claude Code, since "
	for _, command := range []string{"codex", "gemini"} {
		for _, takeOver := range []bool{false, true} {
			name := command
			if takeOver {
				name += " with --take-over"
			}
			t.Run(name, func(t *testing.T) {
				m := newLaunchMachine(t)
				m.onPath[command] = true
				m.register(t, command)
				m.terminals = map[int]bool{0: true, 2: true}
				m.answerSession(t, sessionState("Atlas", "claude", since), nil)
				args := []string{command}
				if takeOver {
					args = append(args, "--take-over")
				}

				r := m.run(t, args...)
				require.Equal(t, 7, r.code, r.stderr)
				require.True(t, r.execed, "the tools-only session starts")
				require.Equal(t, want+since.In(time.Local).Format("15:04")+"); this session uses tools only\n", r.stderr)
				require.NotContains(t, r.env, "PARTYLINE_TAKEOVER=1", "a tools-only session takes nothing over")
			})
		}
	}
}

// SESS-13, SESS-16: --take-over is the whole consent: the launcher starts the harness with
// PARTYLINE_TAKEOVER=1 and asks nothing, and the harness's channel (simulated here) then takes the AI
// over: the old session is closed 4002 taken_over and the new one holds the identity.
func TestLauncherTakesOverTheLiveSessionEndToEnd(t *testing.T) {
	start := time.Date(2026, 10, 21, 12, 30, 0, 0, time.UTC)
	env := testkit.New(t, testkit.WithClock(func() time.Time { return start }))
	alice := env.NewUser("alice")
	_, secret, err := env.App.Users.CreateToken(context.Background(), alice.User.ID, users.TokenInput{Kind: dto.TokenKindAgent, Name: "laptop"})
	require.NoError(t, err)
	folder := testkit.Folder{Machine: "testbox", Path: "/work/acme", Harness: "claude"}
	holder, resp := alice.WithBearer(secret).DialFolder(folder)
	require.NotNil(t, holder, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Equal(t, "Acme", holder.Welcome(chanWait).Intelligence.DisplayName)

	m := newLaunchMachine(t)
	m.onPath["claude"] = true
	m.register(t, "claude")
	m.terminals = map[int]bool{0: true, 2: true}
	m.saveConfig(t, env.URL, secret)
	var taker *testkit.AgentConn
	m.afterExec = func(environ []string) {
		require.Contains(t, environ, "PARTYLINE_TAKEOVER=1")
		conn, resp := alice.WithBearer(secret).DialFolder(testkit.Folder{Machine: "testbox", Path: "/work/acme", Harness: "claude", Takeover: true})
		require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
		conn.Welcome(chanWait)
		taker = conn
	}

	r := m.run(t, "claude", "--take-over")
	require.Equal(t, 7, r.code, r.stderr)
	require.Empty(t, r.stderr, "no prompt: --take-over is the consent")
	require.NotNil(t, taker)
	require.Equal(t, websocket.StatusCode(4002), websocket.CloseStatus(holder.Closed(chanWait)), "the old session is closed taken_over")
	require.NoError(t, taker.Closed(100*time.Millisecond), "the new session is left alone")
}

// fakeWorktree makes this test see folder as a linked worktree of main: the launcher and the
// sessions of one test read the helper through worktreeMainFolder, and calls counts its runs.
func fakeWorktree(t *testing.T, folder, main string, calls *int) {
	t.Helper()
	previous := worktreeMainFolder
	t.Cleanup(func() { worktreeMainFolder = previous })
	worktreeMainFolder = func(dir string) string {
		if calls != nil {
			*calls++
		}
		if dir != folder {
			return ""
		}
		return main
	}
}

// envValues returns the values of the variable name in a child environment, in order.
func envValues(env []string, name string) []string {
	var out []string
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			out = append(out, v)
		}
	}
	return out
}

// SESS-01, SESS-09: a launch in a linked worktree hands the main worktree's folder to the session as
// PARTYLINE_MAIN_FOLDER next to its own folder, and replaces one it inherited; a --parent launch
// hands none, so the child is another AI.
func TestLauncherPassesTheMainWorktreeFolder(t *testing.T) {
	m := newLaunchMachine(t)
	m.onPath["claude"] = true
	m.register(t, "claude")
	m.cwd = "/repo/wt"
	m.environ = append(m.environ, "PARTYLINE_MAIN_FOLDER=/inherited/repo")
	fakeWorktree(t, "/repo/wt", "/repo", nil)

	r := m.run(t, "claude")
	require.Equal(t, 7, r.code, r.stderr)
	require.Equal(t, []string{"/repo/wt"}, envValues(r.env, "PARTYLINE_FOLDER"), "the session keeps its own folder")
	require.Equal(t, []string{"/repo"}, envValues(r.env, "PARTYLINE_MAIN_FOLDER"))

	withParent := m.run(t, "claude", "--parent", "@lead")
	require.Equal(t, 7, withParent.code, withParent.stderr)
	require.Empty(t, envValues(withParent.env, "PARTYLINE_MAIN_FOLDER"), "a child session keeps its own AI")
	require.Equal(t, []string{"@lead"}, envValues(withParent.env, "PARTYLINE_LAUNCH_PARENT"))

	// Outside a linked worktree there is no main folder, and an inherited one is never handed on.
	m.cwd = "/work/plain"
	plain := m.run(t, "claude")
	require.Equal(t, 7, plain.code, plain.stderr)
	require.Empty(t, envValues(plain.env, "PARTYLINE_MAIN_FOLDER"))
	require.Equal(t, []string{"/work/plain"}, envValues(plain.env, "PARTYLINE_FOLDER"))
}

// SESS-01, SESS-09: a session reads the main folder the launcher handed it and runs no git; a session
// started by hand computes it once; a child session never does, so --parent keeps its own AI.
func TestSessionIdentityReadsTheMainFolder(t *testing.T) {
	folder := canonicalDir(t, "repo", "wt")
	calls := 0
	fakeWorktree(t, folder, "/repo", &calls)
	std := func(env map[string]string) stdio {
		return stdio{
			getenv:   func(k string) string { return env[k] },
			getwd:    func() (string, error) { return folder, nil },
			hostname: func() (string, error) { return "devbox", nil },
		}
	}

	id := sessionIdentity("", std(map[string]string{"PARTYLINE_FOLDER": folder, "PARTYLINE_MAIN_FOLDER": "/repo"}))
	require.Equal(t, channel.Identity{Machine: "devbox", Folder: folder, MainFolder: "/repo"}, id)
	require.Equal(t, 0, calls, "a session the launcher started runs no git")

	id = sessionIdentity("", std(map[string]string{"PARTYLINE_FOLDER": folder}))
	require.Equal(t, channel.Identity{Machine: "devbox", Folder: folder, MainFolder: "/repo"}, id)
	require.Equal(t, 1, calls, "a hand-started session computes the main folder once")

	id = sessionIdentity("", std(map[string]string{"PARTYLINE_FOLDER": folder, "PARTYLINE_LAUNCH_PARENT": "@lead"}))
	require.Equal(t, channel.Identity{Machine: "devbox", Folder: folder, Parent: "@lead"}, id)
	require.Equal(t, 1, calls, "a child session computes nothing")

	id = sessionIdentity("", std(map[string]string{"PARTYLINE_FOLDER": "/work/plain"}))
	require.Equal(t, "", id.MainFolder, "outside a linked worktree there is none")
	require.Equal(t, 2, calls)
}

// SESS-01, SESS-03: `partyline listen` in a linked worktree sends the main worktree's folder with its
// connection (percent-encoded like the folder, so a path with a space travels), and the server
// resolves the repository's AI while the session keeps its own folder.
func TestListenSendsTheMainWorktreeFolder(t *testing.T) {
	e := newChannelEnv(t)
	startListen(t, e.vars(
		"PARTYLINE_HARNESS", "claude", "PARTYLINE_FOLDER", "/repo/wt", "PARTYLINE_MAIN_FOLDER", "/repo/the repo",
	), "/repo/wt", nil)

	e.ai(t, "/repo/the repo")
	require.Equal(t, []string{"/repo/the repo"}, e.folders(t))
}

// SESS-01, SESS-09: the launcher's own question to the server names the main folder of the folder it
// launches in - never the one of the session it runs in - and a child launch (--parent) names none.
func TestLauncherSessionLookupRecomputesTheMainFolder(t *testing.T) {
	ask := func(t *testing.T, cwd string, args ...string) http.Header {
		t.Helper()
		m := newLaunchMachine(t)
		m.onPath["claude"] = true
		m.register(t, "claude")
		m.cwd = cwd
		m.environ = append(m.environ, "PARTYLINE_MAIN_FOLDER=/inherited/repo") // the session this launch runs in
		fakeWorktree(t, "/repo/wt", "/repo", nil)
		var header http.Header
		m.serveSession(t, func(w http.ResponseWriter, r *http.Request) {
			header = r.Header.Clone()
			require.NoError(t, json.NewEncoder(w).Encode(dto.SessionState{}))
		})
		r := m.run(t, append([]string{"claude", "--org", "acme"}, args...)...)
		require.Equal(t, 7, r.code, r.stderr)
		require.NotNil(t, header)
		return header
	}

	h := ask(t, "/repo/wt")
	require.Equal(t, "%2Frepo", h.Get(dto.HeaderMainFolder), "the launch folder's own main folder")
	h = ask(t, "/work/plain")
	require.Empty(t, h.Values(dto.HeaderMainFolder), "a plain folder has none, whatever the current session held")
	h = ask(t, "/repo/wt", "--parent", "@lead")
	require.Empty(t, h.Values(dto.HeaderMainFolder), "a child launch names none")
}

// SESS-09: a session whose launch parent is set (a --parent child in a linked worktree) sends no main
// folder, neither the one it was handed nor one it would compute.
func TestParentChildInAWorktreeSendsNoMainFolder(t *testing.T) {
	folder := canonicalDir(t, "repo", "wt")
	calls := 0
	fakeWorktree(t, folder, "/repo", &calls)
	id := sessionIdentity("", stdio{
		getenv: func(k string) string {
			return map[string]string{"PARTYLINE_FOLDER": folder, "PARTYLINE_LAUNCH_PARENT": "@lead", "PARTYLINE_MAIN_FOLDER": "/repo"}[k]
		},
		getwd:    func() (string, error) { return folder, nil },
		hostname: func() (string, error) { return "devbox", nil },
	})
	require.Equal(t, "@lead", id.Parent)
	require.Empty(t, id.MainFolder, "the child keeps its own AI")
	require.Equal(t, 0, calls)
	h := http.Header{}
	setIdentityHeaders(h, id)
	require.Empty(t, h.Values(dto.HeaderMainFolder))
}

// SESS-45: a launch gives its pi or OpenCode session a new live id and never hands on the one of the
// session it runs in; Claude Code, Codex and Gemini get none, whatever they inherit.
func TestLauncherNeverHandsOnAnotherSessionsLiveID(t *testing.T) {
	inherited := liveEnvVar + "=999:AAAAAAAAAAAAAAAAAAAAAA"
	for _, tc := range []struct {
		command string
		wantOwn bool
	}{{"pi", true}, {"opencode", true}, {"claude", false}, {"codex", false}, {"gemini", false}, {"omp", false}} {
		t.Run(tc.command, func(t *testing.T) {
			m := newLaunchMachine(t)
			m.environ = append(m.environ, inherited)
			m.onPath[tc.command] = true
			m.register(t, tc.command)

			r := m.run(t, tc.command)
			require.True(t, r.execed)
			lives := envValues(r.env, liveEnvVar)
			if !tc.wantOwn {
				require.Empty(t, lives)
				return
			}
			require.Len(t, lives, 1)
			require.NotEqual(t, strings.TrimPrefix(inherited, liveEnvVar+"="), lives[0])
			pid, _, ok := parseLive(lives[0])
			require.True(t, ok)
			require.Equal(t, m.pid, pid)
		})
	}
}
