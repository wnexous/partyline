package api

import (
	"context"
	"net/http"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/rooms"
)

// SecretDeliverer hands a secret to the live session of an AI and returns once the session wrote it
// (SR-06): agents.Gateway.DeliverSecret.
type SecretDeliverer interface {
	DeliverSecret(ctx context.Context, d agents.SecretDelivery) (string, error)
}

// errNoSecrets answers a secret given to a server that runs no agent gateway.
var errNoSecrets = httpx.NewError(http.StatusServiceUnavailable, dto.ErrorCodeUnavailable, "secrets cannot be given on this server")

// giveSecret is POST /rooms/{id}/messages/{mid}/secret (SR-05..SR-07): a person who manages the asking AI
// gives the value of a secret request, which goes to the session that asked and nowhere else. The body is
// read here only: no middleware logs a request body, the value is never part of an error, and the answer is
// the reply "🔑 NAME saved" with its structured answer, never the value.
func (s *server) giveSecret(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.GiveSecretRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if n := len(req.Value); n == 0 || n > dto.MaxSecretValue {
		return httpx.Invalid(map[string]string{"value": "must have 1 to 16384 bytes"})
	}
	if s.Secrets == nil {
		return errNoSecrets
	}
	value := req.Value
	req.Value = ""
	m, err := s.Rooms.GiveSecret(r.Context(), p, r.PathValue("id"), r.PathValue("mid"), func(ctx context.Context, t rooms.SecretTarget) error {
		_, err := s.Secrets.DeliverSecret(ctx, agents.SecretDelivery{
			AgentID: t.AgentID, Label: t.Label, RoomID: t.RoomID, MessageID: t.MessageID, Name: t.Name, Value: value,
		})
		return err
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, convert.Message(m))
	return nil
}
