package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/testkit"
)

// operatorFixture is alice's fake connector "wa", shared for use with bob, with alice's AI Atlas
// (shared with bob and dave) and bob's AI Bobby; dave can use Atlas but not the connector; carol
// and the admin can use nothing.
type operatorFixture struct {
	env                          *testkit.Env
	admin, alice, bob, carol     *testkit.Client
	dave                         *testkit.Client
	conn                         dto.Connector
	inst                         *connectortest.Instance
	atlas, bobby                 string
	aliceRef, atlasRef, bobbyRef *dto.IntelligenceRef
}

func newOperatorFixture(t *testing.T) *operatorFixture {
	t.Helper()
	env, fake := fakeConnectorEnv(t)
	f := &operatorFixture{
		env: env, admin: env.SetupAdmin(), alice: env.NewUser("alice"), bob: env.NewUser("bob"),
		carol: env.NewUser("carol"), dave: env.NewUser("dave"),
	}
	f.conn = createFake(f.alice, "wa", true)
	shareConnector(f.alice, f.conn.ID, f.bob.User.ID)
	f.inst = fake.Instance(t, f.conn.ID)
	f.inst.WaitRun(t)
	f.atlas = env.NewAI(f.alice, "Atlas")
	f.bobby = env.NewAI(f.bob, "Bobby")
	shareAgent(f.alice, f.atlas, f.dave.User.ID, f.bob.User.ID)
	ref := func(c *testkit.Client, id string) *dto.IntelligenceRef {
		r := profileOf(t, c, id).IntelligenceRef
		return &r
	}
	f.aliceRef, f.atlasRef, f.bobbyRef = ref(f.alice, f.alice.User.ID), ref(f.alice, f.atlas), ref(f.bob, f.bobby)
	return f
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

// ACC-01: a connector's operator is its owner until the owner picks one of their AIs; every
// view of the connector says who it is.
func TestConnectorOperatorDefaultsToTheOwner(t *testing.T) {
	f := newOperatorFixture(t)

	got := f.connector(f.alice)
	require.Equal(t, f.alice.User.ID, got.Operator.ID)
	require.Equal(t, []any{dto.IntelligenceRoleUser, dto.IntelligenceKindHuman, "alice"}, []any{got.Operator.Role, got.Operator.Kind, got.Operator.Handle})
	require.Empty(t, got.AccountLabel)
	require.Equal(t, got.Operator, f.connector(f.bob).Operator, "a use grantee sees it too")

	list := testkit.Decode[dto.List[dto.Connector]](f.bob.Get("/api/v1/connectors").RequireStatus(http.StatusOK)).Items
	require.Len(t, list, 1)
	require.Equal(t, f.alice.User.ID, list[0].Operator.ID, "the list carries it")
}

// ACC-01: the owner picks one of their own AIs, or themself again (an empty id, or their own
// id); each real change is audited with the operator before and after, and a repeat is not.
func TestOwnerSetsTheOperator(t *testing.T) {
	f := newOperatorFixture(t)

	got := testkit.Decode[dto.Connector](f.setOperator(f.alice, f.atlas).RequireStatus(http.StatusOK))
	require.Equal(t, f.atlasRef, got.Operator, "the answer is the stored connector")
	require.Equal(t, dto.IntelligenceRoleAI, got.Operator.Role)
	require.Equal(t, f.atlasRef, f.connector(f.alice).Operator)
	require.Equal(t, f.atlasRef, f.connector(f.bob).Operator)

	audits := f.operatorAudits()
	require.Len(t, audits, 1)
	e := audits[0]
	require.Equal(t, []any{f.alice.User.ID, "connector", f.conn.ID}, []any{e.Actor.ID, e.TargetKind, e.TargetID})
	require.Equal(t, f.alice.User.ID, e.Detail["from"], "the owner was the operator")
	require.Equal(t, f.atlas, e.Detail["to"])
	require.Equal(t, "wa", e.Detail["name"])

	f.setOperator(f.alice, f.atlas).RequireStatus(http.StatusOK)
	require.Len(t, f.operatorAudits(), 1, "setting the operator it already has changes nothing")

	// Back to the owner: an empty id and the owner's own id are the same thing.
	got = testkit.Decode[dto.Connector](f.setOperator(f.alice, "").RequireStatus(http.StatusOK))
	require.Equal(t, f.aliceRef, got.Operator)
	audits = f.operatorAudits()
	require.Len(t, audits, 2)
	require.Equal(t, []any{f.atlas, f.alice.User.ID}, []any{audits[0].Detail["from"], audits[0].Detail["to"]})
	f.setOperator(f.alice, f.alice.User.ID).RequireStatus(http.StatusOK)
	f.setOperator(f.alice, "").RequireStatus(http.StatusOK)
	require.Len(t, f.operatorAudits(), 2, "the owner stays the owner")
	f.setOperator(f.alice, f.atlas).RequireStatus(http.StatusOK)
	f.setOperator(f.alice, f.alice.User.ID).RequireStatus(http.StatusOK)
	require.Equal(t, f.aliceRef, f.connector(f.alice).Operator, "the owner's own id picks the owner")
	require.Len(t, f.operatorAudits(), 4)

	// Other edits leave the operator alone.
	f.setOperator(f.alice, f.atlas).RequireStatus(http.StatusOK)
	name := "renamed"
	f.alice.Patch("/api/v1/connectors/"+f.conn.ID, dto.UpdateConnectorRequest{Name: &name}).RequireStatus(http.StatusOK)
	require.Equal(t, f.atlasRef, f.connector(f.alice).Operator)
}

// ACC-01 (L-017, L-018): only the owner sets the operator, and only to themself or an AI they
// own; every refusal has its own status and changes nothing, also next to valid edits.
func TestOperatorRefusals(t *testing.T) {
	f := newOperatorFixture(t)
	person := seedPerson(t, f.env, f.conn.ID, "5511999990000", "Pat")

	f.setOperator(f.bob, f.bobby).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden) // may use the connector, may not manage it
	f.setOperator(f.carol, f.atlas).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	f.setOperator(f.admin, f.atlas).RequireError(http.StatusNotFound, dto.ErrorCodeNotFound)
	f.env.Anon().Patch("/api/v1/connectors/"+f.conn.ID, dto.UpdateConnectorRequest{OperatorID: ptr(f.atlas)}).RequireError(http.StatusUnauthorized, dto.ErrorCodeUnauthorized)
	f.alice.Token(dto.TokenKindAgent, "laptop").Patch("/api/v1/connectors/"+f.conn.ID, dto.UpdateConnectorRequest{OperatorID: ptr(f.atlas)}).RequireError(http.StatusForbidden, dto.ErrorCodeForbidden)

	for _, c := range []struct{ name, id string }{
		{"an AI of another owner", f.bobby},
		{"another user", f.bob.User.ID},
		{"a person of the connector", person},
		{"an id nobody has", "no-such-intelligence"},
	} {
		renamed := "renamed by a request that is refused"
		e := f.alice.Patch("/api/v1/connectors/"+f.conn.ID, dto.UpdateConnectorRequest{OperatorID: &c.id, Name: &renamed}).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
		require.Contains(t, e.Fields, "operator_id", c.name)
	}
	got := f.connector(f.alice)
	require.Equal(t, "wa", got.Name, "a refused request changes no other field")
	require.Equal(t, f.aliceRef, got.Operator)
	require.Empty(t, f.operatorAudits())

	// An AI that alice may use but does not own is not hers to name: bob shares Bobby with her.
	shareAgent(f.bob, f.bobby, f.alice.User.ID)
	f.setOperator(f.alice, f.bobby).RequireError(http.StatusUnprocessableEntity, dto.ErrorCodeInvalid)
}

// ACC-01 (spec edge case): when the operator AI is deleted the connector's operator is its
// owner again.
func TestDeletingTheOperatorFallsBackToTheOwner(t *testing.T) {
	f := newOperatorFixture(t)
	f.setOperator(f.alice, f.atlas).RequireStatus(http.StatusOK)

	f.alice.Delete("/api/v1/agents/" + f.atlas).RequireStatus(http.StatusNoContent)

	require.Equal(t, f.aliceRef, f.connector(f.alice).Operator)
	require.Equal(t, f.aliceRef, testkit.Decode[dto.List[dto.Connector]](f.alice.Get("/api/v1/connectors").RequireStatus(http.StatusOK)).Items[0].Operator)
}

// ACC-07: how the platform names the account is stored as the connector's account label
// (trimmed, without control characters, at most 100 characters); an empty label clears it.
func TestConnectorReportsItsAccountLabel(t *testing.T) {
	f := newOperatorFixture(t)
	require.Empty(t, f.connector(f.alice).AccountLabel)

	f.inst.Account("  @atlas_bot \n")
	require.Equal(t, "@atlas_bot", f.connector(f.alice).AccountLabel)
	require.Equal(t, "@atlas_bot", f.connector(f.bob).AccountLabel, "a use grantee sees it")
	require.Equal(t, "@atlas_bot", testkit.Decode[dto.List[dto.Connector]](f.alice.Get("/api/v1/connectors").RequireStatus(http.StatusOK)).Items[0].AccountLabel)

	long := ""
	for range 101 {
		long += "é"
	}
	f.inst.Account(long)
	cut := f.connector(f.alice).AccountLabel
	require.Equal(t, long[:200], cut, "cut to 100 characters, not bytes")

	f.inst.Account("")
	require.Empty(t, f.connector(f.alice).AccountLabel)
}

// ACC-07: the connector's account is an app of its operator: the owner's page until an AI
// operates it, then the AI's page, as "<Platform>: <account label>" ("<Platform>: <connector
// name>" until the platform names the account). The users of the connector see it; a user who
// can only see the AI does not.
func TestOperatorProfileListsTheConnectorsApp(t *testing.T) {
	f := newOperatorFixture(t)
	app := func(label string) dto.App {
		return dto.App{Kind: connectortest.KindName, Label: label, ConnectorID: f.conn.ID}
	}
	web := dto.App{Kind: "web", Label: "Web app"}
	claude := dto.App{Kind: "claude", Label: "Claude Code"}

	require.Equal(t, []dto.App{web, app("Fake: wa")}, profileOf(t, f.alice, f.alice.User.ID).Apps, "the owner operates it; no account label yet")
	f.inst.Account("@atlas_bot")
	require.Equal(t, []dto.App{web, app("Fake: @atlas_bot")}, profileOf(t, f.alice, f.alice.User.ID).Apps)
	require.Equal(t, []dto.App{web, app("Fake: @atlas_bot")}, profileOf(t, f.bob, f.alice.User.ID).Apps, "bob may use the connector")
	require.Equal(t, []dto.App{web}, profileOf(t, f.carol, f.alice.User.ID).Apps, "carol may not")
	require.Equal(t, []dto.App{claude}, profileOf(t, f.alice, f.atlas).Apps)

	f.setOperator(f.alice, f.atlas).RequireStatus(http.StatusOK)
	require.Equal(t, []dto.App{web}, profileOf(t, f.alice, f.alice.User.ID).Apps, "it is Atlas's app now")
	require.Equal(t, []dto.App{claude, app("Fake: @atlas_bot")}, profileOf(t, f.alice, f.atlas).Apps)
	require.Equal(t, []dto.App{claude, app("Fake: @atlas_bot")}, profileOf(t, f.bob, f.atlas).Apps, "bob may use the connector")
	require.Equal(t, []dto.App{claude}, profileOf(t, f.dave, f.atlas).Apps, "dave may use Atlas, not the connector")

	f.setOperator(f.alice, "").RequireStatus(http.StatusOK)
	require.Equal(t, []dto.App{claude}, profileOf(t, f.alice, f.atlas).Apps)
	require.Equal(t, []dto.App{web, app("Fake: @atlas_bot")}, profileOf(t, f.alice, f.alice.User.ID).Apps)
}
