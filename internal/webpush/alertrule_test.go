package webpush

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
)

// whatsappDetail is the stored status detail of a WhatsApp number that dropped (CID-28): the
// platform's own reason is data and never leaves the server.
const whatsappDetail = "the WhatsApp number is not connected (Reconnecting); reconnect it in evolution-go"

// public parses a public URL for the payload tests.
func public(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

// ALERT-03, ALERT-07, ALERT-08: every branch of the reason function in match order, and the detail
// never reaches the result.
func TestAlertReason(t *testing.T) {
	tests := []struct {
		name   string
		kind   string
		detail string
		want   string
	}{
		{"WhatsApp number disconnected with the platform's reason", "whatsapp", whatsappDetail, reasonWhatsAppDisconnected},
		{"WhatsApp number disconnected without a reason", "whatsapp", "the WhatsApp number is not connected; reconnect it in evolution-go", reasonWhatsAppDisconnected},
		{"Telegram unauthorized", "telegram", "unauthorized", reasonLoginExpired},
		{"Discord invalid bot token", "discord", "invalid bot token", reasonLoginExpired},
		{"Discord authentication failed", "discord", "websocket: close 4004: Authentication failed", reasonLoginExpired},
		{"Telegram connection refused", "telegram", "dial tcp: connection refused", reasonServiceUnreachable},
		{"connection with no operator", "whatsapp", dto.ConnectorNoOperator, reasonNoOperator},
		{"an unknown kind with no detail", "webhook", "", reasonServiceUnreachable},
		{"a detail that carries a token", "telegram", "401 token abc123", reasonServiceUnreachable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := alertReason(tt.kind, tt.detail)
			require.Equal(t, tt.want, got)
			require.NotContains(t, got, "abc123", "the platform's detail never reaches the reason")
			if tt.detail != "" {
				require.NotContains(t, got, tt.detail, "the reason is a fixed sentence, not the detail")
			}
		})
	}
}

// ALERT-07: the payload is the shared message payload — version 1, the connection's title, an
// empty author, the fixed reason as the preview, the absolute connection page and the conn tag —
// and nothing else.
func TestAlertPayload(t *testing.T) {
	base := public(t, "https://partyline.example.test/base")

	raw, err := alertPayload(base, "k1", "ops", "whatsapp", whatsappDetail)
	require.NoError(t, err)

	var got Payload
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, Payload{
		V:       1,
		Title:   "Connection down: ops",
		Author:  "",
		Preview: "The WhatsApp number was disconnected",
		URL:     "https://partyline.example.test/base/connectors/k1",
		Tag:     "conn:k1",
	}, got)

	var object map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &object))
	require.Len(t, object, 6, "the payload has exactly the six message payload keys")
	for _, key := range []string{"v", "title", "author", "preview", "url", "tag"} {
		require.Contains(t, object, key)
	}
	require.NotContains(t, string(raw), "Reconnecting", "the platform's detail never reaches the wire")

	raw, err = alertPayload(base, "k2", "web", "telegram", "dial tcp: connection refused")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, "Cannot reach the service", got.Preview, "a generic detail maps to the generic reason")
	require.Equal(t, "https://partyline.example.test/base/connectors/k2", got.URL)
}

// ALERT-01, ALERT-03, ALERT-04, ALERT-05, ALERT-06, ALERT-11: every row of the design's
// transition table, including a connection never seen healthy and the exact 30-minute edge.
func TestAlertRuleTransitions(t *testing.T) {
	t0 := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	type step struct {
		status dto.ConnectorStatus
		at     time.Time
		want   bool
	}
	tests := []struct {
		name   string
		steps  []step
		down   bool
		seenUp bool
		last   time.Time
	}{
		{
			name:  "error on a zero rule only records the failing state",
			steps: []step{{dto.ConnectorStatusError, t0, false}},
			down:  true,
		},
		{
			name:   "connected on a zero rule marks it healthy",
			steps:  []step{{dto.ConnectorStatusConnected, t0, false}},
			seenUp: true,
		},
		{
			name: "connecting never clears a failing rule",
			steps: []step{
				{dto.ConnectorStatusConnecting, t0, false},
				{dto.ConnectorStatusError, t0, false},
				{dto.ConnectorStatusConnecting, t0.Add(time.Minute), false},
			},
			down: true,
		},
		{
			name: "connecting never reopens the cooldown after an alert",
			steps: []step{
				{dto.ConnectorStatusConnected, t0, false},
				{dto.ConnectorStatusError, t0, true},
				{dto.ConnectorStatusConnecting, t0.Add(time.Minute), false},
			},
			down:   true,
			seenUp: true,
			last:   t0,
		},
		{
			name: "connected clears a failing rule",
			steps: []step{
				{dto.ConnectorStatusError, t0, false},
				{dto.ConnectorStatusConnected, t0.Add(time.Minute), false},
			},
			seenUp: true,
		},
		{
			name: "stopped clears a failing rule",
			steps: []step{
				{dto.ConnectorStatusError, t0, false},
				{dto.ConnectorStatusStopped, t0.Add(time.Minute), false},
			},
		},
		{
			name: "error on a rule never seen healthy stays silent",
			steps: []step{
				{dto.ConnectorStatusStopped, t0, false},
				{dto.ConnectorStatusError, t0.Add(time.Minute), false},
			},
			down: true,
		},
		{
			name: "error on an already failing rule stays silent",
			steps: []step{
				{dto.ConnectorStatusError, t0, false},
				{dto.ConnectorStatusError, t0.Add(time.Minute), false},
			},
			down: true,
		},
		{
			name: "connected then error alerts once",
			steps: []step{
				{dto.ConnectorStatusConnected, t0, false},
				{dto.ConnectorStatusError, t0.Add(time.Minute), true},
			},
			down:   true,
			seenUp: true,
			last:   t0.Add(time.Minute),
		},
		{
			name: "a second error after an alert stays silent",
			steps: []step{
				{dto.ConnectorStatusConnected, t0, false},
				{dto.ConnectorStatusError, t0, true},
				{dto.ConnectorStatusError, t0.Add(time.Minute), false},
			},
			down:   true,
			seenUp: true,
			last:   t0,
		},
		{
			name: "a new outage 29 minutes after the alert waits",
			steps: []step{
				{dto.ConnectorStatusConnected, t0, false},
				{dto.ConnectorStatusError, t0, true},
				{dto.ConnectorStatusConnected, t0.Add(time.Minute), false},
				{dto.ConnectorStatusError, t0.Add(29 * time.Minute), false},
			},
			down:   true,
			seenUp: true,
			last:   t0,
		},
		{
			name: "a new outage 30 minutes after the alert alerts",
			steps: []step{
				{dto.ConnectorStatusConnected, t0, false},
				{dto.ConnectorStatusError, t0, true},
				{dto.ConnectorStatusConnected, t0.Add(time.Minute), false},
				{dto.ConnectorStatusError, t0.Add(30 * time.Minute), true},
			},
			down:   true,
			seenUp: true,
			last:   t0.Add(30 * time.Minute),
		},
		{
			name: "an unknown status leaves a healthy rule unchanged",
			steps: []step{
				{dto.ConnectorStatusConnected, t0, false},
				{dto.ConnectorStatus("reconnecting"), t0.Add(time.Minute), false},
			},
			seenUp: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rule alertRule
			for i, s := range tt.steps {
				require.Equal(t, s.want, rule.observe(string(s.status), s.at),
					"step %d (%s) at %s", i+1, s.status, s.at.Format(time.RFC3339))
			}
			require.Equal(t, alertRule{down: tt.down, seenUp: tt.seenUp, last: tt.last}, rule)
		})
	}
}
