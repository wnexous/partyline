package rooms

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// PauseNotice is the text of the system message posted when the loop guard pauses a room.
const PauseNotice = "Agent delivery is paused: agents posted too many messages in a row. A room manager can resume it."

// Message is a stored message with its author name, attachments and outbound deliveries.
type Message struct {
	db.MessageView
	Attachments []db.Attachment
	Outbox      []db.Outbox
	// Chat is the chat a chat-authored message came from (CTX-01): its title, kind and
	// connector kind (platform). Nil for other authors and for chats deleted since.
	Chat *db.ListChatRefsRow
}

// Recipients decodes the explicit chat ids of the message.
func (m Message) Recipients() []string {
	var ids []string
	_ = json.Unmarshal([]byte(m.RecipientsJson), &ids)
	return ids
}

// File is an attachment whose content is already in the blob store.
type File struct {
	SHA256 string
	Name   string
	Mime   string
	Size   int64
	// Status defaults to stored; connectors record too_large or unavailable media.
	Status dto.AttachmentStatus
}

// PostInput is a message posted by a user or an agent.
type PostInput struct {
	Text    string
	ReplyTo string // message id in the same room
	// Recipients are chat ids of chat members; empty reaches every chat with outbound on (DLV-03/04).
	Recipients []string
	Files      []File
	// Kind defaults to text; agent reactions are MessageKindReaction (REACT-01).
	Kind dto.MessageKind
}

// ChatMessage is a message received from an external chat (connectors' inbound pipeline).
type ChatMessage struct {
	ChatID      string
	SenderID    string
	SenderAltID string
	SenderName  string
	// SenderUserID is the user a verified identity resolves to; empty when unverified (DLV-06).
	SenderUserID string
	// ExternalID is the platform message id; a repeat returns the stored message (CON-10).
	ExternalID string
	Kind       dto.MessageKind
	Text       string
	ReplyTo    string
	Files      []File
}

// draft is a message about to be inserted.
type draft struct {
	authorKind   dto.AuthorKind
	authorID     string
	authorUserID string
	senderID     string
	senderAltID  string
	senderName   string
	kind         dto.MessageKind
	text         string
	replyTo      string
	recipients   []string
	externalID   string
	files        []File
}

// Post stores a message from the calling user (needs post, ACL-04) or agent (needs
// membership) and queues its deliveries.
func (s *Service) Post(ctx context.Context, p authn.Principal, roomID string, in PostInput) (Message, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelPost); err != nil {
		return Message{}, err
	}
	kind := in.Kind
	if kind == "" {
		kind = dto.MessageKindText
	}
	d := draft{
		authorKind: dto.AuthorKindUser, authorID: p.UserID, authorUserID: p.UserID,
		kind: kind, text: in.Text, replyTo: in.ReplyTo, recipients: in.Recipients, files: in.Files,
	}
	if p.Kind == authn.KindAgent {
		// An agent's messages resolve to its owner (DLV-06).
		d.authorKind, d.authorID = dto.AuthorKindAgent, p.AgentID
	}
	return s.post(ctx, roomID, d)
}

// PostFromChat stores a message received from a chat that is a member of the room. Callers
// are trusted server components; access.ErrNotFound means the chat is not in the room.
func (s *Service) PostFromChat(ctx context.Context, roomID string, in ChatMessage) (Message, error) {
	n, err := s.store.R().CountRoomMember(ctx, db.CountRoomMemberParams{RoomID: roomID, Kind: string(dto.MemberKindChat), MemberID: in.ChatID})
	if err != nil {
		return Message{}, err
	}
	if n == 0 {
		return Message{}, access.ErrNotFound
	}
	kind := in.Kind
	if kind == "" {
		kind = dto.MessageKindText
	}
	return s.post(ctx, roomID, draft{
		authorKind: dto.AuthorKindChat, authorID: in.ChatID, authorUserID: in.SenderUserID,
		senderID: in.SenderID, senderAltID: in.SenderAltID, senderName: in.SenderName,
		kind: kind, text: in.Text, replyTo: in.ReplyTo, externalID: in.ExternalID, files: in.Files,
	})
}

// validate applies MSG-02.
func (d draft) validate() error {
	check := validation.Check{}
	if utf8.RuneCountInString(d.text) > dto.MaxTextLength {
		check.Add("text", "must have at most 16000 characters")
	}
	if strings.TrimSpace(d.text) == "" && len(d.files) == 0 {
		check.Add("text", "must not be empty without attachments")
	}
	return check.Err()
}

// post inserts the message in one write transaction: dedupe, next sequence number,
// attachments, outbox entries from the delivery plan and the loop guard (with its pause
// notice). MessageCreated is published after commit, except for duplicates.
func (s *Service) post(ctx context.Context, roomID string, d draft) (Message, error) {
	if err := d.validate(); err != nil {
		return Message{}, err
	}
	var created []events.MessageCreated
	var id string
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		created = nil
		if d.externalID != "" {
			existing, err := q.GetMessageIDByExternalID(ctx, db.GetMessageIDByExternalIDParams{
				RoomID: roomID, AuthorKind: string(d.authorKind), AuthorID: d.authorID,
				ExternalID: sql.NullString{String: d.externalID, Valid: true},
			})
			if err == nil {
				id = existing
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		members, err := q.ListRoomMembers(ctx, roomID)
		if err != nil {
			return err
		}
		if err := s.checkRefs(ctx, q, roomID, members, d); err != nil {
			return err
		}
		room, msg, err := s.insert(ctx, q, roomID, members, d)
		if err != nil {
			return err
		}
		id = msg.MessageID
		created = append(created, msg)

		state, pausedNow := s.guard.Next(delivery.LoopState{Streak: int(room.AgentStreak), Paused: room.AgentsPaused}, d.authorKind)
		if err := q.SetRoomLoopState(ctx, db.SetRoomLoopStateParams{AgentStreak: int64(state.Streak), AgentsPaused: state.Paused, ID: roomID}); err != nil {
			return err
		}
		if pausedNow { // DLV-08
			_, notice, err := s.insert(ctx, q, roomID, members, draft{authorKind: dto.AuthorKindSystem, kind: dto.MessageKindSystem, text: PauseNotice})
			if err != nil {
				return err
			}
			created = append(created, notice)
		}
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	for _, e := range created {
		s.bus.Publish(e)
	}
	if len(created) > 1 {
		s.bus.Publish(events.RoomChanged{RoomID: roomID})
	}
	return s.message(ctx, id)
}

// checkRefs validates recipients (chat members of the room with outbound on: a listen-only
// chat receives nothing, DLV-04) and the replied message.
func (s *Service) checkRefs(ctx context.Context, q *db.Queries, roomID string, members []db.ListRoomMembersRow, d draft) error {
	check := validation.Check{}
	for _, id := range d.recipients {
		var member *db.RoomMember
		for i := range members {
			if m := &members[i].RoomMember; m.Kind == string(dto.MemberKindChat) && m.MemberID == id {
				member = m
			}
		}
		switch {
		case member == nil:
			check.Add("recipients", "must be chats of the room")
		case !member.Outbound:
			check.Add("recipients", "listen-only")
		}
	}
	if d.replyTo != "" {
		n, err := q.CountRoomMessage(ctx, db.CountRoomMessageParams{ID: d.replyTo, RoomID: roomID})
		if err != nil {
			return err
		}
		if n == 0 {
			check.Add("reply_to", "must be a message of the room")
		}
	}
	return check.Err()
}

// insert takes the next sequence number and stores the message, its attachments and its
// outbox entries. It returns the room as updated by the sequence bump.
func (s *Service) insert(ctx context.Context, q *db.Queries, roomID string, members []db.ListRoomMembersRow, d draft) (db.Room, events.MessageCreated, error) {
	now := s.now().UnixMilli()
	room, err := q.NextRoomSeq(ctx, db.NextRoomSeqParams{UpdatedAt: now, ID: roomID})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Room{}, events.MessageCreated{}, access.ErrNotFound
	}
	if err != nil {
		return db.Room{}, events.MessageCreated{}, err
	}
	seq := room.NextSeq - 1
	recipients := d.recipients
	if recipients == nil {
		recipients = []string{}
	}
	rcptJSON, err := json.Marshal(recipients)
	if err != nil {
		return db.Room{}, events.MessageCreated{}, err
	}
	id := store.NewID()
	err = q.CreateMessage(ctx, db.CreateMessageParams{
		ID: id, RoomID: roomID, Seq: seq, AuthorKind: string(d.authorKind), AuthorID: d.authorID,
		AuthorUserID: d.authorUserID, SenderID: d.senderID, SenderAltID: d.senderAltID, SenderName: d.senderName,
		Kind: string(d.kind), Text: d.text, ReplyTo: d.replyTo, RecipientsJson: string(rcptJSON),
		ExternalID: sql.NullString{String: d.externalID, Valid: d.externalID != ""}, CreatedAt: now,
	})
	if err != nil {
		return db.Room{}, events.MessageCreated{}, err
	}
	for _, f := range d.files {
		status := f.Status
		if status == "" {
			status = dto.AttachmentStatusStored
		}
		if err := q.CreateAttachment(ctx, db.CreateAttachmentParams{
			ID: store.NewID(), MessageID: id, Sha256: f.SHA256, Mime: f.Mime, Name: f.Name,
			Size: f.Size, Status: string(status), CreatedAt: now,
		}); err != nil {
			return db.Room{}, events.MessageCreated{}, err
		}
	}
	targets := delivery.Plan(delivery.RoomState{BridgeChats: room.BridgeChats}, planMembers(members),
		delivery.Draft{AuthorKind: d.authorKind, AuthorID: d.authorID, Recipients: d.recipients})
	for _, chatID := range targets.ChatIDs {
		if err := q.CreateOutbox(ctx, db.CreateOutboxParams{ID: store.NewID(), MessageID: id, ChatID: chatID, NextAttemptAt: now, UpdatedAt: now}); err != nil {
			return db.Room{}, events.MessageCreated{}, err
		}
	}
	return room, events.MessageCreated{RoomID: roomID, MessageID: id, Seq: seq}, nil
}

func planMembers(rows []db.ListRoomMembersRow) []delivery.Member {
	out := make([]delivery.Member, len(rows))
	for i, r := range rows {
		out[i] = delivery.Member{Kind: dto.MemberKind(r.RoomMember.Kind), ID: r.RoomMember.MemberID, Outbound: r.RoomMember.Outbound}
	}
	return out
}

// message loads one stored message with its attachments and outbox entries.
func (s *Service) message(ctx context.Context, id string) (Message, error) {
	v, err := s.store.R().GetMessageView(ctx, id)
	if err != nil {
		return Message{}, err
	}
	list, err := s.withDetails(ctx, []db.MessageView{v})
	if err != nil {
		return Message{}, err
	}
	return list[0], nil
}

// GetMessage returns one message of a room the caller can view.
func (s *Service) GetMessage(ctx context.Context, p authn.Principal, roomID, messageID string) (Message, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelView); err != nil {
		return Message{}, err
	}
	m, err := s.message(ctx, messageID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && m.RoomID != roomID) {
		return Message{}, access.ErrNotFound
	}
	return m, err
}

// MessageRef returns the room and sequence number of a message without authorizing;
// sql.ErrNoRows when it does not exist.
func (s *Service) MessageRef(ctx context.Context, messageID string) (db.GetMessageRoomSeqRow, error) {
	return s.store.R().GetMessageRoomSeq(ctx, messageID)
}

// Details loads the attachments and outbox entries of stored messages. It does not
// authorize: callers are trusted server components that selected the messages themselves.
func (s *Service) Details(ctx context.Context, views []db.MessageView) ([]Message, error) {
	return s.withDetails(ctx, views)
}

func (s *Service) withDetails(ctx context.Context, views []db.MessageView) ([]Message, error) {
	out := make([]Message, len(views))
	if len(views) == 0 {
		return out, nil
	}
	ids := make([]string, len(views))
	index := make(map[string]int, len(views))
	var chatIDs []string
	for i, v := range views {
		ids[i] = v.ID
		index[v.ID] = i
		out[i] = Message{MessageView: v, Attachments: []db.Attachment{}}
		if v.AuthorKind == string(dto.AuthorKindChat) && !slices.Contains(chatIDs, v.AuthorID) {
			chatIDs = append(chatIDs, v.AuthorID)
		}
	}
	if len(chatIDs) > 0 {
		refs, err := s.store.R().ListChatRefs(ctx, chatIDs)
		if err != nil {
			return nil, err
		}
		byID := make(map[string]*db.ListChatRefsRow, len(refs))
		for i := range refs {
			byID[refs[i].ID] = &refs[i]
		}
		for i := range out {
			if out[i].AuthorKind == string(dto.AuthorKindChat) {
				out[i].Chat = byID[out[i].AuthorID]
			}
		}
	}
	atts, err := s.store.R().ListAttachmentsByMessages(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, a := range atts {
		m := &out[index[a.MessageID]]
		m.Attachments = append(m.Attachments, a)
	}
	outbox, err := s.store.R().ListOutboxByMessages(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, o := range outbox {
		m := &out[index[o.MessageID]]
		m.Outbox = append(m.Outbox, o)
	}
	return out, nil
}

// Page selects messages: before BeforeSeq newest first (the default, from the latest), or
// after AfterSeq oldest first. Limit defaults to dto.DefaultPageSize, at most dto.MaxPageSize.
type Page struct {
	BeforeSeq int64
	AfterSeq  int64
	Limit     int
}

// Messages returns one page of a room's messages and whether more exist; requires view.
func (s *Service) Messages(ctx context.Context, p authn.Principal, roomID string, pg Page) ([]Message, bool, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelView); err != nil {
		return nil, false, err
	}
	limit := pg.Limit
	if limit <= 0 {
		limit = dto.DefaultPageSize
	}
	limit = min(limit, dto.MaxPageSize)
	before := pg.BeforeSeq
	if before <= 0 {
		before = math.MaxInt64
	}
	var views []db.MessageView
	var err error
	if pg.AfterSeq > 0 {
		views, err = s.store.R().ListMessagesAfter(ctx, db.ListMessagesAfterParams{RoomID: roomID, AfterSeq: pg.AfterSeq, BeforeSeq: before, Limit: int64(limit + 1)})
	} else {
		views, err = s.store.R().ListMessagesBefore(ctx, db.ListMessagesBeforeParams{RoomID: roomID, BeforeSeq: before, Limit: int64(limit + 1)})
	}
	if err != nil {
		return nil, false, err
	}
	more := len(views) > limit
	if more {
		views = views[:limit]
	}
	list, err := s.withDetails(ctx, views)
	return list, more, err
}

// Resume clears the loop guard: resets the agent streak and resumes agent delivery (DLV-09).
// Requires manage.
func (s *Service) Resume(ctx context.Context, p authn.Principal, roomID string) (Room, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelManage); err != nil {
		return Room{}, err
	}
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		return q.ResumeRoom(ctx, db.ResumeRoomParams{UpdatedAt: s.now().UnixMilli(), ID: roomID})
	})
	if err != nil {
		return Room{}, err
	}
	s.bus.Publish(events.RoomChanged{RoomID: roomID})
	return s.Get(ctx, p, roomID)
}

// Attachment returns an attachment of a message in a room the caller can view; otherwise
// access.ErrNotFound.
func (s *Service) Attachment(ctx context.Context, p authn.Principal, attachmentID string) (db.Attachment, error) {
	row, err := s.store.R().GetAttachmentRoom(ctx, attachmentID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Attachment{}, access.ErrNotFound
	}
	if err != nil {
		return db.Attachment{}, err
	}
	if _, err := s.authorize(ctx, p, row.RoomID, dto.LevelView); err != nil {
		return db.Attachment{}, access.ErrNotFound
	}
	return row.Attachment, nil
}

// ActivityItem is a message with the room it belongs to.
type ActivityItem struct {
	Room    Room
	Message Message
}

// Activity returns the latest messages, newest first, across every room the calling user
// can view (at most limit).
func (s *Service) Activity(ctx context.Context, p authn.Principal, limit int) ([]ActivityItem, error) {
	visible, err := s.List(ctx, p)
	if err != nil || len(visible) == 0 || limit <= 0 {
		return nil, err
	}
	byID := make(map[string]Room, len(visible))
	ids := make([]string, len(visible))
	for i, r := range visible {
		byID[r.ID], ids[i] = r, r.ID
	}
	views, err := s.store.R().ListLatestMessagesInRooms(ctx, db.ListLatestMessagesInRoomsParams{RoomIds: ids, Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	msgs, err := s.withDetails(ctx, views)
	if err != nil {
		return nil, err
	}
	out := make([]ActivityItem, len(msgs))
	for i, m := range msgs {
		out[i] = ActivityItem{Room: byID[m.RoomID], Message: m}
	}
	return out, nil
}
