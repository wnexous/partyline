package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/cliconfig"
	"github.com/wnexous/partyline/internal/config"
	"github.com/wnexous/partyline/internal/runnerexec"
	"github.com/wnexous/partyline/internal/runners/spec"
)

// The runner's own timings (CA-01, CA-02).
const (
	// runnerBackoffStart and runnerBackoffMax bound the wait between connection attempts: one
	// second at first, sixty seconds at most, with jitter so runners do not reconnect in step.
	runnerBackoffStart = time.Second
	runnerBackoffMax   = 60 * time.Second
	// runnerHealthyAfter is how long a connection must last before the backoff starts over.
	runnerHealthyAfter = 30 * time.Second
	// runnerPingInterval keeps the idle outbound WebSocket open through proxies, like the server's
	// own pings; the peer answers automatically.
	runnerPingInterval = 25 * time.Second
	// runnerWriteTimeout bounds one frame write.
	runnerWriteTimeout = 10 * time.Second
	// runnerHelloTimeout bounds the Docker checks that build the hello frame.
	runnerHelloTimeout = 10 * time.Second
	// runnerOpTimeout bounds one docker operation on this machine.
	runnerOpTimeout = 2 * time.Minute
	// runnerUnitName is the systemd user unit `partyline runner install` writes.
	runnerUnitName = "partyline-runner.service"
)

// Errors the runner treats specially.
var (
	// errRunnerTokenRejected means the server will never accept the saved runner token again
	// (revoked, expired, or the runner was deleted); the token is forgotten and the command ends.
	errRunnerTokenRejected = errors.New("the server does not accept the saved runner token")
	// errRunnerNotRegistered means the token is valid but its runner registration is gone; the
	// runner registers again.
	errRunnerNotRegistered = errors.New("the runner token is not registered")
)

// runnerCmd runs `partyline runner` (CA-01, CA-02): this machine joins Partyline as a server that
// runs container AIs and keeps an outbound WebSocket for commands, or installs/removes the systemd
// user service that keeps it joined.
func runnerCmd(ctx context.Context, args []string, io stdio) int {
	if len(args) > 0 {
		switch args[0] {
		case "install":
			return runnerInstall(ctx, args[1:], io)
		case "uninstall":
			return runnerUninstall(ctx, args[1:], io)
		}
	}
	fs := flag.NewFlagSet("runner", flag.ContinueOnError)
	fs.SetOutput(io.stderr)
	url := fs.String("url", "", "Partyline instance URL (default $PARTYLINE_URL, else the saved config)")
	name := fs.String("name", "", "the name the web app shows for this runner (default: the hostname)")
	check := fs.Bool("check", false, "print whether Docker and the saved runner token are usable, then exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *check {
		return runnerCheck(ctx, io)
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(io.stderr, "partyline runner: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	return runnerJoin(ctx, *url, *name, io)
}

// runnerCheck prints Docker's version (or why Docker is unusable) and whether a runner token is
// saved, and exits 0 only when both are ready (CA-02).
func runnerCheck(ctx context.Context, io stdio) int {
	ok := true
	executor := runnerexec.New(runnerexec.Options{Docker: dockerExecutable(io)})
	if version, err := executor.DockerVersion(ctx); err != nil {
		fmt.Fprintf(io.stdout, "Docker version: not usable: %v\n", err)
		ok = false
	} else {
		fmt.Fprintf(io.stdout, "Docker version: %s\n", version)
	}
	cfg, path, err := loadSavedConfig(io)
	switch {
	case err != nil:
		fmt.Fprintf(io.stdout, "Runner token: not usable: %v\n", err)
		ok = false
	case cfg.RunnerToken == "":
		fmt.Fprintln(io.stdout, "Runner token: none saved: run partyline runner once on this machine to approve it")
		ok = false
	default:
		fmt.Fprintf(io.stdout, "Runner token: saved in %s\n", path)
	}
	if !ok {
		return 1
	}
	return 0
}

// runnerJoin makes this machine a runner: it gets a runner token (the saved one, else the device
// approval flow), registers the machine, and keeps the connection until ctx ends (CA-01).
func runnerJoin(ctx context.Context, urlFlag, name string, io stdio) int {
	cfg, path, err := loadSavedConfig(io)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline runner: ignoring the current config file: %v\n", err)
		cfg = cliconfig.Config{}
	}
	base := firstNonEmpty(urlFlag, io.getenv("PARTYLINE_URL"), cfg.URL)
	if base == "" {
		fmt.Fprintln(io.stderr, "partyline runner: set PARTYLINE_URL (or --url), or run partyline login first")
		return 2
	}
	base, err = normalizeURL(base)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline runner: %v\n", err)
		return 2
	}
	// The config file holds one instance; a runner token never goes to another one (ONB-08).
	if cfg.RunnerToken != "" && cfg.URL != "" && !cliconfig.SameURL(base, cfg.URL) {
		fmt.Fprintf(io.stderr, "partyline runner: the saved runner token is for %s, not %s: log in to %s first (partyline login --url %s)\n",
			cfg.URL, base, base, base)
		return 1
	}
	cfg.URL = base
	if cfg.RunnerToken == "" {
		if path == "" {
			fmt.Fprintln(io.stderr, "partyline runner: no config file path: set HOME or XDG_CONFIG_HOME")
			return 2
		}
		token, approver, err := approveInBrowserKind(ctx, base, io, dto.TokenKindRunner)
		if err != nil {
			fmt.Fprintf(io.stderr, "partyline runner: %v\n", err)
			return 1
		}
		cfg.RunnerToken = token
		if err := cliconfig.Save(path, cfg); err != nil {
			fmt.Fprintf(io.stderr, "partyline runner: save the config file: %v\n", err)
			return 1
		}
		fmt.Fprintf(io.stdout, "Approved by @%s on %s\nRunner token saved in %s\n", approver, base, path)
	}
	images, err := config.ParseRunnerImages(io.getenv)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline runner: %v\n", err)
		return 2
	}
	stateDir, err := runnerStateDir(io)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline runner: %v\n", err)
		return 2
	}
	loop := &runnerLoop{
		base: base, token: cfg.RunnerToken, name: strings.TrimSpace(name), machine: machineName(io),
		path: path, images: images, io: io,
		exec: runnerexec.New(runnerexec.Options{StateDir: stateDir, Images: images, Docker: dockerExecutable(io)}),
	}
	return loop.run(ctx)
}

// runnerLoop holds one `partyline runner` process.
type runnerLoop struct {
	base, token string
	// name is the --name flag; empty lets the server name the runner after the machine.
	name, machine string
	// path is the config file, so a rejected token can be forgotten.
	path   string
	images []string
	exec   *runnerexec.Executor
	io     stdio
}

// run registers the runner and keeps it connected until ctx ends, with the backoff between
// attempts (CA-01). It returns the process exit code.
func (l *runnerLoop) run(ctx context.Context) int {
	backoff := runnerBackoffStart
	registered := ""
	for {
		runner, err := l.register(ctx)
		if err != nil {
			switch {
			case errors.Is(err, errRunnerTokenRejected):
				l.reject()
				return 1
			case ctx.Err() != nil:
				return 0
			}
			if !l.retry(ctx, backoff, err) {
				return 0
			}
			backoff = nextRunnerBackoff(backoff)
			continue
		}
		if runner.ID != registered {
			fmt.Fprintf(l.io.stdout, "Registered runner %q on %s\n", runner.Name, l.base)
			registered = runner.ID
		}
		started := time.Now()
		err = l.serve(ctx)
		if ctx.Err() != nil {
			return 0
		}
		switch {
		case errors.Is(err, errRunnerTokenRejected):
			l.reject()
			return 1
		case errors.Is(err, errRunnerNotRegistered):
			continue // the registration is gone: register again at once
		}
		if time.Since(started) >= runnerHealthyAfter {
			backoff = runnerBackoffStart
		}
		if !l.retry(ctx, backoff, err) {
			return 0
		}
		backoff = nextRunnerBackoff(backoff)
	}
}

// register records this machine with the runner token (POST /api/v1/runner/register), which is
// idempotent per token: 201 the first time, 200 after (CA-01). Only a token the server refuses ends
// the runner: everything else is a server that is restarting or a network that is down, and is
// retried with the connection backoff, so a deploy never leaves a machine without its runner.
func (l *runnerLoop) register(ctx context.Context) (dto.Runner, error) {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+l.token)
	resp, err := request(ctx, http.MethodPost, l.base+"/api/v1/runner/register",
		dto.RegisterRunnerRequest{Name: l.name, Machine: l.machine}, h)
	if err != nil {
		return dto.Runner{}, fmt.Errorf("cannot reach %s: %w", l.base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var runner dto.Runner
		if err := json.NewDecoder(resp.Body).Decode(&runner); err != nil {
			return dto.Runner{}, fmt.Errorf("unreadable runner answer: %w", err)
		}
		return runner, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return dto.Runner{}, errRunnerTokenRejected
	default:
		// A server restarting behind a proxy answers 502, 503 or 504 for a moment and a server that is
		// still starting may answer 404: retrying is what the backoff is for (CA-01).
		if msg := decodeError(resp).Message; msg != "" {
			return dto.Runner{}, fmt.Errorf("register: the server answered %d: %s", resp.StatusCode, msg)
		}
		return dto.Runner{}, fmt.Errorf("register: the server answered %d", resp.StatusCode)
	}
}

// serve holds one WebSocket connection: it sends the hello, answers the server's commands and
// reports container states, until the connection ends (CA-02, CA-09).
func (l *runnerLoop) serve(ctx context.Context) error {
	ws, resp, err := websocket.Dial(ctx, runnerWSURL(l.base), &websocket.DialOptions{
		HTTPClient: &http.Client{}, // no timeout: the connection outlives the handshake
		HTTPHeader: http.Header{"Authorization": {"Bearer " + l.token}},
	})
	if err != nil {
		if resp != nil {
			_ = resp.Body.Close()
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				return errRunnerTokenRejected
			case http.StatusForbidden:
				return errRunnerNotRegistered
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("connect to %s: %w", l.base, err)
	}
	defer func() { _ = ws.CloseNow() }()
	ws.SetReadLimit(1 << 20)

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	hello := l.hello(connCtx)
	if err := writeFrame(connCtx, ws, hello); err != nil {
		return fmt.Errorf("send the hello: %w", err)
	}
	if hello.DockerVersion != "" {
		fmt.Fprintf(l.io.stdout, "Connected to %s (Docker %s)\n", l.base, hello.DockerVersion)
	} else {
		fmt.Fprintf(l.io.stdout, "Connected to %s; Docker is not usable: %s\n", l.base, hello.DockerError)
	}

	conn := &runnerConn{exec: l.exec, images: l.images}
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		l.reportStates(connCtx, ws, cancel)
	}()
	go func() {
		defer workers.Done()
		l.keepAlive(connCtx, ws, cancel)
	}()
	err = conn.readCommands(ctx, connCtx, ws)
	cancel()
	_ = ws.CloseNow()
	workers.Wait()
	return err
}

// hello builds the first frame of a connection: the Docker version (or why it is unusable) and
// the containers this machine already runs (CA-02).
func (l *runnerLoop) hello(ctx context.Context) spec.Hello {
	ctx, cancel := context.WithTimeout(ctx, runnerHelloTimeout)
	defer cancel()
	type versionResult struct {
		version string
		err     error
	}
	versions := make(chan versionResult, 1)
	containers := make(chan []spec.Container, 1)
	go func() {
		version, err := l.exec.DockerVersion(ctx)
		versions <- versionResult{version: version, err: err}
	}()
	go func() {
		list, err := l.exec.State(ctx, "")
		if err != nil {
			list = nil
		}
		containers <- list
	}()
	h := spec.Hello{Type: spec.FrameHello, Containers: <-containers}
	if res := <-versions; res.err != nil {
		h.DockerError = runnerexec.Message(res.err.Error())
	} else {
		h.DockerVersion = res.version
	}
	if h.DockerVersion == "" && h.DockerError == "" {
		h.DockerError = "Docker did not answer"
	}
	return h
}

// reportStates sends every state the executor watches as an unsolicited state frame (CA-06).
func (l *runnerLoop) reportStates(ctx context.Context, ws *websocket.Conn, fail func()) {
	for containers := range l.exec.Watch(ctx) {
		if err := spec.ValidateContainers(containers); err != nil {
			continue // the executor builds them; a bad one never reaches the server
		}
		if err := writeFrame(ctx, ws, spec.StateEvent{Type: spec.FrameState, Containers: containers}); err != nil {
			fail()
			return
		}
	}
}

// keepAlive pings the server so a dead path is noticed even when no command arrives; the peer's
// pings are answered by the WebSocket reader.
func (l *runnerLoop) keepAlive(ctx context.Context, ws *websocket.Conn, fail func()) {
	ticker := time.NewTicker(runnerPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pctx, cancel := context.WithTimeout(ctx, runnerWriteTimeout)
			err := ws.Ping(pctx)
			cancel()
			if err != nil {
				fail()
				return
			}
		}
	}
}

// retry says why the runner is not connected and waits before the next attempt; false when ctx
// ended during the wait.
func (l *runnerLoop) retry(ctx context.Context, d time.Duration, cause error) bool {
	wait := jitter(d)
	fmt.Fprintf(l.io.stdout, "Not connected: %v; retrying in %s\n", cause, wait.Round(100*time.Millisecond))
	return l.sleep(ctx, wait)
}

// sleep waits d, unless a test replaced the wait.
func (l *runnerLoop) sleep(ctx context.Context, d time.Duration) bool {
	if l.io.sleep != nil {
		return l.io.sleep(ctx, d)
	}
	return sleepCtx(ctx, d)
}

// reject forgets a runner token the server will not accept any more, so the next run approves the
// machine again (CA-01).
func (l *runnerLoop) reject() {
	fmt.Fprintf(l.io.stderr, "partyline runner: %v; run partyline runner again to approve this machine\n", errRunnerTokenRejected)
	if l.path == "" {
		return
	}
	cfg, err := cliconfig.Load(l.path)
	if err != nil || cfg.RunnerToken == "" {
		return
	}
	cfg.RunnerToken = ""
	if err := cliconfig.Save(l.path, cfg); err != nil {
		fmt.Fprintf(l.io.stderr, "partyline runner: forget the rejected token: %v\n", err)
	}
}

// runnerConn executes the commands of one connection (CA-09).
type runnerConn struct {
	exec   *runnerexec.Executor
	images []string
}

// readCommands reads and answers commands until the connection ends. Each command runs in its own
// goroutine, so a slow Docker operation never blocks the reader (and the pings it answers).
func (c *runnerConn) readCommands(procCtx, connCtx context.Context, ws *websocket.Conn) error {
	for {
		_, data, err := ws.Read(connCtx)
		if err != nil {
			if connCtx.Err() != nil {
				return errors.New("the connection closed")
			}
			if websocket.CloseStatus(err) == websocket.StatusPolicyViolation {
				return errRunnerTokenRejected
			}
			return fmt.Errorf("the connection closed: %w", err)
		}
		var cmd spec.Command
		if json.Unmarshal(data, &cmd) != nil || cmd.Type != spec.FrameCommand {
			continue
		}
		go func(cmd spec.Command) {
			ctx, cancel := context.WithTimeout(procCtx, runnerOpTimeout)
			defer cancel()
			res := c.execute(ctx, cmd)
			res.Type, res.ID = spec.FrameResult, cmd.ID
			_ = writeFrame(connCtx, ws, res)
		}(cmd)
	}
}

// execute runs one command, validating every value again on this side (CA-09).
func (c *runnerConn) execute(ctx context.Context, cmd spec.Command) spec.Result {
	if err := spec.ValidateCommand(cmd, c.images); err != nil {
		return spec.Result{OK: false, Error: runnerexec.Message("refused the command: " + err.Error())}
	}
	res := spec.Result{OK: true}
	var err error
	switch cmd.Op {
	case spec.OpRun:
		err = c.exec.Run(ctx, *cmd.Run)
	case spec.OpStart:
		err = c.exec.Start(ctx, cmd.Name)
	case spec.OpStop:
		err = c.exec.Stop(ctx, cmd.Name)
	case spec.OpRestart:
		err = c.exec.Restart(ctx, cmd.Name)
	case spec.OpRemove:
		err = c.exec.Remove(ctx, cmd.Name, cmd.RemoveVolume)
	case spec.OpLogs:
		res.Logs, err = c.exec.Logs(ctx, cmd.Name, cmd.Tail)
	case spec.OpState:
		res.Containers, err = c.exec.State(ctx, cmd.Name)
	default:
		err = fmt.Errorf("unknown operation %q", cmd.Op)
	}
	if err != nil {
		return spec.Result{OK: false, Error: runnerexec.Message(err.Error())}
	}
	return res
}

// writeFrame writes one protocol frame, bounded by runnerWriteTimeout.
func writeFrame(ctx context.Context, ws *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, runnerWriteTimeout)
	defer cancel()
	return ws.Write(wctx, websocket.MessageText, data)
}

// runnerWSURL is the WebSocket address of the runner connection of an instance.
func runnerWSURL(base string) string {
	switch {
	case strings.HasPrefix(base, "https://"):
		return "wss://" + strings.TrimPrefix(base, "https://") + "/api/v1/runner/ws"
	default:
		return "ws://" + strings.TrimPrefix(base, "http://") + "/api/v1/runner/ws"
	}
}

// nextRunnerBackoff doubles d up to runnerBackoffMax.
func nextRunnerBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > runnerBackoffMax {
		d = runnerBackoffMax
	}
	return d
}

// jitter spreads d up to a quarter more, so runners that lost the same server do not reconnect
// together; the wait never leaves [runnerBackoffStart, runnerBackoffMax].
func jitter(d time.Duration) time.Duration {
	spread := d / 4
	if spread <= 0 {
		return d
	}
	wait := d + time.Duration(rand.Int64N(int64(spread)+1)) // #nosec G404 -- reconnect jitter, not a secret
	if wait > runnerBackoffMax {
		return runnerBackoffMax
	}
	return wait
}

// dockerExecutable is the docker the runner executes; "" means the docker on PATH (tests inject a
// path or a path that does not exist).
func dockerExecutable(io stdio) string {
	if io.dockerPath == nil {
		return ""
	}
	path, err := io.dockerPath()
	if err != nil {
		return ""
	}
	return path
}

// runnerStateDir is where the runner writes env files:
// $XDG_STATE_HOME/partyline/agents, else ~/.local/state/partyline/agents (CA-08).
func runnerStateDir(io stdio) (string, error) {
	if dir := strings.TrimSpace(io.getenv("XDG_STATE_HOME")); dir != "" {
		return filepath.Join(dir, "partyline", "agents"), nil
	}
	home := userHome(io)
	if home == "" {
		return "", errors.New("no state directory: set HOME or XDG_STATE_HOME")
	}
	return filepath.Join(home, ".local", "state", "partyline", "agents"), nil
}

// systemdUnitPath is the systemd user unit `partyline runner install` writes (CA-02).
func systemdUnitPath(io stdio) (string, error) {
	if dir := strings.TrimSpace(io.getenv("XDG_CONFIG_HOME")); dir != "" {
		return filepath.Join(dir, "systemd", "user", runnerUnitName), nil
	}
	home := userHome(io)
	if home == "" {
		return "", errors.New("no config directory: set HOME or XDG_CONFIG_HOME")
	}
	return filepath.Join(home, ".config", "systemd", "user", runnerUnitName), nil
}

// userHome is HOME, else USERPROFILE on Windows.
func userHome(io stdio) string {
	if home := strings.TrimSpace(io.getenv("HOME")); home != "" {
		return home
	}
	return strings.TrimSpace(io.getenv("USERPROFILE"))
}

// runnerInstall writes the systemd user unit and enables it, so the runner survives reboots and
// logins (CA-02); --dry-run prints the unit only.
func runnerInstall(ctx context.Context, args []string, io stdio) int {
	fs := flag.NewFlagSet("runner install", flag.ContinueOnError)
	fs.SetOutput(io.stderr)
	dryRun := fs.Bool("dry-run", false, "print the systemd user unit instead of installing it")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path, err := systemdUnitPath(io)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline runner install: %v\n", err)
		return 2
	}
	binary, err := runnerExecutable(io)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline runner install: %v\n", err)
		return 1
	}
	unit := systemdUnit(binary)
	if *dryRun {
		fmt.Fprint(io.stdout, unit)
		return 0
	}
	cfg, _, err := loadSavedConfig(io)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline runner install: %v\n", err)
		return 1
	}
	if cfg.RunnerToken == "" {
		fmt.Fprintln(io.stderr, "partyline runner install: no runner token saved yet: run partyline runner once on this machine to approve it, then install the service")
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		fmt.Fprintf(io.stderr, "partyline runner install: %v\n", err)
		return 1
	}
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil { // #nosec G306 -- a systemd unit holds no secret
		fmt.Fprintf(io.stderr, "partyline runner install: write %s: %v\n", path, err)
		return 1
	}
	if err := systemctl(ctx, io, "--user", "daemon-reload"); err != nil {
		fmt.Fprintf(io.stderr, "partyline runner install: %v\n", err)
		return 1
	}
	if err := systemctl(ctx, io, "--user", "enable", "--now", runnerUnitName); err != nil {
		fmt.Fprintf(io.stderr, "partyline runner install: %v\n", err)
		return 1
	}
	fmt.Fprintf(io.stdout, "Installed %s: the runner starts now and after every login.\n", path)
	fmt.Fprintln(io.stdout, "To keep it running when you are not logged in: loginctl enable-linger $USER")
	return 0
}

// runnerUninstall stops and removes the systemd user unit.
func runnerUninstall(ctx context.Context, args []string, io stdio) int {
	fs := flag.NewFlagSet("runner uninstall", flag.ContinueOnError)
	fs.SetOutput(io.stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path, err := systemdUnitPath(io)
	if err != nil {
		fmt.Fprintf(io.stderr, "partyline runner uninstall: %v\n", err)
		return 2
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(io.stdout, "No runner service installed at %s: nothing to remove\n", path)
		return 0
	} else if err != nil {
		fmt.Fprintf(io.stderr, "partyline runner uninstall: %v\n", err)
		return 1
	}
	if err := systemctl(ctx, io, "--user", "disable", "--now", runnerUnitName); err != nil {
		fmt.Fprintf(io.stderr, "partyline runner uninstall: %v\n", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(io.stderr, "partyline runner uninstall: remove %s: %v\n", path, err)
		return 1
	}
	if err := systemctl(ctx, io, "--user", "daemon-reload"); err != nil {
		fmt.Fprintf(io.stderr, "partyline runner uninstall: %v\n", err)
		return 1
	}
	fmt.Fprintf(io.stdout, "Removed %s: the runner no longer starts at login\n", path)
	return 0
}

// systemctl runs `systemctl --user` (tests inject a recorder; the real command is never run there).
func systemctl(ctx context.Context, io stdio, args ...string) error {
	if io.systemctl != nil {
		_, err := io.systemctl(ctx, args...)
		return err
	}
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput() // #nosec G204 -- fixed command, arguments from this file
	if err != nil {
		reason := runnerexec.Message(string(out))
		if reason == "" {
			reason = err.Error()
		}
		return fmt.Errorf("systemctl %s: %s", strings.Join(args, " "), reason)
	}
	return nil
}

// runnerExecutable is this binary's absolute path, as the unit's ExecStart needs.
func runnerExecutable(io stdio) (string, error) {
	executable := io.executable
	if executable == nil {
		executable = os.Executable
	}
	path, err := executable()
	if err != nil {
		return "", fmt.Errorf("find this binary: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("find this binary: %w", err)
	}
	return abs, nil
}

// systemdUnit renders the unit: a simple service that starts the runner at boot and restarts it
// five seconds after a crash (CA-02). It holds no secret: the token stays in the config file.
func systemdUnit(binary string) string {
	return "[Unit]\n" +
		"Description=Partyline runner (container AIs on this machine)\n" +
		"After=network-online.target\n" +
		"Wants=network-online.target\n" +
		"\n" +
		"[Service]\n" +
		"Type=simple\n" +
		"ExecStart=" + systemdEscape(binary) + " runner\n" +
		"Restart=always\n" +
		"RestartSec=5\n" +
		"\n" +
		"[Install]\n" +
		"WantedBy=default.target\n"
}

// systemdEscape quotes a path systemd may split at a space.
func systemdEscape(path string) string {
	if !strings.ContainsAny(path, " \t\"'\\") {
		return path
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(path) + `"`
}
