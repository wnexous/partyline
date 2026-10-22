// Package rooms owns rooms, their members and their messages (AD-002). Every method
// authorizes the caller through package access: callers without view get
// access.ErrNotFound, callers that can view but not act get access.ErrForbidden.
package rooms

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/delivery"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// ErrAlreadyMember is returned when adding a member twice (409).
var ErrAlreadyMember = errors.New("already a member of the room")

// ErrDMMembersFixed is returned when a member of a direct message is added, changed or removed:
// a DM has exactly its two intelligences (422 dm_members_fixed, DM-02).
var ErrDMMembersFixed = errors.New("the members of a direct message are fixed")

// ErrCounterpartDisabled is returned when posting in a direct message whose other participant is a
// disabled user: the DM stays readable (403 counterpart_disabled, DM-02).
var ErrCounterpartDisabled = errors.New("the other participant of the direct message is disabled")

// Limits of room fields.
const (
	MaxNameLength        = 100
	MaxDescriptionLength = 1000
	// DefaultRetentionDays applies when a room is created without retention (OPS-02).
	DefaultRetentionDays = 90
)

// Deps are the collaborators of the Service.
type Deps struct {
	Store  *store.Store
	Access *access.Authorizer
	Bus    *events.Bus
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
	// LoopGuard pauses agent delivery after consecutive agent messages (DLV-08).
	LoopGuard delivery.LoopGuard
	// Blobs is swept by the retention job and cleaned when a room is deleted; nil skips both.
	Blobs *attachments.Store
}

// Service implements rooms, members and messages.
type Service struct {
	store *store.Store
	az    *access.Authorizer
	bus   *events.Bus
	now   func() time.Time
	guard delivery.LoopGuard
	blobs *attachments.Store
}

// New returns a Service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{store: d.Store, az: d.Access, bus: d.Bus, now: d.Now, guard: d.LoopGuard, blobs: d.Blobs}
}

// Room is a room as seen by one caller.
type Room struct {
	db.Room
	OwnerUsername    string
	OwnerDisplayName string
	// Level is the caller's level (ACL-02).
	Level access.Level
	// Visibility is the sharing preset the room's grants form.
	Visibility dto.Visibility
	// MemberCount counts the room's agents, chats and users.
	MemberCount int64
	// CatchAll is set while a connector routes new chats to the room (ROUTE-07).
	CatchAll bool
	// InstructionsByName is the username of InstructionsBy; empty when unknown (INS-04).
	InstructionsByName string
	// OwnerIntelligence is the owner's profile (ID empty when the owner has none).
	OwnerIntelligence db.Intelligence
	// Participants are the two intelligences of a DM, by id; none for a group.
	Participants []Participant
}

// Participant is one of the two intelligences of a DM.
type Participant struct {
	db.Intelligence
	// Role is user, ai or person; Platform is the connector kind of a person.
	Role     string
	Platform string
}

// LastSeq is the sequence number of the room's latest message (0 when empty).
func (r Room) LastSeq() int64 { return r.NextSeq - 1 }

// Require checks that the caller holds want on the room, with the errors of authorize. Use it
// before expensive work such as storing uploads.
func (s *Service) Require(ctx context.Context, p authn.Principal, roomID string, want access.Level) error {
	_, err := s.authorize(ctx, p, roomID, want)
	return err
}

// authorize resolves the caller's level on a room and requires want: no view is
// ErrNotFound (ACL-03), view without want is ErrForbidden (ACL-04).
func (s *Service) authorize(ctx context.Context, p authn.Principal, roomID string, want access.Level) (access.Level, error) {
	l, err := s.az.RoomLevel(ctx, p, roomID)
	if err != nil {
		return access.None, err
	}
	if !access.AtLeast(l, dto.LevelView) {
		return access.None, access.ErrNotFound
	}
	if !access.AtLeast(l, want) {
		return l, access.ErrForbidden
	}
	return l, nil
}

// CreateInput is the data of a new room.
type CreateInput struct {
	Name          string
	Description   string
	Instructions  string // INS-01, at most dto.MaxRoomInstructions characters
	BridgeChats   bool
	RetentionDays *int // nil = DefaultRetentionDays; 0 keeps messages forever
}

func validateRoom(name, description, instructions string, retention int) error {
	check := validation.Check{}
	if n := utf8.RuneCountInString(name); n == 0 || n > MaxNameLength {
		check.Add("name", "must have 1 to 100 characters")
	}
	if utf8.RuneCountInString(description) > MaxDescriptionLength {
		check.Add("description", "must have at most 1000 characters")
	}
	if utf8.RuneCountInString(instructions) > dto.MaxRoomInstructions {
		check.Add("instructions", "must have at most 4000 characters")
	}
	if retention < 0 {
		check.Add("retention_days", "must be 0 (keep forever) or more")
	}
	return check.Err()
}

// roomText is a room's name, description or instructions as the server stores and compares it:
// without leading or trailing white space, where white space is the union of Go's
// unicode.IsSpace and JavaScript's String.prototype.trim (which also removes U+FEFF). The web
// form trims what it sends, so comparing trimmed text keeps a save that only trims from making
// the saver the author of another manager's text (INS-04, INS-06).
func roomText(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' })
}

// instructionsBy is the user recorded as the author of saved instructions (INS-04): the
// caller, or nobody when the instructions are empty.
func instructionsBy(p authn.Principal, instructions string) string {
	if instructions == "" {
		return ""
	}
	return p.UserID
}

// Create makes a room owned by the caller, who gets manage (ACL-01).
func (s *Service) Create(ctx context.Context, p authn.Principal, in CreateInput) (Room, error) {
	if p.Kind != authn.KindUser {
		return Room{}, access.ErrForbidden
	}
	retention := DefaultRetentionDays
	if in.RetentionDays != nil {
		retention = *in.RetentionDays
	}
	name, description, instructions := roomText(in.Name), roomText(in.Description), roomText(in.Instructions)
	if err := validateRoom(name, description, instructions, retention); err != nil {
		return Room{}, err
	}
	now := s.now().UnixMilli()
	id := store.NewID()
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		return q.CreateRoom(ctx, db.CreateRoomParams{
			ID: id, OwnerID: p.UserID, Name: name, Description: description, Instructions: instructions,
			InstructionsBy: instructionsBy(p, instructions),
			BridgeChats:    in.BridgeChats, RetentionDays: int64(retention), CreatedAt: now, UpdatedAt: now,
			UpdatedBy: p.UserID, // INS-06: the creator wrote the name and description
		})
	})
	if err != nil {
		return Room{}, err
	}
	s.bus.Publish(events.RoomChanged{RoomID: id})
	return s.Get(ctx, p, id)
}

// Get returns a room the caller can view.
func (s *Service) Get(ctx context.Context, p authn.Principal, roomID string) (Room, error) {
	l, err := s.authorize(ctx, p, roomID, dto.LevelView)
	if err != nil {
		return Room{}, err
	}
	row, err := s.store.R().GetRoomWithOwner(ctx, roomID)
	if errors.Is(err, sql.ErrNoRows) {
		return Room{}, access.ErrNotFound
	}
	if err != nil {
		return Room{}, err
	}
	owner := ownerIntelligence(row.Room.OwnerID, row.OwnerIkind, row.OwnerIname, row.OwnerHandle, row.OwnerAvatar)
	return s.view(ctx, row.Room, row.OwnerUsername, row.OwnerDisplayName, owner, row.MemberCount, l)
}

// ownerIntelligence rebuilds an owner's profile from the columns a room query joins; the zero
// value when the owner has no intelligence.
func ownerIntelligence(id, kind, name, handle, avatar string) db.Intelligence {
	if handle == "" {
		return db.Intelligence{}
	}
	return db.Intelligence{ID: id, Kind: kind, DisplayName: name, Handle: handle, AvatarSha256: avatar}
}

func (s *Service) view(ctx context.Context, r db.Room, ownerUsername, ownerDisplay string, owner db.Intelligence, members int64, l access.Level) (Room, error) {
	v, err := s.az.Sharing(ctx, access.KindRoom, r.ID)
	if err != nil {
		return Room{}, err
	}
	catchAll, err := s.store.R().CountCatchAllConnectors(ctx, sql.NullString{String: r.ID, Valid: true})
	if err != nil {
		return Room{}, err
	}
	by := ""
	if r.InstructionsBy != "" {
		u, err := s.store.R().GetUser(ctx, r.InstructionsBy)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Room{}, err
		}
		by = u.Username
	}
	var participants []Participant
	if r.Kind == string(dto.ConversationKindDM) {
		rows, err := s.store.R().ListRoomParticipants(ctx, r.ID)
		if err != nil {
			return Room{}, err
		}
		for _, row := range rows {
			participants = append(participants, Participant{Intelligence: row.Intelligence, Role: row.Role, Platform: row.Platform})
		}
	}
	return Room{
		Room: r, OwnerUsername: ownerUsername, OwnerDisplayName: ownerDisplay, Level: l, Visibility: v.Mode,
		MemberCount: members, CatchAll: catchAll > 0, InstructionsByName: by, OwnerIntelligence: owner,
		Participants: participants,
	}, nil
}

// List returns the conversations the calling user lists, by name: the groups they own, were
// granted or are a member of, and the DMs they take part in. A DM the user only views through
// an AI is not listed (it is reachable by id).
func (s *Service) List(ctx context.Context, p authn.Principal) ([]Room, error) {
	if p.Kind != authn.KindUser {
		return nil, access.ErrForbidden
	}
	granted, err := s.az.GrantedRoomLevels(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.R().ListRoomsForUser(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]Room, 0, len(rows))
	for _, row := range rows {
		l := granted[row.Room.ID]
		if row.Room.OwnerID == p.UserID && row.Room.Kind == string(dto.ConversationKindGroup) {
			l = dto.LevelManage // a DM has no owner to manage it (DM-02)
		}
		if l == access.None {
			continue
		}
		owner := ownerIntelligence(row.Room.OwnerID, row.OwnerIkind, row.OwnerIname, row.OwnerHandle, row.OwnerAvatar)
		r, err := s.view(ctx, row.Room, row.OwnerUsername, row.OwnerDisplayName, owner, row.MemberCount, l)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// UpdateInput changes room settings; nil fields are unchanged.
type UpdateInput struct {
	Name          *string
	Description   *string
	Instructions  *string
	BridgeChats   *bool
	Broadcast     *bool // ROUTE-06
	RetentionDays *int
}

// catchAllOff is the 422 reason for turning broadcast or bridge_chats on in a room that is a
// connector's catch-all room (ROUTE-07).
const catchAllOff = "must stay off while the room is a catch-all room"

// Update changes a room's settings; requires manage. While the room is a connector's
// catch-all room, broadcast and bridge_chats cannot be turned on (ROUTE-07).
func (s *Service) Update(ctx context.Context, p authn.Principal, roomID string, in UpdateInput) (Room, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelManage); err != nil {
		return Room{}, err
	}
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		r, err := q.GetRoom(ctx, roomID)
		if err != nil {
			return err
		}
		// Text equal to the stored text once both are trimmed is no new text: it keeps its
		// author and the stored copy (INS-04, INS-06).
		if in.Name != nil && roomText(*in.Name) != roomText(r.Name) {
			r.Name = roomText(*in.Name)
			r.UpdatedBy = p.UserID // INS-06
		}
		if in.Description != nil && roomText(*in.Description) != roomText(r.Description) {
			r.Description = roomText(*in.Description)
			r.UpdatedBy = p.UserID
		}
		if in.Instructions != nil && roomText(*in.Instructions) != roomText(r.Instructions) {
			r.Instructions = roomText(*in.Instructions)
			r.InstructionsBy = instructionsBy(p, r.Instructions)
		}
		if in.BridgeChats != nil {
			r.BridgeChats = *in.BridgeChats
		}
		if in.Broadcast != nil {
			r.Broadcast = *in.Broadcast
		}
		if (in.Broadcast != nil && *in.Broadcast) || (in.BridgeChats != nil && *in.BridgeChats) {
			n, err := q.CountCatchAllConnectors(ctx, sql.NullString{String: roomID, Valid: true})
			if err != nil {
				return err
			}
			if n > 0 {
				check := validation.Check{}
				if in.Broadcast != nil && *in.Broadcast {
					check.Add("broadcast", catchAllOff)
				}
				if in.BridgeChats != nil && *in.BridgeChats {
					check.Add("bridge_chats", catchAllOff)
				}
				return check.Err()
			}
		}
		if in.RetentionDays != nil {
			r.RetentionDays = int64(*in.RetentionDays)
		}
		if err := validateRoom(r.Name, r.Description, r.Instructions, int(r.RetentionDays)); err != nil {
			return err
		}
		return q.UpdateRoom(ctx, db.UpdateRoomParams{
			Name: r.Name, Description: r.Description, Instructions: r.Instructions, InstructionsBy: r.InstructionsBy,
			BridgeChats: r.BridgeChats, Broadcast: r.Broadcast, RetentionDays: r.RetentionDays,
			UpdatedAt: s.now().UnixMilli(), UpdatedBy: r.UpdatedBy, ID: roomID,
		})
	})
	if err != nil {
		return Room{}, err
	}
	s.bus.Publish(events.RoomChanged{RoomID: roomID})
	return s.Get(ctx, p, roomID)
}

// Delete removes a room with its members, grants, messages, attachments, cursors and outbox
// entries, then its blob files no other attachment references (ROOM-02); requires manage.
func (s *Service) Delete(ctx context.Context, p authn.Principal, roomID string) error {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelManage); err != nil {
		return err
	}
	var shas []string
	err := s.store.WithTx(ctx, func(q *db.Queries) (err error) {
		if shas, err = q.ListRoomAttachmentSHAs(ctx, roomID); err != nil {
			return err
		}
		if err := q.DeleteGrants(ctx, db.DeleteGrantsParams{ResourceKind: string(access.KindRoom), ResourceID: roomID}); err != nil {
			return err
		}
		// Members, messages, attachments, cursors and outbox rows cascade from the room.
		return q.DeleteRoom(ctx, roomID)
	})
	if err != nil {
		return err
	}
	s.removeRoomBlobs(ctx, shas)
	s.bus.Publish(events.RoomChanged{RoomID: roomID, Deleted: true})
	return nil
}

// Sharing returns the room's visibility preset; requires view.
func (s *Service) Sharing(ctx context.Context, p authn.Principal, roomID string) (access.Visibility, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelView); err != nil {
		return access.Visibility{}, err
	}
	return s.az.Sharing(ctx, access.KindRoom, roomID)
}

// SetSharing replaces the room's grants with a visibility preset (ACL-07); requires manage. A
// DM cannot be shared: whoever can view it gets access.ErrDMNoSharing (DM-08).
func (s *Service) SetSharing(ctx context.Context, p authn.Principal, roomID string, v access.Visibility) (access.Visibility, error) {
	l, err := s.authorize(ctx, p, roomID, dto.LevelView)
	if err != nil {
		return access.Visibility{}, err
	}
	r, err := s.store.R().GetRoom(ctx, roomID)
	if err != nil {
		return access.Visibility{}, err
	}
	if r.Kind == string(dto.ConversationKindDM) {
		return access.Visibility{}, access.ErrDMNoSharing // whoever views it is told so (DM-08)
	}
	if !access.AtLeast(l, dto.LevelManage) {
		return access.Visibility{}, access.ErrForbidden
	}
	if err := s.az.SetVisibility(ctx, p.UserID, access.KindRoom, roomID, r.OwnerID, v); err != nil {
		return access.Visibility{}, err
	}
	return s.az.Sharing(ctx, access.KindRoom, roomID)
}
