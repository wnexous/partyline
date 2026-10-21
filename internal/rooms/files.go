package rooms

import (
	"context"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// FilesQuery selects a page of a room's files: Kind filters by dto.FileKind ("" = all), Before
// is the id of the last file of the previous page ("" = newest) and Limit defaults to
// dto.DefaultPageSize, capped at dto.MaxPageSize.
type FilesQuery struct {
	Kind   string
	Before string
	Limit  int
}

var fileKinds = map[dto.FileKind]bool{
	dto.FileKindImage: true, dto.FileKindAudio: true, dto.FileKindVideo: true, dto.FileKindDocument: true,
}

// Files returns the attachments of a room the caller can view, newest first, and whether more
// follow (FILES-01..03). No view → access.ErrNotFound; an unknown kind → 422 on kind.
func (s *Service) Files(ctx context.Context, p authn.Principal, roomID string, q FilesQuery) ([]db.ListRoomFilesRow, bool, error) {
	if _, err := s.authorize(ctx, p, roomID, dto.LevelView); err != nil {
		return nil, false, err
	}
	if q.Kind != "" && !fileKinds[dto.FileKind(q.Kind)] {
		check := validation.Check{}
		check.Add("kind", "must be image, audio, video or document")
		return nil, false, check.Err()
	}
	limit := q.Limit
	if limit <= 0 {
		limit = dto.DefaultPageSize
	}
	limit = min(limit, dto.MaxPageSize)
	before := q.Before
	if before == "" {
		before = "~" // sorts after every UUID
	}
	rows, err := s.store.R().ListRoomFiles(ctx, db.ListRoomFilesParams{RoomID: roomID, Before: before, Kind: q.Kind, Limit: int64(limit + 1)})
	if err != nil {
		return nil, false, err
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	return rows, more, nil
}
