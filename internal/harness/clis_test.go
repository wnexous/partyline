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

const agyHooksOrig = "{\n  \"lint\": {\n    \"PostToolUse\": [{\"matcher\": \"run_command\", \"hooks\": [{\"command\": \"./lint.sh\"}]}]\n  }\n}\n"

const agyMCPOrig = "{\n  \"mcpServers\": {\n    \"other\": {\"command\": \"o\", \"disabled\": false}\n  }\n}\n"

const agySettingsOrig = "{\n  \"model\": \"Gemini 3 Pro\",\n  \"trustedWorkspaces\": [\"/home/dev\"],\n  \"permissions\": {\n    \"allow\": [\"command(git status)\"]\n  }\n}\n"

// agyRules is every rule setup adds: one mcp(partyline/<tool>) per tool, never a wildcard.
var agyRules = []string{
	"mcp(partyline/reply)", "mcp(partyline/send_message)", "mcp(partyline/react)", "mcp(partyline/edit_message)",
	"mcp(partyline/ask)", "mcp(partyline/history)", "mcp(partyline/room_info)", "mcp(partyline/list_rooms)",
	"mcp(partyline/wait_for_messages)",
}

const agyHandler = `{"type":"command","command":"/opt/bin/partyline hook antigravity-stop","timeout":30}`

// agyCLI emulates `agy mcp add/remove` on the global mcp_config.json: add writes (or replaces)
// the server, remove deletes it and fails when it is missing, like the real CLI.
func agyCLI(t *testing.T, config string) func(string) ([]byte, error) {
	return func(line string) ([]byte, error) {
		d, _, err := readDoc(config)
		require.NoError(t, err)
		switch {
		case strings.HasPrefix(line, "agy mcp add "):
			require.NoError(t, d.set(server{Command: testBinary, Args: []string{"channel", "--tools-only", "--harness", "antigravity"}}, "mcpServers", "partyline"))
		case strings.HasPrefix(line, "agy mcp remove "):
			ok, err := d.del("mcpServers", "partyline")
			require.NoError(t, err)
			if !ok {
				return []byte(`Error: MCP server "partyline" not found`), errors.New("exit status 1")
			}
		}
		require.NoError(t, os.WriteFile(config, d.data, 0o644))
		return nil, nil
	}
}

// HAR-01, HAR-02, HAR-04 (Antigravity): `agy mcp add partyline <abs> channel --tools-only
// --harness antigravity` (global ~/.gemini/config/mcp_config.json) and a Stop hook under the hook
// name "partyline" in ~/.gemini/config/hooks.json, next to the hooks already there; applying again
// updates in place and uninstall gives back both original files.
func TestAntigravityRegistersAndRemoves(t *testing.T) {
	env, cli := testEnv(t, nil)
	mcp := filepath.Join(env.Home, ".gemini", "config", "mcp_config.json")
	hooks := filepath.Join(env.Home, ".gemini", "config", "hooks.json")
	writeFile(t, mcp, agyMCPOrig)
	writeFile(t, hooks, agyHooksOrig)
	settings := filepath.Join(env.Home, ".gemini", "antigravity-cli", "settings.json")
	writeFile(t, settings, agySettingsOrig)
	cli.handle = agyCLI(t, mcp)
	r := registrar(t, env, "antigravity")
	require.Equal(t, "Antigravity", r.Title())
	require.True(t, r.Detected())
	add := "agy mcp add partyline /opt/bin/partyline channel --tools-only --harness antigravity"
	require.Equal(t, []string{
		"run: " + add, "edit ~/.gemini/config/hooks.json: add to partyline.Stop " + agyHandler,
		"edit ~/.gemini/antigravity-cli/settings.json: add to permissions.allow " + strings.Join(agyRules, ", "),
	}, r.Plan())
	require.Empty(t, r.RemovePlan(context.Background()), "nothing registered yet")

	require.NoError(t, r.Apply(context.Background()))
	require.Equal(t, []string{add}, cli.calls)
	require.Equal(t, decode(t, `[`+agyHandler+`]`), jsonAt(t, hooks, "partyline", "Stop"))
	require.NotNil(t, jsonAt(t, hooks, "lint", "PostToolUse"), "the other hook is kept")
	require.NotNil(t, jsonAt(t, mcp, "mcpServers", "other"), "the other server is kept")
	require.FileExists(t, hooks+".partyline.bak")
	// The tool rules join the user's own rule; the other settings stay, and no rule is a wildcard.
	allow := jsonAt(t, settings, "permissions", "allow").([]any)
	require.Len(t, allow, 1+len(agyRules))
	require.Equal(t, "command(git status)", allow[0])
	for _, rule := range agyRules {
		require.Contains(t, allow, rule)
		require.NotContains(t, rule, "*")
		require.True(t, strings.HasPrefix(rule, "mcp(partyline/"), "only the partyline server: %s", rule)
	}
	require.Equal(t, "Gemini 3 Pro", jsonAt(t, settings, "model"))
	require.FileExists(t, settings+".partyline.bak")
	entries, err := r.Entries(context.Background())
	require.NoError(t, err)
	require.Equal(t, []Entry{
		{What: "MCP server in ~/.gemini/config/mcp_config.json", Binary: testBinary},
		{What: "Stop hook in ~/.gemini/config/hooks.json", Binary: testBinary},
	}, entries)

	require.NoError(t, r.Apply(context.Background()))
	require.Len(t, jsonAt(t, hooks, "partyline", "Stop"), 1, "applying again does not duplicate the hook")
	require.Len(t, jsonAt(t, settings, "permissions", "allow"), 1+len(agyRules), "nor the rules")

	require.Equal(t, []string{
		"run: agy mcp remove partyline", "edit ~/.gemini/config/hooks.json: remove the Partyline Stop hook",
		"edit ~/.gemini/antigravity-cli/settings.json: remove the Partyline tool rules from permissions.allow",
	}, r.RemovePlan(context.Background()))
	cli.calls = nil
	require.NoError(t, r.Remove(context.Background()))
	require.Equal(t, []string{"agy mcp remove partyline"}, cli.calls)
	require.Equal(t, agyMCPOrig, readFile(t, mcp))
	require.Equal(t, agyHooksOrig, readFile(t, hooks))
	requireMissing(t, hooks+".partyline.bak")
	require.JSONEq(t, agySettingsOrig, readFile(t, settings), "the user's own rule and settings are back as they were")
	requireMissing(t, settings+".partyline.bak")

	// Nothing registered: remove runs no CLI and touches nothing.
	cli.calls = nil
	require.NoError(t, r.Remove(context.Background()))
	require.Empty(t, cli.calls)
}

// HAR-04: with no hooks.json of the user's, setup creates one and uninstall deletes it again.
func TestAntigravityCreatesAndDeletesHooksFile(t *testing.T) {
	env, cli := testEnv(t, nil)
	mcp := filepath.Join(env.Home, ".gemini", "config", "mcp_config.json")
	hooks := filepath.Join(env.Home, ".gemini", "config", "hooks.json")
	cli.handle = func(_ string) ([]byte, error) {
		writeFile(t, mcp, `{"mcpServers":{"partyline":{"command":"/opt/bin/partyline","args":["channel","--tools-only","--harness","antigravity"],"disabled":false}}}`)
		return nil, nil
	}
	r := registrar(t, env, "antigravity")
	require.NoError(t, r.Apply(context.Background()))
	require.Equal(t, decode(t, `[`+agyHandler+`]`), jsonAt(t, hooks, "partyline", "Stop"))
	require.Len(t, jsonAt(t, filepath.Join(env.Home, ".gemini", "antigravity-cli", "settings.json"), "permissions", "allow"), len(agyRules))
	cli.handle = func(_ string) ([]byte, error) { return nil, os.Remove(mcp) }
	require.NoError(t, r.Remove(context.Background()))
	requireMissing(t, hooks)
	requireMissing(t, filepath.Join(env.Home, ".gemini", "antigravity-cli", "settings.json"))
}

// A hook of the user's under the name "partyline" that is not Partyline's stays when the Stop
// hook is added and removed; a registration of another binary path is replaced in place.
func TestAntigravityKeepsForeignStopHandlers(t *testing.T) {
	env, cli := testEnv(t, nil)
	mcp := filepath.Join(env.Home, ".gemini", "config", "mcp_config.json")
	hooks := filepath.Join(env.Home, ".gemini", "config", "hooks.json")
	const orig = `{"partyline":{"Stop":[{"command":"./mine.sh"},{"command":"/opt/bin/partylinex hook antigravity-stop"}]}}`
	writeFile(t, hooks, orig)
	cli.handle = agyCLI(t, mcp)
	r := registrar(t, env, "antigravity")
	require.NoError(t, r.Apply(context.Background()))
	require.Len(t, jsonAt(t, hooks, "partyline", "Stop"), 3)
	require.NoError(t, r.Remove(context.Background()))
	require.JSONEq(t, orig, readFile(t, hooks))
}

// setup --always writes the flag into the server and the hook command.
func TestAntigravityAlways(t *testing.T) {
	env, _ := testEnv(t, nil)
	env.Always = true
	plan := strings.Join(registrar(t, env, "antigravity").Plan(), "\n")
	require.Contains(t, plan, "/opt/bin/partyline channel --tools-only --harness antigravity --always")
	require.Contains(t, plan, "/opt/bin/partyline hook antigravity-stop --always")
}

// A settings.json that is not valid JSON is not touched and stops setup before agy runs.
func TestAntigravityInvalidSettingsIsNotTouched(t *testing.T) {
	env, cli := testEnv(t, nil)
	path := filepath.Join(env.Home, ".gemini", "antigravity-cli", "settings.json")
	writeFile(t, path, "{\"permissions\": {,}}")
	err := registrar(t, env, "antigravity").Apply(context.Background())
	var pe *ParseError
	require.True(t, errors.As(err, &pe), "%v", err)
	require.Contains(t, err.Error(), path)
	require.Empty(t, cli.calls)
	require.Equal(t, "{\"permissions\": {,}}", readFile(t, path))
}

// Rules the user wrote by hand stay when ours are removed, and an allow list with only ours goes away
// together with its parent.
func TestAntigravityRemoveKeepsTheUsersRules(t *testing.T) {
	env, cli := testEnv(t, nil)
	mcp := filepath.Join(env.Home, ".gemini", "config", "mcp_config.json")
	settings := filepath.Join(env.Home, ".gemini", "antigravity-cli", "settings.json")
	writeFile(t, settings, `{"permissions":{"allow":["mcp(other/tool)"],"deny":["command(rm)"]}}`)
	writeFile(t, mcp, "{}\n")
	cli.handle = agyCLI(t, mcp)
	r := registrar(t, env, "antigravity")
	require.NoError(t, r.Apply(context.Background()))
	require.NoError(t, r.Remove(context.Background()))
	require.JSONEq(t, `{"permissions":{"allow":["mcp(other/tool)"],"deny":["command(rm)"]}}`, readFile(t, settings))
}

// Edge case (HAR-02): a hooks or settings file that is not valid JSON is not touched, no CLI
// runs, and the parse error names the file.
func TestInvalidHarnessFileIsNotTouched(t *testing.T) {
	for id, rel := range map[string]string{"codex": ".codex/hooks.json", "gemini": ".gemini/settings.json", "antigravity": ".gemini/config/hooks.json"} {
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
	t.Run("antigravity", func(t *testing.T) {
		env, cli := testEnv(t, nil)
		config := filepath.Join(env.Home, ".gemini", "config", "mcp_config.json")
		writeFile(t, config, `{"mcpServers":{"partyline":{"command":"/opt/bin/partyline","args":["channel","--tools-only"],"disabled":false}}}`)
		r := registrar(t, env, "antigravity")
		entries, err := r.Entries(context.Background())
		require.NoError(t, err)
		require.Equal(t, []Entry{{What: "MCP server in ~/.gemini/config/mcp_config.json", Binary: testBinary}}, entries)
		require.NoError(t, r.Remove(context.Background()))
		require.Equal(t, []string{"agy mcp remove partyline"}, cli.calls)
	})
}
