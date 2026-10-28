package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// What older versions of Partyline wrote for the harnesses that are no longer supported (AD-035),
// next to entries of the user's own that must stay.

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

// codexHooksWithOurs is codexHooksOrig with our Stop group after the user's.
func codexHooksWithOurs() string {
	return strings.Replace(codexHooksOrig, `"command": "orca-hook stop", "timeout": 10}
        ]
      }
`, `"command": "orca-hook stop", "timeout": 10}
        ]
      },
      `+codexGroup+"\n", 1)
}

const codexServerJSON = `WARNING: proceeding
{"name":"partyline","transport":{"type":"stdio","command":"/opt/bin/partyline","args":["channel","--tools-only","--harness","codex"]}}`

// codexCLI emulates `codex mcp get/remove`: the server is known while *known is true.
func codexCLI(known *bool) func(string) ([]byte, error) {
	return func(line string) ([]byte, error) {
		switch {
		case line == "codex mcp get partyline --json" && *known:
			return []byte(codexServerJSON), nil
		case line == "codex mcp get partyline --json":
			return []byte("Error: No MCP server named 'partyline' found."), errors.New("exit status 1")
		case line == "codex mcp remove partyline":
			*known = false
		}
		return nil, nil
	}
}

func cleaned(t *testing.T, env Env) map[string]LegacyResult {
	t.Helper()
	out := map[string]LegacyResult{}
	for _, r := range env.CleanLegacy(context.Background()) {
		out[r.ID] = r
	}
	return out
}

func TestFindLegacy(t *testing.T) {
	for in, id := range map[string]string{"codex": "codex", "Gemini": "gemini", "antigravity": "antigravity", " agy ": "antigravity"} {
		l, ok := FindLegacy(in)
		require.True(t, ok, in)
		require.Equal(t, id, l.ID)
	}
	for _, in := range []string{"claude", "pi", "opencode", "omp", "", "agent"} {
		_, ok := FindLegacy(in)
		require.False(t, ok, in)
	}
}

// Nothing of the three on the machine: nothing is reported, run, or created.
func TestCleanLegacyAbsent(t *testing.T) {
	env, cli := testEnv(t, nil)
	known := false
	cli.handle = codexCLI(&known)
	require.Empty(t, env.CleanLegacy(context.Background()))
	require.Equal(t, []string{"codex mcp get partyline --json"}, cli.calls, "only a read")
	entries, err := os.ReadDir(env.Home)
	require.NoError(t, err)
	require.Empty(t, entries, "no file or directory created")
}

// Codex: the MCP server goes through `codex mcp remove` and only our Stop group leaves hooks.json,
// which is then back to the bytes it had; a second run finds nothing.
func TestCleanLegacyCodex(t *testing.T) {
	env, cli := testEnv(t, nil)
	hooks := filepath.Join(env.Home, ".codex", "hooks.json")
	writeFile(t, hooks, codexHooksWithOurs())
	writeFile(t, hooks+".partyline.bak", codexHooksOrig)
	known := true
	cli.handle = codexCLI(&known)

	got := cleaned(t, env)
	require.Len(t, got, 1)
	require.True(t, got["codex"].Removed)
	require.NoError(t, got["codex"].Err)
	require.Equal(t, "Codex", got["codex"].Title)
	require.Equal(t, []string{"codex mcp get partyline --json", "codex mcp remove partyline"}, cli.calls)
	require.Equal(t, codexHooksOrig, readFile(t, hooks), "the user's hooks are back as they were")
	requireMissing(t, hooks+".partyline.bak")

	cli.calls = nil
	require.Empty(t, env.CleanLegacy(context.Background()), "idempotent")
	require.Equal(t, []string{"codex mcp get partyline --json"}, cli.calls)
	require.Equal(t, codexHooksOrig, readFile(t, hooks))
}

// A hooks file that only held our group is deleted; $CODEX_HOME moves it.
func TestCleanLegacyCodexHomeAndCreatedFile(t *testing.T) {
	dir := t.TempDir()
	env, cli := testEnv(t, map[string]string{"CODEX_HOME": dir})
	hooks := filepath.Join(dir, "hooks.json")
	writeFile(t, hooks, `{"hooks":{"Stop":[`+codexGroup+`]}}`)
	known := false
	cli.handle = codexCLI(&known)
	require.True(t, cleaned(t, env)["codex"].Removed)
	requireMissing(t, hooks)
}

// Hooks and servers that are not ours stay: another binary, a wrapper, extra arguments, and an MCP
// server named partyline that does not run `channel --tools-only`.
func TestCleanLegacyKeepsForeignEntries(t *testing.T) {
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
			return []byte(`{"transport":{"command":"/x/partyline","args":["serve"]}}`), nil
		}
		return nil, nil
	}
	require.Empty(t, env.CleanLegacy(context.Background()))
	require.Equal(t, []string{"codex mcp get partyline --json"}, cli.calls, "the server is not removed")
	require.Equal(t, foreign, readFile(t, hooks))

	// The same server entry in the JSON files of Gemini and Antigravity.
	other := `{"mcpServers":{"partyline":{"command":"/x/partyline","args":["serve"]},"fs":{"command":"npx"}}}`
	gemini := filepath.Join(env.Home, ".gemini", "settings.json")
	agy := filepath.Join(env.Home, ".gemini", "config", "mcp_config.json")
	writeFile(t, gemini, other)
	writeFile(t, agy, other)
	cli.calls = nil
	require.Empty(t, env.CleanLegacy(context.Background()))
	require.Equal(t, other, readFile(t, gemini))
	require.Equal(t, other, readFile(t, agy))
	require.NotContains(t, strings.Join(cli.calls, "\n"), "agy mcp remove")
}

const geminiWithOurs = "{\n  \"general\": {\"vimMode\": true},\n  \"mcpServers\": {\n    \"other\": {\"command\": \"o\"},\n" +
	"    \"partyline\": {\"command\": \"/opt/bin/partyline\", \"args\": [\"channel\", \"--tools-only\", \"--harness\", \"gemini\"]}\n  },\n" +
	"  \"hooks\": {\n    \"AfterAgent\": [\n      {\"matcher\": \"*\", \"hooks\": [{\"name\": \"partyline\", \"type\": \"command\", \"command\": \"/opt/bin/partyline hook gemini-after-agent\", \"timeout\": 30000}]},\n" +
	"      {\"matcher\": \"*\", \"hooks\": [{\"name\": \"mine\", \"type\": \"command\", \"command\": \"./mine.sh\"}]}\n    ]\n  }\n}\n"

// Gemini: the server and the AfterAgent hook leave settings.json, without the gemini CLI, and the user's
// own server, hook and settings stay.
func TestCleanLegacyGemini(t *testing.T) {
	env, cli := testEnv(t, nil)
	env.LookPath = func(string) (string, error) { return "", errors.New("not on PATH") }
	settings := filepath.Join(env.Home, ".gemini", "settings.json")
	writeFile(t, settings, geminiWithOurs)

	got := cleaned(t, env)
	require.Len(t, got, 1)
	require.True(t, got["gemini"].Removed)
	require.Equal(t, "Gemini", got["gemini"].Title)
	require.Empty(t, cli.calls)
	require.JSONEq(t, `{"general":{"vimMode":true},"mcpServers":{"other":{"command":"o"}},
		"hooks":{"AfterAgent":[{"matcher":"*","hooks":[{"name":"mine","type":"command","command":"./mine.sh"}]}]}}`, readFile(t, settings))

	require.Empty(t, env.CleanLegacy(context.Background()), "idempotent")
}

// Gemini settings holding only our entries: the file goes, like the one setup created.
func TestCleanLegacyGeminiOnlyOurs(t *testing.T) {
	env, _ := testEnv(t, nil)
	settings := filepath.Join(env.Home, ".gemini", "settings.json")
	writeFile(t, settings, `{"mcpServers":{"partyline":{"command":"/opt/bin/partyline","args":["channel","--tools-only"]}}}`)
	require.True(t, cleaned(t, env)["gemini"].Removed)
	requireMissing(t, settings)
}

const agyMCPOrig = "{\n  \"mcpServers\": {\n    \"other\": {\"command\": \"o\", \"disabled\": false}\n  }\n}\n"

const agySettingsOrig = "{\n  \"model\": \"Gemini 3 Pro\",\n  \"trustedWorkspaces\": [\"/home/dev\"],\n  \"permissions\": {\n    \"allow\": [\"command(git status)\", \"mcp(other/tool)\"]\n  }\n}\n"

const agyOurServer = `{"command":"/opt/bin/partyline","args":["channel","--tools-only","--harness","antigravity"],"disabled":false}`

// agyCLI emulates `agy mcp remove` on the global mcp_config.json; like the real CLI it fails when
// the server is missing.
func agyCLI(t *testing.T, config string) func(string) ([]byte, error) {
	return func(line string) ([]byte, error) {
		if !strings.HasPrefix(line, "agy mcp remove ") {
			return nil, nil
		}
		d, _, err := readDoc(config)
		require.NoError(t, err)
		ok, err := d.del("mcpServers", "partyline")
		require.NoError(t, err)
		if !ok {
			return []byte(`Error: MCP server "partyline" not found`), errors.New("exit status 1")
		}
		require.NoError(t, os.WriteFile(config, d.data, 0o644))
		return nil, nil
	}
}

// writeAgyLeftovers writes the three files older versions edited, each with the user's own entries
// and ours.
func writeAgyLeftovers(t *testing.T, env Env) (mcp, hooks, settings string) {
	t.Helper()
	dir := filepath.Join(env.Home, ".gemini")
	mcp, hooks, settings = filepath.Join(dir, "config", "mcp_config.json"), filepath.Join(dir, "config", "hooks.json"), filepath.Join(dir, "antigravity-cli", "settings.json")
	writeFile(t, mcp, strings.Replace(agyMCPOrig, "false}\n  }", "false},\n    \"partyline\": "+agyOurServer+"\n  }", 1))
	writeFile(t, hooks, `{"lint":{"PostToolUse":[{"matcher":"run_command","hooks":[{"command":"./lint.sh"}]}]},`+
		`"partyline":{"Stop":[{"command":"./mine.sh"},{"type":"command","command":"/opt/bin/partyline hook antigravity-stop","timeout":30}]}}`)
	rules := `"command(git status)","mcp(other/tool)"`
	for _, r := range antigravityToolRules {
		rules += `,"` + r + `"`
	}
	writeFile(t, settings, `{"model":"Gemini 3 Pro","trustedWorkspaces":["/home/dev"],"permissions":{"allow":[`+rules+`]}}`)
	return mcp, hooks, settings
}

// Antigravity: with agy on PATH its server is removed by `agy mcp remove partyline`, our Stop handler
// and our tool rules leave the files, and everything else stays; a second run finds nothing.
func TestCleanLegacyAntigravity(t *testing.T) {
	env, cli := testEnv(t, nil)
	mcp, hooks, settings := writeAgyLeftovers(t, env)
	cli.handle = agyCLI(t, mcp)

	got := cleaned(t, env)
	require.Len(t, got, 1)
	require.NoError(t, got["antigravity"].Err)
	require.True(t, got["antigravity"].Removed)
	require.NoError(t, got["antigravity"].Err)
	require.Equal(t, "Antigravity", got["antigravity"].Title)
	require.Equal(t, []string{"codex mcp get partyline --json", "agy mcp remove partyline"}, cli.calls)
	require.JSONEq(t, `{"mcpServers":{"other":{"command":"o","disabled":false}}}`, readFile(t, mcp))
	require.JSONEq(t, `{"lint":{"PostToolUse":[{"matcher":"run_command","hooks":[{"command":"./lint.sh"}]}]},"partyline":{"Stop":[{"command":"./mine.sh"}]}}`, readFile(t, hooks))
	require.JSONEq(t, agySettingsOrig, readFile(t, settings), "the user's rules and settings stay")

	cli.calls = nil
	require.Empty(t, env.CleanLegacy(context.Background()), "idempotent")
	require.Equal(t, []string{"codex mcp get partyline --json"}, cli.calls, "agy is not asked to remove what is gone")
}

// Without agy on PATH its MCP file is left alone (there is no agy to ask), but the hook and rules are removed.
func TestCleanLegacyAntigravityWithoutAgy(t *testing.T) {
	env, cli := testEnv(t, nil)
	env.LookPath = func(string) (string, error) { return "", errors.New("not on PATH") }
	mcp, hooks, settings := writeAgyLeftovers(t, env)
	before := readFile(t, mcp)

	require.True(t, cleaned(t, env)["antigravity"].Removed)
	require.Empty(t, cli.calls)
	require.Equal(t, before, readFile(t, mcp))
	require.NotContains(t, readFile(t, hooks), "antigravity-stop")
	require.JSONEq(t, agySettingsOrig, readFile(t, settings))
}

// Files setup created for Antigravity (hooks and settings held only ours) are deleted again.
func TestCleanLegacyAntigravityDeletesCreatedFiles(t *testing.T) {
	env, cli := testEnv(t, nil)
	hooks := filepath.Join(env.Home, ".gemini", "config", "hooks.json")
	settings := filepath.Join(env.Home, ".gemini", "antigravity-cli", "settings.json")
	writeFile(t, hooks, `{"partyline":{"Stop":[{"type":"command","command":"/opt/bin/partyline hook antigravity-stop","timeout":30}]}}`)
	writeFile(t, settings, `{"permissions":{"allow":["mcp(partyline/reply)","mcp(partyline/wait_for_messages)"]}}`)
	require.True(t, cleaned(t, env)["antigravity"].Removed)
	require.NotContains(t, strings.Join(cli.calls, "\n"), "agy")
	requireMissing(t, hooks)
	requireMissing(t, settings)
}

// Hooks written with --always and a binary path with spaces are ours too.
func TestCleanLegacyHookCommandForms(t *testing.T) {
	env, _ := testEnv(t, nil)
	env.Binary = "/opt/my tools/partyline"
	hooks := filepath.Join(env.Home, ".codex", "hooks.json")
	writeFile(t, hooks, `{"hooks":{"Stop":[`+
		`{"hooks":[{"type":"command","command":"'/opt/my tools/partyline' hook codex-stop"}]},`+
		`{"hooks":[{"type":"command","command":"'/opt/my tools/partyline' hook codex-stop --always"}]}]}}`)
	env.LookPath = func(string) (string, error) { return "", errors.New("not on PATH") }
	require.True(t, cleaned(t, env)["codex"].Removed)
	requireMissing(t, hooks)
}

// A file that is not valid JSON is not touched, the harness CLI does not run, and the result names the
// file; the other harnesses are still cleaned.
func TestCleanLegacyInvalidFileIsNotTouched(t *testing.T) {
	for id, rel := range map[string]string{"codex": ".codex/hooks.json", "gemini": ".gemini/settings.json", "antigravity": ".gemini/config/hooks.json"} {
		t.Run(id, func(t *testing.T) {
			env, cli := testEnv(t, nil)
			path := filepath.Join(env.Home, rel)
			writeFile(t, path, "{\"hooks\": {,}}")
			got := cleaned(t, env)
			require.Len(t, got, 1)
			var pe *ParseError
			require.True(t, errors.As(got[id].Err, &pe), "%v", got[id].Err)
			require.Contains(t, got[id].Err.Error(), path)
			require.False(t, got[id].Removed)
			require.NotContains(t, strings.Join(cli.calls, "\n"), "remove")
			require.Equal(t, "{\"hooks\": {,}}", readFile(t, path))
		})
	}
}

// A hooks file that is a symlink out of the home is refused, not followed.
func TestCleanLegacyRefusesSymlinkOutOfHome(t *testing.T) {
	env, _ := testEnv(t, nil)
	hooks := filepath.Join(env.Home, ".codex", "hooks.json")
	target := filepath.Join(t.TempDir(), "hooks.json")
	writeFile(t, target, codexHooksWithOurs())
	require.NoError(t, os.MkdirAll(filepath.Dir(hooks), 0o755))
	require.NoError(t, os.Symlink(target, hooks))
	env.LookPath = func(string) (string, error) { return "", errors.New("not on PATH") }

	got := cleaned(t, env)
	require.ErrorContains(t, got["codex"].Err, "refusing to write through the symlink")
	require.Equal(t, codexHooksWithOurs(), readFile(t, target))
}

// A failing `codex mcp remove` is reported with its output and the hook is left for the next run.
func TestCleanLegacyCLIFailure(t *testing.T) {
	env, cli := testEnv(t, nil)
	cli.handle = func(line string) ([]byte, error) {
		if line == "codex mcp get partyline --json" {
			return []byte(codexServerJSON), nil
		}
		return []byte("config is locked"), errors.New("exit status 1")
	}
	got := cleaned(t, env)
	require.ErrorContains(t, got["codex"].Err, "config is locked")
	require.False(t, got["codex"].Removed)
}
