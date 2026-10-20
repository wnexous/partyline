package connectors_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/safehttp"
	"github.com/wnexous/partyline/internal/testkit"
)

// sizedServer answers ?n=<bytes> with that many bytes.
func sizedServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		_, _ = w.Write(bytes.Repeat([]byte("x"), n))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// readAll fetches n bytes through c and returns the read error.
func readAll(t *testing.T, c *http.Client, base string, n int64) error {
	t.Helper()
	resp, err := c.Get(base + "?n=" + strconv.FormatInt(n, 10))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err == nil {
		require.Len(t, data, int(n))
	}
	return err
}

// S8: JSON/API answers are capped at 4 MiB, media downloads at the upload limit (25 MiB
// when unset).
func TestDepsResponseCaps(t *testing.T) {
	srv := sizedServer(t)
	d := connectors.Deps{AllowPrivateNetworks: true, MaxMediaBytes: 1000}

	require.Equal(t, int64(4<<20), int64(connectors.MaxJSONResponse))
	require.NoError(t, readAll(t, d.JSONClient(0), srv.URL, 4<<20))
	require.ErrorIs(t, readAll(t, d.JSONClient(0), srv.URL, 4<<20+1), safehttp.ErrResponseTooLarge)

	require.Equal(t, int64(1000), d.MediaLimit())
	require.NoError(t, readAll(t, d.MediaClient(0), srv.URL, 1000))
	require.ErrorIs(t, readAll(t, d.MediaClient(0), srv.URL, 1001), safehttp.ErrResponseTooLarge)

	require.Equal(t, int64(25<<20), connectors.Deps{}.MediaLimit(), "the default upload limit")
}

// S6, S8, HOOK-05: the app hands every connector the network policy, the upload limit as
// media cap and the persistent replay store.
func TestManagerPassesPolicyAndLimits(t *testing.T) {
	e := newInboundEnv(t, testkit.WithEnv(map[string]string{"PARTYLINE_PRIVATE_NETWORK_ALLOWLIST": "10.1.0.0/16"}))
	d := e.inst.Deps
	require.Equal(t, int64(1<<20), d.MaxMediaBytes, "PARTYLINE_MAX_UPLOAD_MB=1")
	want, err := safehttp.ParseAllowlist("10.1.0.0/16")
	require.NoError(t, err)
	require.Equal(t, want, d.PrivateAllowlist)
	require.False(t, d.AllowPrivateNetworks)
	require.IsType(t, &connectors.StoreReplays{}, d.Replays)
}
