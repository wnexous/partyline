package rooms

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/store/db"
)

// notifyChat returns the chat that receives a copy of the message because the user it is for
// chose to be notified there (NOTI-02), or "" when the message gets no copy. A copy is made of
// an AI's text post in a DM whose other participant is an enabled user with a chosen chat; a
// user's own posts, another user's, reactions, groups and DMs with other AIs get none. A chat
// that already receives the message as a member of the room is not sent it twice.
func (s *Service) notifyChat(ctx context.Context, q *db.Queries, room db.Room, d draft, targets delivery.Targets) (string, error) {
	if d.authorKind != dto.AuthorKindAgent || d.kind == dto.MessageKindReaction || room.Kind != string(dto.ConversationKindDM) {
		return "", nil
	}
	other := room.DmA.String
	if other == d.authorID {
		other = room.DmB.String
	}
	u, err := q.GetUser(ctx, other)
	if errors.Is(err, sql.ErrNoRows) { // the other side is an AI or a person
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if u.DisabledAt.Valid || !u.NotifyChatID.Valid || slices.Contains(targets.ChatIDs, u.NotifyChatID.String) {
		return "", nil
	}
	return u.NotifyChatID.String, nil
}
