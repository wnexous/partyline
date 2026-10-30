package rooms

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"time"
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

// ErrAlreadyAnswered is returned when an author answers the same question twice (409 already_answered,
// QR-08). The partial unique index messages_answer_once enforces it in the database.
var ErrAlreadyAnswered = errors.New("the question was already answered by this author")

// Message is a stored message with its author name, attachments and reactions.
type Message struct {
	db.MessageView
	Attachments []db.Attachment
	// Reactions are the emoji people reacted with, in the order each was first used (RCT-05).
	Reactions []Reaction
	// Chat is the chat a chat-authored message came from (CTX-01): its title, kind and
	// connector kind (platform). Nil for other authors and for chats deleted since.
	Chat *db.ListChatRefsRow
	// Interactive is the question the message asks (QR-01, QR-02), nil for every other message.
	Interactive *dto.MessageInteractive
	// Answers is the structured answer the message carries (QR-05, QR-06), nil for every other
	// message.
	Answers *dto.MessageAnswers
}

// Recipients decodes the explicit chat ids stored with a message posted before rooms stopped
// sending to chats (CID-22); a message posted since names none.
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
	Files   []File
	// Kind defaults to text; agent reactions are MessageKindReaction (REACT-01).
	Kind dto.MessageKind
	// QuickReplies and Wizard ask a question; Answers answers one. At most one of the three
	// shapes is set (QR-01, QR-02, QR-05).
	QuickReplies []string
	Wizard       *dto.WizardRequest
	Answers      *dto.AnswerSubmission
	// SessionLabel is the session an AI posts from, resolved by the API (SESS-29). It is stored for
	// agent principals only: a user's post and a chat's message never carry one (SESS-28).
	SessionLabel string
}

// ChatMessage is a message received from an external chat (connectors' inbound pipeline).
type ChatMessage struct {
	ChatID      string
	SenderID    string
	SenderAltID string
	SenderName  string
	// SenderUserID is the user a verified identity resolves to; empty when unverified (DLV-06).
	SenderUserID string
	// AuthorIID is the intelligence that wrote the message (GRP-03): the user a verified sender
	// is linked to (PEOP-02), else the sender's person. Empty for a sender without an id.
	AuthorIID string
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
	// authorIID is the author's intelligence: the user's or the agent's own id, the linked user
	// or the person of a chat sender. It is empty for system messages and chat senders without
	// an id.
	authorIID   string
	senderID    string
	senderAltID string
	senderName  string
	kind        dto.MessageKind
	text        string
	replyTo     string
	externalID  string
	files       []File
	// interactive is the canonical question document (QR-01, QR-02), empty for every other
	// message.
	interactive string
	// answers is the canonical structured answer document (QR-05, QR-06), empty for every other
	// message.
	answers string
	// authorLabel is the session that wrote an agent's message, empty for the principal and for
	// every other author (SESS-28, SESS-29).
	authorLabel string
	// answering marks a draft that answers a question: checkAnswer settles the stored-message
	// rules even when the answer cannot be rendered before the transaction (QR-05).
	answering bool
	// originalText is the text the caller gave, before the server renders an answer's text
	// (QR-05).
	originalText string
}

// Post stores a message from the calling user (needs post, ACL-04) or agent (needs
// membership) and queues its deliveries.
func (s *Service) Post(ctx context.Context, p authn.Principal, roomID string, in PostInput) (Message, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelPost); err != nil {
		return Message{}, err
	}
	author := p.UserID
	if p.Kind == authn.KindAgent {
		author = p.AgentID
	}
	if err := s.checkCounterpart(ctx, roomID, author); err != nil {
		return Message{}, err
	}
	kind := in.Kind
	if kind == "" {
		kind = dto.MessageKindText
	}
	if err := checkPostShapes(in.QuickReplies, in.Wizard, in.Answers); err != nil {
		return Message{}, err
	}

	var interactiveJSON string
	if kind != dto.MessageKindReaction {
		interactive, err := questionFrom(in.QuickReplies, in.Wizard)
		if err != nil {
			return Message{}, err
		}
		if interactive != nil {
			b, err := json.Marshal(interactive)
			if err != nil {
				return Message{}, err
			}
			interactiveJSON = string(b)
		}
	} else if in.QuickReplies != nil || in.Wizard != nil {
		check := validation.Check{}
		check.Add("quick_replies", "must not be combined with a reaction message")
		return Message{}, check.Err()
	}

	var answersJSON string
	answering := in.Answers != nil
	text := in.Text
	if answering {
		if kind != dto.MessageKindText {
			check := validation.Check{}
			check.Add("answers", "must be a text message")
			return Message{}, check.Err()
		}
		if in.ReplyTo == "" {
			// answersFor reports the missing target on the answers field, as it always has.
			_, err := answersFor(nil, in.Answers)
			return Message{}, err
		}
		target, err := s.replyTarget(ctx, roomID, in.ReplyTo)
		if err != nil {
			return Message{}, err
		}
		// Only a live target from another author is rendered against its question here. The
		// ordered checks in checkAnswer refuse the caller's own question, a deleted target and a
		// non-question inside the post transaction, where the read and the insert see the same
		// rows (QR-05, QR-09).
		if !target.DeletedAt.Valid && target.AuthorIid != author {
			question := dto.ParseMessageInteractive(target.InteractiveJson)
			answers, err := answersFor(question, in.Answers)
			if err != nil {
				return Message{}, err
			}
			b, err := json.Marshal(answers)
			if err != nil {
				return Message{}, err
			}
			answersJSON = string(b)
			text = answerText(answers)
		}
	}

	d := draft{
		authorKind: dto.AuthorKindUser, authorID: p.UserID, authorUserID: p.UserID, authorIID: p.UserID,
		kind: kind, text: text, replyTo: in.ReplyTo, files: in.Files,
		interactive: interactiveJSON, answers: answersJSON, answering: answering, originalText: in.Text,
	}
	if p.Kind == authn.KindAgent {
		// An agent's messages resolve to its owner (DLV-06).
		d.authorKind, d.authorID, d.authorIID = dto.AuthorKindAgent, p.AgentID, p.AgentID
		d.authorLabel = in.SessionLabel
	}
	return s.post(ctx, roomID, d)
}

// checkCounterpart refuses a post in a DM with an external person (access.ErrHistoryOnly): what
// the connections left before AD-015 is history, and a person is answered on their platform,
// through the connection (CID-21). It also refuses a post in a DM whose other participant is a
// disabled user (DM-02). The DMs stay readable. Rooms that are not DMs, and counterparts that are
// AIs or enabled users, pass.
func (s *Service) checkCounterpart(ctx context.Context, roomID, authorIID string) error {
	r, err := s.store.R().GetRoom(ctx, roomID)
	if errors.Is(err, sql.ErrNoRows) {
		return access.ErrNotFound
	}
	if err != nil || r.Kind != string(dto.ConversationKindDM) {
		return err
	}
	for _, participant := range []string{r.DmA.String, r.DmB.String} {
		if _, err := s.store.R().GetPerson(ctx, participant); err == nil {
			return access.ErrHistoryOnly
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	other := r.DmA.String
	if other == authorIID {
		other = r.DmB.String
	}
	u, err := s.store.R().GetUser(ctx, other)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if u.DisabledAt.Valid {
		return ErrCounterpartDisabled
	}
	return nil
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
		authorKind: dto.AuthorKindChat, authorID: in.ChatID, authorUserID: in.SenderUserID, authorIID: in.AuthorIID,
		senderID: in.SenderID, senderAltID: in.SenderAltID, senderName: in.SenderName,
		kind: kind, text: in.Text, replyTo: in.ReplyTo, externalID: in.ExternalID, files: in.Files,
	})
}

// ValidatePost checks a message the way Post does before it stores it (MSG-02) and writes
// nothing, for callers that create something to post into and must not create it for a message
// that will be refused.
func ValidatePost(in PostInput) error {
	if err := checkPostShapes(in.QuickReplies, in.Wizard, in.Answers); err != nil {
		return err
	}
	return draft{text: in.Text, files: in.Files}.validate()
}

// validate applies MSG-02. An answer that could not be rendered yet is refused by checkAnswer
// with the stored-message rule before anything is inserted, so it has no text to measure here.
func (d draft) validate() error {
	if d.answering && d.answers == "" {
		return nil
	}
	return validateText(d.text, len(d.files) > 0)
}

// validateText applies MSG-02 to the text of a message that has files or not: at most 16,000 characters, and not
// empty unless there are files. An edit applies it to the files the message already has (EDT-04).
func validateText(text string, hasFiles bool) error {
	check := validation.Check{}
	if utf8.RuneCountInString(text) > dto.MaxTextLength {
		check.Add("text", "must have at most 16000 characters")
	}
	if strings.TrimSpace(text) == "" && !hasFiles {
		check.Add("text", "must not be empty without attachments")
	}
	return check.Err()
}

// post inserts the message in one write transaction: dedupe, next sequence number, attachments
// and the loop guard (with its pause notice). MessageCreated is published after commit, except
// for duplicates. Nothing is queued for a chat: a room delivers to intelligences only (CID-22).
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
		if err := s.checkReplyTo(ctx, q, roomID, d); err != nil {
			return err
		}
		if err := s.checkAnswer(ctx, q, roomID, d); err != nil {
			return err
		}
		previous, err := q.GetRoom(ctx, roomID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return access.ErrNotFound
			}
			return err
		}
		// The quiet gap is measured from the room's previous message, never from rooms.updated_at, which a
		// rename or any other edit of the room moves too.
		var previousAt time.Time
		if last, err := q.ListMessagesBefore(ctx, db.ListMessagesBeforeParams{RoomID: roomID, BeforeSeq: previous.NextSeq, Limit: 1}); err != nil {
			return err
		} else if len(last) > 0 {
			previousAt = time.UnixMilli(last[0].CreatedAt)
		}
		// An agent message written while the loop guard has the room paused is never context for a later
		// session (SN-03).
		var duringPause int64
		if d.authorKind == dto.AuthorKindAgent && previous.AgentsPaused {
			duringPause = 1
		}
		room, msg, err := s.insert(ctx, q, roomID, d, duringPause)
		if err != nil {
			return err
		}
		id = msg.MessageID
		created = append(created, msg)

		// The quiet gap ends a burst: a message posted more than QuietGap after the room's previous
		// one starts the streak again, so only a fast ping-pong pauses the room (DLV-08).
		state, pausedNow := s.guard.Next(delivery.LoopState{Streak: int(room.AgentStreak), Paused: room.AgentsPaused}, d.authorKind,
			previousAt, time.UnixMilli(room.UpdatedAt))
		if err := q.SetRoomLoopState(ctx, db.SetRoomLoopStateParams{AgentStreak: int64(state.Streak), AgentsPaused: state.Paused, ID: roomID}); err != nil {
			return err
		}
		if pausedNow { // DLV-08
			// The message that tripped the guard starts the burst: it is not context either.
			if d.authorKind == dto.AuthorKindAgent {
				if err := q.MarkMessageDuringPause(ctx, msg.MessageID); err != nil {
					return err
				}
			}
			_, notice, err := s.insert(ctx, q, roomID, draft{authorKind: dto.AuthorKindSystem, kind: dto.MessageKindSystem, text: PauseNotice}, 0)
			if err != nil {
				return err
			}
			created = append(created, notice)
		}
		return nil
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: messages.reply_to, messages.author_iid") {
			return Message{}, ErrAlreadyAnswered
		}
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

// replyTarget loads the message a reply names and checks that it belongs to the room, the rule
// checkReplyTo applies inside the write transaction. A target that does not exist and one of
// another room get the same field error, so an id says nothing about other rooms.
func (s *Service) replyTarget(ctx context.Context, roomID, messageID string) (db.MessageView, error) {
	m, err := s.store.R().GetMessageView(ctx, messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.MessageView{}, replyToNotInRoom()
	}
	if err != nil {
		return db.MessageView{}, err
	}
	if m.RoomID != roomID {
		return db.MessageView{}, replyToNotInRoom()
	}
	return m, nil
}

// replyToNotInRoom is the reply_to field error of a target that is not a message of the room.
func replyToNotInRoom() error {
	check := validation.Check{}
	check.Add("reply_to", "must be a message of the room")
	return check.Err()
}

// checkReplyTo validates the replied message: it must be a message of the room.
func (s *Service) checkReplyTo(ctx context.Context, q *db.Queries, roomID string, d draft) error {
	if d.replyTo == "" {
		return nil
	}
	n, err := q.CountRoomMessage(ctx, db.CountRoomMessageParams{ID: d.replyTo, RoomID: roomID})
	if err != nil {
		return err
	}
	check := validation.Check{}
	if n == 0 {
		check.Add("reply_to", "must be a message of the room")
	}
	return check.Err()
}

// insert takes the next sequence number and stores the message and its attachments. It returns the
// room as updated by the sequence bump.
func (s *Service) insert(ctx context.Context, q *db.Queries, roomID string, d draft, duringPause int64) (db.Room, events.MessageCreated, error) {
	now := s.now().UnixMilli()
	room, err := q.NextRoomSeq(ctx, db.NextRoomSeqParams{UpdatedAt: now, ID: roomID})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Room{}, events.MessageCreated{}, access.ErrNotFound
	}
	if err != nil {
		return db.Room{}, events.MessageCreated{}, err
	}
	seq := room.NextSeq - 1
	id := store.NewID()
	err = q.CreateMessage(ctx, db.CreateMessageParams{
		ID: id, RoomID: roomID, Seq: seq, AuthorKind: string(d.authorKind), AuthorID: d.authorID,
		AuthorUserID: d.authorUserID, AuthorIid: d.authorIID, SenderID: d.senderID, SenderAltID: d.senderAltID, SenderName: d.senderName,
		Kind: string(d.kind), Text: d.text, ReplyTo: d.replyTo, RecipientsJson: "[]",
		ExternalID: sql.NullString{String: d.externalID, Valid: d.externalID != ""}, CreatedAt: now,
		InteractiveJson: d.interactive, AnswersJson: d.answers, AuthorLabel: d.authorLabel,
		DuringPause: duringPause,
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
	return room, events.MessageCreated{RoomID: roomID, MessageID: id, Seq: seq}, nil
}

// message loads one stored message with its attachments.
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

// Details loads the attachments of stored messages. It does not authorize: callers are trusted
// server components that selected the messages themselves.
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
		out[i] = Message{
			MessageView: v,
			Attachments: []db.Attachment{},
			Interactive: dto.ParseMessageInteractive(v.InteractiveJson),
			Answers:     dto.ParseMessageAnswers(v.AnswersJson),
		}
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
	rows, err := s.store.R().ListReactionsByMessages(ctx, ids)
	if err != nil {
		return nil, err
	}
	for id, list := range groupReactions(rows) {
		out[index[id]].Reactions = list
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

// Resume clears the loop guard: resets the agent streak and resumes agent delivery (DLV-09). A group needs
// manage. A DM, which nobody manages, is resumed by whoever can see it as a user: its human participants and
// whoever manages one of its AI participants (access.CanResume). Whoever cannot see the room gets
// access.ErrNotFound and whoever sees it and may not resume it access.ErrForbidden.
func (s *Service) Resume(ctx context.Context, p authn.Principal, roomID string) (Room, error) {
	l, err := s.authorize(ctx, p, roomID, dto.LevelView)
	if err != nil {
		return Room{}, err
	}
	r, err := s.store.R().GetRoom(ctx, roomID)
	if errors.Is(err, sql.ErrNoRows) {
		return Room{}, access.ErrNotFound
	}
	if err != nil {
		return Room{}, err
	}
	if !access.CanResume(r.Kind, p, l) {
		return Room{}, access.ErrForbidden
	}
	err = s.store.WithTx(ctx, func(q *db.Queries) error {
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
