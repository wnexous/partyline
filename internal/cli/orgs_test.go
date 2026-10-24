package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/cliconfig"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/users"
)

func runOrgs(env map[string]string, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = execute(context.Background(), append([]string{"partyline", "orgs"}, args...), stdio{
		stdout: &out, stderr: &errOut, getenv: func(k string) string { return env[k] },
	})
	return code, out.String(), errOut.String()
}

// orgsWorld is alice, who administers her own organization (her default) and Acme, with the chart
//
//	Admin ─ Director ─ Lead developer ─ Worktrees
//	  └ Sentinel
//
// and is a plain member of Globex, where she holds Analyst; Dave is a user in no relation to her.
type orgsWorld struct {
	*channelEnv
	acme, globex orgs.Membership
	analyst      string
}

func newOrgsWorld(t *testing.T) *orgsWorld {
	t.Helper()
	e := newChannelEnv(t)
	ctx := context.Background()
	w := &orgsWorld{channelEnv: e}
	var err error
	w.acme, err = e.env.App.Orgs.Create(ctx, e.alice.User.ID, "Acme")
	require.NoError(t, err)
	director := e.env.AddPosition(w.acme.ID, "Director", w.acme.PositionID)
	lead := e.env.AddPosition(w.acme.ID, "Lead developer", director)
	e.env.AddPosition(w.acme.ID, "Worktrees", lead)
	e.env.AddPosition(w.acme.ID, "Sentinel", w.acme.PositionID)

	bob := e.env.NewUser("bob")
	w.globex, err = e.env.App.Orgs.Create(ctx, bob.User.ID, "Globex")
	require.NoError(t, err)
	w.analyst = e.env.AddPosition(w.globex.ID, "Analyst", w.globex.PositionID)
	e.env.AddMember(w.globex.ID, e.alice.User.ID, orgs.PermissionMember, w.analyst)
	return w
}

// ORG-04 (T37): `partyline orgs` lists the organizations of the token's user - slug, name, permission and
// position - by name, with a star on the default, the one a new AI joins when --org is not given, and says how
// that is changed. The default follows the user's choice.
func TestOrgsListsTheOrganizationsAndMarksTheDefault(t *testing.T) {
	w := newOrgsWorld(t)
	const footer = "\nNew AIs join the one marked * unless you pass --org; change your default in Settings.\n"

	code, out, errOut := runOrgs(w.vars())
	require.Equal(t, 0, code, errOut)
	require.Empty(t, errOut)
	require.Equal(t, `  SLUG    NAME    PERMISSION  POSITION
  acme    Acme    admin       Admin
* alice   alice   admin       Admin
  globex  Globex  member      Analyst
`+footer, out)

	require.NoError(t, w.env.App.Orgs.SetDefault(context.Background(), w.alice.User.ID, w.acme.ID))
	code, out, errOut = runOrgs(w.vars())
	require.Equal(t, 0, code, errOut)
	require.Equal(t, `  SLUG    NAME    PERMISSION  POSITION
* acme    Acme    admin       Admin
  alice   alice   admin       Admin
  globex  Globex  member      Analyst
`+footer, out, "the star follows the default she chose")
}

// A guest is listed too; the position column of a member with none is empty, with no trailing spaces.
func TestOrgsListsAGuestAndAnEmptyPosition(t *testing.T) {
	w := newOrgsWorld(t)
	carol := w.env.NewUser("carol")
	w.env.AddMember(w.globex.ID, carol.User.ID, orgs.PermissionGuest, "")
	env := map[string]string{"PARTYLINE_URL": w.env.URL, "PARTYLINE_TOKEN": w.tokenOf(t, carol)}
	code, out, errOut := runOrgs(env)
	require.Equal(t, 0, code, errOut)
	require.Equal(t, `  SLUG    NAME    PERMISSION  POSITION
* carol   carol   admin       Admin
  globex  Globex  guest

New AIs join the one marked * unless you pass --org; change your default in Settings.
`, out)
	for _, line := range strings.Split(out, "\n") {
		require.Equal(t, strings.TrimRight(line, " "), line, "no trailing spaces: %q", line)
	}
}

// ORG-04: `partyline orgs <slug>` prints the organization's chart of positions as a tree, so --position names
// can be found, and marks the one the user holds. The address is matched in any case, and an id works too.
func TestOrgsPrintsThePositionTreeOfAnOrganization(t *testing.T) {
	w := newOrgsWorld(t)
	const want = `Acme
  Admin (you)
    Director
      Lead developer
        Worktrees
    Sentinel
`
	for _, ref := range []string{"acme", "ACME", w.acme.ID} {
		code, out, errOut := runOrgs(w.vars(), ref)
		require.Equal(t, 0, code, "%s: %s", ref, errOut)
		require.Equal(t, want, out, ref)
	}

	code, out, errOut := runOrgs(w.vars(), "globex")
	require.Equal(t, 0, code, errOut)
	require.Equal(t, "Globex\n  Admin\n    Analyst (you)\n", out, "her own position is the one marked, and only there")

	code, out, errOut = runOrgs(w.vars(), "alice")
	require.Equal(t, 0, code, errOut)
	require.Equal(t, "alice\n  Admin (you)\n", out, "a name that is its address is printed once")

	// an address that differs from the name is shown beside it, as partyline agents does
	updated := "Acme Corp"
	_, err := w.env.App.Orgs.Update(context.Background(), w.alice.User.ID, w.acme.ID, orgs.UpdateInput{Name: &updated})
	require.NoError(t, err)
	code, out, errOut = runOrgs(w.vars(), "acme")
	require.Equal(t, 0, code, errOut)
	require.True(t, strings.HasPrefix(out, "Acme Corp (acme)\n  Admin (you)\n"), out)
}

// L-045: an organization the user does not belong to is not found, whether it exists or not, and says so with
// exit 1 and nothing on stdout.
func TestOrgsOfAnUnknownSlugFails(t *testing.T) {
	w := newOrgsWorld(t)
	stranger := w.env.NewUser("dave")
	_, err := w.env.App.Orgs.Create(context.Background(), stranger.User.ID, "Hidden")
	require.NoError(t, err)

	for _, ref := range []string{"no-such-org", "hidden"} {
		code, out, errOut := runOrgs(w.vars(), ref)
		require.Equal(t, 1, code, ref)
		require.Empty(t, out, ref)
		require.Equal(t, "partyline orgs: you belong to no organization with the address \""+ref+"\"; partyline orgs lists yours\n", errOut, ref)
	}
}

// A blank argument names nothing: the list is printed.
func TestOrgsWithABlankSlugListsThemAll(t *testing.T) {
	w := newOrgsWorld(t)
	code, out, errOut := runOrgs(w.vars(), "  ")
	require.Equal(t, 0, code, errOut)
	require.Contains(t, out, "  SLUG    NAME    PERMISSION  POSITION\n")
}

// A chart with no positions says so; a user in no organization is told how to get one.
func TestOrgsWithNothingToShow(t *testing.T) {
	w := newOrgsWorld(t)
	for _, p := range testkitPositions(t, w, w.globex.ID) {
		require.NoError(t, w.env.App.Orgs.DeletePosition(context.Background(), w.globex.CreatedBy, w.globex.ID, p))
	}
	code, out, errOut := runOrgs(w.vars(), "globex")
	require.Equal(t, 0, code, errOut)
	require.Equal(t, "Globex\n  No positions yet.\n", out)

	loner := w.env.NewUser("loner")
	require.NoError(t, w.env.App.Orgs.Delete(context.Background(), loner.User.ID, loner.PersonalOrg().ID))
	env := map[string]string{"PARTYLINE_URL": w.env.URL, "PARTYLINE_TOKEN": w.tokenOf(t, loner)}
	code, out, errOut = runOrgs(env)
	require.Equal(t, 0, code, errOut)
	require.Equal(t, "You belong to no organization: create one in the web app, or ask an admin to invite you.\n", out)
	code, out, errOut = runOrgs(env, "acme")
	require.Equal(t, 1, code)
	require.Empty(t, out)
	require.Contains(t, errOut, `no organization with the address "acme"`)
}

// L-045: what goes wrong exits non-zero with the reason on stderr and nothing on stdout - no credentials (2), an
// unreachable server, a server that fails, a token it rejects, and a user token where an agent token is needed.
func TestOrgsFailures(t *testing.T) {
	w := newOrgsWorld(t)
	answering := func(status int, body string) map[string]string {
		srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/api/v1/agent/orgs", r.URL.Path)
			rw.WriteHeader(status)
			_, _ = rw.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return map[string]string{"PARTYLINE_URL": srv.URL, "PARTYLINE_TOKEN": "pl_agent_x"}
	}
	userSecret := w.userTokenOf(t, w.alice)
	cases := []struct {
		name   string
		env    map[string]string
		code   int
		stderr string
	}{
		{"no credentials", map[string]string{}, 2, "partyline orgs: set PARTYLINE_URL and PARTYLINE_TOKEN (or --url and --token), or run partyline setup\n"},
		{"an unreachable server", map[string]string{"PARTYLINE_URL": "http://127.0.0.1:1", "PARTYLINE_TOKEN": "pl_agent_x"}, 1, "partyline orgs: cannot reach http://127.0.0.1:1\n"},
		{"a server error", answering(500, `{"error":{"code":"internal","message":"boom"}}`), 1, "partyline orgs: the server answered 500: boom\n"},
		{"a rejected token", answering(401, `{"error":{"code":"unauthorized","message":"invalid or expired credentials"}}`), 1, "partyline orgs: the server answered 401: invalid or expired credentials\n"},
		{"an answer that is not JSON", answering(200, `<html>`), 1, ""},
		{"an error without a body", answering(502, ``), 1, "partyline orgs: the server answered 502\n"},
		{"a user token", map[string]string{"PARTYLINE_URL": w.env.URL, "PARTYLINE_TOKEN": userSecret}, 1, "partyline orgs: the server answered 403: agent token required\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, errOut := runOrgs(c.env)
			require.Equal(t, c.code, code, errOut)
			require.Empty(t, out)
			if c.stderr != "" {
				require.Equal(t, c.stderr, errOut)
			} else {
				require.Contains(t, errOut, "partyline orgs: unexpected answer")
			}
			// the same with an organization named: the failure comes before the lookup
			code, out, _ = runOrgs(c.env, "acme")
			require.Equal(t, c.code, code)
			require.Empty(t, out)
		})
	}
	code, _, errOut := runOrgs(w.vars(), "acme", "globex")
	require.Equal(t, 2, code)
	require.Equal(t, "partyline orgs: takes at most one organization\n", errOut)
}

// The command is in --help, with what it takes.
func TestHelpListsTheOrgsCommand(t *testing.T) {
	var out bytes.Buffer
	require.Equal(t, 0, execute(context.Background(), []string{"partyline", "--help"}, stdio{stdout: &out, stderr: &bytes.Buffer{}, getenv: func(string) string { return "" }}))
	require.Contains(t, out.String(), "  orgs [slug]            list your organizations and where you stand in them; with an address, its positions\n")
	require.Contains(t, out.String(), "AIs starts it; PARTYLINE_ORG, PARTYLINE_POSITION and PARTYLINE_PARENT stand in for the flags. An AI\n"+
		"keeps the organization it was created in. partyline orgs lists the organizations and their positions.\n")
}

// ORG-04: after saving, `partyline login` says which organization new AIs join, in the design's words, from
// GET /agent/orgs: the user's own default.
func TestLoginPrintsTheDefaultOrganization(t *testing.T) {
	w := newOrgsWorld(t)
	login := func(t *testing.T) setupRun {
		home := t.TempDir()
		return runLogin(t, setupOpts{env: map[string]string{"HOME": home}, hostname: "devbox-7", stdin: w.secret + "\n"}, "--url", w.env.URL+"/", "--token-stdin")
	}

	r := login(t)
	require.Equal(t, 0, r.code, r.stderr)
	require.Equal(t, "Logged in as @alice on "+w.env.URL+" (machine devbox-7)\n"+
		"New AIs join alice (alice); pass --org or change your default in Settings.\n", r.stdout)

	require.NoError(t, w.env.App.Orgs.SetDefault(context.Background(), w.alice.User.ID, w.acme.ID))
	r = login(t)
	require.Equal(t, 0, r.code, r.stderr)
	require.Equal(t, "Logged in as @alice on "+w.env.URL+" (machine devbox-7)\n"+
		"New AIs join Acme (acme); pass --org or change your default in Settings.\n", r.stdout, "her chosen default")
	require.NotContains(t, r.stdout+r.stderr, w.secret)
}

// A login that cannot learn the organization still finishes: the token is saved, "Logged in" is printed and the
// exit code is 0, with no line about organizations and no error. So does a user who belongs to none. Each
// condition is the only thing different (L-018).
func TestLoginFinishesWhenTheOrganizationCallFails(t *testing.T) {
	w := newOrgsWorld(t)
	target, err := url.Parse(w.env.URL)
	require.NoError(t, err)
	proxy := func(orgsRoute func(rw http.ResponseWriter, r *http.Request)) string {
		rp := httputil.NewSingleHostReverseProxy(target)
		srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/agent/orgs" {
				orgsRoute(rw, r)
				return
			}
			rp.ServeHTTP(rw, r)
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	cases := map[string]func(rw http.ResponseWriter, r *http.Request){
		"a server error":             func(rw http.ResponseWriter, _ *http.Request) { rw.WriteHeader(http.StatusInternalServerError) },
		"a route it does not have":   func(rw http.ResponseWriter, _ *http.Request) { http.NotFound(rw, nil) },
		"an answer that is not JSON": func(rw http.ResponseWriter, _ *http.Request) { _, _ = rw.Write([]byte("<html>")) },
		"no organization marked as the default": func(rw http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(rw).Encode(dto.List[dto.AgentOrg]{Items: []dto.AgentOrg{{ID: "o1", Name: "Acme", Slug: "acme", Permission: "member"}}})
		},
		"no organizations": func(rw http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(rw).Encode(dto.List[dto.AgentOrg]{Items: []dto.AgentOrg{}})
		},
	}
	for name, orgsRoute := range cases {
		t.Run(name, func(t *testing.T) {
			base := proxy(orgsRoute)
			home := t.TempDir()
			r := runLogin(t, setupOpts{env: map[string]string{"HOME": home}, hostname: "devbox-7", stdin: w.secret + "\n"}, "--url", base+"/", "--token-stdin")
			require.Equal(t, 0, r.code, r.stderr)
			require.Equal(t, "Logged in as @alice on "+base+" (machine devbox-7)\n", r.stdout, "the login line and nothing about organizations")
			require.Empty(t, r.stderr)
			requireSavedConfig(t, home, cliconfig.Config{URL: base, Token: w.secret})
		})
	}
}

// tokenOf mints an agent token for the user and returns its secret.
func (e *channelEnv) tokenOf(t *testing.T, c *testkit.Client) string {
	t.Helper()
	_, secret, err := e.env.App.Users.CreateToken(context.Background(), c.User.ID, users.TokenInput{Kind: dto.TokenKindAgent, Name: "laptop"})
	require.NoError(t, err)
	return secret
}

// userTokenOf mints a user token (the kind the CLI must refuse) for the client and returns its secret.
func (e *channelEnv) userTokenOf(t *testing.T, c *testkit.Client) string {
	t.Helper()
	_, secret, err := e.env.App.Users.CreateToken(context.Background(), c.User.ID, users.TokenInput{Kind: dto.TokenKindUser, Name: "cli"})
	require.NoError(t, err)
	return secret
}

// testkitPositions lists the ids of the positions of an organization.
func testkitPositions(t *testing.T, w *orgsWorld, orgID string) []string {
	t.Helper()
	chart, err := w.env.App.Authz.Chart(context.Background(), orgID)
	require.NoError(t, err)
	var ids []string
	for id := range chart.Positions {
		ids = append(ids, id)
	}
	return ids
}
