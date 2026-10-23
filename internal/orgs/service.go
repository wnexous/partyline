package orgs

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/attachments"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/avatars"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// Limits of organization fields.
const (
	// MaxNameLength is the longest organization name in code points (ORG-01).
	MaxNameLength = 100
	// RootPositionName is the position the creator of an organization holds (ORG-01).
	RootPositionName = "Admin"
)

// Errors of the organization service. The API maps each to one status code.
var (
	// ErrNotEmpty means the organization still owns AIs, connections or groups (409
	// organization_not_empty, ORG-06). The error is a *NotEmptyError that counts them.
	ErrNotEmpty = errors.New("the organization still owns AIs, connections or groups")
	// ErrSlugTaken means another organization already has the address (409 conflict).
	ErrSlugTaken = errors.New("slug already taken")
)

// NotEmptyError is the refusal to delete an organization that owns something: how many AIs (those
// whose home it is), connections and groups remain. It matches ErrNotEmpty.
type NotEmptyError struct {
	Agents, Connectors, Groups int64
}

func (e *NotEmptyError) Error() string {
	return fmt.Sprintf("the organization still owns %d AIs, %d connections and %d groups", e.Agents, e.Connectors, e.Groups)
}

// Is makes errors.Is(err, ErrNotEmpty) true.
func (e *NotEmptyError) Is(target error) bool { return target == ErrNotEmpty }

// Deps are the collaborators of the Service.
type Deps struct {
	Store *store.Store
	// Authz answers the membership questions; defaults to NewAuthz(Store).
	Authz *Authz
	// Bus receives OrgChanged after every committed change; optional.
	Bus *events.Bus
	// Audit records the changes; defaults to audit.New(Store, Now).
	Audit *audit.Log
	// Blobs stores organization avatars; without it SetAvatar fails.
	Blobs *attachments.Store
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// Service is the organization use cases: organizations, their org chart, members and invitations.
// Every method takes the identity it acts as (a user, or an AI that acts as itself) and authorizes it
// through Authz.
type Service struct {
	store *store.Store
	z     *Authz
	bus   *events.Bus
	audit *audit.Log
	blobs *attachments.Store
	now   func() time.Time
}

// NewService returns a Service.
func NewService(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Authz == nil {
		d.Authz = NewAuthz(d.Store)
	}
	if d.Audit == nil {
		d.Audit = audit.New(d.Store, d.Now)
	}
	return &Service{store: d.Store, z: d.Authz, bus: d.Bus, audit: d.Audit, blobs: d.Blobs, now: d.Now}
}

// Authz returns the authorization facade the service asks.
func (s *Service) Authz() *Authz { return s.z }

// Membership is an organization with the place of one identity in it: its permission and position.
type Membership struct {
	db.Organization
	Permission Permission
	// PositionID is the identity's position, "" for none.
	PositionID string
}

// changed announces that something about the organization changed; call it after the commit.
func (s *Service) changed(orgID string) {
	if s.bus != nil {
		s.bus.Publish(events.OrgChanged{OrgID: orgID})
	}
}

// record appends an audit entry about the organization inside the caller's transaction.
func (s *Service) record(ctx context.Context, q *db.Queries, actor, action, orgID string, detail map[string]any) error {
	return s.audit.Record(ctx, q, audit.Entry{
		ActorID: actor, Action: action, TargetKind: audit.TargetOrganization, TargetID: orgID, Detail: detail,
	})
}

// cleanName drops control characters and invalid UTF-8 and trims white space.
func cleanName(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == utf8.RuneError || unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
}

// validName cleans an organization name and checks its length in code points (ORG-01).
func validName(name string) (string, error) {
	name = cleanName(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxNameLength {
		return "", &validation.Error{Fields: map[string]string{"name": "must have 1 to 100 characters"}}
	}
	return name, nil
}

// orgParams describe an organization being created.
type orgParams struct {
	// Creator becomes the admin of the organization, holding the root position.
	Creator string
	Name    string
	// SlugBase is what the address derives from: the name, or a username for a personal one.
	SlugBase string
	Now      time.Time
}

// createOrg inserts an organization with its root position "Admin" and its creator as the admin
// holding it, in the caller's transaction (ORG-01). The address is the first free slug derived from
// SlugBase.
func createOrg(ctx context.Context, q *db.Queries, p orgParams) (Membership, error) {
	var lookupErr error
	slug := handles.AllocateSlug(func(s string) bool {
		n, err := q.CountOrganizationSlug(ctx, s)
		if err != nil {
			lookupErr = err
			return false // ends the search; the error is returned below
		}
		return n > 0
	}, p.SlugBase)
	if lookupErr != nil {
		return Membership{}, lookupErr
	}
	now := p.Now.UnixMilli()
	org := db.Organization{ID: store.NewID(), Name: p.Name, Slug: slug, CreatedBy: p.Creator, CreatedAt: now, UpdatedAt: now}
	positionID := store.NewID()
	if err := q.CreateOrganization(ctx, db.CreateOrganizationParams{
		ID: org.ID, Name: org.Name, Slug: org.Slug, CreatedBy: org.CreatedBy, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return Membership{}, err
	}
	if err := q.CreatePosition(ctx, db.CreatePositionParams{ID: positionID, OrgID: org.ID, Name: RootPositionName, CreatedAt: now}); err != nil {
		return Membership{}, err
	}
	if err := q.CreateOrgMember(ctx, db.CreateOrgMemberParams{
		OrgID: org.ID, Iid: p.Creator, Permission: string(PermissionAdmin), PositionID: nullable(positionID), JoinedAt: now,
	}); err != nil {
		return Membership{}, err
	}
	return Membership{Organization: org, Permission: PermissionAdmin, PositionID: positionID}, nil
}

// activeUser reports whether iid is an enabled account: only users create organizations.
func activeUser(ctx context.Context, q *db.Queries, iid string) (bool, error) {
	if iid == "" {
		return false, nil
	}
	u, err := q.GetUser(ctx, iid)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !u.DisabledAt.Valid, nil
}

// Create makes a new organization named name (1-100 characters) with an address derived from it,
// and its creator - which must be an enabled user - its admin holding the root position "Admin"
// (ORG-01). A name that is too short or too long is a validation error on "name".
func (s *Service) Create(ctx context.Context, creatorIID, name string) (Membership, error) {
	if ok, err := activeUser(ctx, s.store.R(), creatorIID); err != nil || !ok {
		if err == nil {
			err = ErrForbidden
		}
		return Membership{}, err
	}
	name, err := validName(name)
	if err != nil {
		return Membership{}, err
	}
	var out Membership
	err = s.store.WithTx(ctx, func(q *db.Queries) error {
		var err error
		out, err = createOrg(ctx, q, orgParams{Creator: creatorIID, Name: name, SlugBase: name, Now: s.now()})
		if err != nil {
			return err
		}
		return s.record(ctx, q, creatorIID, audit.ActionOrgCreate, out.ID, map[string]any{"name": out.Name, "slug": out.Slug})
	})
	if err != nil {
		return Membership{}, err
	}
	s.changed(out.ID)
	return out, nil
}

// Resolve finds an organization by id or by slug (what --org takes): the id wins, the slug ignores
// case. An unknown reference is ErrNotFound; it says nothing about who may see the organization.
func (s *Service) Resolve(ctx context.Context, ref string) (db.Organization, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Organization{}, ErrNotFound
	}
	q := s.store.R()
	o, err := q.GetOrganization(ctx, ref)
	if errors.Is(err, sql.ErrNoRows) {
		o, err = q.GetOrganizationBySlug(ctx, ref)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return db.Organization{}, ErrNotFound
	}
	return o, err
}

func membership(o db.Organization, permission, position string) Membership {
	return Membership{Organization: o, Permission: Permission(permission), PositionID: position}
}

// Get returns the organization as its member sees it, with the viewer's permission and position.
// Every member sees it, guests included; anyone else gets ErrNotFound (CHART-02).
func (s *Service) Get(ctx context.Context, viewerIID, orgID string) (Membership, error) {
	return getMembership(ctx, s.store.R(), orgID, viewerIID)
}

func getMembership(ctx context.Context, q *db.Queries, orgID, iid string) (Membership, error) {
	r, err := q.GetOrgMembership(ctx, db.GetOrgMembershipParams{OrgID: orgID, Iid: iid})
	if errors.Is(err, sql.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, err
	}
	return membership(r.Organization, r.Permission, r.PositionID), nil
}

// ListMine lists the organizations the identity belongs to, by name, each with its own permission
// and position (OMEM-03).
func (s *Service) ListMine(ctx context.Context, iid string) ([]Membership, error) {
	if iid == "" {
		return nil, nil
	}
	rows, err := s.store.R().ListOrgsOfMember(ctx, iid)
	if err != nil {
		return nil, err
	}
	out := make([]Membership, len(rows))
	for i, r := range rows {
		out[i] = membership(r.Organization, r.Permission, r.PositionID)
	}
	return out, nil
}

// UpdateInput holds the changes to an organization; nil fields are unchanged.
type UpdateInput struct {
	Name *string
	// Slug is the address; it is lower-cased.
	Slug *string
}

// Update renames the organization or changes its address; only an admin does (OMEM-04). An invalid
// field changes nothing (validation error), and an address another organization has is
// ErrSlugTaken. A change that alters nothing is not audited or announced.
func (s *Service) Update(ctx context.Context, actorIID, orgID string, in UpdateInput) (Membership, error) {
	if err := s.z.RequireAdmin(ctx, orgID, actorIID); err != nil {
		return Membership{}, err
	}
	check := validation.Check{}
	var name, slug string
	if in.Name != nil {
		var err error
		if name, err = validName(*in.Name); err != nil {
			check.Add("name", "must have 1 to 100 characters")
		}
	}
	if in.Slug != nil {
		var err error
		if slug, err = handles.ValidateSlug(strings.TrimSpace(*in.Slug)); err != nil {
			check.Add("slug", "must have 1 to 40 characters: a-z, 0-9 and single hyphens between them")
		}
	}
	if err := check.Err(); err != nil {
		return Membership{}, err
	}
	var out Membership
	var changed bool
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		if err := requireAdmin(ctx, q, orgID, actorIID); err != nil {
			return err
		}
		cur, err := q.GetOrganization(ctx, orgID)
		if err != nil {
			return err
		}
		next := cur
		detail := map[string]any{}
		if in.Name != nil && name != cur.Name {
			next.Name = name
			detail["name"] = name
		}
		if in.Slug != nil && !strings.EqualFold(slug, cur.Slug) {
			other, err := q.GetOrganizationBySlug(ctx, slug)
			if err == nil && other.ID != cur.ID {
				return ErrSlugTaken
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			next.Slug = slug
			detail["slug"] = slug
		}
		if len(detail) > 0 {
			changed = true
			next.UpdatedAt = s.now().UnixMilli()
			if err := q.UpdateOrganization(ctx, db.UpdateOrganizationParams{Name: next.Name, Slug: next.Slug, UpdatedAt: next.UpdatedAt, ID: orgID}); err != nil {
				return err
			}
			if err := s.record(ctx, q, actorIID, audit.ActionOrgUpdate, orgID, detail); err != nil {
				return err
			}
		}
		out, err = getMembership(ctx, q, orgID, actorIID)
		return err
	})
	if err != nil {
		return Membership{}, err
	}
	if changed {
		s.changed(orgID)
	}
	return out, nil
}

// SetAvatar replaces the avatar of the organization with the image read from r: PNG, JPEG or WebP up
// to 2 MiB, stored centre-cropped as a 256x256 PNG. Only an admin does (OMEM-04). An image that is
// not acceptable is avatars.ErrInvalid and the old avatar stays.
func (s *Service) SetAvatar(ctx context.Context, actorIID, orgID string, r io.Reader) (Membership, error) {
	if s.blobs == nil {
		return Membership{}, errors.New("no blob store")
	}
	if err := s.z.RequireAdmin(ctx, orgID, actorIID); err != nil {
		return Membership{}, err
	}
	img, err := avatars.Process(r)
	if err != nil {
		return Membership{}, err
	}
	sha, _, err := s.blobs.Put(bytes.NewReader(img), int64(len(img)))
	if err != nil {
		return Membership{}, err
	}
	return s.storeAvatar(ctx, actorIID, orgID, sha)
}

// ClearAvatar removes the avatar of the organization; the app shows initials again. Clearing a
// missing avatar is not an error.
func (s *Service) ClearAvatar(ctx context.Context, actorIID, orgID string) error {
	_, err := s.storeAvatar(ctx, actorIID, orgID, "")
	return err
}

// storeAvatar points the organization at the avatar blob sha ("" for none). The replaced blob stays
// until the retention sweep finds nothing referencing it.
func (s *Service) storeAvatar(ctx context.Context, actorIID, orgID, sha string) (Membership, error) {
	var out Membership
	var changed bool
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		if err := requireAdmin(ctx, q, orgID, actorIID); err != nil {
			return err
		}
		cur, err := q.GetOrganization(ctx, orgID)
		if err != nil {
			return err
		}
		if cur.AvatarSha256 != sha {
			changed = true
			if err := q.SetOrganizationAvatar(ctx, db.SetOrganizationAvatarParams{AvatarSha256: sha, UpdatedAt: s.now().UnixMilli(), ID: orgID}); err != nil {
				return err
			}
			if err := s.record(ctx, q, actorIID, audit.ActionOrgUpdate, orgID, map[string]any{"avatar": sha != ""}); err != nil {
				return err
			}
		}
		out, err = getMembership(ctx, q, orgID, actorIID)
		return err
	})
	if err != nil {
		return Membership{}, err
	}
	if changed {
		s.changed(orgID)
	}
	return out, nil
}

// Avatar opens the stored avatar PNG of an organization the viewer is a member of, with its content
// hash. For an outsider, an organization without an avatar or a blob that is gone it is ErrNotFound.
func (s *Service) Avatar(ctx context.Context, viewerIID, orgID string) (*os.File, string, error) {
	m, err := s.Get(ctx, viewerIID, orgID)
	if err != nil {
		return nil, "", err
	}
	if m.AvatarSha256 == "" || s.blobs == nil {
		return nil, "", ErrNotFound
	}
	f, err := s.blobs.Open(m.AvatarSha256)
	if errors.Is(err, attachments.ErrNotFound) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	return f, m.AvatarSha256, nil
}

// Delete removes the organization with its chart, members and invitations; only an admin does. It
// is refused with a *NotEmptyError (ErrNotEmpty) while any AI has it as its home, or any connection
// or group belongs to it (ORG-06). DMs belong to no organization and never block. The users who had
// it as their default lose that setting.
func (s *Service) Delete(ctx context.Context, actorIID, orgID string) error {
	err := s.store.WithTx(ctx, func(q *db.Queries) error {
		if err := requireAdmin(ctx, q, orgID, actorIID); err != nil {
			return err
		}
		cur, err := q.GetOrganization(ctx, orgID)
		if err != nil {
			return err
		}
		blocked := &NotEmptyError{}
		if blocked.Agents, err = q.CountOrgAgents(ctx, nullable(orgID)); err != nil {
			return err
		}
		if blocked.Connectors, err = q.CountOrgConnectors(ctx, nullable(orgID)); err != nil {
			return err
		}
		if blocked.Groups, err = q.CountOrgGroups(ctx, nullable(orgID)); err != nil {
			return err
		}
		if blocked.Agents+blocked.Connectors+blocked.Groups > 0 {
			return blocked
		}
		if err := q.DeleteOrganization(ctx, orgID); err != nil {
			return err
		}
		return s.record(ctx, q, actorIID, audit.ActionOrgDelete, orgID, map[string]any{"name": cur.Name, "slug": cur.Slug})
	})
	if err != nil {
		return err
	}
	s.changed(orgID)
	return nil
}
