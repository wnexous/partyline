package cli

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/cliconfig"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/users"
)

// setupRun is one `partyline setup` invocation with a fake HOME and scripted stdin.
type setupRun struct {
	code   int
	stdout string
	stderr string
}

type setupOpts struct {
	env   map[string]string
	stdin string
	// onSleep runs on every wait between approval polls, after the approval URL was printed.
	onSleep  func(stdout string) bool
	hostname string
	// waits records each wait between approval polls.
	waits *[]time.Duration
	// lookPath finds harness CLIs (nil: none installed); run answers harness CLI calls (nil:
	// any call fails the test, so no test ever runs a real harness CLI).
	lookPath func(string) (string, error)
	run      func(line string) ([]byte, error)
}

// testBinary is the partyline binary path setup registers in tests.
const testBinary = "/opt/partyline/bin/partyline"

func runSetup(t *testing.T, o setupOpts, args ...string) setupRun {
	t.Helper()
	var out, errOut lockedBuffer
	std := stdio{
		stdin: strings.NewReader(o.stdin), stdout: &out, stderr: &errOut,
		getenv:     func(k string) string { return o.env[k] },
		hostname:   func() (string, error) { return o.hostname, nil },
		executable: func() (string, error) { return testBinary, nil },
		lookPath: func(name string) (string, error) {
			if o.lookPath == nil {
				return "", exec.ErrNotFound
			}
			return o.lookPath(name)
		},
		runHarness: func(_ context.Context, name string, args ...string) ([]byte, error) {
			line := name + " " + strings.Join(args, " ")
			if o.run == nil {
				t.Errorf("unexpected harness CLI call: %s", line)
				return nil, errors.New("unexpected call")
			}
			return o.run(line)
		},
		sleep: func(_ context.Context, d time.Duration) bool {
			if o.waits != nil {
				*o.waits = append(*o.waits, d)
			}
			if o.onSleep == nil {
				return true
			}
			return o.onSleep(out.String())
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), chanWait)
	defer cancel()
	code := execute(ctx, append([]string{"partyline", "setup"}, args...), std)
	return setupRun{code: code, stdout: out.String(), stderr: errOut.String()}
}

func configPath(home string) string {
	return filepath.Join(home, ".config", "partyline", "config.json")
}

// requireSavedConfig asserts the config file holds want and is private (ONB-04).
func requireSavedConfig(t *testing.T, home string, want cliconfig.Config) {
	t.Helper()
	path := configPath(home)
	got, err := cliconfig.Load(path)
	require.NoError(t, err)
	require.Equal(t, want, got)
	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

// requireAgentToken asserts secret authenticates as an agent token of the server.
func requireAgentToken(t *testing.T, env *testkit.Env, secret string) {
	t.Helper()
	er := env.Anon().WithBearer(secret).WithHeader(dto.HeaderAgent, "probe").Get("/api/v1/agent/rooms").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Equal(t, "agent not registered: connect the channel first", er.Message)
}

// ONB-04 (token path): flags give the URL and an agent token; setup verifies them and writes
// the config file with mode 0600. The token is never printed.
func TestSetupWithToken(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}}, "--url", e.env.URL+"/", "--token", e.secret)
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	requireSavedConfig(t, home, cliconfig.Config{URL: e.env.URL, Token: e.secret})
	require.Contains(t, r.stdout, configPath(home))
	require.NotContains(t, r.stdout+r.stderr, e.secret)
}

// ONB-04 + the served installer: a config with the instance URL and an empty token makes that
// URL the default answer; the token is asked for on stdin.
func TestSetupUsesInstallerURLAsDefault(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	writeConfig(t, home, cliconfig.Config{URL: e.env.URL})
	r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}, stdin: "\n" + e.secret + "\n"})
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "Partyline URL ["+e.env.URL+"]: ")
	require.Contains(t, r.stdout, "Agent token")
	requireSavedConfig(t, home, cliconfig.Config{URL: e.env.URL, Token: e.secret})
}

// ONB-04: a typed URL wins over the default.
func TestSetupAsksForURL(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	writeConfig(t, home, cliconfig.Config{URL: "http://127.0.0.1:1"})
	r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}, stdin: e.env.URL + "\n"}, "--token", e.secret)
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	requireSavedConfig(t, home, cliconfig.Config{URL: e.env.URL, Token: e.secret})
}

// ONB-04: running setup again for the same instance keeps the saved token (verified again)
// instead of asking for a new one.
func TestSetupKeepsSavedToken(t *testing.T) {
	e := newChannelEnv(t)
	home := t.TempDir()
	writeConfig(t, home, cliconfig.Config{URL: e.env.URL, Token: e.secret})
	r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}}, "--yes")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "Using the saved agent token")
	requireSavedConfig(t, home, cliconfig.Config{URL: e.env.URL, Token: e.secret})

	// A saved token that no longer works is an error, and the file stays as it was.
	home2 := t.TempDir()
	writeConfig(t, home2, cliconfig.Config{URL: e.env.URL, Token: "pl_agent_revoked"})
	r = runSetup(t, setupOpts{env: map[string]string{"HOME": home2}}, "--yes")
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "the server rejected the token")
	requireSavedConfig(t, home2, cliconfig.Config{URL: e.env.URL, Token: "pl_agent_revoked"})
}

// ONB-04: an invalid token is an error and no file is written; an existing file (here the
// installer's) is left byte-for-byte.
func TestSetupRejectsInvalidToken(t *testing.T) {
	e := newChannelEnv(t)

	home := t.TempDir()
	r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}}, "--url", e.env.URL, "--token", "pl_agent_notarealtoken")
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "the server rejected the token")
	_, err := os.Stat(configPath(home))
	require.ErrorIs(t, err, os.ErrNotExist, "no config file written")

	home2 := t.TempDir()
	path := writeConfig(t, home2, cliconfig.Config{URL: e.env.URL})
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	r = runSetup(t, setupOpts{env: map[string]string{"HOME": home2}}, "--yes", "--token", "pl_agent_notarealtoken")
	require.Equal(t, 1, r.code)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after, "config file untouched")
}

// ONB-04: a user token (not an agent token) is refused too.
func TestSetupRejectsUserToken(t *testing.T) {
	e := newChannelEnv(t)
	_, userSecret, err := e.env.App.Users.CreateToken(context.Background(), e.alice.User.ID, users.TokenInput{Kind: dto.TokenKindUser, Name: "cli"})
	require.NoError(t, err)
	home := t.TempDir()
	r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}}, "--url", e.env.URL, "--token", userSecret)
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "not an agent token")
	_, err = os.Stat(configPath(home))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// ONB-04: an unreachable URL fails before anything is written.
func TestSetupUnreachableServer(t *testing.T) {
	home := t.TempDir()
	r := runSetup(t, setupOpts{env: map[string]string{"HOME": home}}, "--url", "http://127.0.0.1:1", "--token", "pl_agent_x")
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "cannot reach http://127.0.0.1:1")
	_, err := os.Stat(configPath(home))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// Without any URL (no flag, no variable, no config) setup cannot proceed: usage error.
func TestSetupNeedsURL(t *testing.T) {
	r := runSetup(t, setupOpts{env: map[string]string{"HOME": t.TempDir()}}, "--yes", "--token", "pl_agent_x")
	require.Equal(t, 2, r.code)
	require.Contains(t, r.stderr, "no instance URL")
}

// deviceSetupEnv is a server on a fake clock, so polls 2 s apart pass the server's limit.
func deviceSetupEnv(t *testing.T) (*testkit.Env, *testkit.Clock, *testkit.Client) {
	t.Helper()
	clock := testkit.NewClock(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	env := testkit.New(t, testkit.WithClock(clock.Now))
	return env, clock, env.NewUser("alice")
}

// approvalPattern finds the approval page and the code setup prints (DEV-01): the page is
// <instance>/devices without the code, and the code is shown as ABCD-EFGH to type there.
var approvalPattern = regexp.MustCompile(`Open (http://\S+) in a browser[^\n]*\n\n  ([A-HJ-NP-Z2-9]{4})-([A-HJ-NP-Z2-9]{4})\n`)

// approvalCode returns the code setup printed, or "" before it is printed.
func approvalCode(stdout string) string {
	m := approvalPattern.FindStringSubmatch(stdout)
	if m == nil {
		return ""
	}
	return m[2] + m[3]
}

// typeCode approves or denies the request as user, typing the code like the approval page.
func typeCode(user *testkit.Client, action, code string) *testkit.Response {
	return user.Post("/api/v1/devices/"+action, dto.DeviceCode{Code: code})
}

// DEV-01, DEV-02, DEV-06 (browser path): setup prints <instance>/devices and the code to type
// there, polls every 2 s and, once a logged-in user approves, names the approver, asks for
// confirmation and saves the delivered agent token with the instance URL.
func TestSetupBrowserApproval(t *testing.T) {
	env, clock, alice := deviceSetupEnv(t)
	home := t.TempDir()
	var mu sync.Mutex
	sleeps := 0
	var waits []time.Duration
	r := runSetup(t, setupOpts{
		env: map[string]string{"HOME": home}, hostname: "devbox-7", waits: &waits, stdin: "y\n",
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
	}, "--url", env.URL, "--browser")
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	m := approvalPattern.FindStringSubmatch(r.stdout)
	require.NotNil(t, m, r.stdout)
	require.Equal(t, env.URL+"/devices", m[1], "DEV-01: the approval page is <instance>/devices")
	require.NotContains(t, r.stdout, "/devices/", "DEV-01: no link carries the code")
	require.Contains(t, r.stdout, "\nApproved by @alice on "+env.URL+"\nSave this agent token of @alice on this machine? [Y/n]: ", "DEV-06")
	require.Equal(t, 2, sleeps, "polled until approved")
	require.Equal(t, []time.Duration{2 * time.Second, 2 * time.Second}, waits, "DEV-04: one poll per 2 s")

	got, err := cliconfig.Load(configPath(home))
	require.NoError(t, err)
	require.Equal(t, env.URL, got.URL)
	require.True(t, strings.HasPrefix(got.Token, "pl_agent_"), "agent token saved")
	requireSavedConfig(t, home, got)
	requireAgentToken(t, env, got.Token)
	require.NotContains(t, r.stdout+r.stderr, got.Token)

	toks := testkit.Decode[dto.List[dto.Token]](alice.Get("/api/v1/tokens").RequireStatus(http.StatusOK))
	require.Len(t, toks.Items, 1)
	require.Equal(t, "devbox-7 (via setup)", toks.Items[0].Name, "DEV-02: named after the hostname")
}

// ONB-04: with no token given and no saved token, an empty answer to the token prompt starts
// the browser approval.
func TestSetupEmptyTokenAnswerStartsBrowserApproval(t *testing.T) {
	env, clock, alice := deviceSetupEnv(t)
	home := t.TempDir()
	r := runSetup(t, setupOpts{
		env: map[string]string{"HOME": home}, hostname: "devbox", stdin: "\ny\n",
		onSleep: func(stdout string) bool {
			clock.Advance(2 * time.Second)
			if code := approvalCode(stdout); code != "" {
				typeCode(alice, "approve", code)
			}
			return true
		},
	}, "--url", env.URL)
	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	got, err := cliconfig.Load(configPath(home))
	require.NoError(t, err)
	requireAgentToken(t, env, got.Token)
}

// DEV-03: a denied request makes setup exit non-zero with "approval denied" and write nothing.
func TestSetupBrowserDenied(t *testing.T) {
	env, clock, alice := deviceSetupEnv(t)
	home := t.TempDir()
	r := runSetup(t, setupOpts{
		env: map[string]string{"HOME": home}, hostname: "devbox",
		onSleep: func(stdout string) bool {
			clock.Advance(2 * time.Second)
			typeCode(alice, "deny", approvalCode(stdout))
			return true
		},
	}, "--url", env.URL, "--browser")
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "approval denied")
	_, err := os.Stat(configPath(home))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// DEV-06: when the person at the terminal does not confirm the approver (answers "n", or the
// input ends), setup saves nothing and exits 1; --yes accepts without asking.
func TestSetupBrowserApproverConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name, stdin string
		args        []string
		saved       bool
	}{
		{name: "declined", stdin: "n\n"},
		{name: "end of input", stdin: ""},
		{name: "yes flag", args: []string{"--yes"}, saved: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, clock, _ := deviceSetupEnv(t)
			bob := env.NewUser("bob")
			home := t.TempDir()
			r := runSetup(t, setupOpts{
				env: map[string]string{"HOME": home}, hostname: "devbox", stdin: tc.stdin,
				onSleep: func(stdout string) bool {
					clock.Advance(2 * time.Second)
					if code := approvalCode(stdout); code != "" {
						typeCode(bob, "approve", code)
					}
					return true
				},
			}, append([]string{"--url", env.URL, "--browser", "--harness", "none"}, tc.args...)...)
			require.Contains(t, r.stdout, "Approved by @bob on "+env.URL+"\n")
			_, err := os.Stat(configPath(home))
			if tc.saved {
				require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
				require.NotContains(t, r.stdout, "Save this agent token", "--yes does not ask")
				require.NoError(t, err)
				return
			}
			require.Equal(t, 1, r.code)
			require.Contains(t, r.stdout, "Save this agent token of @bob on this machine? [Y/n]: ")
			require.Contains(t, r.stderr, "not saved: the token belongs to @bob")
			require.ErrorIs(t, err, os.ErrNotExist, "nothing saved")
		})
	}
}

// DEV-03: a request nobody approves within 10 minutes makes setup exit non-zero with
// "approval expired".
func TestSetupBrowserExpired(t *testing.T) {
	env, clock, _ := deviceSetupEnv(t)
	home := t.TempDir()
	polls := 0
	r := runSetup(t, setupOpts{
		env: map[string]string{"HOME": home}, hostname: "devbox",
		onSleep: func(string) bool {
			polls++
			clock.Advance(2 * time.Minute)
			return polls < 20
		},
	}, "--url", env.URL, "--browser")
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "approval expired")
	require.Equal(t, 5, polls, "pending until 10 minutes passed")
	_, err := os.Stat(configPath(home))
	require.ErrorIs(t, err, os.ErrNotExist)
}
