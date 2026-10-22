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
	// Names are mentioned as "@<name>" (case-insensitive, on word boundaries): the name set
	// for the member in the room and the AI's profile display name.
	Names []string
	// Handle is the AI's @handle. "@<handle>" mentions it unless more handle characters
	// (a-z, 0-9 or "-") follow, so "@atlas" does not reach "@atlas-2" (AID-06).
	Handle string
	// Keywords count as mentions on word boundaries.
	Keywords []string
}

// Prefs builds the prefs of an AI's membership from its profile (display name and handle,
// which follow renames) and the member's stored settings: the name set in the room, the
// delivery mode and the keywords. The internal agent name is no mention (AID-06).
func Prefs(profile db.Intelligence, memberDisplayName, delivery, keywordsJSON string) MemberPrefs {
	var keywords []string
	_ = json.Unmarshal([]byte(keywordsJSON), &keywords)
	return MemberPrefs{
		Delivery: dto.DeliveryMode(delivery), Names: []string{memberDisplayName, profile.DisplayName},
		Handle: profile.Handle, Keywords: keywords,
	}
}

// IntelligenceOf loads the profile of an agent; the zero value when it has none (an agent
// created before intelligences existed), whose mentions are then the room name only.
func IntelligenceOf(ctx context.Context, q *db.Queries, agentID string) (db.Intelligence, error) {
	i, err := q.GetIntelligence(ctx, agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Intelligence{}, nil
	}
	return i, err
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

// ProjectPrefs builds the prefs of a project membership as its active session receives
// them: "@<name>" also matches the project name.
func ProjectPrefs(profile db.Intelligence, projectName, memberDisplayName, delivery, keywordsJSON string) MemberPrefs {
	p := Prefs(profile, memberDisplayName, delivery, keywordsJSON)
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
			if name != "" && containsBounded(d.Text, "@"+name, continuesName) {
				return true
			}
		}
		if m.Handle != "" && containsBounded(d.Text, "@"+m.Handle, continuesHandle) {
			return true
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
func containsWord(text, word string) bool { return containsBounded(text, word, isWordRune) }

// containsBounded reports whether text contains word, ignoring case, with no letter, digit or
// underscore right before it and no rune for which continues is true right after it.
func containsBounded(text, word string, continues func(rune) bool) bool {
	lt, lw := strings.ToLower(text), strings.ToLower(word)
	for i := 0; i <= len(lt); {
		j := strings.Index(lt[i:], lw)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(lw)
		before, _ := utf8.DecodeLastRuneInString(lt[:start])
		after, _ := utf8.DecodeRuneInString(lt[end:])
		if (start == 0 || !isWordRune(before)) && (end == len(lt) || !continues(after)) {
			return true
		}
		_, size := utf8.DecodeRuneInString(lt[start:])
		i = start + size
	}
	return false
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// continuesName reports whether r continues a name that precedes it: "@Atlas-2" and "@Atlasx"
// do not mention "Atlas".
func continuesName(r rune) bool { return isWordRune(r) || r == '-' }

// continuesHandle reports whether r continues a handle that precedes it: a-z, 0-9 and "-", so
// "@atlas" is not a mention of "@atlas-2" (AID-06).
func continuesHandle(r rune) bool {
	return r == '-' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

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
