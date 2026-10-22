package api_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/conversations"
	"github.com/wnexous/partyline/internal/rooms"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// mergeWorld is alice with two AIs: "old", the v0.3 identity that has no folder, and "atlas", the AI
// of her folder; bob, carol and an admin are the other users.
type mergeWorld struct {
	env                      *testkit.Env
	fake                     *connectortest.Fake
	alice, bob, carol, admin *testkit.Client
	old, atlas               string
}

func newMergeWorld(t *testing.T) *mergeWorld {
	t.Helper()
	env, fake := fakeConnectorEnv(t)
	w := &mergeWorld{env: env, fake: fake, alice: env.NewUser("alice"), bob: env.NewUser("bob"), carol: env.NewUser("carol"), admin: env.NewAdmin("root")}
	w.old = env.NewLegacyAI(w.alice, "old")
	w.atlas = env.NewAI(w.alice, "atlas")
	return w
}

// by is the principal of alice's AI id, as the AI's session would post.
func (w *mergeWorld) by(id string) authn.Principal {
	return authn.Principal{Kind: authn.KindAgent, UserID: w.alice.User.ID, AgentID: id}
}

// say has the AI post in the room.
func (w *mergeWorld) say(t *testing.T, ai, room, text string) {
	t.Helper()
	_, err := w.env.App.Rooms.Post(context.Background(), w.by(ai), room, rooms.PostInput{Text: text})
	require.NoError(t, err)
}

// write has the user post in the room.
func write(t *testing.T, c *testkit.Client, room, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](c.Post("/api/v1/rooms/"+room+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

// join makes the AI a member of the room, as the room's owner would.
func join(t *testing.T, owner *testkit.Client, room, ai string) dto.RoomMember {
	t.Helper()
	return testkit.Decode[dto.RoomMember](owner.Post("/api/v1/rooms/"+room+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: ai}).RequireStatus(http.StatusCreated))
}

// settle sets the per-room settings of an AI member.
func settle(t *testing.T, owner *testkit.Client, room string, m dto.RoomMember, req dto.UpdateMemberRequest) {
	t.Helper()
	owner.Patch("/api/v1/rooms/"+room+"/members/"+m.ID, req).RequireStatus(http.StatusOK)
}

// members returns the members of the room by kind and member id.
func members(t *testing.T, c *testkit.Client, room string) map[dto.MemberKind]map[string]dto.RoomMember {
	t.Helper()
	out := map[dto.MemberKind]map[string]dto.RoomMember{}
	for _, m := range testkit.Decode[dto.List[dto.RoomMember]](c.Get("/api/v1/rooms/" + room + "/members").RequireStatus(http.StatusOK)).Items {
		if out[m.Kind] == nil {
			out[m.Kind] = map[string]dto.RoomMember{}
		}
		out[m.Kind][m.MemberID] = m
	}
	return out
}

// timeline returns the messages of the room, oldest first.
func timeline(t *testing.T, c *testkit.Client, room string) []dto.Message {
	t.Helper()
	msgs := testkit.Decode[dto.Page[dto.Message]](c.Get("/api/v1/rooms/" + room + "/messages?limit=100").RequireStatus(http.StatusOK)).Items
	slices.Reverse(msgs)
	return msgs
}

func texts(msgs []dto.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Text
	}
	return out
}

// setCursor and cursor write and read the delivery cursor of an AI in a room (-1: none).
func (w *mergeWorld) setCursor(t *testing.T, ai, room string, seq int64) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, w.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.AdvanceAgentCursor(ctx, db.AdvanceAgentCursorParams{AgentID: ai, RoomID: room, Seq: seq})
	}))
}

func (w *mergeWorld) cursor(t *testing.T, ai, room string) int64 {
	t.Helper()
	seq, err := w.env.App.Store.R().GetAgentCursor(context.Background(), db.GetAgentCursorParams{AgentID: ai, RoomID: room})
	if errors.Is(err, sql.ErrNoRows) {
		return -1
	}
	require.NoError(t, err)
	return seq
}

// readCursor reads the read position of the user in a room (0: none).
func (w *mergeWorld) readCursor(t *testing.T, user *testkit.Client, room string) int64 {
	t.Helper()
	seq, err := w.env.App.Store.R().GetReadCursor(context.Background(), db.GetReadCursorParams{UserID: user.User.ID, RoomID: room})
	if errors.Is(err, sql.ErrNoRows) {
		return 0
	}
	require.NoError(t, err)
	return seq
}

// pending returns the texts of the messages an AI was not delivered in a room, oldest first.
func (w *mergeWorld) pending(t *testing.T, ai, room string) []string {
	t.Helper()
	views, err := w.env.App.Store.R().ListAgentPending(context.Background(), db.ListAgentPendingParams{
		RoomID: room, AfterSeq: max(w.cursor(t, ai, room), 0), AgentID: ai, Limit: 100,
	})
	require.NoError(t, err)
	out := make([]string, len(views))
	for i, v := range views {
		out[i] = v.Text
	}
	return out
}

// merge merges the AI from into the AI into as c.
func merge(c *testkit.Client, from, into string) *testkit.Response {
	c.T.Helper()
	return c.Post("/api/v1/agents/"+from+"/merge", dto.MergeAgentRequest{Into: into})
}

func visibleAgents(t *testing.T, c *testkit.Client) []string {
	t.Helper()
	var ids []string
	for _, a := range testkit.Decode[dto.List[dto.Agent]](c.Get("/api/v1/agents").RequireStatus(http.StatusOK)).Items {
		ids = append(ids, a.ID)
	}
	return ids
}

// MRG-01 (independent test): merging the unlinked identity into the AI of a folder moves its
// group memberships (the survivor's settings win where both are members), its delivery cursors
// (the larger wins), who may use it and the messages it wrote; it is gone and the merge is audited.
func TestMergeMovesGroupsCursorsGrantsAndMessages(t *testing.T) {
	w := newMergeWorld(t)
	shareAI(w.alice, w.old, w.bob)
	shareAI(w.alice, w.atlas, w.carol)

	ops, dev, ahead, solo := createRoom(w.alice, "ops"), createRoom(w.alice, "dev"), createRoom(w.alice, "ahead"), createRoom(w.alice, "solo")
	bobs := createRoom(w.bob, "bobs")
	trusted, mentions, all := dto.DeliveryModeTrusted, dto.DeliveryModeMentions, dto.DeliveryModeAll
	oldOps := join(t, w.alice, ops.ID, w.old)
	settle(t, w.alice, ops.ID, oldOps, dto.UpdateMemberRequest{DisplayName: ptr("Old Ops"), Delivery: &trusted, Keywords: &[]string{"deploy"}, SignOutbound: ptr(false)})
	oldDev, atlasDev := join(t, w.alice, dev.ID, w.old), join(t, w.alice, dev.ID, w.atlas)
	settle(t, w.alice, dev.ID, oldDev, dto.UpdateMemberRequest{DisplayName: ptr("Old in dev"), Delivery: &all})
	settle(t, w.alice, dev.ID, atlasDev, dto.UpdateMemberRequest{DisplayName: ptr("Atlas in dev"), Delivery: &mentions, Keywords: &[]string{"build"}})
	join(t, w.alice, ahead.ID, w.old)
	join(t, w.alice, ahead.ID, w.atlas)
	soloAtlas := join(t, w.alice, solo.ID, w.atlas)
	settle(t, w.alice, solo.ID, soloAtlas, dto.UpdateMemberRequest{DisplayName: ptr("Solo")})
	join(t, w.bob, bobs.ID, w.old) // bob may use old

	for _, room := range []string{ops.ID, dev.ID, ahead.ID, solo.ID} {
		for range 6 {
			write(t, w.alice, room, "hello")
		}
	}
	w.say(t, w.old, ops.ID, "from old")
	w.say(t, w.old, dev.ID, "also from old")
	w.say(t, w.atlas, dev.ID, "from atlas")
	for room, seqs := range map[string][2]int64{ops.ID: {3, -1}, dev.ID: {4, 2}, ahead.ID: {1, 5}, solo.ID: {-1, 1}} {
		if seqs[0] >= 0 {
			w.setCursor(t, w.old, room, seqs[0])
		}
		if seqs[1] >= 0 {
			w.setCursor(t, w.atlas, room, seqs[1])
		}
	}

	bobLive, _ := w.bob.DialLive("")
	var survivor dto.Agent
	merge(w.alice, w.old, w.atlas).RequireStatus(http.StatusOK).Decode(&survivor)
	require.Equal(t, w.atlas, survivor.ID, "the answer is the AI that remains")

	// Bob's socket hears that his group has a new member, that who may use the survivor changed and
	// that the survivor's profile did.
	for {
		var changed dto.RoomIDData
		bobLive.Expect(dto.LiveEventTypeMembersChanged, &changed, connectortest.Timeout)
		if changed.RoomID == bobs.ID {
			break
		}
	}
	var access dto.AccessChangedData
	bobLive.Expect(dto.LiveEventTypeAccessChanged, &access, connectortest.Timeout)
	require.Equal(t, dto.AccessChangedData{ResourceKind: "agent", ResourceID: w.atlas}, access)
	var profile dto.IntelligenceRef
	bobLive.Expect(dto.LiveEventTypeIntelligenceChanged, &profile, connectortest.Timeout)
	require.Equal(t, w.atlas, profile.ID)

	require.ElementsMatch(t, []string{w.atlas}, visibleAgents(t, w.alice), "old is gone")
	w.alice.Get("/api/v1/intelligences/"+w.old).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)

	// Groups: the membership moved with its settings; where both were members the survivor's row stays.
	agentsOf := func(room string) map[string]dto.RoomMember { return members(t, w.alice, room)[dto.MemberKindAgent] }
	got := agentsOf(ops.ID)
	require.Len(t, got, 1)
	moved := got[w.atlas]
	require.Equal(t, oldOps.ID, moved.ID, "the row moved, it was not recreated")
	require.Equal(t, []any{"Old Ops", dto.DeliveryModeTrusted, []string{"deploy"}, false}, []any{moved.DisplayName, moved.Delivery, moved.Keywords, moved.SignOutbound})
	got = agentsOf(dev.ID)
	require.Len(t, got, 1)
	kept := got[w.atlas]
	require.Equal(t, atlasDev.ID, kept.ID)
	require.Equal(t, []any{"Atlas in dev", dto.DeliveryModeMentions, []string{"build"}}, []any{kept.DisplayName, kept.Delivery, kept.Keywords}, "the survivor's settings win")
	require.Len(t, agentsOf(ahead.ID), 1)
	require.Contains(t, agentsOf(ahead.ID), w.atlas)
	require.Equal(t, "Solo", agentsOf(solo.ID)[w.atlas].DisplayName, "a group of the survivor alone is untouched")
	require.Contains(t, members(t, w.bob, bobs.ID)[dto.MemberKindAgent], w.atlas, "bob's group has the survivor now")

	// Cursors: the larger of the two.
	require.Equal(t, []int64{3, 4, 5, 1}, []int64{w.cursor(t, w.atlas, ops.ID), w.cursor(t, w.atlas, dev.ID), w.cursor(t, w.atlas, ahead.ID), w.cursor(t, w.atlas, solo.ID)})
	require.Equal(t, int64(-1), w.cursor(t, w.old, ops.ID), "the old cursors went with it")

	// Who may use old may use the survivor, next to those who already could; the old grants are gone.
	sharing := testkit.Decode[dto.Sharing](w.alice.Get("/api/v1/agents/" + w.atlas + "/sharing").RequireStatus(http.StatusOK))
	require.Equal(t, dto.VisibilityShared, sharing.Mode)
	var users []string
	for _, u := range sharing.Users {
		require.Equal(t, dto.LevelUse, u.Level)
		users = append(users, u.UserID)
	}
	require.ElementsMatch(t, []string{w.bob.User.ID, w.carol.User.ID}, users)
	require.Contains(t, visibleAgents(t, w.bob), w.atlas, "bob can use the survivor")
	left, err := w.env.App.Store.R().ListGrantsWithUsers(context.Background(), db.ListGrantsWithUsersParams{ResourceKind: "agent", ResourceID: w.old})
	require.NoError(t, err)
	require.Empty(t, left)

	// The messages it wrote are the survivor's.
	for _, room := range []string{ops.ID, dev.ID} {
		for _, m := range timeline(t, w.alice, room) {
			if m.AuthorKind != dto.AuthorKindAgent {
				continue
			}
			require.Equal(t, []any{w.atlas, w.atlas, "atlas", dto.IntelligenceRoleAI}, []any{m.AuthorID, m.Author.ID, m.Author.Handle, m.Author.Role}, m.Text)
		}
	}
	require.Equal(t, w.atlas, timeline(t, w.alice, ops.ID)[6].Author.ID, "from old")

	// The merge is audited.
	entry := findAudit(t, auditPage(w.admin, "").Items, "agent.merge")
	require.Equal(t, []any{w.alice.User.ID, "agent", w.atlas}, []any{entry.Actor.ID, entry.TargetKind, entry.TargetID})
	require.Equal(t, []any{w.old, w.atlas, "old", "atlas"}, []any{entry.Detail["from"], entry.Detail["into"], entry.Detail["from_handle"], entry.Detail["into_handle"]})
}

// MRG-01: where both AIs grant the same user access, the higher level wins.
func TestMergeKeepsTheHigherGrantLevel(t *testing.T) {
	w := newMergeWorld(t)
	ctx := context.Background()
	grant := func(ai string, user *testkit.Client, level dto.Level) {
		t.Helper()
		require.NoError(t, w.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
			return q.CreateGrant(ctx, db.CreateGrantParams{ResourceKind: "agent", ResourceID: ai, Subject: user.User.ID, Level: string(level), CreatedAt: time.Now().UnixMilli()})
		}))
	}
	level := func(user *testkit.Client) []string {
		t.Helper()
		got, err := w.env.App.Store.R().ListGrantLevels(ctx, db.ListGrantLevelsParams{ResourceKind: "agent", ResourceID: w.atlas, UserID: user.User.ID})
		require.NoError(t, err)
		return got
	}
	grant(w.old, w.bob, dto.LevelUse)
	grant(w.atlas, w.bob, dto.LevelView)
	grant(w.old, w.carol, dto.LevelView)
	grant(w.atlas, w.carol, dto.LevelUse)

	merge(w.alice, w.old, w.atlas).RequireStatus(http.StatusOK)

	require.Equal(t, []string{"use"}, level(w.bob), "old's higher level replaced the survivor's lower one")
	require.Equal(t, []string{"use"}, level(w.carol), "the survivor's higher level stays")
}

// MRG-01: a DM of the merged AI with someone the survivor has no DM with becomes the survivor's DM, as
// it is; where the survivor has one too, the history is appended to it after a notice that names the
// merged AI, with the cursors renumbered (the larger wins), and the old DM is deleted; a DM between
// the two AIs goes away. Live sockets hear of each.
func TestMergeCarriesDirectMessages(t *testing.T) {
	w := newMergeWorld(t)
	ctx := context.Background()
	shareAI(w.alice, w.old, w.bob)
	oldWithAlice := dmWith(w.alice, w.old, http.StatusCreated)
	atlasWithAlice := dmWith(w.alice, w.atlas, http.StatusCreated)
	oldWithBob := dmWith(w.bob, w.old, http.StatusCreated)
	between := dmByService(t, w.env, w.by(w.old), w.atlas)

	write(t, w.alice, atlasWithAlice.ID, "to atlas 1")
	w.say(t, w.atlas, atlasWithAlice.ID, "reply from atlas")
	write(t, w.alice, oldWithAlice.ID, "to old 1")
	w.say(t, w.old, oldWithAlice.ID, "reply from old")
	write(t, w.alice, oldWithAlice.ID, "to old 2")
	write(t, w.bob, oldWithBob.ID, "hi old")
	w.say(t, w.old, oldWithBob.ID, "hi bob")
	w.say(t, w.old, between, "talking to myself")

	// Alice read all of her DM with atlas and the first message of the one with old; atlas received
	// both messages of its DM, old the first of its own.
	w.alice.Put("/api/v1/rooms/"+atlasWithAlice.ID+"/read", dto.MarkReadRequest{}).RequireStatus(http.StatusOK)
	w.alice.Put("/api/v1/rooms/"+oldWithAlice.ID+"/read", dto.MarkReadRequest{Seq: ptr(int64(1))}).RequireStatus(http.StatusOK)
	w.setCursor(t, w.atlas, atlasWithAlice.ID, 2)
	w.setCursor(t, w.old, oldWithAlice.ID, 1)
	w.setCursor(t, w.old, oldWithBob.ID, 2) // old received everything of its DM with bob
	aliceLive, _ := w.alice.DialLive("")
	bobLive, _ := w.bob.DialLive("")

	merge(w.alice, w.old, w.atlas).RequireStatus(http.StatusOK)

	// Appended: the survivor's history, the notice, then the merged AI's, in the order written.
	w.alice.Get("/api/v1/rooms/"+oldWithAlice.ID).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	history := timeline(t, w.alice, atlasWithAlice.ID)
	require.Equal(t, []string{"to atlas 1", "reply from atlas", "History merged from @old", "to old 1", "reply from old", "to old 2"}, texts(history))
	for i, m := range history {
		require.Equal(t, int64(i+1), m.Seq)
	}
	require.Equal(t, []any{dto.AuthorKindSystem, dto.MessageKindSystem, ""}, []any{history[2].AuthorKind, history[2].Kind, history[2].Author.ID})
	require.Equal(t, w.alice.User.ID, history[3].Author.ID)
	require.Equal(t, w.atlas, history[4].Author.ID, "what old said, atlas says now")
	require.Equal(t, w.alice.User.ID, history[5].Author.ID)
	kinds := members(t, w.alice, atlasWithAlice.ID)
	require.Len(t, kinds[dto.MemberKindAgent], 1)
	require.Contains(t, kinds[dto.MemberKindAgent], w.atlas)
	require.Len(t, kinds[dto.MemberKindUser], 1)

	// Cursors: alice had read all of the survivor's DM (2) and the first message of the old one,
	// which is message 4 now; atlas had received 2 and old 1 (4): the larger holds.
	conv := conversation(t, w.alice, atlasWithAlice.ID)
	require.Equal(t, 1, conv.Unread, "only 'reply from old' is after message 4 and not hers")
	require.Equal(t, int64(4), w.cursor(t, w.atlas, atlasWithAlice.ID))

	// Re-keyed: bob's DM with old is his DM with the survivor, the same room with its history; one DM per pair.
	room := testkit.Decode[dto.Room](w.bob.Get("/api/v1/rooms/" + oldWithBob.ID).RequireStatus(http.StatusOK))
	require.Equal(t, dto.LevelPost, room.MyLevel)
	var participants []string
	for _, p := range room.Participants {
		participants = append(participants, p.ID)
	}
	require.ElementsMatch(t, []string{w.bob.User.ID, w.atlas}, participants)
	require.Equal(t, []string{"hi old", "hi bob"}, texts(timeline(t, w.bob, oldWithBob.ID)))
	require.Equal(t, w.atlas, timeline(t, w.bob, oldWithBob.ID)[1].Author.ID)
	require.Equal(t, oldWithBob.ID, dmWith(w.bob, w.atlas, http.StatusOK).ID, "bob's DM with the survivor is the one old had")
	require.Equal(t, int64(2), w.cursor(t, w.atlas, oldWithBob.ID), "the survivor does not get that history again")
	rekeyed := members(t, w.bob, oldWithBob.ID)
	require.Contains(t, rekeyed[dto.MemberKindAgent], w.atlas)
	require.Len(t, rekeyed[dto.MemberKindAgent], 1)

	// The DM between the two is gone and nothing of old is left in any DM.
	w.alice.Get("/api/v1/rooms/"+between).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	left, err := w.env.App.Store.R().ListAgentDMRooms(ctx, sql.NullString{String: w.old, Valid: true})
	require.NoError(t, err)
	require.Empty(t, left)

	// The sockets of the participants hear of it: alice of the DM she took part in (she only reads the
	// one between her AIs), bob of the DM that has a new participant.
	for { // the DM that received the history changed, then the old one was deleted
		var changed dto.Room
		aliceLive.Expect(dto.LiveEventTypeRoomChanged, &changed, connectortest.Timeout)
		if changed.ID == atlasWithAlice.ID {
			break
		}
	}
	var gone dto.RoomIDData
	aliceLive.Expect(dto.LiveEventTypeRoomDeleted, &gone, connectortest.Timeout)
	require.Equal(t, oldWithAlice.ID, gone.RoomID)
	for {
		var changed dto.Room
		bobLive.Expect(dto.LiveEventTypeRoomChanged, &changed, connectortest.Timeout)
		if changed.ID == oldWithBob.ID && slices.ContainsFunc(changed.Participants, func(p dto.IntelligenceRef) bool { return p.ID == w.atlas }) {
			break
		}
	}
}

// MRG-01: a cursor moves into the surviving DM only for a party that had received or read all of
// it, so nothing a party has not seen is skipped. Here the survivor was not delivered two
// messages of the user and the user had not read what the survivor wrote, while the merged AI's
// DM was received and read up to its end: both cursors stay where they were.
func TestMergeKeepsWhatTheSurvivorHasNotReceivedOrRead(t *testing.T) {
	w := newMergeWorld(t)
	atlasWithAlice := dmWith(w.alice, w.atlas, http.StatusCreated)
	oldWithAlice := dmWith(w.alice, w.old, http.StatusCreated)

	write(t, w.alice, atlasWithAlice.ID, "to atlas 1")
	write(t, w.alice, atlasWithAlice.ID, "to atlas 2")
	w.say(t, w.atlas, atlasWithAlice.ID, "atlas speaks")
	w.setCursor(t, w.atlas, atlasWithAlice.ID, 0) // connected, nothing delivered yet

	write(t, w.alice, oldWithAlice.ID, "to old")
	w.say(t, w.old, oldWithAlice.ID, "old answers")
	w.setCursor(t, w.old, oldWithAlice.ID, 1) // old received the one message of its DM
	w.alice.Put("/api/v1/rooms/"+oldWithAlice.ID+"/read", dto.MarkReadRequest{}).RequireStatus(http.StatusOK)

	merge(w.alice, w.old, w.atlas).RequireStatus(http.StatusOK)

	require.Equal(t, []string{"to atlas 1", "to atlas 2", "atlas speaks", "History merged from @old", "to old", "old answers"},
		texts(timeline(t, w.alice, atlasWithAlice.ID)))
	require.Equal(t, int64(0), w.cursor(t, w.atlas, atlasWithAlice.ID), "the survivor was not delivered its first messages")
	require.Equal(t, []string{"to atlas 1", "to atlas 2", "History merged from @old", "to old"}, w.pending(t, w.atlas, atlasWithAlice.ID),
		"its two messages are still pending, then what came with the merge")
	require.Equal(t, int64(0), w.readCursor(t, w.alice, atlasWithAlice.ID), "alice had not read what atlas wrote")
	require.Equal(t, 3, conversation(t, w.alice, atlasWithAlice.ID).Unread, "atlas's message, the notice and what old answered")
}

// MRG-01: a party that had received or read everything of the surviving DM takes the renumbered
// position of its cursor in the merged DM, also when its own message was the last one there: the
// AI is not delivered again what it received and the user does not read it again.
func TestMergeMovesTheCursorsOfPartiesThatWereUpToDate(t *testing.T) {
	w := newMergeWorld(t)
	shareAI(w.alice, w.old, w.bob)
	shareAI(w.alice, w.atlas, w.bob)
	read := func(c *testkit.Client, room string, seq *int64) {
		t.Helper()
		c.Put("/api/v1/rooms/"+room+"/read", dto.MarkReadRequest{Seq: seq}).RequireStatus(http.StatusOK)
	}

	// alice: the survivor wrote the last message of its DM with her, so its cursor is below it.
	atlasAlice, oldAlice := dmWith(w.alice, w.atlas, http.StatusCreated), dmWith(w.alice, w.old, http.StatusCreated)
	write(t, w.alice, atlasAlice.ID, "q1")
	w.say(t, w.atlas, atlasAlice.ID, "a1")
	w.setCursor(t, w.atlas, atlasAlice.ID, 1)
	read(w.alice, atlasAlice.ID, nil)
	write(t, w.alice, oldAlice.ID, "o1")
	w.say(t, w.old, oldAlice.ID, "o2")
	w.setCursor(t, w.old, oldAlice.ID, 1)
	read(w.alice, oldAlice.ID, nil)

	// bob: he wrote the last message of his DM with the survivor, so his read position is below it.
	atlasBob, oldBob := dmWith(w.bob, w.atlas, http.StatusCreated), dmWith(w.bob, w.old, http.StatusCreated)
	w.say(t, w.atlas, atlasBob.ID, "hi bob")
	write(t, w.bob, atlasBob.ID, "hello")
	w.setCursor(t, w.atlas, atlasBob.ID, 2)
	read(w.bob, atlasBob.ID, ptr(int64(1)))
	w.say(t, w.old, oldBob.ID, "hey bob")
	write(t, w.bob, oldBob.ID, "hey")
	w.setCursor(t, w.old, oldBob.ID, 2)
	read(w.bob, oldBob.ID, ptr(int64(1)))

	merge(w.alice, w.old, w.atlas).RequireStatus(http.StatusOK)

	// alice's DM is now q1 a1 | notice | o1 o2 (1-2, 3, 4-5): atlas received o1 and wrote o2, alice read both.
	require.Equal(t, int64(4), w.cursor(t, w.atlas, atlasAlice.ID))
	require.Empty(t, w.pending(t, w.atlas, atlasAlice.ID), "nothing is delivered again")
	require.Equal(t, int64(5), w.readCursor(t, w.alice, atlasAlice.ID))
	require.Zero(t, conversation(t, w.alice, atlasAlice.ID).Unread)
	// bob's is hi hello | notice | hey-bob hey (1-2, 3, 4-5): bob read the greeting and wrote the other.
	require.Equal(t, int64(5), w.cursor(t, w.atlas, atlasBob.ID))
	require.Empty(t, w.pending(t, w.atlas, atlasBob.ID))
	require.Equal(t, int64(4), w.readCursor(t, w.bob, atlasBob.ID))
	require.Zero(t, conversation(t, w.bob, atlasBob.ID).Unread, "he does not read the merged history again")
}

// MRG-01: in a merged DM whose counterpart is an AI, each AI's cursor is credited to itself: the
// merged AI's to the survivor, the counterpart's to the counterpart (M19).
func TestMergeCreditsEachAICursorToItself(t *testing.T) {
	w := newMergeWorld(t)
	orion := w.env.NewAI(w.alice, "orion")
	target := dmByService(t, w.env, w.by(orion), w.atlas)
	oldDM := dmByService(t, w.env, w.by(orion), w.old)
	w.say(t, orion, target, "t1")
	w.say(t, w.atlas, target, "t2")
	w.setCursor(t, w.atlas, target, 1) // received t1; t2 is its own
	w.setCursor(t, orion, target, 2)   // received t2; t1 is its own
	w.say(t, orion, oldDM, "o1")
	w.say(t, w.old, oldDM, "o2")
	w.say(t, orion, oldDM, "o3")
	w.setCursor(t, w.old, oldDM, 3) // received o1 and o3
	w.setCursor(t, orion, oldDM, 2) // received o2; o3 is its own

	merge(w.alice, w.old, w.atlas).RequireStatus(http.StatusOK)

	require.Equal(t, []string{"t1", "t2", "History merged from @old", "o1", "o2", "o3"}, texts(timeline(t, w.alice, target)))
	require.Equal(t, int64(6), w.cursor(t, w.atlas, target), "the survivor received what the merged AI received: up to o3")
	require.Equal(t, int64(5), w.cursor(t, orion, target), "orion received o2 and wrote o3: its own cursor moved, not the survivor's")
	require.Empty(t, w.pending(t, w.atlas, target))
	require.Empty(t, w.pending(t, orion, target))
}

func conversation(t *testing.T, c *testkit.Client, room string) dto.Conversation {
	t.Helper()
	for _, cv := range testkit.Decode[dto.List[dto.Conversation]](c.Get("/api/v1/conversations").RequireStatus(http.StatusOK)).Items {
		if cv.ID == room {
			return cv
		}
	}
	t.Fatalf("no conversation %s", room)
	return dto.Conversation{}
}

// MRG-01: the connectors the merged AI operated are operated by the survivor.
func TestMergeMovesTheOperatorOfConnectors(t *testing.T) {
	w := newMergeWorld(t)
	mine, other := createFake(w.alice, "wa", true), createFake(w.alice, "tg", true)
	w.alice.Patch("/api/v1/connectors/"+mine.ID, dto.UpdateConnectorRequest{OperatorID: &w.old}).RequireStatus(http.StatusOK)
	operator := func(id string) string {
		return testkit.Decode[dto.Connector](w.alice.Get("/api/v1/connectors/" + id).RequireStatus(http.StatusOK)).Operator.ID
	}
	require.Equal(t, w.old, operator(mine.ID))
	require.Equal(t, w.alice.User.ID, operator(other.ID))

	merge(w.alice, w.old, w.atlas).RequireStatus(http.StatusOK)

	require.Equal(t, w.atlas, operator(mine.ID))
	require.Equal(t, w.alice.User.ID, operator(other.ID), "the owner still operates the other")
	apps := testkit.Decode[dto.IntelligenceProfile](w.alice.Get("/api/v1/intelligences/" + w.atlas).RequireStatus(http.StatusOK)).Apps
	require.Contains(t, apps, dto.App{Kind: connectortest.KindName, Label: "Fake: wa", ConnectorID: mine.ID})
}

// MRG-02: an AI with a live session is not merged and nothing changes; once its session ended, the
// AI of a folder merges into another that is online.
func TestMergeRefusesAnOnlineAI(t *testing.T) {
	w := newMergeWorld(t)
	old := w.env.NewAI(w.alice, "veteran") // a folder AI that moved: its old identity
	token := w.alice.Token(dto.TokenKindAgent, "laptop")
	conn, resp := token.DialAgent("veteran")
	require.NotNil(t, conn, "status %d", resp.Status)
	conn.Welcome(connectortest.Timeout)
	room := createRoom(w.alice, "ops")
	join(t, w.alice, room.ID, old)
	write(t, w.alice, room.ID, "hello")
	atlasConn, _ := token.DialAgent("atlas")
	atlasConn.Welcome(connectortest.Timeout)

	e := merge(w.alice, old, w.atlas).RequireError(http.StatusConflict, dto.ErrorCodeIdentityOnline)
	require.NotEmpty(t, e.Message)
	require.Contains(t, visibleAgents(t, w.alice), old, "nothing changed")
	require.Contains(t, members(t, w.alice, room.ID)[dto.MemberKindAgent], old)
	require.NotContains(t, members(t, w.alice, room.ID)[dto.MemberKindAgent], w.atlas)
	require.NotContains(t, auditActions(t, w.admin), "agent.merge")

	conn.Close()
	require.Eventually(t, func() bool {
		for _, a := range testkit.Decode[dto.List[dto.Agent]](w.alice.Get("/api/v1/agents").RequireStatus(http.StatusOK)).Items {
			if a.ID == old {
				return !a.Online
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond, "the identity is offline within seconds (SES-06)")
	merge(w.alice, old, w.atlas).RequireStatus(http.StatusOK)
	require.NotContains(t, visibleAgents(t, w.alice), old)
	require.Contains(t, members(t, w.alice, room.ID)[dto.MemberKindAgent], w.atlas, "the survivor was online and is a member now")
}

func auditActions(t *testing.T, admin *testkit.Client) []string {
	t.Helper()
	var actions []string
	for _, e := range auditPage(admin, "").Items {
		actions = append(actions, e.Action)
	}
	return actions
}

// Authorization of POST /agents/{id}/merge: both AIs must be the caller's. Someone who may only use
// an AI is forbidden, someone who cannot see it gets 404 (also admins), a missing or equal target is
// 422, and agent tokens and anonymous callers are refused; every refusal changes nothing.
func TestMergeAuthorization(t *testing.T) {
	w := newMergeWorld(t)
	shareAI(w.alice, w.old, w.bob)
	bobs := w.env.NewAI(w.bob, "bobby")   // private to bob
	shared := w.env.NewAI(w.bob, "sunny") // bob lets alice use it
	shareAI(w.bob, shared, w.alice)
	room := createRoom(w.alice, "ops")
	join(t, w.alice, room.ID, w.old)

	refuse := func(c *testkit.Client, from, into string, status int, code dto.ErrorCode) dto.Error {
		t.Helper()
		return merge(c, from, into).RequireError(status, code)
	}
	// alice owns old and atlas
	refuse(w.alice, w.old, bobs, http.StatusNotFound, dto.ErrorCodeNotFound)     // another owner's AI she cannot use
	refuse(w.alice, w.old, shared, http.StatusForbidden, dto.ErrorCodeForbidden) // one she may only use
	refuse(w.alice, bobs, w.atlas, http.StatusNotFound, dto.ErrorCodeNotFound)
	refuse(w.alice, shared, w.atlas, http.StatusForbidden, dto.ErrorCodeForbidden)
	refuse(w.alice, "no-such-ai", w.atlas, http.StatusNotFound, dto.ErrorCodeNotFound)
	refuse(w.alice, w.old, "no-such-ai", http.StatusNotFound, dto.ErrorCodeNotFound)
	e := refuse(w.alice, w.old, "", http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Equal(t, map[string]string{"into": "required"}, e.Fields)
	e = refuse(w.alice, w.old, w.old, http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Equal(t, map[string]string{"into": "must be a different AI"}, e.Fields)
	// bob may use old, which is not enough; carol and the admin cannot see it; the checks on the
	// target never answer before the caller's right to the AI in the path
	refuse(w.bob, w.old, bobs, http.StatusForbidden, dto.ErrorCodeForbidden)
	refuse(w.carol, w.old, w.atlas, http.StatusNotFound, dto.ErrorCodeNotFound)
	refuse(w.carol, w.old, w.old, http.StatusNotFound, dto.ErrorCodeNotFound)
	refuse(w.admin, w.old, w.atlas, http.StatusNotFound, dto.ErrorCodeNotFound)
	refuse(w.alice.Token(dto.TokenKindAgent, "bot"), w.old, w.atlas, http.StatusForbidden, dto.ErrorCodeForbidden)
	refuse(w.env.Anon(), w.old, w.atlas, http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	w.alice.DoRaw(http.MethodPost, "/api/v1/agents/"+w.old+"/merge", "application/json", strings.NewReader("{")).RequireError(http.StatusBadRequest, dto.ErrorCodeInvalid)

	require.Subset(t, visibleAgents(t, w.alice), []string{w.old, w.atlas, shared})
	require.Contains(t, members(t, w.alice, room.ID)[dto.MemberKindAgent], w.old)
	require.NotContains(t, auditActions(t, w.admin), "agent.merge")

	// A user token is the user.
	w.alice.Token(dto.TokenKindUser, "script").Post("/api/v1/agents/"+w.old+"/merge", dto.MergeAgentRequest{Into: w.atlas}).RequireStatus(http.StatusOK)
}

// The merge is one transaction: when its last step fails nothing of it is left, and it works once
// the cause is gone.
func TestMergeIsAllOrNothing(t *testing.T) {
	w := newMergeWorld(t)
	shareAI(w.alice, w.old, w.bob)
	room := createRoom(w.alice, "ops")
	oldMember := join(t, w.alice, room.ID, w.old)
	w.say(t, w.old, room.ID, "from old")
	w.setCursor(t, w.old, room.ID, 1)
	dm := dmWith(w.alice, w.old, http.StatusCreated)
	write(t, w.alice, dm.ID, "to old")

	raw, err := sql.Open("sqlite", "file:"+filepath.Join(w.env.App.Config.DataDir, app.DatabaseFile)+"?_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	_, err = raw.ExecContext(context.Background(), "CREATE TRIGGER forced_failure BEFORE DELETE ON agents WHEN OLD.name = 'old' BEGIN SELECT RAISE(ABORT, 'forced failure'); END")
	require.NoError(t, err)

	merge(w.alice, w.old, w.atlas).RequireError(http.StatusInternalServerError, dto.ErrorCodeInternal)

	require.Contains(t, visibleAgents(t, w.alice), w.old)
	agents := members(t, w.alice, room.ID)[dto.MemberKindAgent]
	require.Equal(t, oldMember.ID, agents[w.old].ID)
	require.NotContains(t, agents, w.atlas)
	require.Equal(t, w.old, timeline(t, w.alice, room.ID)[0].Author.ID)
	require.Equal(t, int64(-1), w.cursor(t, w.atlas, room.ID))
	require.Equal(t, int64(1), w.cursor(t, w.old, room.ID))
	require.Contains(t, members(t, w.alice, dm.ID)[dto.MemberKindAgent], w.old)
	require.Equal(t, []string{"to old"}, texts(timeline(t, w.alice, dm.ID)))
	require.NotContains(t, visibleAgents(t, w.bob), w.atlas, "no grant was copied")
	require.NotContains(t, auditActions(t, w.admin), "agent.merge")

	_, err = raw.ExecContext(context.Background(), "DROP TRIGGER forced_failure")
	require.NoError(t, err)
	merge(w.alice, w.old, w.atlas).RequireStatus(http.StatusOK)
	require.NotContains(t, visibleAgents(t, w.alice), w.old)
}

// MRG-02 for a session that attaches while the merge runs: after the check, the AI that was merged
// away has its live session closed, so no socket is left holding a deleted identity; and the check
// itself refuses an AI the presence says is online, whatever else is true.
func TestMergeEndsASessionThatAttachedAfterTheCheck(t *testing.T) {
	w := newMergeWorld(t)
	var kicked []string
	var online string
	svc := conversations.New(conversations.Deps{
		Store: w.env.App.Store, Rooms: w.env.App.Rooms, Intelligences: w.env.App.Intelligences, Bus: w.env.App.Bus, Access: w.env.App.Access,
		Online: func(id string) bool { return id == online },
		Kick:   func(_ context.Context, id string) { kicked = append(kicked, id) },
	})
	alice := authn.Principal{Kind: authn.KindUser, UserID: w.alice.User.ID, Role: string(dto.RoleMember)}
	ctx := context.Background()

	online = w.old
	require.ErrorIs(t, svc.Merge(ctx, alice, w.old, w.atlas), conversations.ErrIdentityOnline)
	require.Empty(t, kicked, "nothing was merged, nobody is closed")
	require.Contains(t, visibleAgents(t, w.alice), w.old)

	online = "" // nobody is online at the check
	require.NoError(t, svc.Merge(ctx, alice, w.old, w.atlas))
	require.Equal(t, []string{w.old}, kicked)
}
