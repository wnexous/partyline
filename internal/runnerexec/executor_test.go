package runnerexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/runners/spec"
)

// fakeDockerScript is a docker stand-in that records each call's argument vector and, for `run`,
// the contents of the env file it was given. Tests read the record to prove no secret travels on
// the command line; it never runs a real container.
const fakeDockerScript = `#!/bin/sh
log="${DOCKER_LOG:?}"
printf '%s\n' "$*" >> "$log"
if [ -n "${DOCKER_ERROR:-}" ]; then
  printf '%s\n' "$DOCKER_ERROR" >&2
  exit 1
fi
case "$1" in
version) printf '%s\n' "${DOCKER_VERSION:-27.3.1}" ;;
ps) [ -n "${DOCKER_PS:-}" ] && cat "$DOCKER_PS" ;;
events)
  [ -n "${DOCKER_EVENTS:-}" ] && cat "$DOCKER_EVENTS"
  [ -n "${DOCKER_EVENTS_SLEEP:-}" ] && sleep "$DOCKER_EVENTS_SLEEP"
  ;;
inspect) printf '%s\n' "${DOCKER_VOLUME:-}" ;;
logs) [ -n "${DOCKER_LOGS:-}" ] && cat "$DOCKER_LOGS" ;;
run)
  envfile=""
  prev=""
  for a in "$@"; do
    [ "$prev" = "--env-file" ] && envfile="$a"
    prev="$a"
  done
  if [ -n "$envfile" ]; then
    printf 'envfile %s\n' "$envfile" >> "$log"
    while IFS= read -r line; do printf 'env %s\n' "$line" >> "$log"; done < "$envfile"
  fi
  if [ -n "${DOCKER_RUN_ERROR:-}" ]; then printf '%s\n' "$DOCKER_RUN_ERROR" >&2; exit 1; fi
  ;;
esac
exit 0
`

// syntheticSecret is the fake token every test uses; it is never a real credential.
const syntheticSecret = "pl_agent_synthetic0000secret"

// fakeDocker puts the docker stand-in on PATH and returns the log file it records into.
func fakeDocker(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "docker")
	require.NoError(t, os.WriteFile(script, []byte(fakeDockerScript), 0o755))
	log := filepath.Join(dir, "calls.log")
	t.Setenv("DOCKER_LOG", log)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// calls returns the recorded docker argument vectors (and env file contents).
func calls(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

// firstCall returns the first recorded line that starts with prefix, or "".
func firstCall(t *testing.T, log, prefix string) string {
	t.Helper()
	for _, line := range calls(t, log) {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

func runSpec(name string) spec.RunSpec {
	return spec.RunSpec{
		Name: name, Image: spec.DefaultImageRepo + ":1", Volume: name + "-workspace",
		CPUs: 1.5, MemoryMB: 1024,
		Labels: map[string]string{"partyline.agent": "agent-1"},
		Env:    map[string]string{"PARTYLINE_TOKEN": syntheticSecret, "PARTYLINE_URL": "https://partyline.example.com"},
	}
}

// CA-05, CA-08, CA-09: run writes the environment to a 0600 file in a 0700 directory and passes it
// with --env-file; the argument vector carries no secret and no shell.
func TestRunWritesPrivateEnvFile(t *testing.T) {
	log := fakeDocker(t)
	stateDir := filepath.Join(t.TempDir(), "state", "partyline", "agents")
	executor := New(Options{StateDir: stateDir})

	require.NoError(t, executor.Run(context.Background(), runSpec("pl-atlas")))

	envPath := filepath.Join(stateDir, "pl-atlas.env")
	fi, err := os.Stat(envPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "the env file is private")
	di, err := os.Stat(stateDir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), di.Mode().Perm(), "the env directory is private")
	body, err := os.ReadFile(envPath)
	require.NoError(t, err)
	require.Equal(t, "PARTYLINE_TOKEN="+syntheticSecret+"\nPARTYLINE_URL=https://partyline.example.com\n", string(body))

	run := firstCall(t, log, "run ")
	require.Equal(t,
		"run -d --name pl-atlas --restart unless-stopped --env-file "+envPath+
			" --cpus 1.5 --memory 1024m -v pl-atlas-workspace:/workspace --label partyline.agent=agent-1 "+
			spec.DefaultImageRepo+":1",
		run, "the argument vector of docker run")
	require.NotContains(t, run, syntheticSecret, "no secret on the command line")
	require.NotContains(t, run, "PARTYLINE_TOKEN", "the env file, not a flag, carries the environment")
	require.Contains(t, strings.Join(calls(t, log), "\n"), "envfile "+envPath, "the fake docker was handed the env file")
}

// CA-09: the runner validates every value again, refuses what the server should never send and
// runs no docker at all.
func TestRunRefusesInvalidValues(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*spec.RunSpec)
	}{
		{name: "container name", mutate: func(r *spec.RunSpec) { r.Name = "../../etc/passwd" }},
		{name: "image not allowed", mutate: func(r *spec.RunSpec) { r.Image = "evil.example.com/agent:1" }},
		{name: "volume name", mutate: func(r *spec.RunSpec) { r.Volume = "-v" }},
		{name: "cpus", mutate: func(r *spec.RunSpec) { r.CPUs = 100 }},
		{name: "memory", mutate: func(r *spec.RunSpec) { r.MemoryMB = 1 }},
		{name: "env name", mutate: func(r *spec.RunSpec) { r.Env["bad-name"] = "value" }},
		{name: "env value with a line break", mutate: func(r *spec.RunSpec) { r.Env["PARTYLINE_TOKEN"] = syntheticSecret + "\nDOCKER_HOST=tcp://evil" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := fakeDocker(t)
			executor := New(Options{StateDir: t.TempDir()})
			r := runSpec("pl-atlas")
			tc.mutate(&r)
			err := executor.Run(context.Background(), r)
			require.Error(t, err)
			require.Empty(t, calls(t, log), "no docker call for a refused run")
			require.NotContains(t, err.Error(), syntheticSecret, "an error never quotes an env value (CA-08)")
			require.NotContains(t, err.Error(), "DOCKER_HOST", "an error never quotes an env value (CA-08)")
		})
	}
}

// CA-08: an error Docker returns is redacted of every environment value before it leaves this
// machine, and the failed run leaves no env file behind.
func TestRunRedactsEnvValuesFromErrors(t *testing.T) {
	log := fakeDocker(t)
	stateDir := t.TempDir()
	t.Setenv("DOCKER_RUN_ERROR", "failed to start with token "+syntheticSecret)
	executor := New(Options{StateDir: stateDir})

	err := executor.Run(context.Background(), runSpec("pl-atlas"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "[redacted]")
	require.NotContains(t, err.Error(), syntheticSecret)
	require.NotEmpty(t, firstCall(t, log, "run "), "docker ran before failing")
	_, statErr := os.Stat(filepath.Join(stateDir, "pl-atlas.env"))
	require.ErrorIs(t, statErr, os.ErrNotExist, "the env file of a failed run is gone")
}

// CA-06: remove takes the container, asks Docker for the workspace volume and removes it when
// asked to, and the env file goes away with the container.
func TestRemoveContainerAndVolume(t *testing.T) {
	log := fakeDocker(t)
	stateDir := t.TempDir()
	t.Setenv("DOCKER_VOLUME", "pl-atlas-workspace")
	executor := New(Options{StateDir: stateDir})
	require.NoError(t, executor.Run(context.Background(), runSpec("pl-atlas")))
	require.FileExists(t, filepath.Join(stateDir, "pl-atlas.env"))

	require.NoError(t, executor.Remove(context.Background(), "pl-atlas", false))
	require.Contains(t, calls(t, log), "rm -f pl-atlas")
	require.NotContains(t, strings.Join(calls(t, log), "\n"), "volume rm")
	_, err := os.Stat(filepath.Join(stateDir, "pl-atlas.env"))
	require.ErrorIs(t, err, os.ErrNotExist, "the env file is removed with the container")

	require.NoError(t, executor.Remove(context.Background(), "pl-atlas", true))
	all := strings.Join(calls(t, log), "\n")
	require.Contains(t, all, `inspect --format {{range .Mounts}}{{if eq .Destination "/workspace"}}{{.Name}}{{end}}{{end}} pl-atlas`)
	require.Contains(t, all, "volume rm pl-atlas-workspace")
}

// CA-06: logs read the last tail lines (200 by default, at most MaxTail).
func TestLogsTail(t *testing.T) {
	log := fakeDocker(t)
	executor := New(Options{StateDir: t.TempDir()})

	logs, err := executor.Logs(context.Background(), "pl-atlas", 0)
	require.NoError(t, err)
	require.Equal(t, "", logs)
	require.Contains(t, calls(t, log), "logs --tail 200 pl-atlas")

	_, err = executor.Logs(context.Background(), "pl-atlas", 25)
	require.NoError(t, err)
	require.Contains(t, calls(t, log), "logs --tail 25 pl-atlas")

	_, err = executor.Logs(context.Background(), "pl-atlas", spec.MaxTail+1)
	require.Error(t, err)
	require.NotContains(t, calls(t, log), "logs --tail 1001 pl-atlas")
}

// CA-06: state maps Docker's states onto running, stopped, error and unknown and ignores a
// container that is not a container AI.
func TestStateMapsDockerStates(t *testing.T) {
	log := fakeDocker(t)
	rows := strings.Join([]string{
		`{"Names":"pl-atlas","State":"running","Status":"Up 2 minutes"}`,
		`{"Names":"pl-iris","State":"exited","Status":"Exited (0) 5 minutes ago"}`,
		`{"Names":"pl-juno","State":"exited","Status":"Exited (137) 1 minute ago"}`,
		`{"Names":"pl-kilo","State":"created","Status":"Created"}`,
		`{"Names":"pl-leo","State":"dead","Status":"Dead"}`,
		`{"Names":"other","State":"running","Status":"Up"}`,
	}, "\n") + "\n"
	path := filepath.Join(t.TempDir(), "ps.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(rows), 0o600))
	t.Setenv("DOCKER_PS", path)
	executor := New(Options{StateDir: t.TempDir()})

	containers, err := executor.State(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, []spec.Container{
		{Name: "pl-atlas", State: spec.StateRunning},
		{Name: "pl-iris", State: spec.StateStopped},
		{Name: "pl-juno", State: spec.StateError, Error: "the container exited with code 137"},
		{Name: "pl-kilo", State: spec.StateStopped},
		{Name: "pl-leo", State: spec.StateError, Error: "Dead"},
	}, containers)
	require.Contains(t, calls(t, log), "ps -a --filter label=partyline.agent --format {{json .}}")

	one, err := executor.State(context.Background(), "pl-atlas")
	require.NoError(t, err)
	require.Equal(t, []spec.Container{{Name: "pl-atlas", State: spec.StateRunning}}, one)
}

// CA-02, CA-09: a bad name is refused before Docker sees it.
func TestStateRefusesAnInvalidName(t *testing.T) {
	log := fakeDocker(t)
	executor := New(Options{StateDir: t.TempDir()})
	_, err := executor.State(context.Background(), "pl-Atlas")
	require.Error(t, err)
	require.Empty(t, calls(t, log))
}

// CA-06: Watch reports the containers now and the states of `docker events`, with the periodic
// re-list as the fallback.
func TestWatchReportsEventsAndPolls(t *testing.T) {
	fakeDocker(t)
	dir := t.TempDir()
	ps := filepath.Join(dir, "ps.jsonl")
	require.NoError(t, os.WriteFile(ps, []byte(`{"Names":"pl-atlas","State":"running","Status":"Up"}`+"\n"), 0o600))
	events := filepath.Join(dir, "events.jsonl")
	require.NoError(t, os.WriteFile(events, []byte(strings.Join([]string{
		`{"Type":"container","Action":"start","Actor":{"Attributes":{"name":"pl-atlas"}}}`,
		`{"Type":"container","Action":"die","Actor":{"Attributes":{"name":"pl-atlas","exitCode":"137"}}}`,
		`{"Type":"container","Action":"start","Actor":{"Attributes":{"name":"not-a-container-ai"}}}`,
	}, "\n")+"\n"), 0o600))
	t.Setenv("DOCKER_PS", ps)
	t.Setenv("DOCKER_EVENTS", events)
	t.Setenv("DOCKER_EVENTS_SLEEP", "0.05")
	executor := New(Options{StateDir: t.TempDir(), PollInterval: 20 * time.Millisecond})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var sawRunning, sawError, sawPoll bool
	for report := range executor.Watch(ctx) {
		for _, c := range report {
			switch {
			case c.Name == "pl-atlas" && c.State == spec.StateRunning:
				sawRunning = true
				sawPoll = true
			case c.Name == "pl-atlas" && c.State == spec.StateError:
				sawError = true
				require.Contains(t, c.Error, "137", "the die event carries the exit code")
			case c.Name == "not-a-container-ai":
				t.Fatalf("a container outside the protocol was reported: %+v", c)
			}
		}
		if sawRunning && sawError && sawPoll {
			break
		}
	}
	require.True(t, sawRunning, "the exit code 1 die event and the running container were reported")
	require.True(t, sawError)
}

// CA-02: DockerVersion reports the version, and says why Docker is unusable otherwise, with the
// hint that fixes the usual socket permission problem.
func TestDockerVersion(t *testing.T) {
	t.Run("version", func(t *testing.T) {
		fakeDocker(t)
		version, err := New(Options{}).DockerVersion(context.Background())
		require.NoError(t, err)
		require.Equal(t, "27.3.1", version)
	})
	t.Run("not installed", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "no-docker-here")
		_, err := New(Options{Docker: missing}).DockerVersion(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "not installed")
	})
	t.Run("permission denied", func(t *testing.T) {
		fakeDocker(t)
		t.Setenv("DOCKER_ERROR", "permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock")
		_, err := New(Options{}).DockerVersion(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "usermod -aG docker")
	})
	t.Run("unexpected answer", func(t *testing.T) {
		fakeDocker(t)
		t.Setenv("DOCKER_VERSION", "not a version")
		_, err := New(Options{}).DockerVersion(context.Background())
		require.Error(t, err)
	})
}

// CA-08, CA-09: Message produces what the protocol accepts (one line, bounded, never empty) and
// redact hides every env value, longer values first.
func TestMessageAndRedact(t *testing.T) {
	require.Equal(t, "a b c", Message("  a\n\tb\r\nc  "))
	require.Equal(t, "docker failed without saying why", Message("  \n "))
	long := Message(strings.Repeat("x", spec.MaxMessage+50))
	require.Equal(t, spec.MaxMessage, len([]rune(long)))

	env := map[string]string{"SHORT": "abc", "LONG": "abcdef"}
	require.Equal(t, "x [redacted] y", redact("x abcdef y", env))
	require.Equal(t, "[redacted][redacted]", redact("abcdefabc", env))
}
