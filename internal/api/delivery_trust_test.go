package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
)

// POS-10, AD-018: trusted=true on a message delivered to an AI means its author manages the AI in the
// AI's home organization. These tests drive one set of authors through each way a message reaches an
// AI (a live push, the backlog of a connection and the history tool, L-034), once in the delivery mode
// "all" and once in "trusted", which delivers only what is trusted.

// trustAI is the name of the folder of the AI that receives: testkit.OrgRoles calls it "<org>-ai".
const trustAI = "acme-ai"

// trustWorld is acme of testkit.OrgRoles plus the identities POS-10 tells apart. The recipient, ai, is
// acme's AI, hosted and created by Manager (so Manager manages it) and below Admin, who holds the root
// position above Manager's. Every author but the outsider posts in ops, a group Manager created.
type trustWorld struct {
	env  *testkit.Env
	acme *testkit.OrgRoles
	ai   string
	// lead is an AI at the root position, above Manager: it manages ai through its creator. sibling is
	// another AI of the same user. sub was created by ai, so ai manages it and not the other way round.
	lead, sibling, sub string
	// ops is the group the messages are posted in; aiMember is ai's membership of it.
	ops      dto.Room
	aiMember string

	clients map[string]*testkit.Client // agent-token clients by folder name
}

func newTrustWorld(t *testing.T) *trustWorld {
	t.Helper()
	ctx := context.Background()
	env := testkit.New(t)
	acme := env.OrgRoles("acme")
	org := acme.Org.ID
	w := &trustWorld{env: env, acme: acme, ai: acme.TargetAI, clients: map[string]*testkit.Client{}}
	w.lead = env.NewAIIn(acme.Manager, org, "lead")
	w.sibling = env.NewAIIn(acme.Manager, org, "sibling")
	w.sub = env.NewAIIn(acme.Manager, org, "sub")
	env.SetCreator(w.sub, w.ai)
	_, err := env.App.Orgs.SetPosition(ctx, acme.Admin.User.ID, org, w.lead, acme.AdminPosition)
	require.NoError(t, err)

	// Member is the admin of globex, at its root position, and ai is a worker there: Member manages ai in
	// globex, which is not ai's home.
	globex, err := env.App.Orgs.Create(ctx, acme.Member.User.ID, "globex")
	require.NoError(t, err)
	worker := env.AddPosition(globex.ID, "Worker", globex.PositionID)
	env.AddMember(globex.ID, w.ai, orgs.PermissionMember, worker)

	w.ops = groupIn(t, env, acme.Manager, org, "ops")
	add := func(kind dto.MemberKind, id string) dto.RoomMember {
		return testkit.Decode[dto.RoomMember](acme.Manager.Post("/api/v1/rooms/"+w.ops.ID+"/members",
			dto.AddMemberRequest{Kind: kind, MemberID: id}).RequireStatus(http.StatusCreated))
	}
	for _, u := range []*testkit.Client{acme.Admin, acme.Peer, acme.Member} {
		add(dto.MemberKindUser, u.User.ID)
	}
	for _, id := range []string{w.lead, w.sibling, w.sub} {
		add(dto.MemberKindAgent, id)
	}
	w.aiMember = add(dto.MemberKindAgent, w.ai).ID
	return w
}

// asAI is a client acting as the AI of the folder name, which Manager hosts.
func (w *trustWorld) asAI(name string) *testkit.Client {
	if c, ok := w.clients[name]; ok {
		return c
	}
	c := w.acme.Manager.Token(dto.TokenKindAgent, name).AsAgent(name)
	w.clients[name] = c
	return c
}

func (w *trustWorld) dial(t *testing.T) *testkit.AgentConn {
	t.Helper()
	conn, resp := w.acme.Manager.Token(dto.TokenKindAgent, "session").DialAgent(trustAI)
	require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
	return conn
}

func (w *trustWorld) setDelivery(t *testing.T, mode dto.DeliveryMode) {
	t.Helper()
	w.acme.Manager.Patch("/api/v1/rooms/"+w.ops.ID+"/members/"+w.aiMember, dto.UpdateMemberRequest{Delivery: &mode}).RequireStatus(http.StatusOK)
}

// userPost posts as a user, over the REST API, and returns the message id.
func (w *trustWorld) userPost(t *testing.T, c *testkit.Client, text string) string {
	t.Helper()
	return testkit.Decode[dto.Message](c.Post("/api/v1/rooms/"+w.ops.ID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated)).ID
}

// aiPost posts as an AI with its host's agent token, over the agent API, and returns the message id.
func (w *trustWorld) aiPost(t *testing.T, name, text string) string {
	t.Helper()
	return testkit.Decode[dto.Message](w.asAI(name).Post(agentRoomPath(w.ops.ID, "/messages"), dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated)).ID
}

// plant stores a message of a user in ops without any post check, as history left in a room by someone
// who could post there once, and announces it as a post does.
func (w *trustWorld) plant(t *testing.T, userID, text string) string {
	t.Helper()
	ctx := context.Background()
	id, now := store.NewID(), time.Now().UnixMilli()
	var seq int64
	require.NoError(t, w.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		r, err := q.NextRoomSeq(ctx, db.NextRoomSeqParams{UpdatedAt: now, ID: w.ops.ID})
		if err != nil {
			return err
		}
		seq = r.NextSeq - 1
		return q.CreateMessage(ctx, db.CreateMessageParams{
			ID: id, RoomID: w.ops.ID, Seq: seq, AuthorKind: string(dto.AuthorKindUser), AuthorID: userID,
			AuthorUserID: userID, AuthorIid: userID, Kind: string(dto.MessageKindText), Text: text, RecipientsJson: "[]", CreatedAt: now,
		})
	}))
	w.env.App.Bus.Publish(events.MessageCreated{RoomID: w.ops.ID, MessageID: id, Seq: seq})
	return id
}

// trustAuthor is one author of a message to ai and whether ai trusts it. iid is the author's
// intelligence id.
type trustAuthor struct {
	name    string
	iid     string
	trusted bool
	post    func(t *testing.T, text string) string
}

// authors are the managers of ai, who are trusted, and the identities around it who are not.
func (w *trustWorld) authors() []trustAuthor {
	a := w.acme
	user := func(c *testkit.Client) func(*testing.T, string) string {
		return func(t *testing.T, text string) string { return w.userPost(t, c, text) }
	}
	ai := func(name string) func(*testing.T, string) string {
		return func(t *testing.T, text string) string { return w.aiPost(t, name, text) }
	}
	return []trustAuthor{
		{"the creator of the AI", a.Manager.User.ID, true, user(a.Manager)},
		{"a position above the creator's", a.Admin.User.ID, true, user(a.Admin)},
		{"an AI at a position above the creator's", w.lead, true, ai("lead")},
		{"a sibling AI of the same user", w.sibling, false, ai("sibling")},
		{"a peer", a.Peer.User.ID, false, user(a.Peer)},
		{"a subordinate", w.sub, false, ai("sub")},
		{"an outsider", a.Outsider.User.ID, false, func(t *testing.T, text string) string { return w.plant(t, a.Outsider.User.ID, text) }},
		{"a manager in a shared organization that is not the home", a.Member.User.ID, false, user(a.Member)},
	}
}

// delivered says whether the delivery mode lets an author's message through.
func delivered(mode dto.DeliveryMode, a trustAuthor) bool {
	return mode != dto.DeliveryModeTrusted || a.trusted
}

var trustModes = []dto.DeliveryMode{dto.DeliveryModeAll, dto.DeliveryModeTrusted}

// POS-10, live push: each message arrives with the trust of its author; in trusted mode only a
// manager's does, and nothing else is pushed.
func TestLivePushIsTrustedOnlyWhenTheAuthorManagesTheAI(t *testing.T) {
	for _, mode := range trustModes {
		t.Run(string(mode), func(t *testing.T) {
			w := newTrustWorld(t)
			w.setDelivery(t, mode)
			conn := w.dial(t)
			conn.Welcome(wsWait)
			for _, a := range w.authors() {
				id := a.post(t, a.name)
				if !delivered(mode, a) {
					continue
				}
				got := conn.Message(wsWait)
				require.Equal(t, id, got.Message.ID, a.name)
				require.Equal(t, a.trusted, got.Trusted, a.name)
			}
			conn.ExpectNone(300 * time.Millisecond)
		})
	}
}

// POS-10, backlog: the messages posted while the AI was away arrive with the same flags when it connects.
func TestBacklogIsTrustedOnlyWhenTheAuthorManagesTheAI(t *testing.T) {
	for _, mode := range trustModes {
		t.Run(string(mode), func(t *testing.T) {
			w := newTrustWorld(t)
			w.setDelivery(t, mode)
			type want struct {
				id, name string
				trusted  bool
			}
			var wants []want
			for _, a := range w.authors() {
				id := a.post(t, a.name)
				if delivered(mode, a) {
					wants = append(wants, want{id, a.name, a.trusted})
				}
			}
			conn := w.dial(t)
			conn.Welcome(wsWait)
			for _, e := range wants {
				got := conn.Message(wsWait)
				require.Equal(t, e.id, got.Message.ID, e.name)
				require.Equal(t, e.trusted, got.Trusted, e.name)
			}
			conn.ExpectNone(300 * time.Millisecond)
		})
	}
}

// POS-10, history: the history tool flags every message of the room, whatever the delivery mode.
func TestHistoryIsTrustedOnlyWhenTheAuthorManagesTheAI(t *testing.T) {
	w := newTrustWorld(t)
	want := map[string]bool{}
	for _, a := range w.authors() {
		want[a.post(t, a.name)] = a.trusted
	}
	page := testkit.Decode[dto.Page[dto.AgentMessage]](w.asAI(trustAI).Get(agentRoomPath(w.ops.ID, "/messages?limit=100")).RequireStatus(http.StatusOK))
	require.Len(t, page.Items, len(want))
	for _, m := range page.Items {
		trusted, known := want[m.Message.ID]
		require.True(t, known, "unexpected message %q", m.Message.Text)
		require.Equal(t, trusted, m.Trusted, m.Message.Text)
	}
}

// POS-10 is decided when the message is delivered, so a promotion or a demotion takes effect on the
// next message: the peer is untrusted, then trusted once it holds a position above the AI's creator,
// then untrusted again.
func TestTrustFollowsThePositionsAtDeliveryTime(t *testing.T) {
	ctx := context.Background()
	w := newTrustWorld(t)
	org, admin, peer := w.acme.Org.ID, w.acme.Admin.User.ID, w.acme.Peer
	conn := w.dial(t)
	conn.Welcome(wsWait)
	next := func(text string) bool {
		t.Helper()
		id := w.userPost(t, peer, text)
		got := conn.Message(wsWait)
		require.Equal(t, id, got.Message.ID)
		return got.Trusted
	}

	require.False(t, next("as a peer"))
	_, err := w.env.App.Orgs.SetPosition(ctx, admin, org, peer.User.ID, w.acme.AdminPosition)
	require.NoError(t, err)
	require.True(t, next("after the promotion"), "the root position is above the creator's")
	_, err = w.env.App.Orgs.SetPosition(ctx, admin, org, peer.User.ID, "")
	require.NoError(t, err)
	require.False(t, next("after the demotion"))
}

// POS-10: an author who has left the organization manages nothing there, so what it wrote earlier is
// untrusted when it is read later, in the history and in the backlog alike.
func TestAFormerManagerIsNotTrusted(t *testing.T) {
	ctx := context.Background()
	w := newTrustWorld(t)
	posted := w.userPost(t, w.acme.Manager, "before leaving")
	read := func() dto.AgentMessage {
		t.Helper()
		items := testkit.Decode[dto.Page[dto.AgentMessage]](w.asAI(trustAI).Get(agentRoomPath(w.ops.ID, "/messages")).RequireStatus(http.StatusOK)).Items
		require.Len(t, items, 1)
		require.Equal(t, posted, items[0].Message.ID)
		return items[0]
	}
	require.True(t, read().Trusted, "Manager manages the AI while it is in acme")

	require.NoError(t, w.env.App.Orgs.Leave(ctx, w.acme.Manager.User.ID, w.acme.Org.ID))
	require.False(t, read().Trusted, "the same message, after its author left")
	conn := w.dial(t)
	conn.Welcome(wsWait)
	got := conn.Message(wsWait)
	require.Equal(t, posted, got.Message.ID)
	require.False(t, got.Trusted, "and in the backlog")
}

// POS-10 cost (AD-017): a message pushed to 20 AIs whose homes are three organizations reads three
// charts, one per organization, not one per recipient.
func TestADispatchLoadsEachHomeChartOnce(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	alice := env.NewUser("alice")
	homes := []string{alice.PersonalOrg().ID}
	for _, name := range []string{"globex", "initech"} {
		created, err := env.App.Orgs.Create(ctx, alice.User.ID, name)
		require.NoError(t, err)
		homes = append(homes, created.ID)
	}
	room := createRoom(alice, "all-hands")
	token := alice.Token(dto.TokenKindAgent, "laptop")
	var conns []*testkit.AgentConn
	for i := range 20 {
		name := fmt.Sprintf("bot%02d", i)
		id := env.NewAIIn(alice, homes[i%len(homes)], name)
		if home := homes[i%len(homes)]; home != homes[0] {
			env.AddMember(homes[0], id, orgs.PermissionMember, "") // the group is alice's own, and its members belong to its organization
		}
		alice.Post("/api/v1/rooms/"+room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: id}).RequireStatus(http.StatusCreated)
		conn, resp := token.DialAgent(name)
		require.NotNil(t, conn, "upgrade refused: %d %s", resp.Status, resp.Body)
		conn.Welcome(wsWait)
		conns = append(conns, conn)
	}

	// Planted, so that the only charts read are the dispatcher's.
	before := env.App.Authz.Loads()
	w := &trustWorld{env: env, ops: room}
	id := w.plant(t, alice.User.ID, "to everyone")
	for i, conn := range conns {
		got := conn.Message(wsWait)
		require.Equal(t, id, got.Message.ID, "AI %d", i)
		require.True(t, got.Trusted, "alice created AI %d, in an organization she belongs to", i)
	}
	// One trust session per dispatch pass reads each home chart once. A recipient whose socket was still
	// settling can be served by a second pass, which reads its chart once more; what AD-017 rules out is
	// one read per recipient (20).
	loads := env.App.Authz.Loads() - before
	require.GreaterOrEqual(t, loads, int64(3), "each of the 3 home organizations is read")
	require.LessOrEqual(t, loads, int64(6), "20 recipients, 3 home organizations: at most two passes, never one read per recipient")
}
