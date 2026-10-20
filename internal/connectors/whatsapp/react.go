package whatsapp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/wnexous/partyline/internal/connectors"
)

var _ connectors.Reactor = (*Connector)(nil)

// React sends an emoji reaction with POST /message/react and the instance token (REACT-01).
// In groups the target's sender goes in participant; DMs carry none. The target always came
// from the chat, so fromMe is false. A non-2xx answer is a failure.
func (c *Connector) React(ctx context.Context, r connectors.Reaction) error {
	inst, jid, ok := SplitChatID(r.ChatID)
	if !ok {
		return errors.New("invalid chat id")
	}
	body := map[string]any{"number": jid, "id": r.TargetID, "reaction": r.Emoji, "fromMe": false}
	if strings.HasSuffix(jid, "@g.us") && r.TargetSenderID != "" {
		body["participant"] = r.TargetSenderID
	}
	return c.instanceCall(ctx, inst, http.MethodPost, "/message/react", body, nil)
}
