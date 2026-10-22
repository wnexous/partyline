package connectors_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/testkit"
)

// operatedOutbound is the outbound env with a second AI, Atlas, that can operate the connector,
// and bob, a poster of the room.
type operatedOutbound struct {
	*outboundEnv
	atlasID string
	atlas   *testkit.Client // Atlas's REST client
	bob     *testkit.Client
}

func newOperatedOutbound(t *testing.T) *operatedOutbound {
	t.Helper()
	e := newOutboundEnv(t)
	o := &operatedOutbound{outboundEnv: e, atlasID: e.env.NewAI(e.alice, "atlas"), bob: e.env.NewUser("bob")}
	o.atlas = e.alice.Token(dto.TokenKindAgent, "desk").AsAgent("atlas")
	e.alice.Post("/api/v1/rooms/"+e.room.ID+"/members", dto.AddMemberRequest{Kind: dto.MemberKindAgent, MemberID: o.atlasID}).RequireStatus(http.StatusCreated)
	e.alice.Put("/api/v1/rooms/"+e.room.ID+"/sharing", dto.Sharing{
		Mode: dto.VisibilityShared, Users: []dto.SharingUser{{UserID: o.bob.User.ID, Level: dto.LevelPost}},
	}).RequireStatus(http.StatusOK)
	return o
}

func (o *operatedOutbound) operate(t *testing.T, id string) {
	t.Helper()
	o.alice.Patch("/api/v1/connectors/"+o.conn.ID, dto.UpdateConnectorRequest{OperatorID: &id}).RequireStatus(http.StatusOK)
}

// signature posts as the poster and returns the author line the connector would put in the text.
func (o *operatedOutbound) signature(t *testing.T, post func() dto.Message) string {
	t.Helper()
	m := post()
	sent := o.inst.WaitSend(t)
	require.Equal(t, m.ID, sent.MessageID)
	return sent.Signature()
}

func (o *operatedOutbound) atlasPost() func() dto.Message {
	return func() dto.Message {
		return testkit.Decode[dto.Message](o.atlas.Post("/api/v1/agent/rooms/"+o.room.ID+"/messages", dto.PostMessageRequest{Text: "from atlas"}).RequireStatus(http.StatusCreated))
	}
}

func (o *operatedOutbound) userPost(c *testkit.Client) func() dto.Message {
	return func() dto.Message {
		return testkit.Decode[dto.Message](c.Post("/api/v1/rooms/"+o.room.ID+"/messages", dto.PostMessageRequest{Text: "from the web"}).RequireStatus(http.StatusCreated))
	}
}

// ACC-03: an AI that operates the connector writes through its own account, so its messages go
// out without an author line, whatever its signature setting says; once it is not the operator
// any more it signs again.
func TestOperatorAIWritesUnsigned(t *testing.T) {
	o := newOperatedOutbound(t)
	o.operate(t, o.atlasID)

	require.Empty(t, o.signature(t, o.atlasPost()), "its own account: no author line")
	o.patchMember(t, o.atlasID, dto.UpdateMemberRequest{SignOutbound: ptr(true), DisplayName: ptr("Atlas Prime")})
	require.Empty(t, o.signature(t, o.atlasPost()), "even with the signature on and a name set")
	require.Equal(t, "Atlas Prime", o.authorName(t, o.room.ID, o.latest(t)), "the timeline still names the author")

	o.operate(t, "")
	require.Equal(t, "Atlas Prime", o.signature(t, o.atlasPost()), "no longer the operator: signed")
	o.operate(t, o.atlasID)
	require.Empty(t, o.signature(t, o.atlasPost()))
}

// ACC-04: through a connector operated by the owner, an AI signs per its member setting (on, the
// name set for it in the room, off); and an AI that is not the operator of a connector operated
// by another AI signs too.
func TestAIThroughSomeoneElsesAccountSignsPerItsSetting(t *testing.T) {
	o := newOperatedOutbound(t) // the owner operates the connector
	require.Equal(t, "atlas", o.signature(t, o.atlasPost()), "signature on by default")

	o.patchMember(t, o.atlasID, dto.UpdateMemberRequest{DisplayName: ptr("Atlas Prime")})
	require.Equal(t, "Atlas Prime", o.signature(t, o.atlasPost()))
	o.patchMember(t, o.atlasID, dto.UpdateMemberRequest{SignOutbound: ptr(false)})
	require.Empty(t, o.signature(t, o.atlasPost()), "signature off")
	o.patchMember(t, o.atlasID, dto.UpdateMemberRequest{SignOutbound: ptr(true)})

	// helper is not the operator when Atlas is: it signs, and obeys its own setting.
	o.operate(t, o.atlasID)
	post := func() dto.Message { return o.agentPost(t, o.room.ID, "from helper") }
	require.Equal(t, "helper", o.signature(t, post))
	o.patchMember(t, o.helperID(t), dto.UpdateMemberRequest{SignOutbound: ptr(false)})
	require.Empty(t, o.signature(t, post))
}

// ACC-04, ACC-03: a user posting from the web through the connector's account is not signed
// when they operate it (the owner by default): it is their own account. Anyone else, the owner
// included while an AI operates the account, is named.
func TestUsersAreSignedUnlessTheyOperateTheAccount(t *testing.T) {
	o := newOperatedOutbound(t)

	require.Empty(t, o.signature(t, o.userPost(o.alice)), "the owner operates the account")
	require.Equal(t, "bob", o.signature(t, o.userPost(o.bob)), "a poster who is not the operator is named")

	o.operate(t, o.atlasID)
	require.Equal(t, "alice", o.signature(t, o.userPost(o.alice)), "an AI operates it: the owner is named")
	require.Equal(t, "bob", o.signature(t, o.userPost(o.bob)))
	o.operate(t, "")
	require.Empty(t, o.signature(t, o.userPost(o.alice)))
}

// latest returns the id of the newest message of the room.
func (o *operatedOutbound) latest(t *testing.T) string {
	t.Helper()
	return o.messages(t, o.room.ID)[0].ID
}

func (o *operatedOutbound) helperID(t *testing.T) string {
	t.Helper()
	for _, m := range testkit.Decode[dto.List[dto.RoomMember]](o.alice.Get("/api/v1/rooms/" + o.room.ID + "/members").RequireStatus(http.StatusOK)).Items {
		if m.Kind == dto.MemberKindAgent && m.MemberID != o.atlasID {
			return m.MemberID
		}
	}
	t.Fatal("no helper member")
	return ""
}

// patchMember changes the settings of the room's agent member with the given agent id.
func (o *operatedOutbound) patchMember(t *testing.T, agentID string, req dto.UpdateMemberRequest) {
	t.Helper()
	for _, m := range testkit.Decode[dto.List[dto.RoomMember]](o.alice.Get("/api/v1/rooms/" + o.room.ID + "/members").RequireStatus(http.StatusOK)).Items {
		if m.Kind == dto.MemberKindAgent && m.MemberID == agentID {
			o.alice.Patch("/api/v1/rooms/"+o.room.ID+"/members/"+m.ID, req).RequireStatus(http.StatusOK)
			return
		}
	}
	t.Fatalf("agent %s is not a member", agentID)
}
