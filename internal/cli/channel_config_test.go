package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
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
	writeConfig(t, home, cliconfig.Config{URL: "http://127.0.0.1:1", Token: "pl_agent_saved"})
	c := startChannel(t, nil, map[string]string{"HOME": home, "PARTYLINE_URL": e.env.URL, "PARTYLINE_TOKEN": e.secret, "PARTYLINE_AGENT": "env-url"}, "/tmp/wd")
	initialize(c)
	e.agent(t, "env-url")

	home2 := t.TempDir()
	writeConfig(t, home2, cliconfig.Config{URL: e.env.URL, Token: "pl_agent_wrong"})
	c2 := startChannel(t, nil, map[string]string{"HOME": home2, "PARTYLINE_TOKEN": e.secret, "PARTYLINE_AGENT": "env-token"}, "/tmp/wd")
	initialize(c2)
	e.agent(t, "env-token")
}

// ONB-08: the saved token is never sent to another instance: PARTYLINE_URL naming another
// server without a token makes channel exit 1 with both URLs, before it connects anywhere.
func TestChannelRefusesSavedTokenForOtherURL(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, cliconfig.Config{URL: "http://saved.example.test", Token: "pl_agent_saved"})
	var out bytes.Buffer
	env := map[string]string{"HOME": home, "PARTYLINE_URL": "http://127.0.0.1:1", "PARTYLINE_AGENT": "x"}
	code := execute(context.Background(), []string{"partyline", "channel"}, stdio{
		stdin: bytes.NewReader(nil), stdout: &out, stderr: &out,
		getenv: func(k string) string { return env[k] },
	})
	require.Equal(t, 1, code)
	require.Contains(t, out.String(), "the saved agent token is for http://saved.example.test, not http://127.0.0.1:1")
	require.NotContains(t, out.String(), "pl_agent_saved")
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

// ID-01 (T18): `channel --tools-only --harness <id>` (how setup registers Codex and Gemini)
// registers the session under that harness without any launcher variable, and the flag wins
// over PARTYLINE_HARNESS.
func TestToolsOnlyChannelHarnessFlag(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	e := newChannelEnv(t)
	registered := func(name string) dto.Agent {
		t.Helper()
		require.Eventually(t, func() bool { _, ok := e.onlineNames(t)[name]; return ok }, chanWait, 20*time.Millisecond, name)
		return e.agentNamed(t, name)
	}
	codex := startChannel(t, []string{"--tools-only", "--harness", "codex"}, e.vars(), "/work/notes")
	initialize(codex)
	a := registered("notes-codex")
	require.Equal(t, []string{"codex", "Notes · Codex"}, []string{a.Harness, a.DisplayName})

	gemini := startChannel(t, []string{"--tools-only", "--harness", "gemini"}, e.vars("PARTYLINE_HARNESS", "pi", "PARTYLINE_SESSION", "pl-g"), "/work/notes")
	initialize(gemini)
	a = registered("notes-gemini")
	require.Equal(t, "gemini", a.Harness, "the flag wins over PARTYLINE_HARNESS")
	require.NotContains(t, e.onlineNames(t), "notes-pi")
}

// ID-01 (T18): with --tools-only, --harness takes the tools-only harnesses; anything else
// exits 2 before connecting.
func TestToolsOnlyChannelHarnessFlagErrors(t *testing.T) {
	code, _, errOut := run("channel", "--tools-only", "--harness", "claude")
	require.Equal(t, 2, code)
	require.Contains(t, errOut, `partyline channel: unknown --harness "claude" with --tools-only (known: codex, gemini, opencode, pi)`)
}
