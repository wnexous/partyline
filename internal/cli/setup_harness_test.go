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

// mutationCalls drops the read-only `mcp get` probes setup uses to learn the current state.
func mutationCalls(lines []string) []string {
	var out []string
	for _, l := range lines {
		if !strings.Contains(l, " mcp get ") {
			out = append(out, l)
		}
	}
	return out
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
)

// HAR-01, CLI-05: --harness registers exactly the named harness without a per-harness
// confirmation and leaves the other installed harnesses alone.
func TestSetupRegistersTheSelectedHarness(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	calls := &harnessCalls{}
	r := runSetup(t, setupOpts{
		env:      map[string]string{"HOME": home},
		lookPath: installed("claude", "opencode"), run: calls.run,
	}, "--url", e.env.URL, "--token", e.secret, "--harness", "claude", "--yes")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)

	require.Equal(t, []string{claudeAdd}, calls.calls())
	require.NotContains(t, r.stdout, "[Y/n]", "no per-harness confirmation")
	require.Contains(t, r.stdout, "✓ Claude Code: registered")
	require.NotContains(t, r.stdout, "OpenCode")
	require.True(t, strings.HasSuffix(r.stdout, "\nStart a session with partyline claude.\n"), r.stdout)
	_, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.json"))
	require.ErrorIs(t, err, os.ErrNotExist, "an unselected installed harness is untouched")
}

// WIZ-01: after registering several harnesses setup names each one's launcher.
func TestSetupEndsWithTheLaunchers(t *testing.T) {
	e := newChannelEnv(t)
	calls := &harnessCalls{}
	r := runSetup(t, setupOpts{
		env: map[string]string{"HOME": t.TempDir()}, lookPath: installed("claude", "opencode", "pi"), run: calls.run,
	}, "--url", e.env.URL, "--token", e.secret, "--yes", "--all-detected")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "✓ pi: registered")
	require.True(t, strings.HasSuffix(r.stdout, "\nStart a session with partyline claude.\n"), "the first registered harness: %s", r.stdout)
}

// HAR-01: --harness picks the harnesses and --yes applies without asking.
func TestSetupHarnessFlagAndYes(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	calls := &harnessCalls{}
	r := runSetup(t, setupOpts{
		env: map[string]string{"HOME": home}, lookPath: installed("claude", "opencode"), run: calls.run,
	}, "--url", e.env.URL, "--token", e.secret, "--yes", "--harness", "opencode")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Empty(t, calls.calls(), "OpenCode is a file")
	require.NotContains(t, r.stdout, "[Y/n]")
	require.Contains(t, r.stdout, "✓ OpenCode: registered")
	require.True(t, strings.HasSuffix(r.stdout, "\nStart a session with partyline opencode.\n"), r.stdout)
	require.FileExists(t, filepath.Join(home, ".config", "opencode", "opencode.json"))

	// "none" skips the harness step.
	calls2 := &harnessCalls{}
	r = runSetup(t, setupOpts{env: map[string]string{"HOME": t.TempDir()}, lookPath: installed("claude"), run: calls2.run},
		"--url", e.env.URL, "--token", e.secret, "--yes", "--harness", "none")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Empty(t, calls2.calls())
	require.NotContains(t, r.stdout, "Start a session", "nothing registered, no launcher to name")
}

// setup --always: setup records the choice in the installed extension and in the MCP server
// entry; naming the harness again without it (--always=false) updates both back.
func TestSetupAlwaysRoundTrip(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	ext := filepath.Join(home, ".pi", "agent", "extensions", "partyline.ts")
	cfg := filepath.Join(home, ".pi", "agent", "mcp.json")
	setup := func(flags ...string) setupRun {
		return runSetup(t, setupOpts{env: map[string]string{"HOME": home}, lookPath: installed("pi")},
			append([]string{"--url", e.env.URL, "--token", e.secret, "--harness", "pi"}, flags...)...)
	}
	read := func(path string) string {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		return string(data)
	}

	r := setup()
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, read(ext), "const PARTYLINE_ALWAYS = false;")
	require.NotContains(t, read(cfg), "--always")

	r = setup("--always")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "✓ pi: updated")
	require.Contains(t, read(ext), "const PARTYLINE_ALWAYS = true;")
	require.NotContains(t, read(ext), "const PARTYLINE_ALWAYS = false;")
	require.Contains(t, read(cfg), `"--always"`)

	r = setup("--always=false")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "✓ pi: updated")
	require.Contains(t, read(ext), "const PARTYLINE_ALWAYS = false;")
	require.NotContains(t, read(cfg), "--always")
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
	r := runSetup(t, setupOpts{env: map[string]string{"HOME": t.TempDir()}}, "--url", e.env.URL, "--token", e.secret, "--all-detected")
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
		"--url", e.env.URL, "--token", e.secret, "--yes", "--all-detected")
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "Claude Code: claude mcp add")
	require.Contains(t, r.stderr, "permission denied")
	require.NotContains(t, r.stdout, "Start a session", "a failed registration names no launcher")
	requireSavedConfig(t, home, cliconfig.Config{URL: e.env.URL, Token: e.secret})
}

// HAR-04, ONB-09: --uninstall removes the harness entries setup added and the config file
// (after revoking its token on the server).
func TestSetupUninstall(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	calls := &harnessCalls{}
	r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}, lookPath: installed("claude"), run: calls.run},
		"--url", e.env.URL, "--token", e.secret, "--yes", "--harness", "claude")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)

	// Declining (end of input) keeps everything.
	claudeKnows := func() {
		require.NoError(t, os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"partyline":{"command":"`+testBinary+`","args":["channel"]}}}`), 0o600))
	}
	claudeKnows()
	removes := &harnessCalls{}
	r = runSetup(t, setupOpts{env: map[string]string{"HOME": home}, lookPath: installed("claude"), run: removes.run}, "--uninstall")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "Claude Code:\n  run: claude mcp remove --scope user partyline\n")
	require.Contains(t, r.stdout, "Kept Claude Code.")
	require.Empty(t, removes.calls())
	require.FileExists(t, configPath(home))

	r = runSetup(t, setupOpts{env: map[string]string{"HOME": home}, lookPath: installed("claude"), run: removes.run}, "--uninstall", "--yes")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, removes.calls(), "claude mcp remove --scope user partyline")
	_, err := os.Stat(configPath(home))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Contains(t, r.stdout, "Revoked the agent token on "+e.env.URL+".\n", "ONB-09")
	e.env.Anon().WithBearer(e.secret).AsAgent("x").Get("/api/v1/agent/rooms").RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	// Nothing left (the fake CLI removed nothing, so the entry goes by hand): uninstall says so.
	require.NoError(t, os.Remove(filepath.Join(home, ".claude.json")))
	r = runSetup(t, setupOpts{env: map[string]string{"HOME": home}}, "--uninstall", "--yes")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "Nothing to remove")
}

// ONB-09: a revoke that fails (server unreachable) is reported, and uninstall still deletes the
// config file and succeeds.
func TestSetupUninstallReportsFailedRevoke(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, cliconfig.Config{URL: "http://127.0.0.1:1", Token: "pl_agent_gone"})
	r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}}, "--uninstall", "--yes")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "Could not revoke the agent token on http://127.0.0.1:1: cannot reach http://127.0.0.1:1")
	require.Contains(t, r.stdout, "Revoke it in the web app (Tokens).")
	require.NotContains(t, r.stdout+r.stderr, "pl_agent_gone")
	_, err := os.Stat(configPath(home))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// HAR-05: setup deletes the backups it created once every harness was registered; with
// --keep-backups it keeps them (and names them); after a failed registration they stay. A
// backup that existed before the run is not setup's to delete.
func TestSetupRemovesItsBackups(t *testing.T) {
	e := newChannelEnv(t)
	const orig = "{\"mcpServers\": {\"fs\": {\"command\": \"npx\"}}}\n"
	piConfig := func(home string) string { return filepath.Join(home, ".pi", "agent", "mcp.json") }
	newHome := func(t *testing.T) string {
		home := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Dir(piConfig(home)), 0o755))
		require.NoError(t, os.WriteFile(piConfig(home), []byte(orig), 0o644))
		return home
	}

	t.Run("removed after success", func(t *testing.T) {
		home := newHome(t)
		r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}}, "--url", e.env.URL, "--token", e.secret, "--yes", "--all-detected")
		require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
		require.Contains(t, r.stdout, "✓ pi: registered")
		_, err := os.Lstat(piConfig(home) + ".partyline.bak")
		require.ErrorIs(t, err, os.ErrNotExist)
		require.NotContains(t, r.stdout, "backup")
	})

	t.Run("kept with --keep-backups", func(t *testing.T) {
		home := newHome(t)
		r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}}, "--url", e.env.URL, "--token", e.secret, "--yes", "--keep-backups", "--all-detected")
		require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
		bak := piConfig(home) + ".partyline.bak"
		data, err := os.ReadFile(bak)
		require.NoError(t, err)
		require.Equal(t, orig, string(data))
		fi, err := os.Stat(bak)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
		require.Contains(t, r.stdout, "\nKept the backup "+bak+"\n")
	})

	t.Run("kept after a failure", func(t *testing.T) {
		home := newHome(t)
		calls := &harnessCalls{answer: func(string) ([]byte, error) { return []byte("boom"), errors.New("exit status 1") }}
		r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}, lookPath: installed("claude"), run: calls.run},
			"--url", e.env.URL, "--token", e.secret, "--yes", "--all-detected")
		require.Equal(t, 1, r.code)
		data, err := os.ReadFile(piConfig(home) + ".partyline.bak")
		require.NoError(t, err)
		require.Equal(t, orig, string(data))
	})

	t.Run("an earlier backup stays", func(t *testing.T) {
		home := newHome(t)
		bak := piConfig(home) + ".partyline.bak"
		require.NoError(t, os.WriteFile(bak, []byte("earlier\n"), 0o600))
		r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}}, "--url", e.env.URL, "--token", e.secret, "--yes", "--all-detected")
		require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
		data, err := os.ReadFile(bak)
		require.NoError(t, err)
		require.Equal(t, "earlier\n", string(data))
	})
}

// writeDroppedHarnessLeftovers writes what older versions of Partyline put in the config of Codex, Gemini and
// Antigravity (their hooks and tool rules; no harness CLI is installed, so no MCP server), next to entries of
// the user's own.
func writeDroppedHarnessLeftovers(t *testing.T, home string) (codexHooks, geminiSettings, agyHooks, agySettings string) {
	t.Helper()
	write := func(rel, data string) string {
		path := filepath.Join(home, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
		return path
	}
	codexHooks = write(".codex/hooks.json", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"mine.sh"}]},{"hooks":[{"type":"command","command":"`+testBinary+` hook codex-stop","timeout":30}]}]}}`)
	geminiSettings = write(".gemini/settings.json", `{"theme":"x","hooks":{"AfterAgent":[{"matcher":"*","hooks":[{"command":"`+testBinary+` hook gemini-after-agent"}]}]}}`)
	agyHooks = write(".gemini/config/hooks.json", `{"partyline":{"Stop":[{"command":"mine.sh"},{"command":"`+testBinary+` hook antigravity-stop","timeout":30}]}}`)
	agySettings = write(".gemini/antigravity-cli/settings.json", `{"permissions":{"allow":["command(git status)","mcp(partyline/reply)","mcp(partyline/wait_for_messages)"]}}`)
	return
}

// AD-035: setup takes back what older versions registered in Codex, Gemini and Antigravity, which are no longer
// supported: one line per harness, the user's own entries stay, and a second run says nothing.
func TestSetupRemovesTheOldSetupOfDroppedHarnesses(t *testing.T) {
	for _, args := range [][]string{{"--harness", "none", "--yes"}, {"--harness", "claude", "--yes"}, {"--all-detected", "--yes"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, opts := loggedInSetup(t)
			home := opts.env["HOME"]
			opts.lookPath, opts.run = installed("claude"), (&harnessCalls{}).run
			codexHooks, geminiSettings, agyHooks, agySettings := writeDroppedHarnessLeftovers(t, home)

			r := runSetup(t, opts, args...)
			require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
			for _, name := range []string{"Codex", "Gemini", "Antigravity"} {
				require.Contains(t, r.stdout, "Removed the old Partyline setup for "+name+" (no longer supported)\n")
			}
			require.Equal(t, 3, strings.Count(r.stdout, "Removed the old Partyline setup for"), "one line per harness")
			require.JSONEq(t, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"mine.sh"}]}]}}`, readTestFile(t, codexHooks))
			require.JSONEq(t, `{"theme":"x"}`, readTestFile(t, geminiSettings))
			require.JSONEq(t, `{"partyline":{"Stop":[{"command":"mine.sh"}]}}`, readTestFile(t, agyHooks))
			require.JSONEq(t, `{"permissions":{"allow":["command(git status)"]}}`, readTestFile(t, agySettings))

			again := runSetup(t, opts, args...)
			require.Equal(t, 0, again.code, "stderr: %s", again.stderr)
			require.NotContains(t, again.stdout, "Removed the old Partyline setup", "idempotent")
			require.Empty(t, again.stderr)
		})
	}
}

// With nothing left from those harnesses setup prints nothing about them.
func TestSetupSaysNothingWithoutOldSetup(t *testing.T) {
	_, opts := loggedInSetup(t)
	opts.lookPath, opts.run = installed("claude"), (&harnessCalls{}).run
	r := runSetup(t, opts, "--harness", "claude", "--yes")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.NotContains(t, r.stdout, "no longer supported")
	require.NotContains(t, r.stderr, "no longer supported")
}

// A harness CLI that is installed is asked to remove its MCP server: agy when the entry is in its config, codex
// when `codex mcp get` finds it. A broken file is a warning on stderr, never a failure of setup.
func TestSetupOldSetupCleanupUsesTheHarnessCLIAndWarns(t *testing.T) {
	_, opts := loggedInSetup(t)
	home := opts.env["HOME"]
	agyMCP := filepath.Join(home, ".gemini", "config", "mcp_config.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(agyMCP), 0o755))
	require.NoError(t, os.WriteFile(agyMCP, []byte(`{"mcpServers":{"partyline":{"command":"`+testBinary+`","args":["channel","--tools-only","--harness","antigravity"]}}}`), 0o644))
	calls := &harnessCalls{answer: func(line string) ([]byte, error) {
		if line == "codex mcp get partyline --json" {
			return []byte(`{"transport":{"command":"` + testBinary + `","args":["channel","--tools-only","--harness","codex"]}}`), nil
		}
		return nil, nil
	}}
	opts.lookPath, opts.run = installed("codex", "agy"), calls.run
	broken := filepath.Join(home, ".gemini", "settings.json")
	require.NoError(t, os.WriteFile(broken, []byte("{,}"), 0o644))

	r := runSetup(t, opts, "--harness", "none", "--yes")
	require.Equal(t, 0, r.code, "a cleanup problem does not fail setup: %s", r.stderr)
	require.Contains(t, calls.calls(), "codex mcp remove partyline")
	require.Contains(t, calls.calls(), "agy mcp remove partyline")
	require.Contains(t, r.stdout, "Removed the old Partyline setup for Codex (no longer supported)\n")
	require.Contains(t, r.stdout, "Removed the old Partyline setup for Antigravity (no longer supported)\n")
	require.Contains(t, r.stderr, "could not remove the old Partyline setup for Gemini (no longer supported)")
	require.Contains(t, r.stderr, broken)
	require.Equal(t, "{,}", readTestFile(t, broken), "a file that is not valid JSON is not touched")
}

// uninstall takes the old setup of those harnesses back too, without asking, and does not report "nothing to
// remove" when that was all there was.
func TestSetupUninstallRemovesTheOldSetupOfDroppedHarnesses(t *testing.T) {
	home := t.TempDir()
	opts := setupOpts{env: map[string]string{"HOME": home}}
	codexHooks, geminiSettings, agyHooks, agySettings := writeDroppedHarnessLeftovers(t, home)
	r := runSetup(t, opts, "--uninstall", "--yes")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "Removed the old Partyline setup for Codex (no longer supported)\n")
	require.Contains(t, r.stdout, "Removed the old Partyline setup for Gemini (no longer supported)\n")
	require.Contains(t, r.stdout, "Removed the old Partyline setup for Antigravity (no longer supported)\n")
	require.NotContains(t, r.stdout, "Nothing to remove")
	require.NotContains(t, readTestFile(t, codexHooks), "codex-stop")
	require.NotContains(t, readTestFile(t, geminiSettings), "gemini-after-agent")
	require.NotContains(t, readTestFile(t, agyHooks), "antigravity-stop")
	require.NotContains(t, readTestFile(t, agySettings), "mcp(partyline/")
	require.Contains(t, readTestFile(t, codexHooks), "mine.sh")
}

// The dropped harnesses are no longer names setup takes: --harness or --remove with one is a usage error that
// says what happened, before anything is written.
func TestSetupRefusesDroppedHarnessNames(t *testing.T) {
	for _, flag := range []string{"--harness", "--remove"} {
		for _, name := range []string{"codex", "gemini", "antigravity", "agy"} {
			home := t.TempDir()
			r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}}, "--url", "http://127.0.0.1:1", "--token", "x", flag, "claude,"+name)
			require.Equal(t, 2, r.code, flag+" "+name)
			require.Contains(t, r.stderr, "is no longer supported by Partyline")
			require.NotContains(t, r.stderr, "unknown harness")
			_, err := os.Stat(configPath(home))
			require.ErrorIs(t, err, os.ErrNotExist)
		}
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
