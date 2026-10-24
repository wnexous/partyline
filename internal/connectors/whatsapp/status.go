package whatsapp

import (
	"context"
	"time"

	"github.com/wnexous/partyline/internal/connectors"
)

// notConnectedDetail is the status detail of a connection whose number evolution-go reports as not
// connected (reconnecting, closed, logged out): the API and the WebSocket work, but no message can
// arrive until the number is back (WA-06).
const notConnectedDetail = "the WhatsApp number is not connected; reconnect it in evolution-go"

// report is a status with its detail, as Handler.Status takes them.
type report struct {
	status connectors.Status
	detail string
}

// state is what the connection reports for the instance list insts (WA-06): an error while
// evolution-go says its number is not connected, otherwise connected, with the idle warning when
// evolution-go does not have the instance. An instance that does not say whether it is connected
// counts as connected: nothing is known, so nothing is claimed.
func (c *Connector) state(insts []instance) report {
	for _, inst := range insts {
		if inst.Name == c.instance && inst.Connected != nil && !*inst.Connected {
			return report{connectors.StatusError, notConnectedDetail}
		}
	}
	return report{connectors.StatusConnected, c.idleWarning(insts)}
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
