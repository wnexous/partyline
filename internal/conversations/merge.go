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

// Merge merges the AI fromID into the AI intoID, which the caller manages and which share their home
// organization: everything the first had becomes the second's and the first is deleted (MRG-01, POS-04,
// AD-016).
//
//   - Group memberships move; where both are members, the surviving AI's settings win.
//   - Delivery cursors in groups become the larger of the two.
//   - The organizations only the first belongs to become the second's, with the permission, position and
//     inviter it had there; where both belong, the surviving AI's membership wins. Whoever the first
//     created or invited is the second's.
//   - A DM of the first with someone the second has no DM with becomes the second's DM, kept as it
//     is. Where the second has a DM with that someone too, the first's messages (with their files
//     and deliveries) are appended to it after a system message naming the first, its chats join
//     it, the cursors of both sides move to the new numbers (the larger wins) when their holder
//     had received or read all of the surviving DM, else they stay, so nothing undelivered or
//     unread is skipped, and the old DM is deleted. A DM between the two goes away with its
//     history: they are one AI now.
//   - Their messages, and the connectors they operated, are the second's, with the platform queue
//     of those connectors (acknowledged items included) and the count of platform messages the
//     first lost to the bound or to expiry, which the second is told about on its next
//     connection (CID-05, CID-10).
//   - The organizations only the first belongs to become the second's, with the permission, position
//     and inviter it had there; where both belong, the second's membership stays. What the first
//     created - AIs, groups - and the guests it invited are the second's. If the first created the
//     second, the second's creator is the first's creator, so that it is not left as its own.
//
// It is one write transaction and is audited as agent.merge. The first AI must be offline
// (ErrIdentityOnline). An AI the caller cannot see is access.ErrNotFound; one they see and do not
// manage - in its home organization, where management is decided - or an AI of another home
// organization than the first's is access.ErrForbidden; fromID and intoID must differ.
func (s *Service) Merge(ctx context.Context, p authn.Principal, fromID, intoID string) error {
	if p.Kind != authn.KindUser {
		return access.ErrForbidden
	}
	fromHome, err := s.manages(ctx, p, fromID)
	if err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(intoID) == "":
		return &validation.Error{Fields: map[string]string{"into": "required"}}
	case intoID == fromID:
		return &validation.Error{Fields: map[string]string{"into": "must be a different AI"}}
	}
	intoHome, err := s.manages(ctx, p, intoID)
	if err != nil {
		return err
	}
	if fromHome != intoHome { // an AI belongs to one organization (ORG-02): merging across them would move it
		return access.ErrForbidden
	}
	if s.online != nil && s.online(fromID) {
		return ErrIdentityOnline
	}
	var out mergeOutcome
	err = s.store.WithTx(ctx, func(q *db.Queries) error {
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

// manages requires the user to manage the AI in its home organization (POS-03) and returns that
// organization: an AI the user cannot see is access.ErrNotFound; one they see and do not manage, or one
// that has no home organization and so no manager, access.ErrForbidden.
func (s *Service) manages(ctx context.Context, p authn.Principal, agentID string) (string, error) {
	see, err := s.az.CanSeeAI(ctx, p, agentID)
	if err != nil {
		return "", err
	}
	if !see {
		return "", access.ErrNotFound
	}
	home, err := s.store.R().GetAgentOrg(ctx, agentID)
	if err != nil {
		return "", err
	}
	if home == "" {
		return "", access.ErrForbidden
	}
	ok, err := s.az.Authz().Manages(ctx, p.IID(), agentID, home)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", access.ErrForbidden
	}
	return home, nil
}

// mirrorPrincipalReadSeqs follows the AI's delivery cursors with the read position of its principal, in the
// merge transaction (SN-01). AdvancePrincipalReadSeq resolves the principal's key (its pinned harness session
// id, or "principal" when nothing is pinned) and creates its row when it has none.
func mirrorPrincipalReadSeqs(ctx context.Context, q *db.Queries, agentID string, now time.Time) error {
	states, err := q.ListAgentRoomStates(ctx, agentID)
	if err != nil {
		return err
	}
	for _, st := range states {
		if st.Cursor == 0 {
			continue
		}
		if err := q.AdvancePrincipalReadSeq(ctx, db.AdvancePrincipalReadSeqParams{
			AgentID: agentID, RoomID: st.ID, ReadSeq: st.Cursor, UpdatedAt: now.UnixMilli(),
		}); err != nil {
			return err
		}
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
	// queued is how many platform items changed hands.
	queued int64
	// orgs are the organizations whose members changed: the merged AI's.
	orgs []string
}

func (s *Service) merge(ctx context.Context, q *db.Queries, actorID, fromID, intoID string, out *mergeOutcome) error {
	from, err := q.GetAgent(ctx, fromID)
	if err != nil {
		return notFound(err)
	}
	into, err := q.GetAgent(ctx, intoID)
	if err != nil {
		return notFound(err)
	}
	if !from.OrgID.Valid || from.OrgID != into.OrgID { // checked again inside the transaction
		return access.ErrForbidden
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
	// The surviving AI's principal reads up to its delivery cursors, which the merge moved: a read position
	// follows its cursor everywhere a cursor is written (SN-01), so a merged room does not become all unread.
	if err := mirrorPrincipalReadSeqs(ctx, q, intoID, now); err != nil {
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
	// CID-05: the queue goes with each connection, acknowledged items included, and keeps the
	// survivor within the bound.
	for _, id := range connectors {
		n, err := s.queue.MoveConnector(ctx, q, id, intoID)
		if err != nil {
			return err
		}
		out.queued += n
	}
	// CID-10: what the merged AI lost is the survivor's to be told, as the connections are its own now.
	dropped, err := q.GetPlatformDropped(ctx, fromID)
	if err != nil {
		return err
	}
	if dropped > 0 {
		if err := q.AddPlatformDropped(ctx, db.AddPlatformDroppedParams{N: dropped, AgentID: intoID}); err != nil {
			return err
		}
	}

	left, err := q.DeleteAgentMemberships(ctx, fromID) // nothing should be left; be sure
	if err != nil {
		return err
	}
	out.groups = append(out.groups, left...)
	if err := s.mergeOrganizations(ctx, q, from, into, out); err != nil {
		return err
	}
	if err := q.DeleteAgent(ctx, fromID); err != nil { // its intelligence and cursors go with it
		return err
	}
	return s.audit.Record(ctx, q, audit.Entry{
		ActorID: actorID, Action: audit.ActionAgentMerge, TargetKind: audit.TargetAgent, TargetID: intoID,
		Detail: map[string]any{
			"from": fromID, "into": intoID, "from_handle": fromHandle, "into_handle": intoHandle,
			"connectors": connectors, "platform_items": out.queued,
		},
	})
}

// mergeOrganizations moves what the merged AI is in the organizations to the surviving one: its
// memberships (the survivor's win), and who it created or invited.
func (s *Service) mergeOrganizations(ctx context.Context, q *db.Queries, from, into db.Agent, out *mergeOutcome) error {
	// What the merged AI created and invited is the survivor's. This also makes the survivor its own
	// creator or inviter when the merged AI had made it; those two are repaired below.
	if err := q.ReassignAgentCreator(ctx, db.ReassignAgentCreatorParams{FromID: sql.NullString{String: from.ID, Valid: true}, IntoID: sql.NullString{String: into.ID, Valid: true}}); err != nil {
		return err
	}
	if into.CreatorIid.Valid && into.CreatorIid.String == from.ID {
		if err := q.SetAgentCreator(ctx, db.SetAgentCreatorParams{CreatorIid: from.CreatorIid, ID: into.ID}); err != nil {
			return err
		}
	}
	if err := q.ReassignRoomCreator(ctx, db.ReassignRoomCreatorParams{FromID: sql.NullString{String: from.ID, Valid: true}, IntoID: sql.NullString{String: into.ID, Valid: true}}); err != nil {
		return err
	}
	if err := q.ReassignInviter(ctx, db.ReassignInviterParams{FromID: sql.NullString{String: from.ID, Valid: true}, IntoID: sql.NullString{String: into.ID, Valid: true}}); err != nil {
		return err
	}
	if err := q.ClearSelfInviter(ctx, db.ClearSelfInviterParams{FromID: from.ID, IntoID: into.ID}); err != nil {
		return err
	}
	if err := q.ReassignInvitationInviter(ctx, db.ReassignInvitationInviterParams{FromID: from.ID, IntoID: into.ID}); err != nil {
		return err
	}
	moved, err := q.MoveAgentOrgMemberships(ctx, db.MoveAgentOrgMembershipsParams{FromID: from.ID, IntoID: into.ID})
	if err != nil {
		return err
	}
	left, err := q.DeleteAgentOrgMemberships(ctx, from.ID) // the organizations both belong to
	if err != nil {
		return err
	}
	out.orgs = append(out.orgs, slices.Concat(moved, left)...)
	return nil
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
	if out.queued > 0 { // the survivor's live session reads the items it was handed
		s.bus.Publish(events.PlatformQueued{AgentID: intoID})
	}
	for _, id := range slices.Compact(slices.Sorted(slices.Values(out.orgs))) {
		s.bus.Publish(events.OrgChanged{OrgID: id})
	}
	s.bus.Publish(events.AccessChanged{ResourceKind: string(access.KindAgent), ResourceID: intoID})
	s.bus.Publish(events.IntelligenceChanged{ID: fromID})
	s.bus.Publish(events.IntelligenceChanged{ID: intoID})
}
