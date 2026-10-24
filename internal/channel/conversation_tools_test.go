package channel

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// cast is the chanEnv with the users around it in the tests of the conversation and profile
// tools: alice owns the AI "builder" (the session) and a second AI "helper"; bob owns the AI
// "sage" and shares it with nobody; carol is a user with no relation to alice.
type cast struct {
	*chanEnv
	bob, carol   *testkit.Client
	helper, sage string
}

func newCast(t *testing.T, opts ...testkit.Option) *cast {
	t.Helper()
	e := newChanEnv(t, opts...)
	bob, carol := e.env.NewUser("bob"), e.env.NewUser("carol")
	return &cast{chanEnv: e, bob: bob, carol: carol, helper: e.env.NewAI(e.alice, "helper"), sage: e.env.NewAI(bob, "sage")}
}

// share gives each user sight of the AI, through the organization it belongs to (AD-016).
func (c *cast) share(owner *testkit.Client, aiID string, users ...*testkit.Client) {
	c.t.Helper()
	owner.ShareAI(aiID, users...)
}

// person stores a person seen by a connector of alice, the way the inbound pipeline will, and
// returns its id and handle.
func (c *cast) person(name string) (id, handle string) {
	c.t.Helper()
	ctx := context.Background()
	conn, id := store.NewID(), store.NewID()
	require.NoError(c.t, c.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		orgID, err := orgs.DefaultFor(ctx, q, c.alice.User.ID) // the connection belongs to alice's organization, as an upgraded one does
		if err != nil {
			return err
		}
		if err := q.CreateConnector(ctx, db.CreateConnectorParams{
			ID: conn, OwnerID: c.alice.User.ID, OrgID: sql.NullString{String: orgID, Valid: true}, Kind: "telegram", Name: "tg", ConfigJson: "{}", Enabled: true,
		}); err != nil {
			return err
		}
		if _, err := intelligences.Create(ctx, q, intelligences.NewParams{ID: id, Kind: handles.Person, DisplayName: name, HandleBase: name, Now: time.Now()}); err != nil {
			return err
		}
		return q.CreatePerson(ctx, db.CreatePersonParams{ID: id, ConnectorID: conn, ExternalID: "5511999990000", PlatformName: name, CreatedAt: time.Now().UnixMilli()})
	}))
	return id, c.profile(c.alice, id).Handle
}

// profile reads an intelligence's profile as viewer sees it.
func (c *cast) profile(viewer *testkit.Client, id string) dto.IntelligenceProfile {
	c.t.Helper()
	return testkit.Decode[dto.IntelligenceProfile](viewer.Get("/api/v1/intelligences/" + id).RequireStatus(http.StatusOK))
}

// members lists a group's members as viewer sees them, by member id.
func (c *cast) members(viewer *testkit.Client, roomID string) map[string]dto.RoomMember {
	c.t.Helper()
	out := map[string]dto.RoomMember{}
	for _, m := range testkit.Decode[dto.List[dto.RoomMember]](viewer.Get("/api/v1/rooms/" + roomID + "/members").RequireStatus(http.StatusOK)).Items {
		out[m.MemberID] = m
	}
	return out
}

// listRooms runs the list_rooms tool.
func (c *cast) listRooms(s *session) []roomListing {
	c.t.Helper()
	text, isErr := s.call("list_rooms", map[string]any{})
	require.False(c.t, isErr, text)
	var out struct {
		Rooms []roomListing `json:"rooms"`
	}
	require.NoError(c.t, json.Unmarshal([]byte(text), &out))
	return out.Rooms
}

// conversations lists viewer's conversations other than the group "Support" that every test has.
func (c *cast) conversations(viewer *testkit.Client) []dto.Conversation {
	c.t.Helper()
	var out []dto.Conversation
	for _, cv := range conversationsOf(c.t, viewer) {
		if cv.ID != c.room.ID {
			out = append(out, cv)
		}
	}
	return out
}

func conversationsOf(t *testing.T, c *testkit.Client) []dto.Conversation {
	t.Helper()
	return testkit.Decode[dto.List[dto.Conversation]](c.Get("/api/v1/conversations").RequireStatus(http.StatusOK)).Items
}

func conversationIDs(list []dto.Conversation) []string {
	out := make([]string, len(list))
	for i, c := range list {
		out[i] = c.ID
	}
	return out
}

// decodeText decodes the JSON a tool answered.
func decodeText[T any](t *testing.T, text string) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal([]byte(text), &v), text)
	return v
}

func ptr[T any](v T) *T { return &v }

// --- the tools' schemas ---

// The schemas tell how to call the tools: send_message takes room, to, or connection with chat,
// create_group a name, members, a description and the organization to create the group in (ORG-03),
// update_profile the four profile fields; strings unless noted.
func TestConversationToolSchemas(t *testing.T) {
	e := newChanEnv(t)
	s := e.connected(e.options())
	s.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	want := map[string]map[string]string{
		"send_message":   {"room": "string", "to": "string", "connection": "string", "chat": "string", "text": "string"},
		"create_group":   {"name": "string", "description": "string", "members": "array", "organization": "string"},
		"update_profile": {"display_name": "string", "handle": "string", "bio": "string", "avatar_path": "string"},
	}
	seen := 0
	for _, item := range s.response()["result"].(map[string]any)["tools"].([]any) {
		tool := item.(map[string]any)
		name := tool["name"].(string)
		types, ok := want[name]
		if !ok {
			continue
		}
		seen++
		props := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
		require.Len(t, props, len(types), name)
		for field, typ := range types {
			prop, ok := props[field].(map[string]any)
			require.True(t, ok, "%s.%s", name, field)
			require.Equal(t, typ, prop["type"], "%s.%s", name, field)
			require.NotEmpty(t, prop["description"], "%s.%s", name, field)
		}
	}
	require.Equal(t, len(want), seen)
}

// ORG-03, POS-10: create_group takes the organization to create the group in, by its address and optional, and
// tells that it defaults to the AI's home organization; the refusal for a member of another organization is named.
// No tool is described as working for the AI's owner (the intelligences "your owner can see", a group "owned by
// your owner"): the AI acts as itself, in its organizations.
func TestToolDefinitionsFollowOrganizations(t *testing.T) {
	e := newChanEnv(t)
	s := e.connected(e.options())
	s.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	tools := map[string]map[string]any{}
	for _, item := range s.response()["result"].(map[string]any)["tools"].([]any) {
		tool := item.(map[string]any)
		tools[tool["name"].(string)] = tool
	}

	group := tools["create_group"]
	schema := group["inputSchema"].(map[string]any)
	org, ok := schema["properties"].(map[string]any)["organization"].(map[string]any)
	require.True(t, ok, "create_group lists organization")
	require.Equal(t, "string", org["type"])
	require.Equal(t, "optional: the address (slug) of the organization to create the group in, which you must be a member of and not a guest; your home organization when left out", org["description"])
	require.Equal(t, []any{"name", "members"}, schema["required"], "the organization is optional")
	require.Equal(t, "Create a group in an organization, with you and the given members. You are added automatically; everyone else is a member only when listed, and each must belong to the organization. "+
		"If any member cannot be added nothing is created and the error lists the refused ones with the reason (not_found: unknown, disabled or not one you can see; person: people from chats cannot be members; not_org_member: not a member of the organization).",
		group["description"])

	require.Contains(t, tools["send_message"]["description"], "for any user of this instance and any intelligence you share an organization with")

	for name, tool := range tools {
		raw, err := json.Marshal(tool)
		require.NoError(t, err)
		require.NotContains(t, strings.ToLower(string(raw)), "owner", name)
	}
}

// --- DM-04: send_message to ---

// DM-04: send_message with to opens the DM with the intelligence, even though they never talked,
// and posts in it. The owner finds it in the conversation list, unread, and the answer comes back
// into the AI's session as a DM. to is a "@handle", a handle in any case, or an id.
func TestToolSendMessageToOpensTheDM(t *testing.T) {
	c := newCast(t)
	s := c.connected(c.options())

	text, isErr := s.call("send_message", toolArgs("to", "@alice", "text", "the deploy finished"))
	require.False(t, isErr, text)

	list := c.conversations(c.alice)
	require.Len(t, list, 1, "the DM was created and sits in the owner's list")
	dm := list[0]
	require.Equal(t, []any{dto.ConversationKindDM, c.agentID, 1, "the deploy finished"},
		[]any{dm.Kind, dm.Counterpart.ID, dm.Unread, dm.LastMessage.Text})
	msgs := c.messages(dm.ID)
	require.Len(t, msgs, 1)
	require.Equal(t, []any{dto.AuthorKindAgent, c.agentID}, []any{msgs[0].AuthorKind, msgs[0].AuthorID})
	require.Equal(t, "sent (message_id "+msgs[0].ID+")", text)

	for _, to := range []string{"alice", "@ALICE", c.alice.User.ID} {
		text, isErr = s.call("send_message", toolArgs("to", to, "text", "again via "+to))
		require.False(t, isErr, "%s: %s", to, text)
	}
	require.Equal(t, []string{dm.ID}, conversationIDs(c.conversations(c.alice)), "every form of to reaches the one DM")
	require.Len(t, c.messages(dm.ID), 4)
	require.Empty(t, c.messages(c.room.ID), "nothing reached the group")

	dms := 0
	for _, r := range c.listRooms(s) {
		if r.Kind == dto.ConversationKindDM {
			dms++
			require.Equal(t, []any{dm.ID, "alice"}, []any{r.ID, r.With.Handle})
		}
	}
	require.Equal(t, 1, dms, "list_rooms lists the new DM")

	c.alice.Post("/api/v1/rooms/"+dm.ID+"/messages", dto.PostMessageRequest{Text: "thanks builder"}).RequireStatus(http.StatusCreated)
	n := s.notification()
	require.Equal(t, []string{"thanks builder", "dm", dm.ID}, []string{n.Content, n.Meta["conversation"], n.Meta["room"]})
}

// DM-04: exactly one of room and to names where the message goes. Both, or neither, is a tool
// error that posts and creates nothing; an empty or blank value counts as not given, as models
// that send every parameter do.
func TestToolSendMessageNeedsExactlyOneOfRoomAndTo(t *testing.T) {
	c := newCast(t)
	s := c.connected(c.options())

	both, isErr := s.call("send_message", toolArgs("room", c.room.ID, "to", "@alice", "text", "both"))
	require.True(t, isErr, both)
	neither, isErr := s.call("send_message", toolArgs("text", "neither"))
	require.True(t, isErr, neither)
	for _, text := range []string{both, neither} {
		require.Contains(t, text, "room")
		require.Contains(t, text, "to")
	}
	noText, isErr := s.call("send_message", toolArgs("to", "@alice"))
	require.True(t, isErr, noText)
	require.Contains(t, noText, "text is required")
	require.Empty(t, c.messages(c.room.ID))
	require.Empty(t, c.conversations(c.alice), "nothing was created")

	for name, args := range map[string]map[string]any{
		"empty room": toolArgs("room", "", "to", "@alice", "text", "empty room"),
		"blank room": toolArgs("room", "  ", "to", "@alice", "text", "blank room"),
	} {
		text, isErr := s.call("send_message", args)
		require.False(t, isErr, "%s: %s", name, text)
	}
	require.Len(t, c.conversations(c.alice), 1)
	for name, to := range map[string]string{"empty to": "", "blank to": "  "} {
		text, isErr := s.call("send_message", toolArgs("room", "Support", "to", to, "text", name))
		require.False(t, isErr, "%s: %s", name, text)
		require.Equal(t, name, c.messages(c.room.ID)[0].Text, "%s: the message went to the room", name)
	}
}

// DM-04 (AD-016): to reaches the intelligences the AI itself can see - the users of the instance and the AIs it
// shares an organization with - and nothing else; an unknown target and an invisible one answer alike
// (contact_not_found). A person without a route and the AI itself are refused with their codes, and nothing is
// created. Once sage's organization takes the AI in the same call works; the DM is visible to the managers of
// both AIs and to nobody else.
func TestToolSendMessageToRefusalsFollowWhatTheAISees(t *testing.T) {
	c := newCast(t)
	_, personHandle := c.person("Contact")
	s := c.connected(c.options())
	refused := func(to string) string {
		t.Helper()
		text, isErr := s.call("send_message", toolArgs("to", to, "text", "psst"))
		require.True(t, isErr, "%s: %s", to, text)
		return text
	}

	ghost := refused("@ghost")
	require.Contains(t, ghost, "contact_not_found")
	require.Equal(t, ghost, refused("@sage"), "an AI that shares no organization with it answers like a handle that does not exist")
	require.Equal(t, ghost, refused(c.sage))
	require.Contains(t, refused("@builder"), "dm_self")
	require.Contains(t, refused(c.agentID), "dm_self")
	require.Contains(t, refused("@"+personHandle), "no_route_to_person")
	require.Empty(t, c.listDMs(s), "a refused call creates no DM")

	c.env.AddMember(c.bob.PersonalOrg().ID, c.alice.User.ID, orgs.PermissionMember, "")
	refused("@sage") // alice, its owner, in sage's organization gives the AI no reach
	c.env.AddMember(c.bob.PersonalOrg().ID, c.agentID, orgs.PermissionMember, "")
	text, isErr := s.call("send_message", toolArgs("to", "@sage", "text", "now visible"))
	require.False(t, isErr, text)
	dms := c.listDMs(s)
	require.Len(t, dms, 1)
	require.Equal(t, "sage", dms[0].With.Handle)
	for who, want := range map[*testkit.Client]int{c.alice: http.StatusOK, c.bob: http.StatusOK, c.carol: http.StatusNotFound} {
		require.Equal(t, want, who.Get("/api/v1/rooms/"+dms[0].ID).Status, who.User.Username)
	}

	// Any user of the instance can be reached.
	text, isErr = s.call("send_message", toolArgs("to", "@carol", "text", "hello carol"))
	require.False(t, isErr, text)
	require.Len(t, c.conversations(c.carol), 1)
}

// listDMs lists the DMs of the AI behind the session.
func (c *cast) listDMs(s *session) []roomListing {
	c.t.Helper()
	var out []roomListing
	for _, r := range c.listRooms(s) {
		if r.Kind == dto.ConversationKindDM {
			out = append(out, r)
		}
	}
	return out
}

// DM-02 (edge case) through the tool: the DM with a disabled user cannot take a post
// (counterpart_disabled), and the call leaves no empty DM behind.
func TestToolSendMessageToADisabledUser(t *testing.T) {
	c := newCast(t)
	admin := c.env.NewAdmin("root")
	disabled := true
	admin.Patch("/api/v1/admin/users/"+c.carol.User.ID, dto.UpdateUserRequest{Disabled: &disabled}).RequireStatus(http.StatusOK)
	s := c.connected(c.options())

	text, isErr := s.call("send_message", toolArgs("to", "@carol", "text", "are you there"))
	require.True(t, isErr, text)
	require.Contains(t, text, "counterpart_disabled")
	require.Empty(t, c.listDMs(s), "no empty DM was left")
}

// DM-04, CID-22: chat_ids (and recipients, its older name) are accepted and ignored with to as with
// room: the DM is opened and the message is sent, whatever chats they name.
func TestToolSendMessageToIgnoresChatIDs(t *testing.T) {
	c := newCast(t)
	s := c.connected(c.options())

	for _, param := range []string{"chat_ids", "recipients"} {
		text, isErr := s.call("send_message", toolArgs("to", "@alice", "text", "to "+param, param, []string{"no-such-chat"}))
		require.False(t, isErr, "%s: %s", param, text)
	}
	require.Len(t, c.listDMs(s), 1, "the DM was opened once and used for both")
}

// TOOL-08 with DM-04: posts to a DM count against the same 30 a minute as posts to a room, and
// the limit keeps the server's own words (the code is not repeated before them).
func TestToolPostLimitCoversDMsAndKeepsItsWords(t *testing.T) {
	c := newCast(t)
	s := c.connected(c.options())
	for i := 1; i <= 30; i++ {
		text, isErr := s.call("send_message", toolArgs("room", c.room.ID, "text", fmt.Sprintf("post %d", i)))
		require.False(t, isErr, "post %d: %s", i, text)
	}

	text, isErr := s.call("send_message", toolArgs("to", "@alice", "text", "one too many"))
	require.True(t, isErr)
	require.Equal(t, "rate limited: at most 30 messages per minute", text)
	require.Empty(t, c.listDMs(s))
}

// DM-04, AGRP-01: an AI creates at most 10 new conversations an hour, DMs and groups together; the
// eleventh is refused with rate_limited and creates nothing, while a DM that exists is reopened
// for free.
func TestToolNewConversationsAreLimited(t *testing.T) {
	c := newCast(t)
	peers := make([]string, 11)
	for i := range peers {
		peers[i] = fmt.Sprintf("peer%02d", i)
		c.env.NewAI(c.alice, peers[i])
	}
	s := c.connected(c.options())
	for _, p := range peers[:10] {
		text, isErr := s.call("send_message", toolArgs("to", "@"+p, "text", "hi"))
		require.False(t, isErr, "%s: %s", p, text)
	}

	text, isErr := s.call("send_message", toolArgs("to", "@"+peers[10], "text", "one too many"))
	require.True(t, isErr, text)
	require.True(t, strings.HasPrefix(text, "rate_limited: "), text)
	text, isErr = s.call("create_group", toolArgs("name", "Eleven", "members", []string{"@alice"}))
	require.True(t, isErr, text)
	require.True(t, strings.HasPrefix(text, "rate_limited: "), text)
	require.Empty(t, c.conversations(c.alice), "the refused calls created nothing")

	text, isErr = s.call("send_message", toolArgs("to", "@"+peers[0], "text", "again"))
	require.False(t, isErr, "reopening a DM costs nothing: %s", text)
	require.Len(t, c.listDMs(s), 10)
}

// --- AGRP-01, AGRP-02: create_group ---

// AGRP-01: create_group makes a group of the AI's owner with the AI and the members it names, by
// @handle, handle or id, and returns its room id and name. The owner sees it with manage level in
// the conversation list and the AI posts in it.
func TestToolCreateGroup(t *testing.T) {
	c := newCast(t)
	s := c.connected(c.options())

	text, isErr := s.call("create_group", toolArgs("name", "Ops", "description", "on call", "members", []string{"@alice", c.helper}))
	require.False(t, isErr, text)
	group := decodeText[dto.CreatedGroup](t, text)
	require.Equal(t, "Ops", group.Name)
	require.NotEmpty(t, group.Room)

	room := testkit.Decode[dto.Room](c.alice.Get("/api/v1/rooms/" + group.Room).RequireStatus(http.StatusOK))
	require.Equal(t, []any{dto.ConversationKindGroup, "Ops", "on call", c.alice.User.ID, dto.LevelManage, 3},
		[]any{room.Kind, room.Name, room.Description, room.Owner.ID, room.MyLevel, room.MemberCount})
	members := c.members(c.alice, group.Room)
	require.Equal(t, dto.MemberKindAgent, members[c.agentID].Kind, "the creating AI is a member")
	require.Equal(t, dto.MemberKindAgent, members[c.helper].Kind)
	require.Equal(t, dto.MemberKindUser, members[c.alice.User.ID].Kind)
	require.Contains(t, conversationIDs(conversationsOf(t, c.alice)), group.Room)

	// The owner is a member only when listed: a group of the AI and its helper has no user member.
	text, isErr = s.call("create_group", toolArgs("name", "Bots", "members", []string{"@helper"}))
	require.False(t, isErr, text)
	bots := decodeText[dto.CreatedGroup](t, text)
	botMembers := c.members(c.alice, bots.Room)
	require.Len(t, botMembers, 2)
	require.NotContains(t, botMembers, c.alice.User.ID)
	require.Equal(t, dto.LevelManage, testkit.Decode[dto.Room](c.alice.Get("/api/v1/rooms/"+bots.Room).RequireStatus(http.StatusOK)).MyLevel, "the owner still manages it")

	text, isErr = s.call("send_message", toolArgs("room", "Ops", "text", "the group is ready"))
	require.False(t, isErr, text)
	require.Equal(t, []any{"the group is ready", c.agentID}, []any{c.messages(group.Room)[0].Text, c.messages(group.Room)[0].AuthorID})
	var listed bool
	for _, r := range c.listRooms(s) {
		if r.ID == group.Room {
			listed = true
			require.Equal(t, []any{dto.ConversationKindGroup, "Ops"}, []any{r.Kind, r.Name})
		}
	}
	require.True(t, listed, "list_rooms lists the new group")
}

// AGRP-02: a member the AI cannot add makes the call fail with members_refused, naming every
// refused member as the AI wrote it with its reason, and nothing is created. Members it may
// add are not named. Once sage joins the AI's organization the same call works. The rules are the
// AI's own: a person of alice's connector is refused as a person, and bob's AI sage is refused as
// unknown until it shares an organization with the AI.
func TestToolCreateGroupRefusesAndCreatesNothing(t *testing.T) {
	c := newCast(t)
	_, personHandle := c.person("Contact")
	s := c.connected(c.options())
	rooms := len(c.listRooms(s))

	members := []string{"@alice", "@sage", "@" + personHandle, "@ghost", c.helper}
	text, isErr := s.call("create_group", toolArgs("name", "Ops", "members", members))
	require.True(t, isErr, text)
	require.Equal(t, "members_refused: some members cannot be added to the group; nothing was created (refused: @sage (not_found), @"+personHandle+" (person), @ghost (not_found))", text,
		"every refused member as the AI wrote it, in order, with its reason; the others are not named")
	require.Empty(t, c.conversations(c.alice), "nothing was created")
	require.Len(t, c.listRooms(s), rooms)

	c.env.AddMember(c.alice.PersonalOrg().ID, c.sage, orgs.PermissionMember, "") // sage joins the organization of builder and helper
	text, isErr = s.call("create_group", toolArgs("name", "Ops", "members", []string{"@alice", "@sage", c.helper}))
	require.False(t, isErr, text)
	group := decodeText[dto.CreatedGroup](t, text)
	require.Equal(t, 4, testkit.Decode[dto.Room](c.alice.Get("/api/v1/rooms/"+group.Room).RequireStatus(http.StatusOK)).MemberCount)
}

// create_group needs a name and members; what the server rejects (a blank entry, more than 50
// members) comes back as the tool error with its fields. Nothing is created.
func TestToolCreateGroupArguments(t *testing.T) {
	c := newCast(t)
	s := c.connected(c.options())

	for name, args := range map[string]map[string]any{
		"no name":       toolArgs("members", []string{"@alice"}),
		"blank name":    toolArgs("name", "  ", "members", []string{"@alice"}),
		"no members":    toolArgs("name", "Ops"),
		"empty members": toolArgs("name", "Ops", "members", []string{}),
	} {
		text, isErr := s.call("create_group", args)
		require.True(t, isErr, "%s: %s", name, text)
	}
	text, isErr := s.call("create_group", toolArgs("name", "Ops", "members", []string{"@alice", " "}))
	require.True(t, isErr, text)
	require.Contains(t, text, "members")
	many := make([]string, dto.MaxGroupMembers+1)
	for i := range many {
		many[i] = fmt.Sprintf("member-%02d", i)
	}
	text, isErr = s.call("create_group", toolArgs("name", "Ops", "members", many))
	require.True(t, isErr, text)
	require.Contains(t, text, "members")
	require.NotContains(t, text, "members_refused", "the size is checked before the members")
	require.Empty(t, c.conversations(c.alice), "nothing was created")
}

// --- PROF-04: update_profile ---

// profileAnswer is what update_profile answers: the profile of the AI after the change.
type profileAnswer struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Handle      string `json:"handle"`
	Bio         string `json:"bio"`
	AvatarURL   string `json:"avatar_url"`
}

// PROF-04, PROF-03: update_profile changes the AI's own display name, handle and bio and returns
// the updated profile, which its owner reads the same and which the room's member list shows.
// Fields that are left out stay as they are, an empty bio clears it, and what the server refuses
// (a handle another intelligence has, a display name over 64 characters) is the tool error with
// nothing changed. The other AIs of the owner and the owner's own profile are untouched.
func TestToolUpdateProfile(t *testing.T) {
	c := newCast(t)
	s := c.connected(c.options())

	text, isErr := s.call("update_profile", toolArgs("display_name", "Atlas", "handle", "atlas", "bio", "Runs the builds"))
	require.False(t, isErr, text)
	got := decodeText[profileAnswer](t, text)
	require.Equal(t, []any{c.agentID, "Atlas", "atlas", "Runs the builds"}, []any{got.ID, got.DisplayName, got.Handle, got.Bio})
	owner := c.profile(c.alice, c.agentID)
	require.Equal(t, []any{"Atlas", "atlas", "Runs the builds"}, []any{owner.DisplayName, owner.Handle, owner.Bio})
	require.Equal(t, "atlas", c.members(c.alice, c.room.ID)[c.agentID].Intelligence.Handle, "the member list follows")
	require.Equal(t, []any{"helper", ""}, []any{c.profile(c.alice, c.helper).DisplayName, c.profile(c.alice, c.helper).Bio}, "the other AI is untouched")
	require.Equal(t, "alice", c.profile(c.alice, c.alice.User.ID).Handle, "so is the owner")

	text, isErr = s.call("update_profile", toolArgs("bio", "Builds and ships"))
	require.False(t, isErr, text)
	got = decodeText[profileAnswer](t, text)
	require.Equal(t, []any{"Atlas", "atlas", "Builds and ships"}, []any{got.DisplayName, got.Handle, got.Bio}, "what is left out stays")
	text, isErr = s.call("update_profile", toolArgs("bio", ""))
	require.False(t, isErr, text)
	got = decodeText[profileAnswer](t, text)
	require.Equal(t, []any{"Atlas", ""}, []any{got.DisplayName, got.Bio}, "an empty bio clears it")

	for name, args := range map[string]map[string]any{
		"handle taken":      toolArgs("display_name", "Other", "handle", "helper"),
		"display name long": toolArgs("display_name", strings.Repeat("x", 65)),
	} {
		text, isErr = s.call("update_profile", args)
		require.True(t, isErr, "%s: %s", name, text)
		if name == "handle taken" {
			require.Contains(t, text, "handle_taken")
		} else {
			require.Contains(t, text, "display_name")
		}
		require.Equal(t, []any{"Atlas", "atlas"}, []any{c.profile(c.alice, c.agentID).DisplayName, c.profile(c.alice, c.agentID).Handle}, "%s: nothing changed", name)
	}

	for name, args := range map[string]map[string]any{
		"nothing": {}, "empty avatar_path": toolArgs("avatar_path", ""), "blank avatar_path": toolArgs("avatar_path", "  "),
	} {
		text, isErr = s.call("update_profile", args)
		require.True(t, isErr, "%s: %s", name, text)
		require.Contains(t, text, "display_name", "%s: the error names the fields to give", name)
	}
}

// PROF-04, PROF-07: update_profile sets the avatar from a local PNG, JPEG or WebP file, together
// with other fields or alone; the server stores it as a 256x256 PNG cropped to the centre, serves
// it to the owner, and the answer carries the new avatar_url.
func TestToolUpdateProfileSetsTheAvatarFromAFile(t *testing.T) {
	c := newCast(t)
	s := c.connected(c.options())
	dir := t.TempDir()

	path := writeFile(t, dir, "atlas.png", encodePNG(t, bands(400, 200)))
	text, isErr := s.call("update_profile", toolArgs("avatar_path", path))
	require.False(t, isErr, text)
	first := decodeText[profileAnswer](t, text)
	require.Regexp(t, avatarURLPattern, first.AvatarURL)
	require.Equal(t, "builder", first.Handle, "the other fields are as they were")
	requireGreenSquare(t, c.alice.Get("/api/v1/intelligences/"+c.agentID+"/avatar").RequireStatus(http.StatusOK).Body)
	require.Equal(t, first.AvatarURL, c.profile(c.alice, c.agentID).AvatarURL, "the owner reads the same avatar")

	webp, err := os.ReadFile("../avatars/testdata/tiny.webp")
	require.NoError(t, err)
	// A fixed order: the WebP fixture is solid green, the same picture as the first PNG's middle
	// square, so it must follow the three-band JPEG to be a new avatar.
	for _, f := range []struct {
		name string
		data []byte
	}{{"jpeg", encodeJPEG(t, bands(300, 300))}, {"webp", webp}} {
		name, data := f.name, f.data
		text, isErr = s.call("update_profile", toolArgs("display_name", "Atlas "+name, "avatar_path", writeFile(t, dir, "avatar."+name, data)))
		require.False(t, isErr, "%s: %s", name, text)
		got := decodeText[profileAnswer](t, text)
		require.Regexp(t, avatarURLPattern, got.AvatarURL, name)
		require.NotEqual(t, first.AvatarURL, got.AvatarURL, "%s: a new avatar", name)
		require.Equal(t, "Atlas "+name, got.DisplayName, "%s: name and avatar in one call", name)
		img, err := png.Decode(bytes.NewReader(c.alice.Get("/api/v1/intelligences/" + c.agentID + "/avatar").RequireStatus(http.StatusOK).Body))
		require.NoError(t, err, name)
		require.Equal(t, image.Rect(0, 0, 256, 256), img.Bounds(), name)
		first = got
	}
}

// PROF-04, PROF-06: a file that is no PNG, JPEG or WebP, or cannot be decoded, is the tool error
// avatar_invalid and the old avatar stays; so does a file over 2 MiB, which the tool refuses
// itself with the limit in its words, while one of exactly 2 MiB goes through. A file that is
// missing or is a directory is an error naming the path. Nothing is changed when the file
// cannot be read or is too big, other fields included; when the server refuses the image after
// the other fields were applied, the error says what was applied.
func TestToolUpdateProfileRefusesAvatarFiles(t *testing.T) {
	c := newCast(t)
	s := c.connected(c.options())
	dir := t.TempDir()
	valid := encodePNG(t, bands(64, 64))
	avatarURL := func() string { return c.profile(c.alice, c.agentID).AvatarURL }

	text, isErr := s.call("update_profile", toolArgs("avatar_path", writeFile(t, dir, "good.png", valid)))
	require.False(t, isErr, text)
	kept := avatarURL()
	require.NotEmpty(t, kept)

	for name, data := range map[string][]byte{
		"text":          []byte("not an image"),
		"gif":           encodeGIF(t, bands(64, 64)),
		"empty":         {},
		"truncated png": valid[:30],
	} {
		text, isErr = s.call("update_profile", toolArgs("avatar_path", writeFile(t, dir, name, data)))
		require.True(t, isErr, "%s: %s", name, text)
		require.Contains(t, text, "avatar_invalid", name)
		require.Equal(t, kept, avatarURL(), "%s: the old avatar stays", name)
	}

	// The limit: 2 MiB passes, one byte more is refused by the tool, before anything is sent.
	const limit = 2 << 20
	text, isErr = s.call("update_profile", toolArgs("avatar_path", writeFile(t, dir, "exact.png", padded(t, valid, limit))))
	require.False(t, isErr, text)
	kept = avatarURL()
	text, isErr = s.call("update_profile", toolArgs("display_name", "Too Big", "avatar_path", writeFile(t, dir, "over.png", padded(t, valid, limit+1))))
	require.True(t, isErr, text)
	require.Contains(t, text, "avatar_invalid")
	require.Contains(t, text, "2 MiB")
	require.Equal(t, kept, avatarURL())
	require.Equal(t, "Builder", c.profile(c.alice, c.agentID).DisplayName, "a file the tool refuses changes nothing")

	for name, path := range map[string]string{"missing": filepath.Join(dir, "missing.png"), "directory": dir} {
		text, isErr = s.call("update_profile", toolArgs("display_name", "Unread", "avatar_path", path))
		require.True(t, isErr, "%s: %s", name, text)
		require.Contains(t, text, path, name)
		require.Equal(t, "Builder", c.profile(c.alice, c.agentID).DisplayName, "%s: nothing changed", name)
	}

	text, isErr = s.call("update_profile", toolArgs("display_name", "Half Done", "avatar_path", writeFile(t, dir, "bad.txt", []byte("not an image"))))
	require.True(t, isErr, text)
	require.Contains(t, text, "avatar_invalid")
	require.Contains(t, text, "display_name", "the error says what was applied")
	require.Equal(t, "Half Done", c.profile(c.alice, c.agentID).DisplayName)
	require.Equal(t, kept, avatarURL())
}

// PROF-04 with authorization: the tool edits the profile of the AI that calls and no other, an id
// in its arguments names nothing; the owner and a user the AI is shared with read the new profile,
// the grantee without the right to edit it, and a stranger does not see the AI at all.
func TestToolUpdateProfileOnlyChangesTheCallingAI(t *testing.T) {
	c := newCast(t)
	c.share(c.alice, c.agentID, c.bob)
	s := c.connected(c.options())

	text, isErr := s.call("update_profile", toolArgs("id", c.helper, "display_name", "Renamed"))
	require.False(t, isErr, text)
	require.Equal(t, c.agentID, decodeText[profileAnswer](t, text).ID)
	require.Equal(t, "helper", c.profile(c.alice, c.helper).DisplayName, "the other AI of the owner is untouched")

	owner, grantee := c.profile(c.alice, c.agentID), c.profile(c.bob, c.agentID)
	require.Equal(t, []any{"Renamed", true}, []any{owner.DisplayName, owner.CanEdit})
	require.Equal(t, []any{"Renamed", false}, []any{grantee.DisplayName, grantee.CanEdit})
	c.carol.Get("/api/v1/intelligences/"+c.agentID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
}

// AID-04 with the new tools: the tools-only server (Codex, Gemini, the tools half of pi and
// OpenCode) lists them and runs them as the AI of its folder without a live session of its own.
func TestToolsOnlyServerRunsTheConversationTools(t *testing.T) {
	c := newCast(t)
	o := c.options()
	o.ToolsOnly = true
	s := startSession(t, o)
	s.handshake()

	s.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	listed := map[string]bool{}
	for _, item := range s.response()["result"].(map[string]any)["tools"].([]any) {
		listed[item.(map[string]any)["name"].(string)] = true
	}
	for _, name := range []string{"send_message", "create_group", "update_profile"} {
		require.True(t, listed[name], name)
	}

	text, isErr := s.call("send_message", toolArgs("to", "@alice", "text", "from a session without push"))
	require.False(t, isErr, text)
	dms := c.conversations(c.alice)
	require.Len(t, dms, 1)
	require.Equal(t, []any{dto.ConversationKindDM, c.agentID}, []any{dms[0].Kind, dms[0].Counterpart.ID})
	text, isErr = s.call("create_group", toolArgs("name", "Ops", "members", []string{"@alice"}))
	require.False(t, isErr, text)
	require.Equal(t, "Ops", decodeText[dto.CreatedGroup](t, text).Name)
	text, isErr = s.call("update_profile", toolArgs("bio", "Reached through tools only"))
	require.False(t, isErr, text)
	require.Equal(t, []any{c.agentID, "Reached through tools only"}, []any{decodeText[profileAnswer](t, text).ID, c.profile(c.alice, c.agentID).Bio})

	require.False(t, c.online(), "a tools-only server holds no live session")
	s.expectNoIdentityNotice(100 * time.Millisecond)
}

// --- the tools report the state of the link ---

// The three tools do not reach the server while the token is rejected or another session took the
// AI over: they answer what the other tools answer, and create and change nothing.
func TestConversationToolsAreGuardedByTheLinkState(t *testing.T) {
	calls := []struct {
		tool string
		args map[string]any
	}{
		{"send_message", toolArgs("to", "@alice", "text", "hello")},
		{"create_group", toolArgs("name", "Ops", "members", []string{"@alice"})},
		{"update_profile", toolArgs("display_name", "Changed")},
	}
	untouched := func(c *cast) {
		t.Helper()
		require.Empty(t, c.conversations(c.alice), "nothing was created")
		require.Equal(t, "Builder", c.profile(c.alice, c.agentID).DisplayName, "nothing was changed")
	}

	t.Run("invalid token", func(t *testing.T) {
		c := newCast(t)
		o := c.options()
		o.Token = "pl_agent_invalid"
		s := startSession(t, o)
		s.handshake()
		require.Eventually(t, func() bool { return s.stderr.String() != "" }, wait, 10*time.Millisecond)
		for _, call := range calls {
			text, isErr := s.call(call.tool, call.args)
			require.True(t, isErr, call.tool)
			require.Equal(t, "partyline: not connected (invalid token)", text, call.tool)
		}
		untouched(c)
	})

	t.Run("taken over", func(t *testing.T) {
		c := newCast(t)
		s := c.connected(c.options())
		c.takeOver()
		require.Eventually(t, func() bool {
			text, isErr := s.call("list_rooms", map[string]any{})
			return isErr && text == wantTakenOver
		}, wait, 20*time.Millisecond)
		for _, call := range calls {
			text, isErr := s.call(call.tool, call.args)
			require.True(t, isErr, call.tool)
			require.Equal(t, wantTakenOver, text, call.tool)
		}
		untouched(c)
	})
}

// --- the instructions ---

// The MCP instructions of every variant tell the session about DMs, to, groups and profiles; the
// channel's, which gets the connect notice, also says what that notice tells.
func TestInstructionsDescribeDMsGroupsAndProfiles(t *testing.T) {
	e := newChanEnv(t)
	tools := e.options()
	tools.ToolsOnly = true
	omp := e.options()
	omp.Harness = HarnessOmp
	for name, text := range map[string]string{"channel": instructionsOf(t, e.options()), "tools-only": instructionsOf(t, tools), "omp": instructionsOf(t, omp)} {
		for _, want := range []string{
			"send_message and to instead of room", `"@handle", handle or id`, "direct", "create_group", "nothing is created", "update_profile", "avatar_path", "2 MiB",
		} {
			require.Contains(t, text, want, name)
		}
	}
	require.Contains(t, instructionsOf(t, e.options()), identityNoticeSentence)
}

// --- the notice that says who the AI is ---

// The first welcome of the full channel becomes one notice in the words of the design: "You are
// <display name> (@handle), an AI of <organization> (<address>)." and who manages it (POS-10; the
// organization sentences of the older notice, "Your owner is <display name> (@handle).", are for a server
// that names no organization). It is a system notice (type notice, never trusted), built from the profiles of
// the AI and of its managers, not from the folder or the username; a reconnect after a dropped connection is
// the same session and says nothing.
func TestChannelTellsTheSessionWhoItIsOnce(t *testing.T) {
	e := newChanEnv(t)
	e.alice.Patch("/api/v1/intelligences/"+e.alice.User.ID, dto.UpdateProfileRequest{DisplayName: ptr("Alice Prado"), Handle: ptr("alice-p")}).RequireStatus(http.StatusOK)
	e.alice.Patch("/api/v1/intelligences/"+e.agentID, dto.UpdateProfileRequest{DisplayName: ptr("Atlas"), Handle: ptr("atlas")}).RequireStatus(http.StatusOK)
	p := newProxy(t, strings.TrimPrefix(e.env.URL, "http://"))
	o := e.options()
	o.URL = p.url()
	s := startSession(t, o)
	s.handshake()

	n := s.identityNotice()
	require.Equal(t, `You are Atlas (@atlas), an AI of alice (alice). Messages marked trusted="true" come from the identities that manage you: Alice Prado (@alice-p).`, n.Content,
		"alice created the AI in her own organization, where she is the Admin: she is the one who manages it")
	require.Equal(t, map[string]string{
		"type": "notice", "reason": "identity", "author": "Partyline", "author_kind": "system",
		"sender_name": "", "trusted": "false", "kind": "system",
	}, n.Meta)

	require.Eventually(t, e.online, wait, 10*time.Millisecond)
	p.drop() // the connection drops and the channel connects again
	m := e.post("after the drop")
	require.Equal(t, m.ID, s.notification().Meta["message_id"], "the message arrives over the new connection")
	s.expectNoIdentityNotice(200 * time.Millisecond)
}

// welcomeGateway is an agent WebSocket that sends the given welcome and nothing else.
func welcomeGateway(t *testing.T, welcome dto.AgentWelcomeFrame) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		data, _ := json.Marshal(welcome)
		if ws.Write(r.Context(), websocket.MessageText, data) != nil {
			return
		}
		for {
			if _, _, err := ws.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// The notice says what the welcome says: a server that names no AI (an older one) gets no notice,
// and one that names the AI but not its owner gets the first sentence alone.
func TestChannelIdentityNoticeFollowsTheWelcome(t *testing.T) {
	ai := dto.IntelligenceRef{ID: "ai-1", Kind: dto.IntelligenceKindAI, Role: dto.IntelligenceRoleAI, DisplayName: "Builder", Handle: "builder"}
	owner := dto.IntelligenceRef{ID: "user-1", Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: "alice", Handle: "alice"}
	welcome := func(ai, owner dto.IntelligenceRef) dto.AgentWelcomeFrame {
		return dto.AgentWelcomeFrame{Type: dto.AgentFrameTypeWelcome, Agent: dto.AgentInfo{ID: "ai-1", Name: "builder", Owner: "alice"}, Intelligence: ai, Owner: owner}
	}
	options := func(url string) Options {
		return Options{URL: url, Token: "pl_agent_test", Machine: "testbox", Folder: "/work/builder", TempDir: t.TempDir(), timing: fastTiming}
	}

	s := startSession(t, options(welcomeGateway(t, welcome(ai, owner))))
	s.handshake()
	require.Equal(t, "You are Builder (@builder). Your owner is alice (@alice).", s.identityNotice().Content)

	s = startSession(t, options(welcomeGateway(t, welcome(ai, dto.IntelligenceRef{}))))
	s.handshake()
	require.Equal(t, "You are Builder (@builder).", s.identityNotice().Content)

	s = startSession(t, options(welcomeGateway(t, welcome(dto.IntelligenceRef{}, dto.IntelligenceRef{}))))
	s.handshake()
	s.send(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "ping"})
	require.Equal(t, float64(9), s.response()["id"])
	s.expectNoIdentityNotice(300 * time.Millisecond)
	s.expectNoNotification(10 * time.Millisecond)
}

// `partyline listen` (pi, OpenCode) prints message lines and the busy and taken-over notices
// only: the welcome that makes the full channel say who the AI is adds no line, so the adapters
// read the same output as before.
func TestListenDoesNotAnnounceTheIdentity(t *testing.T) {
	e := newChanEnv(t)
	r := startListen(t, e, "pi", fastTiming)
	require.Eventually(t, e.online, wait, 10*time.Millisecond)
	m := e.post("the first line")
	require.Eventually(t, func() bool { return len(r.lines()) >= 1 }, wait, 10*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	lines := r.lines()
	require.Len(t, lines, 1, "the message is the only line")
	require.Equal(t, m.ID, lines[0]["message_id"])
	require.NotContains(t, lines[0], "reason")
}

// --- fixtures ---

var avatarURLPattern = regexp.MustCompile(`^/api/v1/intelligences/([0-9a-f-]+)/avatar\?v=([0-9a-f]{64})$`)

// bands is a w x h image: the outer quarters red (left) and blue (right), the middle half green,
// so the centre square of a wide image is green.
func bands(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			switch {
			case x < w/4:
				img.Set(x, y, color.RGBA{R: 220, G: 20, B: 20, A: 255})
			case x < 3*w/4:
				img.Set(x, y, color.RGBA{R: 20, G: 200, B: 20, A: 255})
			default:
				img.Set(x, y, color.RGBA{R: 20, G: 20, B: 220, A: 255})
			}
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, nil))
	return buf.Bytes()
}

func encodeGIF(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, gif.Encode(&buf, img, nil))
	return buf.Bytes()
}

// padded is data followed by zero bytes up to size bytes: a PNG decoder stops at its last chunk,
// so a padded PNG is still a valid image of exactly that size.
func padded(t *testing.T, data []byte, size int) []byte {
	t.Helper()
	require.LessOrEqual(t, len(data), size)
	return append(append(make([]byte, 0, size), data...), make([]byte, size-len(data))...)
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

// requireGreenSquare asserts a served avatar: a 256x256 PNG whose pixels are the green centre of
// bands.
func requireGreenSquare(t *testing.T, body []byte) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 256, 256), img.Bounds())
	for _, p := range []image.Point{{4, 4}, {251, 4}, {4, 251}, {251, 251}, {128, 128}} {
		r, g, b, _ := img.At(p.X, p.Y).RGBA()
		require.Greater(t, g>>8, uint32(150), "green at %v", p)
		require.Less(t, r>>8, uint32(60), "no red at %v", p)
		require.Less(t, b>>8, uint32(60), "no blue at %v", p)
	}
}
