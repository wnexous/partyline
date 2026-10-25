package conversations

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// List returns the caller's conversations, newest activity first: the groups they created, administer
// or are a member of and the DMs they take part in (DM-06, LAY-01), each filed under its
// organizations (ORG-02). A DM the caller only views through an AI is reachable by id and not
// listed. Unread and read positions are per user, so only users have a list.
func (s *Service) List(ctx context.Context, p authn.Principal) ([]dto.Conversation, error) {
	if p.Kind != authn.KindUser {
		return nil, access.ErrForbidden
	}
	listed, err := s.rooms.List(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]dto.Conversation, 0, len(listed))
	if len(listed) == 0 {
		return out, nil
	}
	ids := make([]string, len(listed))
	for i, r := range listed {
		ids[i] = r.ID
	}
	q := s.store.R()
	last, err := s.lastMessages(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	unread, err := q.CountUnreadByRoom(ctx, db.CountUnreadByRoomParams{UserID: p.UserID, RoomIds: ids})
	if err != nil {
		return nil, err
	}
	unreadBy := make(map[string]int, len(unread))
	for _, u := range unread {
		unreadBy[u.RoomID] = int(u.Unread)
	}
	names, err := q.ListMemberNames(ctx, ids)
	if err != nil {
		return nil, err
	}
	namesBy := make(map[string][]string, len(listed))
	for _, n := range names {
		if len(namesBy[n.RoomID]) < dto.MaxConversationMemberNames {
			namesBy[n.RoomID] = append(namesBy[n.RoomID], memberName(n.DisplayName, n.Handle))
		}
	}
	for _, r := range listed {
		c := dto.Conversation{
			ID: r.ID, Kind: dto.ConversationKind(r.Kind), Title: r.Name, MyLevel: r.Level, Unread: unreadBy[r.ID],
			ActivityAt: time.UnixMilli(r.CreatedAt).UTC(), MemberNames: []string{}, OrgIDs: r.OrgIDs,
		}
		if c.Kind == dto.ConversationKindDM {
			for _, part := range r.Participants {
				if part.ID != p.UserID {
					ref := convert.IntelligenceRef(part.Intelligence, dto.IntelligenceRole(part.Role), part.Platform)
					c.Counterpart, c.Title = &ref, part.DisplayName
					c.MemberNames = []string{memberName(part.DisplayName, part.Handle)}
				}
			}
		} else {
			c.AvatarURL = convert.RoomAvatarURL(r.ID, r.AvatarSha256)
			if n := namesBy[r.ID]; n != nil {
				c.MemberNames = n
			}
		}
		if m, ok := last[r.ID]; ok {
			if c.Kind == dto.ConversationKindDM {
				m.AuthorName = "" // the name prefix is a group matter (LAY-01)
			}
			c.LastMessage, c.ActivityAt = &m, m.CreatedAt
		}
		out = append(out, c)
	}
	slices.SortStableFunc(out, func(a, b dto.Conversation) int {
		if c := b.ActivityAt.Compare(a.ActivityAt); c != 0 {
			return c
		}
		if c := strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func memberName(displayName, handle string) string { return displayName + " @" + handle }

// lastMessages previews the newest message of each room that has one.
func (s *Service) lastMessages(ctx context.Context, q *db.Queries, roomIDs []string) (map[string]dto.MessagePreview, error) {
	views, err := q.ListLastMessages(ctx, roomIDs)
	if err != nil {
		return nil, err
	}
	var blank []string // messages without text, previewed by their first file
	for _, v := range views {
		if strings.TrimSpace(v.Text) == "" {
			blank = append(blank, v.ID)
		}
	}
	files := map[string]string{}
	if len(blank) > 0 {
		atts, err := q.ListAttachmentsByMessages(ctx, blank)
		if err != nil {
			return nil, err
		}
		for _, a := range atts {
			if _, ok := files[a.MessageID]; !ok {
				files[a.MessageID] = a.Name
			}
		}
	}
	out := make(map[string]dto.MessagePreview, len(views))
	for _, v := range views {
		out[v.RoomID] = dto.MessagePreview{
			Seq: v.Seq, AuthorID: authorOf(v), AuthorName: v.AuthorName, Text: previewText(v.Text, files[v.ID]),
			CreatedAt: time.UnixMilli(v.CreatedAt).UTC(), Deleted: v.DeletedAt.Valid,
		}
	}
	return out, nil
}

// authorOf is the intelligence that wrote the message: the recorded author, else the user or the
// AI of a message written before authors were recorded; empty for system messages and for chat
// senders without an intelligence.
func authorOf(v db.MessageView) string {
	if v.AuthorIid != "" {
		return v.AuthorIid
	}
	if v.AuthorKind == string(dto.AuthorKindUser) || v.AuthorKind == string(dto.AuthorKindAgent) {
		return v.AuthorID
	}
	return ""
}

// previewText is the first dto.MaxPreviewLength code points of the text, or "[file name]" for a
// message without text that has a file (LAY-01).
func previewText(text, firstFile string) string {
	if strings.TrimSpace(text) == "" {
		if firstFile == "" {
			return ""
		}
		return "[" + firstFile + "]"
	}
	n := 0
	for i := range text {
		if n == dto.MaxPreviewLength {
			return text[:i]
		}
		n++
	}
	return text
}

// MarkRead moves the caller's read position in a room they can view to seq and returns the
// position that holds (DM-07). seq nil reads up to the newest message; a seq past it counts as the
// newest, so a later message is still unread; an earlier seq than the current position changes
// nothing; a negative one is a validation error. The caller's sockets (and only theirs) hear
// room.read with the position that holds, also when it did not move.
func (s *Service) MarkRead(ctx context.Context, p authn.Principal, roomID string, seq *int64) (dto.ReadState, error) {
	if p.Kind != authn.KindUser {
		return dto.ReadState{}, access.ErrForbidden
	}
	if seq != nil && *seq < 0 {
		return dto.ReadState{}, &validation.Error{Fields: map[string]string{"seq": "must be 0 or more"}}
	}
	if err := s.rooms.Require(ctx, p, roomID, dto.LevelView); err != nil {
		return dto.ReadState{}, err
	}
	q := s.store.R()
	room, err := q.GetRoom(ctx, roomID)
	if errors.Is(err, sql.ErrNoRows) {
		return dto.ReadState{}, access.ErrNotFound
	}
	if err != nil {
		return dto.ReadState{}, err
	}
	target := room.NextSeq - 1
	if seq != nil {
		target = min(*seq, target)
	}
	if target > 0 {
		err = s.store.WithTx(ctx, func(w *db.Queries) error {
			return w.UpsertReadCursor(ctx, db.UpsertReadCursorParams{UserID: p.UserID, RoomID: roomID, Seq: target})
		})
		if err != nil {
			return dto.ReadState{}, err
		}
	}
	held, err := q.GetReadCursor(ctx, db.GetReadCursorParams{UserID: p.UserID, RoomID: roomID})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return dto.ReadState{}, err
	}
	unread, err := q.CountUnreadByRoom(ctx, db.CountUnreadByRoomParams{UserID: p.UserID, RoomIds: []string{roomID}})
	if err != nil {
		return dto.ReadState{}, err
	}
	state := dto.ReadState{RoomID: roomID, Seq: held}
	if len(unread) > 0 {
		state.Unread = int(unread[0].Unread)
	}
	s.bus.Publish(events.ReadChanged{UserID: p.UserID, RoomID: roomID, Seq: held})
	return state, nil
}
