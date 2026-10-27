package webpush_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/testkit"
	"github.com/wnexous/partyline/internal/users"
	"github.com/wnexous/partyline/internal/webpush"
)

// notifierWait bounds every wait for a delivery; the waits return as soon as the delivery
// arrives, so the bound only matters on a loaded machine.
const notifierWait = 10 * time.Second

// delivered is one job the notifier handed to its Delivery.
type delivered struct {
	sub     webpush.Subscription
	payload webpush.Payload
}

// recorder is a webpush.Delivery that records (Subscription, payload) without sending anything.
type recorder struct {
	mu   sync.Mutex
	jobs []delivered
}

func (r *recorder) Deliver(_ context.Context, sub webpush.Subscription, payload []byte) {
	var p webpush.Payload
	if err := json.Unmarshal(payload, &p); err != nil {
		panic(err) // the notifier always builds JSON; a broken payload is a test bug
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs = append(r.jobs, delivered{sub: sub, payload: p})
}

func (r *recorder) all() []delivered {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.jobs)
}

func (r *recorder) count() int { return len(r.all()) }

// await waits until n jobs were delivered and returns a snapshot. It may return more than n
// when several workers finish at once; the caller asserts the exact count.
func (r *recorder) await(t *testing.T, n int) []delivered {
	t.Helper()
	require.Eventually(t, func() bool { return r.count() >= n }, notifierWait, 5*time.Millisecond,
		"awaiting %d deliveries", n)
	return r.all()
}

// find returns the delivered payload with the given preview, or nil.
func (r *recorder) find(preview string) *delivered {
	jobs := r.all()
	for i := range jobs {
		if jobs[i].payload.Preview == preview {
			return &jobs[i]
		}
	}
	return nil
}

// previews lists the previews of a snapshot, in delivery order.
func previews(jobs []delivered) []string {
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.payload.Preview
	}
	return out
}

// countOf counts the values equal to v.
func countOf(values []string, v string) int {
	n := 0
	for _, value := range values {
		if value == v {
			n++
		}
	}
	return n
}

// noMore asserts that no delivery is added after the snapshot.
func noMore(t *testing.T, r *recorder, n int) {
	t.Helper()
	require.Never(t, func() bool { return r.count() > n }, 300*time.Millisecond, 10*time.Millisecond,
		"no delivery beyond the %d expected", n)
}

// account creates a member account with a display name and returns its logged-in client.
func account(t *testing.T, env *testkit.Env, username, displayName string) *testkit.Client {
	t.Helper()
	_, err := env.App.Users.CreateUser(context.Background(),
		users.AccountInput{Username: username, Password: testkit.Password, DisplayName: displayName}, dto.RoleMember)
	require.NoError(t, err)
	return env.LoginAs(username, testkit.Password)
}

// subscribe stores one push subscription for the user and returns the row.
func subscribe(t *testing.T, env *testkit.Env, userID, endpoint string) db.PushSubscription {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, err = rand.Read(auth)
	require.NoError(t, err)
	ctx := context.Background()
	var sub db.PushSubscription
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		var err error
		sub, err = q.UpsertPushSubscription(ctx, db.UpsertPushSubscriptionParams{
			ID: store.NewID(), UserID: userID, SessionID: sql.NullString{},
			Endpoint:  endpoint,
			P256dh:    base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
			Auth:      base64.RawURLEncoding.EncodeToString(auth),
			CreatedAt: time.Now().UnixMilli(),
		})
		return err
	}))
	return sub
}

// setLocale writes the account's language, which picks the language of the server-written push
// texts.
func setLocale(t *testing.T, env *testkit.Env, userID, locale string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetUserLocale(ctx, db.SetUserLocaleParams{Locale: locale, ID: userID})
	}))
}

// setPushGroups writes the account's group notification setting.
func setPushGroups(t *testing.T, env *testkit.Env, userID, mode string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetUserPushGroups(ctx, db.SetUserPushGroupsParams{PushGroups: mode, ID: userID})
	}))
}

func openDM(t *testing.T, from *testkit.Client, with string) dto.Room {
	t.Helper()
	return testkit.Decode[dto.Room](from.Post("/api/v1/conversations/dm", dto.OpenDMRequest{With: with}).RequireStatus(http.StatusCreated))
}

func postMessage(t *testing.T, c *testkit.Client, roomID, text string) dto.Message {
	t.Helper()
	return testkit.Decode[dto.Message](c.Post("/api/v1/rooms/"+roomID+"/messages", dto.PostMessageRequest{Text: text}).RequireStatus(http.StatusCreated))
}

// newNotifier builds a notifier on the running app's store and bus with a recording delivery.
func newNotifier(t *testing.T, env *testkit.Env, r webpush.Delivery) *webpush.Notifier {
	t.Helper()
	return webpush.NewNotifier(webpush.NotifierDeps{
		Store: env.App.Store, Bus: env.App.Bus, Delivery: r,
		PublicURL: env.App.Config.PublicURL, Logger: env.App.Logger,
	})
}

// startNotifier runs n until the test ends and fails when it does not stop.
func startNotifier(t *testing.T, n *webpush.Notifier) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- n.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		n.Close()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(notifierWait):
			t.Error("the notifier did not stop")
		}
	})
}

// storeSystemNotice stores a system notice in the room and publishes its event, the way the loop
// guard does: a MessageCreated of a system message.
func storeSystemNotice(t *testing.T, env *testkit.Env, roomID string) {
	t.Helper()
	ctx := context.Background()
	id := store.NewID()
	var seq int64
	now := time.Now().UnixMilli()
	require.NoError(t, env.App.Store.WithTx(ctx, func(q *db.Queries) error {
		room, err := q.NextRoomSeq(ctx, db.NextRoomSeqParams{UpdatedAt: now, ID: roomID})
		if err != nil {
			return err
		}
		seq = room.NextSeq - 1
		return q.CreateMessage(ctx, db.CreateMessageParams{
			ID: id, RoomID: roomID, Seq: seq,
			AuthorKind: string(dto.AuthorKindSystem), Kind: string(dto.MessageKindSystem),
			Text: "account notices are not pushed", RecipientsJson: "[]", CreatedAt: now,
		})
	}))
	env.App.Bus.Publish(events.MessageCreated{RoomID: roomID, MessageID: id, Seq: seq})
}

// PUSH-08, PUSH-09, PUSH-10: a DM notifies its other participant, never the author, with the
// payload of the design.
func TestNotifierNotifiesADM(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	alice := account(t, env, "alice", "Alice")
	bob := account(t, env, "bob", "Bob")
	dm := openDM(t, alice, bob.User.ID)
	aliceSub := subscribe(t, env, alice.User.ID, "https://push.example.com/send/alice")
	bobSub := subscribe(t, env, bob.User.ID, "https://push.example.com/send/bob")
	r := &recorder{}
	startNotifier(t, newNotifier(t, env, r))

	postMessage(t, alice, dm.ID, "hi")
	jobs := r.await(t, 1)
	require.Len(t, jobs, 1, "the author's own message notifies nobody")
	require.Equal(t, bobSub.ID, jobs[0].sub.ID)
	require.Equal(t, bobSub.Endpoint, jobs[0].sub.Endpoint)
	require.NotEqual(t, aliceSub.ID, jobs[0].sub.ID)
	require.Equal(t, webpush.Payload{
		V:       1,
		Title:   "Alice",
		Author:  "Alice",
		Preview: "hi",
		URL:     env.App.Config.PublicURL.JoinPath("rooms", dm.ID).String(),
		Tag:     "room:" + dm.ID,
	}, jobs[0].payload)
	noMore(t, r, 1)
}

// PUSH-08: the group setting decides; a DM ignores it. DMs always notify (PUSH-08, PUSH-09).
func TestGroupModes(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	alice := account(t, env, "alice", "Alice")
	bob := account(t, env, "bob", "Bob")
	env.AddMember(alice.PersonalOrg().ID, bob.User.ID, orgs.PermissionMember, "")
	aiID := env.NewAI(alice, "Helper")
	group := testkit.Decode[dto.Room](alice.Post("/api/v1/groups", dto.CreateGroupRequest{
		Name: "Team", Members: []string{bob.User.ID, aiID},
	}).RequireStatus(http.StatusCreated))
	bobSub := subscribe(t, env, bob.User.ID, "https://push.example.com/send/bob")
	r := &recorder{}
	startNotifier(t, newNotifier(t, env, r))

	// The default setting is mentions: a plain message is silent, a mention notifies.
	postMessage(t, alice, group.ID, "hello")
	postMessage(t, alice, group.ID, "@bob hello")
	jobs := r.await(t, 1)
	require.Equal(t, []string{"@bob hello"}, previews(jobs), "the unmentioned message is silent")
	require.Equal(t, "Team", jobs[0].payload.Title)
	require.Equal(t, "Alice", jobs[0].payload.Author)
	require.Equal(t, "room:"+group.ID, jobs[0].payload.Tag)

	// all: every group message notifies.
	setPushGroups(t, env, bob.User.ID, webpush.PushGroupsAll)
	postMessage(t, alice, group.ID, "hello")
	jobs = r.await(t, 2)
	require.ElementsMatch(t, []string{"@bob hello", "hello"}, previews(jobs))

	// off: even a mention is silent, in a group; a DM still notifies.
	setPushGroups(t, env, bob.User.ID, webpush.PushGroupsOff)
	postMessage(t, alice, group.ID, "@bob hello")
	dm := openDM(t, alice, bob.User.ID)
	postMessage(t, alice, dm.ID, "hi")
	jobs = r.await(t, 3)
	require.ElementsMatch(t, []string{"@bob hello", "hello", "hi"}, previews(jobs), "off silences the group mention")
	require.Equal(t, bobSub.ID, jobs[0].sub.ID)
	noMore(t, r, 3)
}

// PUSH-09: a message read before the worker handled its event notifies nobody.
func TestReadCursorSuppresses(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	alice := account(t, env, "alice", "Alice")
	bob := account(t, env, "bob", "Bob")
	dm := openDM(t, alice, bob.User.ID)
	subscribe(t, env, bob.User.ID, "https://push.example.com/send/bob")
	r := &recorder{}
	n := newNotifier(t, env, r) // the constructor subscribes, before Run
	postMessage(t, alice, dm.ID, "read before the worker runs")
	bob.Put("/api/v1/rooms/"+dm.ID+"/read", dto.MarkReadRequest{}).RequireStatus(http.StatusOK)
	startNotifier(t, n)
	require.Never(t, func() bool { return r.count() > 0 }, 500*time.Millisecond, 10*time.Millisecond,
		"a message the cursor covers notifies nobody")
}

// PUSH-08: a system notice carries no author to name and is never pushed.
func TestSystemNoticeIsSkipped(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	alice := account(t, env, "alice", "Alice")
	bob := account(t, env, "bob", "Bob")
	dm := openDM(t, alice, bob.User.ID)
	subscribe(t, env, bob.User.ID, "https://push.example.com/send/bob")
	r := &recorder{}
	startNotifier(t, newNotifier(t, env, r))

	storeSystemNotice(t, env, dm.ID)
	postMessage(t, alice, dm.ID, "after the notice") // the barrier: its delivery is processed after the notice
	require.Eventually(t, func() bool { return r.find("after the notice") != nil }, notifierWait, 5*time.Millisecond)
	require.Equal(t, 1, r.count(), "only the user message is pushed")
	noMore(t, r, 1)
}

// PUSH-23: a message deleted before the event was handled notifies nobody.
func TestDeletedMessageIsSkipped(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	alice := account(t, env, "alice", "Alice")
	bob := account(t, env, "bob", "Bob")
	dm := openDM(t, alice, bob.User.ID)
	subscribe(t, env, bob.User.ID, "https://push.example.com/send/bob")
	r := &recorder{}
	n := newNotifier(t, env, r) // the constructor subscribes, before Run
	msg := postMessage(t, alice, dm.ID, "gone before the worker runs")
	alice.Delete("/api/v1/rooms/" + dm.ID + "/messages/" + msg.ID).RequireStatus(http.StatusOK)
	startNotifier(t, n)
	require.Never(t, func() bool { return r.count() > 0 }, 500*time.Millisecond, 10*time.Millisecond,
		"a deleted message notifies nobody")
}

// PUSH-13: at most 10 notifications per user in a rolling minute.
func TestRateLimitPerUser(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	alice := account(t, env, "alice", "Alice")
	bob := account(t, env, "bob", "Bob")
	dm := openDM(t, alice, bob.User.ID)
	subscribe(t, env, bob.User.ID, "https://push.example.com/send/bob")
	r := &recorder{}
	startNotifier(t, newNotifier(t, env, r))

	for i := range 11 {
		postMessage(t, alice, dm.ID, fmt.Sprintf("message %d", i+1))
	}
	jobs := r.await(t, 10)
	require.Len(t, jobs, 10, "the eleventh message in the minute is dropped")
	noMore(t, r, 10)
}

// PUSH-24: the limit counts one notification per user, not per device: two subscriptions get the
// ten notifications and the eleventh message reaches none.
func TestMultipleDevicesShareOneRateSlot(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	alice := account(t, env, "alice", "Alice")
	bob := account(t, env, "bob", "Bob")
	dm := openDM(t, alice, bob.User.ID)
	phone := subscribe(t, env, bob.User.ID, "https://push.example.com/send/phone")
	desktop := subscribe(t, env, bob.User.ID, "https://push.example.com/send/desktop")
	r := &recorder{}
	startNotifier(t, newNotifier(t, env, r))

	for i := range 11 {
		postMessage(t, alice, dm.ID, fmt.Sprintf("message %d", i+1))
	}
	jobs := r.await(t, 20)
	require.Len(t, jobs, 20, "ten notifications, one per device")
	endpoints := make([]string, len(jobs))
	for i, j := range jobs {
		endpoints[i] = j.sub.Endpoint
	}
	require.Equal(t, 10, countOf(endpoints, phone.Endpoint))
	require.Equal(t, 10, countOf(endpoints, desktop.Endpoint))
	noMore(t, r, 20)
}

// PUSH-15: EnqueueTest queues the fixed test payload for every subscription of the caller.
func TestEnqueueTest(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	bob := account(t, env, "bob", "Bob")
	phone := subscribe(t, env, bob.User.ID, "https://push.example.com/send/phone")
	desktop := subscribe(t, env, bob.User.ID, "https://push.example.com/send/desktop")
	subs, err := env.App.Store.R().ListPushSubscriptionsByUser(context.Background(), bob.User.ID)
	require.NoError(t, err)
	require.Len(t, subs, 2)
	r := &recorder{}
	n := newNotifier(t, env, r)
	startNotifier(t, n)

	require.Equal(t, 2, n.EnqueueTest(context.Background(), subs))
	jobs := r.await(t, 2)
	require.Len(t, jobs, 2)
	endpoints := make([]string, len(jobs))
	for i, j := range jobs {
		endpoints[i] = j.sub.Endpoint
		require.Equal(t, 1, j.payload.V)
		require.Equal(t, "partyline", j.payload.Title)
		require.Equal(t, "Notifications are on", j.payload.Preview)
		require.Equal(t, "test", j.payload.Tag)
	}
	require.ElementsMatch(t, []string{phone.Endpoint, desktop.Endpoint}, endpoints)
	require.Equal(t, 0, n.EnqueueTest(context.Background(), nil), "no subscription queues nothing")
}

// PUSH-15: the test notification follows the account's language; an empty or unsupported locale
// reads English.
func TestEnqueueTestFollowsTheRecipientsLanguage(t *testing.T) {
	tests := []struct {
		name   string
		locale string
		want   string
	}{
		{"pt-BR", "pt-BR", "As notificações estão ligadas"},
		{"en", "en", "Notifications are on"},
		{"an empty locale reads English", "", "Notifications are on"},
		{"an unsupported locale reads English", "fr-FR", "Notifications are on"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testkit.New(t, testkit.WithoutPush())
			bob := account(t, env, "bob", "Bob")
			if tt.locale != "" {
				setLocale(t, env, bob.User.ID, tt.locale)
			}
			sub := subscribe(t, env, bob.User.ID, "https://push.example.com/send/phone")
			r := &recorder{}
			n := newNotifier(t, env, r)
			startNotifier(t, n)

			require.Equal(t, 1, n.EnqueueTest(context.Background(), []db.PushSubscription{sub}))
			jobs := r.await(t, 1)
			require.Len(t, jobs, 1)
			require.Equal(t, tt.want, jobs[0].payload.Preview)
			require.Equal(t, "partyline", jobs[0].payload.Title)
			require.Equal(t, "test", jobs[0].payload.Tag)
			noMore(t, r, 1)
		})
	}
}

// A Lagged marker never panics the notifier and never produces a notification (PUSH-14).
func TestNotifierIgnoresLagged(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	alice := account(t, env, "alice", "Alice")
	bob := account(t, env, "bob", "Bob")
	dm := openDM(t, alice, bob.User.ID)
	subscribe(t, env, bob.User.ID, "https://push.example.com/send/bob")
	r := &recorder{}
	startNotifier(t, newNotifier(t, env, r))

	env.App.Bus.Publish(events.Lagged{})
	postMessage(t, alice, dm.ID, "after lagged")
	jobs := r.await(t, 1)
	require.Equal(t, []string{"after lagged"}, previews(jobs), "the marker delivered nothing on its own")
	noMore(t, r, 1)
}

// Close cancels the bus subscription even when Run never started: the event buffered between the
// constructor and Close is never handled.
func TestCloseCancelsBeforeRun(t *testing.T) {
	env := testkit.New(t, testkit.WithoutPush())
	alice := account(t, env, "alice", "Alice")
	bob := account(t, env, "bob", "Bob")
	dm := openDM(t, alice, bob.User.ID)
	subscribe(t, env, bob.User.ID, "https://push.example.com/send/bob")
	r := &recorder{}
	n := newNotifier(t, env, r)
	postMessage(t, alice, dm.ID, "buffered while nobody runs")
	n.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- n.Run(ctx) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(notifierWait):
		t.Fatal("Run did not return after Close")
	}
	require.Zero(t, r.count())
}
