package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

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
