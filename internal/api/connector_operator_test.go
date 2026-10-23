package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/testkit"
)

// noOperatorDetail is the status detail of a connection nobody operates (CID-02).
const noOperatorDetail = "choose the AI that operates this connection"

// operatorFixture is alice's fake connection "wa" (created without an operator), shared for use
// with bob, with alice's AIs Atlas and Notes (Atlas shared with bob and dave) and bob's AI Bobby;
// dave can use Atlas but not the connection; carol and the admin can use nothing.
type operatorFixture struct {
	env                      *testkit.Env
	fake                     *connectortest.Fake
	admin, alice, bob, carol *testkit.Client
	dave                     *testkit.Client
	conn                     dto.Connector
	atlas, notes, bobby      string
	aliceRef, atlasRef       *dto.IntelligenceRef
}

func newOperatorFixture(t *testing.T) *operatorFixture {
	t.Helper()
	env, fake := fakeConnectorEnv(t)
	f := &operatorFixture{
		env: env, fake: fake, admin: env.SetupAdmin(), alice: env.NewUser("alice"), bob: env.NewUser("bob"),
		carol: env.NewUser("carol"), dave: env.NewUser("dave"),
	}
	f.conn = createFakeIdle(f.alice, "wa", true)
	shareConnector(f.alice, f.conn.ID, f.bob.User.ID)
	f.atlas = env.NewAI(f.alice, "Atlas")
	f.notes = env.NewAI(f.alice, "Notes")
	f.bobby = env.NewAI(f.bob, "Bobby")
	shareAgent(f.alice, f.atlas, f.dave.User.ID, f.bob.User.ID)
	ref := func(c *testkit.Client, id string) *dto.IntelligenceRef {
		r := profileOf(t, c, id).IntelligenceRef
		return &r
	}
	f.aliceRef, f.atlasRef = ref(f.alice, f.alice.User.ID), ref(f.alice, f.atlas)
	return f
}

// operate makes Atlas the operator and returns the running instance once its Run started.
func (f *operatorFixture) operate(t *testing.T) *connectortest.Instance {
	t.Helper()
	f.alice.OperateConnector(f.conn.ID, f.atlas)
	inst := f.fake.Instance(t, f.conn.ID)
	inst.WaitRun(t)
	return inst
}

func (f *operatorFixture) connector(c *testkit.Client) dto.Connector {
	f.env.T.Helper()
	return testkit.Decode[dto.Connector](c.Get("/api/v1/connectors/" + f.conn.ID).RequireStatus(http.StatusOK))
}

func (f *operatorFixture) setOperator(c *testkit.Client, id string) *testkit.Response {
	f.env.T.Helper()
	return c.Patch("/api/v1/connectors/"+f.conn.ID, dto.UpdateConnectorRequest{OperatorID: &id})
}

// operatorAudits returns the connector.operator entries, newest first.
func (f *operatorFixture) operatorAudits() []dto.AuditEntry {
	f.env.T.Helper()
	var out []dto.AuditEntry
	for _, e := range auditPage(f.admin, "").Items {
		if e.Action == "connector.operator" {
			out = append(out, e)
		}
	}
	return out
}

// CID-02: a new connection has no operator: views carry none, nothing runs, and the status says
// what to do. The users of the connection see the same.
func TestNewConnectionHasNoOperatorAndDoesNotRun(t *testing.T) {
	f := newOperatorFixture(t)

	require.Nil(t, f.conn.Operator, "the create answer has no operator")
	require.Equal(t, dto.ConnectorStatusError, f.conn.Status)
	require.Equal(t, "choose the AI that operates this connection", f.conn.LastError)
	require.Nil(t, f.fake.Lookup(f.conn.ID), "a connection nobody operates is not run")

	for name, c := range map[string]*testkit.Client{"owner": f.alice, "use grantee": f.bob} {
		got := f.connector(c)
		require.Nil(t, got.Operator, name)
		require.Equal(t, dto.ConnectorStatusError, got.Status, name)
		require.Equal(t, noOperatorDetail, got.LastError, name)
		list := testkit.Decode[dto.List[dto.Connector]](c.Get("/api/v1/connectors").RequireStatus(http.StatusOK)).Items
		require.Len(t, list, 1, name)
		require.Nil(t, list[0].Operator, "%s: the list carries none either", name)
		require.Equal(t, noOperatorDetail, list[0].LastError, name)
	}
}

// CID-02: the idle status reaches live sockets; choosing the operator starts the connection and
// the status moves on. A disabled connection keeps its stopped status.
func TestChoosingTheOperatorStartsTheConnection(t *testing.T) {
	env, fake := fakeConnectorEnv(t)
	alice := env.NewUser("alice")
	atlas := env.NewAI(alice, "Atlas")
	live, _ := alice.DialLive("")

	c := createFakeIdle(alice, "ops", true)
	var st dto.ConnectorStatusData
	live.Expect(dto.LiveEventTypeConnectorStatus, &st, connectortest.Timeout)
	require.Equal(t, dto.ConnectorStatusData{ConnectorID: c.ID, Status: dto.ConnectorStatusError, LastError: noOperatorDetail}, st)
	require.Nil(t, fake.Lookup(c.ID))

	got := alice.OperateConnector(c.ID, atlas)
	require.Equal(t, atlas, got.Operator.ID)
	inst := fake.Instance(t, c.ID)
	inst.WaitRun(t)
	st = dto.ConnectorStatusData{} // an omitted last_error would keep the old one
	live.Expect(dto.LiveEventTypeConnectorStatus, &st, connectortest.Timeout)
	require.Equal(t, dto.ConnectorStatusData{ConnectorID: c.ID, Status: dto.ConnectorStatusConnecting}, st, "the detail is gone")
	inst.Connected()
	st = dto.ConnectorStatusData{}
	live.Expect(dto.LiveEventTypeConnectorStatus, &st, connectortest.Timeout)
	require.Equal(t, dto.ConnectorStatusConnected, st.Status)
	back := testkit.Decode[dto.Connector](alice.Get("/api/v1/connectors/" + c.ID).RequireStatus(http.StatusOK))
	require.Equal(t, dto.ConnectorStatusConnected, back.Status)
	require.Empty(t, back.LastError)

	// A connection that is disabled is stopped, with or without an operator.
	off := false
	stopped := testkit.Decode[dto.Connector](alice.Patch("/api/v1/connectors/"+c.ID, dto.UpdateConnectorRequest{Enabled: &off}).RequireStatus(http.StatusOK))
	require.Equal(t, dto.ConnectorStatusStopped, stopped.Status)
	parked := createFakeIdle(alice, "parked", false)
	require.Equal(t, dto.ConnectorStatusStopped, parked.Status, "disabled: stopped, not 'choose the AI'")
	require.Empty(t, parked.LastError)
	alice.OperateConnector(parked.ID, atlas)
	require.Nil(t, fake.Lookup(parked.ID), "choosing the operator of a disabled connection starts nothing")
}

// CID-01: the owner picks one of their own AIs. Each real change is audited with the operator
// before ("" when there was none) and after; a repeat is not audited; other edits leave the
// operator alone.
func TestOwnerSetsTheOperator(t *testing.T) {
	f := newOperatorFixture(t)

	got := testkit.Decode[dto.Connector](f.setOperator(f.alice, f.atlas).RequireStatus(http.StatusOK))
	require.Equal(t, f.atlasRef, got.Operator, "the answer is the stored connector")
	require.Equal(t, dto.IntelligenceRoleAI, got.Operator.Role)
	require.Equal(t, f.atlasRef, f.connector(f.alice).Operator)
	require.Equal(t, f.atlasRef, f.connector(f.bob).Operator, "a use grantee sees it too")
	list := testkit.Decode[dto.List[dto.Connector]](f.bob.Get("/api/v1/connectors").RequireStatus(http.StatusOK)).Items
	require.Equal(t, f.atlasRef, list[0].Operator, "the list carries it")

	audits := f.operatorAudits()
	require.Len(t, audits, 1)
	e := audits[0]
	require.Equal(t, []any{f.alice.User.ID, "connector", f.conn.ID}, []any{e.Actor.ID, e.TargetKind, e.TargetID})
	require.Equal(t, "", e.Detail["from"], "nobody operated it")
	require.Equal(t, f.atlas, e.Detail["to"])
	require.Equal(t, "wa", e.Detail["name"])

	f.setOperator(f.alice, f.atlas).RequireStatus(http.StatusOK)
	require.Len(t, f.operatorAudits(), 1, "setting the operator it already has changes nothing")

	// Another of the owner's AIs takes over.
	notesRef := profileOf(t, f.alice, f.notes).IntelligenceRef
	got = testkit.Decode[dto.Connector](f.setOperator(f.alice, f.notes).RequireStatus(http.StatusOK))
	require.Equal(t, &notesRef, got.Operator)
	audits = f.operatorAudits()
	require.Len(t, audits, 2)
	require.Equal(t, []any{f.atlas, f.notes}, []any{audits[0].Detail["from"], audits[0].Detail["to"]})

	// Other edits leave the operator alone.
	name := "renamed"
	f.alice.Patch("/api/v1/connectors/"+f.conn.ID, dto.UpdateConnectorRequest{Name: &name}).RequireStatus(http.StatusOK)
	require.Equal(t, &notesRef, f.connector(f.alice).Operator)
	require.Len(t, f.operatorAudits(), 2)
}

// CID-01 (L-017, L-018): the operator is an AI the owner owns. The owner's own id, an empty id,
// another user's AI, a person, a user and an id nobody has are each a 422 on operator_id that
// changes nothing, also next to valid edits; every refusal isolates its own guard.
func TestOperatorMustBeAnAIOfTheOwner(t *testing.T) {
	f := newOperatorFixture(t)
	person := seedPerson(t, f.env, f.conn.ID, "5511999990000", "Pat")
	shareAgent(f.bob, f.bobby, f.alice.User.ID) // alice may use Bobby; she does not own it

	for _, c := range []struct{ name, id string }{
		{"the owner's own id", f.alice.User.ID},
		{"an empty id", ""},
		{"a blank id", "  "},
		{"an AI of another owner, shared with the owner", f.bobby},
		{"another user", f.bob.User.ID},
		{"a person of the connection", person},
		{"an id nobody has", "no-such-intelligence"},
	} {
		renamed := "renamed by a request that is refused"
		e := f.alice.Patch("/api/v1/connectors/"+f.conn.ID, dto.UpdateConnectorRequest{OperatorID: &c.id, Name: &renamed}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Equal(t, map[string]string{"operator_id": "must be an AI you own"}, e.Fields, c.name)
	}
	got := f.connector(f.alice)
	require.Equal(t, "wa", got.Name, "a refused request changes no other field")
	require.Nil(t, got.Operator)
	require.Empty(t, f.operatorAudits())

	// Once an AI operates it, the owner cannot take the operator away by naming themself or nobody.
	f.setOperator(f.alice, f.atlas).RequireStatus(http.StatusOK)
	f.setOperator(f.alice, f.alice.User.ID).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	f.setOperator(f.alice, "").RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
	require.Equal(t, f.atlasRef, f.connector(f.alice).Operator)
	require.Len(t, f.operatorAudits(), 1)
}

// CID-01 (authorization matrix): only the owner sets the operator. The use grantee may not
// manage the connection (403); a user who cannot see it and the admin get 404 (no implicit
// access); anonymous callers get 401; AI tokens, the connection's own operator included, are not
// users and get 403. Every refusal leaves the operator unset.
func TestOperatorAuthorizationMatrix(t *testing.T) {
	f := newOperatorFixture(t)
	f.operate(t) // Atlas operates it, so a refusal that changed it would show

	other := f.notes
	req := dto.UpdateConnectorRequest{OperatorID: &other}
	patch := func(c *testkit.Client) *testkit.Response { return c.Patch("/api/v1/connectors/"+f.conn.ID, req) }
	patch(f.bob).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden) // use grantee
	patch(f.carol).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound) // stranger
	patch(f.dave).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)  // may use Atlas, not the connection
	patch(f.admin).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound) // admins get no implicit access
	patch(f.env.Anon()).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	ownerToken := f.alice.Token(dto.TokenKindAgent, "laptop")
	patch(ownerToken).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)                                                 // an AI token of the owner
	patch(ownerToken.AsAgent("Atlas")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)                                // the operator itself
	patch(f.bob.Token(dto.TokenKindAgent, "laptop").AsAgent("Bobby")).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden) // an AI of another user

	require.Equal(t, f.atlasRef, f.connector(f.alice).Operator, "no refusal changed the operator")
	require.Len(t, f.operatorAudits(), 1)
	// The owner can.
	f.setOperator(f.alice, f.notes).RequireStatus(http.StatusOK)
}

// CID-24 (the first half, T22 completes it): when the operator AI is deleted the connection has
// no operator again.
func TestDeletingTheOperatorLeavesTheConnectionWithoutOne(t *testing.T) {
	f := newOperatorFixture(t)
	f.operate(t)

	f.alice.Delete("/api/v1/agents/" + f.atlas).RequireStatus(http.StatusNoContent)

	require.Nil(t, f.connector(f.alice).Operator)
	require.Nil(t, testkit.Decode[dto.List[dto.Connector]](f.alice.Get("/api/v1/connectors").RequireStatus(http.StatusOK)).Items[0].Operator)
}

// ACC-07: how the platform names the account is stored as the connector's account label
// (trimmed, without control characters, at most 100 characters); an empty label clears it.
func TestConnectorReportsItsAccountLabel(t *testing.T) {
	f := newOperatorFixture(t)
	inst := f.operate(t)
	require.Empty(t, f.connector(f.alice).AccountLabel)

	inst.Account("  @atlas_bot \n")
	require.Equal(t, "@atlas_bot", f.connector(f.alice).AccountLabel)
	require.Equal(t, "@atlas_bot", f.connector(f.bob).AccountLabel, "a use grantee sees it")
	require.Equal(t, "@atlas_bot", testkit.Decode[dto.List[dto.Connector]](f.alice.Get("/api/v1/connectors").RequireStatus(http.StatusOK)).Items[0].AccountLabel)

	long := ""
	for range 101 {
		long += "é"
	}
	inst.Account(long)
	cut := f.connector(f.alice).AccountLabel
	require.Equal(t, long[:200], cut, "cut to 100 characters, not bytes")

	inst.Account("")
	require.Empty(t, f.connector(f.alice).AccountLabel)
}

// ACC-07, AD-015: a connection is the app of its operator AI: "<Platform>: <account label>"
// ("<Platform>: <connector name>" until the platform names the account) on the AI's page, for the
// users of the connection. Nobody's page lists a connection nobody operates, the owner's included;
// a user who can only see the AI does not see the app.
func TestOperatorProfileListsTheConnectionsApp(t *testing.T) {
	f := newOperatorFixture(t)
	app := func(label string) dto.App {
		return dto.App{Kind: connectortest.KindName, Label: label, ConnectorID: f.conn.ID}
	}
	web := dto.App{Kind: "web", Label: "Web app"}
	claude := dto.App{Kind: "claude", Label: "Claude Code"}

	require.Equal(t, []dto.App{web}, profileOf(t, f.alice, f.alice.User.ID).Apps, "the owner does not operate it")
	require.Equal(t, []dto.App{claude}, profileOf(t, f.alice, f.atlas).Apps, "and nobody else does yet")

	inst := f.operate(t)
	require.Equal(t, []dto.App{claude, app("Fake: wa")}, profileOf(t, f.alice, f.atlas).Apps, "no account label yet")
	inst.Account("@atlas_bot")
	require.Equal(t, []dto.App{claude, app("Fake: @atlas_bot")}, profileOf(t, f.alice, f.atlas).Apps)
	require.Equal(t, []dto.App{claude, app("Fake: @atlas_bot")}, profileOf(t, f.bob, f.atlas).Apps, "bob may use the connection")
	require.Equal(t, []dto.App{claude}, profileOf(t, f.dave, f.atlas).Apps, "dave may use Atlas, not the connection")
	require.Equal(t, []dto.App{web}, profileOf(t, f.alice, f.alice.User.ID).Apps, "it is Atlas's app, not the owner's")

	f.setOperator(f.alice, f.notes).RequireStatus(http.StatusOK)
	require.Equal(t, []dto.App{claude}, profileOf(t, f.alice, f.atlas).Apps, "no longer Atlas's")
	require.Equal(t, []dto.App{claude, app("Fake: @atlas_bot")}, profileOf(t, f.alice, f.notes).Apps)
}
