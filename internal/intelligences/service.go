package intelligences

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/avatars"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// ErrHandleTaken means another intelligence already uses the handle, compared
// case-insensitively (409 handle_taken, PROF-10).
var ErrHandleTaken = errors.New("handle already taken")

// Role says which subtype row an intelligence shares its id with.
type Role string

// Roles of an intelligence.
const (
	RoleUser   Role = "user"
	RoleAI     Role = "ai"
	RolePerson Role = "person"
)

// MaxBio is the longest bio in code points (PROF-03); display names are cut to
// handles.MaxDisplayName.
const MaxBio = 280

// NewParams describes an intelligence being created together with its user, agent or person
// row.
type NewParams struct {
	// ID is the id of the subtype row: users.id, agents.id or people.id.
	ID string
	// Kind picks human (User, Person) or ai (AI) and the prefix of a default handle.
	Kind handles.Kind
	// DisplayName is cleaned and cut to 64 code points; an empty one falls back to HandleBase
	// and then to the handle.
	DisplayName string
	// HandleBase is the name the handle derives from: a username, an agent name, a folder
	// name or a platform name.
	HandleBase string
	Now        time.Time
}

// Create inserts the intelligence of a new user, AI or person into the caller's
// transaction, so every subtype row has exactly one (PROF-01). Its handle is the first free
// one derived from HandleBase (PROF-02, AID-03); the caller's single-writer transaction makes
// the check-then-insert race-free and the unique index is the last guard.
func Create(ctx context.Context, q *db.Queries, in NewParams) (db.Intelligence, error) {
	var lookupErr error
	handle := handles.Allocate(func(h string) bool {
		n, err := q.CountIntelligenceHandle(ctx, h)
		if err != nil {
			lookupErr = err
			return false // ends the search; the error is returned below
		}
		return n > 0
	}, handles.Default(in.Kind, in.HandleBase))
	if lookupErr != nil {
		return db.Intelligence{}, lookupErr
	}
	display := CleanDisplayName(in.DisplayName)
	if display == "" {
		display = CleanDisplayName(in.HandleBase)
	}
	if display == "" {
		display = handle
	}
	kind := "human"
	if in.Kind == handles.AI {
		kind = "ai"
	}
	now := in.Now.UnixMilli()
	row := db.Intelligence{ID: in.ID, Kind: kind, DisplayName: display, Handle: handle, CreatedAt: now, UpdatedAt: now}
	if err := q.CreateIntelligence(ctx, db.CreateIntelligenceParams{
		ID: row.ID, Kind: row.Kind, DisplayName: row.DisplayName, Handle: row.Handle,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}); err != nil {
		return db.Intelligence{}, err
	}
	return row, nil
}

// CleanDisplayName drops control characters and invalid UTF-8, trims white space and cuts the
// name to 64 code points.
func CleanDisplayName(s string) string {
	return cut(clean(s, false), handles.MaxDisplayName)
}

// clean drops control characters (newlines stay when keepNewlines is set) and invalid UTF-8,
// then trims white space.
func clean(s string, keepNewlines bool) string {
	s = strings.Map(func(r rune) rune {
		if r == utf8.RuneError || (unicode.IsControl(r) && (r != '\n' || !keepNewlines)) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// cut keeps the first n code points of s.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n]))
}

// SetDisplayName writes an intelligence's display name for a path that owns the name without
// going through Update (an account's display name, an agent's rename, a person's platform
// name), so that authors are named by their intelligence everywhere. It reports whether the
// stored name changed. A row without an intelligence is left alone.
func SetDisplayName(ctx context.Context, q *db.Queries, id, name string, now time.Time) (bool, error) {
	cur, err := q.GetIntelligence(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	name = CleanDisplayName(name)
	if name == "" {
		name = cur.Handle
	}
	if name == cur.DisplayName {
		return false, nil
	}
	return true, q.SetIntelligenceDisplayName(ctx, db.SetIntelligenceDisplayNameParams{DisplayName: name, UpdatedAt: now.UnixMilli(), ID: id})
}

// Announce tells live clients that an intelligence changed: IntelligenceChanged and, when its
// name or handle changed (members), MembersChanged for every room it is a member of. Call it
// after the transaction committed. Events are hints that clients can recover from by
// refetching, so a failed lookup of the rooms is not an error.
func Announce(ctx context.Context, bus *events.Bus, q *db.Queries, id string, members bool) {
	if bus == nil {
		return
	}
	bus.Publish(events.IntelligenceChanged{ID: id})
	if !members {
		return
	}
	roomIDs, err := q.ListMemberRoomIDs(ctx, id)
	if err != nil {
		return
	}
	for _, roomID := range roomIDs {
		bus.Publish(events.MembersChanged{RoomID: roomID})
	}
}

// Deps are the collaborators of the Service.
type Deps struct {
	Store  *store.Store
	Access *access.Authorizer
	Bus    *events.Bus
	// Blobs stores the avatars.
	Blobs *attachments.Store
	// Online tells whether an AI has a live session; nil counts every AI as offline.
	Online func(agentID string) bool
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// Service reads and edits intelligence profiles and decides who can see and edit them.
type Service struct {
	store *store.Store
	az    *access.Authorizer
	bus   *events.Bus
	blobs *attachments.Store
	// online tells whether an AI has a live session; may be nil.
	online func(agentID string) bool
	now    func() time.Time
}

// New returns a Service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{store: d.Store, az: d.Access, bus: d.Bus, blobs: d.Blobs, online: d.Online, now: d.Now}
}

// Profile is an intelligence as one caller reads it.
type Profile struct {
	db.Intelligence
	Role Role
	// CanEdit tells whether the caller may change the profile (PROF-03, PROF-04, PROF-05).
	CanEdit bool
	// Platform is the connector kind of a person (whatsapp, telegram, …); empty otherwise.
	Platform string
	// Agent is set for an AI.
	Agent *AgentInfo
	// Person is set for a person.
	Person *PersonInfo
	// Accounts are the chat accounts a user verified (PEOP-02) that the caller may see: the
	// user's own, and those linked through a connector the caller owns or may use.
	Accounts []LinkedAccount
	// Operated are the connectors the AI operates (CID-01) that the caller owns or may
	// use: their platform accounts are apps of the operator (ACC-07).
	Operated []db.Connector
}

// AgentInfo is what a profile shows about an AI.
type AgentInfo struct {
	db.Agent
	// Owner is the owner's own intelligence.
	Owner db.Intelligence
	// MachineLabel is the owner's label for the AI's machine, else its hostname; Home is the
	// machine's canonical home folder, empty when unknown.
	MachineLabel string
	Home         string
	// Level is the caller's level on the AI: manage for the owner, use for a grantee, none for
	// a caller who sees it through a conversation they share.
	Level access.Level
}

// profile builds the profile of d as the caller reads it.
func (s *Service) profile(ctx context.Context, p authn.Principal, d db.GetIntelligenceWithRoleRow, canEdit bool) (Profile, error) {
	out := Profile{Intelligence: d.Intelligence, Role: Role(d.Role), CanEdit: canEdit}
	q := s.store.R()
	switch out.Role {
	case RoleUser:
		accounts, err := s.linkedAccounts(ctx, p.UserID, d.Intelligence.ID)
		if err != nil {
			return Profile{}, err
		}
		out.Accounts = accounts
	case RoleAI:
		operated, err := s.operatedConnectors(ctx, p.UserID, d.Intelligence.ID)
		if err != nil {
			return Profile{}, err
		}
		out.Operated = operated
		a, err := q.GetAgent(ctx, d.Intelligence.ID)
		if err != nil {
			return Profile{}, err
		}
		owner, err := q.GetIntelligence(ctx, a.OwnerID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Profile{}, err
		}
		m, err := q.GetMachine(ctx, db.GetMachineParams{OwnerID: a.OwnerID, Hostname: a.Machine})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Profile{}, err
		}
		label := m.Label
		if label == "" {
			label = a.Machine
		}
		level, err := s.az.AgentLevel(ctx, p.UserID, a.ID)
		if err != nil {
			return Profile{}, err
		}
		out.Agent = &AgentInfo{Agent: a, Owner: owner, MachineLabel: label, Home: m.Home, Level: level}
	case RolePerson:
		info, err := s.personInfo(ctx, p.UserID, d.Intelligence.ID)
		if err != nil {
			return Profile{}, err
		}
		out.Person, out.Platform = info, info.Connector.Kind
	}
	return out, nil
}

// Rows returns the intelligences with the given ids by id; unknown ids are left out. It does
// not authorize: callers are server components that render ids they took from data the caller
// can already see (the account's own, the users directory).
func (s *Service) Rows(ctx context.Context, ids []string) (map[string]db.Intelligence, error) {
	out := make(map[string]db.Intelligence, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.store.R().ListIntelligencesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// Get returns the profile of an intelligence the caller can see; for any other id it answers
// access.ErrNotFound.
func (s *Service) Get(ctx context.Context, p authn.Principal, id string) (Profile, error) {
	d, err := s.detail(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	see, edit, err := s.rights(ctx, p, d)
	if err != nil {
		return Profile{}, err
	}
	if !see {
		return Profile{}, access.ErrNotFound
	}
	return s.profile(ctx, p, d, edit)
}

// CanSee reports whether the caller can see the intelligence: itself; any user of the
// instance for user intelligences; an AI when the caller owns it, may use it, or shares a
// viewable conversation where it is a member or an author; a person when the caller owns or
// may use its connector. An agent caller sees what its owner sees, and itself. Admins get no
// implicit access (AD-005).
func (s *Service) CanSee(ctx context.Context, p authn.Principal, id string) (bool, error) {
	d, err := s.detail(ctx, id)
	if errors.Is(err, access.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	see, _, err := s.rights(ctx, p, d)
	return see, err
}

// CanEdit reports whether the caller can change the intelligence's profile: a user edits
// their own; an AI is edited by its owner (web) or by itself (an agent caller acting as that
// AI); a person by the users who own or may use its connector. Grantees of an AI do not
// edit it.
func (s *Service) CanEdit(ctx context.Context, p authn.Principal, id string) (bool, error) {
	d, err := s.detail(ctx, id)
	if errors.Is(err, access.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, edit, err := s.rights(ctx, p, d)
	return edit, err
}

func (s *Service) detail(ctx context.Context, id string) (db.GetIntelligenceWithRoleRow, error) {
	d, err := s.store.R().GetIntelligenceWithRole(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.GetIntelligenceWithRoleRow{}, access.ErrNotFound
	}
	return d, err
}

// rights resolves what the caller may do with the intelligence. Disabled and unknown users
// have no access at all (AUTH-10).
func (s *Service) rights(ctx context.Context, p authn.Principal, d db.GetIntelligenceWithRoleRow) (see, edit bool, err error) {
	if p.UserID == "" || (p.Kind != authn.KindUser && p.Kind != authn.KindAgent) {
		return false, false, nil
	}
	u, err := s.store.R().GetUser(ctx, p.UserID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && u.DisabledAt.Valid) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	id := d.Intelligence.ID
	if (p.Kind == authn.KindUser && p.UserID == id) || (p.Kind == authn.KindAgent && p.AgentID == id) {
		return true, true, nil
	}
	user := p.Kind == authn.KindUser // an agent edits itself only, handled above
	switch Role(d.Role) {
	case RoleUser:
		return true, false, nil
	case RoleAI:
		level, err := s.az.AgentLevel(ctx, p.UserID, id)
		if err != nil {
			return false, false, err
		}
		if access.AtLeast(level, dto.LevelUse) {
			return true, user && level == dto.LevelManage, nil
		}
		shared, err := s.sharesConversation(ctx, p.UserID, id)
		return shared, false, err
	case RolePerson:
		// The users of the connector see and edit its people (PEOP-04); a user who can view a
		// conversation where the person was seen or wrote reads the profile only.
		ok, err := s.az.CanUseConnector(ctx, p.UserID, d.PersonConnectorID)
		if err != nil || ok {
			return ok, ok && user, err
		}
		shared, err := s.sharesPersonConversation(ctx, p.UserID, id)
		return shared, false, err
	}
	return false, false, nil
}

// sharesConversation reports whether the user can view a room where the intelligence is a
// member or wrote a message.
func (s *Service) sharesConversation(ctx context.Context, userID, iid string) (bool, error) {
	roomIDs, err := s.store.R().ListIntelligenceRoomIDs(ctx, iid)
	if err != nil {
		return false, err
	}
	for _, roomID := range roomIDs {
		l, err := s.az.UserRoomLevel(ctx, userID, roomID)
		if err != nil {
			return false, err
		}
		if access.AtLeast(l, dto.LevelView) {
			return true, nil
		}
	}
	return false, nil
}

// editable resolves the intelligence and requires the caller to edit it: access.ErrNotFound
// without view, access.ErrForbidden with view but no edit.
func (s *Service) editable(ctx context.Context, p authn.Principal, id string) (db.GetIntelligenceWithRoleRow, error) {
	d, err := s.detail(ctx, id)
	if err != nil {
		return d, err
	}
	see, edit, err := s.rights(ctx, p, d)
	switch {
	case err != nil:
		return d, err
	case !see:
		return d, access.ErrNotFound
	case !edit:
		return d, access.ErrForbidden
	}
	return d, nil
}

// ProfileUpdate holds the changes to a profile; nil fields are unchanged.
type ProfileUpdate struct {
	DisplayName *string // 1-64 code points after trimming
	Handle      *string // 1-40 of a-z0-9-, a letter first, lower-cased, instance-unique
	Bio         *string // 0-280 code points
}

// valid is a ProfileUpdate after validation.
type valid struct {
	display, handle, bio *string
}

func (in ProfileUpdate) validate() (valid, error) {
	var v valid
	check := validation.Check{}
	if in.DisplayName != nil {
		name := clean(*in.DisplayName, false)
		if n := utf8.RuneCountInString(name); n == 0 || n > handles.MaxDisplayName {
			check.Add("display_name", "must have 1 to 64 characters")
		}
		v.display = &name
	}
	if in.Handle != nil {
		h, err := handles.Validate(strings.TrimSpace(*in.Handle))
		if err != nil {
			check.Add("handle", err.Error())
		}
		v.handle = &h
	}
	if in.Bio != nil {
		bio := clean(*in.Bio, true)
		if utf8.RuneCountInString(bio) > MaxBio {
			check.Add("bio", "must have at most 280 characters")
		}
		v.bio = &bio
	}
	return v, check.Err()
}

// Update changes the profile of an intelligence the caller can edit (PROF-03, PROF-04,
// PROF-05). A caller who cannot see it gets access.ErrNotFound, one who can see but not edit
// access.ErrForbidden. An invalid field is a validation error and nothing is applied; a
// handle in use is ErrHandleTaken (PROF-10). A change announces IntelligenceChanged, and
// MembersChanged for the rooms the intelligence is a member of when its name or handle changed.
func (s *Service) Update(ctx context.Context, p authn.Principal, id string, in ProfileUpdate) (Profile, error) {
	d, err := s.editable(ctx, p, id)
	if err != nil {
		return Profile{}, err
	}
	v, err := in.validate()
	if err != nil {
		return Profile{}, err
	}
	var changed, names bool
	err = s.store.WithTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetIntelligence(ctx, id)
		if err != nil {
			return err
		}
		next := cur
		if v.display != nil {
			next.DisplayName = *v.display
		}
		if v.handle != nil {
			next.Handle = *v.handle
		}
		if v.bio != nil {
			next.Bio = *v.bio
		}
		names = next.DisplayName != cur.DisplayName || next.Handle != cur.Handle
		changed = names || next.Bio != cur.Bio
		if !changed {
			return nil
		}
		if next.Handle != cur.Handle {
			other, err := q.GetIntelligenceByHandle(ctx, next.Handle)
			if err == nil && other.ID != id {
				return ErrHandleTaken
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if err := q.UpdateIntelligenceProfile(ctx, db.UpdateIntelligenceProfileParams{
			DisplayName: next.DisplayName, Handle: next.Handle, Bio: next.Bio, UpdatedAt: s.now().UnixMilli(), ID: id,
		}); err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed: intelligences.handle") {
				return ErrHandleTaken
			}
			return err
		}
		if next.DisplayName == cur.DisplayName || Role(d.Role) != RolePerson {
			return nil
		}
		// PEOP-05, PROF-11: a name chosen here is not replaced by platform name changes.
		return q.SetPersonNameLocked(ctx, db.SetPersonNameLockedParams{NameLocked: true, ID: id})
	})
	if err != nil {
		return Profile{}, err
	}
	if changed {
		Announce(ctx, s.bus, s.store.R(), id, names)
	}
	d, err = s.detail(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	return s.profile(ctx, p, d, true)
}

// SetAvatar replaces the avatar of an intelligence the caller can edit with the image read from
// r: PNG, JPEG or WebP up to 2 MiB, stored centre-cropped as a 256x256 PNG (PROF-06, PROF-07).
// An image that is not acceptable is avatars.ErrInvalid and the old avatar stays.
func (s *Service) SetAvatar(ctx context.Context, p authn.Principal, id string, r io.Reader) (Profile, error) {
	if _, err := s.editable(ctx, p, id); err != nil {
		return Profile{}, err
	}
	img, err := avatars.Process(r)
	if err != nil {
		return Profile{}, err
	}
	sha, _, err := s.blobs.Put(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		return Profile{}, err
	}
	return s.storeAvatar(ctx, p, id, sha)
}

// ClearAvatar removes the avatar of an intelligence the caller can edit; the UI shows initials
// again (PROF-08). Removing a missing avatar is not an error.
func (s *Service) ClearAvatar(ctx context.Context, p authn.Principal, id string) error {
	if _, err := s.editable(ctx, p, id); err != nil {
		return err
	}
	_, err := s.storeAvatar(ctx, p, id, "")
	return err
}

// storeAvatar points the intelligence at the avatar blob sha ("" for none) and announces the
// change. The replaced blob stays until the retention sweep finds nothing referencing it.
func (s *Service) storeAvatar(ctx context.Context, p authn.Principal, id, sha string) (Profile, error) {
	var changed bool
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetIntelligence(ctx, id)
		if err != nil {
			return err
		}
		if cur.AvatarSha256 == sha {
			return nil
		}
		changed = true
		return q.SetIntelligenceAvatar(ctx, db.SetIntelligenceAvatarParams{AvatarSha256: sha, UpdatedAt: s.now().UnixMilli(), ID: id})
	})
	if err != nil {
		return Profile{}, err
	}
	if changed {
		Announce(ctx, s.bus, s.store.R(), id, true) // member lists show avatars
	}
	d, err := s.detail(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	return s.profile(ctx, p, d, true)
}

// Avatar opens the stored avatar PNG of an intelligence the caller can see, with its content
// hash. Without view, without an avatar or with a blob that is gone it is access.ErrNotFound.
func (s *Service) Avatar(ctx context.Context, p authn.Principal, id string) (*os.File, string, error) {
	d, err := s.detail(ctx, id)
	if err != nil {
		return nil, "", err
	}
	see, _, err := s.rights(ctx, p, d)
	if err != nil {
		return nil, "", err
	}
	sha := d.Intelligence.AvatarSha256
	if !see || sha == "" {
		return nil, "", access.ErrNotFound
	}
	f, err := s.blobs.Open(sha)
	if errors.Is(err, attachments.ErrNotFound) {
		return nil, "", access.ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	return f, sha, nil
}
