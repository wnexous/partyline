package rooms

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/avatars"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/store/db"
)

// SetAvatar replaces the photo of a group the caller manages with the image read from r: PNG, JPEG or WebP
// up to 2 MiB, stored centre-cropped as a 256x256 PNG like the avatar of an intelligence (GAV-01). The caller is
// authorized before r is read (GAV-02); a DM has no photo of its own, and nobody manages it. An image that is not
// acceptable is avatars.ErrInvalid and the old photo stays (GAV-03). The same image again changes nothing.
func (s *Service) SetAvatar(ctx context.Context, p authn.Principal, roomID string, r io.Reader) (Room, error) {
	if s.blobs == nil {
		return Room{}, errors.New("no blob store")
	}
	if _, err := s.authorize(ctx, p, roomID, dto.LevelManage); err != nil {
		return Room{}, err
	}
	img, err := avatars.Process(r)
	if err != nil {
		return Room{}, err
	}
	sha, _, err := s.blobs.Put(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		return Room{}, err
	}
	return s.storeAvatar(ctx, p, roomID, sha)
}

// ClearAvatar removes the photo of a group the caller manages; the initials show again. Removing a photo that is
// not there is not an error.
func (s *Service) ClearAvatar(ctx context.Context, p authn.Principal, roomID string) error {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelManage); err != nil {
		return err
	}
	_, err := s.storeAvatar(ctx, p, roomID, "")
	return err
}

// storeAvatar points the group at the avatar blob sha ("" for none) and announces the change to the people who
// see the group (GAV-04). The replaced blob stays until the retention sweep finds nothing referencing it.
func (s *Service) storeAvatar(ctx context.Context, p authn.Principal, roomID, sha string) (Room, error) {
	var changed bool
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetRoom(ctx, roomID)
		if errors.Is(err, sql.ErrNoRows) {
			return access.ErrNotFound // deleted since the caller was authorized
		}
		if err != nil {
			return err
		}
		if cur.AvatarSha256 == sha {
			return nil
		}
		changed = true
		return q.SetRoomAvatar(ctx, db.SetRoomAvatarParams{AvatarSha256: sha, UpdatedAt: s.now().UnixMilli(), ID: roomID})
	})
	if err != nil {
		return Room{}, err
	}
	if changed {
		s.bus.Publish(events.RoomChanged{RoomID: roomID})
	}
	return s.Get(ctx, p, roomID)
}

// Avatar opens the stored PNG of a group the caller can see, with its content hash. Without view, for a group
// without a photo, for a DM and for a blob that is gone it is access.ErrNotFound.
func (s *Service) Avatar(ctx context.Context, p authn.Principal, roomID string) (*os.File, string, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelView); err != nil {
		return nil, "", err
	}
	room, err := s.store.R().GetRoom(ctx, roomID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", access.ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	if room.AvatarSha256 == "" || s.blobs == nil {
		return nil, "", access.ErrNotFound
	}
	f, err := s.blobs.Open(room.AvatarSha256)
	if errors.Is(err, attachments.ErrNotFound) {
		return nil, "", access.ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	return f, room.AvatarSha256, nil
}
