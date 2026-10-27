package api

import (
	"net/http"

	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/connectors"
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
	return s.writeProfile(w, r, p, prof)
}

// updateIntelligence changes the profile of a user or an AI: the identity itself and whoever manages it
// in an organization they share (POS-04). A person is history and cannot be edited (history_only). 403
// for a caller who can see but not edit, 404 for one who cannot see, 422 per invalid field, 409
// handle_taken (PROF-10).
func (s *server) updateIntelligence(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	var req dto.UpdateProfileRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	prof, err := s.Intelligences.Update(r.Context(), p, r.PathValue("id"), profileUpdate(req))
	if err != nil {
		return err
	}
	return s.writeProfile(w, r, p, prof)
}

func profileUpdate(req dto.UpdateProfileRequest) intelligences.ProfileUpdate {
	return intelligences.ProfileUpdate{DisplayName: req.DisplayName, Handle: req.Handle, Bio: req.Bio}
}

// writeProfile answers with the profile as the caller reads it.
func (s *server) writeProfile(w http.ResponseWriter, r *http.Request, p authn.Principal, prof intelligences.Profile) error {
	out := s.toProfile(prof)
	if prof.Role == intelligences.RolePerson {
		convs, err := s.Intelligences.Conversations(r.Context(), p, prof)
		if err != nil {
			return err
		}
		for _, c := range convs {
			ref := dto.RoomRef{ID: c.ID, Name: c.Name}
			if c.Kind == dto.ConversationKindDM {
				ref.Kind = c.Kind
			}
			out.Conversations = append(out.Conversations, ref)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// toProfile renders a profile: the summary, the bio, what the caller may do and the details
// of the kind of intelligence. The conversations of a person are added by writeProfile.
func (s *server) toProfile(prof intelligences.Profile) dto.IntelligenceProfile {
	out := dto.IntelligenceProfile{
		IntelligenceRef: convert.ProfileRef(prof), Bio: prof.Bio, CanEdit: prof.CanEdit, CanEndSession: prof.CanEndSession,
		Apps: []dto.App{}, Conversations: []dto.RoomRef{}, Memberships: make([]dto.ProfileMembership, 0, len(prof.Memberships)),
	}
	for _, m := range prof.Memberships {
		out.Memberships = append(out.Memberships, dto.ProfileMembership{
			OrgID: m.Organization.ID, OrgName: m.Organization.Name, OrgSlug: m.Organization.Slug,
			Permission: string(m.Permission), PositionID: m.PositionID, PositionName: m.PositionName,
			CanDelete: m.CanDelete, DeleteEffect: string(m.DeleteEffect),
		})
	}
	switch prof.Role {
	case intelligences.RoleUser:
		out.Apps = append(out.Apps, dto.App{Kind: "web", Label: "Web app"})
		for _, a := range prof.Accounts { // the verified chat accounts are apps of the user (PEOP-02)
			out.Apps = append(out.Apps, dto.App{
				Kind: a.Connector.Kind, Label: connectors.PlatformLabel(a.Connector.Kind) + ": " + a.Identity.ExternalID, ConnectorID: a.Connector.ID,
			})
		}
	case intelligences.RoleAI:
		a := prof.Agent
		out.AI = &dto.AIDetail{
			Owner:  convert.IntelligenceRef(a.Owner, dto.IntelligenceRoleUser, ""),
			Folder: a.Folder, FolderLabel: intelligences.FolderLabel(a.Home, a.Folder),
			App: a.Harness, Online: s.Agents.Online(a.ID),
			LastSeenAt: convert.OptTime(a.LastSeenAt), ConnectedAt: convert.OptTime(a.ConnectedAt),
			Sessions: liveSessions(s.Agents.Sessions(a.ID)), MyLevel: a.Level,
		}
		out.Apps = append(out.Apps, dto.App{Kind: a.Harness, Label: agents.HarnessLabel(a.Harness)})
	case intelligences.RolePerson:
		pi := prof.Person
		if !pi.CanUse { // the platform, not the account behind it
			out.Apps = append(out.Apps, dto.App{Kind: pi.Connector.Kind, Label: connectors.PlatformLabel(pi.Connector.Kind)})
			break
		}
		out.Apps = append(out.Apps, dto.App{
			Kind: pi.Connector.Kind, Label: connectors.PlatformLabel(pi.Connector.Kind) + ": " + pi.ExternalID, ConnectorID: pi.Connector.ID,
		})
		out.Person = &dto.PersonDetail{
			Connector:  dto.ConnectorRef{ID: pi.Connector.ID, Kind: pi.Connector.Kind, Name: pi.Connector.Name},
			ExternalID: pi.ExternalID, NameLocked: pi.NameLocked,
		}
		if pi.LinkedUser != nil {
			ref := convert.IntelligenceRef(*pi.LinkedUser, dto.IntelligenceRoleUser, "")
			out.Person.LinkedUser = &ref
		}
	}
	for _, c := range prof.Operated { // the connectors it operates are its apps too (ACC-07)
		label := c.AccountLabel
		if label == "" {
			label = c.Name
		}
		out.Apps = append(out.Apps, dto.App{Kind: c.Kind, Label: connectors.PlatformLabel(c.Kind) + ": " + label, ConnectorID: c.ID})
	}
	return out
}

// writeMe answers with the user rendered as Me, with its intelligence (PROF-01).
func (s *server) writeMe(w http.ResponseWriter, r *http.Request, status int, u db.User) error {
	rows, err := s.Intelligences.Rows(r.Context(), []string{u.ID})
	if err != nil {
		return err
	}
	row, ok := rows[u.ID]
	if !ok { // an account without an intelligence: it goes by its username
		row = db.Intelligence{ID: u.ID, Kind: string(dto.IntelligenceKindHuman), DisplayName: u.Username}
	}
	me := toMe(u, row.DisplayName)
	me.Intelligence = convert.IntelligenceRef(row, dto.IntelligenceRoleUser, "")
	if me.DefaultOrgID, err = s.Orgs.DefaultOrg(r.Context(), u.ID); err != nil {
		return err
	}
	httpx.WriteJSON(w, status, me)
	return nil
}
