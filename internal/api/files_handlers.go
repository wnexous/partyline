package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/rooms"
)

// listRoomFiles answers GET /rooms/{id}/attachments?kind=&before=&limit= (FILES-01..03): the
// room's files newest first; no view → 404, unknown kind → 422.
func (s *server) listRoomFiles(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	pg, err := httpx.ParsePage(r, dto.DefaultPageSize, dto.MaxPageSize)
	if err != nil {
		return err
	}
	list, more, err := s.Rooms.Files(r.Context(), p, r.PathValue("id"), rooms.FilesQuery{
		Kind: r.URL.Query().Get("kind"), Before: pg.Before, Limit: pg.Limit,
	})
	if err != nil {
		return err
	}
	out := dto.Page[dto.RoomFile]{Items: make([]dto.RoomFile, len(list)), HasMore: more}
	for i, f := range list {
		out.Items[i] = dto.RoomFile{
			ID: f.ID, Name: f.Name, Mime: f.Mime, Size: f.Size, Status: dto.AttachmentStatus(f.Status),
			Kind: dto.FileKind(f.Kind), MessageID: f.MessageID, MessageSeq: f.MessageSeq,
			AuthorKind: dto.AuthorKind(f.AuthorKind), AuthorName: f.AuthorName, Author: convert.FileAuthor(f), CreatedAt: msTime(f.CreatedAt),
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
