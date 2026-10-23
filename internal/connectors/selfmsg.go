package connectors

import (
	"context"
	"time"

	"github.com/wnexous/partyline/internal/store/db"
)

// SelfHold bounds how long a message of the connector's own account waits for the sends to its
// chat to finish (CID-14).
const SelfHold = 10 * time.Second

// selfPoll is how often a held message looks at the sends to its chat again.
const selfPoll = 50 * time.Millisecond

// settle holds the message back until no send to the chat is running, at most SelfHold, and
// tells whether it is the echo of a message Partyline sent: the platform may report the message
// before the call that created it returns, when only the send in flight can tell (CID-14).
func (p *Pipeline) settle(ctx context.Context, chatID, platformID string) (bool, error) {
	deadline := p.d.Now().Add(SelfHold)
	for {
		sending, err := p.d.Store.R().CountSendingOutboxForChat(ctx, chatID)
		if err != nil {
			return false, err
		}
		if sending == 0 || !p.d.Now().Before(deadline) {
			break
		}
		if err := p.d.Wait(ctx, selfPoll); err != nil {
			return false, err
		}
	}
	if platformID == "" {
		return false, nil
	}
	if p.d.Sent.Has(chatID, platformID) {
		return true, nil
	}
	n, err := p.d.Store.R().CountChatOutboxExternalID(ctx, db.CountChatOutboxExternalIDParams{ChatID: chatID, ExternalID: platformID})
	return n > 0, err
}
