package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// CHART-02, OMEM-06 (T32): the live socket follows organization membership. What an AI's presence, a
// connection's status and its chats are for a user is decided by the organizations the user belongs to,
// when the event happens (an AI is seen by its host and by the members of its organizations, a connection
// by the members of its organization), and a change of an organization reaches its members, and the
// member who just left, as access.changed.

// liveWorld is acme of testkit.OrgRoles with a connection of its own, sharing nothing with anyone else.
type liveWorld struct {
	env  *testkit.Env
	acme *testkit.OrgRoles
	// connector is a connection of acme, and chat one of its chats.
	connector, chat string
}

func newLiveWorld(t *testing.T) *liveWorld {
	t.Helper()
	env := testkit.New(t)
	acme := env.OrgRoles("acme")
	connector, chat := storeChat(t, env, acme.Admin.User.ID, "support")
	setConnectorOrg(t, env, connector, acme.Org.ID)
	return &liveWorld{env: env, acme: acme, connector: connector, chat: chat}
}

// setConnectorOrg puts a connection in an organization, as creating it there does.
func setConnectorOrg(t *testing.T, env *testkit.Env, connectorID, orgID string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetConnectorOrg(ctx, db.SetConnectorOrgParams{OrgID: sql.NullString{String: orgID, Valid: true}, ID: connectorID})
	}))
}

// setRoomHome puts a group in an organization, created by creatorIID, as creating it there does.
func setRoomHome(t *testing.T, env *testkit.Env, roomID, orgID, creatorIID string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetRoomHome(ctx, db.SetRoomHomeParams{
			OrgID: sql.NullString{String: orgID, Valid: true}, CreatorIid: sql.NullString{String: creatorIID, Valid: true}, ID: roomID,
		})
	}))
}

// frameKey names a live frame by what it is about: "access:<kind>:<id>" for access.changed, "<type>:<id>" for the
// frames of a room or an AI, and the type alone for the others.
func frameKey(t *testing.T, f testkit.Frame) string {
	t.Helper()
	switch f.Type {
	case dto.LiveEventTypeAccessChanged:
		var d dto.AccessChangedData
		require.NoError(t, json.Unmarshal(f.Data, &d))
		return "access:" + d.ResourceKind + ":" + d.ResourceID
	case dto.LiveEventTypeRoomDeleted, dto.LiveEventTypeMembersChanged:
		var d dto.RoomIDData
		require.NoError(t, json.Unmarshal(f.Data, &d))
		return string(f.Type) + ":" + d.RoomID
	case dto.LiveEventTypeRoomChanged:
		var d dto.Room
		require.NoError(t, json.Unmarshal(f.Data, &d))
		return string(f.Type) + ":" + d.ID
	case dto.LiveEventTypeAgentPresence:
		var d dto.AgentPresence
		require.NoError(t, json.Unmarshal(f.Data, &d))
		return string(f.Type) + ":" + d.AgentID
	case dto.LiveEventTypeConnectorStatus:
		var d dto.ConnectorStatusData
		require.NoError(t, json.Unmarshal(f.Data, &d))
		return string(f.Type) + ":" + d.ConnectorID
	}
	return string(f.Type)
}

// keysUntil reads frames until every wanted key has arrived (failing the test when one does not come) and
// returns the keys of all the frames read, in order, then those of the frames that follow within quiet.
func keysUntil(t *testing.T, l *testkit.LiveConn, want ...string) []string {
	t.Helper()
	var keys []string
	missing := map[string]bool{}
	for _, w := range want {
		missing[w] = true
	}
	for len(missing) > 0 {
		k := frameKey(t, l.Next(liveWait))
		keys = append(keys, k)
		delete(missing, k)
	}
	for _, f := range l.Drain(quiet) {
		keys = append(keys, frameKey(t, f))
	}
	return keys
}

// keysWithPrefix are the keys that start with one of the prefixes.
func keysWithPrefix(keys []string, prefixes ...string) []string {
	var out []string
	for _, k := range keys {
		for _, p := range prefixes {
			if strings.HasPrefix(k, p) {
				out = append(out, k)
				break
			}
		}
	}
	return out
}

// LIVE-04, CHART-02: an AI's presence reaches the members of its organization (guests too) and its host,
// not an outsider, not the instance admin, and not a user until they join; and it stops reaching a user
// who is removed. A host who left still hears of the AI they run.
func TestLiveAgentPresenceFollowsOrganizationMembership(t *testing.T) {
	ctx := context.Background()
	w := newLiveWorld(t)
	acme, org := w.acme, w.acme.Org.ID
	joiner := w.env.NewUser("joiner")
	presence := events.AgentPresence{AgentID: acme.TargetAI, OwnerID: acme.Manager.User.ID, Online: true}

	seers := map[string]*testkit.LiveConn{}
	for name, c := range map[string]*testkit.Client{"admin": acme.Admin, "host": acme.Manager, "member": acme.Member, "guest": acme.Guest} {
		seers[name] = dialLive(t, c)
	}
	strangers := map[string]*testkit.LiveConn{}
	for name, c := range map[string]*testkit.Client{"outsider": acme.Outsider, "instance admin": acme.InstanceAdmin, "joiner": joiner} {
		strangers[name] = dialLive(t, c)
	}

	w.env.App.Bus.Publish(presence)
	for name, l := range seers {
		var p dto.AgentPresence
		l.Expect(dto.LiveEventTypeAgentPresence, &p, liveWait)
		require.Equal(t, dto.AgentPresence{AgentID: acme.TargetAI, Online: true}, p, name)
	}
	for _, l := range strangers {
		l.ExpectNone(quiet)
	}

	// Joining starts the events, removal stops them.
	w.env.AddMember(org, joiner.User.ID, orgs.PermissionMember, "")
	w.env.App.Bus.Publish(presence)
	var p dto.AgentPresence
	strangers["joiner"].Expect(dto.LiveEventTypeAgentPresence, &p, liveWait)
	require.Equal(t, acme.TargetAI, p.AgentID)

	require.NoError(t, w.env.App.Orgs.Remove(ctx, acme.Admin.User.ID, org, joiner.User.ID))
	strangers["joiner"].Drain(quiet) // what the removal itself announced
	w.env.App.Bus.Publish(events.AgentPresence{AgentID: acme.TargetAI, OwnerID: acme.Manager.User.ID, Online: false})
	for _, f := range strangers["joiner"].Drain(quiet) {
		require.NotEqual(t, dto.LiveEventTypeAgentPresence, f.Type, "a user who was removed hears nothing of the AIs")
	}

	// The host is not a member any more, and still runs the AI.
	require.NoError(t, w.env.App.Orgs.Leave(ctx, acme.Manager.User.ID, org))
	seers["host"].Drain(quiet)
	w.env.App.Bus.Publish(presence)
	seers["host"].Expect(dto.LiveEventTypeAgentPresence, nil, liveWait)
	seers["member"].Expect(dto.LiveEventTypeAgentPresence, nil, liveWait)
}

// The real session of an AI announces itself the same way: a member of its organization hears it come
// online, an outsider does not.
func TestLiveAIConnectingIsHeardByItsOrganizationOnly(t *testing.T) {
	w := newLiveWorld(t)
	member, outsider := dialLive(t, w.acme.Member), dialLive(t, w.acme.Outsider)

	conn, resp := w.acme.Manager.Token(dto.TokenKindAgent, "session").DialAgent(trustAI)
	require.NotNil(t, conn, "upgrade refused: %d", resp.Status)
	conn.Welcome(wsWait)
	var p dto.AgentPresence
	member.Expect(dto.LiveEventTypeAgentPresence, &p, liveWait)
	require.Equal(t, dto.AgentPresence{AgentID: w.acme.TargetAI, Online: true}, p)
	outsider.ExpectNone(quiet)
}

// LIVE-04, CHART-02: a connection's status and its chats reach the members of its organization, and an
// outsider or the instance admin receives none.
func TestLiveConnectorEventsReachTheMembersOfItsOrganizationOnly(t *testing.T) {
	w := newLiveWorld(t)
	acme := w.acme
	members := map[string]*testkit.LiveConn{}
	for name, c := range map[string]*testkit.Client{"admin": acme.Admin, "member": acme.Member, "guest": acme.Guest} {
		members[name] = dialLive(t, c)
	}
	outsider, root := dialLive(t, acme.Outsider), dialLive(t, acme.InstanceAdmin)

	w.env.App.Bus.Publish(events.ConnectorStatus{ConnectorID: w.connector, Status: string(dto.ConnectorStatusError), Detail: "bad gateway"})
	w.env.App.Bus.Publish(events.ChatDiscovered{ConnectorID: w.connector, ChatID: w.chat})
	for name, l := range members {
		var s dto.ConnectorStatusData
		l.Expect(dto.LiveEventTypeConnectorStatus, &s, liveWait)
		require.Equal(t, dto.ConnectorStatusData{ConnectorID: w.connector, Status: dto.ConnectorStatusError, LastError: "bad gateway"}, s, name)
		var chat dto.Chat
		l.Expect(dto.LiveEventTypeChatDiscovered, &chat, liveWait)
		require.Equal(t, w.chat, chat.ID, name)
	}
	outsider.ExpectNone(quiet)
	root.ExpectNone(quiet)
}

// A connection no organization owns yet (T26 puts every new one in an organization) is its owner's: its
// owner hears of it and the members of other organizations do not.
func TestLiveConnectionWithoutAnOrganizationIsItsOwners(t *testing.T) {
	w := newLiveWorld(t)
	owner := w.env.NewUser("owner")
	connector, _ := storeChat(t, w.env, owner.User.ID, "legacy")
	ownerLive, memberLive := dialLive(t, owner), dialLive(t, w.acme.Member)

	w.env.App.Bus.Publish(events.ConnectorStatus{ConnectorID: connector, Status: string(dto.ConnectorStatusConnected)})
	ownerLive.Expect(dto.LiveEventTypeConnectorStatus, nil, liveWait)
	memberLive.ExpectNone(quiet)
}

// The access.changed of an AI or a connection (a merge, a change of who may use it) is told to the same
// users: the members of its organization.
func TestLiveAccessChangedOfAnAIOrAConnectionReachesItsViewersOnly(t *testing.T) {
	w := newLiveWorld(t)
	member, outsider := dialLive(t, w.acme.Member), dialLive(t, w.acme.Outsider)

	w.env.App.Bus.Publish(events.GrantsChanged{ResourceKind: "agent", ResourceID: w.acme.TargetAI})
	w.env.App.Bus.Publish(events.GrantsChanged{ResourceKind: "connector", ResourceID: w.connector})
	require.Equal(t, []string{"access:agent:" + w.acme.TargetAI, "access:connector:" + w.connector},
		keysUntil(t, member, "access:agent:"+w.acme.TargetAI, "access:connector:"+w.connector))
	outsider.ExpectNone(quiet)
}

// OMEM-06, CHART-02: a member removed from an organization hears that the organization changed and loses
// the groups it was taken out of (room.deleted, then access.changed for the group); the members that stay
// hear the organization changed and nothing about the group; an outsider hears nothing.
func TestLiveRemovalFromAnOrganizationTellsThatOrganizationsGroups(t *testing.T) {
	ctx := context.Background()
	w := newLiveWorld(t)
	acme, org := w.acme, w.acme.Org.ID
	ops := createRoom(acme.Manager, "ops")
	setRoomHome(t, w.env, ops.ID, org, acme.Manager.User.ID)
	addToRoom(t, w.env, acme.Manager, ops.ID, acme.Target)
	addToRoom(t, w.env, acme.Manager, ops.ID, acme.Peer)
	targetLive, peerLive, adminLive, outsiderLive := dialLive(t, acme.Target), dialLive(t, acme.Peer), dialLive(t, acme.Admin), dialLive(t, acme.Outsider)

	require.NoError(t, w.env.App.Orgs.Remove(ctx, acme.Admin.User.ID, org, acme.Target.User.ID))

	orgChanged := "access:organization:" + org
	keys := keysUntil(t, targetLive, orgChanged, string(dto.LiveEventTypeRoomDeleted)+":"+ops.ID, "access:room:"+ops.ID)
	require.Contains(t, keys, orgChanged)
	require.Empty(t, keysWithPrefix(keys, "access:agent:", "access:connector:"), "the member who left is told no ids of the AIs and connections it can no longer see")

	for name, l := range map[string]*testkit.LiveConn{"peer": peerLive, "admin": adminLive} {
		keys := keysUntil(t, l, orgChanged)
		require.Empty(t, keysWithPrefix(keys, "room.deleted", "access:room:", "access:agent:", "access:connector:"), name+": its standing did not change")
	}
	outsiderLive.ExpectNone(quiet)

	// The group is gone from the member's view for good: a message in it reaches the others only.
	acme.Manager.Post("/api/v1/rooms/"+ops.ID+"/messages", dto.PostMessageRequest{Text: "after"}).RequireStatus(http.StatusCreated)
	peerLive.Expect(dto.LiveEventTypeRoomMessage, nil, liveWait)
	targetLive.ExpectNone(quiet)
}

// OMEM-06: joining an organization, or a change of the member's permission in it, tells the member's
// socket which AIs and connections of the organization to look at again, and any other change of the
// organization (its name) only that it changed.
func TestLiveMembershipChangesTellTheMemberItsAIsAndConnections(t *testing.T) {
	ctx := context.Background()
	w := newLiveWorld(t)
	acme, org := w.acme, w.acme.Org.ID
	joiner := w.env.NewUser("joiner")
	joinerLive, memberLive := dialLive(t, joiner), dialLive(t, acme.Member)
	orgChanged := "access:organization:" + org
	resources := []string{"access:agent:" + acme.TargetAI, "access:connector:" + w.connector}

	w.env.AddMember(org, joiner.User.ID, orgs.PermissionMember, "")
	keys := keysUntil(t, joinerLive, append([]string{orgChanged}, resources...)...)
	require.Subset(t, keys, resources)
	// The member who was already there hears only that the organization changed.
	require.Empty(t, keysWithPrefix(keysUntil(t, memberLive, orgChanged), "access:agent:", "access:connector:"))

	// A promotion changes what the member can do with them.
	_, err := w.env.App.Orgs.SetPermission(ctx, acme.Admin.User.ID, org, acme.Member.User.ID, orgs.PermissionAdmin)
	require.NoError(t, err)
	keys = keysUntil(t, memberLive, append([]string{orgChanged}, resources...)...)
	require.Subset(t, keys, resources)
	require.Empty(t, keysWithPrefix(keysUntil(t, joinerLive, orgChanged), "access:agent:", "access:connector:"), "the joiner's own standing did not change")

	// A rename changes nobody's standing.
	_, err = w.env.App.Orgs.Update(ctx, acme.Admin.User.ID, org, orgs.UpdateInput{Name: ptr("Acme Inc")})
	require.NoError(t, err)
	for name, l := range map[string]*testkit.LiveConn{"joiner": joinerLive, "member": memberLive} {
		require.Empty(t, keysWithPrefix(keysUntil(t, l, orgChanged), "access:agent:", "access:connector:", "room."), name)
	}
}

// OMEM-06: when the organization changes the socket looks at its user's groups again, so a group the user can
// now view, which the socket did not know, is announced as a room the user gained.
func TestLiveOrganizationChangeAnnouncesGroupsTheUserCanNowView(t *testing.T) {
	ctx := context.Background()
	w := newLiveWorld(t)
	acme, org := w.acme, w.acme.Org.ID
	ops := createRoom(acme.Manager, "ops")
	setRoomHome(t, w.env, ops.ID, org, acme.Manager.User.ID)
	peerLive := dialLive(t, acme.Peer)

	// The peer becomes a member of the group without the socket hearing of it (the event was dropped).
	require.NoError(t, w.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.CreateRoomMember(ctx, db.CreateRoomMemberParams{
			ID: store.NewID(), RoomID: ops.ID, Kind: string(dto.MemberKindUser), MemberID: acme.Peer.User.ID,
			AddedBy: acme.Manager.User.ID, CreatedAt: 1,
		})
	}))
	_, err := w.env.App.Orgs.SetPosition(ctx, acme.Admin.User.ID, org, acme.Peer.User.ID, w.acme.ManagerPosition)
	require.NoError(t, err)

	keys := keysUntil(t, peerLive, "room.changed:"+ops.ID, "access:room:"+ops.ID)
	require.Contains(t, keys, "access:organization:"+org)
}

// A direct message the user only views, through an AI they run, is not in the list of their rooms, and
// looking at the list again because their standing changed does not make the socket lose it.
func TestLiveOrganizationChangeKeepsADMTheUserOnlyViewsThroughAnAI(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	alice, bob := env.NewUser("alice"), env.NewUser("bob")
	atlas := env.NewAI(alice, "atlas")
	shareAI(alice, atlas, bob)
	dm := dmWith(bob, atlas, http.StatusCreated)
	aliceLive := dialLive(t, alice)

	write(t, bob, dm.ID, "hello Atlas")
	aliceLive.Expect(dto.LiveEventTypeRoomMessage, nil, liveWait) // alice views it: the socket knows the room now

	org := alice.PersonalOrg().ID
	_, err := env.App.Orgs.SetPosition(ctx, alice.User.ID, org, alice.User.ID, "")
	require.NoError(t, err)
	keys := keysUntil(t, aliceLive, "access:organization:"+org)
	require.Empty(t, keysWithPrefix(keys, "room.deleted"), "the DM is still hers to view")
}
