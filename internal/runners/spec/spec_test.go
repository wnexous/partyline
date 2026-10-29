package spec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// CA-09: names, images, volumes, environment variables, limits, URLs and refs have strict
// patterns, and the server and the runner apply the same ones.
func TestValidators(t *testing.T) {
	long60 := strings.Repeat("a", 60)
	tests := []struct {
		name string
		ok   bool
		fn   func() bool
	}{
		{"container name", true, func() bool { return ValidContainerName("pl-atlas") }},
		{"container name with digits", true, func() bool { return ValidContainerName("pl-a1-b2") }},
		{"container name 60 characters", true, func() bool { return ValidContainerName("pl-" + long60) }},
		{"container name without prefix", false, func() bool { return ValidContainerName("atlas") }},
		{"container name upper case", false, func() bool { return ValidContainerName("pl-Atlas") }},
		{"container name underscore", false, func() bool { return ValidContainerName("pl-a_b") }},
		{"container name empty suffix", false, func() bool { return ValidContainerName("pl-") }},
		{"container name 61 characters", false, func() bool { return ValidContainerName("pl-" + long60 + "a") }},
		{"container name with slash", false, func() bool { return ValidContainerName("pl-a/b") }},

		{"volume name", true, func() bool { return ValidVolumeName("pl-atlas-workspace") }},
		{"volume name empty", false, func() bool { return ValidVolumeName("") }},
		{"volume name slash", false, func() bool { return ValidVolumeName("pl/a") }},

		{"env name", true, func() bool { return ValidEnvName("OPENCODE_API_KEY") }},
		{"env name leading digit", false, func() bool { return ValidEnvName("1KEY") }},
		{"env name lower case", false, func() bool { return ValidEnvName("key") }},
		{"env name 65 characters", false, func() bool { return ValidEnvName("A" + strings.Repeat("B", 64)) }},

		{"default image", true, func() bool { return ValidImage(DefaultImageRepo+":1.2.3", nil) }},
		{"default image latest", true, func() bool { return ValidImage(DefaultImageRepo+":latest", nil) }},
		{"allowed image", true, func() bool {
			return ValidImage("registry.example.com:5000/team/agent:v1", []string{"registry.example.com:5000/team/agent"})
		}},
		{"foreign image", false, func() bool { return ValidImage("docker.io/library/ubuntu:22.04", nil) }},
		{"foreign image with allowlist", false, func() bool {
			return ValidImage("docker.io/library/ubuntu:22.04", []string{"registry.example.com/team/agent"})
		}},
		{"image without tag", false, func() bool { return ValidImage(DefaultImageRepo, nil) }},
		{"image with a digest", false, func() bool { return ValidImage(DefaultImageRepo+"@sha256:"+long60, nil) }},
		{"image tag with a space", false, func() bool { return ValidImage(DefaultImageRepo+":a b", nil) }},
		{"image allowlist of a bad repo", false, func() bool { return ValidImageRepo("Registry/Team") }},

		{"cpus minimum", true, func() bool { return ValidCPUs(0.1) }},
		{"cpus maximum", true, func() bool { return ValidCPUs(64) }},
		{"cpus below", false, func() bool { return ValidCPUs(0.09) }},
		{"cpus above", false, func() bool { return ValidCPUs(64.5) }},
		{"memory minimum", true, func() bool { return ValidMemoryMB(64) }},
		{"memory maximum", true, func() bool { return ValidMemoryMB(65536) }},
		{"memory below", false, func() bool { return ValidMemoryMB(63) }},
		{"memory above", false, func() bool { return ValidMemoryMB(65537) }},

		{"https repo", true, func() bool { return ValidRepoURL("https://github.com/wnexous/partyline.git") }},
		{"https repo with port", true, func() bool { return ValidRepoURL("https://git.example.com:8443/team/repo") }},
		{"ssh repo", true, func() bool { return ValidRepoURL("ssh://git@github.com/wnexous/partyline.git") }},
		{"scp repo", true, func() bool { return ValidRepoURL("git@github.com:wnexous/partyline.git") }},
		{"http repo", false, func() bool { return ValidRepoURL("http://github.com/wnexous/partyline") }},
		{"https repo with userinfo", false, func() bool { return ValidRepoURL("https://user:pass@github.com/wnexous/partyline") }},
		{"git protocol repo", false, func() bool { return ValidRepoURL("git://github.com/wnexous/partyline") }},
		{"file repo", false, func() bool { return ValidRepoURL("file:///srv/repo") }},
		{"repo with a parent segment", false, func() bool { return ValidRepoURL("https://github.com/wnexous/../repo") }},
		{"empty repo", false, func() bool { return ValidRepoURL("") }},

		{"ref main", true, func() bool { return ValidRef("main") }},
		{"ref branch", true, func() bool { return ValidRef("feature/PS-1") }},
		{"ref tag", true, func() bool { return ValidRef("v1.2.3") }},
		{"ref commit", true, func() bool { return ValidRef(strings.Repeat("a", 40)) }},
		{"ref leading dash", false, func() bool { return ValidRef("-main") }},
		{"ref parent", false, func() bool { return ValidRef("../main") }},
		{"ref double dot", false, func() bool { return ValidRef("main..x") }},
		{"ref reflog", false, func() bool { return ValidRef("main@{1}") }},
		{"ref trailing slash", false, func() bool { return ValidRef("main/") }},
		{"ref double slash", false, func() bool { return ValidRef("main//x") }},
		{"ref lock", false, func() bool { return ValidRef("main.lock") }},
		{"ref empty", false, func() bool { return ValidRef("") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.ok, tc.fn())
		})
	}
}

// CA-02: a hello says whether Docker works, through the version or the reason it does not.
func TestValidateHello(t *testing.T) {
	valid := Hello{Type: FrameHello, DockerVersion: "27.3.1", Containers: []Container{{Name: "pl-atlas", State: StateRunning}}}
	require.NoError(t, ValidateHello(valid))

	withError := Hello{Type: FrameHello, DockerError: "docker socket not found"}
	require.NoError(t, ValidateHello(withError))

	for name, tc := range map[string]struct {
		hello Hello
		field string
	}{
		"no type":       {Hello{DockerVersion: "27.3.1"}, "type"},
		"neither":       {Hello{Type: FrameHello}, "docker_version"},
		"both":          {Hello{Type: FrameHello, DockerVersion: "27.3.1", DockerError: "down"}, "docker_error"},
		"bad version":   {Hello{Type: FrameHello, DockerVersion: "v27"}, "docker_version"},
		"bad error":     {Hello{Type: FrameHello, DockerError: "bad\x00error"}, "docker_error"},
		"bad container": {Hello{Type: FrameHello, DockerVersion: "27.3.1", Containers: []Container{{Name: "atlas", State: StateRunning}}}, "containers"},
		"bad state":     {Hello{Type: FrameHello, DockerVersion: "27.3.1", Containers: []Container{{Name: "pl-atlas", State: "exited"}}}, "containers"},
		"duplicate container": {Hello{Type: FrameHello, DockerVersion: "27.3.1", Containers: []Container{
			{Name: "pl-atlas", State: StateRunning}, {Name: "pl-atlas", State: StateStopped},
		}}, "containers"},
		"too many containers": {Hello{Type: FrameHello, DockerVersion: "27.3.1", Containers: manyContainers(MaxContainers + 1)}, "containers"},
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateHello(tc.hello)
			require.Error(t, err)
			var verr *ValidationError
			require.ErrorAs(t, err, &verr)
			require.Contains(t, verr.Fields, tc.field)
		})
	}
}

func manyContainers(n int) []Container {
	out := make([]Container, n)
	for i := range out {
		out[i] = Container{Name: "pl-c" + strings.Repeat("x", i%10) + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)), State: StateRunning}
	}
	return out
}

// CA-09: a run carries only validated values, and an environment variable value never reaches an
// error message (CA-08).
func TestValidateRun(t *testing.T) {
	valid := RunSpec{
		Name: "pl-atlas", Image: DefaultImageRepo + ":1", Volume: "pl-atlas-workspace",
		CPUs: 1.5, MemoryMB: 2048,
		Labels: map[string]string{"partyline.agent": "a1"},
		Env:    map[string]string{"PARTYLINE_TOKEN": "pl_agent_x"},
	}
	require.NoError(t, ValidateRun(valid, nil))

	secret := "super-secret-value"
	bad := valid
	bad.Env = map[string]string{"PARTYLINE_TOKEN": secret + "\x00"}
	err := ValidateRun(bad, nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), secret, "a value is a secret: it never appears in an error")

	for name, mutate := range map[string]func(*RunSpec){
		"name":      func(r *RunSpec) { r.Name = "atlas" },
		"image":     func(r *RunSpec) { r.Image = "docker.io/library/ubuntu:22.04" },
		"volume":    func(r *RunSpec) { r.Volume = "../volume" },
		"cpus":      func(r *RunSpec) { r.CPUs = 0 },
		"memory":    func(r *RunSpec) { r.MemoryMB = 1024 * 1024 },
		"env name":  func(r *RunSpec) { r.Env = map[string]string{"lower": "x"} },
		"env value": func(r *RunSpec) { r.Env = map[string]string{"OK": strings.Repeat("x", MaxEnvValue+1)} },
		"env count": func(r *RunSpec) {
			r.Env = make(map[string]string, MaxEnvVars+1)
			for i := 0; i <= MaxEnvVars; i++ {
				r.Env["V"+string(rune('A'+i%26))+string(rune('A'+(i/26)%26))] = "x"
			}
		},
		"label key": func(r *RunSpec) { r.Labels = map[string]string{"Bad Key": "x"} },
		"label":     func(r *RunSpec) { r.Labels = map[string]string{"partyline.agent": ""} },
	} {
		t.Run(name, func(t *testing.T) {
			r := valid
			mutate(&r)
			require.Error(t, ValidateRun(r, nil))
		})
	}
}

// CA-09: the server and the runner reject a command outside the protocol.
func TestValidateCommand(t *testing.T) {
	run := RunSpec{Name: "pl-atlas", Image: DefaultImageRepo + ":1", Volume: "pl-atlas-workspace", CPUs: 1, MemoryMB: 512}
	valid := []Command{
		{ID: "c1", Op: OpRun, Run: &run},
		{ID: "c2", Op: OpStart, Name: "pl-atlas"},
		{ID: "c3", Op: OpStop, Name: "pl-atlas"},
		{ID: "c4", Op: OpRestart, Name: "pl-atlas"},
		{ID: "c5", Op: OpRemove, Name: "pl-atlas", RemoveVolume: true},
		{ID: "c6", Op: OpLogs, Name: "pl-atlas", Tail: 200},
		{ID: "c7", Op: OpState},
		{ID: "c8", Op: OpState, Name: "pl-atlas"},
	}
	for _, cmd := range valid {
		t.Run(string(cmd.Op)+cmd.ID, func(t *testing.T) { require.NoError(t, ValidateCommand(cmd, nil)) })
	}

	for name, cmd := range map[string]Command{
		"no id":               {Op: OpState},
		"bad id":              {ID: "a b", Op: OpState},
		"unknown op":          {ID: "c1", Op: "exec"},
		"run without spec":    {ID: "c1", Op: OpRun},
		"run with name":       {ID: "c1", Op: OpRun, Run: &run, Name: "pl-atlas"},
		"start without name":  {ID: "c1", Op: OpStart},
		"start with run":      {ID: "c1", Op: OpStart, Name: "pl-atlas", Run: &run},
		"logs tail too big":   {ID: "c1", Op: OpLogs, Name: "pl-atlas", Tail: MaxTail + 1},
		"tail on start":       {ID: "c1", Op: OpStart, Name: "pl-atlas", Tail: 1},
		"volume on start":     {ID: "c1", Op: OpStart, Name: "pl-atlas", RemoveVolume: true},
		"bad run spec":        {ID: "c1", Op: OpRun, Run: &RunSpec{Name: "atlas"}},
		"state with bad name": {ID: "c1", Op: OpState, Name: "atlas"},
	} {
		t.Run(name, func(t *testing.T) { require.Error(t, ValidateCommand(cmd, nil)) })
	}
}

// A result proves the command was answered, and a failure always says why.
func TestValidateResultAndState(t *testing.T) {
	require.NoError(t, ValidateResult(Result{Type: FrameResult, ID: "c1", OK: true}))
	require.NoError(t, ValidateResult(Result{Type: FrameResult, ID: "c2", OK: true, Logs: "line\n"}))
	require.NoError(t, ValidateResult(Result{Type: FrameResult, ID: "c3", OK: true, Containers: []Container{{Name: "pl-a", State: StateRunning}}}))
	require.NoError(t, ValidateResult(Result{Type: FrameResult, ID: "c4", Error: "docker failed"}))

	require.Error(t, ValidateResult(Result{Type: FrameResult, ID: "c1"}))                         // ok false without error
	require.Error(t, ValidateResult(Result{Type: FrameResult, ID: "c1", OK: true, Error: "why"})) // ok true with error
	require.Error(t, ValidateResult(Result{Type: FrameResult, ID: "c1", OK: true, Logs: strings.Repeat("x", MaxLogs+1)}))
	require.Error(t, ValidateResult(Result{ID: "c1", OK: true}))                     // no type
	require.Error(t, ValidateResult(Result{Type: FrameResult, ID: "a b", OK: true})) // bad id

	require.NoError(t, ValidateStateEvent(StateEvent{Type: FrameState, Containers: []Container{{Name: "pl-a", State: StateStopped}}}))
	require.Error(t, ValidateStateEvent(StateEvent{Type: FrameResult}))
	require.Error(t, ValidateStateEvent(StateEvent{Type: FrameState, Containers: []Container{{Name: "pl-a", State: "exited"}}}))
}
