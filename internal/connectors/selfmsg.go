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
		sending, err := p.sendInFlight(ctx, chatID)
		if err != nil {
			return false, err
		}
		if !sending || !p.d.Now().Before(deadline) {
			break
		}
		if err := p.d.Wait(ctx, selfPoll); err != nil {
			return false, err
		}
	}
	return p.sentByPartyline(ctx, chatID, platformID)
}

// sendInFlight tells whether a send an AI makes through the connection to the chat is running.
func (p *Pipeline) sendInFlight(ctx context.Context, chatID string) (bool, error) {
	n, err := p.d.Store.R().CountSendingPlatformSendsForChat(ctx, chatID)
	return n > 0, err
}

// sentByPartyline tells whether Partyline sent the message with this platform id to the chat: it
// remembers the ids of the last minutes, and the record of the sends an AI made through the
// connection holds the id of the first message of each send (CID-14). A message without an id is
// never one of them.
func (p *Pipeline) sentByPartyline(ctx context.Context, chatID, platformID string) (bool, error) {
	if platformID == "" {
		return false, nil
	}
	if p.d.Sent.Has(chatID, platformID) {
		return true, nil
	}
	n, err := p.d.Store.R().CountChatPlatformSendExternalID(ctx, db.CountChatPlatformSendExternalIDParams{ChatID: chatID, ExternalID: platformID})
	return n > 0, err
}
