package containeragents

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/runners/spec"
	"github.com/wnexous/partyline/internal/store/db"
)

// CA-04: a release build runs the agent image of its own version; a development build runs :latest,
// because no image carries a describe version.
func TestImageFor(t *testing.T) {
	for version, want := range map[string]string{
		"v1.2.3":           "ghcr.io/wnexous/partyline-agent:1.2.3",
		"1.2.3":            "ghcr.io/wnexous/partyline-agent:1.2.3",
		"v1.2.3-rc.1":      "ghcr.io/wnexous/partyline-agent:1.2.3-rc.1",
		"dev":              "ghcr.io/wnexous/partyline-agent:latest",
		"":                 "ghcr.io/wnexous/partyline-agent:latest",
		"v1.2.3-5-gabcdef": "ghcr.io/wnexous/partyline-agent:latest",
		"v1.2.3-dirty":     "ghcr.io/wnexous/partyline-agent:latest",
	} {
		require.Equal(t, want, ImageFor(version), version)
	}
}

// CA-09: the container and volume names are names the runner protocol accepts.
func TestContainerNamesAreValid(t *testing.T) {
	const id = "01a18952-3fbb-7b1a-b247-a8062a7ff40e"
	name, volume := ContainerName(id), VolumeName(id)
	require.Equal(t, "pl-a8062a7ff40e", name)
	require.Equal(t, "pl-a8062a7ff40e-ws", volume)
	require.True(t, spec.ValidContainerName(name))
	require.True(t, spec.ValidVolumeName(volume))
	require.Equal(t, "pl-a8062a7ff40e", ContainerName("01a189523fbb7b1ab247a8062a7ff40e")) // dashes are removed
}

// CA-04, CA-08: a create is validated field by field, the defaults are filled in, and no error
// message carries a value.
func TestValidateCreate(t *testing.T) {
	in, err := validateCreate(CreateInput{
		Harness: "Claude", DisplayName: " Atlas ", Handle: "Atlas",
		Env: []EnvVar{{Var: "ANTHROPIC_API_KEY", SecretName: "PROVIDER_KEY"}},
	})
	require.NoError(t, err)
	require.Equal(t, "claude", in.Harness)
	require.Equal(t, "Atlas", in.DisplayName)
	require.Equal(t, "atlas", in.Handle, "handles are lower case")
	require.Equal(t, float64(DefaultCPUs), in.CPUs)
	require.EqualValues(t, DefaultMemoryMB, in.MemoryMB)
	require.Empty(t, in.RepoRef)

	in, err = validateCreate(CreateInput{
		Harness: "pi", DisplayName: "Nova", Handle: "nova", RepoURL: "https://github.com/example/repo",
		Env: []EnvVar{{Var: "OPENCODE_API_KEY", SecretName: "OPENCODE_KEY"}},
	})
	require.NoError(t, err)
	require.Equal(t, DefaultRepoRef, in.RepoRef)
	require.Equal(t, "OPENCODE_API_KEY", in.Env[0].Var)

	bad := []struct {
		name  string
		edit  func(*CreateInput)
		field string
	}{
		{"harness", func(in *CreateInput) { in.Harness = "codex" }, "harness"},
		{"handle", func(in *CreateInput) { in.Handle = "No Space" }, "handle"},
		{"display", func(in *CreateInput) { in.DisplayName = "  " }, "display_name"},
		{"repo", func(in *CreateInput) { in.RepoURL = "http://example.com/repo" }, "repo_url"},
		{"ref", func(in *CreateInput) { in.RepoURL = "https://example.com/repo"; in.RepoRef = ".." }, "repo_ref"},
		{"cpus", func(in *CreateInput) { in.CPUs = 100 }, "cpus"},
		{"memory", func(in *CreateInput) { in.MemoryMB = 1 }, "memory_mb"},
		{"env var", func(in *CreateInput) {
			in.Env = []EnvVar{{Var: "OPENCODE_API_KEY", SecretName: "OPENCODE_KEY"}, {Var: "OPENCODE_API_KEY", SecretName: "X"}}
		}, "env[1].var"},
		{"reserved var", func(in *CreateInput) { in.Env = []EnvVar{{Var: "PARTYLINE_HARNESS", SecretName: "X"}} }, "env[0].var"},
		{"the AI's machine", func(in *CreateInput) { in.Env = []EnvVar{{Var: "PARTYLINE_MACHINE", SecretName: "X"}} }, "env[0].var"},
		{"the AI's folder", func(in *CreateInput) { in.Env = []EnvVar{{Var: "PARTYLINE_FOLDER", SecretName: "X"}} }, "env[0].var"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			in := CreateInput{Harness: "claude", DisplayName: "Atlas", Handle: "atlas", CPUs: 1, MemoryMB: 512}
			c.edit(&in)
			_, err := validateCreate(in)
			var verr *spec.ValidationError
			require.ErrorAs(t, err, &verr)
			require.Contains(t, verr.Fields, c.field)
			require.NotContains(t, err.Error(), "sk-value")
		})
	}

	// A mapped value over the protocol limit is refused without being echoed.
	long := strings.Repeat("x", spec.MaxEnvValue+1)
	require.Error(t, validateMapped(map[string]string{"OPENCODE_API_KEY": long}))
	require.NotContains(t, validateMapped(map[string]string{"OPENCODE_API_KEY": long}).Error(), long)
}

// CA-08: last_error keeps the message readable but never with control characters and never long.
func TestErrorText(t *testing.T) {
	err := errors.New("docker failed: bad\x00image\nsecond line")
	text := errorText(err)
	require.NotContains(t, text, "\x00")
	require.Contains(t, text, "\n")
	require.LessOrEqual(t, len([]rune(text)), spec.MaxMessage)
	require.Equal(t, "name: must match x", errorText(&spec.ValidationError{Fields: map[string]string{"name": "must match x"}}))
}

// CA-04: a create needs one of the variables the chosen app reads its provider key from. Any one
// of the app's variables is enough; a variable of another app is not one of them.
func TestValidateCreateRequiresACredential(t *testing.T) {
	for harness, vars := range HarnessCredentials {
		for _, v := range vars {
			in, err := validateCreate(CreateInput{
				Harness: harness, DisplayName: "Atlas", Handle: "atlas",
				Env: []EnvVar{{Var: v, SecretName: "PROVIDER_KEY"}},
			})
			require.NoError(t, err, v)
			require.Equal(t, v, in.Env[0].Var)
		}
		_, err := validateCreate(CreateInput{Harness: harness, DisplayName: "Atlas", Handle: "atlas"})
		var missing *CredentialsRequiredError
		require.ErrorAs(t, err, &missing, harness)
		require.Equal(t, harness, missing.Harness)
		for _, v := range vars {
			require.Contains(t, missing.Error(), v, "the refusal names the variables")
		}
	}
	// OpenCode's key is not one Claude Code reads.
	_, err := validateCreate(CreateInput{
		Harness: "claude", DisplayName: "Atlas", Handle: "atlas",
		Env: []EnvVar{{Var: "OPENCODE_API_KEY", SecretName: "PROVIDER_KEY"}},
	})
	var missing *CredentialsRequiredError
	require.ErrorAs(t, err, &missing)
	require.Contains(t, missing.Error(), "CLAUDE_CODE_OAUTH_TOKEN")
}

// CA-04: the container's environment carries the AI's own machine and folder (PARTYLINE_MACHINE,
// PARTYLINE_FOLDER), so the session inside it joins that AI and not one named after the container's
// hostname and working directory; the folder is the one the entrypoint clones into and works in.
func TestContainerEnvCarriesTheAIsIdentity(t *testing.T) {
	svc := &Service{d: Deps{PublicURL: "https://partyline.example.com"}}
	repo := db.Agent{Machine: "container:pl-a1b2c3d4e5f6", Folder: "/workspace/repo"}
	env := svc.containerEnv(
		CreateInput{Harness: "opencode", RepoURL: "https://github.com/example/repo", RepoRef: "main"},
		"main", "pl_agent_synthetic", repo,
		map[string]string{"OPENCODE_API_KEY": "sk-live", EnvMachine: "another-machine", EnvFolder: "/elsewhere"})
	require.Equal(t, repo.Machine, env[EnvMachine])
	require.Equal(t, agents.ContainerRepoFolder, env[EnvFolder])
	require.Equal(t, "sk-live", env["OPENCODE_API_KEY"], "the mapped secret still reaches the container")
	require.Equal(t, "https://github.com/example/repo", env[EnvRepo])

	// A container without a repository works in the bare workspace, and a mapping never replaces the
	// identity Partyline sets (reservedEnv).
	bare := db.Agent{Machine: "container:pl-0f0e0d0c0b0a", Folder: "/workspace"}
	env = svc.containerEnv(CreateInput{Harness: "pi"}, "", "pl_agent_synthetic", bare, map[string]string{})
	require.Equal(t, bare.Machine, env[EnvMachine])
	require.Equal(t, agents.ContainerRootFolder, env[EnvFolder])
	require.NotContains(t, env, EnvRepo, "no repository, no clone")
}
