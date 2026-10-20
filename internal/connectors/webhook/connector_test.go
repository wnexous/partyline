package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
)

const secret = "synthetic-webhook-secret"

// startHook returns a connector at a fixed clock whose Run is active.
func startHook(t *testing.T, now *time.Time) (*Connector, *connectortest.Recorder) {
	t.Helper()
	c := New(connectors.Config{"secret": secret}, connectors.Deps{ID: "c1"})
	c.now = func() time.Time { return *now }
	rec := connectortest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, rec) }()
	t.Cleanup(func() {
		cancel()
		require.ErrorIs(t, <-done, context.Canceled)
	})
	require.Equal(t, connectors.StatusConnected, rec.NextStatus(t))
	return c, rec
}

func request(at time.Time, body string) connectors.HookRequest {
	ts := strconv.FormatInt(at.Unix(), 10)
	return connectors.HookRequest{Timestamp: ts, Signature: Sign(secret, ts, []byte(body)), Body: []byte(body)}
}

const body = `{"chat_id":"alerts","sender_id":"ci","text":"hi"}`

// HOOK-01: the signature is sha256= + hex(HMAC-SHA256(secret, timestamp + "." + body)).
func TestSign(t *testing.T) {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("1792497600." + body))
	require.Equal(t, "sha256="+hex.EncodeToString(mac.Sum(nil)), Sign(secret, "1792497600", []byte(body)))
}

// HOOK-02: timestamps up to 5 minutes away are accepted, older or newer ones refused.
func TestTimestampWindow(t *testing.T) {
	now := time.Unix(1792497600, 0)
	c, rec := startHook(t, &now)
	require.NoError(t, c.ReceiveHook(context.Background(), request(now.Add(-300*time.Second), body)))
	require.Equal(t, "hi", rec.NextInbound(t).Text)
	require.ErrorIs(t, c.ReceiveHook(context.Background(), request(now.Add(-301*time.Second), body)), connectors.ErrBadSignature)
	require.ErrorIs(t, c.ReceiveHook(context.Background(), request(now.Add(301*time.Second), body)), connectors.ErrBadSignature)
	require.True(t, rec.NoInbound())
}

// HOOK-01 payload mapping.
func TestReceiveHookMapsPayload(t *testing.T) {
	now := time.Unix(1792497600, 0)
	c, rec := startHook(t, &now)
	require.NoError(t, c.ReceiveHook(context.Background(), request(now,
		`{"chat_id":"alerts","chat_title":"Alerts","sender_id":"ci","sender_name":"CI","text":"build failed","message_id":"m-9"}`)))
	in := rec.NextInbound(t)
	require.Equal(t, connectors.Inbound{
		ChatID: "alerts", ChatTitle: "Alerts", ChatKind: "channel", MessageID: "m-9", SenderID: "ci", SenderName: "CI",
		Kind: "text", Text: "build failed", At: now,
	}, in)
}

// 60 requests per minute are accepted; more are refused until the next minute.
func TestRateLimit(t *testing.T) {
	now := time.Unix(1792497600, 0)
	c, _ := startHook(t, &now)
	for i := range RateLimit {
		require.NoError(t, c.ReceiveHook(context.Background(), request(now, body)), "request %d", i+1)
	}
	require.ErrorIs(t, c.ReceiveHook(context.Background(), request(now, body)), connectors.ErrRateLimited)
	now = now.Add(time.Minute)
	require.NoError(t, c.ReceiveHook(context.Background(), request(now, body)))
}

// The secret is generated when left empty, and kept when given.
func TestPrepareGeneratesSecret(t *testing.T) {
	cfg, gen := prepare(connectors.Config{})
	require.Equal(t, []string{"secret"}, gen)
	require.Regexp(t, `^[0-9a-f]{64}$`, cfg.String("secret"))
	other, _ := prepare(connectors.Config{})
	require.NotEqual(t, cfg.String("secret"), other.String("secret"))

	cfg, gen = prepare(connectors.Config{"secret": "chosen"})
	require.Empty(t, gen)
	require.Equal(t, "chosen", cfg.String("secret"))

	k := Kind()
	require.True(t, k.InboundHook)
	_, errs := k.Validate(connectors.Config{"outbound_url": "not a url", "secret": "x"})
	require.Equal(t, map[string]string{"outbound_url": "invalid url"}, errs)
}
