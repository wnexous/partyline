package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/cliconfig"
)

// loggedInSetup saves the credentials setup and login read, so a test can start after login.
func loggedInSetup(t *testing.T) (home string, opts setupOpts) {
	t.Helper()
	e := newChannelEnv(t)
	home = t.TempDir()
	writeConfig(t, home, cliconfig.Config{URL: e.env.URL, Token: e.secret})
	return home, setupOpts{env: map[string]string{"HOME": home}, hostname: "devbox"}
}

// CLI-04: a menu apply registers the selected unregistered harnesses, removes Partyline from
// the deselected registered ones, leaves the selected registered ones untouched and prints one
// result line per changed harness. The fake runner records exactly the harness CLI calls.
func TestSetupMenuAppliesTheChecklist(t *testing.T) {
	home, opts := loggedInSetup(t)
	calls := &harnessCalls{}
	opts.lookPath = installed("claude", "gemini", "pi", "omp")
	opts.run = calls.run
	opts.terminal = true

	// pi and omp are registered already (file-based harnesses, no CLI call).
	registered := runSetup(t, opts, "--harness", "pi,omp", "--yes")
	require.Equal(t, 0, registered.code, "stderr: %s", registered.stderr)
	require.Empty(t, calls.calls(), "file harnesses need no CLI")
	piConfig := filepath.Join(home, ".pi", "agent", "mcp.json")
	ompConfig := filepath.Join(home, ".omp", "agent", "mcp.json")
	require.FileExists(t, piConfig)
	require.FileExists(t, ompConfig)

	// Detected harnesses are pre-selected. Move to pi and deselect it: claude and gemini are
	// registered, pi is removed, omp stays exactly as it was.
	opts.stdin = "jjjj \r"
	calls = &harnessCalls{}
	opts.run = calls.run
	r := runSetup(t, opts)
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Equal(t, []string{claudeAdd, geminiAdd}, mutationCalls(calls.calls()), "CLI-04: only the changed harnesses")

	rows := menuRows(r.stdout)
	require.Contains(t, rows, "> [ ] pi  installed · registered", "deselected: %v", rows)
	require.Contains(t, rows, "  [x] Claude Code  installed · not registered")
	require.Contains(t, rows, "  [x] oh-my-pi (omp)  installed · registered")
	require.Contains(t, r.stdout, "✓ Claude Code: registered\n")
	require.Contains(t, r.stdout, "✓ Gemini CLI: registered\n")
	require.Contains(t, r.stdout, "✓ pi: removed\n")
	require.NotContains(t, r.stdout, "oh-my-pi (omp): ", "the untouched harness gets no result line")
	require.True(t, strings.HasSuffix(r.stdout, "\nStart a session with partyline claude.\n"), r.stdout)

	require.NoFileExists(t, piConfig, "pi was removed and its config held nothing else")
	omp, err := os.ReadFile(ompConfig)
	require.NoError(t, err)
	require.Contains(t, string(omp), "partyline", "omp was not touched")
}

// CLI-03 + CLI-04: q cancels the menu without changes and without harness calls.
func TestSetupMenuCancelChangesNothing(t *testing.T) {
	home, opts := loggedInSetup(t)
	calls := &harnessCalls{}
	opts.lookPath = installed("pi")
	opts.run = calls.run
	opts.terminal = true
	require.Equal(t, 0, runSetup(t, opts, "--harness", "pi", "--yes").code)
	piConfig := filepath.Join(home, ".pi", "agent", "mcp.json")
	before, err := os.ReadFile(piConfig)
	require.NoError(t, err)

	opts.stdin = " q"
	r := runSetup(t, opts)
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.NotContains(t, r.stdout, "✓")
	require.Empty(t, mutationCalls(calls.calls()))
	after, err := os.ReadFile(piConfig)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

// CLI-05: without a terminal setup never shows the menu and requires one of the selection
// flags; each flag makes exactly the registrar calls it names.
func TestSetupNonTerminalSelectionFlags(t *testing.T) {
	t.Run("no flags", func(t *testing.T) {
		_, opts := loggedInSetup(t)
		calls := &harnessCalls{}
		opts.lookPath, opts.run = installed("claude"), calls.run
		r := runSetup(t, opts)
		require.Equal(t, 2, r.code)
		require.Contains(t, r.stderr, "no terminal: pass --harness a,b, --all-detected or --remove a,b")
		require.Empty(t, calls.calls())
	})

	t.Run("harness", func(t *testing.T) {
		_, opts := loggedInSetup(t)
		calls := &harnessCalls{}
		opts.lookPath, opts.run = installed("claude", "gemini"), calls.run
		r := runSetup(t, opts, "--harness", "gemini", "--yes")
		require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
		require.Equal(t, []string{geminiAdd}, calls.calls())
		require.Contains(t, r.stdout, "✓ Gemini CLI: registered")
	})

	t.Run("all detected", func(t *testing.T) {
		_, opts := loggedInSetup(t)
		calls := &harnessCalls{}
		opts.lookPath, opts.run = installed("claude", "gemini"), calls.run
		r := runSetup(t, opts, "--all-detected", "--yes")
		require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
		require.Equal(t, []string{claudeAdd, geminiAdd}, calls.calls())
		require.Contains(t, r.stdout, "✓ Claude Code: registered")
		require.Contains(t, r.stdout, "✓ Gemini CLI: registered")
	})

	t.Run("remove", func(t *testing.T) {
		home, opts := loggedInSetup(t)
		settings := filepath.Join(home, ".gemini", "settings.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(settings), 0o755))
		require.NoError(t, os.WriteFile(settings, []byte(`{"mcpServers":{"partyline":{"command":"`+testBinary+`","args":["channel"]}}}`), 0o644))
		calls := &harnessCalls{}
		opts.lookPath, opts.run = installed("gemini"), calls.run
		r := runSetup(t, opts, "--remove", "gemini", "--yes")
		require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
		require.Equal(t, []string{"gemini mcp remove -s user partyline"}, calls.calls())
		require.Contains(t, r.stdout, "✓ Gemini CLI: removed")
	})
}

// CLI-06: a machine without a saved login is told to log in first, with or without a
// terminal, and nothing is written.
func TestSetupNotLoggedIn(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		home := t.TempDir()
		r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}, terminal: terminal})
		require.Equal(t, 1, r.code, "terminal=%v", terminal)
		require.Contains(t, r.stderr, "Run partyline login first")
		require.NotContains(t, r.stdout, cursorHide, "no menu")
		_, err := os.Stat(configPath(home))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

// CLI-02: login on a terminal with nothing registered opens the setup menu and applies it.
func TestLoginOpensTheMenuWhenNothingRegistered(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	calls := &harnessCalls{}
	r := runLogin(t, setupOpts{
		env: map[string]string{"HOME": home}, hostname: "devbox", terminal: true,
		lookPath: installed("claude"), run: calls.run,
		stdin: e.secret + "\n\r",
	}, "--url", e.env.URL, "--token-stdin")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "Logged in as @alice on "+e.env.URL)
	require.Contains(t, r.stdout, "Claude Code  installed · not registered", "the menu opened")
	require.Contains(t, r.stdout, "✓ Claude Code: registered")
	require.Equal(t, []string{claudeAdd}, mutationCalls(calls.calls()))
}

// CLI-02: login does not open the menu when a harness is already registered.
func TestLoginKeepsTheMenuClosedWhenAHarnessIsRegistered(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	opts := setupOpts{env: map[string]string{"HOME": home}, hostname: "devbox", terminal: true, lookPath: installed("pi")}
	require.Equal(t, 0, runSetup(t, opts, "--url", e.env.URL, "--token", e.secret, "--harness", "pi", "--yes").code)

	calls := &harnessCalls{}
	opts.run = calls.run
	opts.stdin = e.secret + "\n"
	r := runLogin(t, opts, "--url", e.env.URL, "--token-stdin")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "Logged in as @alice")
	require.NotContains(t, r.stdout, cursorHide, "a registered harness keeps the menu closed")
	require.Empty(t, mutationCalls(calls.calls()))
}

// CLI-07: the scripted `--token-stdin --harness claude --yes` setup still works.
func TestSetupTokenStdinHarnessClaudeYes(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	calls := &harnessCalls{}
	r := runSetup(t, setupOpts{
		env: map[string]string{"HOME": home}, hostname: "devbox", stdin: e.secret + "\n",
		lookPath: installed("claude"), run: calls.run,
	}, "--url", e.env.URL, "--token-stdin", "--harness", "claude", "--yes")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	requireSavedConfig(t, home, cliconfig.Config{URL: e.env.URL, Token: e.secret})
	require.Equal(t, []string{claudeAdd}, calls.calls())
	require.Contains(t, r.stdout, "Logged in as @alice on "+e.env.URL)
	require.Contains(t, r.stdout, "✓ Claude Code: registered")
}
