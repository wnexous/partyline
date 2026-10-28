package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
		entry: `{"command":"/opt/bin/partyline","args":["channel","--harness","omp"]}`,
		orig:  "{\n\t\"disabledServers\": [\"x\"]\n}\n",
		withEntry: "{\n\t\"disabledServers\": [\"x\"],\n\t\"mcpServers\": {\n\t  \"partyline\": {\n\t    \"command\": \"/opt/bin/partyline\",\n" +
			"\t    \"args\": [\n\t      \"channel\",\n\t      \"--harness\",\n\t      \"omp\"\n\t    ]\n\t  }\n\t}\n}\n",
	},
}

func extensionWant(t *testing.T, source string) string {
	t.Helper()
	src, err := integrations.Files.ReadFile(source)
	require.NoError(t, err)
	installed := strings.Replace(string(src), `const PARTYLINE_BINARY = "partyline";`, `const PARTYLINE_BINARY = "/opt/bin/partyline";`, 1)
	return "// partyline v1.2.3: written by `partyline setup`; `partyline setup --uninstall` removes it.\n" + installed
}

// PUSH-04, PUSH-05: the pi extension and the OpenCode plugin run the binary setup registered
// (its absolute path, quoted as a string literal); the omp extension runs no binary.
func TestExtensionsNameTheAbsoluteBinary(t *testing.T) {
	for _, c := range fileCases {
		t.Run(c.id, func(t *testing.T) {
			env, _ := testEnv(t, nil)
			env.Binary = `/opt/my "bin"\\partyline`
			require.NoError(t, registrar(t, env, c.id).Apply(context.Background()))
			got := readFile(t, filepath.Join(env.Home, c.ext))
			want := `const PARTYLINE_BINARY = "/opt/my \"bin\"\\\\partyline";`
			if c.id == "omp" {
				require.NotContains(t, got, "PARTYLINE_BINARY")
				return
			}
			require.Contains(t, got, "\n"+want+"\n")
			require.NotContains(t, got, `const PARTYLINE_BINARY = "partyline";`)
		})
	}
}

// setup --always: every file harness records the choice twice, in the extension constant and in
// the --always flag of the MCP server entry; applying again without it removes both.
func TestFileHarnessesRecordAlways(t *testing.T) {
	for _, c := range fileCases {
		t.Run(c.id, func(t *testing.T) {
			env, _ := testEnv(t, nil)
			env.Always = true
			cfg, ext := filepath.Join(env.Home, c.config), filepath.Join(env.Home, c.ext)
			r := registrar(t, env, c.id)
			require.Contains(t, strings.Join(r.Plan(), "\n"), "--always")
			require.NoError(t, r.Apply(context.Background()))
			require.Contains(t, readFile(t, ext), "const PARTYLINE_ALWAYS = true;")
			require.NotContains(t, readFile(t, ext), "const PARTYLINE_ALWAYS = false;")
			args := argsAt(t, cfg, strings.Split(c.keys, ".")...)
			require.NotEmpty(t, args)
			require.Equal(t, "--always", args[len(args)-1])

			env.Always = false
			require.NoError(t, registrar(t, env, c.id).Apply(context.Background()))
			require.Contains(t, readFile(t, ext), "const PARTYLINE_ALWAYS = false;")
			require.NotContains(t, readFile(t, ext), "const PARTYLINE_ALWAYS = true;")
			require.NotContains(t, argsAt(t, cfg, strings.Split(c.keys, ".")...), "--always")
		})
	}
}

// argsAt returns the command-line arguments of the MCP server entry at keys of the JSON file at
// path: args for mcp.json, command[1:] for OpenCode.
func argsAt(t *testing.T, path string, keys ...string) []any {
	t.Helper()
	entry, ok := jsonAt(t, path, keys...).(map[string]any)
	require.True(t, ok)
	if args, ok := entry["args"].([]any); ok {
		return args
	}
	command, ok := entry["command"].([]any)
	require.True(t, ok)
	return command[1:]
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

// HAR-02, PUSH-03: an omp entry from an earlier setup (plain "channel") is updated in place to
// the --harness omp channel.
func TestOmpEntryGainsHarnessFlag(t *testing.T) {
	env, _ := testEnv(t, nil)
	cfg := filepath.Join(env.Home, ".omp/agent/mcp.json")
	writeFile(t, cfg, `{"mcpServers": {"partyline": {"command": "/opt/bin/partyline", "args": ["channel"]}, "z": {"command": "z"}}}`)
	require.NoError(t, registrar(t, env, "omp").Apply(context.Background()))
	require.Equal(t, `{"mcpServers": {"partyline": {"command":"/opt/bin/partyline","args":["channel","--harness","omp"]}, "z": {"command": "z"}}}`, readFile(t, cfg))
}

// HAR-04: a config file setup created is deleted by uninstall.
func TestFileHarnessCreatesAndDeletesConfig(t *testing.T) {
	env, _ := testEnv(t, nil)
	r := registrar(t, env, "omp")
	require.NoError(t, r.Apply(context.Background()))
	cfg := filepath.Join(env.Home, ".omp/agent/mcp.json")
	require.Equal(t, decode(t, `{"mcpServers":{"partyline":{"command":"/opt/bin/partyline","args":["channel","--harness","omp"]}}}`), jsonAt(t, cfg))
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

// An OpenCode plugin written by an older setup (OpenCode 1 only, old version in its header) is
// replaced by the current one, which also loads in OpenCode 2: a default export with id and setup.
func TestOpenCodePluginFromOlderSetupIsRewritten(t *testing.T) {
	env, _ := testEnv(t, nil)
	ext := filepath.Join(env.Home, ".config/opencode/plugins/partyline.ts")
	writeFile(t, ext, "// partyline v0.3.0: written by `partyline setup`; `partyline setup --uninstall` removes it.\nexport const PartylinePlugin = async () => ({});\n")
	require.NoError(t, registrar(t, env, "opencode").Apply(context.Background()))
	got := readFile(t, ext)
	require.True(t, strings.HasPrefix(got, "// partyline v1.2.3: written by `partyline setup`;"), "the header names the installed version")
	require.NotContains(t, got, "v0.3.0")
	require.Contains(t, got, "export default {\n\tid: \"partyline\",")
	require.Contains(t, got, "\tsetup: setupOpenCode2,")
	require.Contains(t, got, "\tserver: PartylinePlugin,")
}

// skipUnlessUnixPerms skips tests that rely on unix permission bits or symlinks, and runs
// that are root (root ignores permission bits).
func skipUnlessUnixPerms(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permission bits and a non-root user")
	}
}

// HAR-05: running setup twice keeps the first backup (the file before Partyline was ever
// added), not a copy that already holds the Partyline entry.
func TestFileHarnessKeepsFirstBackup(t *testing.T) {
	env, _ := testEnv(t, nil)
	c := fileCases[1] // pi
	cfg := filepath.Join(env.Home, c.config)
	writeFile(t, cfg, c.orig)
	r := registrar(t, env, c.id)
	require.NoError(t, r.Apply(context.Background()))
	env.Binary = "/opt/new/partyline"
	require.NoError(t, registrar(t, env, c.id).Apply(context.Background()))
	require.Contains(t, readFile(t, cfg), "/opt/new/partyline")
	require.Equal(t, c.orig, readFile(t, cfg+".partyline.bak"), "the first backup is kept")
}

// HAR-05: a symlinked config (dotfile managers) stays a symlink: setup writes the entry into
// the link's target, keeps the backup next to the link, and uninstall restores the target.
func TestFileHarnessWritesThroughSymlink(t *testing.T) {
	skipUnlessUnixPerms(t)
	for _, c := range fileCases {
		t.Run(c.id, func(t *testing.T) {
			env, _ := testEnv(t, nil)
			cfg := filepath.Join(env.Home, c.config)
			target := filepath.Join(env.Home, "dotfiles", c.id+".json")
			writeFile(t, target, c.orig)
			require.NoError(t, os.MkdirAll(filepath.Dir(cfg), 0o755))
			require.NoError(t, os.Symlink(target, cfg))
			r := registrar(t, env, c.id)

			require.NoError(t, r.Apply(context.Background()))
			fi, err := os.Lstat(cfg)
			require.NoError(t, err)
			require.Equal(t, os.ModeSymlink, fi.Mode()&os.ModeSymlink, "the config is still a symlink")
			require.Equal(t, c.withEntry, readFile(t, target))
			require.Equal(t, c.orig, readFile(t, cfg+".partyline.bak"))

			require.NoError(t, r.Remove(context.Background()))
			fi, err = os.Lstat(cfg)
			require.NoError(t, err)
			require.Equal(t, os.ModeSymlink, fi.Mode()&os.ModeSymlink)
			require.Equal(t, c.orig, readFile(t, target))
		})
	}
}

// HAR-05: the extension is written before the config entry, and a config that cannot be
// written rolls the extension back: no config ever points at a missing extension, and an
// extension from an earlier setup keeps its content.
func TestFileHarnessConfigFailureRollsBackExtension(t *testing.T) {
	skipUnlessUnixPerms(t)
	for _, earlier := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "earlier setup"}[earlier], func(t *testing.T) {
			env, _ := testEnv(t, nil)
			c := fileCases[1] // pi
			cfg, ext := filepath.Join(env.Home, c.config), filepath.Join(env.Home, c.ext)
			writeFile(t, cfg, c.orig)
			old := "// partyline v1.0.0: written by `partyline setup`; `partyline setup --uninstall` removes it.\nold\n"
			require.NoError(t, os.MkdirAll(filepath.Dir(ext), 0o755)) // writable, inside the config dir
			if earlier {
				writeFile(t, ext, old)
			}
			require.NoError(t, os.Chmod(filepath.Dir(cfg), 0o555))
			t.Cleanup(func() { _ = os.Chmod(filepath.Dir(cfg), 0o755) })

			require.Error(t, registrar(t, env, c.id).Apply(context.Background()))
			require.Equal(t, c.orig, readFile(t, cfg))
			if earlier {
				require.Equal(t, old, readFile(t, ext))
			} else {
				requireMissing(t, ext)
			}
		})
	}
}

// HAR-05: an extension that cannot be written leaves the config untouched (no entry, no
// backup).
func TestFileHarnessExtensionFailureLeavesConfig(t *testing.T) {
	skipUnlessUnixPerms(t)
	env, _ := testEnv(t, nil)
	c := fileCases[0] // opencode
	cfg, ext := filepath.Join(env.Home, c.config), filepath.Join(env.Home, c.ext)
	writeFile(t, cfg, c.orig)
	require.NoError(t, os.MkdirAll(filepath.Dir(ext), 0o555))
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(ext), 0o755) })

	require.Error(t, registrar(t, env, c.id).Apply(context.Background()))
	require.Equal(t, c.orig, readFile(t, cfg))
	requireMissing(t, cfg+".partyline.bak")
	requireMissing(t, ext)
}

// HAR-05: uninstall keeps a symlinked config that ends up empty: the link stays and its
// target holds the empty object (a plain file left empty is deleted, as before).
func TestFileHarnessKeepsEmptiedSymlink(t *testing.T) {
	skipUnlessUnixPerms(t)
	env, _ := testEnv(t, nil)
	cfg := filepath.Join(env.Home, ".pi/agent/mcp.json")
	target := filepath.Join(env.Home, "dotfiles", "pi.json")
	writeFile(t, target, "{}\n")
	require.NoError(t, os.MkdirAll(filepath.Dir(cfg), 0o755))
	require.NoError(t, os.Symlink(target, cfg))
	r := registrar(t, env, "pi")
	require.NoError(t, r.Apply(context.Background()))
	require.NoError(t, r.Remove(context.Background()))
	fi, err := os.Lstat(cfg)
	require.NoError(t, err)
	require.Equal(t, os.ModeSymlink, fi.Mode()&os.ModeSymlink)
	require.JSONEq(t, "{}", readFile(t, target))
}

// HAR-05: setup writes through a symlinked config or extension only when the link resolves
// to a file inside the user's home (a symlinked home counts as home). A link to a file
// outside home is refused with an error naming the link and its target, and nothing is
// written: not the target, not the extension, not a backup.
func TestHarnessRefusesSymlinkOutsideHome(t *testing.T) {
	skipUnlessUnixPerms(t)
	for _, c := range fileCases {
		for _, which := range []string{"config", "extension"} {
			t.Run(c.id+" "+which, func(t *testing.T) {
				env, _ := testEnv(t, nil)
				cfg, ext := filepath.Join(env.Home, c.config), filepath.Join(env.Home, c.ext)
				link, target, orig := cfg, filepath.Join(t.TempDir(), "outside.json"), c.orig
				if which == "extension" {
					writeFile(t, cfg, c.orig)
					link, target, orig = ext, filepath.Join(t.TempDir(), "outside.ts"), "// partyline v1.0.0: written by `partyline setup`; `partyline setup --uninstall` removes it.\nold\n"
				}
				writeFile(t, target, orig)
				require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
				require.NoError(t, os.Symlink(target, link))

				err := registrar(t, env, c.id).Apply(context.Background())
				require.EqualError(t, err, "refusing to write through the symlink "+link+": its target "+target+" is outside your home directory "+env.Home)
				require.Equal(t, orig, readFile(t, target), "the target is untouched")
				requireMissing(t, cfg+".partyline.bak")
				if which == "config" {
					requireMissing(t, ext)
				} else {
					require.Equal(t, c.orig, readFile(t, cfg), "the config is untouched")
				}
			})
		}
	}

	t.Run("codex hooks", func(t *testing.T) {
		env, cli := testEnv(t, nil)
		hooks := filepath.Join(env.Home, ".codex", "hooks.json")
		target := filepath.Join(t.TempDir(), "hooks.json")
		writeFile(t, target, codexHooksOrig)
		require.NoError(t, os.MkdirAll(filepath.Dir(hooks), 0o755))
		require.NoError(t, os.Symlink(target, hooks))
		err := registrar(t, env, "codex").Apply(context.Background())
		require.EqualError(t, err, "refusing to write through the symlink "+hooks+": its target "+target+" is outside your home directory "+env.Home)
		require.Empty(t, cli.calls, "no CLI ran")
		require.Equal(t, codexHooksOrig, readFile(t, target))
		requireMissing(t, hooks+".partyline.bak")
	})

	t.Run("gemini settings", func(t *testing.T) {
		env, cli := testEnv(t, nil)
		settings := filepath.Join(env.Home, ".gemini", "settings.json")
		target := filepath.Join(t.TempDir(), "settings.json")
		const orig = "{\n  \"theme\": \"Default\"\n}\n"
		writeFile(t, target, orig)
		require.NoError(t, os.MkdirAll(filepath.Dir(settings), 0o755))
		require.NoError(t, os.Symlink(target, settings))
		err := registrar(t, env, "gemini").Apply(context.Background())
		require.EqualError(t, err, "refusing to write through the symlink "+settings+": its target "+target+" is outside your home directory "+env.Home)
		require.Empty(t, cli.calls, "no CLI ran")
		require.Equal(t, orig, readFile(t, target))
		requireMissing(t, settings+".partyline.bak")
	})

	t.Run("symlinked home", func(t *testing.T) {
		env, _ := testEnv(t, nil)
		c := fileCases[1] // pi
		realHome := env.Home
		env.Home = filepath.Join(t.TempDir(), "home-link")
		require.NoError(t, os.Symlink(realHome, env.Home))
		cfg := filepath.Join(env.Home, c.config)
		target := filepath.Join(realHome, "dotfiles", "pi.json")
		writeFile(t, target, c.orig)
		require.NoError(t, os.MkdirAll(filepath.Dir(cfg), 0o755))
		require.NoError(t, os.Symlink(target, cfg))
		require.NoError(t, registrar(t, env, c.id).Apply(context.Background()))
		require.Equal(t, c.withEntry, readFile(t, target))
	})
}

// HAR-05: the backup setup keeps may hold other secrets of the harness config, so it is
// private (0600) even when the config is world-readable; Backups names the backup file of
// every config setup edits itself (none for Claude Code, whose CLI edits its config).
func TestBackupsArePrivate(t *testing.T) {
	skipUnlessUnixPerms(t)
	env, _ := testEnv(t, nil)
	pi := filepath.Join(env.Home, ".pi/agent/mcp.json")
	hooks := filepath.Join(env.Home, ".codex/hooks.json")
	writeFile(t, pi, fileCases[1].orig)
	writeFile(t, hooks, codexHooksOrig)
	for _, id := range []string{"pi", "codex"} {
		require.NoError(t, registrar(t, env, id).Apply(context.Background()))
	}
	for _, p := range []string{pi, hooks} {
		fi, err := os.Stat(p + ".partyline.bak")
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), p)
		fi, err = os.Stat(p)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o644), fi.Mode().Perm(), "the config keeps its mode")
	}

	want := map[string][]string{
		"claude":      nil,
		"codex":       {hooks + ".partyline.bak"},
		"gemini":      {filepath.Join(env.Home, ".gemini/settings.json.partyline.bak")},
		"antigravity": {filepath.Join(env.Home, ".gemini/config/hooks.json.partyline.bak")},
		"opencode":    {filepath.Join(env.Home, ".config/opencode/opencode.json.partyline.bak")},
		"pi":          {pi + ".partyline.bak"},
		"omp":         {filepath.Join(env.Home, ".omp/agent/mcp.json.partyline.bak")},
	}
	for _, r := range All(env) {
		require.Equal(t, want[r.ID()], r.Backups(), r.ID())
	}
}
