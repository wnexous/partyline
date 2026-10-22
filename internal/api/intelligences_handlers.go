package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/store/db"
)

// intelligenceRoutes are the profile endpoints of users, AIs and people (PROF-03, PROF-05).
func (s *server) intelligenceRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/intelligences/{id}", authed(s.getIntelligence))
	s.handle(mux, "PATCH /api/v1/intelligences/{id}", authed(s.updateIntelligence))
}

// getIntelligence returns the profile of an intelligence the caller can see; others are 404.
func (s *server) getIntelligence(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	prof, err := s.Intelligences.Get(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, s.toProfile(prof))
	return nil
}

// updateIntelligence changes the profile of a user (their own), an AI (its owner) or a
// person (the users of its connector): 403 for a caller who can see but not edit, 404 for one
// who cannot see, 422 per invalid field, 409 handle_taken (PROF-10).
func (s *server) updateIntelligence(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.UpdateProfileRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	prof, err := s.Intelligences.Update(r.Context(), p, r.PathValue("id"), profileUpdate(req))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, s.toProfile(prof))
	return nil
}

func profileUpdate(req dto.UpdateProfileRequest) intelligences.ProfileUpdate {
	return intelligences.ProfileUpdate{DisplayName: req.DisplayName, Handle: req.Handle, Bio: req.Bio}
}

// toProfile renders a profile: the summary, the bio, what the caller may do and the details
// of the kind of intelligence.
func (s *server) toProfile(prof intelligences.Profile) dto.IntelligenceProfile {
	out := dto.IntelligenceProfile{
		IntelligenceRef: convert.ProfileRef(prof), Bio: prof.Bio, CanEdit: prof.CanEdit,
		Apps: []dto.App{}, Conversations: []dto.RoomRef{},
	}
	switch prof.Role {
	case intelligences.RoleUser:
		out.Apps = append(out.Apps, dto.App{Kind: "web", Label: "Web app"})
	case intelligences.RoleAI:
		a := prof.Agent
		out.AI = &dto.AIDetail{
			Owner:   convert.IntelligenceRef(a.Owner, dto.IntelligenceRoleUser, ""),
			Machine: a.Machine, MachineLabel: a.MachineLabel,
			Folder: a.Folder, FolderLabel: intelligences.FolderLabel(a.Home, a.Folder),
			App: a.Harness, Online: s.Agents.Online(a.ID),
			LastSeenAt: convert.OptTime(a.LastSeenAt), ConnectedAt: convert.OptTime(a.ConnectedAt), MyLevel: a.Level,
		}
		out.Apps = append(out.Apps, dto.App{Kind: a.Harness, Label: agents.HarnessLabel(a.Harness)})
	}
	return out
}

// writeMe answers with the user rendered as Me, with its intelligence (PROF-01).
func (s *server) writeMe(w http.ResponseWriter, r *http.Request, status int, u db.User) error {
	me := toMe(u)
	rows, err := s.Intelligences.Rows(r.Context(), []string{u.ID})
	if err != nil {
		return err
	}
	if row, ok := rows[u.ID]; ok {
		me.Intelligence = convert.IntelligenceRef(row, dto.IntelligenceRoleUser, "")
	} else { // an account that has no intelligence (created before they existed, not migrated)
		me.Intelligence = dto.IntelligenceRef{ID: u.ID, Kind: dto.IntelligenceKindHuman, Role: dto.IntelligenceRoleUser, DisplayName: me.DisplayName}
	}
	httpx.WriteJSON(w, status, me)
	return nil
}
