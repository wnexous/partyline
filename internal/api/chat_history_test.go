package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/testkit"
)

// chatHistoryFixture is alice with her AIs Atlas (the operator) and Notes, and a running fake connection of
// her organization that keeps 30 days of chat history, with a direct chat ("5511999990000").
type chatHistoryFixture struct {
	env   *testkit.Env
	alice *testkit.Client
	token *testkit.Client
	fake  *connectortest.Fake
	inst  *connectortest.Instance
	conn  dto.Connector
	atlas string
	notes string
	dm    string
}

func newChatHistoryFixture(t *testing.T, tune func(*connectors.Kind)) *chatHistoryFixture {
	t.Helper()
	fake := connectortest.New()
	kind := fake.Kind()
	if tune != nil {
		tune(&kind)
	}
	env := testkit.New(t, testkit.WithConnectorKinds(kind))
	alice := env.NewUser("alice")
	f := &chatHistoryFixture{env: env, alice: alice, token: alice.Token(dto.TokenKindAgent, "laptop"), fake: fake,
		atlas: env.NewAI(alice, "Atlas"), notes: env.NewAI(alice, "Notes")}
	f.conn = alice.OperateConnector(createFakeIdle(alice, "wa", true).ID, f.atlas)
	f.inst = fake.Instance(t, f.conn.ID)
	f.inst.WaitRun(t)
	f.keep(t, 30)
	f.emit(t, "D1", "hello", time.Now().Add(-time.Hour))
	f.dm = f.chatID(t, "5511999990000")
	return f
}

// keep sets the retention as alice; turning it on or off restarts the connection (CH-10).
func (f *chatHistoryFixture) keep(t *testing.T, days int64) {
	t.Helper()
	old := f.inst
	f.alice.Patch("/api/v1/connectors/"+f.conn.ID, dto.UpdateConnectorRequest{HistoryDays: &days}).RequireStatus(http.StatusOK)
	if (days == 0) == (f.conn.HistoryDays == 0) {
		f.conn.HistoryDays = days
		return
	}
	f.conn.HistoryDays = days
	require.Eventually(t, func() bool { return f.fake.Lookup(f.conn.ID) != old }, connectortest.Timeout, 5*time.Millisecond)
	f.inst = f.fake.Lookup(f.conn.ID)
	f.inst.WaitRun(t)
}

// emit has Pat write to the direct chat.
func (f *chatHistoryFixture) emit(t *testing.T, id, text string, at time.Time) {
	t.Helper()
	f.inst.Emit(context.Background(), connectors.Inbound{
		ChatID: "5511999990000", ChatTitle: "Pat", ChatKind: "dm", MessageID: id,
		SenderID: "5511999990000", SenderName: "Pat", Text: text, At: at,
	})
}

func (f *chatHistoryFixture) chatID(t *testing.T, external string) string {
	t.Helper()
	for _, c := range testkit.Decode[dto.List[dto.Chat]](f.alice.Get("/api/v1/connectors/" + f.conn.ID + "/chats").RequireStatus(http.StatusOK)).Items {
		if c.ExternalID == external {
			return c.ID
		}
	}
	t.Fatalf("no chat %s", external)
	return ""
}

// agentPath is the history of the chat on the agent API; managerPath on the API of the managers.
func (f *chatHistoryFixture) agentPath(chat string) string {
	return "/api/v1/agent/connections/" + f.conn.ID + "/chats/" + url.PathEscape(chat) + "/history"
}

func (f *chatHistoryFixture) managerPath(chat string) string {
	return "/api/v1/connectors/" + f.conn.ID + "/chats/" + url.PathEscape(chat) + "/history"
}

func (f *chatHistoryFixture) fetch(name, chat string, count int) *testkit.Response {
	return f.token.AsAgent(name).Post(f.agentPath(chat)+"/fetch", dto.FetchHistoryRequest{Count: count})
}

// CH-05: the operator reads the history of a chat of its connection by the chat's id or its address:
// each message with the id it knew it by, who wrote it ("you" for the account), whether it is trusted,
// its time, kind and text, newest last; the AI's own sends are in it too.
func TestCH05TheOperatorReadsAChatsHistory(t *testing.T) {
	f := newChatHistoryFixture(t, nil)
	sent := testkit.Decode[dto.PlatformSendResult](f.token.AsAgent("Atlas").Post(connectionChatPath(f.conn.ID, f.dm),
		dto.SendToChatRequest{Text: "oi, Pat"}).RequireStatus(http.StatusCreated))
	f.inst.WaitSend(t)

	res := f.token.AsAgent("Atlas").Get(f.agentPath(f.dm)).RequireStatus(http.StatusOK)
	h := testkit.Decode[dto.ChatHistory](res)
	require.Equal(t, []any{f.conn.ID, connectortest.KindName, f.dm, "Pat", dto.ChatKindDM, false},
		[]any{h.ConnectionID, h.Platform, h.ChatID, h.ChatTitle, h.ChatKind, h.HasMore})
	require.Len(t, h.Messages, 2)
	pat, own := h.Messages[0], h.Messages[1]
	require.Equal(t, []any{"Pat", "5511999990000", false, false, dto.MessageKindText, "hello"},
		[]any{pat.From, pat.SenderID, pat.FromSelf, pat.Trusted, pat.Kind, pat.Text})
	require.NotEmpty(t, pat.MessageID)
	require.Equal(t, []any{sent.ID, "you", true, "oi, Pat"}, []any{own.MessageID, own.From, own.FromSelf, own.Text})
	require.True(t, pat.Time.Before(own.Time))

	byAddress := testkit.Decode[dto.ChatHistory](f.token.AsAgent("Atlas").Get(f.agentPath("5511999990000")).RequireStatus(http.StatusOK))
	require.Equal(t, h.Messages, byAddress.Messages, "the chat's address names it as for send_message")
	f.token.AsAgent("Atlas").Get(f.agentPath("120363000000000077@g.us")).RequireError(http.StatusNotFound, dto.ErrorCodeUnknownChat)
}

// CH-06, CH-07: only the operator (agent API) and the admins of the connection's organization (the
// managers' API) read a chat's history; another AI of the organization, a member who only uses the
// connection and an outsider get not_found, as for a connection that does not exist. history_off is
// told to those who may read it, and only to them.
func TestCH06WhoReadsAChatsHistory(t *testing.T) {
	f := newChatHistoryFixture(t, nil)
	f.token.AsAgent("Atlas").Get(f.agentPath(f.dm)).RequireStatus(http.StatusOK)
	f.alice.Get(f.managerPath(f.dm)).RequireStatus(http.StatusOK)

	member := f.env.NewUser("bob")
	f.env.AddMember(f.alice.PersonalOrg().ID, member.User.ID, orgs.PermissionMember)
	member.Get("/api/v1/connectors/" + f.conn.ID).RequireStatus(http.StatusOK) // bob uses it
	outsider := f.env.NewUser("carol")
	refused := map[string]*testkit.Response{
		"another AI of the organization": f.token.AsAgent("Notes").Get(f.agentPath(f.dm)),
		"a member who uses it":           member.Get(f.managerPath(f.dm)),
		"an outsider":                    outsider.Get(f.managerPath(f.dm)),
		"a connection that is not there": f.alice.Get("/api/v1/connectors/nope/chats/" + f.dm + "/history"),
	}
	for who, res := range refused {
		require.Equal(t, http.StatusNotFound, res.Status, who)
		res.RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}
	// An agent token reads through the agent API only.
	f.token.AsAgent("Atlas").Get(f.managerPath(f.dm)).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	f.keep(t, 0)
	off := f.token.AsAgent("Atlas").Get(f.agentPath(f.dm))
	off.RequireError(http.StatusConflict, dto.ErrorCodeHistoryOff)
	require.Contains(t, string(off.Body), "turn it on in the connection's settings")
	f.alice.Get(f.managerPath(f.dm)).RequireError(http.StatusConflict, dto.ErrorCodeHistoryOff)
	f.token.AsAgent("Notes").Get(f.agentPath(f.dm)).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	member.Get(f.managerPath(f.dm)).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// CH-08: fetch_history asks the platform for older messages of the chat, from the oldest one its history
// has; what the platform sends back is added to the history and never delivered, once per platform id.
func TestCH08FetchHistoryAsksThePlatformAndKeepsWhatComesBack(t *testing.T) {
	f := newChatHistoryFixture(t, nil)
	f.emit(t, "D2", "second", time.Now())
	queued, err := f.env.App.Platform.Unacked(context.Background(), f.atlas, 0, 100)
	require.NoError(t, err)

	got := testkit.Decode[dto.FetchHistoryResult](f.fetch("Atlas", f.dm, 10).RequireStatus(http.StatusAccepted))
	require.Equal(t, 10, got.Requested)
	reqs := f.inst.Fetches()
	require.Len(t, reqs, 1)
	require.Equal(t, []any{"5511999990000", "D1", false, 10}, []any{reqs[0].ChatID, reqs[0].AnchorID, reqs[0].AnchorFromSelf, reqs[0].Count})
	require.WithinDuration(t, time.Now().Add(-time.Hour), reqs[0].AnchorAt, time.Second)
	got = testkit.Decode[dto.FetchHistoryResult](f.fetch("Atlas", "5511999990000", 0).RequireStatus(http.StatusAccepted))
	require.Equal(t, 50, got.Requested, "50 by default")

	// The phone answers with the chat's past, the anchor included.
	for _, m := range []struct {
		id, text string
		age      time.Duration
	}{{"P1", "two days ago", 48 * time.Hour}, {"P2", "yesterday", 24 * time.Hour}, {"D1", "hello", time.Hour}} {
		f.inst.Emit(context.Background(), connectors.Inbound{
			ChatID: "5511999990000", ChatKind: "dm", MessageID: m.id, SenderID: "5511999990000", SenderName: "Pat",
			Text: m.text, At: time.Now().Add(-m.age), Backfill: true,
		})
	}
	after, err := f.env.App.Platform.Unacked(context.Background(), f.atlas, 0, 100)
	require.NoError(t, err)
	require.Len(t, after, len(queued), "nothing of the past is delivered")
	h := testkit.Decode[dto.ChatHistory](f.token.AsAgent("Atlas").Get(f.agentPath(f.dm)).RequireStatus(http.StatusOK))
	texts := []string{}
	for _, m := range h.Messages {
		texts = append(texts, m.Text)
	}
	require.Equal(t, []string{"two days ago", "yesterday", "hello", "second"}, texts)

	f.fetch("Atlas", f.dm, 51).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	f.fetch("Notes", f.dm, 10).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	f.alice.Post(f.agentPath(f.dm)+"/fetch", dto.FetchHistoryRequest{Count: 10}).RequireStatus(http.StatusForbidden)
	f.inst.SetFetchError(errors.New("the WhatsApp number is not connected"))
	f.fetch("Atlas", f.dm, 10).RequireError(http.StatusBadGateway, dto.ErrorCodeUnavailable)
	f.inst.SetFetchError(nil)

	// A chat whose history holds nothing yet gives the platform nowhere to start.
	group := f.chatOf(t, "120363000000000001@g.us")
	f.fetch("Atlas", group, 10).RequireError(http.StatusConflict, dto.ErrorCodeConflict)

	f.keep(t, 0)
	f.fetch("Atlas", f.dm, 10).RequireError(http.StatusConflict, dto.ErrorCodeHistoryOff)
}

// chatOf records a group chat of the connection with no message.
func (f *chatHistoryFixture) chatOf(t *testing.T, external string) string {
	t.Helper()
	chat, err := connectors.UpsertChat(context.Background(), f.env.App.Store, f.env.App.Bus, f.conn.ID,
		connectors.ChatInfo{ID: external, Title: "Team", Kind: "group"}, time.Now(), time.Now())
	require.NoError(t, err)
	return chat.ID
}

// CH-08: a platform that cannot send a chat's past answers unsupported, and nothing is asked.
func TestCH08FetchHistoryOnAPlatformWithoutItIsUnsupported(t *testing.T) {
	f := newChatHistoryFixture(t, func(k *connectors.Kind) { k.CanFetchHistory = false })
	res := f.fetch("Atlas", f.dm, 10)
	res.RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeUnsupported)
	require.Contains(t, string(res.Body), "cannot send the past of a chat")
	require.Empty(t, f.inst.Fetches())
}
