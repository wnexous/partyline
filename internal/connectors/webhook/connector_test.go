package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
	"github.com/wnexous/partyline/internal/connectors/connectortest"
	"github.com/wnexous/partyline/internal/validation"
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

// CID-12: the payload's mentions_account reaches Inbound.MentionsAccount; a non-boolean value is
// refused as an invalid field and hands nothing to the pipeline.
func TestReceiveHookMentionsAccount(t *testing.T) {
	now := time.Unix(1792497600, 0)
	c, rec := startHook(t, &now)
	require.NoError(t, c.ReceiveHook(context.Background(), request(now,
		`{"chat_id":"alerts","sender_id":"ci","text":"look","mentions_account":true}`)))
	require.True(t, rec.NextInbound(t).MentionsAccount)
	require.NoError(t, c.ReceiveHook(context.Background(), request(now,
		`{"chat_id":"alerts","sender_id":"ci","text":"quiet","mentions_account":false}`)))
	require.False(t, rec.NextInbound(t).MentionsAccount)

	err := c.ReceiveHook(context.Background(), request(now,
		`{"chat_id":"alerts","sender_id":"ci","text":"bad","mentions_account":"yes"}`))
	var invalid *validation.Error
	require.ErrorAs(t, err, &invalid)
	require.Equal(t, map[string]string{"mentions_account": "must be a boolean"}, invalid.Fields)
	require.True(t, rec.NoInbound(), "an invalid payload is not a message")
}

// numbered is a distinct valid payload.
func numbered(i int) string {
	return `{"chat_id":"alerts","sender_id":"ci","text":"hi","message_id":"m-` + strconv.Itoa(i) + `"}`
}

// HOOK-04: 60 authenticated requests per minute are accepted; more are refused until the
// next minute.
func TestRateLimit(t *testing.T) {
	now := time.Unix(1792497600, 0)
	c, _ := startHook(t, &now)
	for i := range RateLimit {
		require.NoError(t, c.ReceiveHook(context.Background(), request(now, numbered(i))), "request %d", i+1)
	}
	require.ErrorIs(t, c.ReceiveHook(context.Background(), request(now, numbered(RateLimit))), connectors.ErrRateLimited)
	now = now.Add(time.Minute)
	require.NoError(t, c.ReceiveHook(context.Background(), request(now, numbered(RateLimit+1))))
}

// HOOK-04: requests with a bad signature do not use the connector's budget; they have a
// budget of their own per client IP, which resets every minute.
func TestInvalidRequestsHaveTheirOwnBudget(t *testing.T) {
	now := time.Unix(1792497600, 0)
	c, rec := startHook(t, &now)
	forged := request(now, body)
	forged.Signature = Sign("wrong-secret", forged.Timestamp, forged.Body)
	forged.RemoteIP = "203.0.113.7"
	for i := range 100 {
		want := connectors.ErrBadSignature
		if i >= InvalidRateLimit {
			want = connectors.ErrRateLimited
		}
		require.ErrorIs(t, c.ReceiveHook(context.Background(), forged), want, "invalid request %d", i+1)
	}
	other := forged
	other.RemoteIP = "203.0.113.8"
	require.ErrorIs(t, c.ReceiveHook(context.Background(), other), connectors.ErrBadSignature, "another IP has its own budget")

	valid := request(now, body)
	valid.RemoteIP = forged.RemoteIP
	require.NoError(t, c.ReceiveHook(context.Background(), valid), "invalid requests left the budget untouched")
	require.Equal(t, "hi", rec.NextInbound(t).Text)

	now = now.Add(time.Minute)
	forged = request(now, body)
	forged.Signature, forged.RemoteIP = Sign("wrong-secret", forged.Timestamp, forged.Body), "203.0.113.7"
	require.ErrorIs(t, c.ReceiveHook(context.Background(), forged), connectors.ErrBadSignature, "a new minute")
}

// HOOK-05: a signed request is accepted once; the same request again is refused for as
// long as its timestamp would pass (here accepted at the earliest moment, replayed at the
// latest), and is never handed to the pipeline twice.
func TestReplayIsRefused(t *testing.T) {
	now := time.Unix(1792497600, 0)
	c, rec := startHook(t, &now)
	req := request(now.Add(MaxSkew), body)
	require.NoError(t, c.ReceiveHook(context.Background(), req))
	require.Equal(t, "hi", rec.NextInbound(t).Text)
	require.ErrorIs(t, c.ReceiveHook(context.Background(), req), connectors.ErrReplayed)

	now = now.Add(2 * MaxSkew)
	require.ErrorIs(t, c.ReceiveHook(context.Background(), req), connectors.ErrReplayed, "still within the timestamp window")
	now = now.Add(time.Second)
	require.ErrorIs(t, c.ReceiveHook(context.Background(), req), connectors.ErrBadSignature, "the timestamp itself expired")
	require.True(t, rec.NoInbound())

	require.NoError(t, c.ReceiveHook(context.Background(), request(now, body)), "the same body with a new timestamp is a new request")
}

// HOOK-04, HOOK-05: the replay check comes before the budget. A replay of an accepted
// request is refused as a replay (409) even when the budget is spent, and replays never use
// the budget: with budget left, the remaining fresh requests are all accepted after them.
func TestReplayIsRefusedBeforeTheBudget(t *testing.T) {
	now := time.Unix(1792497600, 0)
	c, rec := startHook(t, &now)
	first := request(now, numbered(0))
	require.NoError(t, c.ReceiveHook(context.Background(), first))
	for range 2 * RateLimit {
		require.ErrorIs(t, c.ReceiveHook(context.Background(), first), connectors.ErrReplayed)
	}
	for i := 1; i < RateLimit; i++ {
		require.NoError(t, c.ReceiveHook(context.Background(), request(now, numbered(i))), "fresh request %d", i+1)
	}
	require.ErrorIs(t, c.ReceiveHook(context.Background(), request(now, numbered(RateLimit))), connectors.ErrRateLimited,
		"the budget is now spent")

	for _, i := range []int{0, RateLimit - 1} {
		require.ErrorIs(t, c.ReceiveHook(context.Background(), request(now, numbered(i))), connectors.ErrReplayed,
			"a replay with the budget spent is still a replay (request %d)", i+1)
	}
	for i := range RateLimit {
		require.Equal(t, "m-"+strconv.Itoa(i), rec.NextInbound(t).MessageID)
	}
	require.True(t, rec.NoInbound())
}

// HOOK-02, HOOK-05: the signature header is trimmed and its hex is case-insensitive, so a
// padded or upper-cased copy of an accepted request is a replay, not a new request.
func TestReplayDetectionNormalizesTheSignature(t *testing.T) {
	now := time.Unix(1792497600, 0)
	c, rec := startHook(t, &now)
	req := request(now, body)
	require.NoError(t, c.ReceiveHook(context.Background(), req))
	require.Equal(t, "hi", rec.NextInbound(t).Text)

	padded := req
	padded.Signature = "  " + req.Signature + "\t\n"
	require.ErrorIs(t, c.ReceiveHook(context.Background(), padded), connectors.ErrReplayed)
	upper := req
	upper.Signature = strings.ToUpper(req.Signature)
	require.ErrorIs(t, c.ReceiveHook(context.Background(), upper), connectors.ErrReplayed)
	require.True(t, rec.NoInbound())

	fresh := request(now, `{"chat_id":"alerts","sender_id":"ci","text":"again"}`)
	fresh.Signature = " " + strings.ToUpper(fresh.Signature)
	require.NoError(t, c.ReceiveHook(context.Background(), fresh), "a new request with an upper-cased, padded signature is valid")
	require.Equal(t, "again", rec.NextInbound(t).Text)
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
