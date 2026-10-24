package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/wnexous/partyline/internal/connectors"
)

var _ connectors.Reactor = (*Connector)(nil)

// React sends an emoji reaction with POST /message/react and the instance token (REACT-01). In groups
// the target's sender goes in participant, unless the account wrote the target itself (typed on its
// phone): that is fromMe, and the account needs no name; DMs carry none. A non-2xx answer is a failure.
// evolution-go answers with the message the reaction created, like any send, and its id goes to
// r.Created so that the echo of the reaction is recognized (CID-33); an answer without it is no failure.
func (c *Connector) React(ctx context.Context, r connectors.Reaction) error {
	inst, jid, ok := SplitChatID(r.ChatID)
	if !ok {
		return errors.New("invalid chat id")
	}
	body := map[string]any{"number": jid, "id": r.TargetID, "reaction": r.Emoji, "fromMe": r.TargetFromMe}
	if strings.HasSuffix(jid, "@g.us") && r.TargetSenderID != "" && !r.TargetFromMe {
		body["participant"] = r.TargetSenderID
	}
	var ack sentAck
	if err := c.instanceCall(ctx, inst, http.MethodPost, "/message/react", body, &ack); err != nil {
		return err
	}
	if ack.ID != "" && r.Created != nil {
		r.Created(ack.ID)
	}
	return nil
}

// sentAck is the id of the message evolution-go says it created, which only a caller that can do
// without it asks for: an answer that is empty or not what is expected leaves the id empty instead of
// failing the call (bestEffort).
type sentAck struct{ ID string }

func (sentAck) bestEffort() {}

func (a *sentAck) UnmarshalJSON(b []byte) error {
	var v struct {
		Data struct {
			Info struct {
				ID string `json:"ID"`
			} `json:"Info"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &v) == nil {
		a.ID = v.Data.Info.ID
	}
	return nil
}
