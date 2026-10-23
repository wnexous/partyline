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
