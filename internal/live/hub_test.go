package live

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Edge case "live sockets are re-checked at least every 60 seconds": a hub built without an
// explicit interval (as the app builds it in production) re-checks every 60 s.
func TestDefaultSessionCheckIsOneMinute(t *testing.T) {
	h := New(Deps{PublicURL: &url.URL{Scheme: "https", Host: "partyline.example.test"}})
	t.Cleanup(h.Close)
	require.Equal(t, time.Minute, h.d.SessionCheckInterval)
	require.Equal(t, time.Minute, DefaultSessionCheckInterval)
}
