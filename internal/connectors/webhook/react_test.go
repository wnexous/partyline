package webhook

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
)

// REACT-01: without an outbound URL a reaction fails like a message send.
func TestReactWithoutOutboundURLFails(t *testing.T) {
	c := New(connectors.Config{"secret": secret}, connectors.Deps{ID: "c1"})
	err := c.React(context.Background(), connectors.Reaction{ChatID: "deploys", TargetID: "in-1", Emoji: "👍"})
	require.EqualError(t, err, "no outbound URL configured")
}
