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
	// Blobs is swept by the retention job; nil skips the blob sweep.
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
	BridgeChats   bool
	RetentionDays *int // nil = DefaultRetentionDays; 0 keeps messages forever
}

func validateRoom(name, description string, retention int) error {
	check := validation.Check{}
	if n := utf8.RuneCountInString(name); n == 0 || n > MaxNameLength {
		check.Add("name", "must have 1 to 100 characters")
	}
	if utf8.RuneCountInString(description) > MaxDescriptionLength {
		check.Add("description", "must have at most 1000 characters")
	}
	if retention < 0 {
		check.Add("retention_days", "must be 0 (keep forever) or more")
	}
	return check.Err()
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
	name := strings.TrimSpace(in.Name)
	if err := validateRoom(name, in.Description, retention); err != nil {
		return Room{}, err
	}
	now := s.now().UnixMilli()
	id := store.NewID()
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		return q.CreateRoom(ctx, db.CreateRoomParams{
			ID: id, OwnerID: p.UserID, Name: name, Description: in.Description,
			BridgeChats: in.BridgeChats, RetentionDays: int64(retention), CreatedAt: now, UpdatedAt: now,
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
	return s.view(ctx, row.Room, row.OwnerUsername, row.OwnerDisplayName, l)
}

func (s *Service) view(ctx context.Context, r db.Room, ownerUsername, ownerDisplay string, l access.Level) (Room, error) {
	v, err := s.az.Sharing(ctx, access.KindRoom, r.ID)
	if err != nil {
		return Room{}, err
	}
	return Room{Room: r, OwnerUsername: ownerUsername, OwnerDisplayName: ownerDisplay, Level: l, Visibility: v.Mode}, nil
}

// List returns the rooms the calling user can view, by name.
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
		if row.Room.OwnerID == p.UserID {
			l = dto.LevelManage
		}
		if l == access.None {
			continue
		}
		r, err := s.view(ctx, row.Room, row.OwnerUsername, row.OwnerDisplayName, l)
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
	BridgeChats   *bool
	RetentionDays *int
}

// Update changes a room's settings; requires manage.
func (s *Service) Update(ctx context.Context, p authn.Principal, roomID string, in UpdateInput) (Room, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelManage); err != nil {
		return Room{}, err
	}
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		r, err := q.GetRoom(ctx, roomID)
		if err != nil {
			return err
		}
		if in.Name != nil {
			r.Name = strings.TrimSpace(*in.Name)
		}
		if in.Description != nil {
			r.Description = *in.Description
		}
		if in.BridgeChats != nil {
			r.BridgeChats = *in.BridgeChats
		}
		if in.RetentionDays != nil {
			r.RetentionDays = int64(*in.RetentionDays)
		}
		if err := validateRoom(r.Name, r.Description, int(r.RetentionDays)); err != nil {
			return err
		}
		return q.UpdateRoom(ctx, db.UpdateRoomParams{
			Name: r.Name, Description: r.Description, BridgeChats: r.BridgeChats,
			RetentionDays: r.RetentionDays, UpdatedAt: s.now().UnixMilli(), ID: roomID,
		})
	})
	if err != nil {
		return Room{}, err
	}
	s.bus.Publish(events.RoomChanged{RoomID: roomID})
	return s.Get(ctx, p, roomID)
}

// Delete removes a room with its members, grants, messages, attachments, cursors and outbox
// entries (ROOM-02); requires manage.
func (s *Service) Delete(ctx context.Context, p authn.Principal, roomID string) error {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelManage); err != nil {
		return err
	}
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteGrants(ctx, db.DeleteGrantsParams{ResourceKind: string(access.KindRoom), ResourceID: roomID}); err != nil {
			return err
		}
		// Members, messages, attachments, cursors and outbox rows cascade from the room.
		return q.DeleteRoom(ctx, roomID)
	})
	if err != nil {
		return err
	}
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

// SetSharing replaces the room's grants with a visibility preset (ACL-07); requires manage.
func (s *Service) SetSharing(ctx context.Context, p authn.Principal, roomID string, v access.Visibility) (access.Visibility, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelManage); err != nil {
		return access.Visibility{}, err
	}
	r, err := s.store.R().GetRoom(ctx, roomID)
	if err != nil {
		return access.Visibility{}, err
	}
	if err := s.az.SetVisibility(ctx, p.UserID, access.KindRoom, roomID, r.OwnerID, v); err != nil {
		return access.Visibility{}, err
	}
	return s.az.Sharing(ctx, access.KindRoom, roomID)
}
