package rooms

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/validation"
)

// ErrNotSecretRequest is a secret given for a message that does not ask for one: another message, a
// deleted request (deleting clears the interactive part) or a message of another room (404, SR-05).
var ErrNotSecretRequest = errors.New("the message is not a secret request")

// ErrSecretInFlight is a secret given while another person's value for the same request is on its way to
// the session (409): the request is answered once (SR-05), and the first save decides.
var ErrSecretInFlight = errors.New("someone is saving this secret right now")

var secretNamePattern = regexp.MustCompile(dto.SecretNamePattern)

// SecretTarget is where a secret goes (SR-06): the live session of the AI that posted the request, by the
// label the server recorded when it was posted ("" for the principal), and what the session is told.
type SecretTarget struct {
	AgentID   string
	Label     string
	RoomID    string
	MessageID string
	Name      string
}

// secretFlights holds the secret requests whose value is on its way to a session, so that two people
// saving at once do not both write the file (SR-05). In memory: a value is never kept anywhere.
type secretFlights struct {
	mu   sync.Mutex
	busy map[string]struct{}
}

func (f *secretFlights) claim(messageID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy == nil {
		f.busy = map[string]struct{}{}
	}
	if _, ok := f.busy[messageID]; ok {
		return false
	}
	f.busy[messageID] = struct{}{}
	return true
}

func (f *secretFlights) release(messageID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.busy, messageID)
}

// secretRequestFrom canonicalises the secret an AI asks for (SR-01..SR-03): only an AI asks, the name
// matches dto.SecretNamePattern, the text says why in 1 to dto.MaxSecretReason characters, and the message
// carries no files. A person has no session to receive the value, so a person's request is refused.
func secretRequestFrom(p authn.Principal, in PostInput) (*dto.MessageInteractive, error) {
	check := validation.Check{}
	if p.Kind != authn.KindAgent {
		check.Add("secret", "only an AI can ask for a secret: a person has no session to receive it")
		return nil, check.Err()
	}
	if !secretNamePattern.MatchString(in.Secret.Name) {
		check.Add("secret", "the name must be 1 to 64 upper-case letters, digits or underscores, starting with a letter (like EXAMPLE_API_KEY)")
	}
	text := strings.TrimSpace(in.Text)
	switch {
	case text == "":
		check.Add("text", "must say why the secret is needed")
	case utf8.RuneCountInString(text) > dto.MaxSecretReason:
		check.Add("text", "must have at most "+strconv.Itoa(dto.MaxSecretReason)+" characters in a secret request")
	}
	if len(in.Files) > 0 {
		check.Add("secret", "a secret request carries no files")
	}
	if err := check.Err(); err != nil {
		return nil, err
	}
	return &dto.MessageInteractive{V: 1, Secret: &dto.MessageSecret{Name: in.Secret.Name}}, nil
}

// secretSavedText is the text of the answer a saved secret leaves in the conversation (SR-06): the name,
// never the value.
func secretSavedText(name string) string { return "🔑 " + name + " saved" }

// GiveSecret hands the value of a secret request to the session that asked for it and records that it was
// saved (SR-05, SR-06). The caller must be a person who may post in the room and who manages the asking AI
// in its home organization, the same rule that makes a message trusted="true" for it (POS-10); 403
// otherwise. The message must be a live secret request of the room (ErrNotSecretRequest), answered by
// nobody yet (ErrAlreadyAnswered) and not being saved by someone else (ErrSecretInFlight).
//
// deliver sends the value to the session and returns once the session wrote it, or with the reason it
// did not; it is the only place the value goes, and it never comes back here. Only when deliver succeeds
// is the answer stored: a reply "🔑 NAME saved" whose structured answer is {"v":1,"secret":{"name":NAME,
// "saved":true}}, authored by the caller. Nothing else is written, logged or published.
func (s *Service) GiveSecret(ctx context.Context, p authn.Principal, roomID, messageID string, deliver func(context.Context, SecretTarget) error) (Message, error) {
	if p.Kind != authn.KindUser {
		return Message{}, access.ErrForbidden
	}
	if _, err := s.authorize(ctx, p, roomID, dto.LevelPost); err != nil {
		return Message{}, err
	}
	if err := s.checkCounterpart(ctx, roomID, p.UserID); err != nil {
		return Message{}, err
	}
	msg, err := s.store.R().GetMessageView(ctx, messageID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && msg.RoomID != roomID) {
		return Message{}, ErrNotSecretRequest
	}
	if err != nil {
		return Message{}, err
	}
	request := dto.ParseMessageInteractive(msg.InteractiveJson)
	if msg.DeletedAt.Valid || request == nil || request.Secret == nil || msg.AuthorKind != string(dto.AuthorKindAgent) {
		return Message{}, ErrNotSecretRequest
	}
	trusted, err := s.az.Authz().Trusts(ctx, p.UserID, msg.AuthorID)
	if err != nil {
		return Message{}, err
	}
	if !trusted {
		return Message{}, access.ErrForbidden
	}
	if n, err := s.store.R().CountAnswers(ctx, messageID); err != nil {
		return Message{}, err
	} else if n > 0 {
		return Message{}, ErrAlreadyAnswered
	}
	if !s.secrets.claim(messageID) {
		return Message{}, ErrSecretInFlight
	}
	defer s.secrets.release(messageID)
	name := request.Secret.Name
	if err := deliver(ctx, SecretTarget{
		AgentID: msg.AuthorID, Label: msg.AuthorLabel, RoomID: roomID, MessageID: messageID, Name: name,
	}); err != nil {
		return Message{}, err
	}
	answer, err := json.Marshal(dto.MessageAnswers{V: 1, Secret: &dto.MessageSecretAnswer{Name: name, Saved: true}})
	if err != nil {
		return Message{}, err
	}
	// The session has the file now: the answer is recorded even when the request was deleted meanwhile,
	// so the conversation says what happened.
	return s.post(context.WithoutCancel(ctx), roomID, draft{
		authorKind: dto.AuthorKindUser, authorID: p.UserID, authorUserID: p.UserID, authorIID: p.UserID,
		kind: dto.MessageKindText, text: secretSavedText(name), replyTo: messageID,
		answers: string(answer), answerOnce: true,
	})
}
