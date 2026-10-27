package webpush_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/webpush"
)

// alertWhatsAppDetail is the stored, already-redacted detail of a WhatsApp number that dropped
// (CID-28); the alert only ever sends a fixed reason (ALERT-08).
const alertWhatsAppDetail = "the WhatsApp number is not connected (Reconnecting); reconnect it in evolution-go"

// alertFixture is one organization with an admin and its connection, the people responsible for it
// and their devices (ALERT-02). Every test builds its own environment, so a second fixture in one
// environment is not supported.
type alertFixture struct {
	t     *testing.T
	env   *testkit.Env
	org   string
	admin *testkit.Client
}

// newAlertFixture creates the organization "acme" with alice as its admin.
func newAlertFixture(t *testing.T, env *testkit.Env) *alertFixture {
	t.Helper()
	admin := env.NewUser("alice")
	created, err := env.App.Orgs.Create(context.Background(), admin.User.ID, "acme")
	require.NoError(t, err)
	return &alertFixture{t: t, env: env, org: created.ID, admin: admin}
}

// member adds an account of the organization with the permission.
func (f *alertFixture) member(name string, permission orgs.Permission) *testkit.Client {
	f.t.Helper()
	c := f.env.NewUser(name)
	f.env.AddMember(f.org, c.User.ID, permission, "")
	return c
}

// host adds an account outside the organization and returns it with the AI it hosts; that AI
// operates the connections of the fixture.
func (f *alertFixture) host(name string) (*testkit.Client, string) {
	f.t.Helper()
	c := f.env.NewUser(name)
	return c, f.env.NewAI(c, "ops-ai")
}

// device registers one browser of the account and returns the stored subscription.
func (f *alertFixture) device(userID, endpoint string) db.PushSubscription {
	f.t.Helper()
	return subscribe(f.t, f.env, userID, endpoint)
}

// connector stores the organization's connection with its operator and stored status, so Seed
// marks it as seen healthy unless the status says otherwise (ALERT-10, ALERT-11).
func (f *alertFixture) connector(id, name, operator, status string) {
	f.t.Helper()
	f.connectorOfKind(id, name, "whatsapp", operator, status)
}

// connectorOfKind stores the connection with an arbitrary kind; the app's registry is what decides
// whether it can start.
func (f *alertFixture) connectorOfKind(id, name, kind, operator, status string) {
	f.t.Helper()
	ctx := context.Background()
	now := time.Now().UnixMilli()
	require.NoError(f.t, f.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateConnector(ctx, db.CreateConnectorParams{
			ID: id, OwnerID: f.admin.User.ID, OrgID: sql.NullString{String: f.org, Valid: true},
			Kind: kind, Name: name, ConfigJson: "{}", Enabled: true,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return q.SetConnectorOperator(ctx, db.SetConnectorOperatorParams{
			OperatorIid: sql.NullString{String: operator, Valid: operator != ""}, ID: id,
		})
	}))
	f.status(id, status)
}

// status writes the stored status of the connection.
func (f *alertFixture) status(id, status string) {
	f.t.Helper()
	ctx := context.Background()
	require.NoError(f.t, f.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.UpdateConnectorStatus(ctx, db.UpdateConnectorStatusParams{Status: status, ID: id})
	}))
}

// enabled toggles the stored enabled flag, the way an owner disables a connection on purpose
// (ALERT-06).
func (f *alertFixture) enabled(id string, on bool) {
	f.t.Helper()
	ctx := context.Background()
	require.NoError(f.t, f.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		c, err := q.GetConnector(ctx, id)
		if err != nil {
			return err
		}
		return q.UpdateConnector(ctx, db.UpdateConnectorParams{
			Name: c.Name, ConfigJson: c.ConfigJson, SecretsEnc: c.SecretsEnc,
			Enabled: on, UpdatedAt: time.Now().UnixMilli(), ID: id,
		})
	}))
}

// delete removes the connection's row, the way deleting it does (ALERT-12).
func (f *alertFixture) delete(id string) {
	f.t.Helper()
	ctx := context.Background()
	require.NoError(f.t, f.env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.DeleteConnector(ctx, id)
	}))
}

// disableUser disables the account, whose devices must then receive nothing (ALERT-02).
func disableUser(t *testing.T, env *testkit.Env, id string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetUserDisabledAt(ctx, db.SetUserDisabledAtParams{
			DisabledAt: sql.NullInt64{Int64: 1, Valid: true}, ID: id,
		})
	}))
}

// publishStatus publishes one status event, the way connectors.Manager.persist does after the
// commit.
func publishStatus(env *testkit.Env, id, status, detail string) {
	env.App.Bus.Publish(events.ConnectorStatus{ConnectorID: id, Status: status, Detail: detail})
}

// newAlerter builds the alerter under test on the app's store, bus, public URL and logger.
func newAlerter(t *testing.T, env *testkit.Env, delivery webpush.Delivery, deps webpush.AlertDeps) *webpush.Alerter {
	t.Helper()
	deps.Store = env.App.Store
	deps.Bus = env.App.Bus
	deps.Delivery = delivery
	if deps.PublicURL == nil {
		deps.PublicURL = env.App.Config.PublicURL
	}
	if deps.Logger == nil {
		deps.Logger = env.App.Logger
	}
	a := webpush.NewAlerter(deps)
	t.Cleanup(a.Close)
	return a
}

// runAlerter runs a until the test ends and fails when it does not stop.
func runAlerter(t *testing.T, a *webpush.Alerter) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		a.Close()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(notifierWait):
			t.Error("the alerter did not stop")
		}
	})
}

// subscriptionIDs lists the devices a snapshot of jobs went to, in delivery order.
func subscriptionIDs(jobs []delivered) []string {
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.sub.ID
	}
	return out
}

// gatedDelivery holds every job until the test opens the gate, so the alerter's queue fills up.
type gatedDelivery struct {
	gate chan struct{}
	next *recorder
}

func (g *gatedDelivery) Deliver(ctx context.Context, sub webpush.Subscription, payload []byte) {
	select {
	case <-g.gate:
		g.next.Deliver(ctx, sub, payload)
	case <-ctx.Done():
	}
}

// ALERT-01, ALERT-02, ALERT-07: one error sends exactly one job per device of the organization's
// admins and of the operator AI's host, with the fixed payload, and nothing to the member, the
// guest or the disabled admin.
func TestAlerterAlertsOnAnError(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	f := newAlertFixture(t, env)
	aliceOne := f.device(f.admin.User.ID, "https://push.example.test/alice-1")
	aliceTwo := f.device(f.admin.User.ID, "https://push.example.test/alice-2")
	bob := f.member("bob", orgs.PermissionMember)
	f.device(bob.User.ID, "https://push.example.test/bob")
	carol, operator := f.host("carol")
	carolDevice := f.device(carol.User.ID, "https://push.example.test/carol")
	dan := f.member("dan", orgs.PermissionAdmin)
	f.device(dan.User.ID, "https://push.example.test/dan")
	disableUser(t, env, dan.User.ID)
	f.connector("k1", "ops", operator, "connected")

	r := &recorder{}
	a := newAlerter(t, env, r, webpush.AlertDeps{})
	a.Seed(context.Background())
	runAlerter(t, a)

	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	jobs := r.await(t, 3)
	require.Len(t, jobs, 3, "the admin's two devices and the operator host's one")
	require.ElementsMatch(t, []string{aliceOne.ID, aliceTwo.ID, carolDevice.ID}, subscriptionIDs(jobs))
	want := webpush.Payload{
		V:       1,
		Title:   "Connection down: ops",
		Author:  "",
		Preview: "The WhatsApp number was disconnected",
		URL:     env.App.Config.PublicURL.JoinPath("connectors", "k1").String(),
		Tag:     "conn:k1",
	}
	for _, job := range jobs {
		require.Equal(t, want, job.payload, "every device gets the same fixed payload")
	}
	noMore(t, r, 3)
}

// ALERT-07, ALERT-08: each responsible user reads the alert in the account's language, and both
// languages share the connection's tag so the alert still collapses per connection.
func TestAlerterAlertsInTheRecipientsLanguage(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	f := newAlertFixture(t, env)
	setLocale(t, env, f.admin.User.ID, "pt-BR")
	aliceDevice := f.device(f.admin.User.ID, "https://push.example.test/alice")
	carol, operator := f.host("carol")
	setLocale(t, env, carol.User.ID, "en")
	carolDevice := f.device(carol.User.ID, "https://push.example.test/carol")
	f.connector("k1", "ops", operator, "connected")

	r := &recorder{}
	a := newAlerter(t, env, r, webpush.AlertDeps{})
	a.Seed(context.Background())
	runAlerter(t, a)

	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	jobs := r.await(t, 2)
	require.Len(t, jobs, 2, "the pt-BR admin and the en host get one payload each")
	page := env.App.Config.PublicURL.JoinPath("connectors", "k1").String()
	want := map[string]webpush.Payload{
		aliceDevice.ID: {
			V: 1, Title: "Conexão caiu: ops", Author: "",
			Preview: "O número do WhatsApp foi desconectado", URL: page, Tag: "conn:k1",
		},
		carolDevice.ID: {
			V: 1, Title: "Connection down: ops", Author: "",
			Preview: "The WhatsApp number was disconnected", URL: page, Tag: "conn:k1",
		},
	}
	for _, job := range jobs {
		require.Equal(t, want[job.sub.ID], job.payload, "the device reads its recipient's language")
	}
	noMore(t, r, 2)
}

// ALERT-02: an admin who also hosts the operator AI is one responsible user, so her devices get
// one job each and not one per role.
func TestAlerterNotifiesEachUserOnce(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	f := newAlertFixture(t, env)
	one := f.device(f.admin.User.ID, "https://push.example.test/alice-1")
	two := f.device(f.admin.User.ID, "https://push.example.test/alice-2")
	operator := env.NewAI(f.admin, "ops-ai")
	f.connector("k1", "ops", operator, "connected")

	r := &recorder{}
	a := newAlerter(t, env, r, webpush.AlertDeps{})
	a.Seed(context.Background())
	runAlerter(t, a)

	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	jobs := r.await(t, 2)
	require.Len(t, jobs, 2)
	require.ElementsMatch(t, []string{one.ID, two.ID}, subscriptionIDs(jobs))
	noMore(t, r, 2)
}

// ALERT-03, ALERT-04, ALERT-05: one outage alerts once even when the platform's detail changes;
// recovery is silent; a new outage after the 30-minute window alerts again.
func TestAlerterStaysQuietWhileFailing(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	f := newAlertFixture(t, env)
	f.device(f.admin.User.ID, "https://push.example.test/alice-1")
	_, operator := f.host("carol")
	f.connector("k1", "ops", operator, "connected")
	clock := testkit.NewClock(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))

	r := &recorder{}
	a := newAlerter(t, env, r, webpush.AlertDeps{Now: clock.Now})
	a.Seed(context.Background())
	runAlerter(t, a)

	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	require.Len(t, r.await(t, 1), 1)

	// The platform reports another reason for the same outage: no second alert (ALERT-04).
	publishStatus(env, "k1", string(dto.ConnectorStatusError),
		"the WhatsApp number is not connected (logged out from another device); reconnect it in evolution-go")
	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	noMore(t, r, 1)

	publishStatus(env, "k1", string(dto.ConnectorStatusConnected), "")
	noMore(t, r, 1)

	clock.Advance(31 * time.Minute)
	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	require.Len(t, r.await(t, 2), 2)
}

// ALERT-05: a connection that recovers and fails again inside the 30 minutes stays quiet; at
// exactly 30 minutes the next outage alerts.
func TestAlerterCooldown(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	f := newAlertFixture(t, env)
	f.device(f.admin.User.ID, "https://push.example.test/alice-1")
	_, operator := f.host("carol")
	f.connector("k1", "ops", operator, "connected")
	clock := testkit.NewClock(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))

	r := &recorder{}
	a := newAlerter(t, env, r, webpush.AlertDeps{Now: clock.Now})
	a.Seed(context.Background())
	runAlerter(t, a)

	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	require.Len(t, r.await(t, 1), 1)

	publishStatus(env, "k1", string(dto.ConnectorStatusConnected), "")
	clock.Advance(29 * time.Minute)
	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	noMore(t, r, 1)

	publishStatus(env, "k1", string(dto.ConnectorStatusConnected), "")
	clock.Advance(time.Minute)
	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	require.Len(t, r.await(t, 2), 2)
}

// ALERT-06: stopping the connection clears the outage without a notification, and the cooldown
// from the last alert still holds.
func TestAlerterStoppedConnection(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	f := newAlertFixture(t, env)
	f.device(f.admin.User.ID, "https://push.example.test/alice-1")
	_, operator := f.host("carol")
	f.connector("k1", "ops", operator, "connected")
	clock := testkit.NewClock(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))

	r := &recorder{}
	a := newAlerter(t, env, r, webpush.AlertDeps{Now: clock.Now})
	a.Seed(context.Background())
	runAlerter(t, a)

	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	require.Len(t, r.await(t, 1), 1)

	publishStatus(env, "k1", string(dto.ConnectorStatusStopped), "")
	noMore(t, r, 1)
	publishStatus(env, "k1", string(dto.ConnectorStatusConnected), "")
	noMore(t, r, 1)

	clock.Advance(30 * time.Minute)
	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	require.Len(t, r.await(t, 2), 2)
}

// ALERT-06: a connection disabled in the store is not read as an outage, and its rule stays
// untouched, so re-enabling it and failing again alerts without a cooldown.
func TestAlerterSkipsADisabledConnection(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	f := newAlertFixture(t, env)
	f.device(f.admin.User.ID, "https://push.example.test/alice-1")
	_, operator := f.host("carol")
	f.connector("k1", "ops", operator, "connected")
	f.enabled("k1", false)
	clock := testkit.NewClock(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))

	r := &recorder{}
	a := newAlerter(t, env, r, webpush.AlertDeps{Now: clock.Now})
	a.Seed(context.Background())
	runAlerter(t, a)

	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	noMore(t, r, 0)

	f.enabled("k1", true)
	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	require.Len(t, r.await(t, 1), 1, "the disabled event left the rule untouched")
	noMore(t, r, 1)
}

// ALERT-10, ALERT-11: the seed reads the stored status — an error is already the outage, a
// connected connection alerts on its first error.
func TestAlerterSeedsTheStoredStatus(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	f := newAlertFixture(t, env)
	f.device(f.admin.User.ID, "https://push.example.test/alice-1")
	_, operator := f.host("carol")
	f.connector("k1", "ops", operator, "error")
	f.connector("k2", "ops2", operator, "connected")
	clock := testkit.NewClock(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))

	r := &recorder{}
	a := newAlerter(t, env, r, webpush.AlertDeps{Now: clock.Now})
	a.Seed(context.Background())
	runAlerter(t, a)

	// k1 was already down when the server started: its boot burst is silent.
	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	noMore(t, r, 0)

	publishStatus(env, "k1", string(dto.ConnectorStatusConnected), "")
	clock.Advance(31 * time.Minute)
	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	require.Len(t, r.await(t, 1), 1, "a new outage after recovery alerts")

	// k2 was healthy when the server started: its first error is a new outage.
	publishStatus(env, "k2", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	require.Len(t, r.await(t, 2), 2)
}

// ALERT-12: an event of a connection with no row, or of one deleted after the seed, sends nothing
// and does not panic.
func TestAlerterIgnoresUnknownAndDeletedConnections(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	f := newAlertFixture(t, env)
	f.device(f.admin.User.ID, "https://push.example.test/alice-1")
	_, operator := f.host("carol")
	f.connector("k1", "ops", operator, "connected")

	r := &recorder{}
	a := newAlerter(t, env, r, webpush.AlertDeps{})
	a.Seed(context.Background())
	runAlerter(t, a)

	publishStatus(env, "k-unknown", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	noMore(t, r, 0)

	f.delete("k1")
	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	noMore(t, r, 0)
}

// ALERT-10, ALERT-11: a connection created after the start is unseen, so its first error is a
// baseline and only a later connected-then-error alerts.
func TestAlerterANewConnectionIsABaseline(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	f := newAlertFixture(t, env)
	f.device(f.admin.User.ID, "https://push.example.test/alice-1")
	_, operator := f.host("carol")
	clock := testkit.NewClock(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))

	r := &recorder{}
	a := newAlerter(t, env, r, webpush.AlertDeps{Now: clock.Now})
	a.Seed(context.Background()) // before the connection exists
	runAlerter(t, a)
	f.connector("k1", "ops", operator, "connected")

	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	noMore(t, r, 0)

	publishStatus(env, "k1", string(dto.ConnectorStatusConnected), "")
	clock.Advance(31 * time.Minute)
	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	require.Len(t, r.await(t, 1), 1)
}

// ALERT-14: the alert's log line names the connection id and the status and never the name, the
// reason, the platform detail, the URL or a device.
func TestAlerterLogsOnlyTheIdAndStatus(t *testing.T) {
	var buf logBuffer
	env := testkit.New(t, testkit.WithoutPush(), testkit.WithLogger(testLogger(&buf)))
	f := newAlertFixture(t, env)
	f.device(f.admin.User.ID, "https://push.example.test/alice-1")
	_, operator := f.host("carol")
	f.connector("k1", "payments-bot", operator, "connected")

	r := &recorder{}
	a := newAlerter(t, env, r, webpush.AlertDeps{})
	a.Seed(context.Background())
	runAlerter(t, a)

	publishStatus(env, "k1", string(dto.ConnectorStatusError),
		"the WhatsApp number is not connected (logged out from another device); reconnect it in evolution-go")
	require.Len(t, r.await(t, 1), 1)
	require.Eventually(t, func() bool { return strings.Contains(buf.String(), "connection alert queued") },
		notifierWait, 5*time.Millisecond, "the alert was logged")

	line := ""
	for _, candidate := range strings.Split(buf.String(), "\n") {
		if strings.Contains(candidate, "connection alert") {
			line = candidate
			break
		}
	}
	require.NotEmpty(t, line, "the alert logged a line")
	require.Contains(t, line, "connector_id=k1")
	require.Contains(t, line, "status=error")
	logs := buf.String()
	require.NotContains(t, logs, "payments-bot", "the connection name never reaches a log")
	require.NotContains(t, logs, "logged out", "the platform detail never reaches a log")
	require.NotContains(t, logs, "WhatsApp number")
	require.NotContains(t, logs, "/connectors/")
	require.NotContains(t, logs, "push.example.test", "no device endpoint reaches a log")
}

// ALERT-15: a full queue drops the alert without blocking the status event, logs only the
// connection id and the status, and never hands the dropped job to a delivery.
func TestAlerterDropsOnAFullQueue(t *testing.T) {
	var buf logBuffer
	env := testkit.New(t, testkit.WithoutPush(), testkit.WithLogger(testLogger(&buf)))
	f := newAlertFixture(t, env)
	for _, endpoint := range []string{
		"https://push.example.test/device-1",
		"https://push.example.test/device-2",
		"https://push.example.test/device-3",
		"https://push.example.test/device-4",
		"https://push.example.test/device-5",
	} {
		f.device(f.admin.User.ID, endpoint)
	}
	_, operator := f.host("carol")
	f.connector("k1", "payments-bot", operator, "connected")

	gate := make(chan struct{})
	r := &recorder{}
	a := newAlerter(t, env, &gatedDelivery{gate: gate, next: r}, webpush.AlertDeps{QueueSize: 1})
	a.Seed(context.Background())
	runAlerter(t, a)

	published := make(chan struct{})
	go func() {
		publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
		close(published)
	}()
	select {
	case <-published:
	case <-time.After(notifierWait):
		t.Fatal("publishing the status blocked")
	}
	require.Eventually(t, func() bool { return strings.Contains(buf.String(), "webpush: connection alert queue full") },
		notifierWait, 5*time.Millisecond, "the full queue was logged")
	require.Contains(t, buf.String(), "connector_id=k1")
	require.NotContains(t, buf.String(), "payments-bot")
	require.NotContains(t, buf.String(), "WhatsApp number")

	// Two workers hold one job each and the queue takes one: at most three of the five devices
	// ever see the alert, and the drop never blocks the consumer.
	close(gate)
	require.Eventually(t, func() bool { return r.count() >= 1 }, notifierWait, 5*time.Millisecond)
	noMore(t, r, 3)
	require.LessOrEqual(t, r.count(), 3, "a dropped alert is never delivered")
}

// ALERT-12, ALERT-15: a consumer that sees Lagged neither panics nor sends, and an alerter closed
// before Run cancels its subscription and delivers nothing afterwards.
func TestAlerterIgnoresLaggedAndClosesWithoutRun(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	f := newAlertFixture(t, env)
	f.device(f.admin.User.ID, "https://push.example.test/alice-1")
	_, operator := f.host("carol")
	f.connector("k1", "ops", operator, "connected")

	r := &recorder{}

	// Close before Run: Start never calls it under DisablePush, and Shutdown still must not panic.
	closed := newAlerter(t, env, r, webpush.AlertDeps{})
	closed.Seed(context.Background())
	closed.Close()
	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	noMore(t, r, 0)

	// Lagged is a missed event, not an outage.
	running := newAlerter(t, env, r, webpush.AlertDeps{})
	running.Seed(context.Background())
	runAlerter(t, running)
	env.App.Bus.Publish(events.Lagged{})
	noMore(t, r, 0)

	running.Close()
	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	noMore(t, r, 0)
}

// lineWith returns the first log line containing marker, or the empty string.
func lineWith(logs, marker string) string {
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	return ""
}

// ALERT-01, ALERT-09, ALERT-14: the app builds the alerter and Start runs it, so a connection that
// reports healthy and then fails sends the fixed alert through the shared Deliverer.
func TestAppStartsTheConnectionAlerter(t *testing.T) {
	var buf logBuffer
	env := testkit.New(t, testkit.WithLogger(testLogger(&buf)))
	require.NotNil(t, env.App.Alerts, "app.New builds the alerter")

	f := newAlertFixture(t, env)
	f.device(f.admin.User.ID, "not a push endpoint")
	_, operator := f.host("carol")
	// The app seeded before this row existed, so the connection is unseen and must report
	// connected before its error is a new outage (ALERT-11).
	f.connector("k1", "ops", operator, "connected")

	publishStatus(env, "k1", string(dto.ConnectorStatusConnected), "")
	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)

	require.Eventually(t, func() bool {
		line := lineWith(buf.String(), "webpush: connection alert queued")
		return strings.Contains(line, "connector_id=k1") && strings.Contains(line, "status=error")
	}, notifierWait, 10*time.Millisecond, "the app's alerter queued the alert")
	require.NotContains(t, buf.String(), "WhatsApp number", "the platform detail never reaches a log")

	// The app's own Deliverer took the job and failed before any network I/O, because the endpoint
	// is not a URL.
	require.Eventually(t, func() bool { return strings.Contains(buf.String(), "endpoint_host=invalid") },
		notifierWait, 10*time.Millisecond, "the shared Deliverer delivered the queued job")
}

// ALERT-01: WithoutPush still builds the alerter, but Start never runs it, so an outage event sends
// nothing.
func TestWithoutPushStopsTheConnectionAlerter(t *testing.T) {
	var buf logBuffer
	env := testkit.New(t, testkit.WithoutPush(), testkit.WithLogger(testLogger(&buf)))
	require.NotNil(t, env.App.Alerts, "app.New builds the alerter even with push off")

	f := newAlertFixture(t, env)
	f.device(f.admin.User.ID, "https://push.example.test/alice-1")
	_, operator := f.host("carol")
	f.connector("k1", "ops", operator, "connected")

	publishStatus(env, "k1", string(dto.ConnectorStatusConnected), "")
	publishStatus(env, "k1", string(dto.ConnectorStatusError), alertWhatsAppDetail)
	require.Never(t, func() bool { return strings.Contains(buf.String(), "connection alert") },
		300*time.Millisecond, 10*time.Millisecond, "a disabled alerter sends nothing")
}

// ALERT-10, ALERT-11: Start seeds the alert state before the connections start, so a connection
// stored in error stays silent through the boot error and one stored healthy alerts on it.
func TestAppSeedsTheAlertStateBeforeTheConnectionsStart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stored string
		alerts bool
	}{
		{name: "stored error is the same outage", stored: string(dto.ConnectorStatusError), alerts: false},
		{name: "stored healthy is a new outage", stored: string(dto.ConnectorStatusConnected), alerts: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf logBuffer
			env := testkit.New(t, testkit.WithLogger(testLogger(&buf)))
			f := newAlertFixture(t, env)
			f.device(f.admin.User.ID, "not a push endpoint")
			_, operator := f.host("carol")
			// A kind the app does not know: the running manager never applies this row, and the
			// manager started by Restart persists that boot error during its Start.
			f.connectorOfKind("k1", "ops", "unknown-platform", operator, tc.stored)

			env.Restart()

			if tc.alerts {
				require.Eventually(t, func() bool {
					return strings.Contains(lineWith(buf.String(), "webpush: connection alert queued"), "connector_id=k1")
				}, notifierWait, 10*time.Millisecond, "a connection stored healthy alerts on its first error")
				return
			}
			require.Never(t, func() bool { return strings.Contains(buf.String(), "connection alert") },
				300*time.Millisecond, 10*time.Millisecond,
				"a connection stored in error stays silent through its boot burst")
		})
	}
}
