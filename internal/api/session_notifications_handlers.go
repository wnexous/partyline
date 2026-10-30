package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
)

// The owner's view of the session notifications (SN-08): the settings of every live session per
// conversation, and the way to change one. Whoever manages the AI acts on them; everyone else gets 403.

// sessionNotifications lists the notification state of one of an AI's sessions in every room of the AI
// (GET /api/v1/agents/{id}/sessions/{label}/notifications). The principal is addressed as "_principal";
// a labelled session must be live. A session the AI does not have, or a room the AI is not a member of, is
// a 404.
func (s *server) sessionNotifications(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	a, cs, err := s.ownerSession(r, p)
	if err != nil {
		return err
	}
	list, err := s.Agents.SessionNotifications(r.Context(), a.ID, cs)
	if err != nil {
		return err
	}
	out := dto.List[dto.RoomNotification]{Items: make([]dto.RoomNotification, len(list))}
	for i, n := range list {
		out.Items[i] = roomNotification(n)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// setSessionNotification changes the notification setting of one session in one room
// (PUT /api/v1/agents/{id}/sessions/{label}/notifications/{room}, SN-08). Whoever manages the AI may send
// it, and notify must be on or mentions (422 on the field otherwise).
func (s *server) setSessionNotification(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.NotifyRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if !agents.ValidNotify(req.Notify) {
		return httpx.Invalid(map[string]string{"notify": "must be on or mentions"})
	}
	a, cs, err := s.ownerSession(r, p)
	if err != nil {
		return err
	}
	roomID := r.PathValue("room")
	if _, ok, err := s.Agents.SessionNotification(r.Context(), a.ID, roomID, cs); err != nil {
		return err
	} else if !ok {
		return errRoomNotFound
	}
	if err := s.Agents.SetSessionNotify(r.Context(), a.ID, cs.Key, roomID, cs.IsPrincipal, req.Notify); err != nil {
		return err
	}
	notification, _, err := s.Agents.SessionNotification(r.Context(), a.ID, roomID, cs)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, roomNotification(notification))
	return nil
}

// ownerSession loads the AI the path names for whoever manages it, and the session the path's label
// addresses. An AI the caller cannot see is 404, one they only see is 403 (POS-04), and a session that is
// not live (or, for the principal, does not exist) is 404.
func (s *server) ownerSession(r *http.Request, p authn.Principal) (agents.Agent, agents.CallingSession, error) {
	a, err := s.Agents.Get(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return agents.Agent{}, agents.CallingSession{}, err
	}
	if a.Level != dto.LevelManage {
		return agents.Agent{}, agents.CallingSession{}, access.ErrForbidden
	}
	cs, ok, err := s.Agents.LiveCallingSession(r.Context(), a.ID, r.PathValue("label"))
	if err != nil {
		return agents.Agent{}, agents.CallingSession{}, err
	}
	if !ok {
		return agents.Agent{}, agents.CallingSession{}, errSessionNotFound
	}
	return a, cs, nil
}

var (
	// errSessionNotFound is the owner's notification route answering a session that is not live.
	errSessionNotFound = httpx.NewError(http.StatusNotFound, dto.ErrorCodeNotFound, "session not found")
	// errRoomNotFound answers a room the AI is not a member of.
	errRoomNotFound = httpx.NewError(http.StatusNotFound, dto.ErrorCodeNotFound, "room not found")
)
