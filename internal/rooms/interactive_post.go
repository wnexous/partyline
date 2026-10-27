package rooms

import (
	"context"
	"database/sql"
	"errors"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// checkAnswer applies the rules that need the stored message before an answer is inserted (QR-05,
// QR-08, QR-09): the reply_to is set, the request carries no text and no files, the target carries a
// question, the caller is not its author, the question is not deleted and the caller has no live
// answer to it. It is called inside the post transaction, so the checks and the insert see the same
// rows. A message that does not carry an answer passes untouched.
func (s *Service) checkAnswer(ctx context.Context, q *db.Queries, roomID string, d draft) error {
	if d.answers == "" {
		return nil
	}
	check := validation.Check{}
	if d.replyTo == "" {
		check.Add("answers", "must reply to a question")
		return check.Err()
	}
	if d.originalText != "" {
		check.Add("text", "must be empty when answering a question")
		return check.Err()
	}
	if len(d.files) > 0 {
		check.Add("answers", "must not carry files")
		return check.Err()
	}
	msg, err := q.GetMessageView(ctx, d.replyTo)
	if errors.Is(err, sql.ErrNoRows) {
		check.Add("answers", "must answer a message of the room")
		return check.Err()
	}
	if err != nil {
		return err
	}
	if msg.RoomID != roomID {
		check.Add("answers", "must answer a message of the room")
		return check.Err()
	}
	if msg.DeletedAt.Valid {
		return ErrMessageDeleted
	}
	question := dto.ParseMessageInteractive(msg.InteractiveJson)
	if question == nil {
		check.Add("answers", "must answer a message that asks a question")
		return check.Err()
	}
	if msg.AuthorIid == d.authorIID {
		return access.ErrForbidden
	}
	n, err := q.CountAnswersByAuthor(ctx, db.CountAnswersByAuthorParams{
		QuestionID: d.replyTo, AuthorIid: d.authorIID,
	})
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrAlreadyAnswered
	}
	return nil
}
