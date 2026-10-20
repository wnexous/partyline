package whatsapp

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
)

// REACT-01: a group reaction goes to /message/react with the instance token, the target id,
// the emoji, fromMe false and the target's sender as participant.
func TestReactInGroupNamesParticipant(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	err := c.React(context.Background(), connectors.Reaction{
		ChatID: ChatID(testInstance, groupJID), TargetID: "ORIG1", TargetSenderID: peerJID, Emoji: "👍",
	})
	require.NoError(t, err)
	req := f.next("/message/react")
	require.Equal(t, http.MethodPost, req.Method)
	require.Equal(t, acmeToken, req.APIKey)
	require.Equal(t, userAgent, req.UserAgent)
	require.Equal(t, map[string]any{
		"number": groupJID, "id": "ORIG1", "reaction": "👍", "fromMe": false, "participant": peerJID,
	}, req.Body)
}

// REACT-01: a DM reaction carries no participant.
func TestReactInDMHasNoParticipant(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	err := c.React(context.Background(), connectors.Reaction{
		ChatID: ChatID(testInstance, peerJID), TargetID: "ORIG2", TargetSenderID: peerJID, Emoji: "❤️",
	})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"number": peerJID, "id": "ORIG2", "reaction": "❤️", "fromMe": false}, f.next("/message/react").Body)
}

// A non-2xx answer fails the reaction without the token in the error.
func TestReactFailure(t *testing.T) {
	f := newFakeEvolution(t)
	f.sendErr = http.StatusInternalServerError
	c := newConnector(t, f)
	err := c.React(context.Background(), connectors.Reaction{ChatID: ChatID(testInstance, groupJID), TargetID: "ORIG1", TargetSenderID: peerJID, Emoji: "👍"})
	require.ErrorContains(t, err, "500")
	require.NotContains(t, err.Error(), acmeToken)
}
