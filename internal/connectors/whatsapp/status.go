package whatsapp

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/connectors"
)

// notConnectedDetail is the status detail of a connection whose number evolution-go reports as not
// connected (reconnecting, closed, logged out): the API and the WebSocket work, but no message can
// arrive until the number is back (WA-06). When evolution-go says why, the reason goes in
// parentheses after the fact (CID-28).
const notConnectedDetail = "the WhatsApp number is not connected; reconnect it in evolution-go"

// maxReason is the longest reason shown, in characters: evolution-go's own reasons are a sentence at
// most, and the detail is stored and shown on the connection's page.
const maxReason = 120

// report is a status with its detail, as Handler.Status takes them.
type report struct {
	status connectors.Status
	detail string
}

// state is what the connection reports for the instance list insts (WA-06): an error while
// evolution-go says its number is not connected, with the reason it gives (CID-28), otherwise
// connected, with the idle warning when evolution-go does not have the instance. An instance that
// does not say whether it is connected counts as connected: nothing is known, so nothing is
// claimed; and the flag decides, so a reason left over from before is not shown for a number that is
// connected.
func (c *Connector) state(insts []instance) report {
	for _, inst := range insts {
		if inst.Name == c.instance && inst.Connected != nil && !*inst.Connected {
			return report{connectors.StatusError, disconnectedDetail(string(inst.DisconnectReason))}
		}
	}
	return report{connectors.StatusConnected, c.idleWarning(insts)}
}

// disconnectedDetail is the status detail for a number that is not connected, with the reason
// evolution-go gave for it when it gave one that says something.
func disconnectedDetail(why string) string {
	why = cleanReason(why)
	if why == "" {
		return notConnectedDetail
	}
	return "the WhatsApp number is not connected (" + why + "); reconnect it in evolution-go"
}

// cleanReason makes the reason fit in a status detail. It comes from the server the connection
// points at, so it is data: white space, control and formatting characters (a newline, an escape
// sequence, a zero-width space) become single spaces, the full stop that ends a sentence goes because
// the detail goes on, and more than maxReason characters are cut with an ellipsis.
func cleanReason(s string) string {
	words := strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError
	})
	s = strings.TrimRight(strings.Join(words, " "), ". ")
	if utf8.RuneCountInString(s) > maxReason {
		s = strings.TrimRight(string([]rune(s)[:maxReason-1]), " ") + "…"
	}
	return s
}

// watch looks at the instance every statusInterval and tells h when what the connection reports
// changes from last (WA-06): evolution-go can lose its number while its API and its WebSocket keep
// working, which only its instance list shows. It returns nil once ctx is done and the error of a
// check that failed, which ends Run as a failed check at start does.
func (c *Connector) watch(ctx context.Context, h connectors.Handler, last report) error {
	for c.wait(ctx, statusInterval) == nil {
		insts, err := c.refreshInstances(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		if next := c.state(insts); next != last {
			c.log.Info("whatsapp: instance status changed", "instance", c.instance, "status", string(next.status))
			h.Status(next.status, next.detail)
			last = next
		}
	}
	return nil
}

// sleep waits for d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
