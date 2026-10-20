package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/integrations"
)

type fileCase struct {
	id, title       string
	config, ext     string // relative to HOME
	source          string // embedded source
	keys            string // where the entry goes, for the plan
	entry           string // the entry setup writes (JSON)
	orig            string // an existing config with other entries
	withEntry       string // orig after setup, byte for byte
	extKind, detect string
}

var fileCases = []fileCase{
	{
		id: "opencode", title: "OpenCode", detect: ".config/opencode",
		config: ".config/opencode/opencode.json", ext: ".config/opencode/plugins/partyline.ts", source: "opencode/partyline.ts",
		keys: "mcp.partyline", extKind: "plugin",
		entry: `{"type":"local","command":["/opt/bin/partyline","channel","--tools-only"],"enabled":true}`,
		orig:  "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {\n    \"other\": {\"type\": \"remote\", \"url\": \"https://x.example\"}\n  },\n  \"theme\": \"tokyonight\"\n}\n",
		withEntry: "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {\n    \"other\": {\"type\": \"remote\", \"url\": \"https://x.example\"},\n" +
			"    \"partyline\": {\n      \"type\": \"local\",\n      \"command\": [\n        \"/opt/bin/partyline\",\n        \"channel\",\n        \"--tools-only\"\n      ],\n      \"enabled\": true\n    }\n" +
			"  },\n  \"theme\": \"tokyonight\"\n}\n",
	},
	{
		id: "pi", title: "pi", detect: ".pi/agent",
		config: ".pi/agent/mcp.json", ext: ".pi/agent/extensions/partyline.ts", source: "pi/partyline.ts",
		keys: "mcpServers.partyline", extKind: "extension",
		entry: `{"command":"/opt/bin/partyline","args":["channel","--tools-only"]}`,
		orig:  "{\"mcpServers\": {\"fs\": {\"command\": \"npx\", \"args\": [\"-y\", \"fs\"]}}}",
		withEntry: "{\"mcpServers\": {\"fs\": {\"command\": \"npx\", \"args\": [\"-y\", \"fs\"]}," +
			"\"partyline\":{\"command\":\"/opt/bin/partyline\",\"args\":[\"channel\",\"--tools-only\"]}}}",
	},
	{
		id: "omp", title: "oh-my-pi (omp)", detect: ".omp/agent",
		config: ".omp/agent/mcp.json", ext: ".omp/agent/extensions/partyline.ts", source: "omp/partyline.ts",
		keys: "mcpServers.partyline", extKind: "extension",
		entry: `{"command":"/opt/bin/partyline","args":["channel"]}`,
		orig:  "{\n\t\"disabledServers\": [\"x\"]\n}\n",
		withEntry: "{\n\t\"disabledServers\": [\"x\"],\n\t\"mcpServers\": {\n\t  \"partyline\": {\n\t    \"command\": \"/opt/bin/partyline\",\n" +
			"\t    \"args\": [\n\t      \"channel\"\n\t    ]\n\t  }\n\t}\n}\n",
	},
}

func extensionWant(t *testing.T, source string) string {
	t.Helper()
	src, err := integrations.Files.ReadFile(source)
	require.NoError(t, err)
	return "// partyline v1.2.3: written by `partyline setup`; `partyline setup --uninstall` removes it.\n" + string(src)
}

// HAR-01, HAR-02, HAR-04 (OpenCode, pi, omp): setup adds one entry with the absolute binary
// path, keeps every other byte of the config, installs the extension with a version header
// and keeps a backup; applying again changes nothing; uninstall restores the original file
// and deletes the extension and the backup.
func TestFileHarnessesRegisterAndRestore(t *testing.T) {
	for _, c := range fileCases {
		t.Run(c.id, func(t *testing.T) {
			env, cli := testEnv(t, nil)
			cfg, ext := filepath.Join(env.Home, c.config), filepath.Join(env.Home, c.ext)
			writeFile(t, cfg, c.orig)
			r := registrar(t, env, c.id)
			require.Equal(t, c.title, r.Title())
			require.Equal(t, []string{
				"edit ~/" + c.config + ": set " + c.keys + " = " + c.entry,
				"write ~/" + c.ext + " (Partyline v1.2.3 " + c.extKind + ")",
			}, r.Plan())

			require.NoError(t, r.Apply(context.Background()))
			require.Equal(t, c.withEntry, readFile(t, cfg))
			require.Equal(t, c.orig, readFile(t, cfg+".partyline.bak"))
			require.Equal(t, extensionWant(t, c.source), readFile(t, ext))
			entries, err := r.Entries(context.Background())
			require.NoError(t, err)
			require.Equal(t, []Entry{{What: "MCP server in ~/" + c.config, Binary: testBinary}}, entries)

			require.NoError(t, r.Apply(context.Background()))
			require.Equal(t, c.withEntry, readFile(t, cfg), "idempotent")

			require.Equal(t, []string{"edit ~/" + c.config + ": remove " + c.keys, "delete ~/" + c.ext}, r.RemovePlan(context.Background()))
			require.NoError(t, r.Remove(context.Background()))
			require.Equal(t, c.orig, readFile(t, cfg))
			requireMissing(t, ext)
			requireMissing(t, cfg+".partyline.bak")
			require.Empty(t, r.RemovePlan(context.Background()))
			require.Empty(t, cli.calls, "file-based registrars run no CLI")
		})
	}
}

// HAR-02 (update in place): an existing Partyline entry with an old binary is replaced; the
// members around it keep their bytes.
func TestFileHarnessUpdatesEntryInPlace(t *testing.T) {
	env, _ := testEnv(t, nil)
	cfg := filepath.Join(env.Home, ".pi/agent/mcp.json")
	writeFile(t, cfg, `{"mcpServers": {"a": {"command": "a"}, "partyline": {"command": "/old/partyline", "args": ["channel"]}, "z": {"command": "z"}}}`)
	require.NoError(t, registrar(t, env, "pi").Apply(context.Background()))
	require.Equal(t, `{"mcpServers": {"a": {"command": "a"}, "partyline": {"command":"/opt/bin/partyline","args":["channel","--tools-only"]}, "z": {"command": "z"}}}`, readFile(t, cfg))
}

// HAR-04: a config file setup created is deleted by uninstall.
func TestFileHarnessCreatesAndDeletesConfig(t *testing.T) {
	env, _ := testEnv(t, nil)
	r := registrar(t, env, "omp")
	require.NoError(t, r.Apply(context.Background()))
	cfg := filepath.Join(env.Home, ".omp/agent/mcp.json")
	require.Equal(t, decode(t, `{"mcpServers":{"partyline":{"command":"/opt/bin/partyline","args":["channel"]}}}`), jsonAt(t, cfg))
	requireMissing(t, cfg+".partyline.bak")
	require.NoError(t, r.Remove(context.Background()))
	requireMissing(t, cfg)
	requireMissing(t, filepath.Join(env.Home, ".omp/agent/extensions/partyline.ts"))
}

// Edge case (HAR-02): invalid JSON is reported and nothing is written, not even the
// extension.
func TestFileHarnessInvalidJSON(t *testing.T) {
	for _, c := range fileCases {
		t.Run(c.id, func(t *testing.T) {
			env, _ := testEnv(t, nil)
			cfg := filepath.Join(env.Home, c.config)
			writeFile(t, cfg, "{\"mcp\": {\"a\": 1,}}")
			err := registrar(t, env, c.id).Apply(context.Background())
			var pe *ParseError
			require.True(t, errors.As(err, &pe), "%v", err)
			require.Contains(t, err.Error(), cfg)
			require.Equal(t, "{\"mcp\": {\"a\": 1,}}", readFile(t, cfg))
			requireMissing(t, filepath.Join(env.Home, c.ext))
			requireMissing(t, cfg+".partyline.bak")
		})
	}
}

// An extension file of the same name that setup did not write is neither overwritten nor
// deleted.
func TestFileHarnessKeepsForeignExtension(t *testing.T) {
	env, _ := testEnv(t, nil)
	ext := filepath.Join(env.Home, ".pi/agent/extensions/partyline.ts")
	writeFile(t, ext, "// my own\n")
	r := registrar(t, env, "pi")
	require.ErrorContains(t, r.Apply(context.Background()), "was not written by partyline setup")
	requireMissing(t, filepath.Join(env.Home, ".pi/agent/mcp.json"))
	require.Equal(t, "// my own\n", readFile(t, ext))
	require.NoError(t, r.Remove(context.Background()))
	require.Equal(t, "// my own\n", readFile(t, ext))
}

// Detection: the harness CLI on PATH or its config directory.
func TestFileHarnessDetection(t *testing.T) {
	for _, c := range fileCases {
		t.Run(c.id, func(t *testing.T) {
			env, _ := testEnv(t, nil)
			env.LookPath = func(string) (string, error) { return "", errors.New("not found") }
			require.False(t, registrar(t, env, c.id).Detected())
			require.NoError(t, os.MkdirAll(filepath.Join(env.Home, c.detect), 0o755))
			require.True(t, registrar(t, env, c.id).Detected())

			env2, _ := testEnv(t, nil)
			env2.LookPath = func(name string) (string, error) {
				if name == c.id {
					return "/usr/bin/" + name, nil
				}
				return "", errors.New("not found")
			}
			require.True(t, registrar(t, env2, c.id).Detected())
		})
	}
}

// OpenCode follows $XDG_CONFIG_HOME.
func TestOpenCodeXDGConfigHome(t *testing.T) {
	xdg := t.TempDir()
	env, _ := testEnv(t, map[string]string{"XDG_CONFIG_HOME": xdg})
	require.NoError(t, registrar(t, env, "opencode").Apply(context.Background()))
	require.FileExists(t, filepath.Join(xdg, "opencode", "opencode.json"))
	require.FileExists(t, filepath.Join(xdg, "opencode", "plugins", "partyline.ts"))
}
