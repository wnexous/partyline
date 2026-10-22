package dto

import "time"

// Contacts is GET /contacts: everyone the caller can reach, grouped as the contacts page shows
// them (CONT-01).
type Contacts struct {
	Me IntelligenceRef `json:"me"`
	// Machines are the caller's own AIs that live in a folder, per machine and as a folder tree
	// (TREE-01..03): machines with an online AI first, then by label.
	Machines []MachineTree `json:"machines"`
	// Unlinked are the caller's AIs without a folder, the identities v0.3 left (TREE-06), the
	// most recently seen first.
	Unlinked []AIContact `json:"unlinked"`
	// Shared are the AIs of other users that the caller may use, by name.
	Shared []AIContact `json:"shared"`
	// Users are the other enabled users of the instance, by name.
	Users []IntelligenceRef `json:"users"`
	// People has one group for each connector the caller owns or may use, by connector name;
	// GET /contacts/people lists the people of one.
	People []PeopleGroup `json:"people"`
}

// AgentContactsTree is GET /agent/contacts/tree, the data of `partyline agents` (TREE-07): the
// token owner's own AIs.
type AgentContactsTree struct {
	Machines []MachineTree `json:"machines"`
	Unlinked []AIContact   `json:"unlinked"`
}

// MachineTree is one machine with the AIs that live on it (TREE-01).
type MachineTree struct {
	Hostname string `json:"hostname"`
	// Label is the owner's name for the machine (TREE-04), else the hostname.
	Label string `json:"label"`
	// Online tells whether any AI of the machine is online.
	Online bool       `json:"online"`
	Nodes  []TreeNode `json:"nodes"`
}

// TreeNode is one AI in a machine's folder tree. An AI whose folder is inside another AI's
// folder is that AI's child; folders between two AIs that hold no AI collapse into Path
// (TREE-02). Render "Path › Name" when Path is not empty, else Name.
type TreeNode struct {
	AI AIContact `json:"ai"`
	// Path is the collapsed chain of folders before the node ("c/d"), empty when there is none;
	// a top-level node outside the home folder starts with "/".
	Path string `json:"path"`
	// Name is the folder's own label: its last element, "~" for the home folder, "/" for the root.
	Name     string     `json:"name"`
	Children []TreeNode `json:"children"`
}

// AIContact is an AI as the contacts page and `partyline agents` list it (TREE-03).
type AIContact struct {
	Intelligence IntelligenceRef `json:"intelligence"`
	// Owner is set for the AIs of other users (Contacts.Shared).
	Owner       *IntelligenceRef `json:"owner,omitempty"`
	Machine     string           `json:"machine"`
	Folder      string           `json:"folder"`
	FolderLabel string           `json:"folder_label"`
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

// Machine is a machine of the caller after its label changed.
type Machine struct {
	Hostname string `json:"hostname"`
	// Label is the owner's name for the machine; empty shows the hostname (TREE-04).
	Label string `json:"label"`
}

// SetMachineLabelRequest is the body of PUT /machines/{hostname}: a label of 1 to 64 characters,
// or an empty one to show the hostname again (TREE-04).
type SetMachineLabelRequest struct {
	Label string `json:"label"`
}
