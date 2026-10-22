package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/harness"
)

// launchMachine is a machine for `partyline <harness>`: a fake HOME, the programs on PATH, the
// harnesses Partyline is registered in, the folder the launcher runs in and the environment it
// passes on.
type launchMachine struct {
	home    string
	cwd     string
	onPath  map[string]bool
	codex   bool // `codex mcp get partyline` finds the server
	environ []string
}

func newLaunchMachine(t *testing.T) *launchMachine {
	t.Helper()
	home := t.TempDir()
	return &launchMachine{home: home, cwd: "/work/acme", onPath: map[string]bool{}, environ: []string{
		"HOME=" + home, "PATH=/usr/local/bin:/usr/bin", "PARTYLINE_FOLDER=/inherited/folder", "LANG=C.UTF-8",
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
		getwd:      func() (string, error) { return m.cwd, nil },
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

			require.Equal(t, []string{"HOME=" + m.home, "PATH=/usr/local/bin:/usr/bin", "LANG=C.UTF-8", "PARTYLINE_FOLDER=/work/acme", "PARTYLINE_HARNESS=" + c.binary}, r.env)
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
