package dto

import "time"

// Contacts is GET /contacts: everyone the caller can reach, grouped as the contacts page shows
// them (CONT-01): the AIs by organization, the ones they host that no organization lists, the other users
// and the people of their connections. The machines are not part of it (AD-022).
type Contacts struct {
	Me IntelligenceRef `json:"me"`
	// OrgAIs has one group for each organization the caller belongs to, by organization name (empty ones
	// included), with the AIs that are members of it, by name. An AI that belongs to several of them is
	// listed in each (AD-016).
	OrgAIs []OrgAIs `json:"org_ais"`
	// Hosted are the AIs hosted on the caller's computers that no group of OrgAIs lists, by name: they belong
	// to none of the caller's organizations any more. Every other AI the caller hosts is in its groups.
	Hosted []AIContact `json:"hosted"`
	// Users are the other enabled users of the instance, by name.
	Users []IntelligenceRef `json:"users"`
	// People has one group for each connection of the organizations the caller belongs to, by connection
	// name; GET /contacts/people lists the people of one.
	People []PeopleGroup `json:"people"`
}

// OrgAIs is the AIs of one organization of the caller.
type OrgAIs struct {
	Org ContactOrg  `json:"org"`
	AIs []AIContact `json:"ais"`
}

// ContactOrg names an organization in compact form: in the contacts, and as the home of an AI of the
// agents list.
type ContactOrg struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// AIContact is an AI as the contacts page lists it (TREE-03).
type AIContact struct {
	Intelligence IntelligenceRef `json:"intelligence"`
	// Owner is the user who hosts the AI, set when it is another user than the caller.
	Owner *IntelligenceRef `json:"owner,omitempty"`
	// OrgID is the AI's home organization.
	OrgID       string `json:"org_id"`
	Folder      string `json:"folder"`
	FolderLabel string `json:"folder_label"`
	// App is the id of the harness the AI currently runs in.
	App        string     `json:"app"`
	Online     bool       `json:"online"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

// PeopleGroup is the people one connector has seen. They are listed by GET /contacts/people;
// people whose account is linked to a user are not counted: they are an app of that user.
type PeopleGroup struct {
	Connector ConnectorRef `json:"connector"`
	Count     int          `json:"count"`
}

// Paging of GET /contacts/people.
const (
	// DefaultPeoplePage is the page size when limit is absent.
	DefaultPeoplePage = 50
	// MaxPeoplePage is the largest page size.
	MaxPeoplePage = 200
	// MaxPeopleSearch is the longest search text in code points.
	MaxPeopleSearch = 100
)
