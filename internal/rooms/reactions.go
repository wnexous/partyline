package rooms

import (
	"context"
	"database/sql"
	"errors"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// ErrReactionLimit is returned for a new emoji on a message that carries dto.MaxMessageEmoji different
// ones already (RCT-04).
var ErrReactionLimit = errors.New("rooms: a message has at most 12 different reactions")

// Reaction is one emoji of a message and the intelligences that used it, in the order they reacted.
type Reaction struct {
	Emoji string
	By    []string
}

// groupReactions folds the rows of ListReactionsByMessages, which come in the order the reactions
// were made, into the reactions of each message: the emoji in the order each was first used, each
// with whoever used it in order.
func groupReactions(rows []db.ListReactionsByMessagesRow) map[string][]Reaction {
	out := map[string][]Reaction{}
	for _, r := range rows {
		list := out[r.MessageID]
		at := -1
		for i := range list {
			if list[i].Emoji == r.Emoji {
				at = i
				break
			}
		}
		if at < 0 {
			list = append(list, Reaction{Emoji: r.Emoji})
			at = len(list) - 1
		}
		list[at].By = append(list[at].By, r.IntelligenceID)
		out[r.MessageID] = list
	}
	return out
}

// React adds the caller's reaction to a message of the room (RCT-03). The caller needs post on the
// room, as for a message, and the emoji must be exactly one. Reacting twice with the same emoji
// changes nothing. It answers the message's reactions as they are afterwards.
func (s *Service) React(ctx context.Context, p authn.Principal, roomID, messageID, emoji string) ([]Reaction, error) {
	return s.react(ctx, p, roomID, messageID, emoji, true)
}

// Unreact takes the caller's reaction back (RCT-03); taking back one that is not there changes
// nothing. It answers the message's reactions as they are afterwards.
func (s *Service) Unreact(ctx context.Context, p authn.Principal, roomID, messageID, emoji string) ([]Reaction, error) {
	return s.react(ctx, p, roomID, messageID, emoji, false)
}

func (s *Service) react(ctx context.Context, p authn.Principal, roomID, messageID, emoji string, add bool) ([]Reaction, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelPost); err != nil {
		return nil, err
	}
	if !validation.OneEmoji(emoji) { // RCT-04
		check := validation.Check{}
		check.Add("emoji", "must be exactly one emoji")
		return nil, check.Err()
	}
	// A reaction is taking part, like a message: not in a DM that is history, not with a disabled user.
	if err := s.checkCounterpart(ctx, roomID, p.IID()); err != nil {
		return nil, err
	}
	changed := false
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		m, err := q.GetMessageView(ctx, messageID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && m.RoomID != roomID) {
			return access.ErrNotFound // an id says nothing about the rooms the caller cannot see
		}
		if err != nil {
			return err
		}
		if !add {
			rows, err := q.RemoveMessageReaction(ctx, db.RemoveMessageReactionParams{MessageID: messageID, IntelligenceID: p.IID(), Emoji: emoji})
			changed = rows > 0
			return err
		}
		if m.DeletedAt.Valid { // a placeholder has no reactions and takes none
			return ErrMessageDeleted
		}
		uses, err := q.CountMessageEmojiUses(ctx, db.CountMessageEmojiUsesParams{MessageID: messageID, Emoji: emoji})
		if err != nil {
			return err
		}
		if uses == 0 { // an emoji nobody used yet takes one of the places (RCT-04)
			kinds, err := q.CountMessageEmoji(ctx, messageID)
			if err != nil {
				return err
			}
			if kinds >= dto.MaxMessageEmoji {
				return ErrReactionLimit
			}
		}
		rows, err := q.AddMessageReaction(ctx, db.AddMessageReactionParams{
			MessageID: messageID, IntelligenceID: p.IID(), Emoji: emoji, CreatedAt: s.now().UnixMilli(),
		})
		changed = rows > 0
		return err
	})
	if err != nil {
		return nil, err
	}
	if changed {
		s.bus.Publish(events.ReactionsChanged{RoomID: roomID, MessageID: messageID})
	}
	rows, err := s.store.R().ListReactionsByMessages(ctx, []string{messageID})
	if err != nil {
		return nil, err
	}
	return groupReactions(rows)[messageID], nil
}
