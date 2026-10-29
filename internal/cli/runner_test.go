package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/cliconfig"
	"github.com/wnexous/partyline/internal/runners/spec"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/users"
)

// runnerSecret is the synthetic token a run command passes to the container; no test ever uses a
// real credential.
const runnerSecret = "pl_agent_syntheticrun0secret"

// fakeDocker is a docker stand-in for the CLI tests: it records each argument vector, answers the
// reads from files the test controls and sleeps on `docker events` until it is killed.
type fakeDocker struct {
	path, log, ps, logs, volume, fail string
}

// newFakeDocker writes the stand-in; the test writes ps and logs and the fail marker.
func newFakeDocker(t *testing.T) *fakeDocker {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	dir := t.TempDir()
	f := &fakeDocker{
		path: filepath.Join(dir, "docker"), log: filepath.Join(dir, "calls.log"),
		ps: filepath.Join(dir, "ps.jsonl"), logs: filepath.Join(dir, "logs.txt"),
		volume: "pl-atlas-workspace", fail: filepath.Join(dir, "fail"),
	}
	require.NoError(t, os.WriteFile(f.ps, []byte(`{"Names":"pl-atlas","State":"running","Status":"Up 2 minutes"}`+"\n"), 0o600))
	require.NoError(t, os.WriteFile(f.logs, []byte("atlas is thinking\n"), 0o600))
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + fakeShellQuote(f.log) + "\n" +
		"if [ -e " + fakeShellQuote(f.fail) + " ]; then\n" +
		"  printf '%s\\n' 'permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock' >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"case \"$1\" in\n" +
		"version) printf '%s\\n' 27.3.1 ;;\n" +
		"ps) cat " + fakeShellQuote(f.ps) + " ;;\n" +
		"events) exec sleep 30 ;;\n" +
		"logs) cat " + fakeShellQuote(f.logs) + " ;;\n" +
		"inspect) printf '%s\\n' " + fakeShellQuote(f.volume) + " ;;\n" +
		"esac\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(f.path, []byte(script), 0o755))
	return f
}

// fakeShellQuote quotes a path for the fake script.
func fakeShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// calls returns the recorded docker argument vectors.
func (f *fakeDocker) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

// runnerProcess is one `partyline runner` running in the background.
type runnerProcess struct {
	done        chan int
	out, errOut *lockedBuffer
	cancel      context.CancelFunc
	stopped     bool
}

// startRunner runs the CLI in the background with the fakes of the test.
func startRunner(t *testing.T, o setupOpts, args ...string) *runnerProcess {
	t.Helper()
	var out, errOut lockedBuffer
	std := testStdio(t, o, &out, &errOut)
	ctx, cancel := context.WithCancel(context.Background())
	p := &runnerProcess{done: make(chan int, 1), out: &out, errOut: &errOut, cancel: cancel}
	go func() { p.done <- execute(ctx, append([]string{"partyline"}, args...), std) }()
	t.Cleanup(func() {
		if p.stopped {
			return
		}
		p.stopped = true
		cancel()
		select {
		case <-p.done:
		case <-time.After(chanWait):
			t.Errorf("the runner did not stop; stdout: %s", out.String())
		}
	})
	return p
}

// output is everything the runner printed.
func (p *runnerProcess) output() string { return p.out.String() + p.errOut.String() }

// waitFor waits until the runner printed substr.
func (p *runnerProcess) waitFor(t *testing.T, substr string) {
	t.Helper()
	require.Eventually(t, func() bool { return strings.Contains(p.output(), substr) },
		20*time.Second, 10*time.Millisecond, "no %q; stdout: %s; stderr: %s", substr, p.out.String(), p.errOut.String())
}

// wait waits for the runner to end by itself and returns its exit code.
func (p *runnerProcess) wait(t *testing.T) int {
	t.Helper()
	select {
	case code := <-p.done:
		p.stopped = true
		return code
	case <-time.After(30 * time.Second):
		t.Fatalf("the runner did not end; stdout: %s; stderr: %s", p.out.String(), p.errOut.String())
		return -1
	}
}

// runnerEnv is a server with alice, her agent token and her runner tokens.
type runnerEnv struct {
	env        *testkit.Env
	alice      *testkit.Client
	agentToken string
}

// newRunnerEnv returns a server with alice and an agent token, like a logged-in machine.
func newRunnerEnv(t *testing.T) *runnerEnv {
	t.Helper()
	env := testkit.New(t)
	alice := env.NewUser("alice")
	_, agent, err := env.App.Users.CreateToken(context.Background(), alice.User.ID, users.TokenInput{Kind: dto.TokenKindAgent, Name: "laptop"})
	require.NoError(t, err)
	return &runnerEnv{env: env, alice: alice, agentToken: agent}
}

// runnerToken creates a runner token of alice, as the device approval does.
func (e *runnerEnv) runnerToken(t *testing.T) string {
	t.Helper()
	_, secret, err := e.env.App.Users.CreateRunnerToken(context.Background(), e.alice.User.ID, "devbox (runner)")
	require.NoError(t, err)
	return secret
}

// online waits until one of alice's runners holds a live connection and returns it (CA-01).
func (e *runnerEnv) online(t *testing.T) dto.Runner {
	t.Helper()
	var found dto.Runner
	require.Eventually(t, func() bool {
		for _, r := range testkit.Decode[dto.List[dto.Runner]](e.alice.Get("/api/v1/runners").RequireStatus(http.StatusOK)).Items {
			if r.Online {
				found = r
				return true
			}
		}
		return false
	}, 20*time.Second, 20*time.Millisecond, "the runner never came online")
	return found
}

// CA-02: --check prints the Docker version and whether a runner token is saved, and exits 0 only
// when both are ready.
func TestRunnerCheck(t *testing.T) {
	docker := newFakeDocker(t)
	home := t.TempDir()
	saved := setupOpts{env: map[string]string{"HOME": home}, dockerPath: docker.path}
	require.NoError(t, cliconfig.Save(configPath(home), cliconfig.Config{
		URL: "https://partyline.example.com", Token: "pl_agent_synthetic", RunnerToken: "pl_runner_synthetic",
	}))

	r := runCommand(t, saved, "runner", "--check")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "Docker version: 27.3.1\n")
	require.Contains(t, r.stdout, "Runner token: saved in "+configPath(home)+"\n")
	require.NotContains(t, r.stdout+r.stderr, "pl_runner_synthetic", "the token value is never printed")

	noToken := setupOpts{env: map[string]string{"HOME": t.TempDir()}, dockerPath: docker.path}
	r = runCommand(t, noToken, "runner", "--check")
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stdout, "Runner token: none saved: run partyline runner once on this machine to approve it\n")

	missing := setupOpts{env: map[string]string{"HOME": home}, dockerPath: filepath.Join(t.TempDir(), "no-docker")}
	r = runCommand(t, missing, "runner", "--check")
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stdout, "Docker version: not usable:")
	require.Contains(t, r.stdout, "not installed")

	require.NoError(t, os.WriteFile(docker.fail, nil, 0o600))
	r = runCommand(t, saved, "runner", "--check")
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stdout, "permission denied")
	require.Contains(t, r.stdout, "usermod -aG docker", "the hint that fixes the socket permission")
}

// CA-01: without a runner token `partyline runner` approves the machine through the device flow
// with kind=runner, saves that token next to the agent token (0600) and joins.
func TestRunnerDeviceApprovalSavesTheRunnerToken(t *testing.T) {
	env, clock, alice := deviceSetupEnv(t)
	_, agentSecret, err := env.App.Users.CreateToken(context.Background(), alice.User.ID, users.TokenInput{Kind: dto.TokenKindAgent, Name: "laptop"})
	require.NoError(t, err)
	home := t.TempDir()
	writeConfig(t, home, cliconfig.Config{URL: env.URL, Token: agentSecret})
	docker := newFakeDocker(t)

	approved := false
	process := startRunner(t, setupOpts{
		env: map[string]string{"HOME": home}, hostname: "devbox-7", dockerPath: docker.path,
		onSleep: func(stdout string) bool {
			clock.Advance(2 * time.Second)
			if !approved {
				if code := approvalCode(stdout); code != "" {
					typeCode(alice, "approve", code).RequireStatus(http.StatusOK)
					approved = true
				}
			}
			return true
		},
	}, "runner")

	process.waitFor(t, "Approved by @alice on "+env.URL+"\n")
	process.waitFor(t, "Runner token saved in "+configPath(home)+"\n")
	process.waitFor(t, "Connected to "+env.URL+" (Docker 27.3.1)\n")
	runner := waitRunnerOnline(t, alice)
	require.Equal(t, "devbox-7", runner.Machine)

	cfg, err := cliconfig.Load(configPath(home))
	require.NoError(t, err)
	require.Equal(t, agentSecret, cfg.Token, "the login of the machine is untouched")
	require.True(t, strings.HasPrefix(cfg.RunnerToken, "pl_runner_"), "runner token saved: %q", cfg.RunnerToken)
	fi, err := os.Stat(configPath(home))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	require.NotContains(t, process.output(), cfg.RunnerToken, "the token is never printed")

	tokens := testkit.Decode[dto.List[dto.Token]](alice.Get("/api/v1/tokens").RequireStatus(http.StatusOK)).Items
	found := false
	for _, token := range tokens {
		if token.Kind == dto.TokenKindRunner {
			found = true
			require.Equal(t, "devbox-7 (runner)", token.Name)
		}
	}
	require.True(t, found, "the approval created a runner token")

	res, err := env.App.Runners.Hub().Do(context.Background(), runner.ID, spec.Command{Op: spec.OpState})
	require.NoError(t, err)
	require.Equal(t, []spec.Container{{Name: "pl-atlas", State: spec.StateRunning}}, res.Containers)
}

// waitRunnerOnline waits for the runner alice owns to come online.
func waitRunnerOnline(t *testing.T, alice *testkit.Client) dto.Runner {
	t.Helper()
	var found dto.Runner
	require.Eventually(t, func() bool {
		items := testkit.Decode[dto.List[dto.Runner]](alice.Get("/api/v1/runners").RequireStatus(http.StatusOK)).Items
		if len(items) == 1 && items[0].Online {
			found = items[0]
			return true
		}
		return false
	}, 20*time.Second, 20*time.Millisecond, "the runner never came online")
	return found
}

// CA-05, CA-06, CA-08, CA-09: a live runner executes the protocol's operations through the hub,
// with the environment in a private env file and no secret anywhere else.
func TestRunnerExecutesContainerOperations(t *testing.T) {
	e := newRunnerEnv(t)
	home := t.TempDir()
	writeConfig(t, home, cliconfig.Config{URL: e.env.URL, Token: e.agentToken, RunnerToken: e.runnerToken(t)})
	docker := newFakeDocker(t)
	process := startRunner(t, setupOpts{env: map[string]string{"HOME": home}, hostname: "devbox", dockerPath: docker.path}, "runner")
	process.waitFor(t, "Connected to "+e.env.URL+" (Docker 27.3.1)\n")
	runner := e.online(t)
	hub := e.env.App.Runners.Hub()
	ctx := context.Background()

	res, err := hub.Do(ctx, runner.ID, spec.Command{Op: spec.OpState})
	require.NoError(t, err)
	require.Equal(t, []spec.Container{{Name: "pl-atlas", State: spec.StateRunning}}, res.Containers)

	run := spec.RunSpec{
		Name: "pl-atlas", Image: spec.DefaultImageRepo + ":1", Volume: "pl-atlas-workspace",
		CPUs: 1, MemoryMB: 1024, Labels: map[string]string{"partyline.agent": "agent-1"},
		Env: map[string]string{"PARTYLINE_TOKEN": runnerSecret, "PARTYLINE_URL": e.env.URL},
	}
	_, err = hub.Do(ctx, runner.ID, spec.Command{Op: spec.OpRun, Run: &run})
	require.NoError(t, err)

	stateDir := filepath.Join(home, ".local", "state", "partyline", "agents")
	envPath := filepath.Join(stateDir, "pl-atlas.env")
	body, err := os.ReadFile(envPath)
	require.NoError(t, err)
	require.Equal(t, "PARTYLINE_TOKEN="+runnerSecret+"\nPARTYLINE_URL="+e.env.URL+"\n", string(body))
	fi, err := os.Stat(envPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "the env file is private (CA-08)")
	di, err := os.Stat(stateDir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), di.Mode().Perm())

	runCall := ""
	for _, line := range docker.calls(t) {
		if strings.HasPrefix(line, "run ") {
			runCall = line
		}
	}
	require.Contains(t, runCall, "--env-file "+envPath)
	require.NotContains(t, runCall, runnerSecret, "no secret on the command line (CA-08)")
	require.NotContains(t, process.output(), runnerSecret, "no secret in the runner's output (CA-08)")

	res, err = hub.Do(ctx, runner.ID, spec.Command{Op: spec.OpLogs, Name: "pl-atlas", Tail: 10})
	require.NoError(t, err)
	require.Contains(t, res.Logs, "atlas is thinking")

	_, err = hub.Do(ctx, runner.ID, spec.Command{Op: spec.OpRemove, Name: "pl-atlas", RemoveVolume: true})
	require.NoError(t, err)
	_, err = os.Stat(envPath)
	require.ErrorIs(t, err, os.ErrNotExist, "remove deletes the env file")
	require.Contains(t, strings.Join(docker.calls(t), "\n"), "volume rm pl-atlas-workspace")
}

// CA-01: a token the server rejects is forgotten (the agent login stays) and the command ends 1.
func TestRunnerForgetsARejectedToken(t *testing.T) {
	e := newRunnerEnv(t)
	home := t.TempDir()
	writeConfig(t, home, cliconfig.Config{URL: e.env.URL, Token: e.agentToken, RunnerToken: "pl_runner_syntheticrevoked"})
	docker := newFakeDocker(t)
	process := startRunner(t, setupOpts{env: map[string]string{"HOME": home}, hostname: "devbox", dockerPath: docker.path}, "runner")
	require.Equal(t, 1, process.wait(t))
	require.Contains(t, process.errOut.String(), "does not accept the saved runner token")
	require.Contains(t, process.errOut.String(), "run partyline runner again")

	cfg, err := cliconfig.Load(configPath(home))
	require.NoError(t, err)
	require.Empty(t, cfg.RunnerToken, "the rejected token is forgotten")
	require.Equal(t, e.agentToken, cfg.Token, "the agent login stays")
	require.Empty(t, docker.calls(t), "nothing reached Docker")
}

// CA-09: the runner refuses a command whose values the server should never send, without running
// docker, and reconnects when the connection ends (CA-01).
func TestRunnerRefusesInvalidCommandsAndReconnects(t *testing.T) {
	docker := newFakeDocker(t)
	server := newFakeRunnerServer(t)
	server.onConn = func(n int, ctx context.Context, ws *websocket.Conn, _ spec.Hello) {
		switch n {
		case 1:
			writeJSONFrame(ws, spec.Command{Type: spec.FrameCommand, ID: "c1", Op: spec.OpStart, Name: "../../evil"})
			if res, err := readResultFrame(ctx, ws); err == nil {
				server.results <- res
			}
			_ = ws.Close(websocket.StatusServiceRestart, "reconnect")
		case 2:
			writeJSONFrame(ws, spec.Command{Type: spec.FrameCommand, ID: "c2", Op: spec.OpState})
			if res, err := readResultFrame(ctx, ws); err == nil {
				server.results <- res
			}
			_, _, _ = ws.Read(ctx) // hold the connection until the runner stops
		}
	}
	home := t.TempDir()
	writeConfig(t, home, cliconfig.Config{URL: server.srv.URL, Token: "pl_agent_synthetic", RunnerToken: "pl_runner_synthetic"})
	process := startRunner(t, setupOpts{env: map[string]string{"HOME": home}, hostname: "devbox", dockerPath: docker.path}, "runner")

	hello := waitValue(t, server.hellos, "the hello frame")
	require.Equal(t, spec.FrameHello, hello.Type)
	require.Equal(t, "27.3.1", hello.DockerVersion)
	require.Equal(t, []spec.Container{{Name: "pl-atlas", State: spec.StateRunning}}, hello.Containers)
	require.Equal(t, "Bearer pl_runner_synthetic", waitValue(t, server.auths, "the runner token"))

	refused := waitValue(t, server.results, "the result of the invalid command")
	require.False(t, refused.OK)
	require.Contains(t, refused.Error, "name")
	require.NotContains(t, refused.Error, "../../evil", "the error never echoes the value")

	state := waitValue(t, server.results, "the state result")
	require.True(t, state.OK, "state result: %+v; stdout: %s", state, process.output())
	require.Equal(t, []spec.Container{{Name: "pl-atlas", State: spec.StateRunning}}, state.Containers)

	require.Eventually(t, func() bool { return server.connections.Load() == 2 }, 20*time.Second, 10*time.Millisecond,
		"the runner reconnected")
	process.waitFor(t, "Not connected:")
	require.Equal(t, 1, strings.Count(process.output(), "Registered runner"), "registration is printed once")
	for _, line := range docker.calls(t) {
		require.NotContains(t, line, "../../evil", "no docker call carries a refused value")
	}
}

// CA-02: install writes the systemd user unit and enables it; --dry-run prints it only.
func TestRunnerInstallAndUninstall(t *testing.T) {
	home := t.TempDir()
	var mu sync.Mutex
	var calls [][]string
	o := setupOpts{env: map[string]string{"HOME": home}, systemctl: func(args []string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, args)
		return nil, nil
	}}
	unitPath := filepath.Join(home, ".config", "systemd", "user", "partyline-runner.service")

	dry := runCommand(t, o, "runner", "install", "--dry-run")
	require.Equal(t, 0, dry.code, "stderr: %s", dry.stderr)
	require.Contains(t, dry.stdout, "[Unit]\nDescription=Partyline runner")
	require.Contains(t, dry.stdout, "ExecStart="+testBinary+" runner\n")
	require.Contains(t, dry.stdout, "Restart=always\n")
	require.Contains(t, dry.stdout, "RestartSec=5\n")
	require.Contains(t, dry.stdout, "WantedBy=default.target\n")
	require.NotContains(t, dry.stdout, "loginctl")
	require.NoFileExists(t, unitPath)
	require.Empty(t, calls, "the dry run runs no systemctl")

	without := runCommand(t, o, "runner", "install")
	require.Equal(t, 1, without.code)
	require.Contains(t, without.stderr, "no runner token saved yet")
	require.Contains(t, without.stderr, "run partyline runner once")
	require.NoFileExists(t, unitPath)

	writeConfig(t, home, cliconfig.Config{URL: "https://partyline.example.com", RunnerToken: "pl_runner_synthetic"})
	installed := runCommand(t, o, "runner", "install")
	require.Equal(t, 0, installed.code, "stderr: %s", installed.stderr)
	unit, err := os.ReadFile(unitPath)
	require.NoError(t, err)
	require.Contains(t, string(unit), "ExecStart="+testBinary+" runner\n")
	require.Contains(t, installed.stdout, "loginctl enable-linger $USER")
	require.Equal(t, [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", "partyline-runner.service"}}, calls)

	mu.Lock()
	calls = nil
	mu.Unlock()
	removed := runCommand(t, o, "runner", "uninstall")
	require.Equal(t, 0, removed.code, "stderr: %s", removed.stderr)
	require.NoFileExists(t, unitPath)
	require.Equal(t, [][]string{{"--user", "disable", "--now", "partyline-runner.service"}, {"--user", "daemon-reload"}}, calls)

	again := runCommand(t, o, "runner", "uninstall")
	require.Equal(t, 0, again.code)
	require.Contains(t, again.stdout, "nothing to remove")

	// XDG_CONFIG_HOME moves the unit, like the other config files.
	xdg := t.TempDir()
	require.NoError(t, cliconfig.Save(filepath.Join(xdg, "partyline", "config.json"), cliconfig.Config{
		URL: "https://partyline.example.com", RunnerToken: "pl_runner_synthetic",
	}))
	r := runCommand(t, setupOpts{env: map[string]string{"HOME": home, "XDG_CONFIG_HOME": xdg}}, "runner", "install", "--dry-run")
	require.Equal(t, 0, r.code)
	r = runCommand(t, setupOpts{env: map[string]string{"HOME": home, "XDG_CONFIG_HOME": xdg}, systemctl: o.systemctl}, "runner", "install")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.FileExists(t, filepath.Join(xdg, "systemd", "user", "partyline-runner.service"))
}

// CA-01: the instance comes from --url, PARTYLINE_URL or the config, and a saved runner token
// never goes to another instance.
func TestRunnerNeedsItsOwnInstance(t *testing.T) {
	r := runCommand(t, setupOpts{env: map[string]string{"HOME": t.TempDir()}}, "runner")
	require.Equal(t, 2, r.code)
	require.Contains(t, r.stderr, "PARTYLINE_URL")

	home := t.TempDir()
	writeConfig(t, home, cliconfig.Config{URL: "https://one.example.com", RunnerToken: "pl_runner_synthetic"})
	r = runCommand(t, setupOpts{env: map[string]string{"HOME": home}}, "runner", "--url", "https://two.example.com")
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "https://one.example.com")
	require.Contains(t, r.stderr, "https://two.example.com")
	require.NotContains(t, r.stdout+r.stderr, "pl_runner_synthetic")
}

// fakeRunnerServer answers the runner's two routes and scripts each WebSocket connection.
type fakeRunnerServer struct {
	srv         *httptest.Server
	hellos      chan spec.Hello
	results     chan spec.Result
	auths       chan string
	connections atomic.Int32
	onConn      func(n int, ctx context.Context, ws *websocket.Conn, hello spec.Hello)
}

// newFakeRunnerServer starts the server; the test sets onConn before starting the runner.
func newFakeRunnerServer(t *testing.T) *fakeRunnerServer {
	t.Helper()
	f := &fakeRunnerServer{
		hellos: make(chan spec.Hello, 8), results: make(chan spec.Result, 8), auths: make(chan string, 8),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/runner/register", func(w http.ResponseWriter, r *http.Request) {
		f.auths <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"runner-1","name":"devbox","machine":"devbox","online":true,"created_at":"2026-10-29T09:00:00Z"}`)
	})
	mux.HandleFunc("GET /api/v1/runner/ws", func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		n := int(f.connections.Add(1))
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var hello spec.Hello
		if json.Unmarshal(data, &hello) != nil {
			return
		}
		f.hellos <- hello
		if f.onConn != nil {
			f.onConn(n, ctx, ws, hello)
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// writeJSONFrame writes one protocol frame.
func writeJSONFrame(ws *websocket.Conn, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = ws.Write(ctx, websocket.MessageText, data)
}

// readResultFrame reads the next result frame, skipping the state frames the runner sends on its
// own.
func readResultFrame(ctx context.Context, ws *websocket.Conn) (spec.Result, error) {
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return spec.Result{}, err
		}
		var head struct {
			Type spec.FrameType `json:"type"`
		}
		if json.Unmarshal(data, &head) != nil || head.Type != spec.FrameResult {
			continue
		}
		var res spec.Result
		if err := json.Unmarshal(data, &res); err != nil {
			return spec.Result{}, err
		}
		return res, nil
	}
}

// waitValue waits for one value on ch.
func waitValue[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(30 * time.Second):
		t.Fatalf("no %s within 30s", what)
		var zero T
		return zero
	}
}
