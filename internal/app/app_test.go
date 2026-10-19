package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/config"
)

func newApp(t *testing.T) *App {
	t.Helper()
	env := map[string]string{"PARTYLINE_DATA_DIR": t.TempDir()}
	cfg, err := config.Load(func(k string) string { return env[k] })
	require.NoError(t, err)
	a, err := New(cfg, Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	return a
}

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
