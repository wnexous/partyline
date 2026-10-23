package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
)

// listActivity serves GET /activity?limit=: the latest messages across the rooms the caller
// can view, newest first (default 20, at most 50).
func (s *server) listActivity(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	pg, err := httpx.ParsePage(r, dto.DefaultActivityLimit, dto.MaxActivityLimit)
	if err != nil {
		return err
	}
	items, err := s.Rooms.Activity(r.Context(), p, pg.Limit)
	if err != nil {
		return err
	}
	out := dto.List[dto.ActivityItem]{Items: make([]dto.ActivityItem, len(items))}
	for i, it := range items {
		out.Items[i] = dto.ActivityItem{Room: dto.RoomRef{ID: it.Room.ID, Name: it.Room.Name}, Message: convert.Message(it.Message)}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
