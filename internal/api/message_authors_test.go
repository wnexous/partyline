package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

func timelineOf(t *testing.T, c *testkit.Client, roomID string) []dto.Message {
	t.Helper()
	page := testkit.Decode[dto.Page[dto.Message]](c.Get("/api/v1/rooms/" + roomID + "/messages").RequireStatus(http.StatusOK))
	return page.Items
}

func membersOf(t *testing.T, c *testkit.Client, roomID string) map[string]dto.RoomMember {
	t.Helper()
	list := testkit.Decode[dto.List[dto.RoomMember]](c.Get("/api/v1/rooms/" + roomID + "/members").RequireStatus(http.StatusOK))
	out := map[string]dto.RoomMember{}
	for _, m := range list.Items {
		out[m.MemberID] = m
	}
	return out
}

// AID-06, PROF-03, GRP-04: after renaming a user and an AI (name, handle, avatar), old and new
// messages in the timeline and the member list show the new display name, handle and avatar;
// the per-room agent display name still wins in its room.
func TestRenamedAuthorsShowEverywhere(t *testing.T) {
	f := newAgentRoom(t)
	bob := f.env.NewUser("bob")
	addToRoom(t, f.env, f.alice, f.room.ID, bob)

	oldAlice := f.post(f.alice, "alice before")
	oldBob := f.post(bob, "bob before")
	oldAgent := f.postAsAgent(t, "agent before")
	before := timelineOf(t, f.alice, f.room.ID)
	for _, m := range before {
		require.NotEmpty(t, m.Author.ID, "every message has its author")
	}

	pic := pngBytes(t, bands(300, 300))
	patchProfile(f.alice, f.alice.User.ID, dto.UpdateProfileRequest{DisplayName: ptr("Alice A."), Handle: ptr("alice-a")}).RequireStatus(http.StatusOK)
	putAvatar(f.alice, f.alice.User.ID, pic).RequireStatus(http.StatusOK)
	patchProfile(bob, bob.User.ID, dto.UpdateProfileRequest{DisplayName: ptr("Bob B."), Handle: ptr("bobby")}).RequireStatus(http.StatusOK)
	patchProfile(f.alice, f.agentID, dto.UpdateProfileRequest{DisplayName: ptr("Atlas"), Handle: ptr("atlas")}).RequireStatus(http.StatusOK)
	putAvatar(f.alice, f.agentID, pic).RequireStatus(http.StatusOK)
	newAlice := f.post(f.alice, "alice after")
	newAgent := f.postAsAgent(t, "agent after")

	alicePic := profileOf(t, f.alice, f.alice.User.ID).AvatarURL
	agentPic := profileOf(t, f.alice, f.agentID).AvatarURL
	require.NotEmpty(t, alicePic)
	require.NotEmpty(t, agentPic)

	byID := map[string]dto.Message{}
	for _, m := range timelineOf(t, bob, f.room.ID) {
		byID[m.ID] = m
	}
	for _, c := range []struct {
		name   string
		id     string
		author dto.IntelligenceRef
	}{
		{"alice, old", oldAlice.ID, dto.IntelligenceRef{ID: f.alice.User.ID, Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "Alice A.", Handle: "alice-a", AvatarURL: alicePic}},
		{"alice, new", newAlice.ID, dto.IntelligenceRef{ID: f.alice.User.ID, Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "Alice A.", Handle: "alice-a", AvatarURL: alicePic}},
		{"bob, old", oldBob.ID, dto.IntelligenceRef{ID: bob.User.ID, Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "Bob B.", Handle: "bobby"}},
		{"agent, old", oldAgent.ID, dto.IntelligenceRef{ID: f.agentID, Kind: dto.IntelligenceKindAI, Role: dto.IntelligenceRoleAI, DisplayName: "Atlas", Handle: "atlas", AvatarURL: agentPic}},
		{"agent, new", newAgent.ID, dto.IntelligenceRef{ID: f.agentID, Kind: dto.IntelligenceKindAI, Role: dto.IntelligenceRoleAI, DisplayName: "Atlas", Handle: "atlas", AvatarURL: agentPic}},
	} {
		got, ok := byID[c.id]
		require.True(t, ok, c.name)
		require.Equal(t, c.author, got.Author, c.name)
		require.Equal(t, c.author.DisplayName, got.AuthorName, c.name)
	}

	members := membersOf(t, bob, f.room.ID)
	agent, user := members[f.agentID], members[bob.User.ID]
	require.Equal(t, "Atlas", agent.Name)
	require.NotNil(t, agent.Intelligence)
	require.Equal(t, dto.IntelligenceRef{ID: f.agentID, Kind: dto.IntelligenceKindAI, Role: dto.IntelligenceRoleAI, DisplayName: "Atlas", Handle: "atlas", AvatarURL: agentPic}, *agent.Intelligence)
	require.Equal(t, "Bob B.", user.Name)
	require.NotNil(t, user.Intelligence)
	require.Equal(t, dto.IntelligenceRef{ID: bob.User.ID, Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "Bob B.", Handle: "bobby"}, *user.Intelligence)

	// GRP-04: the name set for the agent in this room wins in this room's timeline; the member
	// list keeps the profile name and carries the room name apart.
	setMember(t, f.alice, f.room.ID, f.agentID, dto.UpdateMemberRequest{DisplayName: ptr("Vega")})
	byID = map[string]dto.Message{}
	for _, m := range timelineOf(t, f.alice, f.room.ID) {
		byID[m.ID] = m
	}
	for _, id := range []string{oldAgent.ID, newAgent.ID} {
		require.Equal(t, "Vega", byID[id].AuthorName)
		require.Equal(t, "Vega", byID[id].Author.DisplayName)
		require.Equal(t, "atlas", byID[id].Author.Handle, "the handle is the profile's")
	}
	agent = membersOf(t, f.alice, f.room.ID)[f.agentID]
	require.Equal(t, []any{"Atlas", "Vega", "Atlas"}, []any{agent.Name, agent.DisplayName, agent.Intelligence.DisplayName})
}

// Chat members have no intelligence of their own: the person of a chat shows through the
// authors of its messages (GRP-02 comes with the people tasks).
func TestChatMembersCarryNoIntelligence(t *testing.T) {
	env := testkit.New(t)
	alice := env.NewUser("alice")
	_, chatID := storeChat(t, env, alice.User.ID, "family")
	room := createRoom(alice, "ops")
	alice.SeedChatMember(room.ID, chatID)
	m := membersOf(t, alice, room.ID)[chatID]
	require.Equal(t, "family", m.Name)
	require.Nil(t, m.Intelligence)
}

// GRP-03 (display path): a chat-authored message exposes the author intelligence once
// author_iid is set: a person with its platform, or the linked user; without one only the
// sender name shows.
func TestChatAuthoredMessagesExposeTheirAuthor(t *testing.T) {
	env := testkit.New(t)
	ctx := context.Background()
	alice := env.NewUser("alice")
	connectorID, chatID := storeChat(t, env, alice.User.ID, "family")
	room := createRoom(alice, "ops")
	alice.SeedChatMember(room.ID, chatID)
	pat := seedPerson(t, env, connectorID, "5511999990000", "Pat")

	post := func(text, senderName, senderUserID string) rooms.Message {
		t.Helper()
		m, err := env.App.Rooms.PostFromChat(ctx, room.ID, rooms.ChatMessage{
			ChatID: chatID, SenderID: "5511999990000", SenderName: senderName, SenderUserID: senderUserID, Text: text,
		})
		require.NoError(t, err)
		return m
	}
	setAuthor := func(messageID, iid string) {
		t.Helper()
		require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
			return q.SetMessageAuthorIID(ctx, db.SetMessageAuthorIIDParams{AuthorIid: iid, ID: messageID})
		}))
	}
	fromPerson, linked, anonymous := post("from a person", "Pat P.", ""), post("from a linked user", "Alice phone", alice.User.ID), post("no author yet", "Pat P.", "")
	setAuthor(fromPerson.ID, pat)
	setAuthor(linked.ID, alice.User.ID)

	byID := map[string]dto.Message{}
	for _, m := range timelineOf(t, alice, room.ID) {
		byID[m.ID] = m
	}
	require.Equal(t, dto.IntelligenceRef{
		ID: pat, Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRolePerson, DisplayName: "Pat", Handle: "pat", Platform: "telegram",
	}, byID[fromPerson.ID].Author, "a person: platform badge and handle")
	require.Equal(t, "telegram", byID[fromPerson.ID].Chat.Platform)

	require.Equal(t, dto.IntelligenceRef{
		ID: alice.User.ID, Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "alice", Handle: "alice",
	}, byID[linked.ID].Author, "the linked user's own intelligence, no platform")

	require.Equal(t, dto.IntelligenceRef{DisplayName: "Pat P."}, byID[anonymous.ID].Author, "no intelligence yet: only the shown name")
}

// Room owners are named by their intelligence too: GET /rooms/{id} and the list carry the
// owner's handle.
func TestRoomOwnerCarriesItsIntelligence(t *testing.T) {
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	room := createRoom(alice, "ops")
	addToRoom(t, env, alice, room.ID, bob)
	patchProfile(alice, alice.User.ID, dto.UpdateProfileRequest{DisplayName: ptr("Alice A."), Handle: ptr("alice-a")}).RequireStatus(http.StatusOK)
	putAvatar(alice, alice.User.ID, pngBytes(t, bands(300, 300))).RequireStatus(http.StatusOK)
	pic := profileOf(t, alice, alice.User.ID).AvatarURL

	want := dto.IntelligenceRef{
		ID: alice.User.ID, Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "Alice A.", Handle: "alice-a", AvatarURL: pic,
	}
	got := testkit.Decode[dto.Room](bob.Get("/api/v1/rooms/" + room.ID).RequireStatus(http.StatusOK))
	require.Equal(t, []any{"alice", "Alice A."}, []any{got.Owner.Username, got.Owner.DisplayName})
	require.NotNil(t, got.Owner.Intelligence)
	require.Equal(t, want, *got.Owner.Intelligence)

	list := testkit.Decode[dto.List[dto.Room]](bob.Get("/api/v1/rooms").RequireStatus(http.StatusOK))
	require.Len(t, list.Items, 1)
	require.NotNil(t, list.Items[0].Owner.Intelligence)
	require.Equal(t, want, *list.Items[0].Owner.Intelligence)
}

// The author reaches the agents too: a message pushed to an agent carries it.
func TestPushedMessagesCarryTheirAuthor(t *testing.T) {
	f := newAgentRoom(t)
	patchProfile(f.alice, f.alice.User.ID, dto.UpdateProfileRequest{Handle: ptr("boss")}).RequireStatus(http.StatusOK)
	conn := f.dial(t)
	conn.Welcome(wsWait)
	sent := f.post(f.alice, "hello agent")
	got := conn.Message(wsWait)
	require.Equal(t, sent.ID, got.Message.ID)
	require.Equal(t, []any{f.alice.User.ID, dto.IntelligenceRoleUser, "boss"},
		[]any{got.Message.Author.ID, got.Message.Author.Role, got.Message.Author.Handle})
}
