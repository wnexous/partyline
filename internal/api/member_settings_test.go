package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// settingsRoom is a room of alice with her agent and a chat as members, and bob as a member of the
// room and of her organization; carol has no access.
type settingsRoom struct {
	env               *testkit.Env
	alice, bob, carol *testkit.Client
	room              dto.Room
	agent, chat       dto.RoomMember
}

func newSettingsRoom(t *testing.T) *settingsRoom {
	t.Helper()
	env := testkit.New(t)
	f := &settingsRoom{env: env, alice: env.NewUser("alice"), bob: env.NewUser("bob"), carol: env.NewUser("carol")}
	f.room = createRoom(f.alice, "ops")
	addToRoom(t, env, f.alice, f.room.ID, f.bob)
	agentID := storeAgent(t, env, f.alice.User.ID, "builder")
	_, chatID := storeChat(t, env, f.alice.User.ID, "family")
	f.agent = testkit.Decode[dto.RoomMember](f.alice.Post(f.members(), dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: agentID}).RequireStatus(http.StatusCreated))
	f.chat = f.alice.SeedChatMember(f.room.ID, chatID)
	return f
}

func (f *settingsRoom) members() string { return "/api/v1/rooms/" + f.room.ID + "/members" }

func (f *settingsRoom) listed(t *testing.T, id string) dto.RoomMember {
	t.Helper()
	for _, m := range testkit.Decode[dto.List[dto.RoomMember]](f.alice.Get(f.members()).RequireStatus(http.StatusOK)).Items {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("member %s not listed", id)
	return dto.RoomMember{}
}

// MEM-02..05 (settings): a new agent member receives everything and has no display name or
// keywords; a manager can set each field and the member list returns what was stored. Member
// signatures (MEM-01) are retired with the delivery to chats (CID-22).
func TestAgentMemberSettingsAreStored(t *testing.T) {
	f := newSettingsRoom(t)
	require.Equal(t, "", f.agent.DisplayName)
	require.Equal(t, dto.DeliveryModeAll, f.agent.Delivery)
	require.Equal(t, []string{}, f.agent.Keywords)

	path := f.members() + "/" + f.agent.ID
	got := testkit.Decode[dto.RoomMember](f.alice.Patch(path, dto.UpdateMemberRequest{DisplayName: ptr("Acme Bot")}).RequireStatus(http.StatusOK))
	require.Equal(t, "Acme Bot", got.DisplayName)
	require.Equal(t, dto.DeliveryModeAll, got.Delivery, "other fields unchanged")
	got = testkit.Decode[dto.RoomMember](f.alice.Patch(path, dto.UpdateMemberRequest{Delivery: ptr(dto.DeliveryModeMentions)}).RequireStatus(http.StatusOK))
	require.Equal(t, dto.DeliveryModeMentions, got.Delivery)
	require.Equal(t, "Acme Bot", got.DisplayName, "other fields unchanged")
	got = testkit.Decode[dto.RoomMember](f.alice.Patch(path, dto.UpdateMemberRequest{Keywords: &[]string{"deploy", "urgente"}}).RequireStatus(http.StatusOK))
	require.Equal(t, []string{"deploy", "urgente"}, got.Keywords)
	got = testkit.Decode[dto.RoomMember](f.alice.Patch(path, dto.UpdateMemberRequest{Delivery: ptr(dto.DeliveryModeTrusted)}).RequireStatus(http.StatusOK))
	require.Equal(t, dto.DeliveryModeTrusted, got.Delivery)

	listed := f.listed(t, f.agent.ID)
	require.Equal(t, "Acme Bot", listed.DisplayName)
	require.Equal(t, dto.DeliveryModeTrusted, listed.Delivery)
	require.Equal(t, []string{"deploy", "urgente"}, listed.Keywords)

	// An empty list clears the keywords; an empty display name clears it.
	got = testkit.Decode[dto.RoomMember](f.alice.Patch(path, dto.UpdateMemberRequest{Keywords: &[]string{}, DisplayName: ptr("")}).RequireStatus(http.StatusOK))
	require.Equal(t, []string{}, got.Keywords)
	require.Equal(t, "", got.DisplayName)
}

// MEM-01..05 (access): only managers change settings; a viewer gets 403, a user without
// access 404, and neither changes anything.
func TestAgentMemberSettingsNeedManage(t *testing.T) {
	f := newSettingsRoom(t)
	path := f.members() + "/" + f.agent.ID
	f.bob.Patch(path, dto.UpdateMemberRequest{Delivery: ptr(dto.DeliveryModeMentions)}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)
	f.carol.Patch(path, dto.UpdateMemberRequest{Delivery: ptr(dto.DeliveryModeMentions)}).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	require.Equal(t, dto.DeliveryModeAll, f.listed(t, f.agent.ID).Delivery)
}

// MEM-05: display names over 64 characters, more than 20 keywords, keywords over 64
// characters and unknown delivery modes get 422 naming the field; the limits count Unicode
// code points; a rejected request changes nothing.
func TestAgentMemberSettingsLimits(t *testing.T) {
	f := newSettingsRoom(t)
	path := f.members() + "/" + f.agent.ID
	invalid := func(req dto.UpdateMemberRequest, field string) {
		t.Helper()
		e := f.alice.Patch(path, req).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, field)
	}

	invalid(dto.UpdateMemberRequest{DisplayName: ptr(strings.Repeat("a", 65))}, "display_name")
	name := strings.Repeat("é", 64) // 128 bytes, 64 code points
	require.Equal(t, name, testkit.Decode[dto.RoomMember](f.alice.Patch(path, dto.UpdateMemberRequest{DisplayName: &name}).RequireStatus(http.StatusOK)).DisplayName)

	many := make([]string, 21)
	for i := range many {
		many[i] = "k" + strings.Repeat("x", i)
	}
	invalid(dto.UpdateMemberRequest{Keywords: &many}, "keywords")
	twenty := many[:20]
	require.Len(t, testkit.Decode[dto.RoomMember](f.alice.Patch(path, dto.UpdateMemberRequest{Keywords: &twenty}).RequireStatus(http.StatusOK)).Keywords, 20)

	invalid(dto.UpdateMemberRequest{Keywords: &[]string{strings.Repeat("k", 65)}}, "keywords")
	long := []string{strings.Repeat("ç", 64)}
	require.Equal(t, long, testkit.Decode[dto.RoomMember](f.alice.Patch(path, dto.UpdateMemberRequest{Keywords: &long}).RequireStatus(http.StatusOK)).Keywords)
	invalid(dto.UpdateMemberRequest{Keywords: &[]string{"ok", "  "}}, "keywords")

	invalid(dto.UpdateMemberRequest{Delivery: ptr(dto.DeliveryMode("loud"))}, "delivery")

	// A rejected request applies none of its fields.
	invalid(dto.UpdateMemberRequest{Delivery: ptr(dto.DeliveryModeMentions), DisplayName: ptr(strings.Repeat("a", 65))}, "display_name")
	m := f.listed(t, f.agent.ID)
	require.Equal(t, dto.DeliveryModeAll, m.Delivery)
	require.Equal(t, name, m.DisplayName)
	require.Equal(t, long, m.Keywords)
}

// Design: member settings apply to agent members only; the API refuses them for chats (422). The
// chat's outbound flag is retired: a request that sets it changes nothing (CID-22).
func TestMemberSettingsRefusedForChats(t *testing.T) {
	f := newSettingsRoom(t)
	path := f.members() + "/" + f.chat.ID
	for field, req := range map[string]dto.UpdateMemberRequest{
		"display_name": {DisplayName: ptr("x")},
		"delivery":     {Delivery: ptr(dto.DeliveryModeMentions)},
		"keywords":     {Keywords: &[]string{"x"}},
	} {
		e := f.alice.Patch(path, req).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, field)
	}
	got := testkit.Decode[dto.RoomMember](f.alice.Patch(path, map[string]any{"outbound": false}).RequireStatus(http.StatusOK))
	require.Equal(t, f.chat, got, "the chat member reads as it did")
}
