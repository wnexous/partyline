package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/channel"
)

// The three placement variables the launcher consumes, and the ones it hands to the session instead.
var (
	placementUserVars   = []string{"PARTYLINE_ORG", "PARTYLINE_POSITION", "PARTYLINE_PARENT"}
	placementLaunchVars = []string{"PARTYLINE_LAUNCH_ORG", "PARTYLINE_LAUNCH_POSITION", "PARTYLINE_LAUNCH_PARENT"}
)

// launchBase is the environment a launch passes on when nothing else is asked.
func (m *launchMachine) launchBase() []string {
	return []string{"HOME=" + m.home, "PATH=/usr/local/bin:/usr/bin", "LANG=C.UTF-8", "PARTYLINE_FOLDER=/work/acme", "PARTYLINE_HARNESS=claude"}
}

// claudeLaunch is a machine with Claude Code installed and Partyline registered in it.
func claudeLaunch(t *testing.T) *launchMachine {
	t.Helper()
	m := newLaunchMachine(t)
	m.onPath["claude"] = true
	m.register(t, "claude")
	return m
}

// launchedWith returns the environment a launch passes on with the given PARTYLINE_LAUNCH_* entries.
func (m *launchMachine) launchedWith(entries ...string) []string {
	return append(m.launchBase(), entries...)
}

// requireNoPlacement checks an environment has none of the placement variables, in either spelling.
func requireNoPlacement(t *testing.T, env []string, msgAndArgs ...any) {
	t.Helper()
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		require.NotContains(t, placementUserVars, name, msgAndArgs...)
		require.NotContains(t, placementLaunchVars, name, msgAndArgs...)
	}
}

// ORG-04, POS-07, POS-08 (AD-020): the leading --org, --position and --parent (as `--x v` and as `--x=v`, in
// any order among the leading flags) are taken off the harness's arguments and handed to the session as
// PARTYLINE_LAUNCH_ORG, PARTYLINE_LAUNCH_POSITION and PARTYLINE_LAUNCH_PARENT. The harness never gets the flags.
func TestLauncherPassesTheOrganizationPositionAndParent(t *testing.T) {
	m := claudeLaunch(t)
	want := m.launchedWith("PARTYLINE_LAUNCH_ORG=acme", "PARTYLINE_LAUNCH_POSITION=Lead developer", "PARTYLINE_LAUNCH_PARENT=@lead")
	argv := []string{"claude", "--dangerously-load-development-channels", "server:partyline", "--resume", "--org", "x"}

	for name, flags := range map[string][]string{
		"separate values":  {"--org", "acme", "--position", "Lead developer", "--parent", "@lead"},
		"inline values":    {"--org=acme", "--position=Lead developer", "--parent=@lead"},
		"mixed, reordered": {"--parent", "@lead", "--org=acme", "--take-over", "--position", "Lead developer"},
	} {
		t.Run(name, func(t *testing.T) {
			r := m.run(t, append(append([]string{"claude"}, flags...), "--resume", "--org", "x")...)
			require.Equal(t, 7, r.code, r.stderr)
			require.Empty(t, r.stderr)
			require.Equal(t, argv, r.argv, "the flags are not the harness's, a later --org is")
			wantEnv := want
			if name == "mixed, reordered" { // --take-over is among the leading flags too
				wantEnv = slices.Concat(m.launchBase(), []string{"PARTYLINE_TAKEOVER=1"}, want[len(m.launchBase()):])
			}
			require.Equal(t, wantEnv, r.env)
		})
	}

	// each one on its own: only what was asked for is passed on (L-018)
	for _, one := range []struct{ flag, value, variable string }{
		{"--org", "acme", "PARTYLINE_LAUNCH_ORG"},
		{"--position", "Lead developer", "PARTYLINE_LAUNCH_POSITION"},
		{"--parent", "@lead", "PARTYLINE_LAUNCH_PARENT"},
	} {
		t.Run("only "+one.flag, func(t *testing.T) {
			r := m.run(t, "claude", one.flag, one.value)
			require.Equal(t, 7, r.code, r.stderr)
			require.Equal(t, m.launchedWith(one.variable+"="+one.value), r.env)
		})
	}
}

// A flag with no value stops the launch with exit 2, like --as does, and nothing runs. Placement flags that follow
// the harness's own arguments belong to the harness.
func TestLauncherPlacementFlagsNeedAValueAndOnlyCountWhenLeading(t *testing.T) {
	m := claudeLaunch(t)
	for _, flag := range []string{"--org", "--position", "--parent"} {
		r := m.run(t, "claude", flag)
		require.Equal(t, 2, r.code, flag)
		require.False(t, r.execed, flag)
		require.Equal(t, "partyline claude: "+flag+" needs a value\n", r.stderr)
	}
	r := m.run(t, "claude", "--resume", "--org", "acme", "--position=Worktrees")
	require.Equal(t, 7, r.code, r.stderr)
	require.Equal(t, []string{"claude", "--dangerously-load-development-channels", "server:partyline", "--resume", "--org", "acme", "--position=Worktrees"}, r.argv,
		"after the harness's own argument they are the harness's")
	requireNoPlacement(t, r.env)
}

// ORG-04: PARTYLINE_ORG, PARTYLINE_POSITION and PARTYLINE_PARENT stand in for a flag that is absent, one by
// one; a flag wins over its variable (and an empty flag value is no flag).
func TestLauncherFallsBackOnThePlacementVariablesAndTheFlagWins(t *testing.T) {
	m := claudeLaunch(t)
	m.getenv = map[string]string{"PARTYLINE_ORG": "acme", "PARTYLINE_POSITION": "Worktrees", "PARTYLINE_PARENT": "@lead"}
	m.environ = append(m.environ, "PARTYLINE_ORG=acme", "PARTYLINE_POSITION=Worktrees", "PARTYLINE_PARENT=@lead")

	r := m.run(t, "claude")
	require.Equal(t, 7, r.code, r.stderr)
	require.Equal(t, m.launchedWith("PARTYLINE_LAUNCH_ORG=acme", "PARTYLINE_LAUNCH_POSITION=Worktrees", "PARTYLINE_LAUNCH_PARENT=@lead"), r.env,
		"the variables stand in for the flags")

	r = m.run(t, "claude", "--org", "globex", "--parent=@sentinel")
	require.Equal(t, 7, r.code, r.stderr)
	require.Equal(t, m.launchedWith("PARTYLINE_LAUNCH_ORG=globex", "PARTYLINE_LAUNCH_POSITION=Worktrees", "PARTYLINE_LAUNCH_PARENT=@sentinel"), r.env,
		"a flag wins over its variable, the others fall back")

	r = m.run(t, "claude", "--org=", "--position", "   ")
	require.Equal(t, 7, r.code, r.stderr)
	require.Equal(t, m.launchedWith("PARTYLINE_LAUNCH_ORG=acme", "PARTYLINE_LAUNCH_POSITION=Worktrees", "PARTYLINE_LAUNCH_PARENT=@lead"), r.env,
		"an empty flag value is no flag")

	m.getenv = map[string]string{"PARTYLINE_ORG": "  acme  "}
	r = m.run(t, "claude")
	require.Equal(t, m.launchedWith("PARTYLINE_LAUNCH_ORG=acme"), r.env, "surrounding spaces of a variable are not part of the value")
}

// AD-020: the launcher consumes the values. The user-facing variables are not in the harness's environment, so
// a session started from a shell that exports them does not hand them on to the harness's own shells.
func TestLauncherConsumesThePlacementVariables(t *testing.T) {
	m := claudeLaunch(t)
	m.getenv = map[string]string{"PARTYLINE_ORG": "acme", "PARTYLINE_POSITION": "Worktrees", "PARTYLINE_PARENT": "@lead"}
	m.environ = append(m.environ, "PARTYLINE_ORG=acme", "PARTYLINE_POSITION=Worktrees", "PARTYLINE_PARENT=@lead")
	r := m.run(t, "claude")
	for _, name := range placementUserVars {
		for _, kv := range r.env {
			require.False(t, strings.HasPrefix(kv, name+"="), "%s is not passed on", name)
		}
	}
	require.Contains(t, r.env, "PARTYLINE_LAUNCH_POSITION=Worktrees")
}

// L-035: without a flag and without a variable no placement is handed on at all, whatever else is in the
// environment, so the session asks the server for nothing and gets its defaults.
func TestLauncherWithoutAPlacementHandsNothingOn(t *testing.T) {
	m := claudeLaunch(t)
	m.getenv = map[string]string{"PARTYLINE_ORG": "", "PARTYLINE_POSITION": " ", "PARTYLINE_AGENT": "atlas"}
	r := m.run(t, "claude")
	require.Equal(t, 7, r.code, r.stderr)
	require.Equal(t, m.launchBase(), r.env)
	requireNoPlacement(t, r.env)
}

// Design, Risks: a session that was launched with a position runs shells where PARTYLINE_LAUNCH_* are set. A
// launch from there is another AI: it never reads those variables as input and replaces them in the harness it
// starts, so it does not inherit its parent's position (or organization, or parent AI).
func TestANestedLaunchDoesNotInheritThePlacementOfItsParent(t *testing.T) {
	parentSession := []string{"PARTYLINE_LAUNCH_ORG=acme", "PARTYLINE_LAUNCH_POSITION=Lead developer", "PARTYLINE_LAUNCH_PARENT=@boss"}
	nested := func(t *testing.T) *launchMachine {
		m := claudeLaunch(t)
		m.environ = append(m.environ, parentSession...)
		m.getenv = map[string]string{}
		for _, kv := range parentSession {
			name, value, _ := strings.Cut(kv, "=")
			m.getenv[name] = value
		}
		return m
	}

	t.Run("no flags: nothing is inherited", func(t *testing.T) {
		m := nested(t)
		r := m.run(t, "claude")
		require.Equal(t, 7, r.code, r.stderr)
		require.Equal(t, m.launchBase(), r.env)
	})
	t.Run("a position of its own: only that", func(t *testing.T) {
		m := nested(t)
		r := m.run(t, "claude", "--position", "Worktrees")
		require.Equal(t, m.launchedWith("PARTYLINE_LAUNCH_POSITION=Worktrees"), r.env, "not the parent's organization or parent AI")
	})
	t.Run("a parent of its own: only that", func(t *testing.T) {
		m := nested(t)
		r := m.run(t, "claude", "--parent=@lead")
		require.Equal(t, m.launchedWith("PARTYLINE_LAUNCH_PARENT=@lead"), r.env)
	})
	t.Run("the user-facing variables are the input, the launch ones are not", func(t *testing.T) {
		m := nested(t)
		m.getenv["PARTYLINE_POSITION"] = "Worktrees"
		r := m.run(t, "claude")
		require.Equal(t, m.launchedWith("PARTYLINE_LAUNCH_POSITION=Worktrees"), r.env)
	})
	t.Run("the server is asked for what this launch asks, not for the inherited placement", func(t *testing.T) {
		m := nested(t)
		var seen atomic.Pointer[http.Header]
		m.serveSession(t, func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Clone()
			seen.Store(&h)
			require.NoError(t, json.NewEncoder(w).Encode(dto.SessionState{}))
		})
		r := m.run(t, "claude", "--position", "Worktrees")
		require.Equal(t, 7, r.code, r.stderr)
		h := seen.Load()
		require.NotNil(t, h, "a placement is checked with the server")
		require.Equal(t, "Worktrees", h.Get(dto.HeaderPosition))
		require.Empty(t, h.Values(dto.HeaderOrg), "the parent's organization is not asked for")
		require.Empty(t, h.Values(dto.HeaderParent), "nor its parent AI")
	})
}

// fixedSince is when the live session of the takeover question started.
var fixedSince = time.Date(2026, 10, 21, 12, 30, 0, 0, time.UTC)

// serveSession answers GET /agent/session with handler and points the launcher at it.
func (m *launchMachine) serveSession(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/agent/session", r.URL.Path)
		require.Equal(t, "Bearer "+launchToken, r.Header.Get("Authorization"))
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	m.saveConfig(t, srv.URL, launchToken)
	m.sessionHTTP = srv.Client()
}

// refuseSession answers GET /agent/session with the error status, code and message of a refused start, and counts
// the requests.
func (m *launchMachine) refuseSession(t *testing.T, status int, code dto.ErrorCode, message string) *atomic.Int32 {
	t.Helper()
	var asked atomic.Int32
	m.serveSession(t, func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		require.NoError(t, json.NewEncoder(w).Encode(dto.ErrorBody{Error: dto.Error{Code: code, Message: message}}))
	})
	return &asked
}

// ORG-04, ORG-05, POS-07: with a placement the launcher asks the server before it starts the harness, with the
// headers a session sends, and does so without a terminal too - it is not the takeover prompt. A start that is
// accepted goes on. The position travels percent-encoded.
func TestLauncherChecksThePlacementWithTheServerBeforeStarting(t *testing.T) {
	m := claudeLaunch(t)
	var header atomic.Pointer[http.Header]
	m.serveSession(t, func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Clone()
		header.Store(&h)
		require.NoError(t, json.NewEncoder(w).Encode(dto.SessionState{}))
	})

	r := m.run(t, "claude", "--org", "acme", "--position", "Lead developer · Café", "--parent", "@lead", "--as", "atlas-bot")
	require.Equal(t, 7, r.code, r.stderr)
	require.Empty(t, r.stderr)
	require.True(t, r.execed, "an accepted placement starts the harness")
	h := header.Load()
	require.NotNil(t, h, "the server was asked, though stdin and stderr are not terminals")
	require.Equal(t, []string{"testbox", "%2Fwork%2Facme", "claude", "atlas-bot", "acme", "Lead%20developer%20%C2%B7%20Caf%C3%A9", "@lead"}, []string{
		h.Get(dto.HeaderMachine), h.Get(dto.HeaderFolder), h.Get(dto.HeaderHarness), h.Get(dto.HeaderAdopt),
		h.Get(dto.HeaderOrg), h.Get(dto.HeaderPosition), h.Get(dto.HeaderParent),
	})

	t.Run("nothing is asked without a placement and without a terminal", func(t *testing.T) {
		m := claudeLaunch(t)
		m.serveSession(t, func(http.ResponseWriter, *http.Request) { t.Error("the launcher asked the server for nothing") })
		r := m.run(t, "claude", "--as", "atlas-bot")
		require.Equal(t, 7, r.code, r.stderr)
	})
}

// ORG-05, POS-07, ORG-04 (design "Error Handling Strategy"): when the server refuses the placement - a creator
// who is not in the organization (403), an organization or position that does not exist (422), an organization
// other than the AI's home (409) - the launcher prints the server's message, exits 1 and starts nothing. This is
// so for a harness with a live session and for a tools-only one, and the refusal comes before the "not
// registered" warning, which says "Starting anyway".
func TestLauncherStopsWhenTheServerRefusesThePlacement(t *testing.T) {
	refusals := []struct {
		name    string
		status  int
		code    dto.ErrorCode
		message string
	}{
		{"a creator who is not in the organization", 403, dto.ErrorCodeForbidden, "you are not a member of that organization"},
		{"a position the creator may not give", 403, dto.ErrorCodeForbidden, "that position is not below your own"},
		{"an organization that does not exist", 422, dto.ErrorCodeOrganizationRequired, "no organization has that slug or id: partyline orgs lists yours"},
		{"a position that does not exist", 422, dto.ErrorCodePositionUnknown, "the organization has no position with that name: partyline orgs <slug> lists them"},
		{"an organization other than the AI's home", 409, dto.ErrorCodeOrganizationMismatch, "this AI belongs to Acme (acme): an AI does not move to another organization"},
	}
	for _, harness := range []string{"claude", "codex"} {
		for _, c := range refusals {
			t.Run(harness+": "+c.name, func(t *testing.T) {
				m := newLaunchMachine(t) // Partyline is not registered in the harness: the warning must not come first
				m.onPath[harness] = true
				asked := m.refuseSession(t, c.status, c.code, c.message)
				m.terminals = map[int]bool{0: true, 2: true}
				m.input = "y\n"

				r := m.run(t, harness, "--org", "acme", "--position", "Worktrees")
				require.Equal(t, 1, r.code, r.stderr)
				require.False(t, r.execed, "the harness is not started")
				require.Equal(t, "partyline "+harness+": "+c.message+"\n", r.stderr, "the server's message, and nothing about starting anyway")
				require.EqualValues(t, 1, asked.Load(), "one request")
			})
		}
	}
}

// A refusal only counts when the launch asked for a placement: the session lookup of a plain launch ignores
// whatever an older or odd server answers (SES-01), as it always did.
func TestLauncherIgnoresARefusalOfALaunchThatAskedForNoPlacement(t *testing.T) {
	m := claudeLaunch(t)
	m.terminals = map[int]bool{0: true, 2: true}
	m.refuseSession(t, 422, dto.ErrorCodeFolderInvalid, "the folder must be an absolute path")
	r := m.run(t, "claude")
	require.Equal(t, 7, r.code, r.stderr)
	require.True(t, r.execed)
	require.Empty(t, r.stderr)
}

// The check is best effort, like the takeover question: an answer that is not a refusal of the placement - a
// server that does not answer, a token it rejects, a route it does not have, an error without a message, a
// server error - never stops a launch. Each is the only thing different (L-018).
func TestLauncherStartsWhenTheCheckCannotTellAboutThePlacement(t *testing.T) {
	cases := map[string]func(m *launchMachine){
		"an unreachable server": func(m *launchMachine) { m.saveConfig(t, "http://127.0.0.1:1", launchToken) },
		"a token it rejects": func(m *launchMachine) {
			m.refuseSession(t, 401, dto.ErrorCodeUnauthorized, "invalid or expired credentials")
		},
		"no such route":  func(m *launchMachine) { m.refuseSession(t, 404, dto.ErrorCodeNotFound, "not found") },
		"a server error": func(m *launchMachine) { m.refuseSession(t, 500, dto.ErrorCodeInternal, "boom") },
		"a refusal with no message": func(m *launchMachine) {
			m.serveSession(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":"forbidden"}}`))
			})
		},
		"a refusal that is not JSON": func(m *launchMachine) {
			m.serveSession(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte("<html>forbidden</html>"))
			})
		},
		"no credentials at all": func(*launchMachine) {},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			m := claudeLaunch(t)
			setup(m)
			r := m.run(t, "claude", "--org", "acme")
			require.Equal(t, 7, r.code, r.stderr)
			require.True(t, r.execed, "the session will report the problem itself")
			require.Empty(t, r.stderr)
			require.Contains(t, r.env, "PARTYLINE_LAUNCH_ORG=acme")
		})
	}
}

// SES-01 with a placement: one request answers both questions. An accepted placement and a live session still
// ask whether to take over, and the answer starts the harness with the takeover and the placement.
func TestLauncherAsksOnceForThePlacementAndTheTakeover(t *testing.T) {
	m := claudeLaunch(t)
	m.terminals = map[int]bool{0: true, 2: true}
	m.input = "y\n"
	var asked atomic.Int32
	m.serveSession(t, func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		require.NoError(t, json.NewEncoder(w).Encode(sessionState("Atlas", "claude", fixedSince)))
	})
	m.now = func() time.Time { return fixedSince }

	r := m.run(t, "claude", "--org", "acme")
	require.Equal(t, 7, r.code, r.stderr)
	require.Contains(t, r.stderr, "Atlas is open in another session")
	require.Contains(t, r.env, "PARTYLINE_TAKEOVER=1")
	require.Contains(t, r.env, "PARTYLINE_LAUNCH_ORG=acme")
	require.EqualValues(t, 1, asked.Load(), "one lookup serves both")
}

// ORG-04: --help tells how to start an AI in an organization, with a position and under a parent AI, and that the
// variables stand in for the flags (the words are the help's own, written out here).
func TestHelpDocumentsThePlacementFlags(t *testing.T) {
	var out, errOut bytes.Buffer
	code := execute(context.Background(), []string{"partyline", "--help"}, stdio{stdout: &out, stderr: &errOut, getenv: func(string) string { return "" }})
	require.Equal(t, 0, code)
	require.Contains(t, out.String(), "A new AI joins your default organization. Put --org <slug> before the arguments to start it in\n"+
		"another one, --position <name> to give it a position there and --parent <@handle> when one of your\n"+
		"AIs starts it; PARTYLINE_ORG, PARTYLINE_POSITION and PARTYLINE_PARENT stand in for the flags. An AI\n"+
		"keeps the organization it was created in.\n")
}

// The pre-flight of the launcher and the processes of the session send the identity headers in the same way:
// the same names, the same percent-encoding, the same omissions (AID-07). They are written twice - the launcher
// builds a request of its own - so this pins them together.
func TestTheLauncherSendsTheHeadersTheSessionSends(t *testing.T) {
	record := func(t *testing.T) (string, func() http.Header) {
		var got atomic.Pointer[http.Header]
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Clone()
			got.Store(&h)
			_, _ = w.Write([]byte(`{"items":[]}`))
		}))
		t.Cleanup(srv.Close)
		return srv.URL, func() http.Header { return *got.Load() }
	}
	names := []string{dto.HeaderMachine, dto.HeaderFolder, dto.HeaderHome, dto.HeaderHarness, dto.HeaderAdopt, dto.HeaderOrg, dto.HeaderPosition, dto.HeaderParent}
	for name, id := range map[string]channel.Identity{
		"everything":  {Machine: "dev box", Folder: "/work/the lab/Café", Home: "/home/alice", Harness: "pi", Adopt: "old-helper", Org: "acme", Position: "Lead developer · Café", Parent: "@lead"},
		"the minimum": {Machine: "devbox", Folder: "/work/acme"},
		"no machine":  {Folder: "/work/acme", Org: "acme"},
	} {
		t.Run(name, func(t *testing.T) {
			url, session := record(t)
			_, err := channel.Pending(context.Background(), channel.PendingOptions{URL: url, Token: "pl_agent_x", Identity: id})
			require.NoError(t, err)
			fromSession := session()

			launcher := http.Header{}
			setIdentityHeaders(launcher, id)
			for _, n := range names {
				require.Equal(t, fromSession.Values(n), launcher.Values(n), n)
			}
		})
	}
}
