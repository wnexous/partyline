package connectors_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// notifyEnv is alice's connector "wa" with a catch-all room (the way v0.3 routes DMs) and her
// phone, a DM chat of the account linked to her. Her AI Atlas can write to her.
type notifyEnv struct {
	*inboundEnv
	atlasID string
	atlas   *testkit.Client // Atlas's REST client
	phone   string          // the id of the chat of alice's phone
	inbox   dto.Room        // the catch-all room
}

// phoneChat is a DM from the account behind chat, named name.
func phoneChat(chat, id, body, name string) connectors.Inbound {
	m := dmText(chat, id, body)
	m.ChatTitle, m.SenderName = name, name
	return m
}

func newNotifyEnv(t *testing.T, opts ...testkit.Option) *notifyEnv {
	t.Helper()
	e := newInboundEnv(t, append([]testkit.Option{testkit.WithOutbox()}, opts...)...)
	n := &notifyEnv{inboundEnv: e, atlasID: e.env.NewAI(e.alice, "atlas")}
	e.alice.Patch("/api/v1/intelligences/"+n.atlasID, dto.UpdateProfileRequest{DisplayName: ptr("Atlas")}).RequireStatus(http.StatusOK)
	n.atlas = e.alice.Token(dto.TokenKindAgent, "laptop").AsAgent("atlas")
	n.inbox = testkit.Decode[dto.Room](e.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "inbox"}).RequireStatus(http.StatusCreated))
	e.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeDMs), AutoRouteRoomID: &n.inbox.ID})
	n.phone = e.linkPhone(t, e.inst, e.conn.ID, e.alice, dmA, "Alice phone")
	return n
}

// linkPhone links the account that writes in the DM chat to the user through the connector and
// has it write once more, so that the chat is routed and its person is known. It returns the
// chat's id.
func (e *inboundEnv) linkPhone(t *testing.T, inst *connectortest.Instance, connectorID string, user *testkit.Client, chat, name string) string {
	t.Helper()
	code := testkit.Decode[dto.PairingCode](user.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: connectorID}).RequireStatus(http.StatusCreated))
	inst.Emit(t.Context(), phoneChat(chat, "link-"+chat, "!link "+code.Code, name))
	inst.Emit(t.Context(), phoneChat(chat, "hello-"+chat, "hello from my phone", name))
	for _, c := range testkit.Decode[dto.List[dto.Chat]](e.alice.Get("/api/v1/connectors/" + connectorID + "/chats").RequireStatus(http.StatusOK)).Items {
		if c.ExternalID == chat {
			return c.ID
		}
	}
	t.Fatalf("no chat %s", chat)
	return ""
}

func options(t *testing.T, c *testkit.Client) []dto.NotifyOption {
	t.Helper()
	return testkit.Decode[dto.List[dto.NotifyOption]](c.Get("/api/v1/me/notify-options").RequireStatus(http.StatusOK)).Items
}

// choose sets the user's notify chat and returns the account as the server then reports it.
func choose(t *testing.T, c *testkit.Client, chat string) dto.Me {
	t.Helper()
	return testkit.Decode[dto.Me](c.Put("/api/v1/me/notify", dto.SetNotifyRequest{ChatID: &chat}).RequireStatus(http.StatusOK))
}

func clearChoice(t *testing.T, c *testkit.Client) dto.Me {
	t.Helper()
	return testkit.Decode[dto.Me](c.Put("/api/v1/me/notify", dto.SetNotifyRequest{}).RequireStatus(http.StatusOK))
}

func me(t *testing.T, c *testkit.Client) dto.Me {
	t.Helper()
	return testkit.Decode[dto.Me](c.Get("/api/v1/me").RequireStatus(http.StatusOK))
}

// dm has Atlas write to alice, opening their DM when it does not exist.
func (n *notifyEnv) dm(t *testing.T, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](n.atlas.Post("/api/v1/agent/dms", dto.AgentDMRequest{To: "@alice", Text: text}).RequireStatus(http.StatusCreated))
}

// notifyRows counts the copies queued so far, whatever their state.
func (n *notifyEnv) notifyRows(t *testing.T) int {
	t.Helper()
	return n.count(t, "SELECT COUNT(*) FROM outbox WHERE purpose = 'notify'")
}

// copyOf returns the copy of the message sent to the user's app once it satisfies done; viewer
// is a user who can read the DM.
func (n *notifyEnv) copyOf(t *testing.T, viewer *testkit.Client, roomID, messageID string, done func(dto.OutboxItem) bool) dto.OutboxItem {
	t.Helper()
	deadline := time.Now().Add(connectortest.Timeout)
	for {
		for _, m := range testkit.Decode[dto.Page[dto.Message]](viewer.Get("/api/v1/rooms/" + roomID + "/messages").RequireStatus(http.StatusOK)).Items {
			if m.ID != messageID {
				continue
			}
			for _, o := range m.Outbox {
				if o.Purpose == dto.OutboxPurposeNotify && done(o) {
					return o
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the copy of %s never reached the expected state", messageID)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func status(s dto.OutboxStatus) func(dto.OutboxItem) bool {
	return func(o dto.OutboxItem) bool { return o.Status == s }
}

func attempts(n int) func(dto.OutboxItem) bool {
	return func(o dto.OutboxItem) bool { return o.Attempts >= n }
}

// NOTI-01: the options are the DM chats of the caller's linked accounts on connectors they may
// use; a group chat, an account nobody linked, an account linked through another connector and
// somebody else's account are no options.
func TestNotifyOptionsAreTheCallersLinkedDMChats(t *testing.T) {
	n := newNotifyEnv(t)
	n.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeAll)})
	n.emit(phoneChat(dmB, "B1", "who am I", "Stranger")) // a DM chat whose account nobody linked
	n.emit(as(text(groupID, "G1", "from the group"), dmA, "", "Alice phone"))

	want := []dto.NotifyOption{{
		ChatID: n.phone, Title: "Alice phone", Account: "Alice phone",
		Connector: dto.ConnectorRef{ID: n.conn.ID, Kind: connectortest.KindName, Name: "wa"},
	}}
	require.Equal(t, want, options(t, n.alice), "the phone only: not the group, not the stranger's DM")

	// The same account on a second connector of alice is linked there only if she linked it there.
	second := testkit.Decode[dto.Connector](n.alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "wb", Config: map[string]any{"token": "fake-token-not-real"}, Enabled: ptr(true),
	}).RequireStatus(http.StatusCreated))
	inst := n.fake.Instance(t, second.ID)
	inst.WaitRun(t)
	n.alice.Patch("/api/v1/connectors/"+second.ID, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeDMs), AutoRouteRoomID: &n.inbox.ID}).RequireStatus(http.StatusOK)
	inst.Emit(t.Context(), phoneChat(dmA, "W1", "same number, other connector", "Alice phone"))
	require.Equal(t, want, options(t, n.alice), "an identity belongs to the connector it was linked through (AD-008)")

	// Linking it there adds that chat.
	other := n.linkPhone(t, inst, second.ID, n.alice, dmA, "Alice phone")
	got := options(t, n.alice)
	require.Len(t, got, 2)
	require.Equal(t, []string{n.phone, other}, []string{got[0].ChatID, got[1].ChatID}, "ordered by connector name")

	bob := n.env.NewUser("bob")
	require.Empty(t, options(t, bob), "another user's accounts are theirs")
}

// NOTI-01: an account is linked to a person by either of its ids (WhatsApp reports a phone number
// and a LID for the same account): an identity linked under one id matches a person known under the
// other, in both directions.
func TestNotifyOptionsMatchAccountsByAlternativeIds(t *testing.T) {
	n := newNotifyEnv(t)
	dm := func(chat, id, body, sender, alt string) connectors.Inbound {
		m := phoneChat(chat, id, body, "Alice other phone")
		m.SenderID, m.SenderAltID = sender, alt
		return m
	}
	link := func(chat, sender, alt string) string {
		code := testkit.Decode[dto.PairingCode](n.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: n.conn.ID}).RequireStatus(http.StatusCreated))
		n.emit(dm(chat, "link-"+chat, "!link "+code.Code, sender, alt))
		return code.Code
	}

	// The person is known by its phone with the LID as alternative; the identity was linked under the LID.
	link(dmB, senderAlt, "")
	n.emit(dm(dmB, "B1", "from the same account", senderID, senderAlt))
	// The person is known by its LID only; the identity was linked under the phone with the LID as alternative.
	link(dmC, sender2, lid2)
	n.emit(dm(dmC, "C1", "from the same account", lid2, ""))

	got := options(t, n.alice)
	require.Len(t, got, 3)
	require.ElementsMatch(t, []string{n.phone, n.chatID(t, dmB), n.chatID(t, dmC)}, []string{got[0].ChatID, got[1].ChatID, got[2].ChatID})
}

// NOTI-01: a user who may use the connector sees the chats of the accounts they linked through
// it, never alice's, and loses them with the access.
func TestNotifyOptionsFollowTheConnectorAccess(t *testing.T) {
	n := newNotifyEnv(t)
	carol := n.env.NewUser("carol")
	share := func(mode dto.Visibility) {
		t.Helper()
		sharing := dto.Sharing{Mode: mode}
		if mode == dto.VisibilityShared {
			sharing.Users = []dto.SharingUser{{UserID: carol.User.ID, Level: dto.LevelUse}}
		}
		n.alice.Put("/api/v1/connectors/"+n.conn.ID+"/sharing", sharing).RequireStatus(http.StatusOK)
	}

	share(dto.VisibilityShared)
	tablet := n.linkPhone(t, n.inst, n.conn.ID, carol, dmB, "Carol phone")
	got := options(t, carol)
	require.Len(t, got, 1)
	require.Equal(t, []string{tablet, "Carol phone"}, []string{got[0].ChatID, got[0].Account})
	require.Equal(t, n.phone, options(t, n.alice)[0].ChatID, "alice keeps her own")
	require.Len(t, options(t, n.alice), 1)
	require.Equal(t, tablet, choose(t, carol, tablet).NotifyChatID)

	share(dto.VisibilityPrivate)
	require.Empty(t, options(t, carol), "no access to the connector, no option")
	carol.Put("/api/v1/me/notify", dto.SetNotifyRequest{ChatID: &tablet}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)

	// Public use gives the access back.
	share(dto.VisibilityPublic)
	require.Len(t, options(t, carol), 1)
}

// NOTI-01: choosing one of the options saves it (idempotent), anything else is 422 with nothing
// changed, and null clears the choice.
func TestChooseWhereToBeNotified(t *testing.T) {
	n := newNotifyEnv(t)
	require.Empty(t, me(t, n.alice).NotifyChatID, "nothing chosen at first")

	require.Equal(t, n.phone, choose(t, n.alice, n.phone).NotifyChatID)
	require.Equal(t, n.phone, choose(t, n.alice, n.phone).NotifyChatID, "choosing it again changes nothing")
	require.Equal(t, n.phone, me(t, n.alice).NotifyChatID)
	require.Equal(t, n.phone, testkit.Decode[dto.Me](n.alice.Patch("/api/v1/me", dto.UpdateMeRequest{Locale: ptr("pt-BR")}).RequireStatus(http.StatusOK)).NotifyChatID)

	// Chats that exist but are no option of alice, and ids that exist nowhere.
	n.patch(t, dto.UpdateConnectorRequest{AutoRouteScope: scopeOf(dto.AutoRouteScopeAll)})
	n.emit(phoneChat(dmB, "B1", "who am I", "Stranger"))
	n.emit(as(text(groupID, "G1", "from the group"), dmA, "", "Alice phone"))
	bob := n.env.NewUser("bob")
	for name, chat := range map[string]string{
		"a DM nobody linked": n.chatID(t, dmB), "a group chat": n.chatID(t, groupID),
		"an unknown id": "no-such-chat", "an empty id": "", "a room id": n.inbox.ID,
	} {
		e := n.alice.Put("/api/v1/me/notify", dto.SetNotifyRequest{ChatID: &chat}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Equal(t, map[string]string{"chat_id": "must be one of your notify options"}, e.Fields, name)
		require.Equal(t, n.phone, me(t, n.alice).NotifyChatID, "%s: the choice stays", name)
	}
	phone := n.phone
	bob.Put("/api/v1/me/notify", dto.SetNotifyRequest{ChatID: &phone}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Empty(t, me(t, bob).NotifyChatID, "alice's chat is not an option of bob")
	require.Equal(t, n.phone, me(t, n.alice).NotifyChatID)

	body := n.alice.Do(http.MethodPut, "/api/v1/me/notify", map[string]any{"chat_id": nil}).RequireStatus(http.StatusOK)
	require.NotContains(t, body.String(), "notify_chat_id", "null clears it")
	require.Empty(t, me(t, n.alice).NotifyChatID)
	require.Empty(t, clearChoice(t, n.alice).NotifyChatID, "clearing twice is fine")
}

// Authorization of the two routes: users only (a session or a user token), each for themselves;
// anonymous callers get 401 and agent tokens 403.
func TestNotifyRoutesAuthorization(t *testing.T) {
	n := newNotifyEnv(t)
	routes := map[string]func(c *testkit.Client) *testkit.Response{
		"options": func(c *testkit.Client) *testkit.Response { return c.Get("/api/v1/me/notify-options") },
		"choose": func(c *testkit.Client) *testkit.Response {
			return c.Put("/api/v1/me/notify", dto.SetNotifyRequest{ChatID: ptr(n.phone)})
		},
		"clear": func(c *testkit.Client) *testkit.Response { return c.Put("/api/v1/me/notify", dto.SetNotifyRequest{}) },
	}
	agent := n.alice.Token(dto.TokenKindAgent, "bot")
	for _, call := range routes {
		call(n.env.Anon()).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
		call(agent).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		call(n.atlas).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
		call(n.alice).RequireStatus(http.StatusOK)
	}

	token := n.alice.Token(dto.TokenKindUser, "script")
	require.Equal(t, n.phone, choose(t, token, n.phone).NotifyChatID, "a user token acts as the user")
	require.Len(t, options(t, token), 1)
	require.Equal(t, n.phone, me(t, n.alice).NotifyChatID)
}

// notifiedUser creates a user after Atlas, lets them use alice's connector, links their phone (a DM
// chat of the account) through it and chooses it to be notified there.
func (n *notifyEnv) notifiedUser(t *testing.T, name, chat string) (*testkit.Client, string) {
	t.Helper()
	user := n.env.NewUser(name)
	n.alice.Put("/api/v1/connectors/"+n.conn.ID+"/sharing", dto.Sharing{
		Mode: dto.VisibilityShared, Users: []dto.SharingUser{{UserID: user.User.ID, Level: dto.LevelUse}},
	}).RequireStatus(http.StatusOK)
	phone := n.linkPhone(t, n.inst, n.conn.ID, user, chat, name+" phone")
	choose(t, user, phone)
	return user, phone
}

// NOTI-02 (L-044): the copy goes to the user of the DM whichever of the two sorts first. Here the AI
// was created before the user, so it is the first participant (dm_a) of their DM.
func TestAIDMIsCopiedToTheAppOfAUserThatSortsAfterTheAI(t *testing.T) {
	n := newNotifyEnv(t)
	bob, phone := n.notifiedUser(t, "bob", dmB) // created after Atlas: Atlas sorts first
	require.Less(t, n.atlasID, bob.User.ID, "Atlas is the first participant of the DM")

	msg := testkit.Decode[dto.Message](n.atlas.Post("/api/v1/agent/dms", dto.AgentDMRequest{To: "@bob", Text: "your report is ready"}).RequireStatus(http.StatusCreated))

	sent := n.inst.WaitSend(t)
	require.Equal(t, []string{dmB, "Atlas: your report is ready"}, []string{sent.ChatID, sent.Text}, "bob's app, not alice's")
	item := n.copyOf(t, bob, msg.RoomID, msg.ID, status(dto.OutboxStatusSent))
	require.Equal(t, []any{phone, dto.OutboxPurposeNotify}, []any{item.ChatID, item.Purpose})
	require.Equal(t, 1, n.notifyRows(t), "one copy, for bob's app only")
}

// benignLive are the frames a viewer of the DM may get while a copy is delivered: everything but
// the status of the copy.
var benignLive = []dto.LiveEventType{
	dto.LiveEventTypeHello, dto.LiveEventTypeRoomMessage, dto.LiveEventTypeRoomChanged, dto.LiveEventTypeMembersChanged,
	dto.LiveEventTypeRoomRead, dto.LiveEventTypeAgentPresence, dto.LiveEventTypeConnectorStatus,
	dto.LiveEventTypeChatDiscovered, dto.LiveEventTypeIntelligenceChanged, dto.LiveEventTypeAccessChanged,
}

// NOTI-02, NOTI-03, DM-08: the delivery of the copy (the id of the user's chat and its errors) is for
// the user the DM is with. The AI's owner and a user granted use on the AI read the DM, and the AI wrote
// the message, but none of them gets the copy's row, in a timeline, in the AI's answers and history or
// in live frames and status events; the user gets all of it.
func TestTheCopysDeliveryIsVisibleOnlyToTheUserOfTheDM(t *testing.T) {
	n := newNotifyEnv(t)
	bob, phone := n.notifiedUser(t, "bob", dmB)
	carol := n.env.NewUser("carol")
	n.alice.Put("/api/v1/agents/"+n.atlasID+"/sharing", dto.Sharing{
		Mode: dto.VisibilityShared, Users: []dto.SharingUser{{UserID: carol.User.ID, Level: dto.LevelUse}},
	}).RequireStatus(http.StatusOK)
	live := map[string]*testkit.LiveConn{}
	for name, c := range map[string]*testkit.Client{"bob": bob, "alice": n.alice, "carol": carol} {
		live[name], _ = c.DialLive("")
	}

	msg := testkit.Decode[dto.Message](n.atlas.Post("/api/v1/agent/dms", dto.AgentDMRequest{To: "@bob", Text: "your report is ready"}).RequireStatus(http.StatusCreated))
	require.Empty(t, msg.Outbox, "not even the AI that wrote the message gets the copy's delivery")
	n.inst.WaitSend(t)

	// bob's timeline shows the copy and its delivery, and his socket hears of both.
	item := n.copyOf(t, bob, msg.RoomID, msg.ID, status(dto.OutboxStatusSent))
	require.Equal(t, []any{phone, dto.OutboxPurposeNotify}, []any{item.ChatID, item.Purpose})
	var posted dto.Message
	live["bob"].Expect(dto.LiveEventTypeRoomMessage, &posted, connectortest.Timeout)
	require.Len(t, posted.Outbox, 1, "the live message carries the copy")
	var update dto.OutboxUpdate
	for update.Status != dto.OutboxStatusSent {
		live["bob"].Expect(dto.LiveEventTypeOutboxStatus, &update, connectortest.Timeout)
	}
	require.Equal(t, []any{msg.RoomID, msg.ID, phone}, []any{update.RoomID, update.MessageID, update.ChatID})

	// Everyone else who reads the DM sees the message and nothing of the copy.
	for name, viewer := range map[string]*testkit.Client{"the AI's owner": n.alice, "a use grantee": carol} {
		items := testkit.Decode[dto.Page[dto.Message]](viewer.Get("/api/v1/rooms/" + msg.RoomID + "/messages").RequireStatus(http.StatusOK)).Items
		require.Len(t, items, 1, name)
		require.Equal(t, "your report is ready", items[0].Text, name)
		require.Empty(t, items[0].Outbox, name)
	}
	history := testkit.Decode[dto.Page[dto.AgentMessage]](n.atlas.Get("/api/v1/agent/rooms/" + msg.RoomID + "/messages").RequireStatus(http.StatusOK)).Items
	require.Len(t, history, 1)
	require.Empty(t, history[0].Message.Outbox, "nor the AI's history")
	for _, who := range []string{"alice", "carol"} {
		var seen dto.Message
		live[who].Expect(dto.LiveEventTypeRoomMessage, &seen, connectortest.Timeout)
		require.Empty(t, seen.Outbox, "%s: the live message", who)
		live[who].ExpectNone(300*time.Millisecond, benignLive...) // no outbox.status for them
	}
}

// NOTI-02, DM-08: an AI that acts with its owner's user id is not the user of its DM either. Here the
// owner is the user the copy is for, and what Atlas gets back for its own posts (the answer of the room
// route and its history) still carries no row of the copy, while alice's own timeline does.
func TestTheCopysDeliveryIsNotShownToTheAIEvenForItsOwnersDM(t *testing.T) {
	n := newNotifyEnv(t)
	choose(t, n.alice, n.phone)
	opened := n.dm(t, "first")
	n.inst.WaitSend(t)

	reply := testkit.Decode[dto.Message](n.atlas.Post("/api/v1/agent/rooms/"+opened.RoomID+"/messages", dto.PostMessageRequest{Text: "second"}).RequireStatus(http.StatusCreated))
	require.Empty(t, reply.Outbox, "the answer of the room route")
	n.inst.WaitSend(t)
	history := testkit.Decode[dto.Page[dto.AgentMessage]](n.atlas.Get("/api/v1/agent/rooms/" + opened.RoomID + "/messages").RequireStatus(http.StatusOK)).Items
	require.Len(t, history, 2)
	for _, m := range history {
		require.Empty(t, m.Message.Outbox, m.Message.Text)
	}

	n.copyOf(t, n.alice, opened.RoomID, reply.ID, status(dto.OutboxStatusSent)) // alice sees it
}

// NOTI-02 (independent test): once alice chose her phone, what Atlas writes to her in their DM
// also reaches the phone as "<AI name>: <text>", with no author line, no quote and no files, from
// the chat that is no member of the DM; the copy's outbox row is marked like any other and the
// DM keeps the message.
func TestAIDMIsCopiedToTheChosenApp(t *testing.T) {
	n := newNotifyEnv(t)
	choose(t, n.alice, n.phone)
	live, _ := n.alice.DialLive("")

	msg := n.dm(t, "the build is green")
	require.Empty(t, msg.Outbox, "the copy's delivery is for alice, the user of the DM, not for the AI that wrote it")

	sent := n.inst.WaitSend(t)
	require.Equal(t, dmA, sent.ChatID, "the platform id of the chat")
	require.Equal(t, "Atlas: the build is green", sent.Text)
	require.Empty(t, sent.Signature(), "the prefix is the only author mark")
	require.Empty(t, sent.ReplyToID)
	require.Empty(t, sent.Files)
	require.Equal(t, []string{msg.RoomID, msg.ID}, []string{sent.RoomID, sent.MessageID})

	item := n.copyOf(t, n.alice, msg.RoomID, msg.ID, status(dto.OutboxStatusSent))
	require.Equal(t, 1, item.Attempts)
	require.Empty(t, item.LastError)
	var update dto.OutboxUpdate // the viewers of the DM hear of the copy's delivery like any other
	for update.Status != dto.OutboxStatusSent {
		live.Expect(dto.LiveEventTypeOutboxStatus, &update, connectortest.Timeout)
		require.Equal(t, []any{msg.RoomID, msg.ID, n.phone, dto.OutboxPurposeNotify}, []any{update.RoomID, update.MessageID, update.ChatID, update.Purpose})
	}
	room := n.room(t, n.alice, msg.RoomID)
	require.Equal(t, dto.ConversationKindDM, room.Kind)
	require.Equal(t, "the build is green", n.messages(t, msg.RoomID)[0].Text)
	for _, m := range n.members(t, n.alice, msg.RoomID) {
		require.NotEqual(t, dto.MemberKindChat, m.Kind, "the phone does not become a member of the DM")
	}

	// The profile name is the prefix, so a rename shows from the next message on.
	n.alice.Patch("/api/v1/intelligences/"+n.atlasID, dto.UpdateProfileRequest{DisplayName: ptr("Atlas Prime")}).RequireStatus(http.StatusOK)
	n.dm(t, "second")
	require.Equal(t, "Atlas Prime: second", n.inst.WaitSend(t).Text)
}

// NOTI-02: only an AI's posts in a DM with the user are copied: not the user's own posts, not
// another user's, not an AI's posts in a group or its reactions, and nothing before the user
// chose or after they cleared. A reply is copied without a quote.
func TestOnlyAIPostsInTheUsersDMsAreCopied(t *testing.T) {
	n := newNotifyEnv(t)
	bob := n.env.NewUser("bob")
	post := func(c *testkit.Client, room, text string) dto.Message {
		return testkit.Decode[dto.Message](c.Post("/api/v1/rooms/"+room+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
	}
	withAtlas := testkit.Decode[dto.Room](n.alice.Post("/api/v1/conversations/dm", dto.OpenDMRequest{With: n.atlasID}).RequireStatus(http.StatusCreated))
	withBob := testkit.Decode[dto.Room](n.alice.Post("/api/v1/conversations/dm", dto.OpenDMRequest{With: bob.User.ID}).RequireStatus(http.StatusCreated))
	group := testkit.Decode[dto.Room](n.alice.Post("/api/v1/groups", dto.CreateGroupRequest{Name: "team", Members: []string{n.atlasID}}).RequireStatus(http.StatusCreated))
	hello := post(n.alice, withAtlas.ID, "hello atlas")

	// Nothing chosen yet: Atlas's reply is not copied.
	n.atlas.Post("/api/v1/agent/rooms/"+withAtlas.ID+"/messages", dto.PostMessageRequest{Text: "before the choice"}).RequireStatus(http.StatusCreated)
	require.Zero(t, n.notifyRows(t))

	choose(t, n.alice, n.phone)
	post(n.alice, withAtlas.ID, "my own words")
	post(n.alice, withBob.ID, "hi bob")
	post(bob, withBob.ID, "hi alice")
	post(n.alice, group.ID, "to the group")
	n.atlas.Post("/api/v1/agent/rooms/"+group.ID+"/messages", dto.PostMessageRequest{Text: "in the group"}).RequireStatus(http.StatusCreated)
	_, err := n.env.App.Rooms.Post(t.Context(), authn.Principal{Kind: authn.KindAgent, UserID: n.alice.User.ID, AgentID: n.atlasID}, withAtlas.ID,
		rooms.PostInput{Kind: dto.MessageKindReaction, Text: "👍", ReplyTo: hello.ID})
	require.NoError(t, err)
	require.Zero(t, n.notifyRows(t), "a user's posts, another user's, a group, a reaction")

	reply := testkit.Decode[dto.Message](n.atlas.Post("/api/v1/agent/messages/"+hello.ID+"/reply", dto.ReplyRequest{Text: "hi alice"}).RequireStatus(http.StatusCreated))
	require.Equal(t, hello.ID, reply.ReplyTo)
	sent := n.inst.WaitSend(t)
	require.Equal(t, "Atlas: hi alice", sent.Text)
	require.Empty(t, sent.ReplyToID, "the phone never saw the message it answers")
	require.Empty(t, sent.ReplyToSenderID)
	require.Equal(t, 1, n.notifyRows(t))

	clearChoice(t, n.alice)
	n.atlas.Post("/api/v1/agent/rooms/"+withAtlas.ID+"/messages", dto.PostMessageRequest{Text: "after clearing"}).RequireStatus(http.StatusCreated)
	require.Equal(t, 1, n.notifyRows(t))
}

// NOTI-02 with ACC-05: the platform reports the copy Partyline sent through the account as a message
// of that account; it is the echo of a send, not something the operator typed, so it is not stored
// in the rooms the chat belongs to, while a message typed on the account is.
func TestTheEchoOfACopyIsNotAMessageOfTheOperator(t *testing.T) {
	n := newNotifyEnv(t)
	choose(t, n.alice, n.phone)
	stored := len(n.messages(t, n.inbox.ID))

	n.dm(t, "the build is green")
	sent := n.inst.WaitSend(t)
	require.Equal(t, "Atlas: the build is green", sent.Text)
	n.emit(own(dmA, "ext-1", sent.Text)) // the platform id the fake connector gave the copy
	require.Equal(t, stored, len(n.messages(t, n.inbox.ID)), "the echo is dropped")

	n.emit(own(dmA, "typed-1", "alice types on the account"))
	require.Equal(t, stored+1, len(n.messages(t, n.inbox.ID)), "what the operator types is stored")
}

// NOTI-02: attachments go as links to the instance, one line each, and the chat receives no file.
func TestCopyLinksTheAttachments(t *testing.T) {
	n := newNotifyEnv(t)
	choose(t, n.alice, n.phone)
	opened := n.dm(t, "first")
	n.inst.WaitSend(t)

	post := func(text string, files ...string) dto.Message {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		part, err := w.CreateFormField(dto.MultipartMessageField)
		require.NoError(t, err)
		require.NoError(t, json.NewEncoder(part).Encode(dto.PostMessageRequest{Text: text}))
		for _, name := range files {
			file, err := w.CreateFormFile(dto.MultipartFileField, name)
			require.NoError(t, err)
			_, err = file.Write([]byte("synthetic content of " + name))
			require.NoError(t, err)
		}
		require.NoError(t, w.Close())
		return testkit.Decode[dto.Message](n.atlas.DoRaw(http.MethodPost, "/api/v1/agent/rooms/"+opened.RoomID+"/messages", w.FormDataContentType(), &body).RequireStatus(http.StatusCreated))
	}

	one := post("the report", "report.txt")
	require.Len(t, one.Attachments, 1)
	sent := n.inst.WaitSend(t)
	require.Equal(t, "Atlas: the report\n"+n.env.URL+"/api/v1/attachments/"+one.Attachments[0].ID, sent.Text)
	require.Empty(t, sent.Files, "links, not uploads")

	two := post("", "a.txt", "b.txt") // files without text
	require.Len(t, two.Attachments, 2)
	sent = n.inst.WaitSend(t)
	require.Equal(t, "Atlas:\n"+n.env.URL+"/api/v1/attachments/"+two.Attachments[0].ID+"\n"+n.env.URL+"/api/v1/attachments/"+two.Attachments[1].ID, sent.Text)

	// The link opens for the user once they are logged in.
	n.alice.Get("/api/v1/attachments/" + one.Attachments[0].ID).RequireStatus(http.StatusOK)
}

// NOTI-02: when the chosen chat is already a member of the DM that carries the message (the user
// writes to the AI that operates the account from that very phone), it receives the message once.
func TestACopyIsNotSentToAChatThatAlreadyReceivesTheDM(t *testing.T) {
	pc := newPerChat(t, testkit.WithOutbox())
	pc.patch(t, dto.UpdateConnectorRequest{OperatorID: &pc.atlasID})
	phone := pc.linkPhone(t, pc.inst, pc.conn.ID, pc.alice, dmA, "Alice phone")
	room := pc.roomsOf(t, phone)
	require.Len(t, room, 1, "the phone sits in alice's DM with Atlas")
	choose(t, pc.alice, phone)

	msg := testkit.Decode[dto.Message](pc.tok.AsAgent("atlas").Post("/api/v1/agent/rooms/"+room[0]+"/messages", dto.PostMessageRequest{Text: "one copy"}).RequireStatus(http.StatusCreated))
	require.Len(t, msg.Outbox, 1)
	require.Empty(t, msg.Outbox[0].Purpose, "the ordinary delivery to a member chat")
	require.Equal(t, "one copy", pc.inst.WaitSend(t).Text, "the member chat's delivery is not prefixed")
	require.Zero(t, pc.count(t, "SELECT COUNT(*) FROM outbox WHERE purpose = 'notify'"))
}

// NOTI-03: while the connector is down the copy is retried with the usual backoff (2, 4, 8 and 16
// seconds), marked failed after the fifth attempt, and the DM stays readable.
func TestCopyIsRetriedWhileTheConnectorIsDown(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	n := newNotifyEnv(t, testkit.WithClock(clock.Now))
	choose(t, n.alice, n.phone)
	n.alice.Patch("/api/v1/connectors/"+n.conn.ID, dto.UpdateConnectorRequest{Enabled: ptr(false)}).RequireStatus(http.StatusOK)
	n.inst.WaitStop(t)

	msg := n.dm(t, "are you there?")
	for attempt, wait := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second} {
		item := n.copyOf(t, n.alice, msg.RoomID, msg.ID, attempts(attempt+1))
		require.Equal(t, dto.OutboxStatusPending, item.Status, "after attempt %d", attempt+1)
		require.Equal(t, "connector is not running", item.LastError)
		if attempt == 0 { // a retry waits for its backoff: a second short of it, the worker (polling each second) leaves it
			clock.Advance(wait - time.Second)
			time.Sleep(1500 * time.Millisecond)
			require.Equal(t, 1, n.copyOf(t, n.alice, msg.RoomID, msg.ID, attempts(1)).Attempts, "not retried before its backoff")
			clock.Advance(time.Second)
			continue
		}
		clock.Advance(wait)
	}
	item := n.copyOf(t, n.alice, msg.RoomID, msg.ID, status(dto.OutboxStatusFailed))
	require.Equal(t, 5, item.Attempts)
	require.Equal(t, "connector is not running", item.LastError)
	require.Empty(t, n.inst.Sent())

	require.Equal(t, "are you there?", n.messages(t, msg.RoomID)[0].Text, "the DM is still readable on the web")
	require.Equal(t, 1, n.notifyRows(t))
	n.room(t, n.alice, msg.RoomID)
}

// NOTI-03: a platform error is retried and the copy is sent when the platform answers again.
func TestCopyIsSentWhenThePlatformAnswersAgain(t *testing.T) {
	clock := testkit.NewClock(time.Now())
	n := newNotifyEnv(t, testkit.WithClock(clock.Now))
	choose(t, n.alice, n.phone)
	n.inst.SetSendError(errors.New("platform unavailable"))

	msg := n.dm(t, "ping")
	item := n.copyOf(t, n.alice, msg.RoomID, msg.ID, attempts(1))
	require.Equal(t, dto.OutboxStatusPending, item.Status)
	require.Equal(t, "platform unavailable", item.LastError)

	n.inst.SetSendError(nil)
	clock.Advance(3 * time.Second)
	require.Equal(t, "Atlas: ping", n.inst.WaitSend(t).Text)
	item = n.copyOf(t, n.alice, msg.RoomID, msg.ID, status(dto.OutboxStatusSent))
	require.Equal(t, 2, item.Attempts)
	require.Empty(t, item.LastError)
}

// A copy waiting for its retry is not sent once the app is no longer the user's target: the
// attempt fails at once ("notify target no longer available", no more attempts) and nothing reaches
// the app, although the connector is back.
func TestCopyFailsWhenTheTargetIsNoLongerAvailable(t *testing.T) {
	// Each case prepares what it needs while the connector is up and returns the change that makes
	// the phone stop being a target.
	cases := map[string]func(t *testing.T, n *notifyEnv) func(){
		"cleared": func(t *testing.T, n *notifyEnv) func() { return func() { clearChoice(t, n.alice) } },
		"chose another app": func(t *testing.T, n *notifyEnv) func() {
			tablet := n.linkPhone(t, n.inst, n.conn.ID, n.alice, dmC, "Alice tablet")
			return func() { require.Equal(t, tablet, choose(t, n.alice, tablet).NotifyChatID) }
		},
		"unlinked the account": func(t *testing.T, n *notifyEnv) func() {
			return func() {
				ids := testkit.Decode[dto.List[dto.Identity]](n.alice.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items
				require.Len(t, ids, 1)
				n.alice.Delete("/api/v1/identities/" + ids[0].ID).RequireStatus(http.StatusNoContent)
			}
		},
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			clock := testkit.NewClock(time.Now())
			n := newNotifyEnv(t, testkit.WithClock(clock.Now))
			change := prepare(t, n)
			choose(t, n.alice, n.phone)
			n.alice.Patch("/api/v1/connectors/"+n.conn.ID, dto.UpdateConnectorRequest{Enabled: ptr(false)}).RequireStatus(http.StatusOK)
			n.inst.WaitStop(t)
			msg := n.dm(t, "while it was down")
			n.copyOf(t, n.alice, msg.RoomID, msg.ID, attempts(1))

			change()
			n.alice.Patch("/api/v1/connectors/"+n.conn.ID, dto.UpdateConnectorRequest{Enabled: ptr(true)}).RequireStatus(http.StatusOK)
			back := n.fake.Instance(t, n.conn.ID)
			back.WaitRun(t)
			clock.Advance(3 * time.Second)

			item := n.copyOf(t, n.alice, msg.RoomID, msg.ID, status(dto.OutboxStatusFailed))
			require.Equal(t, "notify target no longer available", item.LastError)
			require.Equal(t, 2, item.Attempts, "failed at the first retry, not after five")
			require.Empty(t, back.Sent(), "nothing reached the app")
			require.Empty(t, n.inst.Sent())
		})
	}
}

// A user who loses the connector (its owner stops sharing it) or is disabled no longer gets the
// copies that wait for their retry.
func TestCopyFailsWhenTheUserLosesTheConnectorOrIsDisabled(t *testing.T) {
	cases := map[string]func(n *notifyEnv, carol *testkit.Client){
		"the connector is no longer shared": func(n *notifyEnv, _ *testkit.Client) {
			n.alice.Put("/api/v1/connectors/"+n.conn.ID+"/sharing", dto.Sharing{Mode: dto.VisibilityPrivate}).RequireStatus(http.StatusOK)
		},
		"the user is disabled": func(n *notifyEnv, carol *testkit.Client) {
			admin := n.env.NewAdmin("root")
			admin.Patch("/api/v1/admin/users/"+carol.User.ID, dto.UpdateUserRequest{Disabled: ptr(true)}).RequireStatus(http.StatusOK)
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			clock := testkit.NewClock(time.Now())
			n := newNotifyEnv(t, testkit.WithClock(clock.Now))
			carol := n.env.NewUser("carol")
			n.alice.Put("/api/v1/connectors/"+n.conn.ID+"/sharing", dto.Sharing{
				Mode: dto.VisibilityShared, Users: []dto.SharingUser{{UserID: carol.User.ID, Level: dto.LevelUse}},
			}).RequireStatus(http.StatusOK)
			tablet := n.linkPhone(t, n.inst, n.conn.ID, carol, dmB, "Carol phone")
			choose(t, carol, tablet)
			n.env.NewAI(carol, "sage")
			sage := carol.Token(dto.TokenKindAgent, "desk").AsAgent("sage")

			n.alice.Patch("/api/v1/connectors/"+n.conn.ID, dto.UpdateConnectorRequest{Enabled: ptr(false)}).RequireStatus(http.StatusOK)
			n.inst.WaitStop(t)
			msg := testkit.Decode[dto.Message](sage.Post("/api/v1/agent/dms", dto.AgentDMRequest{To: "@carol", Text: "hello carol"}).RequireStatus(http.StatusCreated))
			n.copyOf(t, carol, msg.RoomID, msg.ID, attempts(1))

			change(n, carol)
			n.alice.Patch("/api/v1/connectors/"+n.conn.ID, dto.UpdateConnectorRequest{Enabled: ptr(true)}).RequireStatus(http.StatusOK)
			back := n.fake.Instance(t, n.conn.ID)
			back.WaitRun(t)
			clock.Advance(3 * time.Second)

			// carol may be disabled: read the outcome from the database
			deadline := time.Now().Add(connectortest.Timeout)
			for n.count(t, "SELECT COUNT(*) FROM outbox WHERE purpose = 'notify' AND status = 'failed' AND last_error = 'notify target no longer available'") == 0 {
				require.True(t, time.Now().Before(deadline), "the copy never failed")
				time.Sleep(50 * time.Millisecond)
			}
			require.Empty(t, back.Sent())
		})
	}
}

// ROOM-01 still cancels what waits for a chat that leaves a room, but not the copies that go to
// a user's chosen app: they do not depend on room membership.
func TestLeavingARoomDoesNotCancelNotifyCopies(t *testing.T) {
	n := newNotifyEnv(t)
	choose(t, n.alice, n.phone)
	n.alice.Patch("/api/v1/connectors/"+n.conn.ID, dto.UpdateConnectorRequest{Enabled: ptr(false)}).RequireStatus(http.StatusOK)
	n.inst.WaitStop(t)
	msg := n.dm(t, "waiting")
	n.copyOf(t, n.alice, msg.RoomID, msg.ID, attempts(1))

	var cancelled int64
	require.NoError(t, n.env.App.Store.WithTx(t.Context(), func(q *db.Queries) (err error) {
		// An ordinary pending delivery of the same message to the same chat, as a member chat has.
		if err = q.CreateOutbox(t.Context(), db.CreateOutboxParams{ID: store.NewID(), MessageID: msg.ID, ChatID: n.phone, NextAttemptAt: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
			return err
		}
		cancelled, err = q.DeletePendingRoomChatOutbox(t.Context(), db.DeletePendingRoomChatOutboxParams{ChatID: n.phone, RoomID: msg.RoomID})
		return err
	}))
	require.Equal(t, int64(1), cancelled, "the ordinary delivery is cancelled")
	require.Equal(t, 1, n.notifyRows(t), "the copy stays")
}
