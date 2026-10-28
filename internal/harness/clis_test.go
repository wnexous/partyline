package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const testBinary = "/opt/bin/partyline"

// fakeCLI records every harness CLI call as one line ("claude mcp add …") and answers through
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

// setup --always: Claude Code's MCP server carries --always, so a plain session runs it.
func TestAlwaysFlagsCLIRegistrations(t *testing.T) {
	env, cli := testEnv(t, nil)
	env.Always = true
	claude := registrar(t, env, "claude")
	require.Equal(t, []string{"run: claude mcp add --scope user partyline -- /opt/bin/partyline channel --always"}, claude.Plan())
	require.NoError(t, claude.Apply(context.Background()))
	require.Equal(t, []string{"claude mcp add --scope user partyline -- /opt/bin/partyline channel --always"}, cli.calls)
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

// A failing harness CLI is reported with its output.
func TestCLIFailureCarriesOutput(t *testing.T) {
	env, cli := testEnv(t, nil)
	cli.handle = func(string) ([]byte, error) {
		return []byte("MCP server partyline already exists"), errors.New("exit status 1")
	}
	err := registrar(t, env, "claude").Apply(context.Background())
	require.ErrorContains(t, err, "MCP server partyline already exists")
}
