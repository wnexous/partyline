package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/channel"
	"github.com/wnexous/partyline/internal/cliconfig"
)

// writeConfig saves a config file under home the way `partyline setup` does and returns its path.
func writeConfig(t *testing.T, home string, c cliconfig.Config) string {
	t.Helper()
	path := filepath.Join(home, ".config", "partyline", "config.json")
	require.NoError(t, cliconfig.Save(path, c))
	return path
}

func initialize(c *mcpClient) {
	c.send(map[string]any{"jsonrpc": "2.0", "id": 0, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	c.next()
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

// ONB-05: without PARTYLINE_URL and PARTYLINE_TOKEN the channel reads both from the config file.
func TestChannelReadsConfigFile(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	writeConfig(t, home, cliconfig.Config{URL: e.env.URL, Token: e.secret})

	c := startChannel(t, nil, map[string]string{"HOME": home, "PARTYLINE_AGENT": "from-file"}, "/tmp/wd")
	initialize(c)
	e.agent(t, "from-file")
}

// ONB-05: environment variables override the file (the file's URL and token here are wrong).
func TestChannelEnvironmentOverridesConfigFile(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	writeConfig(t, home, cliconfig.Config{URL: "http://127.0.0.1:1", Token: e.secret})
	c := startChannel(t, nil, map[string]string{"HOME": home, "PARTYLINE_URL": e.env.URL, "PARTYLINE_AGENT": "env-url"}, "/tmp/wd")
	initialize(c)
	e.agent(t, "env-url")

	home2 := t.TempDir()
	writeConfig(t, home2, cliconfig.Config{URL: e.env.URL, Token: "pl_agent_wrong"})
	c2 := startChannel(t, nil, map[string]string{"HOME": home2, "PARTYLINE_TOKEN": e.secret, "PARTYLINE_AGENT": "env-token"}, "/tmp/wd")
	initialize(c2)
	e.agent(t, "env-token")
}

// ONB-05: flags override the file too.
func TestChannelFlagsOverrideConfigFile(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	writeConfig(t, home, cliconfig.Config{URL: "http://127.0.0.1:1", Token: "pl_agent_wrong"})
	c := startChannel(t, []string{"--url", e.env.URL, "--token", e.secret, "--agent", "flagged"}, map[string]string{"HOME": home}, "/tmp/wd")
	initialize(c)
	e.agent(t, "flagged")
}

// Edge case (ONB-05): a config file others can read is refused with the chmod fix.
func TestChannelRefusesConfigFileNot0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	home := t.TempDir()
	path := writeConfig(t, home, cliconfig.Config{URL: "http://127.0.0.1:1", Token: "pl_agent_x"})
	require.NoError(t, os.Chmod(path, 0o644))

	var out bytes.Buffer
	code := execute(context.Background(), []string{"partyline", "channel"}, stdio{
		stdin: bytes.NewReader(nil), stdout: &out, stderr: &out,
		getenv: func(k string) string { return map[string]string{"HOME": home}[k] },
	})
	require.Equal(t, 1, code)
	require.Contains(t, out.String(), "chmod 600 "+path)
	require.NotContains(t, out.String(), "pl_agent_x")
}

// PUSH-03, PUSH-06: `channel --harness omp` runs the full channel (the agent comes online)
// with the instructions that describe the omp extension's <partyline …> framing.
func TestChannelHarnessOmp(t *testing.T) {
	e := newChannelEnv(t)
	c := startChannel(t, []string{"--harness", "omp"}, map[string]string{"PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": e.secret, "PARTYLINE_AGENT": "omp-proj"}, "/tmp/wd")
	c.send(map[string]any{"jsonrpc": "2.0", "id": 0, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-11-25"}})
	res, _ := c.next()["result"].(map[string]any)
	text, _ := res["instructions"].(string)
	require.Equal(t, channel.OmpInstructions, text)
	require.Contains(t, text, "<partyline room=")
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	e.agent(t, "omp-proj")
}

// An unknown harness, or omp together with --tools-only, exits 2 before anything connects.
func TestChannelHarnessFlagErrors(t *testing.T) {
	code, _, errOut := run("channel", "--harness", "codex")
	require.Equal(t, 2, code)
	require.Contains(t, errOut, `partyline channel: unknown --harness "codex" (known: omp)`)

	code, _, errOut = run("channel", "--harness", "omp", "--tools-only")
	require.Equal(t, 2, code)
	require.Contains(t, errOut, "partyline channel: --harness omp runs the full channel; drop --tools-only")
}
