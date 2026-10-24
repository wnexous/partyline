package intelligences

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// AIRow is an AI as the contacts list it: its profile, where it lives and whether it is
// online (TREE-03). The machine it lives on is not shown (AD-022): only its folder.
type AIRow struct {
	Intelligence db.Intelligence
	// Owner is the user who hosts the AI; set when it is another user than the one listing it.
	Owner *db.Intelligence
	// OrgID is the AI's home organization.
	OrgID       string
	Folder      string
	FolderLabel string
	// App is the id of the harness the AI currently runs in.
	App        string
	Online     bool
	LastSeenAt sql.NullInt64
}

// PeopleGroup is the people one connector has seen, leaving out the accounts verified as a user.
type PeopleGroup struct {
	Connector db.Connector
	Count     int
}

// OrgGroup is the AIs of one organization a user belongs to (AD-016).
type OrgGroup struct {
	Organization db.Organization
	AIs          []AIRow
}

// Contacts is everyone a user can reach (CONT-01, AD-016, AD-022): the AIs of the organizations they belong to
// by organization, the AIs they host that no such group lists, the other enabled users and the people of their
// connections. The machines are not part of it.
type Contacts struct {
	Me db.Intelligence
	// OrgAIs has one group for each organization the user belongs to, by name, empty ones included, with the
	// AIs that are members of it, by name. An AI that belongs to several of them is listed in each.
	OrgAIs []OrgGroup
	// Hosted are the AIs hosted on the user's computers that no group lists, by name: the user belongs to none
	// of the organizations they belong to, for instance after leaving them.
	Hosted []AIRow
	// Users are the other enabled users, by name.
	Users []db.Intelligence
	// People has one group for each connection of the organizations the user belongs to.
	People []PeopleGroup
}

// aiRow describes an AI; home is its machine's home folder as the machine's owner reported it.
func (s *Service) aiRow(a db.Agent, i db.Intelligence, owner *db.Intelligence, home string) AIRow {
	if i.ID == "" { // an AI without an intelligence (not migrated): its row's name
		i = db.Intelligence{ID: a.ID, Kind: "ai", DisplayName: a.Name, Handle: a.Name}
	}
	return AIRow{
		Intelligence: i, Owner: owner, OrgID: a.OrgID.String, Folder: a.Folder, FolderLabel: FolderLabel(home, a.Folder),
		App: a.Harness, Online: s.online != nil && s.online(a.ID), LastSeenAt: a.LastSeenAt,
	}
}

// byName sorts AI rows by display name, case-insensitively, then by id.
func byName(a, b AIRow) int {
	if c := strings.Compare(strings.ToLower(a.Intelligence.DisplayName), strings.ToLower(b.Intelligence.DisplayName)); c != 0 {
		return c
	}
	return strings.Compare(a.Intelligence.ID, b.Intelligence.ID)
}

// Contacts lists everyone the user can reach (CONT-01): the AIs of each organization they belong to, the AIs
// they host that none of those groups lists, the other enabled users and, for each connection of their
// organizations, the people it has seen.
func (s *Service) Contacts(ctx context.Context, userID string) (Contacts, error) {
	q := s.store.R()
	out := Contacts{OrgAIs: []OrgGroup{}, Hosted: []AIRow{}, Users: []db.Intelligence{}, People: []PeopleGroup{}}
	var err error
	if out.Me, err = q.GetIntelligence(ctx, userID); err != nil {
		return Contacts{}, err
	}
	memberships, err := q.ListOrgsOfMember(ctx, userID)
	if err != nil {
		return Contacts{}, err
	}
	inOrgs, err := q.ListOrgAIs(ctx, userID)
	if err != nil {
		return Contacts{}, err
	}
	hosted, err := q.ListAgentsByOwner(ctx, userID)
	if err != nil {
		return Contacts{}, err
	}

	agents := make([]db.Agent, 0, len(inOrgs)+len(hosted))
	ids := []string{}
	for _, r := range inOrgs {
		agents, ids = append(agents, r.Agent), append(ids, r.Agent.ID, r.Agent.OwnerID)
	}
	for _, a := range hosted {
		agents, ids = append(agents, a), append(ids, a.ID)
	}
	profiles, err := s.Rows(ctx, ids)
	if err != nil {
		return Contacts{}, err
	}
	homes, err := s.homeFolders(ctx, agents)
	if err != nil {
		return Contacts{}, err
	}
	row := func(a db.Agent) AIRow {
		var host *db.Intelligence // the host is named when it is someone else
		if h, ok := profiles[a.OwnerID]; ok && a.OwnerID != userID {
			host = &h
		}
		return s.aiRow(a, profiles[a.ID], host, homes[[2]string{a.OwnerID, a.Machine}])
	}

	groups := make(map[string][]AIRow, len(memberships))
	listed := map[string]bool{}
	for _, r := range inOrgs {
		groups[r.OrgID] = append(groups[r.OrgID], row(r.Agent))
		listed[r.Agent.ID] = true
	}
	for _, m := range memberships {
		ais := groups[m.Organization.ID]
		if ais == nil {
			ais = []AIRow{}
		}
		slices.SortStableFunc(ais, byName)
		out.OrgAIs = append(out.OrgAIs, OrgGroup{Organization: m.Organization, AIs: ais})
	}
	for _, a := range hosted {
		if !listed[a.ID] {
			out.Hosted = append(out.Hosted, row(a))
		}
	}
	slices.SortStableFunc(out.Hosted, byName)

	users, err := q.ListContactUsers(ctx, userID)
	if err != nil {
		return Contacts{}, err
	}
	for _, u := range users {
		out.Users = append(out.Users, u.Intelligence)
	}

	conns, err := q.ListConnectorsOfMember(ctx, userID) // the connections of my organizations (AD-016)
	if err != nil {
		return Contacts{}, err
	}
	if len(conns) == 0 {
		return out, nil
	}
	connIDs := make([]string, len(conns))
	for i, c := range conns {
		connIDs[i] = c.Connector.ID
	}
	counts, err := q.CountConnectorPeople(ctx, connIDs)
	if err != nil {
		return Contacts{}, err
	}
	count := make(map[string]int, len(counts))
	for _, c := range counts {
		count[c.ConnectorID] = int(c.People)
	}
	for _, c := range conns {
		out.People = append(out.People, PeopleGroup{Connector: c.Connector, Count: count[c.Connector.ID]})
	}
	return out, nil
}

// homeFolders returns, for each (host, machine) the agents live on, the host's home folder on it, which
// lets a folder read "~/x" in the contacts.
func (s *Service) homeFolders(ctx context.Context, agents []db.Agent) (map[[2]string]string, error) {
	homes := map[[2]string]string{}
	for _, a := range agents {
		key := [2]string{a.OwnerID, a.Machine}
		if _, ok := homes[key]; ok {
			continue
		}
		m, err := s.store.R().GetMachine(ctx, db.GetMachineParams{OwnerID: a.OwnerID, Hostname: a.Machine})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		homes[key] = m.Home
	}
	return homes, nil
}

// PeopleQuery selects a page of the people of one connector.
type PeopleQuery struct {
	ConnectorID string
	// Search matches the name, the handle or the account id, ignoring ASCII case; blank matches all.
	Search string
	// After is the id of the last person of the previous page.
	After string
	// Limit is the page size, 1 to dto.MaxPeoplePage.
	Limit int
}

// PeoplePage is one page of a connector's people, in name order.
type PeoplePage struct {
	// Platform is the connector's kind.
	Platform string
	People   []db.Intelligence
	More     bool
}

// People lists the people a connection has seen, for a member of its organization (AD-016): in name order
// (case-insensitively for ASCII), without the accounts verified as a user, matching the search,
// from the person after the cursor. A connection the caller does not see is access.ErrNotFound; an
// invalid request is a validation error with nothing read.
func (s *Service) People(ctx context.Context, p authn.Principal, in PeopleQuery) (PeoplePage, error) {
	check := validation.Check{}
	if in.ConnectorID == "" {
		check.Add("connector_id", "is required")
	}
	limit := in.Limit
	if limit < 1 || limit > dto.MaxPeoplePage {
		check.Add("limit", "must be between 1 and 200")
	}
	search := strings.TrimSpace(in.Search)
	if utf8.RuneCountInString(search) > dto.MaxPeopleSearch {
		check.Add("q", "must have at most 100 characters")
	}
	if err := check.Err(); err != nil {
		return PeoplePage{}, err
	}
	ok, err := s.az.CanSeeConnection(ctx, p, in.ConnectorID)
	if err != nil {
		return PeoplePage{}, err
	}
	if !ok {
		return PeoplePage{}, access.ErrNotFound
	}
	q := s.store.R()
	conn, err := q.GetConnector(ctx, in.ConnectorID)
	if err != nil {
		return PeoplePage{}, err
	}
	params := db.ListConnectorContactsParams{ConnectorID: conn.ID, Q: asciiLower(search), RowLimit: int64(limit) + 1}
	if in.After != "" {
		name, err := q.GetConnectorContactSortName(ctx, db.GetConnectorContactSortNameParams{ConnectorID: conn.ID, ID: in.After})
		if errors.Is(err, sql.ErrNoRows) {
			check.Add("after", "must be the id of a person of the connector")
			return PeoplePage{}, check.Err()
		}
		if err != nil {
			return PeoplePage{}, err
		}
		params.AfterID, params.AfterName = in.After, name
	}
	rows, err := q.ListConnectorContacts(ctx, params)
	if err != nil {
		return PeoplePage{}, err
	}
	page := PeoplePage{Platform: conn.Kind, People: make([]db.Intelligence, 0, min(len(rows), limit))}
	for i, r := range rows {
		if i == limit {
			page.More = true
			break
		}
		page.People = append(page.People, r.Intelligence)
	}
	return page, nil
}

// asciiLower lower-cases the ASCII letters only, as SQLite's lower() does, so a search matches
// exactly where the database's own case folding does.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}
