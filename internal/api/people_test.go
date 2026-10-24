package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/avatars"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/conversations"
	"github.com/wnexous/partyline/internal/identities"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// plantAvatar gives the intelligence the avatar an older version let a user set for a person (a
// person's profile is history now, CID-21): the image goes through the avatar pipeline into the blob
// store and onto the profile, and the avatar URL is returned.
func plantAvatar(t *testing.T, env *testkit.Env, id string, image []byte) string {
	t.Helper()
	ctx := context.Background()
	png, err := avatars.Process(bytes.NewReader(image))
	require.NoError(t, err)
	sha, _, err := env.App.Blobs.Put(bytes.NewReader(png), int64(len(png)))
	require.NoError(t, err)
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetIntelligenceAvatar(ctx, db.SetIntelligenceAvatarParams{AvatarSha256: sha, UpdatedAt: time.Now().UnixMilli(), ID: id})
	}))
	return "/api/v1/intelligences/" + id + "/avatar?v=" + sha
}

// plantProfile gives the intelligence the name and handle an older version let a user choose for a
// person.
func plantProfile(t *testing.T, env *testkit.Env, id, name, handle string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetIntelligence(ctx, id)
		if err != nil {
			return err
		}
		return q.UpdateIntelligenceProfile(ctx, db.UpdateIntelligenceProfileParams{
			DisplayName: name, Handle: handle, Bio: cur.Bio, UpdatedAt: time.Now().UnixMilli(), ID: id,
		})
	}))
}

// Synthetic platform ids of the people fixture.
const (
	peopleGroupX = "120363000000000001@g.us"
	peopleGroupY = "120363000000000002@g.us"
	peopleSender = "5511999990000@s.whatsapp.net"
)

// peopleFixture is a fake connector of alice's organization "line", which bob belongs to, operated by
// Atlas. Alice's rooms live in her personal organization, apart from it: the connector's chat X is bridged
// into her room A ("Support", with dave in it) and its chat Y into her private room B ("Billing"). The
// sender "Customer" wrote in both chats, so the connector has one person. dave and erin belong to the
// personal organization only, so they see the person through the rooms they are in, not through the
// connection; carol and the admin have no access to anything; erin is in a room C only, which holds chat X
// since after the person wrote.
type peopleFixture struct {
	env                                  *testkit.Env
	clock                                *testkit.Clock
	admin, alice, bob, carol, dave, erin *testkit.Client
	conn                                 dto.Connector
	inst                                 *connectortest.Instance
	line                                 string // the organization of the connection
	roomA, roomB, roomC                  dto.Room
	chatX, chatY                         string
	person                               string
	history                              *connectortest.History
}

// emit stores what the retired inbound pipeline stored for a group message of the sender in the
// chat (history, since CID-07): the chat is recorded; when it sits in rooms the sender is a person
// seen in it and the message is stored in each of its rooms.
func (f *peopleFixture) emit(chat, id, sender, name string) {
	f.env.T.Helper()
	ctx := context.Background()
	title := "Support group"
	if chat == peopleGroupY {
		title = "Billing group"
	}
	at := f.clock.Now()
	c, err := connectors.UpsertChat(ctx, f.env.App.Store, f.env.App.Bus, f.conn.ID, connectors.ChatInfo{ID: chat, Title: title, Kind: "group"}, at, at)
	require.NoError(f.env.T, err)
	chatRooms, err := f.env.App.Store.R().ListChatRooms(ctx, c.ID)
	require.NoError(f.env.T, err)
	if len(chatRooms) == 0 {
		return // an unrouted chat keeps nobody
	}
	person, err := f.history.Seen(ctx, connectortest.Sighting{
		ConnectorID: f.conn.ID, ConnectorKind: connectortest.KindName, Chat: c, SenderID: sender, Name: name, At: at,
	})
	require.NoError(f.env.T, err)
	for _, r := range chatRooms {
		_, err := f.env.App.Rooms.PostFromChat(ctx, r.ID, rooms.ChatMessage{
			ChatID: c.ID, SenderID: sender, SenderName: name, AuthorIID: person.ID, ExternalID: id, Kind: dto.MessageKindText, Text: "hello " + id,
		})
		require.NoError(f.env.T, err)
	}
}

// platformDM stores what the retired pipeline stored when a person wrote to a DM chat of the
// connection while the AI operated it: the person seen in the chat, the DM between them with the
// chat as a member, and the message. It returns the chat, the room and the person.
func (f *peopleFixture) platformDM(ai, external, name, body string) (chat, room, person string) {
	f.env.T.Helper()
	ctx := context.Background()
	at := f.clock.Now()
	c, err := connectors.UpsertChat(ctx, f.env.App.Store, f.env.App.Bus, f.conn.ID, connectors.ChatInfo{ID: external, Title: name, Kind: "dm"}, at, at)
	require.NoError(f.env.T, err)
	p, err := f.history.Seen(ctx, connectortest.Sighting{
		ConnectorID: f.conn.ID, ConnectorKind: connectortest.KindName, Chat: c, SenderID: external, Name: name, At: at,
	})
	require.NoError(f.env.T, err)
	route, err := f.env.App.Conversations.EnsurePlatformDM(ctx, conversations.PlatformDM{
		OwnerID: f.alice.User.ID, ChatID: c.ID, Author: conversations.Party{ID: p.ID}, Operator: conversations.Party{ID: ai, Kind: dto.MemberKindAgent},
	})
	require.NoError(f.env.T, err)
	_, err = f.env.App.Rooms.PostFromChat(ctx, route.RoomID, rooms.ChatMessage{
		ChatID: c.ID, SenderID: external, SenderName: name, AuthorIID: p.ID, ExternalID: "d1", Kind: dto.MessageKindText, Text: body,
	})
	require.NoError(f.env.T, err)
	return c.ID, route.RoomID, p.ID
}

func (f *peopleFixture) addChat(owner *testkit.Client, roomID, chatID string) {
	f.env.T.Helper()
	owner.SeedChatMember(roomID, chatID)
}

func (f *peopleFixture) members(c *testkit.Client, roomID string) []dto.RoomMember {
	f.env.T.Helper()
	return testkit.Decode[dto.List[dto.RoomMember]](c.Get("/api/v1/rooms/" + roomID + "/members").RequireStatus(http.StatusOK)).Items
}

func (f *peopleFixture) chatMember(c *testkit.Client, roomID, chatID string) dto.RoomMember {
	f.env.T.Helper()
	for _, m := range f.members(c, roomID) {
		if m.Kind == dto.MemberKindChat && m.MemberID == chatID {
			return m
		}
	}
	require.FailNow(f.env.T, "the chat is not a member of the room")
	return dto.RoomMember{}
}

func newPeopleFixture(t *testing.T) *peopleFixture {
	t.Helper()
	fake := connectortest.New()
	clock := testkit.NewClock(time.Now().Add(-6 * time.Hour))
	env := testkit.New(t, testkit.WithConnectorKinds(fake.Kind()), testkit.WithClock(clock.Now))
	f := &peopleFixture{
		env: env, clock: clock, admin: env.SetupAdmin(),
		alice: env.NewUser("alice"), bob: env.NewUser("bob"), carol: env.NewUser("carol"), dave: env.NewUser("dave"), erin: env.NewUser("erin"),
	}
	// Atlas operates the connection (the contacts fixture names the same AI).
	f.history = connectortest.NewHistory(connectortest.HistoryDeps{Store: env.App.Store, Bus: env.App.Bus, Now: clock.Now})
	line, err := env.App.Orgs.Create(context.Background(), f.alice.User.ID, "line")
	require.NoError(t, err)
	f.line = line.ID
	on := true
	f.conn = testkit.Decode[dto.Connector](f.alice.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		OrgID: f.line, Kind: connectortest.KindName, Name: "wa", Config: map[string]any{"token": "fake-token-not-real"}, Enabled: &on,
	}).RequireStatus(http.StatusCreated))
	f.alice.OperateConnector(f.conn.ID, aiAtIn(t, env, f.alice, f.line, "Atlas", "atlas", "devbox", "/home/alice/atlas", "/home/alice", "claude"))
	shareConnector(f.alice, f.conn.ID, f.bob.User.ID) // bob joins "line"
	f.inst = fake.Instance(t, f.conn.ID)
	f.inst.WaitRun(t)

	f.emit(peopleGroupX, "x0", peopleSender, "Customer") // unrouted: records the chats only
	f.emit(peopleGroupY, "y0", peopleSender, "Customer")
	for _, c := range testkit.Decode[dto.List[dto.Chat]](f.alice.Get("/api/v1/connectors/" + f.conn.ID + "/chats").RequireStatus(http.StatusOK)).Items {
		switch c.ExternalID {
		case peopleGroupX:
			f.chatX = c.ID
		case peopleGroupY:
			f.chatY = c.ID
		}
	}
	require.NotEmpty(t, f.chatX)
	require.NotEmpty(t, f.chatY)
	f.roomA = testkit.Decode[dto.Room](f.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Support"}).RequireStatus(http.StatusCreated))
	f.roomB = testkit.Decode[dto.Room](f.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Billing"}).RequireStatus(http.StatusCreated))
	f.addChat(f.alice, f.roomA.ID, f.chatX)
	f.addChat(f.alice, f.roomB.ID, f.chatY)
	addToRoom(t, env, f.alice, f.roomA.ID, f.dave)

	clock.Advance(time.Minute)
	f.emit(peopleGroupX, "x1", peopleSender, "Customer")
	clock.Advance(time.Minute)
	f.emit(peopleGroupY, "y1", peopleSender, "Customer")
	msgs := testkit.Decode[dto.Page[dto.Message]](f.alice.Get("/api/v1/rooms/" + f.roomA.ID + "/messages").RequireStatus(http.StatusOK)).Items
	require.Len(t, msgs, 1)
	f.person = msgs[0].Author.ID
	require.NotEmpty(t, f.person)

	// Room C holds chat X from now on: the person was seen in the chat but wrote nothing in C.
	clock.Advance(time.Minute)
	f.roomC = testkit.Decode[dto.Room](f.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: "Zulu archive"}).RequireStatus(http.StatusCreated))
	f.addChat(f.alice, f.roomC.ID, f.chatX)
	addToRoom(t, env, f.alice, f.roomC.ID, f.erin)
	return f
}

// PEOP-04 (L-017, L-018, AD-016): the members of the connection's organization see its people whatever
// else they can view; a user who can view a conversation where the person was seen reads the profile too;
// nobody else, admins of the instance included, finds it. Nobody edits a person: its profile is history
// (CID-21), so the members of the connection's organization are told so, and the others are refused as
// before.
func TestPersonProfileFollowsTheConnector(t *testing.T) {
	f := newPeopleFixture(t)
	path := "/api/v1/intelligences/" + f.person
	liveAlice, _ := f.alice.DialLive("")
	liveDave, _ := f.dave.DialLive("")
	liveCarol, _ := f.carol.DialLive("")

	owner := profileOf(t, f.alice, f.person)
	require.Equal(t, []any{dto.IntelligenceRolePerson, dto.IntelligenceKindHuman, "Customer", "customer", connectortest.KindName, false},
		[]any{owner.Role, owner.Kind, owner.DisplayName, owner.Handle, owner.Platform, owner.CanEdit})
	require.NotNil(t, owner.Person)

	// bob belongs to the connection's organization and can view no room at all: the connection alone gives access.
	colleague := profileOf(t, f.bob, f.person)
	require.False(t, colleague.CanEdit)
	require.NotNil(t, colleague.Person)
	require.Empty(t, colleague.Conversations)

	// dave can view room A and is no member of the connection's organization.
	viewer := profileOf(t, f.dave, f.person)
	require.Equal(t, []any{"Customer", connectortest.KindName, false}, []any{viewer.DisplayName, viewer.Platform, viewer.CanEdit})
	require.Nil(t, viewer.Person, "the account behind the person is for the members of the connection's organization")
	patchProfile(f.dave, f.person, dto.UpdateProfileRequest{Bio: ptr("Hijacked")}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.dave.DoRaw(http.MethodPut, path+"/avatar", "image/png", bytes.NewReader([]byte("x"))).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.dave.Delete(path+"/avatar").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	// carol and the admin can view nothing that involves the person.
	for _, c := range []*testkit.Client{f.carol, f.admin} {
		getProfile(c, f.person).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		patchProfile(c, f.person, dto.UpdateProfileRequest{Bio: ptr("Hijacked")}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		c.DoRaw(http.MethodPut, path+"/avatar", "image/png", bytes.NewReader([]byte("x"))).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		c.Delete(path+"/avatar").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		c.Get(path+"/avatar").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}
	f.env.Anon().Get(path).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	getProfile(f.alice.Token(dto.TokenKindAgent, "laptop"), f.person).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	require.Empty(t, profileOf(t, f.alice, f.person).Bio, "refused edits change nothing")

	// The members of the connection's organization are told the profile is history: nothing changes and nobody hears of a change.
	patchProfile(f.bob, f.person, dto.UpdateProfileRequest{Bio: ptr("Orders desk")}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeHistoryOnly)
	patchProfile(f.alice, f.person, dto.UpdateProfileRequest{Bio: ptr("Orders desk")}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeHistoryOnly)
	require.Empty(t, profileOf(t, f.alice, f.person).Bio)
	for _, live := range []*testkit.LiveConn{liveAlice, liveDave, liveCarol} {
		live.ExpectNone(300*time.Millisecond, dto.LiveEventTypeHello)
	}
}

// DM-08, PEOP-04 (AD-016, which supersedes AD-013): the managers of the AI that operates a connector account
// read the AI's platform DMs with external people and those people's profiles and avatars: the user that
// holds a position above the AI's, and the admin of the organization whose position is above that one. They
// belong to the AI's home organization, which is the connection's (ORG-07), so they see the account behind
// the person too. A user with no relation to the AI finds neither the DM nor the person, an admin of the
// instance included, and the members of the connection's organization see the person but not the AI's DM.
func TestManagersOfAnAIReadItsPlatformDMsAndPeople(t *testing.T) {
	ctx := context.Background()
	f := newPeopleFixture(t)
	atlas := f.env.NewAIIn(f.alice, f.line, "atlas")
	f.alice.OperateConnector(f.conn.ID, atlas)

	// the chart of "line": alice holds the root Admin; dave holds Lead below it and manages Atlas, who holds Worker below him.
	org := f.line
	root, err := f.env.App.Store.R().GetPositionByName(ctx, db.GetPositionByNameParams{OrgID: org, Name: "Admin"})
	require.NoError(t, err)
	lead := f.env.AddPosition(org, "Lead", root.ID)
	worker := f.env.AddPosition(org, "Worker", lead)
	f.env.AddMember(org, f.dave.User.ID, orgs.PermissionMember, lead) // dave works in "line" too
	require.NoError(t, f.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.UpdateOrgMemberPosition(ctx, db.UpdateOrgMemberPositionParams{
			PositionID: sql.NullString{String: worker, Valid: true}, OrgID: org, Iid: atlas,
		})
	}))

	f.clock.Advance(time.Minute)
	_, dm, person := f.platformDM(atlas, "5511999990003@s.whatsapp.net", "Dana", "hello Atlas")
	plantAvatar(t, f.env, person, pngBytes(t, bands(300, 300)))

	// alice and dave manage Atlas: both read the DM and the person, neither as a participant.
	for _, c := range []*testkit.Client{f.alice, f.dave} {
		room := testkit.Decode[dto.Room](c.Get("/api/v1/rooms/" + dm).RequireStatus(http.StatusOK))
		require.Equal(t, []any{dto.ConversationKindDM, dto.LevelView}, []any{room.Kind, room.MyLevel}, c.User.Username)
		msgs := testkit.Decode[dto.Page[dto.Message]](c.Get("/api/v1/rooms/" + dm + "/messages").RequireStatus(http.StatusOK)).Items
		require.Len(t, msgs, 1, c.User.Username)
		require.Equal(t, []any{"hello Atlas", person}, []any{msgs[0].Text, msgs[0].Author.ID}, c.User.Username)
		getAvatar(c, person).RequireStatus(http.StatusOK)
	}
	seen := profileOf(t, f.dave, person)
	require.Equal(t, []any{"Dana", connectortest.KindName, false}, []any{seen.DisplayName, seen.Platform, seen.CanEdit})
	require.NotNil(t, seen.Person, "dave belongs to the connection's organization, so he sees the account too")
	require.Equal(t, []dto.RoomRef{{ID: dm, Kind: dto.ConversationKindDM}}, seen.Conversations, "the DM with Atlas is the one conversation dave can view")
	patchProfile(f.dave, person, dto.UpdateProfileRequest{Bio: ptr("Hijacked")}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeHistoryOnly)

	// bob belongs to the connection's organization: he sees the person, whom nobody edits, but the DM is Atlas's, not his.
	require.False(t, profileOf(t, f.bob, person).CanEdit)
	f.bob.Get("/api/v1/rooms/"+dm).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	f.bob.Get("/api/v1/rooms/"+dm+"/messages").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// carol is in no organization of the connection, erin is a user of nothing here, and the admin of the
	// instance is no exception (AD-016): none of them finds the DM or the person.
	for _, c := range []*testkit.Client{f.carol, f.erin, f.admin} {
		f.env.T.Helper()
		c.Get("/api/v1/rooms/"+dm).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		c.Get("/api/v1/rooms/"+dm+"/messages").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		getAvatar(c, person).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}
	for _, c := range []*testkit.Client{f.carol, f.admin} {
		getProfile(c, person).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}
}

// A person is visible through the conversations that involve it: a chat where it was seen, or a
// room where it wrote; the access follows the viewer's access to the room (design, security).
func TestPersonProfileIsVisibleThroughAConversation(t *testing.T) {
	f := newPeopleFixture(t)

	// Room A: dave is in it and the person wrote there. Take him out and it is gone.
	require.Equal(t, "Customer", profileOf(t, f.dave, f.person).DisplayName)
	f.alice.Delete("/api/v1/rooms/" + f.roomA.ID + "/members/" + membersOf(t, f.alice, f.roomA.ID)[f.dave.User.ID].ID).RequireStatus(http.StatusNoContent)
	getProfile(f.dave, f.person).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	addToRoom(t, f.env, f.alice, f.roomA.ID, f.dave)
	require.Equal(t, "Customer", profileOf(t, f.dave, f.person).DisplayName)

	// Remove the chat from room A: the person's message stays there, so its viewers still see it.
	f.alice.Delete("/api/v1/rooms/" + f.roomA.ID + "/members/" + f.chatMember(f.alice, f.roomA.ID, f.chatX).ID).RequireStatus(http.StatusNoContent)
	require.Equal(t, "Customer", profileOf(t, f.dave, f.person).DisplayName, "a room where it wrote")

	// Room C: erin is in it; the person was seen in its chat but wrote nothing there.
	require.Equal(t, "Customer", profileOf(t, f.erin, f.person).DisplayName, "a chat where it was seen")
	f.alice.Delete("/api/v1/rooms/" + f.roomC.ID + "/members/" + f.chatMember(f.alice, f.roomC.ID, f.chatX).ID).RequireStatus(http.StatusNoContent)
	getProfile(f.erin, f.person).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// Room B: erin sees the person once she is in it, because it wrote there; frank views
	// nothing and still finds nothing.
	addToRoom(t, f.env, f.alice, f.roomB.ID, f.erin)
	require.Equal(t, "Customer", profileOf(t, f.erin, f.person).DisplayName)
	getProfile(f.env.NewUser("frank"), f.person).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// PROF-09: the room's files name a person that wrote one with its platform, so the list shows the
// platform badge next to the name.
func TestRoomFilesNameAPersonWithItsPlatform(t *testing.T) {
	f := newPeopleFixture(t)
	_, err := f.env.App.Rooms.PostFromChat(context.Background(), f.roomA.ID, rooms.ChatMessage{
		ChatID: f.chatX, SenderID: peopleSender, SenderName: "Customer", AuthorIID: f.person, Text: "the invoice",
		Files: []rooms.File{{Name: "invoice.pdf", Mime: "application/pdf", Status: dto.AttachmentStatusUnavailable}},
	})
	require.NoError(t, err)

	files := testkit.Decode[dto.Page[dto.RoomFile]](f.dave.Get("/api/v1/rooms/" + f.roomA.ID + "/attachments").RequireStatus(http.StatusOK)).Items
	require.Len(t, files, 1)
	require.Equal(t, dto.IntelligenceRef{
		ID: f.person, Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRolePerson, DisplayName: "Customer", Handle: "customer", Platform: connectortest.KindName,
	}, files[0].Author)
}

// PROF-07: the avatar a person has (a user of the connector set it before a person's profile became
// history, CID-21) is stored as a 256x256 PNG and served to whoever can see the person: the users of
// the connector and the viewers of a room where it wrote or of a chat where it was seen; carol and
// the admin get 404.
func TestPersonAvatarIsServedToWhoCanSeeThePerson(t *testing.T) {
	f := newPeopleFixture(t)
	getAvatar(f.alice, f.person).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	set := dto.IntelligenceProfile{}
	set.AvatarURL = plantAvatar(t, f.env, f.person, pngBytes(t, bands(400, 200)))
	require.Regexp(t, avatarURLPattern, set.AvatarURL)
	viewers := map[string]*testkit.Client{
		"the connector's owner": f.alice, "a user of the connector": f.bob,
		"a viewer of a room it wrote in": f.dave, "a viewer of a chat it was seen in": f.erin,
	}
	for name, c := range viewers {
		resp := getAvatar(c, f.person).RequireStatus(http.StatusOK)
		require.Equal(t, "image/png", resp.Header.Get("Content-Type"), name)
		requireGreenSquare(t, resp.Body)
		require.Equal(t, set.AvatarURL, profileOf(t, c, f.person).AvatarURL, name)
	}
	for _, c := range []*testkit.Client{f.carol, f.admin} {
		getAvatar(c, f.person).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		getProfile(c, f.person).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}
}

// PEOP-03: the person page shows the name, the platform account and only the conversations the
// caller can view, the most recently active first. A viewer without access to the connector sees
// the platform but not the account.
func TestPersonPageListsTheAccountAndTheConversationsTheCallerCanView(t *testing.T) {
	f := newPeopleFixture(t)

	owner := profileOf(t, f.alice, f.person)
	require.Equal(t, "Customer", owner.DisplayName)
	require.Equal(t, []dto.App{{Kind: connectortest.KindName, Label: "Fake: " + peopleSender, ConnectorID: f.conn.ID}}, owner.Apps)
	require.Equal(t, dto.ConnectorRef{ID: f.conn.ID, Kind: connectortest.KindName, Name: "wa"}, owner.Person.Connector)
	require.Equal(t, peopleSender, owner.Person.ExternalID)
	require.Nil(t, owner.Person.LinkedUser)
	require.False(t, owner.Person.NameLocked)
	require.Equal(t, []dto.RoomRef{{ID: f.roomC.ID, Name: "Zulu archive"}, {ID: f.roomB.ID, Name: "Billing"}, {ID: f.roomA.ID, Name: "Support"}},
		owner.Conversations, "the owner views them all, the most recently active first (a room without messages counts from its creation)")

	viewer := profileOf(t, f.dave, f.person)
	require.Equal(t, []dto.RoomRef{{ID: f.roomA.ID, Name: "Support"}}, viewer.Conversations, "dave views room A only")
	require.Equal(t, []dto.App{{Kind: connectortest.KindName, Label: "Fake"}}, viewer.Apps, "the platform, not the account")

	erin := profileOf(t, f.erin, f.person)
	require.Equal(t, []dto.RoomRef{{ID: f.roomC.ID, Name: "Zulu archive"}}, erin.Conversations, "a room whose chat saw the person counts")

	bob := profileOf(t, f.bob, f.person)
	require.NotNil(t, bob.Conversations, "an empty list, not null")
	require.Empty(t, bob.Conversations, "the connector's users see the person, not the rooms they cannot view")
}

// The conversations of a person are cut to the 100 most recently active ones the caller can view.
func TestPersonPageListsAtMost100Conversations(t *testing.T) {
	f := newPeopleFixture(t)
	var newest []string // newest first
	for i := range 101 {
		f.clock.Advance(time.Second)
		room := testkit.Decode[dto.Room](f.alice.Post("/api/v1/rooms", dto.CreateRoomRequest{Name: fmt.Sprintf("Room %03d", i)}).RequireStatus(http.StatusCreated))
		f.addChat(f.alice, room.ID, f.chatX)
		newest = append([]string{room.ID}, newest...)
	}

	got := profileOf(t, f.alice, f.person).Conversations
	require.Len(t, got, 100)
	for i, c := range got {
		require.Equal(t, newest[i], c.ID, "position %d", i)
	}
	require.Len(t, profileOf(t, f.dave, f.person).Conversations, 1, "the cut is made among the rooms the caller can view")
}

// PEOP-02: a person whose account a user verified (pairing, AD-008) shows who it is; the link
// is for the users of the connector.
func TestPersonPageShowsTheUserItIsLinkedTo(t *testing.T) {
	f := newPeopleFixture(t)
	code := testkit.Decode[dto.PairingCode](f.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: f.conn.ID}).RequireStatus(http.StatusCreated))
	f.clock.Advance(time.Minute)
	f.emit(peopleGroupX, "link", peopleSender, "Customer")
	require.Nil(t, profileOf(t, f.alice, f.person).Person.LinkedUser, "an ordinary message is no link")
	f.inst.Emit(context.Background(), connectors.Inbound{
		ChatID: peopleGroupX, ChatKind: "group", MessageID: "link2", SenderID: peopleSender, SenderName: "Customer", Text: "!link " + code.Code, At: f.clock.Now(),
	})

	for _, c := range []*testkit.Client{f.alice, f.bob} {
		linked := profileOf(t, c, f.person).Person.LinkedUser
		require.NotNil(t, linked)
		require.Equal(t, []any{f.alice.User.ID, dto.IntelligenceRoleUser, "alice"}, []any{linked.ID, linked.Role, linked.Handle})
	}
	require.Nil(t, profileOf(t, f.dave, f.person).Person, "the viewer of a room does not get the account")
}

// PEOP-02: the chat accounts a user verified are the user's apps, for the user and for the users
// of the connector they were linked through, and for nobody else.
func TestLinkedAccountsAreAppsOfTheUser(t *testing.T) {
	f := newPeopleFixture(t)
	web := dto.App{Kind: "web", Label: "Web app"}
	require.Equal(t, []dto.App{web}, profileOf(t, f.alice, f.alice.User.ID).Apps, "the connection is its operator AI's app, not the owner's")

	code := testkit.Decode[dto.PairingCode](f.alice.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: f.conn.ID}).RequireStatus(http.StatusCreated))
	f.clock.Advance(time.Minute)
	f.inst.Emit(context.Background(), connectors.Inbound{
		ChatID: peopleGroupX, ChatKind: "group", MessageID: "link", SenderID: peopleSender, SenderName: "Customer", Text: "!link " + code.Code, At: f.clock.Now(),
	})

	account := dto.App{Kind: connectortest.KindName, Label: "Fake: " + peopleSender, ConnectorID: f.conn.ID}
	require.Equal(t, []dto.App{web, account}, profileOf(t, f.alice, f.alice.User.ID).Apps, "the user's own accounts")
	require.Equal(t, []dto.App{web, account}, profileOf(t, f.bob, f.alice.User.ID).Apps, "bob may use the connector it was linked through")
	for _, c := range []*testkit.Client{f.carol, f.dave} {
		require.Equal(t, []dto.App{web}, profileOf(t, c, f.alice.User.ID).Apps, "no access to the connector, no account")
	}
	require.Equal(t, []dto.App{web}, profileOf(t, f.alice, f.bob.User.ID).Apps, "another user's accounts are theirs")

	idents := testkit.Decode[dto.List[dto.Identity]](f.alice.Get("/api/v1/identities").RequireStatus(http.StatusOK)).Items
	f.alice.Delete("/api/v1/identities/" + idents[0].ID).RequireStatus(http.StatusNoContent)
	require.Equal(t, []dto.App{web}, profileOf(t, f.alice, f.alice.User.ID).Apps)
}

// GRP-02: a room's members list shows, for each bridged chat, the people seen in it with their
// platform, the most recently seen first and at most 200 of them; a person seen in two chats of
// the room is listed under both; agent and user members carry none.
func TestRoomMembersShowEachChatsPeople(t *testing.T) {
	f := newPeopleFixture(t)
	f.addChat(f.alice, f.roomA.ID, f.chatY)
	f.clock.Advance(time.Minute)
	f.emit(peopleGroupX, "x2", "5511999990001@s.whatsapp.net", "Dana")

	x := f.chatMember(f.alice, f.roomA.ID, f.chatX)
	require.Len(t, x.People, 2)
	require.Equal(t, []any{"Dana", dto.IntelligenceRolePerson, connectortest.KindName, "dana"},
		[]any{x.People[0].DisplayName, x.People[0].Role, x.People[0].Platform, x.People[0].Handle}, "Dana wrote last")
	require.Equal(t, f.person, x.People[1].ID)
	require.Equal(t, dto.IntelligenceKindHuman, x.People[1].Kind)
	y := f.chatMember(f.alice, f.roomA.ID, f.chatY)
	require.Len(t, y.People, 1)
	require.Equal(t, f.person, y.People[0].ID, "the same person is listed under chat Y too")

	// dave views the room and sees the same people; carol cannot see the members at all.
	require.Len(t, f.chatMember(f.dave, f.roomA.ID, f.chatX).People, 2)
	f.carol.Get("/api/v1/rooms/"+f.roomA.ID+"/members").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// 205 more senders: the list keeps the 200 most recently seen.
	for i := range 205 {
		f.clock.Advance(time.Second)
		f.emit(peopleGroupX, fmt.Sprintf("s%03d", i), fmt.Sprintf("55119998%05d@s.whatsapp.net", i), fmt.Sprintf("Sender %03d", i))
	}
	people := f.chatMember(f.alice, f.roomA.ID, f.chatX).People
	require.Len(t, people, 200)
	require.Equal(t, "Sender 204", people[0].DisplayName)
	require.Equal(t, "Sender 005", people[199].DisplayName)
	for _, p := range people {
		require.NotEqual(t, f.person, p.ID, "the oldest sightings are cut")
		require.NotEqual(t, "Dana", p.DisplayName)
	}
	require.Len(t, f.chatMember(f.alice, f.roomA.ID, f.chatY).People, 1, "another chat keeps its own list")
}

// Agent and user members have no people; a chat nobody was seen in lists none.
func TestMembersOtherThanChatsCarryNoPeople(t *testing.T) {
	f := newPeopleFixture(t)
	agentID := f.env.NewAI(f.alice, "helper")
	f.alice.Post("/api/v1/rooms/"+f.roomA.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: agentID}).RequireStatus(http.StatusCreated)
	addToRoom(t, f.env, f.alice, f.roomA.ID, f.bob)
	f.inst.Emit(context.Background(), connectors.Inbound{ChatID: "channel-9", ChatTitle: "Quiet", ChatKind: "group", MessageID: "q0", SenderID: "u9", Text: "x", At: f.clock.Now()})
	var quiet string
	for _, c := range testkit.Decode[dto.List[dto.Chat]](f.alice.Get("/api/v1/connectors/" + f.conn.ID + "/chats").RequireStatus(http.StatusOK)).Items {
		if c.ExternalID == "channel-9" {
			quiet = c.ID
		}
	}
	f.addChat(f.alice, f.roomA.ID, quiet)

	kinds := map[dto.MemberKind]int{}
	for _, m := range f.members(f.alice, f.roomA.ID) {
		kinds[m.Kind]++
		if m.MemberID != f.chatX {
			require.Empty(t, m.People, "%s member %s", m.Kind, m.Name)
		}
	}
	require.Equal(t, map[dto.MemberKind]int{dto.MemberKindChat: 2, dto.MemberKindAgent: 1, dto.MemberKindUser: 2}, kinds, "two chats, the helper, and the users dave and bob")
	require.NotEmpty(t, f.chatMember(f.alice, f.roomA.ID, f.chatX).People)
}

// PEOP-04, ORG-02, AD-016: the people a connection has seen, their profiles, the chat accounts users linked
// through it and the connection's app on its operator's page are for the members of the connection's
// organization, guests included, and for nobody else: a user outside it and an admin of the instance find the
// list, the person, the account and the app nowhere.
func TestAConnectionsPeopleAreForTheMembersOfItsOrganization(t *testing.T) {
	env, _ := fakeConnectorEnv(t)
	roles := env.OrgRoles("acme")
	off := false
	conn := testkit.Decode[dto.Connector](roles.Admin.Post("/api/v1/connectors", dto.CreateConnectorRequest{
		OrgID: roles.Org.ID, Kind: connectortest.KindName, Name: "wa", Config: map[string]any{"token": "fake-token-not-real"}, Enabled: &off,
	}).RequireStatus(http.StatusCreated))
	roles.Admin.OperateConnector(conn.ID, roles.TargetAI)
	pat := seedPerson(t, env, conn.ID, "5511999990000", "Pat")

	// The target links a chat account through the connection (any member may pair through an ordinary one).
	code := testkit.Decode[dto.PairingCode](roles.Target.Post("/api/v1/identities/pairing", dto.PairingRequest{ConnectorID: conn.ID}).RequireStatus(http.StatusCreated))
	_, ok, err := env.App.Identities.Link(context.Background(), code.Code, identities.Account{
		ConnectorID: conn.ID, ConnectorKind: connectortest.KindName, SenderID: "target-phone", Display: "Target",
	}, "")
	require.NoError(t, err)
	require.True(t, ok)

	account := dto.App{Kind: connectortest.KindName, Label: "Fake: target-phone", ConnectorID: conn.ID}
	web := dto.App{Kind: "web", Label: "Web app"}
	operated := dto.App{Kind: connectortest.KindName, Label: "Fake: wa", ConnectorID: conn.ID}
	for _, role := range roles.All() {
		inOrg := role.Name != "outsider" && role.Name != "instance_admin"
		c := role.Client

		peoplePath := "/api/v1/contacts/people?connector_id=" + conn.ID
		groups := contactsOf(t, c).People
		person := c.Get("/api/v1/intelligences/" + pat)
		targetApps := profileOf(t, c, roles.Target.User.ID).Apps
		if !inOrg {
			c.Get(peoplePath).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
			person.RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
			getAvatar(c, pat).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
			getProfile(c, roles.TargetAI).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
			for _, g := range groups {
				require.NotEqual(t, conn.ID, g.Connector.ID, "%s: the connection is not in their contacts", role.Name)
			}
			require.Equal(t, []dto.App{web}, targetApps, "%s: the user, not the accounts behind it", role.Name)
			continue
		}
		page := testkit.Decode[dto.Page[dto.IntelligenceRef]](c.Get(peoplePath).RequireStatus(http.StatusOK))
		require.Len(t, page.Items, 1, role.Name)
		require.Equal(t, pat, page.Items[0].ID, role.Name)
		var seen *dto.PeopleGroup
		for i := range groups {
			if groups[i].Connector.ID == conn.ID {
				seen = &groups[i]
			}
		}
		require.NotNil(t, seen, "%s: the connection is in their contacts", role.Name)
		require.Equal(t, 1, seen.Count, role.Name)
		profile := testkit.Decode[dto.IntelligenceProfile](person.RequireStatus(http.StatusOK))
		require.NotNil(t, profile.Person, "%s: the account behind the person", role.Name)
		require.Equal(t, "5511999990000", profile.Person.ExternalID, role.Name)
		require.Equal(t, []dto.App{web, account}, targetApps, "%s: the account is an app of the user it was linked to", role.Name)
		require.Contains(t, profileOf(t, c, roles.TargetAI).Apps, operated, "%s: the connection is an app of its operator", role.Name)
	}
}
