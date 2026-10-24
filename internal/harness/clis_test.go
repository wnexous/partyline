package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const testBinary = "/opt/bin/partyline"

// fakeCLI records every harness CLI call as one line ("codex mcp add …") and answers through
// handle (nil: success with no output).
type fakeCLI struct {
	calls  []string
	handle func(line string) ([]byte, error)
}

func (f *fakeCLI) run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if f.handle != nil {
		return f.handle(line)
	}
	return nil, nil
}

func testEnv(t *testing.T, vars map[string]string) (Env, *fakeCLI) {
	t.Helper()
	f := &fakeCLI{}
	home := t.TempDir()
	return Env{
		Home: home, Binary: testBinary, Version: "v1.2.3",
		Getenv:   func(k string) string { return vars[k] },
		Run:      f.run,
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
	}, f
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func requireMissing(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	require.True(t, errors.Is(err, os.ErrNotExist), "%s should not exist: %v", path, err)
}

// jsonAt decodes the value at keys of the JSON file at path.
func jsonAt(t *testing.T, path string, keys ...string) any {
	t.Helper()
	var v any
	require.NoError(t, json.Unmarshal([]byte(readFile(t, path)), &v))
	for _, k := range keys {
		m, ok := v.(map[string]any)
		require.True(t, ok, "%s: %v is not an object", path, keys)
		v = m[k]
	}
	return v
}

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	require.NoError(t, json.Unmarshal([]byte(s), &v))
	return v
}

func registrar(t *testing.T, env Env, id string) Registrar {
	t.Helper()
	for _, r := range All(env) {
		if r.ID() == id {
			return r
		}
	}
	require.FailNow(t, "no registrar "+id)
	return nil
}

// HAR-01 (Claude Code): `claude mcp add --scope user` with the binary's absolute path.
func TestClaudeRegisters(t *testing.T) {
	env, cli := testEnv(t, nil)
	r := registrar(t, env, "claude")
	require.Equal(t, "Claude Code", r.Title())
	add := "claude mcp add --scope user partyline -- /opt/bin/partyline channel"
	require.Equal(t, []string{"run: " + add}, r.Plan())
	require.NoError(t, r.Apply(context.Background()))
	require.Equal(t, []string{add}, cli.calls)
}

// HAR-02 (Claude Code): an existing user-scope entry is replaced (claude mcp add refuses an
// existing name, so it is removed first); Entries reports the command it runs.
func TestClaudeUpdatesAndRemoves(t *testing.T) {
	env, cli := testEnv(t, nil)
	writeFile(t, filepath.Join(env.Home, ".claude.json"), `{"mcpServers":{"partyline":{"type":"stdio","command":"/old/partyline","args":["channel"]}}}`)
	r := registrar(t, env, "claude")
	remove := "claude mcp remove --scope user partyline"
	add := "claude mcp add --scope user partyline -- /opt/bin/partyline channel"
	require.Equal(t, []string{"run: " + remove, "run: " + add}, r.Plan())
	require.NoError(t, r.Apply(context.Background()))
	require.Equal(t, []string{remove, add}, cli.calls)

	entries, err := r.Entries(context.Background())
	require.NoError(t, err)
	require.Equal(t, []Entry{{What: "MCP server in ~/.claude.json", Binary: "/old/partyline"}}, entries)

	// HAR-04: uninstall removes the entry; with no entry it runs nothing.
	cli.calls = nil
	require.Equal(t, []string{"run: " + remove}, r.RemovePlan(context.Background()))
	require.NoError(t, r.Remove(context.Background()))
	require.Equal(t, []string{remove}, cli.calls)

	env2, cli2 := testEnv(t, nil)
	r2 := registrar(t, env2, "claude")
	require.Empty(t, r2.RemovePlan(context.Background()))
	require.NoError(t, r2.Remove(context.Background()))
	require.Empty(t, cli2.calls)
}

// setup --always: Claude Code's MCP server, Codex's server and Stop hook and Gemini's server
// and AfterAgent hook carry --always, so a plain session runs them; applying again without it
// replaces the same hook group instead of adding a second one.
func TestAlwaysFlagsCLIRegistrations(t *testing.T) {
	env, cli := testEnv(t, nil)
	env.Always = true
	claude := registrar(t, env, "claude")
	require.Equal(t, []string{"run: claude mcp add --scope user partyline -- /opt/bin/partyline channel --always"}, claude.Plan())
	require.NoError(t, claude.Apply(context.Background()))
	require.Equal(t, []string{"claude mcp add --scope user partyline -- /opt/bin/partyline channel --always"}, cli.calls)

	hooks := filepath.Join(env.Home, ".codex", "hooks.json")
	writeFile(t, hooks, codexHooksOrig)
	codex := registrar(t, env, "codex")
	require.NoError(t, codex.Apply(context.Background()))
	stop := jsonAt(t, hooks, "hooks", "Stop").([]any)
	require.Len(t, stop, 2)
	command := jsonAt(t, hooks, "hooks", "Stop").([]any)[1].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"]
	require.Equal(t, "/opt/bin/partyline hook codex-stop --always", command)

	// Without --always the same group is updated in place and the server loses the flag.
	env.Always = false
	require.NoError(t, registrar(t, env, "codex").Apply(context.Background()))
	stop = jsonAt(t, hooks, "hooks", "Stop").([]any)
	require.Len(t, stop, 2, "the --always group was replaced, not duplicated")
	command = stop[1].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"]
	require.Equal(t, "/opt/bin/partyline hook codex-stop", command)

	env.Always = true
	gemini := registrar(t, env, "gemini")
	require.Contains(t, strings.Join(gemini.Plan(), "\n"), "/opt/bin/partyline channel --tools-only --harness gemini --always")
	require.Contains(t, strings.Join(gemini.Plan(), "\n"), "/opt/bin/partyline hook gemini-after-agent --always")
}

// HAR-01: $CLAUDE_CONFIG_DIR moves Claude Code's user config.
func TestClaudeConfigDir(t *testing.T) {
	dir := t.TempDir()
	env, _ := testEnv(t, map[string]string{"CLAUDE_CONFIG_DIR": dir})
	writeFile(t, filepath.Join(dir, ".claude.json"), `{"mcpServers":{"partyline":{"command":"/x/partyline"}}}`)
	entries, err := registrar(t, env, "claude").Entries(context.Background())
	require.NoError(t, err)
	require.Equal(t, "/x/partyline", entries[0].Binary)
}

const codexHooksOrig = `{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {"type": "command", "command": "orca-hook start", "timeout": 10}
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {"type": "command", "command": "orca-hook stop", "timeout": 10}
        ]
      }
    ]
  }
}
`

const codexGroup = `{"hooks":[{"type":"command","command":"/opt/bin/partyline hook codex-stop","timeout":30}]}`

// HAR-01, HAR-02 (Codex CLI): `codex mcp add partyline -- <abs> channel --tools-only --harness
// codex` (the harness named for ID-01, T18) and a
// Stop hook appended to ~/.codex/hooks.json next to the existing hooks; applying again
// updates in place.
func TestCodexRegistersToolsAndStopHook(t *testing.T) {
	env, cli := testEnv(t, nil)
	hooks := filepath.Join(env.Home, ".codex", "hooks.json")
	writeFile(t, hooks, codexHooksOrig)
	r := registrar(t, env, "codex")
	require.Equal(t, "Codex CLI", r.Title())
	add := "codex mcp add partyline -- /opt/bin/partyline channel --tools-only --harness codex"
	require.Equal(t, []string{"run: " + add, "edit ~/.codex/hooks.json: add to hooks.Stop " + codexGroup}, r.Plan())

	require.NoError(t, r.Apply(context.Background()))
	require.Equal(t, []string{add}, cli.calls)
	stop := jsonAt(t, hooks, "hooks", "Stop").([]any)
	require.Len(t, stop, 2)
	require.Equal(t, decode(t, `{"hooks":[{"type":"command","command":"orca-hook stop","timeout":10}]}`), stop[0])
	require.Equal(t, decode(t, codexGroup), stop[1])
	stopEnd := "\"orca-hook stop\", \"timeout\": 10}\n        ]\n      }"
	require.Equal(t, strings.Replace(codexHooksOrig, stopEnd, stopEnd+",\n      {\n        \"hooks\": [\n          {\n"+
		"            \"type\": \"command\",\n            \"command\": \"/opt/bin/partyline hook codex-stop\",\n            \"timeout\": 30\n"+
		"          }\n        ]\n      }", 1), readFile(t, hooks), "HAR-02: every other byte unchanged")
	require.Equal(t, codexHooksOrig, readFile(t, hooks+".partyline.bak"), "backup of the previous file")

	// Again with the same binary: the group is updated in place, not duplicated.
	after := readFile(t, hooks)
	require.NoError(t, registrar(t, env, "codex").Apply(context.Background()))
	require.Equal(t, after, readFile(t, hooks))
}

// HAR-02, HAR-04: a hook group is Partyline's only when its command is exactly the one setup
// writes for this binary. Groups of other programs named partyline* (a wrapper, or a
// partyline binary at another path) are kept byte for byte by Apply and Remove, and Entries
// does not list them.
func TestHookGroupsOwnedOnlyWithExactCommand(t *testing.T) {
	foreign := `{
  "hooks": {
    "Stop": [
      {"hooks": [{"type": "command", "command": "/usr/local/bin/partyline-wrapper hook codex-stop"}]},
      {"hooks": [{"type": "command", "command": "/other/partyline hook codex-stop"}]},
      {"hooks": [{"type": "command", "command": "/opt/bin/partyline hook codex-stop --extra"}]}
    ]
  }
}
`
	env, cli := testEnv(t, nil)
	hooks := filepath.Join(env.Home, ".codex", "hooks.json")
	writeFile(t, hooks, foreign)
	cli.handle = func(line string) ([]byte, error) {
		if line == "codex mcp get partyline --json" {
			return []byte("Error: No MCP server named 'partyline' found."), errors.New("exit status 1")
		}
		return nil, nil
	}
	r := registrar(t, env, "codex")
	entries, err := r.Entries(context.Background())
	require.NoError(t, err)
	require.Empty(t, entries, "no hook of another binary is listed")
	require.Empty(t, r.RemovePlan(context.Background()))

	require.NoError(t, r.Apply(context.Background()))
	stop := jsonAt(t, hooks, "hooks", "Stop").([]any)
	require.Len(t, stop, 4, "the three foreign groups stay, ours is appended")
	require.Equal(t, decode(t, codexGroup), stop[3])
	entries, err = r.Entries(context.Background())
	require.NoError(t, err)
	require.Equal(t, []Entry{{What: "Stop hook in ~/.codex/hooks.json", Binary: testBinary}}, entries)

	require.NoError(t, r.Remove(context.Background()))
	require.Equal(t, foreign, readFile(t, hooks), "uninstall removed only our group")
}

// HAR-04 (Codex CLI): uninstall removes the MCP server and only the Partyline hook; the
// hooks file is back to its original bytes and the backup is gone.
func TestCodexRemoveRestoresOriginal(t *testing.T) {
	env, cli := testEnv(t, nil)
	hooks := filepath.Join(env.Home, ".codex", "hooks.json")
	writeFile(t, hooks, codexHooksOrig)
	r := registrar(t, env, "codex")
	require.NoError(t, r.Apply(context.Background()))

	cli.calls = nil
	cli.handle = func(line string) ([]byte, error) {
		if line == "codex mcp get partyline --json" {
			return []byte("WARNING: proceeding\n{\"name\":\"partyline\",\"transport\":{\"type\":\"stdio\",\"command\":\"/opt/bin/partyline\",\"args\":[\"channel\",\"--tools-only\"]}}"), nil
		}
		return nil, nil
	}
	entries, err := r.Entries(context.Background())
	require.NoError(t, err)
	require.Equal(t, []Entry{
		{What: "MCP server in ~/.codex/config.toml", Binary: testBinary},
		{What: "Stop hook in ~/.codex/hooks.json", Binary: testBinary},
	}, entries)
	require.Equal(t, []string{"run: codex mcp remove partyline", "edit ~/.codex/hooks.json: remove the Partyline Stop hook"}, r.RemovePlan(context.Background()))

	cli.calls = nil
	require.NoError(t, r.Remove(context.Background()))
	require.Equal(t, []string{"codex mcp get partyline --json", "codex mcp remove partyline"}, cli.calls)
	require.Equal(t, codexHooksOrig, readFile(t, hooks))
	requireMissing(t, hooks+".partyline.bak")
}

// HAR-04: a hooks file setup created is deleted again; a server Codex does not know is not
// removed.
func TestCodexRemoveDeletesCreatedHooksFile(t *testing.T) {
	env, cli := testEnv(t, map[string]string{"CODEX_HOME": ""})
	r := registrar(t, env, "codex")
	require.NoError(t, r.Apply(context.Background()))
	hooks := filepath.Join(env.Home, ".codex", "hooks.json")
	require.Equal(t, decode(t, `{"hooks":{"Stop":[`+codexGroup+`]}}`), jsonAt(t, hooks))

	cli.calls = nil
	cli.handle = func(string) ([]byte, error) {
		return []byte("Error: No MCP server named 'partyline' found."), errors.New("exit status 1")
	}
	require.NoError(t, r.Remove(context.Background()))
	require.Equal(t, []string{"codex mcp get partyline --json"}, cli.calls)
	requireMissing(t, hooks)
}

// HAR-01: $CODEX_HOME moves the Codex hooks file.
func TestCodexHome(t *testing.T) {
	dir := t.TempDir()
	env, _ := testEnv(t, map[string]string{"CODEX_HOME": dir})
	require.NoError(t, registrar(t, env, "codex").Apply(context.Background()))
	require.Equal(t, decode(t, codexGroup), jsonAt(t, filepath.Join(dir, "hooks.json"), "hooks", "Stop").([]any)[0])
}

// A binary path with spaces is quoted in hook commands and read back by Entries.
func TestHookCommandQuotesBinary(t *testing.T) {
	env, _ := testEnv(t, nil)
	env.Binary = "/opt/my tools/partyline"
	require.NoError(t, registrar(t, env, "codex").Apply(context.Background()))
	hooks := filepath.Join(env.Home, ".codex", "hooks.json")
	group := jsonAt(t, hooks, "hooks", "Stop").([]any)[0].(map[string]any)
	require.Equal(t, "'/opt/my tools/partyline' hook codex-stop", group["hooks"].([]any)[0].(map[string]any)["command"])
	bins, err := env.hookBinaries(hooks, CodexStopHook, codexHookKeys...)
	require.NoError(t, err)
	require.Equal(t, []string{"/opt/my tools/partyline"}, bins)
}

const geminiOrig = "{\n  \"general\": {\"vimMode\": true},\n  \"mcpServers\": {\n    \"other\": {\"command\": \"o\"}\n  }\n}\n"

const geminiGroup = `{"matcher":"*","hooks":[{"name":"partyline","type":"command","command":"/opt/bin/partyline hook gemini-after-agent","timeout":30000}]}`

// geminiCLI emulates `gemini mcp add/remove -s user` on settings.json.
func geminiCLI(t *testing.T, settings string) func(string) ([]byte, error) {
	return func(line string) ([]byte, error) {
		d, _, err := readDoc(settings)
		require.NoError(t, err)
		switch {
		case strings.HasPrefix(line, "gemini mcp add "):
			require.NoError(t, d.set(server{Command: testBinary, Args: []string{"channel", "--tools-only"}}, "mcpServers", "partyline"))
		case strings.HasPrefix(line, "gemini mcp remove "):
			_, err := d.del("mcpServers", "partyline")
			require.NoError(t, err)
		}
		require.NoError(t, os.WriteFile(settings, d.data, 0o644))
		return nil, nil
	}
}

// HAR-01, HAR-02, HAR-04 (Gemini CLI): `gemini mcp add -s user partyline <abs> channel
// --tools-only --harness gemini` (ID-01, T18) and an AfterAgent hook in ~/.gemini/settings.json; uninstall gives back the
// original file.
func TestGeminiRegistersAndRemoves(t *testing.T) {
	env, cli := testEnv(t, nil)
	settings := filepath.Join(env.Home, ".gemini", "settings.json")
	writeFile(t, settings, geminiOrig)
	cli.handle = geminiCLI(t, settings)
	r := registrar(t, env, "gemini")
	require.Equal(t, "Gemini CLI", r.Title())
	add := "gemini mcp add -s user partyline /opt/bin/partyline channel --tools-only --harness gemini"
	require.Equal(t, []string{"run: " + add, "edit ~/.gemini/settings.json: add to hooks.AfterAgent " + geminiGroup}, r.Plan())

	require.NoError(t, r.Apply(context.Background()))
	require.Equal(t, []string{add}, cli.calls)
	require.Equal(t, decode(t, `[`+geminiGroup+`]`), jsonAt(t, settings, "hooks", "AfterAgent"))
	require.Equal(t, map[string]any{"vimMode": true}, jsonAt(t, settings, "general"))
	entries, err := r.Entries(context.Background())
	require.NoError(t, err)
	require.Equal(t, []Entry{
		{What: "MCP server in ~/.gemini/settings.json", Binary: testBinary},
		{What: "AfterAgent hook in ~/.gemini/settings.json", Binary: testBinary},
	}, entries)

	require.NoError(t, r.Apply(context.Background()))
	require.Len(t, jsonAt(t, settings, "hooks", "AfterAgent"), 1, "applying again does not duplicate the hook")

	cli.calls = nil
	require.NoError(t, r.Remove(context.Background()))
	require.Equal(t, []string{"gemini mcp remove -s user partyline"}, cli.calls)
	require.Equal(t, geminiOrig, readFile(t, settings))
	requireMissing(t, settings+".partyline.bak")
}

// Edge case (HAR-02): a hooks or settings file that is not valid JSON is not touched, no CLI
// runs, and the parse error names the file.
func TestInvalidHarnessFileIsNotTouched(t *testing.T) {
	for id, rel := range map[string]string{"codex": ".codex/hooks.json", "gemini": ".gemini/settings.json"} {
		t.Run(id, func(t *testing.T) {
			env, cli := testEnv(t, nil)
			path := filepath.Join(env.Home, rel)
			writeFile(t, path, "{\"hooks\": {,}}")
			err := registrar(t, env, id).Apply(context.Background())
			var pe *ParseError
			require.True(t, errors.As(err, &pe), "%v", err)
			require.Contains(t, err.Error(), path)
			require.Empty(t, cli.calls)
			require.Equal(t, "{\"hooks\": {,}}", readFile(t, path))
		})
	}
}

// invalidCodexTOML is a config.toml with an unclosed table header.
const invalidCodexTOML = "model = \"gpt-5\"\n[mcp_servers.other\ncommand = \"x\"\n"

// codexTOMLError is what codex-cli 0.163.0 prints for invalidCodexTOML (captured on the devbox),
// with the config path in place of its first line's path.
func codexTOMLError(path string) string {
	return "Error: failed to load bootstrap configuration\n\nCaused by:\n    0: " + path +
		":2:19: unclosed table, expected `]`\n    1: TOML parse error at line 2, column 19\n" +
		"         |\n       2 | [mcp_servers.other\n         |                   ^\n       unclosed table, expected `]`\n"
}

// Edge case (HAR-02): Codex's config.toml is managed by `codex mcp add`. When it is not valid
// TOML, codex refuses it; setup reports codex's parse error (naming the file), leaves
// config.toml as it was and does not add the Stop hook.
func TestCodexInvalidTOMLIsNotTouched(t *testing.T) {
	env, cli := testEnv(t, nil)
	cfg := filepath.Join(env.Home, ".codex", "config.toml")
	writeFile(t, cfg, invalidCodexTOML)
	cli.handle = func(line string) ([]byte, error) {
		if strings.HasPrefix(line, "codex mcp add ") {
			return []byte(codexTOMLError(cfg)), errors.New("exit status 1")
		}
		return nil, nil
	}
	err := registrar(t, env, "codex").Apply(context.Background())
	require.ErrorContains(t, err, cfg+":2:19: unclosed table")
	require.ErrorContains(t, err, "TOML parse error at line 2, column 19")
	require.Equal(t, []string{"codex mcp add partyline -- /opt/bin/partyline channel --tools-only --harness codex"}, cli.calls)
	require.Equal(t, invalidCodexTOML, readFile(t, cfg))
	requireMissing(t, filepath.Join(env.Home, ".codex", "hooks.json"))
}

// The same with the real Codex CLI when it is installed: its own TOML parser refuses the file.
func TestCodexInvalidTOMLRealCLI(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not installed")
	}
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome) // never the user's real Codex config
	env := Env{Home: home, Binary: testBinary, Version: "v1.2.3", Getenv: os.Getenv}
	cfg := filepath.Join(codexHome, "config.toml")
	writeFile(t, cfg, invalidCodexTOML)

	err := registrar(t, env, "codex").Apply(context.Background())
	require.ErrorContains(t, err, "TOML parse error at line 2")
	require.ErrorContains(t, err, cfg)
	require.Equal(t, invalidCodexTOML, readFile(t, cfg))
	requireMissing(t, filepath.Join(codexHome, "hooks.json"))
}

// A failing harness CLI is reported with its output.
func TestCLIFailureCarriesOutput(t *testing.T) {
	env, cli := testEnv(t, nil)
	cli.handle = func(string) ([]byte, error) {
		return []byte("MCP server partyline already exists"), errors.New("exit status 1")
	}
	err := registrar(t, env, "claude").Apply(context.Background())
	require.ErrorContains(t, err, "MCP server partyline already exists")
}

// HAR-04, ID-01 (T18): entries registered before setup named the harness
// (`channel --tools-only`, no --harness) are still Partyline's: Entries lists them, setup
// replaces them with the new command line and uninstall removes them.
func TestToolsOnlyEntriesWithoutHarnessFlagStayOwned(t *testing.T) {
	t.Run("codex", func(t *testing.T) {
		env, cli := testEnv(t, nil)
		cli.handle = func(line string) ([]byte, error) {
			if line == "codex mcp get partyline --json" {
				return []byte(`{"name":"partyline","transport":{"type":"stdio","command":"/opt/bin/partyline","args":["channel","--tools-only"]}}`), nil
			}
			return nil, nil
		}
		r := registrar(t, env, "codex")
		entries, err := r.Entries(context.Background())
		require.NoError(t, err)
		require.Equal(t, []Entry{{What: "MCP server in ~/.codex/config.toml", Binary: testBinary}}, entries)
		require.Equal(t, []string{"run: codex mcp remove partyline"}, r.RemovePlan(context.Background()))
		cli.calls = nil
		require.NoError(t, r.Apply(context.Background()))
		require.Equal(t, []string{"codex mcp add partyline -- /opt/bin/partyline channel --tools-only --harness codex"}, cli.calls,
			"codex mcp add replaces the old entry")
		cli.calls = nil
		require.NoError(t, r.Remove(context.Background()))
		require.Equal(t, []string{"codex mcp get partyline --json", "codex mcp remove partyline"}, cli.calls)
	})
	t.Run("gemini", func(t *testing.T) {
		env, cli := testEnv(t, nil)
		settings := filepath.Join(env.Home, ".gemini", "settings.json")
		writeFile(t, settings, `{"mcpServers":{"partyline":{"command":"/opt/bin/partyline","args":["channel","--tools-only"]}}}`)
		r := registrar(t, env, "gemini")
		entries, err := r.Entries(context.Background())
		require.NoError(t, err)
		require.Equal(t, []Entry{{What: "MCP server in ~/.gemini/settings.json", Binary: testBinary}}, entries)
		require.NoError(t, r.Remove(context.Background()))
		require.Equal(t, []string{"gemini mcp remove -s user partyline"}, cli.calls)
	})
}
