package agents

import (
	"context"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store/db"
)

// UnreadLimit is the most messages read_unread returns at once (SN-05): the newest ones, oldest first,
// and the answer says how many earlier ones were left out. The catch-up batch that goes with a pushed
// message is bounded by BacklogLimit; a session that asks for what it missed may read a little more.
const UnreadLimit = 100

// CallingSession is the session an agent API call acts as (SN-01, SN-02, SN-05): its key (the harness
// session id it sent, else its label, else the principal role), the label it is addressed by ("" for the
// principal) and whether it is the principal. Two calls of the same process act as one session; a
// tools-only process that sends its harness session id keeps its state too, even without a socket.
type CallingSession struct {
	Key         string
	Label       string
	IsPrincipal bool
}

// CallingSessionOf resolves the session an agent API request acts as from what the channel sends with
// every call: the live id (X-Partyline-Live, and the folder for a process without a socket, SESS-29) gives
// the label, and the harness session id (X-Partyline-Harness-Session) is the key that outlives the label
// (SN-01). The principal is the session whose key is the AI's principal key, so a process that sends no
// live id and no id still acts as the principal.
func (r *Registry) CallingSessionOf(ctx context.Context, agentID, liveID, folder, harnessSession string) (CallingSession, error) {
	a, err := r.store.R().GetAgent(ctx, agentID)
	if err != nil {
		return CallingSession{}, err
	}
	label := r.SessionLabelOf(ctx, agentID, liveID, folder)
	key := SessionKey(harnessSession, label, a.PrincipalSession)
	return CallingSession{Key: key, Label: label, IsPrincipal: key == SessionKey("", "", a.PrincipalSession)}, nil
}

// SessionRoomNotification is one conversation's state for one session (SN-05, SN-08): the setting, the
// room it belongs to, and how many messages the session has not read there.
type SessionRoomNotification struct {
	RoomID string
	Name   string
	Kind   dto.ConversationKind
	Notify string
	Unread int64
}

// SessionNotifications returns the notification state of one session in every room of the AI, creating
// the state row of a room the session has not seen yet at its join point with the default of its role
// (SN-01, SN-02). The unread count is the session's: a sibling's message counts, its own does not (SN-05,
// SN-06).
func (r *Registry) SessionNotifications(ctx context.Context, agentID string, s CallingSession) ([]SessionRoomNotification, error) {
	rooms, err := r.SessionRooms(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]SessionRoomNotification, 0, len(rooms))
	for _, room := range rooms {
		if err := r.EnsureRoomState(ctx, agentID, s.Key, room.ID, s.IsPrincipal); err != nil {
			return nil, err
		}
		out = append(out, SessionRoomNotification{
			RoomID: room.ID, Name: room.Name, Kind: dto.ConversationKind(room.Kind), Notify: DefaultNotify(s.IsPrincipal),
		})
	}
	return r.fillNotifications(ctx, agentID, s, out)
}

// SessionNotification returns the notification state of one session in one room, creating its state row
// when it has none (SN-01). A room the AI is not a member of is not found.
func (r *Registry) SessionNotification(ctx context.Context, agentID, roomID string, s CallingSession) (SessionRoomNotification, bool, error) {
	rooms, err := r.SessionRooms(ctx, agentID)
	if err != nil {
		return SessionRoomNotification{}, false, err
	}
	for _, room := range rooms {
		if room.ID != roomID {
			continue
		}
		if err := r.EnsureRoomState(ctx, agentID, s.Key, roomID, s.IsPrincipal); err != nil {
			return SessionRoomNotification{}, false, err
		}
		out, err := r.fillNotifications(ctx, agentID, s, []SessionRoomNotification{
			{RoomID: room.ID, Name: room.Name, Kind: dto.ConversationKind(room.Kind), Notify: DefaultNotify(s.IsPrincipal)},
		})
		if err != nil {
			return SessionRoomNotification{}, false, err
		}
		return out[0], true, nil
	}
	return SessionRoomNotification{}, false, nil
}

// fillNotifications adds the stored setting and the unread count of each room to out.
func (r *Registry) fillNotifications(ctx context.Context, agentID string, s CallingSession, out []SessionRoomNotification) ([]SessionRoomNotification, error) {
	if len(out) == 0 {
		return out, nil
	}
	states, err := r.store.R().ListSessionRoomStates(ctx, db.ListSessionRoomStatesParams{AgentID: agentID, SessionKey: s.Key})
	if err != nil {
		return nil, err
	}
	byRoom := make(map[string]db.SessionRoomState, len(states))
	for _, st := range states {
		byRoom[st.RoomID] = st
	}
	counts, err := r.store.R().CountSessionUnreadByRoom(ctx, db.CountSessionUnreadByRoomParams{
		AgentID: agentID, SessionKey: s.Key, AuthorLabel: s.Label,
	})
	if err != nil {
		return nil, err
	}
	unread := make(map[string]int64, len(counts))
	for _, row := range counts {
		unread[row.RoomID] = row.Unread
	}
	for i := range out {
		if st, ok := byRoom[out[i].RoomID]; ok {
			out[i].Notify = st.Notify
		}
		out[i].Unread = unread[out[i].RoomID]
	}
	return out, nil
}

// ReadSessionUnread returns the messages of one room the session has not read yet, oldest first, at most
// limit of them, and moves its read position to the newest one returned (SN-05). The filters of the pushed
// catch-up apply (SN-03): its own messages are not part of it (SN-06), nothing from before the AI's
// current membership (ROOM-01), no agent message written while the loop guard had paused the room (DLV-08)
// and no message it acknowledged. The count is what the caller announces as skipped when the room had
// more unread messages than the limit.
func (r *Registry) ReadSessionUnread(ctx context.Context, agentID, roomID string, s CallingSession, limit int) ([]db.MessageView, int64, error) {
	if s.Key == "" {
		return nil, 0, nil
	}
	if limit <= 0 || limit > UnreadLimit {
		limit = UnreadLimit
	}
	if err := r.EnsureRoomState(ctx, agentID, s.Key, roomID, s.IsPrincipal); err != nil {
		return nil, 0, err
	}
	state, err := r.store.R().GetSessionRoomState(ctx, db.GetSessionRoomStateParams{
		AgentID: agentID, SessionKey: s.Key, RoomID: roomID,
	})
	if err != nil {
		return nil, 0, err
	}
	high, err := r.store.R().GetRoomHighSeq(ctx, roomID)
	if err != nil {
		return nil, 0, err
	}
	before := high + 1
	params := db.CountSessionUnreadInRoomParams{
		RoomID: roomID, AfterSeq: state.ReadSeq, BeforeSeq: before,
		AgentID: agentID, AuthorLabel: s.Label, Label: s.Label,
	}
	count, err := r.store.R().CountSessionUnreadInRoom(ctx, params)
	if err != nil || count == 0 {
		return nil, 0, err
	}
	views, err := r.store.R().ListSessionUnread(ctx, db.ListSessionUnreadParams{
		RoomID: params.RoomID, AfterSeq: params.AfterSeq, BeforeSeq: params.BeforeSeq,
		AgentID: agentID, AuthorLabel: s.Label, Label: s.Label, Limit: int64(limit),
	})
	if err != nil {
		return nil, 0, err
	}
	skipped := count
	if len(views) > 0 {
		skipped = count - int64(len(views))
		if err := r.AdvanceSessionReadSeq(ctx, agentID, s.Key, roomID, views[len(views)-1].Seq, s.IsPrincipal); err != nil {
			return nil, 0, err
		}
	}
	return views, skipped, nil
}

// TrustedOnly reports whether the AI's delivery mode in the room is trusted (MEM-04): an untrusted message
// is then no context and no unread message it may read either (SN-03, POS-10).
func (r *Registry) TrustedOnly(ctx context.Context, agentID, roomID string) (bool, error) {
	member, err := r.store.R().GetRoomMemberByMember(ctx, db.GetRoomMemberByMemberParams{
		RoomID: roomID, Kind: string(dto.MemberKindAgent), MemberID: agentID,
	})
	if err != nil {
		return false, err
	}
	return dto.DeliveryMode(member.Delivery) == dto.DeliveryModeTrusted, nil
}

// SessionNotifyOf returns the notification setting of a live session of the AI in one room (SN-02,
// SN-08): the member card marks the sessions that are muted there. A session that is not live, and one
// whose state row is missing, answers with the default of its role; a read failure is not worth failing a
// member list for, so it answers the same.
func (r *Registry) SessionNotifyOf(ctx context.Context, agentID, label, roomID string) string {
	isPrincipal := label == ""
	a, err := r.store.R().GetAgent(ctx, agentID)
	if err != nil {
		return DefaultNotify(isPrincipal)
	}
	key, ok := r.presence.SessionKeyOf(agentID, label, a.PrincipalSession)
	if !ok {
		return DefaultNotify(isPrincipal)
	}
	state, err := r.store.R().GetSessionRoomState(ctx, db.GetSessionRoomStateParams{
		AgentID: agentID, SessionKey: key, RoomID: roomID,
	})
	if err != nil {
		return DefaultNotify(isPrincipal)
	}
	return state.Notify
}

// PrincipalSessionPath is the label a route uses for the principal session (SN-08): the principal's
// label is the empty string, which a path cannot carry, and "principal" is a label a session could
// have, while an underscore never is one (dto.SessionLabelPattern).
const PrincipalSessionPath = "_principal"

// LiveCallingSession resolves the session the owner's notification routes address by label (SN-08). The
// principal ("", or PrincipalSessionPath in a path) is always addressable, live or not; a labelled
// session only while it is live, because that is the only session an owner sees.
func (r *Registry) LiveCallingSession(ctx context.Context, agentID, label string) (CallingSession, bool, error) {
	if label == PrincipalSessionPath {
		label = ""
	}
	a, err := r.store.R().GetAgent(ctx, agentID)
	if err != nil {
		return CallingSession{}, false, err
	}
	principalKey := SessionKey("", "", a.PrincipalSession)
	if label == "" {
		key, ok := r.presence.SessionKeyOf(agentID, "", a.PrincipalSession)
		if !ok {
			key = principalKey
		}
		return CallingSession{Key: key, IsPrincipal: true}, true, nil
	}
	key, ok := r.presence.SessionKeyOf(agentID, label, a.PrincipalSession)
	if !ok {
		return CallingSession{}, false, nil
	}
	return CallingSession{Key: key, Label: label}, true, nil
}
