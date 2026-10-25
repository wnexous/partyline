package webpush

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
)

// The connection alert's fixed values (ALERT-05, ALERT-07, ALERT-08).
const (
	// alertGap is the shortest interval between two alerts for one connection: one alert per
	// outage, and a flapping connection stays quiet (ALERT-05).
	alertGap = 30 * time.Minute
	// alertTitlePrefix starts the alert's title; the connection's name completes it (ALERT-07).
	alertTitlePrefix = "Connection down: "
	// alertTagPrefix starts the collapse tag, "conn:<id>" (ALERT-07).
	alertTagPrefix = "conn:"
	// kindWhatsApp is the connector kind whose "not connected" detail has its own reason.
	kindWhatsApp = "whatsapp"

	// The four fixed reason sentences; the platform's own detail is never sent (ALERT-08).
	reasonWhatsAppDisconnected = "The WhatsApp number was disconnected"
	reasonLoginExpired         = "Login expired"
	reasonNoOperator           = "The connection has no AI operating it"
	reasonServiceUnreachable   = "Cannot reach the service"
)

// alertReason maps the stored, already-redacted status detail to one of the four fixed sentences
// (ALERT-08). Matching is a lower-case substring test, in this order: the WhatsApp number
// disconnected, a connection without an operator, a credential failure, and the generic reason for
// everything else. The detail itself is never copied: it can carry a phone number or a server
// message.
func alertReason(kind, detail string) string {
	lower := strings.ToLower(detail)
	switch {
	case kind == kindWhatsApp && strings.Contains(lower, "not connected"):
		return reasonWhatsAppDisconnected
	case strings.Contains(lower, strings.ToLower(dto.ConnectorNoOperator)):
		return reasonNoOperator
	case strings.Contains(lower, "unauthorized"),
		strings.Contains(lower, "invalid bot token"),
		strings.Contains(lower, "authentication failed"):
		return reasonLoginExpired
	default:
		return reasonServiceUnreachable
	}
}

// alertPayload builds the service worker payload of a connection that went down (ALERT-07): the
// shared message payload with the connection's title, an empty author, the fixed reason as the
// preview, the absolute connection page and a per-connection collapse tag. It carries no
// configuration, platform error, token or chat content.
func alertPayload(public *url.URL, id, name, kind, detail string) ([]byte, error) {
	page := ""
	if public != nil {
		page = public.JoinPath("connectors", id).String()
	}
	return json.Marshal(Payload{
		V:       payloadVersion,
		Title:   alertTitlePrefix + name,
		Author:  "",
		Preview: alertReason(kind, detail),
		URL:     page,
		Tag:     alertTagPrefix + id,
	})
}

// alertRule is the in-memory outage state of one connection (ALERT-04, ALERT-05, ALERT-10,
// ALERT-11). The zero value is a connection never seen healthy in this process.
type alertRule struct {
	// down is true while the connection is known to be failing.
	down bool
	// seenUp is true once the connection was observed healthy in this process, or seeded healthy.
	// A connection never seen healthy only records a baseline, so a boot burst is silent.
	seenUp bool
	// last is when the last alert was sent for it; zero means never.
	last time.Time
}

// observe applies one status event at now and reports whether it should produce an alert. Connected
// and stopped clear a failing connection without a notification (ALERT-03, ALERT-06); connecting and
// unknown statuses change nothing; an error alerts only on a new outage of a connection seen healthy
// and outside the 30-minute cooldown (ALERT-01, ALERT-04, ALERT-05, ALERT-11).
func (r *alertRule) observe(status string, now time.Time) bool {
	switch dto.ConnectorStatus(status) {
	case dto.ConnectorStatusConnected:
		r.down = false
		r.seenUp = true
	case dto.ConnectorStatusStopped:
		r.down = false
	case dto.ConnectorStatusError:
		if r.down {
			return false
		}
		r.down = true
		if !r.seenUp {
			return false
		}
		if !r.last.IsZero() && now.Sub(r.last) < alertGap {
			return false
		}
		r.last = now
		return true
	}
	return false
}
