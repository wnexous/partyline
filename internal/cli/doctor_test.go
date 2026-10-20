package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/cliconfig"
	"github.com/wnexous/partyline/internal/harness"
	"github.com/wnexous/partyline/internal/testkit"
)

type doctorRun struct {
	code   int
	stdout string
}

// doctorEnv is a machine for `partyline doctor`: a fake HOME, a partyline binary file and
// the programs found on PATH.
type doctorEnv struct {
	home, binary string
	env          map[string]string
	onPath       map[string]bool
}

func newDoctorEnv(t *testing.T) *doctorEnv {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "partyline")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755))
	home := t.TempDir()
	return &doctorEnv{home: home, binary: bin, env: map[string]string{"HOME": home}, onPath: map[string]bool{"partyline": true}}
}

func (d *doctorEnv) run(t *testing.T) doctorRun {
	t.Helper()
	var out, errOut bytes.Buffer
	code := execute(context.Background(), []string{"partyline", "doctor"}, stdio{
		stdin: bytes.NewReader(nil), stdout: &out, stderr: &errOut,
		getenv:     func(k string) string { return d.env[k] },
		executable: func() (string, error) { return d.binary, nil },
		lookPath: func(name string) (string, error) {
			if d.onPath[name] {
				return "/usr/local/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		runHarness: func(_ context.Context, name string, args ...string) ([]byte, error) {
			t.Errorf("unexpected harness CLI call: %s %v", name, args)
			return nil, errors.New("unexpected")
		},
	})
	require.Empty(t, errOut.String())
	return doctorRun{code: code, stdout: out.String()}
}

// registerOmp registers Partyline in omp (a file-based harness) with binary.
func (d *doctorEnv) registerOmp(t *testing.T, binary string) {
	t.Helper()
	d.onPath["omp"] = true
	r := harness.All(harness.Env{Home: d.home, Binary: binary, Version: "dev", Getenv: func(string) string { return "" }})
	for _, reg := range r {
		if reg.ID() == "omp" {
			require.NoError(t, reg.Apply(context.Background()))
		}
	}
}

// ONB-06: on a machine set up correctly every check passes and doctor exits 0.
func TestDoctorAllPass(t *testing.T) {
	e := newChannelEnv(t)
	d := newDoctorEnv(t)
	writeConfig(t, d.home, cliconfig.Config{URL: e.env.URL, Token: e.secret})
	d.registerOmp(t, d.binary)

	r := d.run(t)
	require.Equal(t, 0, r.code, r.stdout)
	require.Equal(t, "ok   binary: partyline on PATH at /usr/local/bin/partyline\n"+
		"ok   config: "+configPath(d.home)+" is private (0600)\n"+
		"ok   server: "+e.env.URL+" is reachable\n"+
		"ok   token: agent token accepted\n"+
		"ok   oh-my-pi (omp): MCP server in ~/.omp/agent/mcp.json runs "+d.binary+"\n"+
		"ok   versions: partyline dev, server dev\n"+
		"\nAll checks passed.\n", r.stdout)
}

// ONB-06: in a crafted bad environment each check fails with its fix hint and doctor exits 1.
func TestDoctorReportsEachFailure(t *testing.T) {
	t.Run("binary not on PATH", func(t *testing.T) {
		d := newDoctorEnv(t)
		d.onPath = map[string]bool{}
		r := d.run(t)
		require.Equal(t, 1, r.code)
		require.Contains(t, r.stdout, "FAIL binary: partyline is not on PATH (this one is "+d.binary+")\n"+
			"     fix: add it to your shell profile: export PATH=\""+filepath.Dir(d.binary)+":$PATH\"\n")
	})
	t.Run("config missing", func(t *testing.T) {
		d := newDoctorEnv(t)
		r := d.run(t)
		require.Equal(t, 1, r.code)
		require.Contains(t, r.stdout, "FAIL config: "+configPath(d.home)+" does not exist\n     fix: run partyline setup\n")
		require.Contains(t, r.stdout, "FAIL server: no instance URL\n     fix: run partyline setup\n")
	})
	t.Run("config readable by others", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("unix permission bits")
		}
		d := newDoctorEnv(t)
		path := writeConfig(t, d.home, cliconfig.Config{URL: "http://127.0.0.1:1", Token: "pl_agent_x"})
		require.NoError(t, os.Chmod(path, 0o640))
		r := d.run(t)
		require.Equal(t, 1, r.code)
		require.Contains(t, r.stdout, "FAIL config: config file "+path+" has mode 0640")
		require.Contains(t, r.stdout, "     fix: chmod 600 "+path+"\n")
		require.NotContains(t, r.stdout, "pl_agent_x")
	})
	t.Run("config directory readable by the group", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("unix permission bits")
		}
		d := newDoctorEnv(t)
		path := writeConfig(t, d.home, cliconfig.Config{URL: "http://127.0.0.1:1", Token: "pl_agent_x"})
		require.NoError(t, os.Chmod(filepath.Dir(path), 0o750))
		r := d.run(t)
		require.Equal(t, 1, r.code)
		require.Contains(t, r.stdout, "     fix: chmod 700 "+filepath.Dir(path)+"\n")
	})
	t.Run("installer config without token", func(t *testing.T) {
		e := newChannelEnv(t)
		d := newDoctorEnv(t)
		writeConfig(t, d.home, cliconfig.Config{URL: e.env.URL})
		r := d.run(t)
		require.Equal(t, 1, r.code)
		require.Contains(t, r.stdout, "FAIL config: "+configPath(d.home)+" has no instance URL or no agent token\n     fix: run partyline setup\n")
		require.Contains(t, r.stdout, "FAIL token: no agent token\n     fix: run partyline setup\n")
	})
	t.Run("server unreachable", func(t *testing.T) {
		d := newDoctorEnv(t)
		writeConfig(t, d.home, cliconfig.Config{URL: "http://127.0.0.1:1", Token: "pl_agent_x"})
		r := d.run(t)
		require.Equal(t, 1, r.code)
		require.Contains(t, r.stdout, "FAIL server: cannot reach http://127.0.0.1:1")
		require.Contains(t, r.stdout, "     fix: check the URL and your network, or run partyline setup --url <instance>\n")
	})
	t.Run("token rejected", func(t *testing.T) {
		e := newChannelEnv(t)
		d := newDoctorEnv(t)
		writeConfig(t, d.home, cliconfig.Config{URL: e.env.URL, Token: "pl_agent_revoked"})
		r := d.run(t)
		require.Equal(t, 1, r.code)
		require.Contains(t, r.stdout, "FAIL token: the server rejected the token (mistyped, revoked or expired)\n"+
			"     fix: run partyline setup --browser (or --token <agent token>)\n")
	})
	t.Run("harness entry points to a missing binary", func(t *testing.T) {
		e := newChannelEnv(t)
		d := newDoctorEnv(t)
		writeConfig(t, d.home, cliconfig.Config{URL: e.env.URL, Token: e.secret})
		gone := filepath.Join(t.TempDir(), "old", "partyline")
		d.registerOmp(t, gone)
		r := d.run(t)
		require.Equal(t, 1, r.code)
		require.Contains(t, r.stdout, "FAIL oh-my-pi (omp): MCP server in ~/.omp/agent/mcp.json runs "+gone+", which does not exist\n"+
			"     fix: run partyline setup --harness omp\n")
	})
	t.Run("no harness registered", func(t *testing.T) {
		e := newChannelEnv(t)
		d := newDoctorEnv(t)
		writeConfig(t, d.home, cliconfig.Config{URL: e.env.URL, Token: e.secret})
		r := d.run(t)
		require.Equal(t, 1, r.code)
		require.Contains(t, r.stdout, "FAIL harnesses: Partyline is not registered in any installed harness\n     fix: run partyline setup\n")
	})
	t.Run("versions differ", func(t *testing.T) {
		old := Version
		t.Cleanup(func() { Version = old })
		Version = "v1.2.0"
		env := testkit.New(t, testkit.WithVersion("v1.3.0"))
		d := newDoctorEnv(t)
		writeConfig(t, d.home, cliconfig.Config{URL: env.URL, Token: "pl_agent_x"})
		r := d.run(t)
		require.Equal(t, 1, r.code)
		require.Contains(t, r.stdout, "FAIL versions: partyline v1.2.0, server v1.3.0\n"+
			"     fix: update this machine: curl -fsSL "+env.URL+"/install.sh | sh\n")
	})
	t.Run("server version unknown", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":"ok"}`)) }))
		t.Cleanup(srv.Close)
		d := newDoctorEnv(t)
		writeConfig(t, d.home, cliconfig.Config{URL: srv.URL, Token: "pl_agent_x"})
		r := d.run(t)
		require.Equal(t, 1, r.code)
		require.Contains(t, r.stdout, "FAIL versions: partyline dev, server unknown\n     fix: the server did not report its version; update the server\n")
	})
}

// HAR-03: `partyline claude [args…]` execs claude with the development-channels flag for the
// partyline server, followed by the user's arguments.
func TestClaudeLauncher(t *testing.T) {
	var gotPath string
	var gotArgv []string
	var errOut bytes.Buffer
	code := execute(context.Background(), []string{"partyline", "claude", "--resume", "abc", "-p", "hi there"}, stdio{
		stdout: &bytes.Buffer{}, stderr: &errOut, getenv: func(string) string { return "" },
		lookPath: func(name string) (string, error) {
			require.Equal(t, "claude", name)
			return "/home/me/.local/bin/claude", nil
		},
		exec: func(path string, argv, _ []string) (int, error) {
			gotPath, gotArgv = path, argv
			return 7, nil
		},
	})
	require.Equal(t, 7, code, errOut.String())
	require.Equal(t, "/home/me/.local/bin/claude", gotPath)
	require.Equal(t, []string{"claude", "--dangerously-load-development-channels", "server:partyline", "--resume", "abc", "-p", "hi there"}, gotArgv)
}

// HAR-03: without claude on PATH the launcher says so and exits 1.
func TestClaudeLauncherWithoutClaude(t *testing.T) {
	var errOut bytes.Buffer
	code := execute(context.Background(), []string{"partyline", "claude"}, stdio{
		stdout: &bytes.Buffer{}, stderr: &errOut, getenv: func(string) string { return "" },
		lookPath: func(string) (string, error) { return "", errors.New("not found") },
		exec: func(string, []string, []string) (int, error) {
			t.Error("exec without claude")
			return 0, nil
		},
	})
	require.Equal(t, 1, code)
	require.Contains(t, errOut.String(), "Claude Code (claude) is not on PATH")
}
