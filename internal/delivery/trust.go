package delivery

import (
	"context"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
)

// Trust answers whether a message written by authorIID is trusted by the AI agentID (POS-10, AD-018):
// true only when the author manages the AI in the AI's home organization. orgs.Session is a Trust.
type Trust interface {
	Trusts(ctx context.Context, authorIID, agentID string) (bool, error)
}

// TrustSource opens the Trust of one operation: a dispatch, a backlog, a page of pending messages or
// of history. The Trust it returns may remember what it read, such as an organization's chart, for as
// long as the operation lasts, so every operation opens its own and never keeps it (AD-017: no cache
// across requests, so an answer is never stale).
type TrustSource func() Trust

// Open returns the Trust of a new operation. A nil source opens a Trust that trusts nobody: a
// component that was given no source fails closed.
func (s TrustSource) Open() Trust {
	if s == nil {
		return nobody{}
	}
	return s()
}

type nobody struct{}

func (nobody) Trusts(context.Context, string, string) (bool, error) { return false, nil }

// AuthorOf is the intelligence that wrote a message: the recorded author, else the user or the AI of
// a message written before authors were recorded. It is empty for a system message and for a chat
// sender with no intelligence, whom nobody trusts. An AI's post is its own, never its host's
// (author_user_id resolves to the host), so that an AI is trusted only as the identity it is.
func AuthorOf(v db.MessageView) string {
	if v.AuthorIid != "" {
		return v.AuthorIid
	}
	if v.AuthorKind == string(dto.AuthorKindUser) || v.AuthorKind == string(dto.AuthorKindAgent) {
		return v.AuthorID
	}
	return ""
}
