// Package runnerexec executes the container operations a runner receives from Partyline (CA-09).
//
// Every operation is one exec.CommandContext(ctx, "docker", args...) with an argument vector built
// from values the spec validators accepted, never a shell string. The environment of a container
// AI (tokens and secrets) is written to a private env file (0600 in a 0700 directory) and passed
// with --env-file, so it never appears on a command line, in a process list or in a log line
// (CA-08); the errors returned to the server are redacted of every value.
package runnerexec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/wnexous/partyline/internal/runners/spec"
)

// Limits and names of the executor.
const (
	// DefaultPollInterval is how often Watch re-lists the containers on top of `docker events`,
	// so a missed event is corrected (design, "Runner protocol").
	DefaultPollInterval = 15 * time.Second
	// DefaultOpTimeout bounds one docker operation when the caller sets no deadline.
	DefaultOpTimeout = 2 * time.Minute
	// AgentLabel is the Docker label every container AI carries.
	AgentLabel = "partyline.agent"
	// WorkspaceMount is where the workspace volume is mounted in a container AI (CA-05).
	WorkspaceMount = "/workspace"
	// EnvFileSuffix ends the name of the env file of a container.
	EnvFileSuffix = ".env"
	// stateDirMode and envFileMode keep secrets out of other users' reach (CA-08).
	stateDirMode = 0o700
	envFileMode  = 0o600
)

// inspectWorkspaceVolume prints the name of the volume mounted at WorkspaceMount.
const inspectWorkspaceVolume = `{{range .Mounts}}{{if eq .Destination "` + WorkspaceMount + `"}}{{.Name}}{{end}}{{end}}`

// psFormat is the one-line JSON `docker ps` prints for every container.
const psFormat = "{{json .}}"

// exitStatusPattern finds the exit code Docker prints in the status of an exited container.
var exitStatusPattern = regexp.MustCompile(`^Exited \(([0-9]{1,3})\)`)

// lastLogTail is how many output lines are read to find the reason a container is not running.
const lastLogTail = 20

// Options configure an Executor.
type Options struct {
	// StateDir is where env files are written, one per container
	// (<StateDir>/<name>.env, mode 0600 in a 0700 directory).
	StateDir string
	// Images are the image repositories a run may use besides the default agent image (CA-09).
	Images []string
	// Docker is the docker executable; empty means "docker" from PATH.
	Docker string
	// PollInterval is how often Watch re-lists the containers (default DefaultPollInterval).
	PollInterval time.Duration
	// OpTimeout bounds one docker operation that arrives without a deadline (default
	// DefaultOpTimeout).
	OpTimeout time.Duration
}

// Executor runs the container operations of the runner protocol on this machine.
type Executor struct {
	opts Options
}

// New returns an Executor with the defaults applied.
func New(opts Options) *Executor {
	if opts.PollInterval <= 0 {
		opts.PollInterval = DefaultPollInterval
	}
	if opts.OpTimeout <= 0 {
		opts.OpTimeout = DefaultOpTimeout
	}
	return &Executor{opts: opts}
}

// DockerVersion returns the version of the Docker daemon, or an error whose message says why
// Docker is unusable on this machine (not installed, no permission on the socket) (CA-02).
func (e *Executor) DockerVersion(ctx context.Context) (string, error) {
	ctx, cancel := e.bounded(ctx)
	defer cancel()
	out, err := e.docker(ctx, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(out)
	if !spec.ValidDockerVersion(version) {
		return "", fmt.Errorf("docker reported an unexpected version %q", Message(version))
	}
	return version, nil
}

// Run creates and starts the container a run command describes (CA-04, CA-05). The environment
// goes through a private env file, never through the command line (CA-08), and every value is
// validated again here before Docker sees it (CA-09).
func (e *Executor) Run(ctx context.Context, r spec.RunSpec) error {
	if err := spec.ValidateRun(r, e.opts.Images); err != nil {
		return fmt.Errorf("refusing an invalid run command: %w", err)
	}
	envPath, err := e.writeEnvFile(r.Name, r.Env)
	if err != nil {
		return err
	}
	ctx, cancel := e.bounded(ctx)
	defer cancel()
	if _, err := e.docker(ctx, e.runArgs(r, envPath)...); err != nil {
		// No container uses the file: do not leave the secrets behind (CA-08).
		e.removeEnvFile(r.Name)
		return errors.New(redact(err.Error(), r.Env))
	}
	return nil
}

// Start starts a stopped container.
func (e *Executor) Start(ctx context.Context, name string) error {
	return e.containerOp(ctx, "start", name)
}

// Stop stops a running container.
func (e *Executor) Stop(ctx context.Context, name string) error {
	return e.containerOp(ctx, "stop", name)
}

// Restart restarts a container.
func (e *Executor) Restart(ctx context.Context, name string) error {
	return e.containerOp(ctx, "restart", name)
}

// Remove removes a container and, when removeVolume is set, the workspace volume mounted at
// WorkspaceMount; the container's env file goes away with it (CA-06, CA-08).
func (e *Executor) Remove(ctx context.Context, name string, removeVolume bool) error {
	if err := validateContainerName(name); err != nil {
		return err
	}
	volume := ""
	if removeVolume {
		volume = e.workspaceVolume(ctx, name)
	}
	ctx, cancel := e.bounded(ctx)
	defer cancel()
	if _, err := e.docker(ctx, "rm", "-f", name); err != nil {
		return err
	}
	e.removeEnvFile(name)
	if volume == "" {
		return nil
	}
	if _, err := e.docker(ctx, "volume", "rm", volume); err != nil {
		return err
	}
	return nil
}

// Logs returns the last tail lines of a container's output, at most spec.MaxLogs bytes keeping
// the most recent lines (CA-06).
func (e *Executor) Logs(ctx context.Context, name string, tail int) (string, error) {
	if err := validateContainerName(name); err != nil {
		return "", err
	}
	if tail < 0 || tail > spec.MaxTail {
		return "", fmt.Errorf("tail: must be between 0 and %d", spec.MaxTail)
	}
	if tail == 0 {
		tail = spec.DefaultTail
	}
	ctx, cancel := e.bounded(ctx)
	defer cancel()
	out, err := e.dockerCombined(ctx, "logs", "--tail", strconv.Itoa(tail), name)
	if err != nil {
		return "", err
	}
	if len(out) > spec.MaxLogs {
		out = out[len(out)-spec.MaxLogs:]
		if i := strings.IndexByte(out, '\n'); i >= 0 {
			out = out[i+1:] // start on a whole line
		}
	}
	return out, nil
}

// State reports the state of the runner's containers, all of them when name is empty (CA-06). A
// container that is not running carries the last line of its output, redacted of the values of its
// environment, so the owner sees why it stopped instead of only Docker's exit code.
func (e *Executor) State(ctx context.Context, name string) ([]spec.Container, error) {
	if name != "" {
		if err := validateContainerName(name); err != nil {
			return nil, err
		}
	}
	ctx, cancel := e.bounded(ctx)
	defer cancel()
	out, err := e.docker(ctx, "ps", "-a", "--filter", "label="+AgentLabel, "--format", psFormat)
	if err != nil {
		return nil, err
	}
	containers := parseContainers(out)
	for i := range containers {
		e.enrichLastLog(ctx, &containers[i])
	}
	if name == "" {
		return containers, nil
	}
	for _, c := range containers {
		if c.Name == name {
			return []spec.Container{c}, nil
		}
	}
	return nil, nil
}

// Watch reports container states until ctx ends: the containers as they are now, then every
// `docker events` entry for a container AI, with a re-list every PollInterval as a fallback and
// after the event stream ends (design, "Runner protocol").
func (e *Executor) Watch(ctx context.Context) <-chan []spec.Container {
	out := make(chan []spec.Container)
	go func() {
		defer close(out)
		for ctx.Err() == nil {
			if containers, err := e.State(ctx, ""); err == nil && !sendStates(ctx, out, containers) {
				return
			}
			if !e.streamEvents(ctx, out) {
				return
			}
			if !wait(ctx, e.opts.PollInterval) {
				return
			}
		}
	}()
	return out
}

// Message turns Docker's output into a message the protocol accepts: one line, at most
// spec.MaxMessage characters and no control characters, never empty (CA-08).
func Message(text string) string {
	var b strings.Builder
	space := false
	for _, r := range text {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	msg := b.String()
	if msg == "" {
		return "docker failed without saying why"
	}
	if runes := []rune(msg); len(runes) > spec.MaxMessage {
		msg = string(runes[:spec.MaxMessage])
	}
	return msg
}

// bounded adds the operation timeout when the caller set no deadline.
func (e *Executor) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, e.opts.OpTimeout)
}

// binary is the docker executable.
func (e *Executor) binary() string {
	if e.opts.Docker != "" {
		return e.opts.Docker
	}
	return "docker"
}

// runArgs builds the argument vector of `docker run` from the validated run description; no
// value of Env is part of it (CA-08, CA-09).
func (e *Executor) runArgs(r spec.RunSpec, envPath string) []string {
	args := []string{
		"run", "-d",
		"--name", r.Name,
		"--restart", "unless-stopped",
		"--env-file", envPath,
		"--cpus", strconv.FormatFloat(r.CPUs, 'f', -1, 64),
		"--memory", strconv.FormatInt(r.MemoryMB, 10) + "m",
		"-v", r.Volume + ":" + WorkspaceMount,
	}
	keys := make([]string, 0, len(r.Labels))
	for key := range r.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--label", key+"="+r.Labels[key])
	}
	return append(args, r.Image)
}

// containerOp runs one `docker <op> <name>` command.
func (e *Executor) containerOp(ctx context.Context, op, name string) error {
	if err := validateContainerName(name); err != nil {
		return err
	}
	ctx, cancel := e.bounded(ctx)
	defer cancel()
	_, err := e.docker(ctx, op, name)
	return err
}

// workspaceVolume reads the name of the volume mounted at WorkspaceMount, or "" when the
// container does not exist or has no such mount.
func (e *Executor) workspaceVolume(ctx context.Context, name string) string {
	ctx, cancel := e.bounded(ctx)
	defer cancel()
	out, err := e.docker(ctx, "inspect", "--format", inspectWorkspaceVolume, name)
	if err != nil {
		return ""
	}
	volume := strings.TrimSpace(out)
	if !spec.ValidVolumeName(volume) {
		return ""
	}
	return volume
}

// docker runs one docker command and returns its standard output.
func (e *Executor) docker(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, e.binary(), args...) // #nosec G204 -- an argument vector built from validated values, never a shell (CA-09)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), dockerFailure(args[0], err, stderr.String())
	}
	return string(out), nil
}

// dockerCombined runs one docker command and returns its standard output and error together.
func (e *Executor) dockerCombined(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, e.binary(), args...) // #nosec G204 -- an argument vector built from validated values, never a shell (CA-09)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), dockerFailure(args[0], err, string(out))
	}
	return string(out), nil
}

// dockerFailure turns a failed docker call into an error, with the hint that fixes the usual
// permission problem on the socket (CA-02).
func dockerFailure(op string, err error, stderr string) error {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return errors.New("docker is not installed: the docker command is not in PATH")
	}
	reason := Message(stderr)
	if reason == "" || strings.Contains(stderr, "exit status") {
		reason = Message(err.Error())
	}
	if strings.Contains(strings.ToLower(stderr), "permission denied") {
		return fmt.Errorf("docker %s: %s; no permission on the Docker socket: add your user to the docker group (sudo usermod -aG docker $USER, then log out and back in)", op, reason)
	}
	return fmt.Errorf("docker %s: %s", op, reason)
}

// streamEvents reads `docker events` until the stream ends or ctx ends, re-listing the
// containers on every tick of PollInterval. It reports false when ctx ended.
func (e *Executor) streamEvents(ctx context.Context, out chan<- []spec.Container) bool {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.binary(), "events", "--filter", "label="+AgentLabel, "--format", psFormat) // #nosec G204 -- an argument vector, never a shell (CA-09)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ctx.Err() == nil
	}
	if err := cmd.Start(); err != nil {
		return ctx.Err() == nil
	}
	lines := make(chan string)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	ticker := time.NewTicker(e.opts.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = cmd.Wait()
			return false
		case <-ticker.C:
			if containers, err := e.State(ctx, ""); err == nil && !sendStates(ctx, out, containers) {
				_ = cmd.Wait()
				return false
			}
		case line, ok := <-lines:
			if !ok {
				_ = cmd.Wait()
				return ctx.Err() == nil
			}
			if c, ok := parseEvent(line); ok {
				e.enrichLastLog(ctx, &c)
				if !sendStates(ctx, out, []spec.Container{c}) {
					_ = cmd.Wait()
					return false
				}
			}
		}
	}
}

// writeEnvFile writes the environment of one container to a private file (CA-08). Docker's env
// file has no escaping, so a value with a line break is refused instead of injecting another
// variable; the value never appears in the error.
func (e *Executor) writeEnvFile(name string, env map[string]string) (string, error) {
	if err := validateContainerName(name); err != nil {
		return "", err
	}
	if e.opts.StateDir == "" {
		return "", errors.New("no state directory for the env file: set HOME or XDG_STATE_HOME")
	}
	if err := os.MkdirAll(e.opts.StateDir, stateDirMode); err != nil {
		return "", fmt.Errorf("create the env directory: %w", err)
	}
	if err := os.Chmod(e.opts.StateDir, stateDirMode); err != nil {
		return "", fmt.Errorf("make the env directory private: %w", err)
	}
	names := make([]string, 0, len(env))
	for key := range env {
		names = append(names, key)
	}
	sort.Strings(names)
	var body strings.Builder
	for _, key := range names {
		value := env[key]
		if !spec.ValidEnvName(key) {
			return "", fmt.Errorf("environment variable name %q is not accepted", key)
		}
		if strings.ContainsAny(value, "\r\n") || strings.ContainsRune(value, 0) {
			return "", fmt.Errorf("environment variable %s cannot be written to the env file: line breaks and NUL are not allowed", key)
		}
		body.WriteString(key)
		body.WriteByte('=')
		body.WriteString(value)
		body.WriteByte('\n')
	}
	f, err := os.CreateTemp(e.opts.StateDir, "."+name+".env-*")
	if err != nil {
		return "", fmt.Errorf("create the env file: %w", err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // no-op after the rename
	if err := f.Chmod(envFileMode); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("make the env file private: %w", err)
	}
	if _, err := f.WriteString(body.String()); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write the env file: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("write the env file: %w", err)
	}
	path := filepath.Join(e.opts.StateDir, name+EnvFileSuffix)
	if err := os.Rename(tmp, path); err != nil {
		return "", fmt.Errorf("write the env file: %w", err)
	}
	return path, nil
}

// removeEnvFile deletes the env file of a container that no longer exists.
func (e *Executor) removeEnvFile(name string) {
	if e.opts.StateDir == "" || !spec.ValidContainerName(name) {
		return
	}
	_ = os.Remove(filepath.Join(e.opts.StateDir, name+EnvFileSuffix))
}

// validateContainerName refuses a name the protocol does not accept (CA-09).
func validateContainerName(name string) error {
	if !spec.ValidContainerName(name) {
		return fmt.Errorf("name: must match %s", spec.ContainerNamePattern)
	}
	return nil
}

// psRow is the part of one `docker ps --format '{{json .}}'` line the runner reads.
type psRow struct {
	Names  string `json:"Names"`
	State  string `json:"State"`
	Status string `json:"Status"`
}

// parseContainers reads the JSON lines of `docker ps` and maps Docker's states onto the four the
// protocol reports, skipping anything that is not a container AI.
func parseContainers(out string) []spec.Container {
	containers := make([]spec.Container, 0)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row psRow
		if json.Unmarshal([]byte(line), &row) != nil {
			continue
		}
		name := strings.TrimSpace(strings.SplitN(row.Names, ",", 2)[0])
		if !spec.ValidContainerName(name) {
			continue
		}
		containers = append(containers, containerState(name, row.State, row.Status))
		if len(containers) >= spec.MaxContainers {
			break
		}
	}
	return containers
}

// containerState maps Docker's container state onto the state Partyline stores (CA-06). Docker's
// restarting is a restart loop, not a running container: the app inside is not serving anything.
func containerState(name, dockerState, status string) spec.Container {
	switch strings.ToLower(strings.TrimSpace(dockerState)) {
	case "running":
		return spec.Container{Name: name, State: spec.StateRunning}
	case "restarting":
		return spec.Container{Name: name, State: spec.StateRestarting}
	case "exited":
		if m := exitStatusPattern.FindStringSubmatch(strings.TrimSpace(status)); m != nil && m[1] != "0" {
			return spec.Container{Name: name, State: spec.StateError, Error: "the container exited with code " + m[1]}
		}
		return spec.Container{Name: name, State: spec.StateStopped}
	case "created", "paused", "removing":
		return spec.Container{Name: name, State: spec.StateStopped}
	case "dead":
		return spec.Container{Name: name, State: spec.StateError, Error: Message(status)}
	default:
		return spec.Container{Name: name, State: spec.StateUnknown}
	}
}

// enrichLastLog attaches the last non-empty line of the container's output to a container that is
// not running, with every environment value redacted (CA-06, CA-08). The line is read only when the
// runner still has the container's env file: without the values, a log line could not be redacted.
func (e *Executor) enrichLastLog(ctx context.Context, c *spec.Container) {
	if c.State != spec.StateError && c.State != spec.StateRestarting {
		return
	}
	env, ok := e.readEnvFile(c.Name)
	if !ok {
		return
	}
	out, err := e.dockerCombined(ctx, "logs", "--tail", strconv.Itoa(lastLogTail), c.Name)
	if err != nil {
		return
	}
	c.LastLog = lastLogLine(out, env)
}

// lastLogLine is the last non-empty line of a container's output, on one line, at most
// spec.MaxMessage characters, with every environment value replaced by [redacted] (CA-08).
func lastLogLine(out string, env map[string]string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		line = strings.Join(strings.Fields(redact(line, env)), " ")
		if line == "" {
			continue
		}
		if runes := []rune(line); len(runes) > spec.MaxMessage {
			line = string(runes[:spec.MaxMessage])
		}
		return line
	}
	return ""
}

// readEnvFile reads the values of a container's env file for redaction; false when it is missing,
// so no line is reported rather than one that may carry a value.
func (e *Executor) readEnvFile(name string) (map[string]string, bool) {
	if e.opts.StateDir == "" || !spec.ValidContainerName(name) {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(e.opts.StateDir, name+EnvFileSuffix))
	if err != nil {
		return nil, false
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && spec.ValidEnvName(key) && value != "" {
			env[key] = value
		}
	}
	return env, true
}

// dockerEvent is the part of one `docker events --format '{{json .}}'` line the runner reads.
type dockerEvent struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Actor  struct {
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
}

// parseEvent turns one Docker event into the state it reports, for a container AI only.
func parseEvent(line string) (spec.Container, bool) {
	var event dockerEvent
	if json.Unmarshal([]byte(line), &event) != nil || event.Type != "container" {
		return spec.Container{}, false
	}
	name := event.Actor.Attributes["name"]
	if !spec.ValidContainerName(name) {
		return spec.Container{}, false
	}
	switch strings.TrimSpace(event.Action) {
	case "start", "restart", "unpause":
		return spec.Container{Name: name, State: spec.StateRunning}, true
	case "stop", "kill", "pause", "destroy":
		return spec.Container{Name: name, State: spec.StateStopped}, true
	case "die":
		if code := event.Actor.Attributes["exitCode"]; code != "" && code != "0" {
			return spec.Container{Name: name, State: spec.StateError, Error: "the container exited with code " + Message(code)}, true
		}
		return spec.Container{Name: name, State: spec.StateStopped}, true
	case "oom":
		return spec.Container{Name: name, State: spec.StateError, Error: "the container ran out of memory"}, true
	case "health_status: unhealthy":
		return spec.Container{Name: name, State: spec.StateError, Error: "the container is unhealthy"}, true
	case "health_status: healthy":
		return spec.Container{Name: name, State: spec.StateRunning}, true
	}
	return spec.Container{}, false
}

// sendStates hands one state report to the reader; false when ctx ended first.
func sendStates(ctx context.Context, out chan<- []spec.Container, containers []spec.Container) bool {
	select {
	case out <- containers:
		return true
	case <-ctx.Done():
		return false
	}
}

// wait sleeps d and reports false when ctx ended first.
func wait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// redact replaces every occurrence of an environment value in msg, longest value first, so a
// Docker error that quotes one never leaves this machine (CA-08).
func redact(msg string, env map[string]string) string {
	values := make([]string, 0, len(env))
	for _, value := range env {
		if value != "" {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, value := range values {
		msg = strings.ReplaceAll(msg, value, "[redacted]")
	}
	return msg
}
