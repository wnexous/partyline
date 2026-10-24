package connectors_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/connectors/safehttp"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/platform"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// Synthetic platform ids.
const (
	groupID   = "120363000000000001@g.us"
	otherID   = "120363000000000002@g.us"
	senderID  = "5511999990000@s.whatsapp.net"
	senderAlt = "100000000000001@lid"
	// Other senders, a direct chat and its peer.
	sender2 = "5511999990005@s.whatsapp.net"
	sender3 = "5511999990006@s.whatsapp.net"
	lid2    = "100000000000002@lid"
	dmA     = "5511999990001@s.whatsapp.net"
	dmB     = "5511999990002@s.whatsapp.net"
)

type inboundEnv struct {
	fake  *connectortest.Fake
	env   *testkit.Env
	alice *testkit.Client
	conn  dto.Connector
	inst  *connectortest.Instance
	// operator is the AI that operates the connection: alice's "relay".
	operator string
}

// newInboundEnv runs an app (1 MiB upload limit) with a running fake connector of alice, operated
// by her AI "relay" (a connection runs once an AI operates it, CID-02).
func newInboundEnv(t *testing.T, opts ...testkit.Option) *inboundEnv {
	t.Helper()
	fake := connectortest.New()
	opts = append(opts, testkit.WithConnectorKinds(fake.Kind()), testkit.WithEnv(map[string]string{"PARTYLINE_MAX_UPLOAD_MB": "1"}))
	env := testkit.New(t, opts...)
	alice := env.NewUser("alice")
	on := true
	conn := testkit.Decode[dto.Connector](alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "wa", Config: map[string]any{"token": "fake-token-not-real"}, Enabled: &on,
	}).RequireStatus(http.StatusCreated))
	operator := alice.NewAI("relay")
	conn = alice.OperateConnector(conn.ID, operator)
	inst := fake.Instance(t, conn.ID)
	inst.WaitRun(t)
	return &inboundEnv{fake: fake, env: env, alice: alice, conn: conn, inst: inst, operator: operator}
}

func (e *inboundEnv) emit(m connectors.Inbound) { e.inst.Emit(context.Background(), m) }

func (e *inboundEnv) chats(t *testing.T) []dto.Chat {
	t.Helper()
	return testkit.Decode[dto.List[dto.Chat]](e.alice.Get("/api/v1/connectors/" + e.conn.ID + "/chats").RequireStatus(http.StatusOK)).Items
}

func (e *inboundEnv) chatID(t *testing.T, external string) string {
	t.Helper()
	for _, c := range e.chats(t) {
		if c.ExternalID == external {
			return c.ID
		}
	}
	t.Fatalf("no chat %s", external)
	return ""
}

func (e *inboundEnv) hasChat(t *testing.T, external string) bool {
	t.Helper()
	for _, c := range e.chats(t) {
		if c.ExternalID == external {
			return true
		}
	}
	return false
}

// count runs a SELECT COUNT(*) on the database.
func (e *inboundEnv) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(e.env.App.Config.DataDir, app.DatabaseFile)+"?mode=ro&_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	var n int
	require.NoError(t, raw.QueryRowContext(context.Background(), query, args...).Scan(&n))
	return n
}

func (e *inboundEnv) patch(t *testing.T, req dto.UpdateConnectorRequest) dto.Connector {
	t.Helper()
	return testkit.Decode[dto.Connector](e.alice.Patch("/api/v1/connectors/"+e.conn.ID, req).RequireStatus(http.StatusOK))
}

func (e *inboundEnv) connector(t *testing.T) dto.Connector {
	t.Helper()
	return testkit.Decode[dto.Connector](e.alice.Get("/api/v1/connectors/" + e.conn.ID).RequireStatus(http.StatusOK))
}

// queuedFor returns the AI's unacknowledged queue items, oldest first.
func (e *inboundEnv) queuedFor(t *testing.T, ai string) []platform.Event {
	t.Helper()
	list, err := e.env.App.Platform.Unacked(context.Background(), ai, 0, 1000)
	require.NoError(t, err)
	return list
}

// drain acknowledges everything the operator has queued, so that a test starts from an empty queue.
func (e *inboundEnv) drain(t *testing.T) {
	t.Helper()
	for _, it := range e.queued(t) {
		require.NoError(t, e.env.App.Platform.Ack(context.Background(), e.operator, it.ID))
	}
}

// queued returns the connection operator's unacknowledged queue items, oldest first.
func (e *inboundEnv) queued(t *testing.T) []platform.Event { return e.queuedFor(t, e.operator) }

func texts(list []platform.Event) []string {
	out := make([]string, len(list))
	for i, ev := range list {
		out[i] = ev.Text
	}
	return out
}

func (e *inboundEnv) messages(t *testing.T, roomID string) []dto.Message {
	t.Helper()
	return testkit.Decode[dto.Page[dto.Message]](e.alice.Get("/api/v1/rooms/" + roomID + "/messages").RequireStatus(http.StatusOK)).Items
}

// source is the connection as the manager names it to the pipeline.
func (e *inboundEnv) source() connectors.Source {
	return connectors.Source{ConnectorID: e.conn.ID, Kind: connectortest.KindName}
}

// pipeline returns a pipeline on the app's store and queue, with its own registry of sent ids.
func (e *inboundEnv) pipeline(sent *connectors.SentIDs) *connectors.Pipeline {
	a := e.env.App
	return connectors.NewPipeline(connectors.PipelineDeps{
		Store: a.Store, Bus: a.Bus, Blobs: a.Blobs, Identities: a.Identities, Trust: a.Authz, Queue: a.Platform, Sent: sent,
		MaxUploadBytes: a.Config.MaxUploadBytes, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// link links the sender to the user through the connection with a pairing command.
func (e *inboundEnv) link(t *testing.T, user *testkit.Client, sender, alt, name string) {
	t.Helper()
	code := testkit.Decode[dto.PairingCode](user.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: e.conn.ID}).RequireStatus(http.StatusCreated))
	e.emit(as(dmText(dmA, "link-"+sender, "!link "+code.Code), sender, alt, name))
}

func text(chat, id, body string) connectors.Inbound {
	return connectors.Inbound{
		ChatID: chat, ChatTitle: "Support group", ChatKind: "group", MessageID: id,
		SenderID: senderID, SenderAltID: senderAlt, SenderName: "Customer", Text: body, At: time.Now(),
	}
}

// mention is a group message that mentions the account.
func mention(chat, id, body string) connectors.Inbound {
	m := text(chat, id, body)
	m.MentionsAccount = true
	return m
}

// dmText is a direct message from the peer whose id is the chat's.
func dmText(chat, id, body string) connectors.Inbound {
	return connectors.Inbound{
		ChatID: chat, ChatTitle: "Customer", ChatKind: "dm", MessageID: id,
		SenderID: chat, SenderName: "Customer", Text: body, At: time.Now(),
	}
}

// as returns m written by another sender.
func as(m connectors.Inbound, id, alt, name string) connectors.Inbound {
	m.SenderID, m.SenderAltID, m.SenderName = id, alt, name
	return m
}

// CID-06: a group message that mentions the account and a direct message reach the operator's
// queue with every field of a platform event, oldest first, and the queue announces them.
func TestInboundMessagesReachTheOperatorsQueueWithEveryField(t *testing.T) {
	e := newInboundEnv(t)
	announcements, cancel := e.env.App.Bus.Subscribe(64)
	defer cancel()
	// The platform's timestamps are a month old (a message synced late): the queue keeps the
	// arrival time, so the 7 days of CID-10 start when partyline got the message.
	at := time.Now().Add(-30 * 24 * time.Hour).Truncate(time.Millisecond)
	before := time.Now().Truncate(time.Millisecond)
	group := mention(groupID, "M1", "where is my order?")
	group.At = at
	e.emit(group)
	dm := dmText(dmA, "D1", "oi")
	dm.At = at.Add(time.Second)
	e.emit(dm)

	got := e.queued(t)
	require.Len(t, got, 2)
	g, d := got[0], got[1]
	require.Equal(t, []any{connectortest.KindName, e.conn.ID, e.chatID(t, groupID), "Support group", dto.ChatKindGroup},
		[]any{g.Platform, g.ConnectionID, g.ChatID, g.ChatTitle, g.ChatKind})
	require.Equal(t, []any{"Customer", senderID, "where is my order?", dto.MessageKindText, false, false, "M1"},
		[]any{g.SenderName, g.SenderID, g.Text, g.Kind, g.Trusted, g.FromSelf, g.ExternalID})
	require.False(t, g.ReceivedAt.Before(before), "received_at is the arrival time, not the platform time: %v", g.ReceivedAt)
	require.False(t, g.ReceivedAt.After(time.Now()), "received_at is the arrival time: %v", g.ReceivedAt)
	require.Empty(t, g.Attachments)
	require.NotEmpty(t, g.ID)
	require.Equal(t, []any{connectortest.KindName, e.chatID(t, dmA), "Customer", dto.ChatKindDM, dmA, "oi"},
		[]any{d.Platform, d.ChatID, d.ChatTitle, d.ChatKind, d.SenderID, d.Text})
	require.Less(t, g.Seq, d.Seq)

	var announced []events.PlatformQueued
	for len(announcements) > 0 {
		if q, ok := (<-announcements).(events.PlatformQueued); ok {
			announced = append(announced, q)
		}
	}
	require.Len(t, announced, 2, "each queued message wakes the operator's session")
	require.Equal(t, []any{e.operator, g.ID, g.Seq}, []any{announced[0].AgentID, announced[0].ItemID, announced[0].Seq})
}

// CID-07: an inbound message is never a message of a conversation: no message, person, room,
// member or intelligence is created, and the chat is recorded as metadata only.
func TestInboundCreatesNoMessagePersonOrRoom(t *testing.T) {
	e := newInboundEnv(t)
	tables := []string{"messages", "attachments", "people", "chat_people", "rooms", "room_members", "intelligences", "read_cursors"}
	counts := func() []int {
		out := make([]int, len(tables))
		for i, table := range tables {
			out[i] = e.count(t, "SELECT COUNT(*) FROM "+table)
		}
		return out
	}
	before := counts()

	e.emit(mention(groupID, "M1", "hello"))
	e.emit(dmText(dmA, "D1", "oi"))
	e.emit(dmText(dmB, "D2", "oi de novo"))
	typed := dmText(dmA, "P1", "typed on the phone")
	typed.FromSelf = true
	e.emit(typed)

	require.Len(t, e.queued(t), 4, "all four reached the queue")
	require.Equal(t, before, counts(), "and nothing else was created")
	require.Len(t, e.chats(t), 3, "the chats are recorded")
}

// CON-07, CID-02: the chat is recorded (id, title, kind) and announced live for a message that
// reaches nobody: one a filter drops, one without text or media, and one of a connection nobody
// operates.
func TestChatsAreRecordedForMessagesThatReachNobody(t *testing.T) {
	e := newInboundEnv(t)
	live, _ := e.alice.DialLive("")

	e.emit(text(groupID, "M1", "no mention: dropped by mentions-only"))
	var discovered dto.Chat
	live.Expect(dto.LiveEventTypeChatDiscovered, &discovered, connectortest.Timeout)
	chats := e.chats(t)
	require.Len(t, chats, 1)
	require.Equal(t, discovered.ID, chats[0].ID)
	require.Equal(t, []any{groupID, "Support group", dto.ChatKindGroup, e.conn.ID}, []any{chats[0].ExternalID, chats[0].Title, chats[0].Kind, chats[0].ConnectorID})
	require.NotNil(t, chats[0].LastSeenAt)

	e.emit(dmText(dmA, "E1", "   "))
	require.True(t, e.hasChat(t, dmA), "a message without content still records its chat")

	idle := testkit.Decode[dto.Connector](e.alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "idle", Config: map[string]any{"token": "fake-token-not-real"}, Enabled: ptr(true),
	}).RequireStatus(http.StatusCreated))
	require.Nil(t, idle.Operator)
	e.pipeline(nil).Handle(context.Background(), connectors.Source{ConnectorID: idle.ID, Kind: connectortest.KindName}, dmText(dmA, "N1", "anyone there?"))
	idleChats := testkit.Decode[dto.List[dto.Chat]](e.alice.Get("/api/v1/connectors/" + idle.ID + "/chats").RequireStatus(http.StatusOK)).Items
	require.Len(t, idleChats, 1, "the chat of the idle connection is recorded")

	require.Empty(t, e.queued(t), "nothing was delivered")
	require.Zero(t, e.count(t, "SELECT COUNT(*) FROM platform_items"), "and nothing was queued for anyone")
}

// CID-12 (L-042): each condition of the filters excludes a message on its own. Every case varies
// one thing from a message that is delivered.
func TestDeliverAndMentionsOnlyFilters(t *testing.T) {
	e := newInboundEnv(t)
	dm := func(id string) connectors.Inbound { return dmText(dmA, id, "dm") }
	group := func(id string) connectors.Inbound { return text(groupID, id, "group") }
	groupMention := func(id string) connectors.Inbound { return mention(groupID, id, "group mention") }
	channel := func(id string) connectors.Inbound {
		m := text(otherID, id, "channel")
		m.ChatKind = "channel"
		return m
	}
	channelMention := func(id string) connectors.Inbound {
		m := channel(id)
		m.MentionsAccount = true
		return m
	}
	unknown := func(id string) connectors.Inbound {
		m := text("unknown-kind-chat", id, "unknown")
		m.ChatKind = ""
		return m
	}
	for i, c := range []struct {
		name         string
		deliver      dto.ConnectorDeliver
		mentionsOnly bool
		msg          func(id string) connectors.Inbound
		delivered    bool
	}{
		// mentions-only on: only group chats are filtered, by the mention alone.
		{"all/on: a direct message needs no mention", dto.ConnectorDeliverAll, true, dm, true},
		{"all/on: a group message without a mention is dropped", dto.ConnectorDeliverAll, true, group, false},
		{"all/on: a group message that mentions the account is delivered", dto.ConnectorDeliverAll, true, groupMention, true},
		{"all/on: a channel message without a mention is dropped", dto.ConnectorDeliverAll, true, channel, false},
		{"all/on: a channel message that mentions the account is delivered", dto.ConnectorDeliverAll, true, channelMention, true},
		{"all/on: a chat of unknown kind is not a group", dto.ConnectorDeliverAll, true, unknown, true},
		// mentions-only off: the setting alone lets the group message through.
		{"all/off: a group message without a mention is delivered", dto.ConnectorDeliverAll, false, group, true},
		{"all/off: a channel message without a mention is delivered", dto.ConnectorDeliverAll, false, channel, true},
		// deliver=dms: the chat kind alone excludes, mention or not.
		{"dms/off: a direct message is delivered", dto.ConnectorDeliverDMs, false, dm, true},
		{"dms/off: a group message is dropped", dto.ConnectorDeliverDMs, false, group, false},
		{"dms/off: a group message that mentions the account is dropped", dto.ConnectorDeliverDMs, false, groupMention, false},
		{"dms/off: a channel message is dropped", dto.ConnectorDeliverDMs, false, channel, false},
		{"dms/off: a chat of unknown kind is dropped", dto.ConnectorDeliverDMs, false, unknown, false},
		// deliver=groups: group and channel chats, never direct chats.
		{"groups/off: a group message is delivered", dto.ConnectorDeliverGroups, false, group, true},
		{"groups/off: a channel message is delivered", dto.ConnectorDeliverGroups, false, channel, true},
		{"groups/on: a group message that mentions the account is delivered", dto.ConnectorDeliverGroups, true, groupMention, true},
		{"groups/on: a group message without a mention is dropped", dto.ConnectorDeliverGroups, true, group, false},
		{"groups/off: a direct message is dropped", dto.ConnectorDeliverGroups, false, dm, false},
		{"groups/on: a direct message is dropped", dto.ConnectorDeliverGroups, true, dm, false},
		{"groups/off: a chat of unknown kind is dropped", dto.ConnectorDeliverGroups, false, unknown, false},
	} {
		deliver, mentionsOnly := c.deliver, c.mentionsOnly
		e.patch(t, dto.UpdateConnectorRequest{Deliver: &deliver, GroupsMentionsOnly: &mentionsOnly})
		before := len(e.queued(t))
		e.emit(c.msg(fmt.Sprintf("m%d", i)))
		want := before
		if c.delivered {
			want++
		}
		require.Len(t, e.queued(t), want, c.name)
	}
}

// CID-12: a reply to a message Partyline sent in the last minutes is recognised from memory too,
// before the delivery is recorded anywhere else.
func TestMentionsOnlyKeepsRepliesToRecentSends(t *testing.T) {
	e := newInboundEnv(t)
	sent := connectors.NewSentIDs(nil)
	p := e.pipeline(sent)
	p.Handle(context.Background(), e.source(), text(groupID, "M0", "first, dropped")) // records the chat
	sent.Add(e.chatID(t, groupID), "ext-9")

	reply := text(groupID, "R1", "answering you")
	reply.ReplyToID = "ext-9"
	p.Handle(context.Background(), e.source(), reply)
	other := text(groupID, "R2", "answering someone")
	other.ReplyToID = "ext-8"
	p.Handle(context.Background(), e.source(), other)

	require.Equal(t, []string{"answering you"}, texts(e.queued(t)))
}

// linkDirect links the sender to the user through the connection without the pairing flow, which the
// tests of identities cover: what is under test here is what a linked sender is trusted for.
func (e *inboundEnv) linkDirect(t *testing.T, user *testkit.Client, sender, alt, name string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, e.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.CreateIdentity(ctx, db.CreateIdentityParams{
			ID: store.NewID(), UserID: user.User.ID, ConnectorID: e.conn.ID, ConnectorKind: connectortest.KindName,
			ExternalID: sender, AltExternalID: alt, Display: name, CreatedAt: time.Now().UnixMilli(),
		})
	}))
}

// trustChart builds the organization the operator AI "relay" belongs to: alice created relay and holds
// the root position Admin, which now sits under Founder, held by boss. mate is a member with no
// relation to relay. It returns the organization's id and the id of the position Founder.
func (e *inboundEnv) trustChart(t *testing.T) (org, founder string, boss, mate *testkit.Client) {
	t.Helper()
	ctx := context.Background()
	org = e.alice.PersonalOrg().ID
	mine, err := e.env.App.Orgs.Get(ctx, e.alice.User.ID, org)
	require.NoError(t, err)
	founder = e.env.AddPosition(org, "Founder", "")
	_, err = e.env.App.Orgs.UpdatePosition(ctx, e.alice.User.ID, org, mine.PositionID, orgs.PositionUpdate{ParentID: &founder})
	require.NoError(t, err)
	boss, mate = e.env.NewUser("boss"), e.env.NewUser("mate")
	e.env.AddMember(org, boss.User.ID, orgs.PermissionMember, founder)
	e.env.AddMember(org, mate.User.ID, orgs.PermissionMember, "")
	return org, founder, boss, mate
}

// CID-13, POS-10, AD-018: a message is trusted only when its sender is a verified account, through this
// connection, of a user who manages the operator AI in the AI's home organization. Every other sender,
// and the account's own typing, is not.
func TestInboundTrustIsWhoManagesTheOperator(t *testing.T) {
	ctx := context.Background()
	e := newInboundEnv(t)
	org, founder, boss, mate := e.trustChart(t)
	// The operator is relay, which alice created: she manages it, and so does boss, who is above her.

	// visitor is the admin of an organization other than relay's home, where relay is a worker: visitor
	// manages relay there, which does not count.
	visitor := e.env.NewUser("visitor")
	visitorOrg := visitor.PersonalOrg().ID
	root, err := e.env.App.Orgs.Get(ctx, visitor.User.ID, visitorOrg)
	require.NoError(t, err)
	worker := e.env.AddPosition(visitorOrg, "Worker", root.PositionID)
	e.env.AddMember(visitorOrg, e.operator, orgs.PermissionMember, worker)

	// ex held Founder, like boss, and has left.
	ex := e.env.NewUser("former")
	e.env.AddMember(org, ex.User.ID, orgs.PermissionMember, founder)
	require.NoError(t, e.env.App.Orgs.Leave(ctx, ex.User.ID, org))

	e.link(t, e.alice, sender2, lid2, "Alice phone")
	e.linkDirect(t, boss, "5511999990011@s.whatsapp.net", "", "Boss phone")
	e.linkDirect(t, mate, sender3, "", "Mate phone")
	e.linkDirect(t, visitor, "5511999990012@s.whatsapp.net", "", "Visitor phone")
	e.linkDirect(t, ex, "5511999990013@s.whatsapp.net", "", "Ex phone")
	require.Empty(t, e.queued(t), "pairing commands are not delivered")

	// alice and boss also link an account on a second connection of alice's.
	second := testkit.Decode[dto.Connector](e.alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "second", Config: map[string]any{"token": "fake-token-not-real"}, Enabled: ptr(true),
	}).RequireStatus(http.StatusCreated))
	e.alice.OperateConnector(second.ID, e.operator)
	secondInst := e.fake.Instance(t, second.ID)
	secondInst.WaitRun(t)
	code := testkit.Decode[dto.PairingCode](e.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: second.ID}).RequireStatus(http.StatusCreated))
	const elsewhere = "5511999990077@s.whatsapp.net"
	secondInst.Emit(context.Background(), as(dmText(dmB, "link2", "!link "+code.Code), elsewhere, "", "Alice elsewhere"))

	for _, c := range []struct {
		name    string
		msg     connectors.Inbound
		trusted bool
	}{
		{"the account of the user who created the operator", as(dmText(dmA, "T1", "from alice"), sender2, "", "Alice phone"), true},
		{"that account seen under its other id", as(dmText(dmA, "T2", "from alice, lid"), "unrelated-id", lid2, "Alice phone"), true},
		{"the account of a user who manages the operator through a position above its creator's", as(dmText(dmA, "T3", "from boss"), "5511999990011@s.whatsapp.net", "", "Boss phone"), true},
		{"the account of a member who does not manage the operator", as(dmText(dmA, "T4", "from mate"), sender3, "", "Mate phone"), false},
		{"the account of a user who manages the operator only in another organization", as(dmText(dmA, "T5", "from visitor"), "5511999990012@s.whatsapp.net", "", "Visitor phone"), false},
		{"the account of a user who left the operator's organization", as(dmText(dmA, "T6", "from ex"), "5511999990013@s.whatsapp.net", "", "Ex phone"), false},
		{"an account nobody linked", as(dmText(dmA, "T7", "from a stranger"), "5511999990099@s.whatsapp.net", "", "Stranger"), false},
		{"a sender without an id", as(dmText(dmA, "T8", "from nobody"), "", "", ""), false},
		{"a sender named like the creator", as(dmText(dmA, "T9", "I am alice"), "5511999990098@s.whatsapp.net", "", "alice"), false},
		{"the creator's account linked on another connection", as(dmText(dmA, "T10", "from alice, second"), elsewhere, "", "Alice elsewhere"), false},
		{"a manager's account typing as the connection's own account", func() connectors.Inbound {
			m := as(dmText(dmA, "T11", "typed on the device"), "5511999990011@s.whatsapp.net", "", "Boss phone")
			m.FromSelf = true
			return m
		}(), false},
	} {
		e.emit(c.msg)
		got := e.queued(t)
		last := got[len(got)-1]
		require.Equal(t, c.msg.Text, last.Text, c.name)
		require.Equal(t, c.trusted, last.Trusted, c.name)
	}
}

// AD-018: a platform event is judged when it arrives and keeps that verdict, unlike a message of a room.
// A manager who leaves afterwards does not turn the events already queued into untrusted ones, and the
// next event of the same account is untrusted.
func TestInboundTrustIsDecidedWhenTheMessageArrives(t *testing.T) {
	e := newInboundEnv(t)
	org, _, boss, _ := e.trustChart(t)
	const bossPhone = "5511999990011@s.whatsapp.net"
	e.linkDirect(t, boss, bossPhone, "", "Boss phone")

	e.emit(as(dmText(dmA, "D1", "before"), bossPhone, "", "Boss phone"))
	require.NoError(t, e.env.App.Orgs.Leave(context.Background(), boss.User.ID, org))
	e.emit(as(dmText(dmA, "D2", "after"), bossPhone, "", "Boss phone"))

	got := e.queued(t)
	require.Len(t, got, 2)
	require.Equal(t, []any{"before", true, "after", false}, []any{got[0].Text, got[0].Trusted, got[1].Text, got[1].Trusted})
}

// CID-26: the same platform message arriving twice is queued once, and its media are not
// downloaded again; messages without a platform id cannot be told apart.
func TestInboundRepeatIsQueuedOnceAndDownloadsNothing(t *testing.T) {
	e := newInboundEnv(t)
	e.patch(t, dto.UpdateConnectorRequest{GroupsMentionsOnly: ptr(false)})

	var opens atomic.Int32
	m := text(groupID, "M2", "photo")
	m.Kind = "image"
	m.Media = []connectors.Media{{Name: "p.png", Mime: "image/png", Open: func(ctx context.Context) (io.ReadCloser, error) {
		opens.Add(1)
		return opener([]byte("synthetic image bytes"))(ctx)
	}}}
	e.emit(m)
	e.emit(m)

	require.Equal(t, int32(1), opens.Load(), "the repeat opened no media")
	got := e.queued(t)
	require.Len(t, got, 1)
	require.Len(t, got[0].Attachments, 1)
	require.Equal(t, 1, blobCount(t, e.env))

	e.emit(text(otherID, "M2", "the same platform id in another chat is another message"))
	require.Len(t, e.queued(t), 2)
	e.emit(text(groupID, "", "no id"))
	e.emit(text(groupID, "", "no id"))
	require.Len(t, e.queued(t), 4, "a platform that gives no id cannot be told apart")
}

// CID-26: an acknowledged message is still a repeat for as long as its row stays.
func TestInboundRepeatOfAnAcknowledgedMessageIsNotQueuedAgain(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(dmText(dmA, "D1", "oi"))
	first := e.queued(t)[0]
	require.NoError(t, e.env.App.Platform.Ack(context.Background(), e.operator, first.ID))

	e.emit(dmText(dmA, "D1", "oi"))
	require.Empty(t, e.queued(t))
	e.emit(dmText(dmA, "D2", "oi?"))
	require.Equal(t, []string{"oi?"}, texts(e.queued(t)))
}

// CON-11: media within the upload limit is stored as blobs and listed with the item; larger media
// is recorded as too_large without a blob; a failed download is unavailable.
func TestInboundMedia(t *testing.T) {
	e := newInboundEnv(t)
	e.patch(t, dto.UpdateConnectorRequest{GroupsMentionsOnly: ptr(false)})

	small := []byte("synthetic image bytes")
	big := bytes.Repeat([]byte{1}, 1<<20+1)
	m := text(groupID, "M2", "photos")
	m.Kind = "image"
	m.Media = []connectors.Media{
		{Name: "small.png", Mime: "image/png", Open: opener(small)},
		{Name: "big.png", Mime: "image/png", Open: opener(big)},
		{Name: "declared.bin", Mime: "application/octet-stream", Size: 1<<20 + 1, Open: func(context.Context) (io.ReadCloser, error) {
			t.Error("media declared too large must not be downloaded")
			return io.NopCloser(bytes.NewReader(nil)), nil
		}},
		{Name: "gone.png", Mime: "image/png", Open: func(context.Context) (io.ReadCloser, error) { return nil, fmt.Errorf("platform said no") }},
		{Open: opener([]byte("anonymous bytes"))},
	}
	e.emit(m)

	got := e.queued(t)
	require.Len(t, got, 1)
	require.Equal(t, dto.MessageKindImage, got[0].Kind)
	atts := got[0].Attachments
	require.Len(t, atts, 5)
	require.Equal(t, []any{"small.png", "image/png", dto.AttachmentStatusStored, int64(len(small))}, []any{atts[0].Name, atts[0].Mime, atts[0].Status, atts[0].Size})
	require.NotEmpty(t, atts[0].SHA256)
	blob, err := e.env.App.Blobs.Open(atts[0].SHA256)
	require.NoError(t, err)
	content, err := io.ReadAll(blob)
	require.NoError(t, err)
	require.NoError(t, blob.Close())
	require.Equal(t, small, content, "the blob holds the file")

	require.Equal(t, []any{"big.png", dto.AttachmentStatusTooLarge, ""}, []any{atts[1].Name, atts[1].Status, atts[1].SHA256})
	require.Equal(t, []any{"declared.bin", dto.AttachmentStatusTooLarge, int64(1<<20 + 1), ""}, []any{atts[2].Name, atts[2].Status, atts[2].Size, atts[2].SHA256})
	require.Equal(t, []any{"gone.png", dto.AttachmentStatusUnavailable, ""}, []any{atts[3].Name, atts[3].Status, atts[3].SHA256})
	require.Equal(t, []any{"file", "application/octet-stream", dto.AttachmentStatusStored}, []any{atts[4].Name, atts[4].Mime, atts[4].Status}, "defaults for a file without a name or type")
	require.Equal(t, 2, blobCount(t, e.env), "only the two readable files are in the blob store")
}

// CON-11, S8: a download the platform client cuts off at the upload limit is recorded as
// too_large, not unavailable.
func TestInboundMediaCutOffByTheClientCapIsTooLarge(t *testing.T) {
	e := newInboundEnv(t)
	e.patch(t, dto.UpdateConnectorRequest{GroupsMentionsOnly: ptr(false)})
	m := text(groupID, "M2", "video")
	m.Media = []connectors.Media{{Name: "clip.mp4", Mime: "video/mp4", Open: func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(io.MultiReader(bytes.NewReader([]byte("partial")), errReader{fmt.Errorf("file download: %w", safehttp.ErrResponseTooLarge)})), nil
	}}}
	e.emit(m)

	got := e.queued(t)
	require.Len(t, got, 1)
	require.Len(t, got[0].Attachments, 1)
	require.Equal(t, dto.AttachmentStatusTooLarge, got[0].Attachments[0].Status)
	require.Zero(t, blobCount(t, e.env))
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// A message with media and no text is delivered; a message with neither is not.
func TestInboundMediaOnlyMessageIsDelivered(t *testing.T) {
	e := newInboundEnv(t)
	m := dmText(dmA, "D1", "")
	m.Kind = "image"
	m.Media = []connectors.Media{{Name: "p.png", Mime: "image/png", Open: opener([]byte("bytes"))}}
	e.emit(m)
	e.emit(dmText(dmA, "D2", ""))

	got := e.queued(t)
	require.Len(t, got, 1)
	require.Empty(t, got[0].Text)
	require.Len(t, got[0].Attachments, 1)
}

// CON-14: inbound text over 16,000 characters (code points) is queued cut to 16,000, ending
// with the truncation marker; text at the limit is queued as is.
func TestInboundLongTextIsTruncated(t *testing.T) {
	e := newInboundEnv(t)
	e.patch(t, dto.UpdateConnectorRequest{GroupsMentionsOnly: ptr(false)})

	long := strings.Repeat("é", 20000)
	e.emit(text(groupID, "M2", long))
	exact := strings.Repeat("a", dto.MaxTextLength)
	e.emit(text(groupID, "M3", exact))
	// 20,000 bytes but 10,000 code points: the limit counts code points, not bytes.
	multiByte := strings.Repeat("é", 10000)
	e.emit(text(groupID, "M4", multiByte))

	got := e.queued(t) // oldest first
	require.Len(t, got, 3)
	cut := got[0].Text
	require.Equal(t, 16000, utf8.RuneCountInString(cut))
	require.Equal(t, strings.Repeat("é", 16000-utf8.RuneCountInString("…[truncated]"))+"…[truncated]", cut)
	require.Equal(t, exact, got[1].Text)
	require.Equal(t, multiByte, got[2].Text, "under 16,000 code points is queued unchanged")
}

func opener(data []byte) func(context.Context) (io.ReadCloser, error) {
	return func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
}

func blobCount(t *testing.T, env *testkit.Env) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(filepath.Join(env.App.Config.DataDir, app.BlobsDir), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			n++
		}
		return err
	})
	require.NoError(t, err)
	return n
}

// IDN-02, IDN-03: "!link CODE" and "/link code" link the sender and are never delivered; unknown
// codes link nothing and are not delivered either. CID-13: later messages of the linked sender are
// trusted.
func TestInboundPairingCommands(t *testing.T) {
	e := newInboundEnv(t)

	e.emit(dmText(dmA, "M2", "!link ZZZZZZ"))
	require.Empty(t, testkit.Decode[dto.List[dto.Identity]](e.alice.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items)

	code := testkit.Decode[dto.PairingCode](e.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: e.conn.ID}).RequireStatus(http.StatusCreated))
	e.emit(as(dmText(dmA, "M3", "  /link "+strings.ToLower(code.Code)+" "), senderID, senderAlt, "Customer"))
	ids := testkit.Decode[dto.List[dto.Identity]](e.alice.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items
	require.Len(t, ids, 1)
	require.Equal(t, e.conn.ID, ids[0].ConnectorID)
	require.Equal(t, connectortest.KindName, ids[0].ConnectorKind)
	require.Equal(t, senderID, ids[0].ExternalID)
	require.Equal(t, senderAlt, ids[0].AltExternalID)

	code2 := testkit.Decode[dto.PairingCode](e.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: e.conn.ID}).RequireStatus(http.StatusCreated))
	e.emit(as(dmText(dmA, "M4", "!link "+code2.Code), senderID, senderAlt, "Customer"))
	require.Empty(t, e.queued(t), "pairing commands are not delivered")
	require.Empty(t, e.chats(t), "and record no chat")

	e.emit(as(dmText(dmA, "M5", "it is me"), senderID, senderAlt, "Customer"))
	got := e.queued(t)
	require.Len(t, got, 1)
	require.True(t, got[0].Trusted, "a verified sender is the owner")
}

// IDN-02 (validation probe S5): alice's code typed through mallory's connector, which listens
// in the same chat, links nothing there, is not delivered to mallory's AI, and still links through
// alice's own connector.
func TestInboundPairingCodeOnlyLinksOnItsConnector(t *testing.T) {
	e := newInboundEnv(t)
	mallory := e.env.NewUser("mallory")
	mconn := testkit.Decode[dto.Connector](mallory.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "listener", Config: map[string]any{"token": "fake-token-not-real"}, Enabled: ptr(true),
	}).RequireStatus(http.StatusCreated))
	malloryAI := mallory.NewAI("relay")
	mallory.OperateConnector(mconn.ID, malloryAI)
	minst := e.fake.Instance(t, mconn.ID)
	minst.WaitRun(t)

	code := testkit.Decode[dto.PairingCode](e.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: e.conn.ID}).RequireStatus(http.StatusCreated))
	minst.Emit(context.Background(), dmText(dmA, "M2", "!link "+code.Code))
	require.Empty(t, testkit.Decode[dto.List[dto.Identity]](e.alice.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items,
		"mallory's connector linked nothing")
	require.Empty(t, e.queuedFor(t, malloryAI), "the command is not delivered")

	e.emit(dmText(dmA, "M3", "!link "+code.Code))
	ids := testkit.Decode[dto.List[dto.Identity]](e.alice.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items
	require.Len(t, ids, 1)
	require.Equal(t, e.conn.ID, ids[0].ConnectorID)
}

// TG-01 edge case: a migrated chat keeps its record under the new platform id, and the messages
// that arrive there are the same chat's; a migration onto an existing chat changes nothing.
func TestMigrateChatKeepsTheChatRecord(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(mention("-4000000001", "M1", "before"))
	chat := e.chatID(t, "-4000000001")

	e.inst.MigrateChat(context.Background(), "-4000000001", "-1001000000001")
	require.Equal(t, chat, e.chatID(t, "-1001000000001"), "same chat, new external id")
	require.Len(t, e.chats(t), 1)

	e.emit(mention("-1001000000001", "M2", "after"))
	got := e.queued(t)
	require.Len(t, got, 2)
	require.Equal(t, chat, got[1].ChatID, "messages at the new id are the chat's")

	e.emit(mention(otherID, "M3", "x"))
	e.inst.MigrateChat(context.Background(), "-1001000000001", otherID)
	require.Equal(t, chat, e.chatID(t, "-1001000000001"), "the new id was already a chat: nothing moves")
}

// CTX-08: a changed title on a later inbound message updates the stored chat, and the items carry
// the chat's current title; an empty title keeps the stored one.
func TestInboundTitleChangeUpdatesTheChat(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(mention(groupID, "M1", "hello"))

	renamed := mention(groupID, "M2", "again")
	renamed.ChatTitle = "Support group (renamed)"
	e.emit(renamed)
	require.Equal(t, "Support group (renamed)", e.chats(t)[0].Title)
	untitled := mention(groupID, "M3", "no title this time")
	untitled.ChatTitle = ""
	e.emit(untitled)
	require.Equal(t, "Support group (renamed)", e.chats(t)[0].Title)

	got := e.queued(t)
	require.Len(t, got, 3)
	for _, it := range got {
		require.Equal(t, "Support group (renamed)", it.ChatTitle, "an item carries its chat's current title")
	}
}

// CID-01, CID-05: the operator of the moment receives what arrives; when the owner picks another
// AI, what was queued and not yet delivered goes with the connection (the old operator keeps
// nothing of it), and the next messages queue behind it, oldest first.
func TestInboundGoesToTheCurrentOperator(t *testing.T) {
	e := newInboundEnv(t)
	atlas := e.alice.NewAI("Atlas")
	e.emit(dmText(dmA, "D1", "to relay"))

	e.alice.OperateConnector(e.conn.ID, atlas)
	e.emit(dmText(dmA, "D2", "to atlas"))

	require.Empty(t, texts(e.queued(t)), "the first operator keeps nothing of the connection")
	require.Equal(t, []string{"to relay", "to atlas"}, texts(e.queuedFor(t, atlas)))
}

func ptr[T any](v T) *T { return &v }
