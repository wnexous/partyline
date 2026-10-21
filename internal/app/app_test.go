package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/config"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

func newApp(t *testing.T) *App {
	t.Helper()
	env := map[string]string{"PARTYLINE_DATA_DIR": t.TempDir()}
	cfg, err := config.Load(func(k string) string { return env[k] })
	require.NoError(t, err)
	// Background job errors land in the test log, so a failure shows its cause.
	a, err := New(cfg, Options{Logger: slog.New(slog.NewTextHandler(t.Output(), nil))})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	return a
}

// jobDeadline bounds how long a test waits for a background job. It is generous because a
// loaded machine (parallel packages, slow fsync) can delay the first run by many seconds;
// the waits poll, so a healthy run still finishes in milliseconds.
const jobDeadline = time.Minute

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// OPS-01: /healthz is 200 without authentication; /readyz is 503 until migrations finished.
func TestHealthAndReadiness(t *testing.T) {
	a := newApp(t)
	h := a.Handler()

	require.Equal(t, http.StatusOK, get(t, h, "/healthz").Code)

	rec := get(t, h, "/readyz")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var body dto.ErrorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, dto.ErrorCodeUnavailable, body.Error.Code)

	require.NoError(t, a.Start(context.Background()))
	require.Equal(t, http.StatusOK, get(t, h, "/readyz").Code)
	require.Equal(t, http.StatusOK, get(t, h, "/healthz").Code)

	needs, err := a.Users.NeedsSetup(context.Background())
	require.NoError(t, err)
	require.True(t, needs)
	require.NotEmpty(t, a.SetupToken())
}

// LIVE-02 edge case: a default app re-checks live sessions every minute, so a revoked session
// outlives its sockets by at most 60 s even if no revocation event arrives.
func TestLiveSessionCheckDefaultsToOneMinute(t *testing.T) {
	a := newApp(t)
	require.Equal(t, time.Minute, a.Live.SessionCheckInterval())
}

type recordingSender struct{ sent chan delivery.Delivery }

func (s recordingSender) Send(_ context.Context, d delivery.Delivery) (string, error) {
	s.sent <- d
	return "ext-1", nil
}

// DLV-07: a started app runs the outbox worker with the configured sender, and Shutdown
// stops it.
func TestStartRunsOutboxWorker(t *testing.T) {
	env := map[string]string{"PARTYLINE_DATA_DIR": t.TempDir()}
	cfg, err := config.Load(func(k string) string { return env[k] })
	require.NoError(t, err)
	sender := recordingSender{sent: make(chan delivery.Delivery, 1)}
	a, err := New(cfg, Options{Logger: slog.New(slog.NewTextHandler(t.Output(), nil)), Sender: sender})
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, a.Start(ctx))

	now := time.Now().UnixMilli() // recent, so the retention job started with the app keeps it
	ids := map[string]string{"user": store.NewID(), "room": store.NewID(), "conn": store.NewID(), "chat": store.NewID(), "msg": store.NewID(), "out": store.NewID()}
	require.NoError(t, a.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateUser(ctx, db.CreateUserParams{ID: ids["user"], Username: "alice", PasswordHash: "x", Role: "member"}); err != nil {
			return err
		}
		if err := q.CreateRoom(ctx, db.CreateRoomParams{ID: ids["room"], OwnerID: ids["user"], Name: "ops", RetentionDays: 90, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		if err := q.CreateConnector(ctx, db.CreateConnectorParams{ID: ids["conn"], OwnerID: ids["user"], Kind: "telegram", Name: "tg"}); err != nil {
			return err
		}
		if err := q.CreateChat(ctx, db.CreateChatParams{ID: ids["chat"], ConnectorID: ids["conn"], ExternalID: "x", Title: "g", Kind: "group"}); err != nil {
			return err
		}
		if err := q.CreateRoomMember(ctx, db.CreateRoomMemberParams{ID: store.NewID(), RoomID: ids["room"], Kind: "chat", MemberID: ids["chat"], Outbound: true, CreatedAt: now}); err != nil {
			return err
		}
		if err := q.CreateMessage(ctx, db.CreateMessageParams{ID: ids["msg"], RoomID: ids["room"], Seq: 1, AuthorKind: "user", AuthorID: ids["user"], Kind: "text", Text: "hi", CreatedAt: now}); err != nil {
			return err
		}
		return q.CreateOutbox(ctx, db.CreateOutboxParams{ID: ids["out"], MessageID: ids["msg"], ChatID: ids["chat"], NextAttemptAt: now, UpdatedAt: now})
	}))
	a.Bus.Publish(events.MessageCreated{RoomID: ids["room"], MessageID: ids["msg"], Seq: 1})

	select {
	case d := <-sender.sent:
		require.Equal(t, ids["out"], d.OutboxID)
		require.Equal(t, ids["chat"], d.ChatID)
	case <-time.After(jobDeadline):
		t.Fatal("outbox entry was not sent")
	}
	require.NoError(t, a.Shutdown(ctx))
}

// OPS-02: the retention job runs once when the app starts.
func TestStartRunsRetention(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	require.NoError(t, a.Store.Migrate(ctx))
	ids := map[string]string{"user": store.NewID(), "room": store.NewID(), "msg": store.NewID()}
	old := time.Now().Add(-91 * 24 * time.Hour).UnixMilli()
	require.NoError(t, a.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateUser(ctx, db.CreateUserParams{ID: ids["user"], Username: "alice", PasswordHash: "x", Role: "member"}); err != nil {
			return err
		}
		if err := q.CreateRoom(ctx, db.CreateRoomParams{ID: ids["room"], OwnerID: ids["user"], Name: "ops", RetentionDays: 90}); err != nil {
			return err
		}
		return q.CreateMessage(ctx, db.CreateMessageParams{ID: ids["msg"], RoomID: ids["room"], Seq: 1, AuthorKind: "user", AuthorID: ids["user"], Kind: "text", Text: "old", CreatedAt: old})
	}))
	require.NoError(t, a.Start(ctx))
	require.Eventually(t, func() bool {
		_, err := a.Store.R().GetMessageView(ctx, ids["msg"])
		return errors.Is(err, sql.ErrNoRows)
	}, jobDeadline, 10*time.Millisecond, "retention did not delete the expired message")
}

// PROJ-02: Start runs the daily archive job, whose first pass archives a temporary agent
// offline for more than 24 hours.
func TestStartRunsAgentArchive(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	require.NoError(t, a.Store.Migrate(ctx))
	user, agent := store.NewID(), store.NewID()
	old := time.Now().Add(-25 * time.Hour).UnixMilli()
	require.NoError(t, a.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateUser(ctx, db.CreateUserParams{ID: user, Username: "alice", PasswordHash: "x", Role: "member"}); err != nil {
			return err
		}
		if err := q.CreateAgent(ctx, db.CreateAgentParams{ID: agent, OwnerID: user, Name: "proj-claude", CreatedAt: old}); err != nil {
			return err
		}
		return q.UpdateAgentIdentity(ctx, db.UpdateAgentIdentityParams{Harness: "claude", Kind: string(dto.AgentKindTemporary), ID: agent})
	}))
	require.NoError(t, a.Start(ctx))
	require.Eventually(t, func() bool {
		got, err := a.Store.R().GetAgent(ctx, agent)
		return err == nil && got.ArchivedAt.Valid
	}, jobDeadline, 10*time.Millisecond, "the archive job did not archive the stale temporary agent")
}
