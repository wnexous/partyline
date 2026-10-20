package discord

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"

	"github.com/wnexous/partyline/internal/connectors"
)

var _ connectors.Reactor = (*Connector)(nil)

// React adds the bot's reaction to the target message with MessageReactionAdd (REACT-01).
func (c *Connector) React(ctx context.Context, r connectors.Reaction) error {
	if err := c.s.MessageReactionAdd(r.ChatID, r.TargetID, r.Emoji, discordgo.WithContext(ctx)); err != nil {
		return fmt.Errorf("react: %w", err)
	}
	return nil
}
