package connectors

import (
	"context"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/store/db"
)

// SelfHold bounds how long a message of the connector's own account waits for the sends to its
// chat to finish (ACC-05).
const SelfHold = 10 * time.Second

// selfPoll is how often a held message looks at the sends to its chat again.
const selfPoll = 50 * time.Millisecond

// handleSelf stores a message the connector's own account wrote (ACC-05): typed on the platform
// itself, e.g. on the phone of the account, or the echo of a message Partyline sent through it.
// Only a chat that already sits in rooms takes it: it creates no chat, no conversation and no
// person, and it is no pairing command. The echo of a send is dropped; anything else is authored
// by the connector's operator, as the owner's own account, so the owner's AIs trust it (AD-006).
func (p *Pipeline) handleSelf(ctx context.Context, src Source, m Inbound) error {
	if (strings.TrimSpace(m.Text) == "" && len(m.Media) == 0) || pairingCommand.MatchString(m.Text) {
		return nil
	}
	chat, found, err := p.findChat(ctx, src.ConnectorID, m.ChatID)
	if err != nil || !found {
		return err
	}
	chatRooms, err := p.d.Store.R().ListChatRooms(ctx, chat.ID)
	if err != nil || len(chatRooms) == 0 {
		return err
	}
	echo, err := p.settle(ctx, chat.ID, m.MessageID)
	if err != nil || echo {
		return err
	}
	roomIDs := make([]string, len(chatRooms))
	for i, r := range chatRooms {
		roomIDs[i] = r.ID
	}
	if roomIDs, err = p.pending(ctx, chat.ID, m.MessageID, roomIDs); err != nil || len(roomIDs) == 0 {
		return err
	}
	conn, err := p.d.Store.R().GetConnector(ctx, src.ConnectorID)
	if err != nil {
		return err
	}
	return p.post(ctx, src, chat, m, roomIDs, conn.OwnerID, operatorID(conn))
}

// settle holds the message back until no send to the chat is running, at most SelfHold, and
// tells whether it is the echo of a message Partyline sent: the platform may report the message
// before the call that created it returns, when only the send in flight can tell.
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
