package api_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// acme is a Claude Code session in ~/acme on the machine "devbox".
var acme = testkit.Folder{Machine: "devbox", Path: "/home/alice/acme", Home: "/home/alice", Harness: "claude"}

// ownedAIs lists the AIs the user owns, by GET /agents.
func ownedAIs(t *testing.T, c *testkit.Client) []dto.Agent {
	t.Helper()
	return testkit.Decode[dto.List[dto.Agent]](c.Get("/api/v1/agents").RequireStatus(http.StatusOK)).Items
}

// aiApp is the app (harness) the owner's profile of the AI reports.
func aiApp(t *testing.T, c *testkit.Client, id string) string {
	t.Helper()
	return profileOf(t, c, id).AI.App
}

// AID-04: a tools-only REST call with the machine and folder acts as that folder's AI, creating
// it on first sight; what it posts is authored by it.
func TestAgentToolsCallActsAsTheFoldersAI(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	room := createRoom(alice, "Support")
	tools := alice.Token(dto.TokenKindAgent, "laptop").AsFolder(testkit.Folder{
		Machine: "devbox", Path: "/home/alice/acme", Home: "/home/alice", Harness: "omp",
	})

	require.Empty(t, ownedAIs(t, alice))
	rooms := testkit.Decode[dto.List[dto.AgentRoom]](tools.Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK))
	require.Empty(t, rooms.Items)

	list := ownedAIs(t, alice)
	require.Len(t, list, 1, "the first call creates the AI of the folder")
	ai := list[0]
	require.Equal(t, []any{"omp", "devbox", "Acme", false}, []any{ai.Harness, ai.Machine, ai.Intelligence.DisplayName, ai.Online})
	prof := profileOf(t, alice, ai.ID)
	require.Equal(t, []any{dto.IntelligenceKindAI, "acme", "Acme"}, []any{prof.Kind, prof.Handle, prof.DisplayName})
	require.Equal(t, []any{"/home/alice/acme", "~/acme", "omp"}, []any{prof.AI.Folder, prof.AI.FolderLabel, prof.AI.App})

	alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: ai.ID}).RequireStatus(http.StatusCreated)
	m := testkit.Decode[dto.Message](tools.Post(agentRoomPath(room.ID, "/messages"), dto.PostMessageRequest{Text: "from the folder"}).RequireStatus(http.StatusCreated))
	require.Equal(t, []any{dto.AuthorKindAgent, ai.ID, alice.User.ID}, []any{m.AuthorKind, m.AuthorID, m.AuthorUserID})
	require.Equal(t, []any{ai.ID, "acme", dto.IntelligenceRoleAI}, []any{m.Author.ID, m.Author.Handle, m.Author.Role})
	require.Len(t, ownedAIs(t, alice), 1, "the same folder is the same AI")

	// Another folder, and the same folder on another machine, are other AIs.
	tok := alice.Token(dto.TokenKindAgent, "desktop")
	tok.AsFolder(testkit.Folder{Machine: "devbox", Path: "/home/alice/notes"}).Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK)
	tok.AsFolder(testkit.Folder{Machine: "notebook", Path: "/home/alice/acme"}).Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK)
	handles := map[string]bool{}
	for _, a := range ownedAIs(t, alice) {
		handles[profileOf(t, alice, a.ID).Handle] = true
	}
	require.Equal(t, map[string]bool{"acme": true, "notes": true, "acme-2": true}, handles)
}

// AID-02: a WebSocket connection with the folder of an AI that tools already created resolves
// the same id and sets the app; a later tools-only call (omp) leaves a live session's app.
func TestAgentWebSocketByFolderResolvesTheSameIdentity(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	tools := tok.AsFolder(testkit.Folder{Machine: "devbox", Path: "/home/alice/acme", Harness: "omp"})
	reg := testkit.Decode[dto.AgentInfo](tools.Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	require.Equal(t, "omp", aiApp(t, alice, reg.ID))

	conn, resp := tok.DialFolder(acme)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	w := conn.Welcome(wsWait)
	require.Equal(t, reg.ID, w.Agent.ID, "the folder resolves the same id from any harness")
	require.Len(t, ownedAIs(t, alice), 1)
	prof := profileOf(t, alice, reg.ID)
	require.Equal(t, []any{"claude", true}, []any{prof.AI.App, prof.AI.Online})

	tools.Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK)
	require.Equal(t, "claude", aiApp(t, alice, reg.ID), "a tools-only call does not relabel a live session")

	conn.Close()
	require.Eventually(t, func() bool { return !env.App.Presence.Online(reg.ID) }, wsWait, 20*time.Millisecond)
	tools.Get("/api/v1/agent/rooms").RequireStatus(http.StatusOK)
	require.Equal(t, "omp", aiApp(t, alice, reg.ID), "with nobody connected a tools-only call names the app")
}

// The welcome frame tells the channel who the AI is and who its owner is.
func TestAgentWelcomeCarriesTheIntelligenceAndOwner(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	conn, resp := tok.DialFolder(acme)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	w := conn.Welcome(wsWait)
	require.Equal(t, dto.IntelligenceRef{ID: w.Agent.ID, Kind: dto.IntelligenceKindAI, Role: dto.IntelligenceRoleAI, DisplayName: "Acme", Handle: "acme"}, w.Intelligence)
	require.Equal(t, dto.IntelligenceRef{ID: alice.User.ID, Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "alice", Handle: "alice"}, w.Owner)
	require.Equal(t, "alice", w.Agent.Owner)

	// A rename shows on the next connection.
	patchProfile(alice, w.Agent.ID, dto.UpdateProfileRequest{DisplayName: ptr("Acme Prime"), Handle: ptr("prime")}).RequireStatus(http.StatusOK)
	conn.Close()
	require.Eventually(t, func() bool { return !env.App.Presence.Online(w.Agent.ID) }, wsWait, 20*time.Millisecond)
	again, resp := tok.DialFolder(acme)
	require.NotNil(t, again, "upgrade refused: %d %s", resp.Status, resp.Body)
	w = again.Welcome(wsWait)
	require.Equal(t, []any{"Acme Prime", "prime"}, []any{w.Intelligence.DisplayName, w.Intelligence.Handle})
	require.Equal(t, "Acme Prime", w.Agent.DisplayName)
}

// MIG-02 through the header: POST /agent/register by folder with X-Partyline-Adopt links the
// named unlinked identity; it keeps its id and profile and is registered idempotently.
func TestAgentRegisterByFolderAdoptsTheNamedLegacyIdentity(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	legacy := env.NewLegacyAI(alice, "old-helper")
	require.Empty(t, profileOf(t, alice, legacy).AI.Folder, "a v0.3 identity starts unlinked")

	adopting := acme
	adopting.Adopt = "old-helper"
	info := testkit.Decode[dto.AgentInfo](tok.AsFolder(adopting).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	require.Equal(t, legacy, info.ID)
	require.Equal(t, "alice", info.Owner)
	prof := profileOf(t, alice, legacy)
	require.Equal(t, []any{"/home/alice/acme", "old-helper", "claude"}, []any{prof.AI.Folder, prof.Handle, prof.AI.App})

	again := testkit.Decode[dto.AgentInfo](tok.AsFolder(acme).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	require.Equal(t, legacy, again.ID)
	require.Len(t, ownedAIs(t, alice), 1)
}

// GET /agent/pending by folder returns what the folder's AI has not received, once; an AI that
// holds a live session gets its messages pushed and the call answers 409.
func TestAgentPendingByFolder(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	room := createRoom(alice, "Support")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	tools := tok.AsFolder(testkit.Folder{Machine: "devbox", Path: "/home/alice/acme", Harness: "omp"})
	reg := testkit.Decode[dto.AgentInfo](tools.Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: reg.ID}).RequireStatus(http.StatusCreated)

	posted := testkit.Decode[dto.Message](alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "ping"}).RequireStatus(http.StatusCreated))
	got := testkit.Decode[dto.List[dto.AgentMessage]](tools.Get("/api/v1/agent/pending").RequireStatus(http.StatusOK))
	require.Len(t, got.Items, 1)
	require.Equal(t, []any{posted.ID, "ping", true}, []any{got.Items[0].Message.ID, got.Items[0].Message.Text, got.Items[0].Trusted})
	again := testkit.Decode[dto.List[dto.AgentMessage]](tools.Get("/api/v1/agent/pending").RequireStatus(http.StatusOK))
	require.Empty(t, again.Items, "delivered once")

	conn, resp := tok.DialFolder(acme)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	require.Equal(t, reg.ID, conn.Welcome(wsWait).Agent.ID)
	tools.Get("/api/v1/agent/pending").RequireError(http.StatusConflict, dto.ErrorCodeConflict)
}

// SES-01: GET /agent/session reports whether the folder has an AI, whether it is online, its app
// and when its live session connected, and creates nothing.
func TestAgentSessionReportsTheFoldersLiveState(t *testing.T) {
	connected := time.Date(2026, 10, 21, 12, 30, 0, 0, time.UTC)
	env := testkit.New(t, testkit.WithClock(testkit.NewClock(connected).Now))
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	session := func(c *testkit.Client, f testkit.Folder) dto.SessionState {
		t.Helper()
		return testkit.Decode[dto.SessionState](c.AsFolder(f).Get("/api/v1/agent/session").RequireStatus(http.StatusOK))
	}

	require.Equal(t, dto.SessionState{}, session(tok, acme), "no AI in the folder yet")
	require.Empty(t, ownedAIs(t, alice), "the lookup creates no AI")
	machines, err := env.App.Store.R().ListMachines(context.Background(), alice.User.ID)
	require.NoError(t, err)
	require.Empty(t, machines, "and does not record the machine")

	tools := tok.AsFolder(testkit.Folder{Machine: "devbox", Path: "/home/alice/acme", Harness: "omp"})
	reg := testkit.Decode[dto.AgentInfo](tools.Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	s := session(tok, acme)
	require.True(t, s.Exists)
	require.False(t, s.Online)
	require.Equal(t, "omp", s.App)
	require.Nil(t, s.ConnectedAt)
	require.NotNil(t, s.Intelligence)
	require.Equal(t, []any{reg.ID, "acme", "Acme", dto.IntelligenceRoleAI}, []any{s.Intelligence.ID, s.Intelligence.Handle, s.Intelligence.DisplayName, s.Intelligence.Role})

	conn, resp := tok.DialFolder(acme)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	conn.Welcome(wsWait)
	s = session(tok, testkit.Folder{Machine: "devbox", Path: "/home/alice/acme"})
	require.True(t, s.Exists)
	require.True(t, s.Online)
	require.Equal(t, "claude", s.App)
	require.NotNil(t, s.ConnectedAt)
	require.True(t, connected.Equal(*s.ConnectedAt), "connected at %s", s.ConnectedAt)

	conn.Close()
	require.Eventually(t, func() bool { return !session(tok, acme).Online }, wsWait, 20*time.Millisecond)
	s = session(tok, acme)
	require.True(t, s.Exists)
	require.Nil(t, s.ConnectedAt)

	for name, f := range map[string]testkit.Folder{
		"another folder":  {Machine: "devbox", Path: "/home/alice/other"},
		"another machine": {Machine: "notebook", Path: "/home/alice/acme"},
	} {
		require.Equal(t, dto.SessionState{}, session(tok, f), name)
	}
	bob := env.NewUser("bob")
	require.Equal(t, dto.SessionState{}, session(bob.Token(dto.TokenKindAgent, "desktop"), acme), "another owner's AI is not visible")
	require.Len(t, ownedAIs(t, alice), 1)
}

// GET /agent/whoami returns the token owner's intelligence; it needs no folder and no AI.
func TestAgentWhoAmIReturnsTheOwnersIntelligence(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")

	me := testkit.Decode[dto.IntelligenceRef](tok.Get("/api/v1/agent/whoami").RequireStatus(http.StatusOK))
	require.Equal(t, dto.IntelligenceRef{ID: alice.User.ID, Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "alice", Handle: "alice"}, me)
	require.Empty(t, ownedAIs(t, alice), "asking who the owner is creates no AI")

	patchProfile(alice, alice.User.ID, dto.UpdateProfileRequest{DisplayName: ptr("Alice Lab"), Handle: ptr("alice-lab")}).RequireStatus(http.StatusOK)
	me = testkit.Decode[dto.IntelligenceRef](tok.Get("/api/v1/agent/whoami").RequireStatus(http.StatusOK))
	require.Equal(t, []any{"Alice Lab", "alice-lab"}, []any{me.DisplayName, me.Handle})

	bob := env.NewUser("bob")
	other := testkit.Decode[dto.IntelligenceRef](bob.Token(dto.TokenKindAgent, "desktop").Get("/api/v1/agent/whoami").RequireStatus(http.StatusOK))
	require.Equal(t, bob.User.ID, other.ID, "each token reports its own owner")
}

// The new routes are agent routes: sessions and user tokens are refused, anonymous callers too.
func TestAgentSessionAndWhoAmIRequireAnAgentToken(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	for _, path := range []string{"/api/v1/agent/session", "/api/v1/agent/whoami"} {
		for name, c := range map[string]*testkit.Client{"session": alice, "user token": alice.Token(dto.TokenKindUser, "script")} {
			e := c.AsFolder(acme).Get(path).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
			require.Equal(t, "agent token required", e.Message, "%s %s", name, path)
		}
		env.Anon().AsFolder(acme).Get(path).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	}
}

// AID-05 on every route that takes the folder: a machine or folder the server cannot use is
// 422 folder_required or folder_invalid, and nothing is created.
func TestAgentFolderHeadersAreValidated(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	escaped := func(p string) string { return url.PathEscape(p) }
	cases := []struct {
		name    string
		headers map[string]string
		code    dto.ErrorCode
	}{
		{"empty folder", map[string]string{dto.HeaderMachine: "devbox", dto.HeaderFolder: ""}, dto.ErrorCodeFolderRequired},
		{"missing machine", map[string]string{dto.HeaderFolder: escaped("/home/alice/acme")}, dto.ErrorCodeFolderRequired},
		{"relative folder", map[string]string{dto.HeaderMachine: "devbox", dto.HeaderFolder: "acme"}, dto.ErrorCodeFolderInvalid},
		{"trailing slash", map[string]string{dto.HeaderMachine: "devbox", dto.HeaderFolder: escaped("/home/alice/acme/")}, dto.ErrorCodeFolderInvalid},
		{"dot-dot segment", map[string]string{dto.HeaderMachine: "devbox", dto.HeaderFolder: escaped("/home/../acme")}, dto.ErrorCodeFolderInvalid},
		{"NUL", map[string]string{dto.HeaderMachine: "devbox", dto.HeaderFolder: "%2Fhome%2Fa%00b"}, dto.ErrorCodeFolderInvalid},
		{"bad percent-encoding", map[string]string{dto.HeaderMachine: "devbox", dto.HeaderFolder: "%zz"}, dto.ErrorCodeFolderInvalid},
		{"1025 bytes", map[string]string{dto.HeaderMachine: "devbox", dto.HeaderFolder: escaped("/" + strings.Repeat("a", 1024))}, dto.ErrorCodeFolderInvalid},
	}
	routes := map[string]func(c *testkit.Client) *testkit.Response{
		"tool call": func(c *testkit.Client) *testkit.Response { return c.Get("/api/v1/agent/rooms") },
		"register":  func(c *testkit.Client) *testkit.Response { return c.Post("/api/v1/agent/register", nil) },
		"pending":   func(c *testkit.Client) *testkit.Response { return c.Get("/api/v1/agent/pending") },
		"session":   func(c *testkit.Client) *testkit.Response { return c.Get("/api/v1/agent/session") },
	}
	for _, c := range cases {
		withHeaders := tok
		h := http.Header{}
		for k, v := range c.headers {
			withHeaders = withHeaders.WithHeader(k, v)
			h.Set(k, v)
		}
		for name, call := range routes {
			e := call(withHeaders).RequireError(http.StatusUnprocessableEntity, c.code)
			require.NotEmpty(t, e.Message, "%s %s", c.name, name)
		}
		conn, resp := tok.DialAgentHeaders(h)
		require.Nil(t, conn, c.name)
		resp.RequireError(http.StatusUnprocessableEntity, c.code)
	}
	require.Empty(t, ownedAIs(t, alice), "refused requests create nothing")
	machines, err := env.App.Store.R().ListMachines(context.Background(), alice.User.ID)
	require.NoError(t, err)
	require.Empty(t, machines)
}

// AID-07: the folder and home headers are percent-encoded; spaces and non-ASCII characters
// decode to the same folder, which names the AI and is shown relative to the home folder.
func TestAgentFolderHeadersAreDecoded(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	folder := testkit.Folder{Machine: "dev box", Path: "/home/alice/the lab/Café Órdenes", Home: "/home/alice", Harness: "claude"}
	require.Contains(t, folder.Headers().Get(dto.HeaderFolder), "%20", "the CLI sends spaces percent-encoded")
	require.NotContains(t, folder.Headers().Get(dto.HeaderFolder), "/", "and the separators too")

	conn, resp := tok.DialFolder(folder)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	w := conn.Welcome(wsWait)
	require.Equal(t, []any{"Café Órdenes", "cafe-ordenes"}, []any{w.Intelligence.DisplayName, w.Intelligence.Handle})
	ai := profileOf(t, alice, w.Agent.ID).AI
	require.Equal(t, []any{"/home/alice/the lab/Café Órdenes", "~/the lab/Café Órdenes"}, []any{ai.Folder, ai.FolderLabel})
	require.Equal(t, "dev box", listedAgent(t, alice, w.Agent.ID).Machine, "the machine is decoded too")

	reg := testkit.Decode[dto.AgentInfo](tok.AsFolder(folder).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	require.Equal(t, w.Agent.ID, reg.ID, "the REST call decodes to the same folder as the WebSocket")
}

// The folder identifies an AI per owner: another owner's token with the same machine and
// folder gets, and sees, its own AI.
func TestAgentFolderIdentityIsPerOwner(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	a := testkit.Decode[dto.AgentInfo](alice.Token(dto.TokenKindAgent, "laptop").AsFolder(acme).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	b := testkit.Decode[dto.AgentInfo](bob.Token(dto.TokenKindAgent, "desktop").AsFolder(acme).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	require.NotEqual(t, a.ID, b.ID)
	require.Equal(t, "alice", a.Owner)
	require.Equal(t, "bob", b.Owner)
	require.Equal(t, []string{a.ID}, agentIDs(ownedAIs(t, alice)))
	require.Equal(t, []string{b.ID}, agentIDs(ownedAIs(t, bob)))
	require.Equal(t, "acme", profileOf(t, alice, a.ID).Handle)
	require.Equal(t, "acme-2", profileOf(t, bob, b.ID).Handle, "handles are unique across the instance")
}

func agentIDs(list []dto.Agent) []string {
	ids := make([]string, len(list))
	for i, a := range list {
		ids[i] = a.ID
	}
	return ids
}

// agentCalls are the agent routes that act as an AI and so need the folder, one request each.
func agentCalls(room, message string) map[string]func(c *testkit.Client) *testkit.Response {
	return map[string]func(c *testkit.Client) *testkit.Response{
		"list rooms": func(c *testkit.Client) *testkit.Response { return c.Get("/api/v1/agent/rooms") },
		"room info":  func(c *testkit.Client) *testkit.Response { return c.Get(agentRoomPath(room, "/info")) },
		"history":    func(c *testkit.Client) *testkit.Response { return c.Get(agentRoomPath(room, "/messages")) },
		"post": func(c *testkit.Client) *testkit.Response {
			return c.Post(agentRoomPath(room, "/messages"), dto.PostMessageRequest{Text: "x"})
		},
		"reply": func(c *testkit.Client) *testkit.Response {
			return c.Post("/api/v1/agent/messages/"+message+"/reply", dto.ReplyRequest{Text: "x"})
		},
		"react": func(c *testkit.Client) *testkit.Response {
			return c.Post("/api/v1/agent/messages/"+message+"/react", dto.ReactRequest{Emoji: "👍"})
		},
		"attachment": func(c *testkit.Client) *testkit.Response { return c.Get("/api/v1/agent/attachments/" + message) },
		"pending":    func(c *testkit.Client) *testkit.Response { return c.Get("/api/v1/agent/pending") },
		"register":   func(c *testkit.Client) *testkit.Response { return c.Post("/api/v1/agent/register", nil) },
		"session":    func(c *testkit.Client) *testkit.Response { return c.Get("/api/v1/agent/session") },
		"update profile": func(c *testkit.Client) *testkit.Response {
			return c.Patch("/api/v1/agent/profile", dto.UpdateProfileRequest{Bio: ptr("x")})
		},
		"profile picture": func(c *testkit.Client) *testkit.Response {
			return c.DoRaw(http.MethodPut, "/api/v1/agent/profile/avatar", "image/png", strings.NewReader("x"))
		},
	}
}

// AID-05 (L-017): a request that names no folder, like every request of a CLI from before
// folders, is 422 folder_required with the hint to update partyline, on the WebSocket and on every
// route that acts as an AI; the v0.3 naming headers do not stand in for the folder. Nothing is
// created.
func TestAgentRoutesRequireTheFolder(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	room := createRoom(alice, "Support")
	msg := testkit.Decode[dto.Message](alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "hello"}).RequireStatus(http.StatusCreated))

	old := http.Header{}
	for k, v := range map[string]string{
		"X-Partyline-Agent": "builder", "X-Partyline-Agent-Mode": "unique", "X-Partyline-Project": "atlas",
		"X-Partyline-Session": "pl-0123456789abcdef", dto.HeaderHarness: "claude", dto.HeaderMachine: "laptop",
	} {
		old.Set(k, v)
	}
	for name, headers := range map[string]http.Header{
		"no headers at all":         {},
		"a CLI from before folders": old,
		"a machine and no folder":   {dto.HeaderMachine: {"laptop"}},
	} {
		for route, call := range agentCalls(room.ID, msg.ID) {
			c := tok
			for k, v := range headers {
				c = c.WithHeader(k, v[0])
			}
			e := call(c).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeFolderRequired)
			require.Contains(t, e.Message, "update partyline", "%s: %s", name, route)
		}
		conn, resp := tok.DialAgentHeaders(headers.Clone())
		require.Nil(t, conn, name)
		e := resp.RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeFolderRequired)
		require.Contains(t, e.Message, "update partyline", name)
	}
	require.Empty(t, ownedAIs(t, alice), "refused requests create no AI")

	// The routes that do not act as an AI need no folder.
	tok.Get("/api/v1/agent/whoami").RequireStatus(http.StatusOK)
	tok.Get("/api/v1/agent/agents").RequireStatus(http.StatusOK)
	tok.Delete("/api/v1/agent/token").RequireStatus(http.StatusNoContent)
}

// AID-05 (L-017): a folder the server cannot use is 422 folder_invalid on every route that acts
// as an AI.
func TestAgentRoutesRefuseAnInvalidFolder(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	room := createRoom(alice, "Support")
	msg := testkit.Decode[dto.Message](alice.Post("/api/v1/rooms/"+room.ID+"/messages", dto.PostMessageRequest{Text: "hello"}).RequireStatus(http.StatusCreated))

	bad := tok.WithHeader(dto.HeaderMachine, "laptop").WithHeader(dto.HeaderFolder, "relative%2Fdir")
	for route, call := range agentCalls(room.ID, msg.ID) {
		call(bad).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeFolderInvalid)
		_ = route
	}
	require.Empty(t, ownedAIs(t, alice))
}

// The v0.3 naming headers are ignored: the AI is the folder's, whatever name, mode, project or
// session key a client from before folders also sends, and nothing named after them is created.
func TestRemovedNamingHeadersAreIgnored(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	tok := alice.Token(dto.TokenKindAgent, "laptop")
	named := tok.AsFolder(acme).
		WithHeader("X-Partyline-Agent", "ghost").WithHeader("X-Partyline-Agent-Mode", "temporary").
		WithHeader("X-Partyline-Project", "atlas").WithHeader("X-Partyline-Session", "pl-0123456789abcdef")

	reg := testkit.Decode[dto.AgentInfo](named.Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK))
	require.Equal(t, reg.ID, testkit.Decode[dto.AgentInfo](tok.AsFolder(acme).Post("/api/v1/agent/register", nil).RequireStatus(http.StatusOK)).ID)
	list := ownedAIs(t, alice)
	require.Len(t, list, 1)
	a := list[0]
	require.Equal(t, "acme", a.Intelligence.Handle, "named after its folder, not after the header")
}
