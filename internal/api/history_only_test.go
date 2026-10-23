package api_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/conversations"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/testkit"
)

// historyFixture is the people fixture with the AI Atlas as operator of the connection, one platform
// DM between Atlas and a person (what the connection proxy left as history) with its message, and
// the clients' view of it: alice owns Atlas and the connection, bob may use the connection only,
// dave may use Atlas only, erin can view a room where the person was seen, carol and the admin have
// nothing.
//
// A DM names its two participants in sorted order, so the rule has to hold whichever side the person
// is on: Atlas existed before its person (the person's id sorts after Atlas's), and the second AI
// "late" was created after its person (the person's id sorts before the AI's).
type historyFixture struct {
	*peopleFixture
	atlas   string
	atlasAI *testkit.Client // Atlas's REST client
	chat    string
	dm      string
	person  string // the person of the DM (not the fixture's "Customer")
	message string // the person's message in the DM

	late, latePerson string
	lateAI           *testkit.Client
	lateDM           string
	lateMessage      string
}

func newHistoryFixture(t *testing.T) *historyFixture {
	t.Helper()
	f := &historyFixture{peopleFixture: newPeopleFixture(t)}
	f.atlas = f.env.NewAI(f.alice, "atlas")
	shareAI(f.alice, f.atlas, f.dave)
	f.alice.OperateConnector(f.conn.ID, f.atlas)
	f.clock.Advance(time.Minute)
	f.chat, f.dm, f.person = f.platformDM(f.atlas, "5511999990009@s.whatsapp.net", "Pat", "hello Atlas")
	f.atlasAI = f.alice.Token(dto.TokenKindAgent, "laptop").AsAgent("atlas")
	f.message = f.onlyMessage(t, f.dm)
	require.Less(t, f.atlas, f.person, "Atlas is the first participant of its DM")

	// The second AI is created after its person.
	ctx := context.Background()
	f.latePerson = seedPerson(t, f.env, f.conn.ID, "5511999990010@s.whatsapp.net", "Quinn")
	f.late = f.env.NewAI(f.alice, "late")
	require.Less(t, f.latePerson, f.late, "the person is the first participant of the DM with the late AI")
	at := f.clock.Now()
	c, err := connectors.UpsertChat(ctx, f.env.App.Store, f.env.App.Bus, f.conn.ID, connectors.ChatInfo{ID: "5511999990010@s.whatsapp.net", Title: "Quinn", Kind: "dm"}, at, at)
	require.NoError(t, err)
	route, err := f.env.App.Conversations.EnsurePlatformDM(ctx, conversations.PlatformDM{
		OwnerID: f.alice.User.ID, ChatID: c.ID, Author: conversations.Party{ID: f.latePerson}, Operator: conversations.Party{ID: f.late, Kind: dto.MemberKindAgent},
	})
	require.NoError(t, err)
	f.lateDM = route.RoomID
	_, err = f.env.App.Rooms.PostFromChat(ctx, f.lateDM, rooms.ChatMessage{
		ChatID: c.ID, SenderID: "5511999990010@s.whatsapp.net", SenderName: "Quinn", AuthorIID: f.latePerson, ExternalID: "d1", Kind: dto.MessageKindText, Text: "hello Late",
	})
	require.NoError(t, err)
	f.lateAI = f.alice.Token(dto.TokenKindAgent, "laptop").AsAgent("late")
	f.lateMessage = f.onlyMessage(t, f.lateDM)
	return f
}

// onlyMessage returns the id of the one message of the DM, as alice reads it.
func (f *historyFixture) onlyMessage(t *testing.T, room string) string {
	t.Helper()
	msgs := testkit.Decode[dto.Page[dto.Message]](f.alice.Get("/api/v1/rooms/" + room + "/messages").RequireStatus(http.StatusOK)).Items
	require.Len(t, msgs, 1)
	return msgs[0].ID
}

// texts of the DM as alice reads them, oldest first.
func (f *historyFixture) history(t *testing.T, c *testkit.Client) []string {
	t.Helper()
	return texts(timeline(t, c, f.dm))
}

// CID-21 (L-017, L-018): the AI posts in its old platform DM with a person, which is history now:
// 422 history_only for a post, a reply to the person's message and a reaction to it, and the
// history is unchanged and still readable by everyone who could read it. Every other guard accepts
// the request: the AI is a participant of the DM with the right to post, the person is no disabled
// user, the text is valid.
func TestPostInAPlatformDMIsRefusedAsHistory(t *testing.T) {
	f := newHistoryFixture(t)

	for _, c := range []struct {
		name         string
		ai           *testkit.Client
		dm, message  string
		opening, say string
	}{
		{"the person is the second participant", f.atlasAI, f.dm, f.message, "hello Atlas", "hello Pat"},
		{"the person is the first participant", f.lateAI, f.lateDM, f.lateMessage, "hello Late", "hello Quinn"},
	} {
		e := c.ai.Post("/api/v1/agent/rooms/"+c.dm+"/messages", dto.PostMessageRequest{Text: c.say}).
			RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeHistoryOnly)
		require.NotEmpty(t, e.Message, c.name)
		c.ai.Post("/api/v1/agent/messages/"+c.message+"/reply", dto.ReplyRequest{Text: "answering"}).
			RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeHistoryOnly)
		c.ai.Post("/api/v1/agent/messages/"+c.message+"/react", dto.ReactRequest{Emoji: "👍"}).
			RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeHistoryOnly)
		require.Equal(t, []string{c.opening}, texts(timeline(t, f.alice, c.dm)), "%s: nothing was added", c.name)
	}

	// Reading history still works: the owner and the use grantee of Atlas read the DM, the AI reads
	// its own history, and the room is listed.
	require.Equal(t, []string{"hello Atlas"}, f.history(t, f.dave))
	hist := testkit.Decode[dto.Page[dto.AgentMessage]](f.atlasAI.Get("/api/v1/agent/rooms/" + f.dm + "/messages").RequireStatus(http.StatusOK))
	require.Len(t, hist.Items, 1)
	require.Equal(t, "hello Atlas", hist.Items[0].Message.Text)
	platformDM := testkit.Decode[dto.Room](f.alice.Get("/api/v1/rooms/" + f.dm).RequireStatus(http.StatusOK))
	require.Equal(t, []any{dto.ConversationKindDM, dto.LevelView}, []any{platformDM.Kind, platformDM.MyLevel}, "the owner still reads the DM")
	f.atlasAI.Get("/api/v1/agent/rooms/" + f.dm + "/info").RequireStatus(http.StatusOK)

	// A DM with a user or an AI is a conversation: the same AI posts there as before.
	other := f.env.NewAI(f.alice, "orion")
	dm := dmWith(f.alice, other, http.StatusCreated)
	f.atlasAI.Post("/api/v1/agent/dms", dto.AgentDMRequest{To: "@orion", Text: "hello Orion"}).RequireStatus(http.StatusCreated)
	require.NotEmpty(t, dm.ID)
}

// CID-21 (authorization matrix of POST /rooms/{id}/messages in a platform DM): who may not post in
// the DM are refused as before, whatever else is true of it: the owner and the use grantee of the
// AI can only view it (403 forbidden), the connection's user and the others cannot see it (404), and
// anonymous callers get 401; another AI of the owner and another user's AI are not members (403).
// The refusal for history is the answer of the one who may post, and nothing is added.
func TestPlatformDMPostAuthorizationMatrix(t *testing.T) {
	f := newHistoryFixture(t)
	path := "/api/v1/rooms/" + f.dm + "/messages"
	post := func(c *testkit.Client) *testkit.Response { return c.Post(path, dto.PostMessageRequest{Text: "hello"}) }

	post(f.alice).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden) // owns Atlas: views the DM
	post(f.dave).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)  // may use Atlas: views the DM
	post(f.bob).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)     // may use the connection, not Atlas
	post(f.carol).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)   // stranger
	post(f.admin).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)   // no implicit access
	post(f.env.Anon()).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)

	agentPost := func(c *testkit.Client) *testkit.Response {
		return c.Post("/api/v1/agent/rooms/"+f.dm+"/messages", dto.PostMessageRequest{Text: "hello"})
	}
	owner := f.alice.Token(dto.TokenKindAgent, "laptop")
	f.env.NewAI(f.alice, "orion")
	f.env.NewAI(f.bob, "bobby")
	agentPost(owner.AsAgent("orion")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)                                   // another AI of the owner: not a member
	agentPost(f.bob.Token(dto.TokenKindAgent, "desk").AsAgent("bobby")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden) // another user's AI: not a member
	agentPost(f.atlasAI).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeHistoryOnly)                                    // the participant

	require.Equal(t, []string{"hello Atlas"}, f.history(t, f.alice), "nothing was added")
}

// CID-21, CID-22: a person's profile is history: the users of the connection, who could edit it
// before, get 422 history_only for a change of name, handle or bio, an avatar and the removal of one,
// whatever the request carries; a user who can only read the person is still forbidden, a stranger
// and the admin still find nothing, anonymous callers are refused, and AI tokens are not users. The
// profile is unchanged and still readable, and says nobody can edit it.
func TestEditingAPersonIsRefusedAsHistory(t *testing.T) {
	f := newPeopleFixture(t)
	path := "/api/v1/intelligences/" + f.person
	before := profileOf(t, f.alice, f.person)
	live, _ := f.alice.DialLive("")
	live.Expect(dto.LiveEventTypeHello, nil, connectortest.Timeout)

	for _, c := range []*testkit.Client{f.alice, f.bob} { // owner and use grantee of the connection
		name, handle, bio := "Renamed", "renamed", "A new bio"
		for _, req := range []dto.UpdateProfileRequest{{DisplayName: &name}, {Handle: &handle}, {Bio: &bio}, {DisplayName: &name, Handle: &handle, Bio: &bio}} {
			e := patchProfile(c, f.person, req).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeHistoryOnly)
			require.NotEmpty(t, e.Message, c.User.Username)
		}
		putAvatar(c, f.person, pngBytes(t, bands(300, 300))).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeHistoryOnly)
		c.Delete(path+"/avatar").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeHistoryOnly)
	}

	// Who could not edit it before are refused as before: the authorization comes first.
	patchProfile(f.dave, f.person, dto.UpdateProfileRequest{Bio: ptr("x")}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden) // reads it through room A
	f.dave.DoRaw(http.MethodPut, path+"/avatar", "image/png", bytes.NewReader([]byte("x"))).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.dave.Delete(path+"/avatar").RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	for _, c := range []*testkit.Client{f.carol, f.admin} {
		patchProfile(c, f.person, dto.UpdateProfileRequest{Bio: ptr("x")}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		putAvatar(c, f.person, pngBytes(t, bands(300, 300))).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
		c.Delete(path+"/avatar").RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	}
	patchProfile(f.env.Anon(), f.person, dto.UpdateProfileRequest{Bio: ptr("x")}).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	patchProfile(f.alice.Token(dto.TokenKindAgent, "laptop"), f.person, dto.UpdateProfileRequest{Bio: ptr("x")}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	// Nothing changed, nothing was announced, and reading still works for everyone who read it.
	after := profileOf(t, f.alice, f.person)
	require.Equal(t, []any{before.DisplayName, before.Handle, before.Bio, before.AvatarURL}, []any{after.DisplayName, after.Handle, after.Bio, after.AvatarURL})
	live.ExpectNone(300 * time.Millisecond)
	for _, c := range []*testkit.Client{f.alice, f.bob, f.dave, f.erin} {
		require.Equal(t, "Customer", profileOf(t, c, f.person).DisplayName, c.User.Username)
	}
	require.False(t, profileOf(t, f.alice, f.person).CanEdit, "the owner of the connection cannot edit a person any more")
	require.False(t, profileOf(t, f.bob, f.person).CanEdit, "nor the use grantee")
	require.False(t, profileOf(t, f.dave, f.person).CanEdit)
}

// CID-22: the profile of an AI and of a user are edited as before: a person's are the only ones that
// are history.
func TestOtherProfilesAreStillEdited(t *testing.T) {
	f := newPeopleFixture(t)
	atlas := f.env.NewAI(f.alice, "atlas")

	patchProfile(f.alice, atlas, dto.UpdateProfileRequest{Bio: ptr("Writes the docs")}).RequireStatus(http.StatusOK)
	patchProfile(f.alice, f.alice.User.ID, dto.UpdateProfileRequest{Bio: ptr("The owner")}).RequireStatus(http.StatusOK)
	require.True(t, profileOf(t, f.alice, atlas).CanEdit)
	require.True(t, profileOf(t, f.alice, f.alice.User.ID).CanEdit)
}

// CID-22, DM-05 retired: an AI cannot write to a person by name any more, even the AI that operated
// the connection the person wrote to and holds the platform DM with them: send_message to @person
// answers no_route_to_person (the AI answers on its connection instead), nothing is posted, and the
// user's own request to open a DM with a person is refused the same way. An AI's DM with a user or
// another AI is unaffected.
func TestDMToAPersonHasNoRoute(t *testing.T) {
	f := newHistoryFixture(t)
	personHandle := profileOf(t, f.alice, f.person).Handle

	for _, to := range []string{"@" + personHandle, personHandle, f.person} {
		f.atlasAI.Post("/api/v1/agent/dms", dto.AgentDMRequest{To: to, Text: "hello Pat"}).
			RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeNoRouteToPerson)
	}
	rooms := roomIDs(roomListOf(t, f.alice))
	openDM(f.alice, f.person).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeNoRouteToPerson) // owns Atlas and the connection
	openDM(f.bob, f.person).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeNoRouteToPerson)   // may use the connection: sees the person
	openDM(f.carol, f.person).RequireError(http.StatusNotFound, dto.ErrorCodeContactNotFound)            // cannot see the person
	require.Equal(t, []string{"hello Atlas"}, f.history(t, f.alice), "nothing was posted into the DM")
	require.Equal(t, rooms, roomIDs(roomListOf(t, f.alice)), "no DM was created")
}
