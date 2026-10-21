package cli

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/cliconfig"
)

// harnessCalls records the harness CLI calls of a setup run.
type harnessCalls struct {
	mu    sync.Mutex
	lines []string
	// answer gives the output of a call (nil: success, no output).
	answer func(line string) ([]byte, error)
}

func (h *harnessCalls) run(line string) ([]byte, error) {
	h.mu.Lock()
	h.lines = append(h.lines, line)
	h.mu.Unlock()
	if h.answer != nil {
		return h.answer(line)
	}
	return nil, nil
}

func (h *harnessCalls) calls() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.lines...)
}

// installed finds only the named harness CLIs.
func installed(names ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, n := range names {
			if n == name {
				return "/usr/bin/" + name, nil
			}
		}
		return "", errors.New("not found")
	}
}

const (
	claudeAdd = "claude mcp add --scope user partyline -- " + testBinary + " channel"
	codexAdd  = "codex mcp add partyline -- " + testBinary + " channel --tools-only"
	geminiAdd = "gemini mcp add -s user partyline " + testBinary + " channel --tools-only"
)

// HAR-01: setup offers each detected harness, shows the exact change before asking, and
// registers only what the user confirms (Enter = yes, "n" = no).
func TestSetupRegistersConfirmedHarnesses(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	calls := &harnessCalls{}
	r := runSetup(t, setupOpts{
		env: map[string]string{"HOME": home}, stdin: "\nn\n",
		lookPath: installed("claude", "codex"), run: calls.run,
	}, "--url", e.env.URL, "--token", e.secret)
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)

	require.Equal(t, []string{claudeAdd}, calls.calls(), "claude confirmed, codex declined, gemini not installed")
	claudeAt := strings.Index(r.stdout, "Claude Code:\n  run: "+claudeAdd+"\nRegister Partyline in Claude Code? [Y/n]: ")
	require.GreaterOrEqual(t, claudeAt, 0, r.stdout)
	require.Contains(t, r.stdout, "Registered Partyline in Claude Code.")
	require.Contains(t, r.stdout, "Codex CLI:\n  run: "+codexAdd+"\n")
	require.Contains(t, r.stdout, "Skipped Codex CLI.")
	require.NotContains(t, r.stdout, "Gemini CLI:")
	_, err := os.Stat(filepath.Join(home, ".codex", "hooks.json"))
	require.ErrorIs(t, err, os.ErrNotExist, "declined: no hook written")
	// WIZ-01: setup ends with the launcher of the harness it registered, not of the skipped one.
	require.True(t, strings.HasSuffix(r.stdout, "Skipped Codex CLI.\n\nStart a session with partyline claude.\n"), r.stdout)
}

// WIZ-01: after registering several harnesses setup names each one's launcher.
func TestSetupEndsWithTheLaunchers(t *testing.T) {
	e := newChannelEnv(t)
	calls := &harnessCalls{}
	r := runSetup(t, setupOpts{
		env: map[string]string{"HOME": t.TempDir()}, lookPath: installed("claude", "codex", "gemini"), run: calls.run,
	}, "--url", e.env.URL, "--token", e.secret, "--yes")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.True(t, strings.HasSuffix(r.stdout, "Registered Partyline in Gemini CLI.\n\n"+
		"Start a session with partyline claude, partyline codex or partyline gemini.\n"), r.stdout)
}

// HAR-01: --harness picks the harnesses and --yes applies without asking.
func TestSetupHarnessFlagAndYes(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	calls := &harnessCalls{}
	r := runSetup(t, setupOpts{
		env: map[string]string{"HOME": home}, lookPath: installed("claude", "gemini"), run: calls.run,
	}, "--url", e.env.URL, "--token", e.secret, "--yes", "--harness", "gemini")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Equal(t, []string{geminiAdd}, calls.calls())
	require.NotContains(t, r.stdout, "[Y/n]")
	require.Contains(t, r.stdout, "Registered Partyline in Gemini CLI.")
	require.True(t, strings.HasSuffix(r.stdout, "\nStart a session with partyline gemini.\n"), r.stdout)
	require.FileExists(t, filepath.Join(home, ".gemini", "settings.json"))

	// "none" skips the harness step.
	calls2 := &harnessCalls{}
	r = runSetup(t, setupOpts{env: map[string]string{"HOME": t.TempDir()}, lookPath: installed("claude"), run: calls2.run},
		"--url", e.env.URL, "--token", e.secret, "--yes", "--harness", "none")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Empty(t, calls2.calls())
	require.NotContains(t, r.stdout, "Start a session", "nothing registered, no launcher to name")
}

// An unknown harness name is a usage error, before anything is written.
func TestSetupUnknownHarness(t *testing.T) {
	home := t.TempDir()
	r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}}, "--url", "http://127.0.0.1:1", "--token", "x", "--harness", "claude,vim")
	require.Equal(t, 2, r.code)
	require.Contains(t, r.stderr, `unknown harness "vim"`)
	_, err := os.Stat(configPath(home))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// Without any installed harness setup says how to register one later.
func TestSetupNoHarnessFound(t *testing.T) {
	e := newChannelEnv(t)
	r := runSetup(t, setupOpts{env: map[string]string{"HOME": t.TempDir()}}, "--url", e.env.URL, "--token", e.secret)
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "No supported harness found")
	require.Contains(t, r.stdout, "Register one later with: partyline setup --harness <name>, then start it with partyline <name>\n")
	require.NotContains(t, r.stdout, "Start a session")
}

// A failing registration is reported and makes setup exit 1; the credentials stay saved.
func TestSetupHarnessFailure(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	calls := &harnessCalls{answer: func(string) ([]byte, error) {
		return []byte("permission denied"), errors.New("exit status 1")
	}}
	r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}, lookPath: installed("claude"), run: calls.run},
		"--url", e.env.URL, "--token", e.secret, "--yes")
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "Claude Code: claude mcp add")
	require.Contains(t, r.stderr, "permission denied")
	require.NotContains(t, r.stdout, "Start a session", "a failed registration names no launcher")
	requireSavedConfig(t, home, cliconfig.Config{URL: e.env.URL, Token: e.secret})
}

// HAR-04, ONB-09: --uninstall removes the harness entries setup added and the config file
// (after revoking its token on the server); the hook file setup created is gone again.
func TestSetupUninstall(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	calls := &harnessCalls{}
	r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}, lookPath: installed("codex"), run: calls.run},
		"--url", e.env.URL, "--token", e.secret, "--yes")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	hooks := filepath.Join(home, ".codex", "hooks.json")
	require.FileExists(t, hooks)

	// Declining (end of input) keeps everything.
	codexKnows := &harnessCalls{answer: func(line string) ([]byte, error) {
		if line == "codex mcp get partyline --json" {
			return []byte(`{"name":"partyline","transport":{"type":"stdio","command":"` + testBinary + `"}}`), nil
		}
		return nil, nil
	}}
	r = runSetup(t, setupOpts{env: map[string]string{"HOME": home}, lookPath: installed("codex"), run: codexKnows.run}, "--uninstall")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "Codex CLI:\n  run: codex mcp remove partyline\n  edit ~/.codex/hooks.json: remove the Partyline Stop hook\n")
	require.Contains(t, r.stdout, "Kept Codex CLI.")
	require.FileExists(t, hooks)
	require.FileExists(t, configPath(home))

	r = runSetup(t, setupOpts{env: map[string]string{"HOME": home}, lookPath: installed("codex"), run: codexKnows.run}, "--uninstall", "--yes")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, codexKnows.calls(), "codex mcp remove partyline")
	_, err := os.Stat(hooks)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(configPath(home))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Contains(t, r.stdout, "Revoked the agent token on "+e.env.URL+".\n", "ONB-09")
	e.env.Anon().WithBearer(e.secret).WithHeader(dto.HeaderAgent, "x").Get("/api/v1/agent/rooms").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	// Nothing left: uninstall says so.
	nothing := &harnessCalls{answer: func(string) ([]byte, error) {
		return []byte("Error: No MCP server named 'partyline' found."), errors.New("exit status 1")
	}}
	r = runSetup(t, setupOpts{env: map[string]string{"HOME": home}, run: nothing.run}, "--uninstall", "--yes")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "Nothing to remove")
}

// ONB-09: a revoke that fails (server unreachable) is reported, and uninstall still deletes the
// config file and succeeds.
func TestSetupUninstallReportsFailedRevoke(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, cliconfig.Config{URL: "http://127.0.0.1:1", Token: "pl_agent_gone"})
	nothing := &harnessCalls{answer: func(string) ([]byte, error) {
		return []byte("Error: No MCP server named 'partyline' found."), errors.New("exit status 1")
	}}
	r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}, run: nothing.run}, "--uninstall", "--yes")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "Could not revoke the agent token on http://127.0.0.1:1: cannot reach http://127.0.0.1:1")
	require.Contains(t, r.stdout, "Revoke it in the web app (Tokens).")
	require.NotContains(t, r.stdout+r.stderr, "pl_agent_gone")
	_, err := os.Stat(configPath(home))
	require.ErrorIs(t, err, os.ErrNotExist)
}
