package connectors_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// Synthetic senders besides the default "Customer" of text().
const (
	sender2 = "5511999990005@s.whatsapp.net"
	sender3 = "5511999990006@s.whatsapp.net"
	lid2    = "100000000000002@lid"
)

// as returns m written by another sender.
func as(m connectors.Inbound, id, alt, name string) connectors.Inbound {
	m.SenderID, m.SenderAltID, m.SenderName = id, alt, name
	return m
}

// people returns the connector's people, oldest first.
func (e *inboundEnv) people(t *testing.T) []db.Person {
	t.Helper()
	list, err := e.env.App.Store.R().ListConnectorPeople(context.Background(), e.conn.ID)
	require.NoError(t, err)
	return list
}

func (e *inboundEnv) personRow(t *testing.T, id string) db.Person {
	t.Helper()
	p, err := e.env.App.Store.R().GetPerson(context.Background(), id)
	require.NoError(t, err)
	return p
}

func (e *inboundEnv) chatRow(t *testing.T, id string) db.Chat {
	t.Helper()
	c, err := e.env.App.Store.R().GetChat(context.Background(), id)
	require.NoError(t, err)
	return c
}

// chatPeople returns the ids of the people seen in the chat.
func (e *inboundEnv) chatPeople(t *testing.T, chatID string) []string {
	t.Helper()
	rows, err := e.env.App.Store.R().ListChatPeople(context.Background(), chatID)
	require.NoError(t, err)
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.PersonID
	}
	return ids
}

func (e *inboundEnv) profile(t *testing.T, id string) dto.IntelligenceProfile {
	t.Helper()
	return testkit.Decode[dto.IntelligenceProfile](e.alice.Get("/api/v1/intelligences/" + id).RequireStatus(http.StatusOK))
}

// routedChat records the chat with a first message nobody receives (its chat is unrouted) and
// then routes it into a new room of alice.
func (e *inboundEnv) routedChat(t *testing.T, name, external string) (dto.Room, string) {
	t.Helper()
	e.emit(text(external, "boot-"+external, "first"))
	chat := e.chatID(t, external)
	return e.routedRoom(t, name, chat), chat
}

// PEOP-01, GRP-03: the first message of a new sender in a routed chat creates one person
// intelligence for (connector, sender id) with the platform's name, and the timeline shows that
// person with the platform instead of the chat. A chat that is routed nowhere records nobody
// (CON-07 keeps nothing of unrouted chats).
func TestInboundNewSenderBecomesOnePerson(t *testing.T) {
	e := newInboundEnv(t)
	live, _ := e.alice.DialLive("")
	e.emit(text(groupID, "M0", "first"))
	require.Empty(t, e.people(t), "an unrouted chat records no person")
	room := e.routedRoom(t, "A", e.chatID(t, groupID))
	chatID := e.chatID(t, groupID)

	e.emit(text(groupID, "M1", "where is my order?"))

	people := e.people(t)
	require.Len(t, people, 1)
	p := people[0]
	require.Equal(t, e.conn.ID, p.ConnectorID)
	require.Equal(t, senderID, p.ExternalID)
	require.Equal(t, senderAlt, p.AltExternalID)
	require.Equal(t, "Customer", p.PlatformName)
	require.False(t, p.NameLocked)
	require.True(t, p.LastSeenAt.Valid)

	msgs := e.messages(t, room.ID)
	require.Len(t, msgs, 1)
	m := msgs[0]
	require.Equal(t, dto.AuthorKindChat, m.AuthorKind, "routing and dedupe keep the chat as the author")
	require.Equal(t, chatID, m.AuthorID)
	require.Empty(t, m.AuthorUserID)
	require.Equal(t, p.ID, m.Author.ID, "the author is the person's intelligence")
	require.Equal(t, dto.IntelligenceRolePerson, m.Author.Role)
	require.Equal(t, dto.IntelligenceKindHuman, m.Author.Kind)
	require.Equal(t, "Customer", m.Author.DisplayName)
	require.Equal(t, "customer", m.Author.Handle)
	require.Equal(t, connectortest.KindName, m.Author.Platform, "the badge is the platform")
	require.Equal(t, "Customer", m.AuthorName, "the timeline names the person, not the chat")
	require.Equal(t, "Customer", m.SenderName, "the platform's own name stays on the message")

	prof := e.profile(t, p.ID)
	require.Equal(t, dto.IntelligenceRolePerson, prof.Role)
	require.Equal(t, "Customer", prof.DisplayName)
	require.Equal(t, connectortest.KindName, prof.Platform)

	var pushed dto.Message
	live.Expect(dto.LiveEventTypeRoomMessage, &pushed, connectortest.Timeout)
	require.Equal(t, p.ID, pushed.Author.ID, "the live frame names the person too")
	require.Equal(t, connectortest.KindName, pushed.Author.Platform)
}

// PEOP-01: a sender without a name gets "<Platform> contact".
func TestInboundNamelessSenderGetsAPlatformContactName(t *testing.T) {
	e := newInboundEnv(t)
	room, _ := e.routedChat(t, "A", groupID)

	e.emit(as(text(groupID, "M1", "hi"), sender2, "", "  \t "))

	msgs := e.messages(t, room.ID)
	require.Len(t, msgs, 1)
	require.Equal(t, "Fake contact", msgs[0].Author.DisplayName, "the fake kind is called Fake")
	require.Equal(t, "fake-contact", msgs[0].Author.Handle)
	require.Equal(t, "Fake contact", msgs[0].AuthorName)
}

// PEOP-01: the platform label of a nameless person for the supported platforms.
func TestPlatformLabel(t *testing.T) {
	for kind, want := range map[string]string{
		"whatsapp": "WhatsApp", "telegram": "Telegram", "discord": "Discord", "webhook": "Webhook", "matrix": "Matrix",
	} {
		require.Equal(t, want, connectors.PlatformLabel(kind), kind)
	}
}

// PEOP-01: a person is one (connector, external id). The same sender in two chats is one
// person, two senders with the same name are two people, and handles share the instance's one
// namespace with users (PROF-02).
func TestInboundPeopleAreKeyedBySender(t *testing.T) {
	e := newInboundEnv(t)
	roomA, chatA := e.routedChat(t, "A", groupID)
	roomB, chatB := e.routedChat(t, "B", otherID)

	e.emit(text(groupID, "M1", "in A"))
	e.emit(text(otherID, "M2", "in B"))
	inA, inB := e.messages(t, roomA.ID)[0].Author, e.messages(t, roomB.ID)[0].Author
	require.NotEmpty(t, inA.ID)
	require.Equal(t, inA.ID, inB.ID, "the same sender in two chats is one person")
	require.Len(t, e.people(t), 1)
	require.Equal(t, []string{inA.ID}, e.chatPeople(t, chatA))
	require.Equal(t, []string{inA.ID}, e.chatPeople(t, chatB))

	e.emit(as(text(groupID, "M3", "me too"), sender2, "", "Customer"))
	e.emit(as(text(groupID, "M4", "and me"), sender3, "", "alice"))
	msgs := e.messages(t, roomA.ID) // newest first
	require.Len(t, msgs, 3)
	same, byAlice := msgs[1].Author, msgs[0].Author
	require.Equal(t, "Customer", same.DisplayName)
	require.NotEqual(t, inA.ID, same.ID, "a name does not make two senders one person")
	require.Equal(t, "customer-2", same.Handle)
	require.Equal(t, "alice", byAlice.DisplayName)
	require.Equal(t, "alice-2", byAlice.Handle, "alice the user already has @alice")
	require.Len(t, e.people(t), 3)
	require.ElementsMatch(t, []string{inA.ID, same.ID, byAlice.ID}, e.chatPeople(t, chatA))
	require.Equal(t, []string{inA.ID}, e.chatPeople(t, chatB))
}

// AD-008: an external account belongs to the connector it was seen through; the same sender id
// through a second connector is another person.
func TestInboundPeopleBelongToTheirConnector(t *testing.T) {
	e := newInboundEnv(t)
	room, _ := e.routedChat(t, "A", groupID)
	e.emit(text(groupID, "M1", "first connector"))

	on := true
	other := testkit.Decode[dto.Connector](e.alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		Kind: connectortest.KindName, Name: "second", Config: map[string]any{"token": "fake-token-not-real"}, Enabled: &on,
	}).RequireStatus(http.StatusCreated))
	e.alice.OperateConnector(other.ID, e.alice.NewAI("relay"))
	inst := e.fake.Instance(t, other.ID)
	inst.WaitRun(t)
	inst.Emit(context.Background(), text(otherID, "X0", "first"))
	var chat string
	for _, c := range testkit.Decode[dto.List[dto.Chat]](e.alice.Get("/api/v1/connectors/" + other.ID + "/chats").RequireStatus(http.StatusOK)).Items {
		chat = c.ID
	}
	room2 := e.routedRoom(t, "B", chat)
	inst.Emit(context.Background(), text(otherID, "X1", "second connector"))

	first, second := e.messages(t, room.ID)[0].Author, e.messages(t, room2.ID)[0].Author
	require.NotEmpty(t, first.ID)
	require.NotEmpty(t, second.ID)
	require.NotEqual(t, first.ID, second.ID, "the same sender id on another connector is another person")
	require.Equal(t, "Customer", second.DisplayName)
	require.Equal(t, "customer-2", second.Handle)
	require.Len(t, e.people(t), 1)
	peopleOfSecond, err := e.env.App.Store.R().ListConnectorPeople(context.Background(), other.ID)
	require.NoError(t, err)
	require.Len(t, peopleOfSecond, 1)
}

// PEOP-01 (WhatsApp phone/LID switching): a sender seen under its other id is the same person,
// in either order, and the ids it was seen under keep matching it.
func TestInboundSenderSeenUnderItsOtherIDIsTheSamePerson(t *testing.T) {
	e := newInboundEnv(t)
	room, _ := e.routedChat(t, "A", groupID)

	e.emit(text(groupID, "M1", "as phone")) // senderID with senderAlt (the LID)
	e.emit(as(text(groupID, "M2", "as lid"), senderAlt, "", "Customer"))
	e.emit(as(text(groupID, "M3", "as lid with the phone"), senderAlt, senderID, "Customer"))
	e.emit(as(text(groupID, "M4", "phone only"), senderID, "", "Customer"))
	require.Len(t, e.people(t), 1, "four sightings under two ids are one person")
	for _, m := range e.messages(t, room.ID) {
		require.Equal(t, e.people(t)[0].ID, m.Author.ID, m.Text)
	}

	// A sender first seen without its alternative id learns it from a later sighting.
	e.emit(as(text(groupID, "N1", "phone, no alt yet"), sender2, "", "Dana"))
	e.emit(as(text(groupID, "N2", "lid with the phone"), lid2, sender2, "Dana"))
	e.emit(as(text(groupID, "N3", "lid only"), lid2, "", "Dana"))
	require.Len(t, e.people(t), 2)
	latest := e.messages(t, room.ID)
	require.Equal(t, latest[0].Author.ID, latest[1].Author.ID)
	require.Equal(t, latest[1].Author.ID, latest[2].Author.ID)
	require.Equal(t, "Dana", latest[0].Author.DisplayName)
	require.Equal(t, sender2, e.personRow(t, latest[0].Author.ID).ExternalID, "the id it was first seen under stays its external id")
}

// pair links the sender to the user through the connector with a pairing command.
func (e *inboundEnv) pair(t *testing.T, user *testkit.Client, chat, sender, name string) {
	t.Helper()
	code := testkit.Decode[dto.PairingCode](user.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: e.conn.ID}).RequireStatus(http.StatusCreated))
	e.emit(as(text(chat, "link-"+sender, "!link "+code.Code), sender, "", name))
}

// PEOP-02: once a sender is linked to a user (AD-008), its messages are authored by the user's
// intelligence; earlier messages keep their person, and the person remains.
func TestInboundLinkedSenderIsAuthoredByTheUser(t *testing.T) {
	e := newInboundEnv(t)
	room, _ := e.routedChat(t, "A", groupID)
	e.emit(as(text(groupID, "M1", "before linking"), sender2, "", "Alice on the phone"))
	person := e.people(t)[0]

	e.pair(t, e.alice, groupID, sender2, "Alice on the phone")
	e.emit(as(text(groupID, "M2", "after linking"), sender2, "", "Alice on the phone"))

	msgs := e.messages(t, room.ID) // newest first
	require.Len(t, msgs, 2)
	after, before := msgs[0], msgs[1]
	require.Equal(t, e.alice.User.ID, after.Author.ID)
	require.Equal(t, dto.IntelligenceRoleUser, after.Author.Role)
	require.Equal(t, "alice", after.Author.Handle)
	require.Empty(t, after.Author.Platform, "the user is not a platform badge")
	require.Equal(t, e.alice.User.ID, after.AuthorUserID)
	require.Equal(t, dto.AuthorKindChat, after.AuthorKind)
	require.Equal(t, person.ID, before.Author.ID, "history keeps the person")
	require.Equal(t, dto.IntelligenceRolePerson, before.Author.Role)
	require.Empty(t, before.AuthorUserID)
	require.Len(t, e.people(t), 1, "the linked account stays a person, as an app of the user")
}

// PEOP-06 (AD-006): trust is exactly what it was. A message is trusted for an agent only when
// its author resolves to the agent's owner: the owner, or a chat sender verified as the owner
// through this connector. A person, a sender named like the owner, a sender without an id and a
// sender verified as someone else are not.
func TestInboundTrustIsUnchangedByPeople(t *testing.T) {
	c := newCatchAll(t)
	bob := c.env.NewUser("bob")
	c.alice.Put("/api/v1/connectors/"+c.conn.ID+"/sharing", dto.Sharing{
		Mode: dto.VisibilityShared, Users: []dto.SharingUser{{UserID: bob.User.ID, Level: dto.LevelUse}},
	}).RequireStatus(http.StatusOK)
	c.emit(text(groupID, "T0", "first"))
	c.alice.Post("/api/v1/rooms/"+c.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindChat, MemberID: c.chatID(t, groupID)}).RequireStatus(http.StatusCreated)
	c.pair(t, c.alice, groupID, sender2, "Alice on the phone")
	c.pair(t, bob, groupID, sender3, "Bob on the phone")

	next := func(what string, trusted bool) dto.Message {
		t.Helper()
		got := c.agent.Message(connectortest.Timeout)
		require.Equal(t, trusted, got.Trusted, what)
		return got.Message
	}
	c.alice.Post("/api/v1/rooms/"+c.room.ID+"/messages", dto.PostMessageRequest{Text: "from alice on the web"}).RequireStatus(http.StatusCreated)
	require.Equal(t, dto.IntelligenceRoleUser, next("the owner on the web", true).Author.Role)
	c.emit(as(text(groupID, "T1", "verified as alice"), sender2, "", "Alice on the phone"))
	require.Equal(t, c.alice.User.ID, next("a sender verified as the owner", true).Author.ID)
	c.emit(text(groupID, "T2", "a customer"))
	require.Equal(t, dto.IntelligenceRolePerson, next("a person", false).Author.Role)
	c.emit(as(text(groupID, "T3", "named like the owner"), "5511999990007@s.whatsapp.net", "", "alice"))
	named := next("a person named like the owner", false)
	require.Equal(t, dto.IntelligenceRolePerson, named.Author.Role)
	require.NotEqual(t, c.alice.User.ID, named.Author.ID)
	c.emit(as(text(groupID, "T4", "verified as bob"), sender3, "", "Bob on the phone"))
	require.Equal(t, bob.User.ID, next("a sender verified as another user", false).Author.ID)
	c.emit(as(text(groupID, "T5", "no sender id"), "", "", ""))
	senderless := next("a chat message without a sender", false)
	require.Empty(t, senderless.Author.ID)
	require.Empty(t, senderless.AuthorUserID)
}

// PEOP-05: when the platform name of a person changes, the person is renamed on the next
// message (the timeline follows, the handle stays); a message without a name keeps it, and live
// clients that can see the person hear about it.
func TestInboundPlatformRenameUpdatesThePerson(t *testing.T) {
	e := newInboundEnv(t)
	room, _ := e.routedChat(t, "A", groupID)
	e.emit(text(groupID, "M1", "hello"))
	id := e.people(t)[0].ID
	live, _ := e.alice.DialLive("")

	e.emit(as(text(groupID, "M2", "same name"), senderID, senderAlt, "Customer"))
	e.emit(as(text(groupID, "M3", "new name"), senderID, senderAlt, "Customer Prime"))

	var changed dto.IntelligenceRef
	live.Expect(dto.LiveEventTypeIntelligenceChanged, &changed, connectortest.Timeout)
	require.Equal(t, id, changed.ID)
	require.Equal(t, "Customer Prime", changed.DisplayName)
	require.Equal(t, "customer", changed.Handle, "a platform rename keeps the handle")
	require.Len(t, e.people(t), 1)
	p := e.personRow(t, id)
	require.Equal(t, "Customer Prime", p.PlatformName)
	require.False(t, p.NameLocked)
	require.Equal(t, "Customer Prime", e.profile(t, id).DisplayName)
	for _, m := range e.messages(t, room.ID) {
		require.Equal(t, "Customer Prime", m.Author.DisplayName, "the timeline shows the current name: %s", m.Text)
		require.Equal(t, "Customer Prime", m.AuthorName)
	}

	e.emit(as(text(groupID, "M4", "no name this time"), senderID, senderAlt, ""))
	e.emit(as(text(groupID, "M5", "blank name"), senderID, senderAlt, " \n\t"))
	require.Equal(t, "Customer Prime", e.profile(t, id).DisplayName, "a message without a name keeps the last one")
	require.Equal(t, "Customer Prime", e.personRow(t, id).PlatformName)

	e.emit(as(text(groupID, "M6", "late name"), sender2, "", ""))
	nameless := e.messages(t, room.ID)[0].Author
	require.Equal(t, "Fake contact", nameless.DisplayName)
	e.emit(as(text(groupID, "M7", "now with a name"), sender2, "", "Dana"))
	require.Equal(t, "Dana", e.profile(t, nameless.ID).DisplayName, "a person first seen without a name takes the name it later shows")
}

// PEOP-05, PROF-11: after a user renames the contact, platform name changes no longer rename
// it (the platform name is still tracked); changing only the bio or the handle does not count
// as renaming.
func TestInboundUserRenameSurvivesPlatformRename(t *testing.T) {
	e := newInboundEnv(t)
	room, _ := e.routedChat(t, "A", groupID)
	e.emit(text(groupID, "M1", "hello"))
	id := e.people(t)[0].ID

	bio := "Orders desk"
	e.alice.Patch("/api/v1/intelligences/"+id, dto.UpdateProfileRequest{Bio: &bio}).RequireStatus(http.StatusOK)
	handle := "customer-desk"
	e.alice.Patch("/api/v1/intelligences/"+id, dto.UpdateProfileRequest{Handle: &handle}).RequireStatus(http.StatusOK)
	require.False(t, e.personRow(t, id).NameLocked, "only a new display name locks the name")
	e.emit(as(text(groupID, "M2", "renamed on the platform"), senderID, senderAlt, "Customer Prime"))
	require.Equal(t, "Customer Prime", e.profile(t, id).DisplayName)

	mine := "Dana (Acme)"
	e.alice.Patch("/api/v1/intelligences/"+id, dto.UpdateProfileRequest{DisplayName: &mine}).RequireStatus(http.StatusOK)
	require.True(t, e.personRow(t, id).NameLocked)
	e.emit(as(text(groupID, "M3", "renamed again"), senderID, senderAlt, "Customer Third"))

	prof := e.profile(t, id)
	require.Equal(t, "Dana (Acme)", prof.DisplayName, "the user's name wins over the platform's")
	require.Equal(t, "customer-desk", prof.Handle)
	require.Equal(t, "Orders desk", prof.Bio)
	require.Equal(t, "Customer Third", e.personRow(t, id).PlatformName, "the platform name is still tracked")
	require.Equal(t, "Dana (Acme)", e.messages(t, room.ID)[0].Author.DisplayName)

	// Renaming to the name it already has changes nothing.
	same := "Dana (Acme)"
	e.alice.Patch("/api/v1/intelligences/"+id, dto.UpdateProfileRequest{DisplayName: &same}).RequireStatus(http.StatusOK)
	require.True(t, e.personRow(t, id).NameLocked)
}

// GRP-03: a chat message without a sender id has no person and no author intelligence; it
// shows the chat as before.
func TestInboundSenderlessMessageHasNoPerson(t *testing.T) {
	e := newInboundEnv(t)
	room, _ := e.routedChat(t, "A", groupID)

	e.emit(as(text(groupID, "M1", "channel post"), "", "", ""))

	require.Empty(t, e.people(t))
	msgs := e.messages(t, room.ID)
	require.Len(t, msgs, 1)
	require.Empty(t, msgs[0].Author.ID)
	require.Empty(t, msgs[0].Author.Handle)
	require.Equal(t, "Support group", msgs[0].Author.DisplayName, "the chat's title names the author, as before")
	require.Equal(t, "Support group", msgs[0].AuthorName)
}

// The counterpart of a DM chat is the person who writes in it; group chats have none.
func TestInboundDMChatRemembersItsPerson(t *testing.T) {
	e := newInboundEnv(t)
	e.emit(dmText(dmA, "D0", "first"))
	dm := e.chatID(t, dmA)
	e.routedRoom(t, "dm", dm)
	_, group := e.routedChat(t, "group", groupID)
	require.False(t, e.chatRow(t, dm).PersonID.Valid)

	e.emit(dmText(dmA, "D1", "oi"))
	e.emit(text(groupID, "G1", "hello"))

	people := e.people(t)
	require.Len(t, people, 2)
	var dmPerson, groupPerson string
	for _, p := range people {
		if p.ExternalID == dmA {
			dmPerson = p.ID
		} else {
			groupPerson = p.ID
		}
	}
	require.NotEmpty(t, dmPerson)
	require.NotEmpty(t, groupPerson)
	require.Equal(t, dmPerson, e.chatRow(t, dm).PersonID.String)
	require.True(t, e.chatRow(t, dm).PersonID.Valid)
	require.False(t, e.chatRow(t, group).PersonID.Valid, "a group chat has no counterpart")
	require.Equal(t, []string{dmPerson}, e.chatPeople(t, dm))
	require.Equal(t, []string{groupPerson}, e.chatPeople(t, group))

	// A person already known from a group is the counterpart of the DM chat it later writes in
	// (a WhatsApp DM chat is named after one id of the account, the sender may use the other).
	e.emit(dmText(dmB, "D1", "first"))
	dm2 := e.chatID(t, dmB)
	e.routedRoom(t, "dm 2", dm2)
	e.emit(text(groupID, "G2", "known from the group")) // senderID wrote in the group before
	e.emit(as(dmText(dmB, "D2", "now in private"), senderID, senderAlt, "Customer"))
	require.Equal(t, groupPerson, e.chatRow(t, dm2).PersonID.String, "the person of the group is the counterpart")
	require.Len(t, e.people(t), 2)
}

// A message time in the future (a phone with a wrong clock) counts as now, so it cannot hold
// back later sightings.
func TestInboundFutureMessageTimeCountsAsNow(t *testing.T) {
	e := newInboundEnv(t)
	e.routedChat(t, "A", groupID)
	m := text(groupID, "F1", "from the future")
	m.At = time.Now().Add(5 * time.Hour)

	before := time.Now()
	e.emit(m)

	seen := time.UnixMilli(e.people(t)[0].LastSeenAt.Int64)
	require.False(t, seen.Before(before.Add(-time.Second)), "not before the message arrived: %s", seen)
	require.False(t, seen.After(time.Now()), "never after now: %s", seen)
}

// The times a person and a chat link were last seen are written at most once per ten minutes,
// never go back, and follow the message's time.
func TestInboundSeenTimesAreWrittenOncePerTenMinutes(t *testing.T) {
	e := newInboundEnv(t)
	_, chat := e.routedChat(t, "A", groupID)
	t0 := time.Now().Add(-3 * time.Hour).Truncate(time.Millisecond)
	emitAt := func(id string, after time.Duration) {
		m := text(groupID, id, "x")
		m.At = t0.Add(after)
		e.emit(m)
	}
	seen := func() (person, link time.Time) {
		t.Helper()
		p := e.people(t)[0]
		cp, err := e.env.App.Store.R().GetChatPerson(context.Background(), db.GetChatPersonParams{ChatID: chat, PersonID: p.ID})
		require.NoError(t, err)
		return time.UnixMilli(p.LastSeenAt.Int64), time.UnixMilli(cp.LastSeenAt)
	}

	emitAt("S1", 0)
	person, link := seen()
	require.True(t, person.Equal(t0), "the first sighting is written: %s", person)
	require.True(t, link.Equal(t0))

	emitAt("S2", 5*time.Minute)
	emitAt("S3", 9*time.Minute+59*time.Second)
	person, link = seen()
	require.True(t, person.Equal(t0), "inside the ten minutes nothing is written: %s", person)
	require.True(t, link.Equal(t0))

	emitAt("S4", 10*time.Minute+time.Second)
	person, link = seen()
	require.True(t, person.Equal(t0.Add(10*time.Minute+time.Second)), "after the ten minutes it is written")
	require.True(t, link.Equal(t0.Add(10*time.Minute+time.Second)))

	emitAt("S5", 15*time.Minute) // 4m59s after the last write, and a message delivered late
	emitAt("S6", 5*time.Minute)
	person, link = seen()
	require.True(t, person.Equal(t0.Add(10*time.Minute+time.Second)), "an older time never moves it back")
	require.True(t, link.Equal(t0.Add(10*time.Minute+time.Second)))
}

// PEOP-01: concurrent first messages of one new sender in several chats create one person and
// lose no message.
func TestInboundConcurrentFirstMessagesCreateOnePerson(t *testing.T) {
	e := newInboundEnv(t)
	const chats = 6
	roomIDs := make([]string, chats)
	externals := make([]string, chats)
	for i := range chats {
		externals[i] = fmt.Sprintf("12036300000000011%02d@g.us", i)
		room, _ := e.routedChat(t, fmt.Sprintf("room %d", i), externals[i])
		roomIDs[i] = room.ID
	}

	var wg sync.WaitGroup
	for i := range chats {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.emit(text(externals[i], fmt.Sprintf("C%d", i), "hello"))
		}()
	}
	wg.Wait()

	people := e.people(t)
	require.Len(t, people, 1)
	for i, roomID := range roomIDs {
		msgs := e.messages(t, roomID)
		require.Len(t, msgs, 1, "room %d lost its message", i)
		require.Equal(t, people[0].ID, msgs[0].Author.ID)
	}
}
