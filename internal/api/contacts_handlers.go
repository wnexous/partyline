package api

import (
	"net/http"
	"strconv"

	"github.com/wnexous/partyline/internal/api/convert"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/validation"
)

// contactRoutes list the intelligences a user can reach (CONT-01). Machines are internal (AD-022): no
// route names or labels one.
func (s *server) contactRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/v1/contacts", authed(s.getContacts))
	s.handle(mux, "GET /api/v1/contacts/people", authed(s.listContactPeople))
}

// getContacts lists the AIs of the caller's organizations by organization, the AIs they host that none of
// those lists, the other users and the people groups of their connections.
func (s *server) getContacts(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	c, err := s.Intelligences.Contacts(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	out := dto.Contacts{
		Me:     convert.IntelligenceRef(c.Me, dto.IntelligenceRoleUser, ""),
		OrgAIs: make([]dto.OrgAIs, len(c.OrgAIs)),
		Hosted: toAIContacts(c.Hosted),
		Users:  make([]dto.IntelligenceRef, len(c.Users)),
		People: make([]dto.PeopleGroup, len(c.People)),
	}
	for i, g := range c.OrgAIs {
		out.OrgAIs[i] = dto.OrgAIs{
			Org: dto.ContactOrg{ID: g.Organization.ID, Name: g.Organization.Name, Slug: g.Organization.Slug},
			AIs: toAIContacts(g.AIs),
		}
	}
	for i, u := range c.Users {
		out.Users[i] = convert.IntelligenceRef(u, dto.IntelligenceRoleUser, "")
	}
	for i, g := range c.People {
		out.People[i] = dto.PeopleGroup{
			Connector: dto.ConnectorRef{ID: g.Connector.ID, Kind: g.Connector.Kind, Name: g.Connector.Name}, Count: g.Count,
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// listContactPeople pages through the people of one connection of an organization the caller belongs to:
// ?connector_id=&q=&after=<id of the last person>&limit= (50 by default, at most 200).
func (s *server) listContactPeople(w http.ResponseWriter, r *http.Request, p authn.Principal) error {
	q := r.URL.Query()
	in := intelligences.PeopleQuery{ConnectorID: q.Get("connector_id"), Search: q.Get("q"), After: q.Get("after"), Limit: dto.DefaultPeoplePage}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			check := validation.Check{}
			check.Add("limit", "must be a number")
			return check.Err()
		}
		in.Limit = n
	}
	page, err := s.Intelligences.People(r.Context(), p, in)
	if err != nil {
		return err
	}
	out := dto.Page[dto.IntelligenceRef]{Items: make([]dto.IntelligenceRef, len(page.People)), HasMore: page.More}
	for i, person := range page.People {
		out.Items[i] = convert.IntelligenceRef(person, dto.IntelligenceRolePerson, page.Platform)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func toAIContact(row intelligences.AIRow) dto.AIContact {
	out := dto.AIContact{
		Intelligence: convert.IntelligenceRef(row.Intelligence, dto.IntelligenceRoleAI, ""),
		OrgID:        row.OrgID, Folder: row.Folder, FolderLabel: row.FolderLabel,
		App: row.App, Online: row.Online, LastSeenAt: convert.OptTime(row.LastSeenAt),
	}
	if row.Owner != nil {
		owner := convert.IntelligenceRef(*row.Owner, dto.IntelligenceRoleUser, "")
		out.Owner = &owner
	}
	return out
}

func toAIContacts(rows []intelligences.AIRow) []dto.AIContact {
	out := make([]dto.AIContact, len(rows))
	for i, row := range rows {
		out[i] = toAIContact(row)
	}
	return out
}
