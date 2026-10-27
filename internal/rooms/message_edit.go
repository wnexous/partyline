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

// ErrMessageDeleted is returned when a deleted message is edited: its text is gone for good (409 conflict,
// EDT-03).
var ErrMessageDeleted = errors.New("the message was deleted")

// writtenBy is the intelligence that wrote the message from Partyline: the user or the AI that posted it. A
// system notice has none, and neither has a message a chat wrote - however its sender is linked to a user (the
// message has an author_iid then), that user did not write it here - so nobody is their author (EDT-02, EDT-03).
func writtenBy(m db.MessageView) string {
	switch dto.AuthorKind(m.AuthorKind) {
	case dto.AuthorKindUser, dto.AuthorKindAgent:
		if m.AuthorIid != "" {
			return m.AuthorIid
		}
		return m.AuthorID
	}
	return ""
}

// target loads the message of a room the caller is acting on; access.ErrNotFound when it does not exist or
// belongs to another room, so that an id says nothing about rooms the caller cannot see.
func (s *Service) target(ctx context.Context, roomID, messageID string) (Message, error) {
	m, err := s.message(ctx, messageID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && m.RoomID != roomID) {
		return Message{}, access.ErrNotFound
	}
	return m, err
}

// EditMessage replaces the text of a message its author wrote (EDT-01): the message keeps its id, sequence
// number, author, time and attachments, and gets edited_at. The caller must be the author - a person, or an AI as
// itself - and still able to post in the room (EDT-02); the message must be text and not deleted (EDT-03); the
// text follows MSG-02 except that a message with attachments may have none (EDT-04); a DM that is history, or whose
// other participant is disabled, refuses it as it refuses a post (EDT-05). The same text changes nothing. There is
// no time limit. Whoever cannot view the room, or a message that is not in it, gets access.ErrNotFound.
func (s *Service) EditMessage(ctx context.Context, p authn.Principal, roomID, messageID, text string) (Message, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelPost); err != nil {
		return Message{}, err
	}
	m, err := s.target(ctx, roomID, messageID)
	if err != nil {
		return Message{}, err
	}
	if p.IID() == "" || writtenBy(m.MessageView) != p.IID() {
		return Message{}, access.ErrForbidden
	}
	if m.DeletedAt.Valid {
		return Message{}, ErrMessageDeleted
	}
	if m.Kind != string(dto.MessageKindText) {
		check := validation.Check{}
		check.Add("message", "only a text message can be edited")
		return Message{}, check.Err()
	}
	if m.AnswersJson != "" {
		check := validation.Check{}
		check.Add("text", "an answer is deleted and posted again, never edited")
		return Message{}, check.Err()
	}
	if err := s.checkCounterpart(ctx, roomID, p.IID()); err != nil {
		return Message{}, err
	}
	if err := validateText(text, len(m.Attachments) > 0); err != nil {
		return Message{}, err
	}

	var prev string
	var changed bool
	err = s.store.WithTx(ctx, func(q *db.Queries) error {
		// The text it has now: another edit or a delete may have come since the message was read.
		cur, err := q.GetMessageView(ctx, messageID)
		if err != nil {
			return err
		}
		if cur.DeletedAt.Valid {
			return ErrMessageDeleted
		}
		prev, changed = cur.Text, cur.Text != text
		if !changed {
			return nil
		}
		_, err = q.EditMessageText(ctx, db.EditMessageTextParams{
			Text: text, EditedAt: sql.NullInt64{Int64: s.now().UnixMilli(), Valid: true}, ID: messageID,
		})
		return err
	})
	if err != nil {
		return Message{}, err
	}
	if changed {
		s.bus.Publish(events.MessageUpdated{RoomID: roomID, MessageID: messageID, Seq: m.Seq, PrevText: prev})
	}
	return s.message(ctx, messageID)
}

// DeleteMessage turns a message into its placeholder (DLT-01): its text and its attachment rows are removed in
// one transaction, deleted_at is set and edited_at cleared, and the row keeps its id, sequence number, author,
// time and reply_to, so that replies and the order stay. The blobs nothing references any more are released as a
// deleted room's are (DLT-04). The author may delete, and in a group so may whoever manages it; in a DM only the
// author (DLT-02). A system notice is never deleted, and a DM that is history refuses it; a DM whose other
// participant is disabled does not, for taking something back is not posting (DLT-03). Deleting a deleted message
// returns the placeholder and publishes nothing. Whoever cannot view the room gets access.ErrNotFound and whoever
// may not delete the message access.ErrForbidden.
func (s *Service) DeleteMessage(ctx context.Context, p authn.Principal, roomID, messageID string) (Message, error) {
	level, err := s.authorize(ctx, p, roomID, dto.LevelView)
	if err != nil {
		return Message{}, err
	}
	m, err := s.target(ctx, roomID, messageID)
	if err != nil {
		return Message{}, err
	}
	room, err := s.store.R().GetRoom(ctx, roomID)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, access.ErrNotFound
	}
	if err != nil {
		return Message{}, err
	}
	author := p.IID() != "" && writtenBy(m.MessageView) == p.IID()
	manager := room.Kind != string(dto.ConversationKindDM) && access.AtLeast(level, dto.LevelManage)
	if !author && !manager {
		return Message{}, access.ErrForbidden
	}
	if m.AuthorKind == string(dto.AuthorKindSystem) {
		check := validation.Check{}
		check.Add("message", "a system message cannot be deleted")
		return Message{}, check.Err()
	}
	if err := s.checkCounterpart(ctx, roomID, p.IID()); err != nil && !errors.Is(err, ErrCounterpartDisabled) {
		return Message{}, err
	}
	if m.DeletedAt.Valid {
		return m, nil
	}

	var shas []string
	var prev string
	var changed bool
	err = s.store.WithTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetMessageView(ctx, messageID)
		if err != nil {
			return err
		}
		if cur.DeletedAt.Valid {
			return nil // deleted meanwhile
		}
		files, err := q.ListAttachmentsByMessages(ctx, []string{messageID})
		if err != nil {
			return err
		}
		for _, f := range files {
			shas = append(shas, f.Sha256)
		}
		if err := q.DeleteMessageAttachments(ctx, messageID); err != nil {
			return err
		}
		if err := q.DeleteMessageReactions(ctx, messageID); err != nil { // RCT-07
			return err
		}
		n, err := q.MarkMessageDeleted(ctx, db.MarkMessageDeletedParams{
			DeletedAt: sql.NullInt64{Int64: s.now().UnixMilli(), Valid: true}, ID: messageID,
		})
		prev, changed = cur.Text, n > 0
		return err
	})
	if err != nil {
		return Message{}, err
	}
	if changed {
		s.ReleaseBlobs(ctx, shas)
		s.bus.Publish(events.MessageUpdated{RoomID: roomID, MessageID: messageID, Seq: m.Seq, Deleted: true, PrevText: prev})
	}
	return s.message(ctx, messageID)
}
