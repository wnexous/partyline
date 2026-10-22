package conversations

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// ErrIdentityOnline is a merge of an AI that has a live session: it must be offline first (409
// identity_online, MRG-02).
var ErrIdentityOnline = errors.New("the AI is online: end its session before merging it")

// MergeNotice is the text of the system message that opens the history a merge appends to a DM:
// the handle names the AI the history came from.
func MergeNotice(handle string) string { return "History merged from @" + handle }

// Merge merges the AI fromID into the AI intoID, both owned by the caller: everything the first
// had becomes the second's and the first is deleted (MRG-01).
//
//   - Group memberships move; where both are members, the surviving AI's settings win.
//   - Delivery cursors in groups become the larger of the two.
//   - Who may use the first may use the second (the higher level where both grant).
//   - A DM of the first with someone the second has no DM with becomes the second's DM, kept as it
//     is. Where the second has a DM with that someone too, the first's messages (with their files
//     and deliveries) are appended to it after a system message naming the first, its chats join
//     it, the cursors of both sides move to the new numbers (the larger wins) when their holder
//     had received or read all of the surviving DM, else they stay, so nothing undelivered or
//     unread is skipped, and the old DM is deleted. A DM between the two goes away with its
//     history: they are one AI now.
//   - Their messages, and the connectors they operated, are the second's.
//
// It is one write transaction and is audited as agent.merge. The first AI must be offline
// (ErrIdentityOnline); another user's AI is not found (access.ErrNotFound) or, when the caller may
// only use it, forbidden; fromID and intoID must differ.
func (s *Service) Merge(ctx context.Context, p authn.Principal, fromID, intoID string) error {
	if p.Kind != authn.KindUser {
		return access.ErrForbidden
	}
	if err := s.owns(ctx, p.UserID, fromID); err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(intoID) == "":
		return &validation.Error{Fields: map[string]string{"into": "required"}}
	case intoID == fromID:
		return &validation.Error{Fields: map[string]string{"into": "must be a different AI"}}
	}
	if err := s.owns(ctx, p.UserID, intoID); err != nil {
		return err
	}
	if s.online != nil && s.online(fromID) {
		return ErrIdentityOnline
	}
	var out mergeOutcome
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		out = mergeOutcome{}
		return s.merge(ctx, q, p.UserID, fromID, intoID, &out)
	})
	if err != nil {
		return err
	}
	if s.kick != nil { // a session that attached after the check is not left holding a deleted AI
		s.kick(ctx, fromID)
	}
	s.announceMerge(fromID, intoID, out)
	return nil
}

// owns requires the user to own the AI: no access to it is access.ErrNotFound, use without
// ownership access.ErrForbidden.
func (s *Service) owns(ctx context.Context, userID, agentID string) error {
	l, err := s.az.AgentLevel(ctx, userID, agentID)
	if err != nil {
		return err
	}
	switch {
	case !access.AtLeast(l, dto.LevelUse):
		return access.ErrNotFound
	case !access.AtLeast(l, dto.LevelManage):
		return access.ErrForbidden
	}
	return nil
}

// mergeOutcome is what changed, for the events published once the merge committed.
type mergeOutcome struct {
	// groups are the groups whose members changed.
	groups []string
	// rekeyed are the DMs that became the surviving AI's, appended the DMs that received the
	// history of another, deleted the DMs that no longer exist.
	rekeyed, appended, deleted []string
}

func (s *Service) merge(ctx context.Context, q *db.Queries, ownerID, fromID, intoID string, out *mergeOutcome) error {
	from, err := q.GetAgent(ctx, fromID)
	if err != nil {
		return notFound(err)
	}
	into, err := q.GetAgent(ctx, intoID)
	if err != nil {
		return notFound(err)
	}
	if from.OwnerID != ownerID || into.OwnerID != ownerID {
		return access.ErrNotFound
	}
	fromHandle, err := handleOf(ctx, q, fromID)
	if err != nil {
		return err
	}
	intoHandle, err := handleOf(ctx, q, intoID)
	if err != nil {
		return err
	}
	now := s.now()

	dup, err := q.DeleteDuplicateAgentGroupMemberships(ctx, db.DeleteDuplicateAgentGroupMembershipsParams{FromID: fromID, IntoID: intoID})
	if err != nil {
		return err
	}
	moved, err := q.MoveAgentGroupMemberships(ctx, db.MoveAgentGroupMembershipsParams{FromID: fromID, IntoID: intoID})
	if err != nil {
		return err
	}
	out.groups = append(dup, moved...)
	if err := q.MergeAgentGroupCursors(ctx, db.MergeAgentGroupCursorsParams{FromID: fromID, IntoID: intoID}); err != nil {
		return err
	}

	dms, err := q.ListAgentDMRooms(ctx, sql.NullString{String: fromID, Valid: true})
	if err != nil {
		return err
	}
	for _, dm := range dms {
		if err := s.mergeDM(ctx, q, dm, fromID, intoID, fromHandle, now, out); err != nil {
			return err
		}
	}

	if err := q.MergeAgentGrants(ctx, db.MergeAgentGrantsParams{FromID: fromID, IntoID: intoID}); err != nil {
		return err
	}
	if err := q.ReassignAgentMessages(ctx, db.ReassignAgentMessagesParams{FromID: fromID, IntoID: intoID}); err != nil {
		return err
	}
	if err := q.ReassignAuthorIntelligence(ctx, db.ReassignAuthorIntelligenceParams{FromID: fromID, IntoID: intoID}); err != nil {
		return err
	}
	connectors, err := q.ReassignConnectorOperator(ctx, db.ReassignConnectorOperatorParams{
		FromID: sql.NullString{String: fromID, Valid: true}, IntoID: sql.NullString{String: intoID, Valid: true},
	})
	if err != nil {
		return err
	}

	left, err := q.DeleteAgentMemberships(ctx, fromID) // nothing should be left; be sure
	if err != nil {
		return err
	}
	out.groups = append(out.groups, left...)
	if err := q.DeleteGrants(ctx, db.DeleteGrantsParams{ResourceKind: string(access.KindAgent), ResourceID: fromID}); err != nil {
		return err
	}
	if err := q.DeleteAgent(ctx, fromID); err != nil { // its intelligence and cursors go with it
		return err
	}
	return s.audit.Record(ctx, q, audit.Entry{
		ActorID: ownerID, Action: audit.ActionAgentMerge, TargetKind: audit.TargetAgent, TargetID: intoID,
		Detail: map[string]any{"from": fromID, "into": intoID, "from_handle": fromHandle, "into_handle": intoHandle, "connectors": connectors},
	})
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return access.ErrNotFound
	}
	return err
}

// handleOf returns the @handle of an intelligence, "" when it has none.
func handleOf(ctx context.Context, q *db.Queries, id string) (string, error) {
	i, err := q.GetIntelligence(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return i.Handle, err
}

// mergeDM carries one DM of the merged AI over to the surviving AI.
func (s *Service) mergeDM(ctx context.Context, q *db.Queries, dm db.Room, fromID, intoID, fromHandle string, now time.Time, out *mergeOutcome) error {
	other := dm.DmA.String
	if other == fromID {
		other = dm.DmB.String
	}
	if other == intoID { // a DM with oneself is none
		out.deleted = append(out.deleted, dm.ID)
		return q.DeleteRoom(ctx, dm.ID)
	}
	pair := sortedPair(intoID, other)
	target, err := q.GetDMRoom(ctx, pair)
	if errors.Is(err, sql.ErrNoRows) { // the surviving AI has no DM with them: this one becomes its DM
		if err := q.SetDMRoomPair(ctx, db.SetDMRoomPairParams{DmA: pair.DmA, DmB: pair.DmB, UpdatedAt: now.UnixMilli(), ID: dm.ID}); err != nil {
			return err
		}
		if err := q.ReplaceAgentMember(ctx, db.ReplaceAgentMemberParams{FromID: fromID, IntoID: intoID, RoomID: dm.ID}); err != nil {
			return err
		}
		if err := q.ReplaceAgentCursor(ctx, db.ReplaceAgentCursorParams{FromID: fromID, IntoID: intoID, RoomID: dm.ID}); err != nil {
			return err
		}
		out.rekeyed = append(out.rekeyed, dm.ID)
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.appendDM(ctx, q, dm, target, fromID, intoID, fromHandle, now); err != nil {
		return err
	}
	out.appended = append(out.appended, target.ID)
	out.deleted = append(out.deleted, dm.ID)
	return nil
}

// movedMessage is a message of the merged DM and the number it has in the surviving DM.
type movedMessage struct{ oldSeq, newSeq int64 }

// appendDM moves the history of dm to the end of target, after a system message that names the AI
// it came from, and then deletes dm. Chats that were members of dm join target; the cursors of dm
// move to the numbers the messages now have, the larger of the two cursors holding, but only for a
// party that had received (an AI) or read (a user) everything target held before the merge: a
// cursor that jumped over the appended history would skip what its holder has not seen.
func (s *Service) appendDM(ctx context.Context, q *db.Queries, dm, target db.Room, fromID, intoID, fromHandle string, now time.Time) error {
	agentCursors, err := q.ListRoomAgentCursors(ctx, dm.ID)
	if err != nil {
		return err
	}
	readCursors, err := q.ListRoomReadCursors(ctx, dm.ID)
	if err != nil {
		return err
	}
	// Asked before the history is appended, while target holds nothing but its own messages.
	agentsUpToDate := make(map[string]bool, len(agentCursors))
	for _, c := range agentCursors {
		who := cursorHolder(c.AgentID, fromID, intoID)
		if agentsUpToDate[who], err = agentUpToDate(ctx, q, target.ID, who); err != nil {
			return err
		}
	}
	usersUpToDate := make(map[string]bool, len(readCursors))
	for _, c := range readCursors {
		if usersUpToDate[c.UserID], err = userUpToDate(ctx, q, target.ID, c.UserID); err != nil {
			return err
		}
	}

	ms := now.UnixMilli()
	next := func() (int64, error) {
		r, err := q.NextRoomSeq(ctx, db.NextRoomSeqParams{UpdatedAt: ms, ID: target.ID})
		return r.NextSeq - 1, err
	}
	seq, err := next()
	if err != nil {
		return err
	}
	if err := q.CreateMessage(ctx, db.CreateMessageParams{
		ID: store.NewID(), RoomID: target.ID, Seq: seq, AuthorKind: string(dto.AuthorKindSystem), Kind: string(dto.MessageKindSystem),
		Text: MergeNotice(fromHandle), RecipientsJson: "[]", CreatedAt: ms,
	}); err != nil {
		return err
	}

	history, err := q.ListRoomMessageOrder(ctx, dm.ID)
	if err != nil {
		return err
	}
	moved := make([]movedMessage, 0, len(history))
	for _, m := range history {
		if seq, err = next(); err != nil {
			return err
		}
		if err := q.MoveMessage(ctx, db.MoveMessageParams{RoomID: target.ID, Seq: seq, ID: m.ID}); err != nil {
			return err
		}
		moved = append(moved, movedMessage{oldSeq: m.Seq, newSeq: seq})
	}

	chats, err := q.ListRoomChatMemberRows(ctx, dm.ID)
	if err != nil {
		return err
	}
	for _, c := range chats { // the chat keeps its deliveries; it was already in target: it is deleted with dm
		n, err := q.CountRoomMember(ctx, db.CountRoomMemberParams{RoomID: target.ID, Kind: c.Kind, MemberID: c.MemberID})
		if err != nil {
			return err
		}
		if n == 0 {
			if err := q.MoveRoomMember(ctx, db.MoveRoomMemberParams{RoomID: target.ID, ID: c.ID}); err != nil {
				return err
			}
		}
	}

	for _, c := range agentCursors {
		who := cursorHolder(c.AgentID, fromID, intoID)
		if !agentsUpToDate[who] {
			continue
		}
		if at := renumber(moved, c.Seq); at > 0 {
			if err := q.AdvanceAgentCursor(ctx, db.AdvanceAgentCursorParams{AgentID: who, RoomID: target.ID, Seq: at}); err != nil {
				return err
			}
		}
	}
	for _, c := range readCursors {
		if !usersUpToDate[c.UserID] {
			continue
		}
		if at := renumber(moved, c.Seq); at > 0 {
			if err := q.UpsertReadCursor(ctx, db.UpsertReadCursorParams{UserID: c.UserID, RoomID: target.ID, Seq: at}); err != nil {
				return err
			}
		}
	}
	return q.DeleteRoom(ctx, dm.ID)
}

// cursorHolder is who holds in the surviving DM the cursor an AI held in the merged one: the
// merged AI's is the surviving AI's, the counterpart's stays its own.
func cursorHolder(agentID, fromID, intoID string) string {
	if agentID == fromID {
		return intoID
	}
	return agentID
}

// agentUpToDate reports whether the AI was delivered every message of the room that it did not
// write itself: none lies after its cursor (an AI that never received anything is at 0).
func agentUpToDate(ctx context.Context, q *db.Queries, roomID, agentID string) (bool, error) {
	cursor, err := q.GetAgentCursor(ctx, db.GetAgentCursorParams{AgentID: agentID, RoomID: roomID})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	pending, err := q.CountAgentBacklog(ctx, db.CountAgentBacklogParams{RoomID: roomID, AfterSeq: cursor, AgentID: agentID})
	return pending == 0, err
}

// userUpToDate reports whether the user has no unread message in the room (DM-06): everything
// after their read cursor, if any, they wrote themselves.
func userUpToDate(ctx context.Context, q *db.Queries, roomID, userID string) (bool, error) {
	unread, err := q.CountUnreadByRoom(ctx, db.CountUnreadByRoomParams{UserID: userID, RoomIds: []string{roomID}})
	return len(unread) == 0, err
}

// renumber returns the number, in the surviving DM, of the last moved message that was at or
// before the old position, 0 when none was: a cursor that had read up to that old position has
// read up to there.
func renumber(moved []movedMessage, oldSeq int64) int64 {
	var at int64
	for _, m := range moved {
		if m.oldSeq > oldSeq {
			break
		}
		at = m.newSeq
	}
	return at
}

// announceMerge tells the sockets what changed.
func (s *Service) announceMerge(fromID, intoID string, out mergeOutcome) {
	for _, id := range slices.Compact(slices.Sorted(slices.Values(out.groups))) {
		s.bus.Publish(events.MembersChanged{RoomID: id})
	}
	for _, id := range slices.Concat(out.rekeyed, out.appended) {
		s.bus.Publish(events.RoomChanged{RoomID: id})
		s.bus.Publish(events.MembersChanged{RoomID: id})
	}
	for _, id := range out.deleted {
		s.bus.Publish(events.RoomChanged{RoomID: id, Deleted: true})
	}
	s.bus.Publish(events.GrantsChanged{ResourceKind: string(access.KindAgent), ResourceID: intoID})
	s.bus.Publish(events.IntelligenceChanged{ID: fromID})
	s.bus.Publish(events.IntelligenceChanged{ID: intoID})
}
