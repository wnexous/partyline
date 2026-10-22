package cli

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/cliconfig"
	"github.com/wnexous/partyline/internal/users"
)

// CLI-01 (browser path): login prints the approval page and code, polls, saves the delivered
// agent token with the instance URL (0600) and names the account from GET /agent/whoami.
func TestLoginBrowserApproval(t *testing.T) {
	env, clock, alice := deviceSetupEnv(t)
	home := t.TempDir()
	var mu sync.Mutex
	sleeps := 0
	r := runLogin(t, setupOpts{
		env: map[string]string{"HOME": home}, hostname: "devbox-7", stdin: "y\n",
		onSleep: func(stdout string) bool {
			mu.Lock()
			defer mu.Unlock()
			sleeps++
			clock.Advance(2 * time.Second)
			if sleeps == 2 { // approved while the CLI waits
				code := approvalCode(stdout)
				require.NotEmpty(t, code, "approval page and code printed: %s", stdout)
				typeCode(alice, "approve", code).RequireStatus(http.StatusOK)
			}
			return true
		},
	}, "--url", env.URL+"/")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "Approved by @alice on "+env.URL+"\nSave this agent token of @alice on this machine? [Y/n]: ")
	require.Contains(t, r.stdout, "Logged in as @alice on "+env.URL+" (machine devbox-7)\n", "CLI-01: the exact login line")
	require.Equal(t, 2, sleeps, "polled until approved")

	got, err := cliconfig.Load(configPath(home))
	require.NoError(t, err)
	require.Equal(t, env.URL, got.URL)
	require.True(t, strings.HasPrefix(got.Token, "pl_agent_"), "agent token saved")
	requireSavedConfig(t, home, got)
	requireAgentToken(t, env, got.Token)
	require.NotContains(t, r.stdout+r.stderr, got.Token)
}

// CLI-01 (stdin path): --token-stdin reads the token from the first line and saves it with
// the URL without the browser flow and without printing it.
func TestLoginTokenFromStdin(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	r := runLogin(t, setupOpts{
		env: map[string]string{"HOME": home}, hostname: "devbox-7", stdin: e.secret + "\n",
	}, "--url", e.env.URL+"/", "--token-stdin")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "Logged in as @alice on "+e.env.URL+" (machine devbox-7)\n")
	require.NotContains(t, r.stdout+r.stderr, "Open "+e.env.URL+"/devices")
	require.NotContains(t, r.stdout+r.stderr, e.secret)
	requireSavedConfig(t, home, cliconfig.Config{URL: e.env.URL, Token: e.secret})
	requireAgentToken(t, e.env, e.secret)
}

// CLI-01: the URL comes from the flag, PARTYLINE_URL, the saved config, else the prompt.
func TestLoginResolvesTheInstanceURL(t *testing.T) {
	for _, name := range []string{"flag wins", "environment", "saved config"} {
		t.Run(name, func(t *testing.T) {
			env := newChannelEnv(t)
			home := t.TempDir()
			full := map[string]string{"HOME": home}
			var args []string
			switch name {
			case "flag wins":
				args = []string{"--url", env.env.URL + "/"}
			case "environment":
				full["PARTYLINE_URL"] = env.env.URL + "/"
			case "saved config":
				writeConfig(t, home, cliconfig.Config{URL: env.env.URL})
			}
			r := runLogin(t, setupOpts{env: full, hostname: "devbox", stdin: env.secret + "\n"},
				append(args, "--token-stdin")...)
			require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
			got, err := cliconfig.Load(configPath(home))
			require.NoError(t, err)
			require.Equal(t, env.env.URL, got.URL)
			require.Equal(t, env.secret, got.Token)
		})
	}
}

// CLI-01: login prompts for the URL when the flag, the environment and the config are empty.
func TestLoginPromptsForTheURL(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	r := runLogin(t, setupOpts{
		env: map[string]string{"HOME": home}, hostname: "devbox",
		stdin: e.env.URL + "\n" + e.secret + "\n",
	}, "--token-stdin")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "Partyline URL: ")
	requireSavedConfig(t, home, cliconfig.Config{URL: e.env.URL, Token: e.secret})
}

// CLI-01: a denied approval exits 1 with the existing message and saves nothing.
func TestLoginBrowserDenied(t *testing.T) {
	env, clock, alice := deviceSetupEnv(t)
	home := t.TempDir()
	r := runLogin(t, setupOpts{
		env: map[string]string{"HOME": home}, hostname: "devbox",
		onSleep: func(stdout string) bool {
			clock.Advance(2 * time.Second)
			typeCode(alice, "deny", approvalCode(stdout))
			return true
		},
	}, "--url", env.URL)
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "approval denied")
	_, err := os.Stat(configPath(home))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// CLI-01: an approval nobody answers expires; login exits 1 with the existing message.
func TestLoginBrowserExpired(t *testing.T) {
	env, clock, _ := deviceSetupEnv(t)
	home := t.TempDir()
	polls := 0
	r := runLogin(t, setupOpts{
		env: map[string]string{"HOME": home}, hostname: "devbox",
		onSleep: func(string) bool {
			polls++
			clock.Advance(2 * time.Minute)
			return polls < 20
		},
	}, "--url", env.URL)
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "approval expired")
	require.Equal(t, 5, polls, "pending until 10 minutes passed")
	_, err := os.Stat(configPath(home))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// CLI-01: a token the server rejects saves nothing and never prints the token; a user token
// is refused with the agent-token message.
func TestLoginRejectsInvalidTokens(t *testing.T) {
	e := newChannelEnv(t)
	_, userSecret, err := e.env.App.Users.CreateToken(t.Context(), e.alice.User.ID, users.TokenInput{Kind: dto.TokenKindUser, Name: "cli"})
	require.NoError(t, err)
	for _, tc := range []struct {
		name, token, want string
	}{
		{name: "unknown", token: "pl_agent_notarealtoken", want: "the server rejected the token"},
		{name: "user token", token: userSecret, want: "not an agent token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			r := runLogin(t, setupOpts{env: map[string]string{"HOME": home}, stdin: tc.token + "\n"},
				"--url", e.env.URL, "--token-stdin")
			require.Equal(t, 1, r.code)
			require.Contains(t, r.stderr, tc.want)
			require.NotContains(t, r.stdout+r.stderr, tc.token)
			_, err := os.Stat(configPath(home))
			require.ErrorIs(t, err, os.ErrNotExist, "no config file written")
		})
	}
}

// CLI-01: the login line names the token owner's handle, not the approver's display name;
// a renamed handle is what whoami reports.
func TestLoginReportsTheWhoamiHandle(t *testing.T) {
	e := newChannelEnv(t)
	e.alice.Patch("/api/v1/intelligences/"+e.alice.User.ID, dto.UpdateProfileRequest{Handle: strPtr("alice-lab")}).RequireStatus(http.StatusOK)
	home := t.TempDir()
	r := runLogin(t, setupOpts{env: map[string]string{"HOME": home}, hostname: "devbox", stdin: e.secret + "\n"},
		"--url", e.env.URL, "--token-stdin")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "Logged in as @alice-lab on "+e.env.URL+" (machine devbox)\n")
}

func strPtr(s string) *string { return &s }

// The saved config is private (0600) after both login paths (CLI-01).
func TestLoginSavesPrivateConfig(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	r := runLogin(t, setupOpts{env: map[string]string{"HOME": home}, stdin: e.secret + "\n"},
		"--url", e.env.URL, "--token-stdin")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	fi, err := os.Stat(configPath(home))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}
