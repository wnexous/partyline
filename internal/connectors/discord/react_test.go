package discord

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
)

// REACT-01: a reaction is MessageReactionAdd(channel, target message, emoji).
func TestReactAddsMessageReaction(t *testing.T) {
	f := newFakeSession()
	c := newWithSession(f, connectors.Deps{AllowPrivateNetworks: true, ID: "c1", UserAgent: "partyline/test"})
	require.NoError(t, c.React(context.Background(), connectors.Reaction{ChatID: channelID, TargetID: "MSG42", TargetSenderID: userID, Emoji: "👍"}))
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Equal(t, []reaction{{ChannelID: channelID, MessageID: "MSG42", Emoji: "👍"}}, f.reactions)
}

func TestReactFailure(t *testing.T) {
	f := newFakeSession()
	f.reactErr = errors.New("HTTP 403 Forbidden")
	c := newWithSession(f, connectors.Deps{AllowPrivateNetworks: true, ID: "c1", UserAgent: "partyline/test"})
	err := c.React(context.Background(), connectors.Reaction{ChatID: channelID, TargetID: "MSG42", Emoji: "👍"})
	require.EqualError(t, err, "react: HTTP 403 Forbidden")
}
