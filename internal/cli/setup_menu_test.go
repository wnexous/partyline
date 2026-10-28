package cli

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
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

// resultLinesOf are the lines of a setup run that report the harness: ✓ for what worked, - for a
// harness that is not registered and was left so, ✗ for a failure.
func resultLinesOf(out, title string) []string {
	return regexp.MustCompile(`(?m)^(?:✓|✗|-) `+regexp.QuoteMeta(title)+`: .+$`).FindAllString(ansi.ReplaceAllString(out, ""), -1)
}

// CLI-04: a menu apply registers the selected unregistered harnesses, removes Partyline from
// the deselected registered ones, leaves the selected registered ones untouched and prints one
// result line per harness, changed or not. The fake runner records exactly the harness CLI calls.
func TestSetupMenuAppliesTheChecklist(t *testing.T) {
	home, opts := loggedInSetup(t)
	calls := &harnessCalls{}
	opts.lookPath = installed("claude", "opencode", "pi", "omp")
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

	// Detected harnesses are pre-selected. Move to pi and deselect it: claude and opencode are
	// registered, pi is removed, omp stays exactly as it was.
	opts.stdin = "jj \r"
	calls = &harnessCalls{}
	opts.run = calls.run
	r := runSetup(t, opts)
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Equal(t, []string{claudeAdd}, mutationCalls(calls.calls()), "CLI-04: only the changed harnesses (OpenCode is a file)")
	require.FileExists(t, filepath.Join(home, ".config", "opencode", "opencode.json"))

	rows := menuRows(r.stdout)
	require.Contains(t, rows, "> [ ] pi  installed · registered", "deselected: %v", rows)
	require.Contains(t, rows, "  [x] Claude Code  installed · not registered")
	require.Contains(t, rows, "  [x] oh-my-pi (omp)  installed · registered")
	require.Contains(t, r.stdout, "✓ Claude Code: registered\n")
	require.Contains(t, r.stdout, "✓ OpenCode: registered\n")
	require.Contains(t, r.stdout, "✓ pi: removed\n")
	require.Contains(t, r.stdout, "✓ oh-my-pi (omp): already registered\n", "an untouched registered harness still gets its line")
	for _, title := range []string{"Claude Code", "OpenCode", "pi", "oh-my-pi (omp)"} {
		require.Len(t, resultLinesOf(r.stdout, title), 1, "one result line for %s", title)
	}
	require.True(t, strings.HasSuffix(r.stdout, "\nStart a session with partyline claude.\n"), r.stdout)

	require.NoFileExists(t, piConfig, "pi was removed and its config held nothing else")
	omp, err := os.ReadFile(ompConfig)
	require.NoError(t, err)
	require.Contains(t, string(omp), "partyline", "omp was not touched")
}

// CLI-03, CLI-04 (L-045, M15): a harness that is registered but not installed starts checked, so a
// plain Enter changes nothing and says so; unchecking it is what removes Partyline from it.
func TestSetupMenuKeepsARegisteredHarnessThatIsNotInstalled(t *testing.T) {
	home, opts := loggedInSetup(t)
	claudeConfig := filepath.Join(home, ".claude.json")
	require.NoError(t, os.WriteFile(claudeConfig, []byte(`{"mcpServers":{"partyline":{"command":"`+testBinary+`","args":["channel"]}}}`), 0o600))
	calls := &harnessCalls{}
	opts.lookPath, opts.run = installed(), calls.run // no harness CLI is installed
	opts.terminal = true

	opts.stdin = "\r"
	r := runSetup(t, opts)
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, menuRows(r.stdout), "> [x] Claude Code  not installed · registered", "it starts checked")
	require.Contains(t, r.stdout, "✓ Claude Code: already registered\n")
	require.NotContains(t, r.stdout, "removed")
	require.Empty(t, mutationCalls(calls.calls()), "Enter changed nothing")

	opts.stdin = " \r" // uncheck it
	r = runSetup(t, opts)
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "✓ Claude Code: removed\n")
	require.Equal(t, []string{"claude mcp remove --scope user partyline"}, mutationCalls(calls.calls()))
}

// CLI-03 (L-045): `a` checks the installed harnesses only; one that is not installed would fail to
// register and stays unchecked.
func TestSetupMenuAllChecksOnlyInstalledHarnesses(t *testing.T) {
	_, opts := loggedInSetup(t)
	calls := &harnessCalls{}
	opts.lookPath, opts.run = installed("claude"), calls.run
	opts.terminal = true
	opts.stdin = " a\r" // uncheck the preselected claude, then a checks it again
	r := runSetup(t, opts)
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Equal(t, []string{claudeAdd}, mutationCalls(calls.calls()), "only the installed harness is registered")
	require.Contains(t, r.stdout, "✓ Claude Code: registered\n")
	require.Contains(t, r.stdout, "- OpenCode: not registered\n")
	require.Empty(t, r.stderr)
}

// CLI-04 (L-045, M16): a harness that cannot be changed prints its error on stderr and setup exits 1;
// the other harnesses are still applied and still reported.
func TestSetupMenuExitsNonZeroWhenAHarnessFails(t *testing.T) {
	_, opts := loggedInSetup(t)
	calls := &harnessCalls{answer: func(line string) ([]byte, error) {
		if strings.HasPrefix(line, "claude mcp add") {
			return []byte("boom"), errors.New("exit status 1")
		}
		return nil, nil
	}}
	opts.lookPath, opts.run = installed("claude", "opencode"), calls.run
	opts.terminal = true
	opts.stdin = "\r" // the detected harnesses start checked
	r := runSetup(t, opts)
	require.Equal(t, 1, r.code, "a failed apply is not a success")
	require.Contains(t, r.stderr, "✗ Claude Code: ")
	require.Contains(t, r.stdout, "✓ OpenCode: registered\n", "the others are still applied")
	require.NotContains(t, r.stdout, "Claude Code: registered")
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
		opts.lookPath, opts.run = installed("claude", "pi"), calls.run
		r := runSetup(t, opts, "--harness", "pi", "--yes")
		require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
		require.Empty(t, calls.calls(), "pi is a file")
		require.Contains(t, r.stdout, "✓ pi: registered")
	})

	t.Run("all detected", func(t *testing.T) {
		_, opts := loggedInSetup(t)
		calls := &harnessCalls{}
		opts.lookPath, opts.run = installed("claude", "pi"), calls.run
		r := runSetup(t, opts, "--all-detected", "--yes")
		require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
		require.Equal(t, []string{claudeAdd}, calls.calls())
		require.Contains(t, r.stdout, "✓ Claude Code: registered")
		require.Contains(t, r.stdout, "✓ pi: registered")
	})

	t.Run("remove", func(t *testing.T) {
		home, opts := loggedInSetup(t)
		require.NoError(t, os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"partyline":{"command":"`+testBinary+`","args":["channel"]}}}`), 0o644))
		calls := &harnessCalls{}
		opts.lookPath, opts.run = installed("claude"), calls.run
		r := runSetup(t, opts, "--remove", "claude", "--yes")
		require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
		require.Equal(t, []string{"claude mcp remove --scope user partyline"}, calls.calls())
		require.Contains(t, r.stdout, "✓ Claude Code: removed")
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
