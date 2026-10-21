package delivery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
)

// MemberPrefs are an agent member's delivery settings (MEM-03, MEM-04).
type MemberPrefs struct {
	// Delivery is all (also when empty), mentions or trusted.
	Delivery dto.DeliveryMode
	// Names are mentioned as "@<name>": the member's display name and the agent name (and,
	// through AgentPrefs, the agent's display name).
	Names []string
	// Keywords count as mentions on word boundaries.
	Keywords []string
}

// Prefs builds the prefs of an agent member from its stored settings.
func Prefs(agentName, displayName, delivery, keywordsJSON string) MemberPrefs {
	var keywords []string
	_ = json.Unmarshal([]byte(keywordsJSON), &keywords)
	return MemberPrefs{Delivery: dto.DeliveryMode(delivery), Names: []string{displayName, agentName}, Keywords: keywords}
}

// AgentDisplayName is the name people read for an agent: the owner's rename, else the
// readable name of a temporary agent, else the agent name (ID-01, ID-04).
func AgentDisplayName(a db.Agent) string {
	switch {
	case a.DisplayName != "":
		return a.DisplayName
	case a.AutoName != "":
		return a.AutoName
	default:
		return a.Name
	}
}

// AgentPrefs builds the prefs of agent a's membership: "@<name>" also matches the agent's
// display name (ID-04).
func AgentPrefs(a db.Agent, memberDisplayName, delivery, keywordsJSON string) MemberPrefs {
	p := Prefs(a.Name, memberDisplayName, delivery, keywordsJSON)
	p.Names = append(p.Names, AgentDisplayName(a))
	return p
}

// ProjectPrefs builds the prefs of a project membership as its active session a receives
// them: "@<name>" also matches the project name.
func ProjectPrefs(a db.Agent, projectName, memberDisplayName, delivery, keywordsJSON string) MemberPrefs {
	p := AgentPrefs(a, memberDisplayName, delivery, keywordsJSON)
	p.Names = append(p.Names, projectName)
	return p
}

// Delivered describes a message as one agent sees it.
type Delivered struct {
	Text string
	// Trusted is the message's trust for this agent (DLV-06).
	Trusted bool
	// ChatKind is the kind of the authoring chat (group, dm, channel); empty for other authors.
	ChatKind string
	// ReplyToAuthorIsMe is set when the message replies to one of this agent's messages.
	ReplyToAuthorIsMe bool
}

// Wants reports whether an agent member with prefs m receives message d (MEM-03, MEM-04):
// all receives everything; trusted only trusted messages; mentions only DM chat messages,
// replies to its messages, "@<name>" mentions (case-insensitive) and keywords, both on word
// boundaries.
func Wants(m MemberPrefs, d Delivered) bool {
	switch m.Delivery {
	case dto.DeliveryModeTrusted:
		return d.Trusted
	case dto.DeliveryModeMentions:
		if d.ChatKind == string(dto.ChatKindDM) || d.ReplyToAuthorIsMe {
			return true
		}
		for _, name := range m.Names {
			if name != "" && containsWord(d.Text, "@"+name) {
				return true
			}
		}
		for _, k := range m.Keywords {
			if k != "" && containsWord(d.Text, k) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

// containsWord reports whether text contains word, ignoring case, with no letter, digit or
// underscore right before or after it.
func containsWord(text, word string) bool {
	lt, lw := strings.ToLower(text), strings.ToLower(word)
	for i := 0; i <= len(lt); {
		j := strings.Index(lt[i:], lw)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(lw)
		before, _ := utf8.DecodeLastRuneInString(lt[:start])
		after, _ := utf8.DecodeRuneInString(lt[end:])
		if (start == 0 || !isWordRune(before)) && (end == len(lt) || !isWordRune(after)) {
			return true
		}
		_, size := utf8.DecodeRuneInString(lt[start:])
		i = start + size
	}
	return false
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// Facts are what the delivery filter needs to know about a stored message.
type Facts struct {
	Text         string
	AuthorUserID string
	// ChatKind is the authoring chat's kind; empty for other authors.
	ChatKind string
	// ReplyAuthorKind and ReplyAuthorID name the author of the replied message.
	ReplyAuthorKind, ReplyAuthorID string
	// ReplyAuthorProjectID is the project of the replied message's agent author, if any.
	ReplyAuthorProjectID string
}

// LoadFacts reads the chat kind and the replied message's author of a stored message.
func LoadFacts(ctx context.Context, q *db.Queries, v db.MessageView) (Facts, error) {
	f := Facts{Text: v.Text, AuthorUserID: v.AuthorUserID}
	if v.AuthorKind == string(dto.AuthorKindChat) {
		chat, err := q.GetChat(ctx, v.AuthorID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return f, err
		}
		f.ChatKind = chat.Kind
	}
	if v.ReplyTo != "" {
		r, err := q.GetMessageView(ctx, v.ReplyTo)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return f, err
		}
		f.ReplyAuthorKind, f.ReplyAuthorID = r.AuthorKind, r.AuthorID
		if r.AuthorKind == string(dto.AuthorKindAgent) {
			a, err := q.GetAgent(ctx, r.AuthorID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return f, err
			}
			f.ReplyAuthorProjectID = a.ProjectID.String
		}
	}
	return f, nil
}

// WantedBy applies Wants for the agent (id, owner) with prefs.
func (f Facts) WantedBy(agentID, ownerID string, prefs MemberPrefs) bool {
	return Wants(prefs, Delivered{
		Text: f.Text, Trusted: Trusted(f.AuthorUserID, ownerID), ChatKind: f.ChatKind,
		ReplyToAuthorIsMe: f.ReplyAuthorKind == string(dto.AuthorKindAgent) && f.ReplyAuthorID == agentID,
	})
}

// WantedByProject applies Wants for the active session (agentID, ownerID) of project
// projectID: a reply to any agent of the project counts as a reply to it.
func (f Facts) WantedByProject(agentID, projectID, ownerID string, prefs MemberPrefs) bool {
	replyToMe := f.ReplyAuthorKind == string(dto.AuthorKindAgent) &&
		(f.ReplyAuthorID == agentID || (projectID != "" && f.ReplyAuthorProjectID == projectID))
	return Wants(prefs, Delivered{
		Text: f.Text, Trusted: Trusted(f.AuthorUserID, ownerID), ChatKind: f.ChatKind, ReplyToAuthorIsMe: replyToMe,
	})
}
