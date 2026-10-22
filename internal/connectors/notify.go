package connectors

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/conversations"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/store/db"
)

// errNotifyGone is why a copy for a user's chosen app is not sent: the chat is no longer one of
// their notify targets.
var errNotifyGone = errors.New("notify target no longer available")

// AttachmentLinkPath is the path, under the instance's public URL, of the link a copy puts for
// each attachment: the user opens it logged in.
const AttachmentLinkPath = "/api/v1/attachments/"

// sendNotify sends the copy of an AI's DM to the app its user chose (NOTI-02): "<AI name>: <text>"
// and, for each stored attachment, a line with its link on the instance; no author line (the
// prefix names the AI), no quote and no files. The chat must still be one of the user's notify
// targets (they chose it, still have the account linked and may use the connector): when it is not,
// the copy fails at once instead of being retried, since waiting cannot bring the target back. A
// connector that is down or a platform error is retried like any delivery (NOTI-03).
func (s *Sender) sendNotify(ctx context.Context, d delivery.Delivery, chat db.Chat) (string, error) {
	q := s.store.R()
	view, err := q.GetMessageView(ctx, d.MessageID)
	if err != nil {
		return "", err
	}
	room, err := q.GetRoom(ctx, view.RoomID)
	if err != nil {
		return "", err
	}
	author := view.AuthorIid
	if author == "" {
		author = view.AuthorID
	}
	user := room.DmA.String
	if user == author {
		user = room.DmB.String
	}
	ok, err := s.notifyTarget(ctx, room, user, chat.ID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", delivery.Permanent(errNotifyGone)
	}
	msgs, err := s.rooms.Details(ctx, []db.MessageView{view})
	if err != nil {
		return "", err
	}
	msg := msgs[0]
	head := msg.AuthorName + ":"
	if strings.TrimSpace(msg.Text) != "" {
		head += " " + msg.Text
	}
	lines := []string{head}
	for _, a := range msg.Attachments {
		if a.Status == string(dto.AttachmentStatusStored) {
			lines = append(lines, s.publicURL+AttachmentLinkPath+a.ID)
		}
	}
	return s.manager.Send(ctx, chat.ConnectorID, Outbound{
		ChatID: chat.ExternalID, Text: strings.Join(lines, "\n"), MessageID: msg.ID, RoomID: msg.RoomID,
		CreatedAt: time.UnixMilli(msg.CreatedAt).UTC(),
		Created:   func(id string) { s.sent.Add(chat.ID, id) },
	})
}

// notifyTarget says whether the chat is, now, where the user wants the copies of the DMs: the
// room is a DM, the user exists, is enabled and chose this chat, and the chat is still one of
// their options.
func (s *Sender) notifyTarget(ctx context.Context, room db.Room, userID, chatID string) (bool, error) {
	if room.Kind != string(dto.ConversationKindDM) {
		return false, nil
	}
	q := s.store.R()
	u, err := q.GetUser(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if u.DisabledAt.Valid || u.NotifyChatID.String != chatID {
		return false, nil
	}
	return conversations.NotifyUsable(ctx, q, userID, chatID)
}
