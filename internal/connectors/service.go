package connectors

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/secrets"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// MaxNameLength is the longest connector name in characters.
const MaxNameLength = 100

// Runner starts and stops running connectors; the Manager implements it.
type Runner interface {
	// Apply (re)starts the connector when it is enabled (CON-04).
	Apply(id string)
	// Stop stops the connector and its outbound sends (CON-05).
	Stop(id string)
}

// ServiceDeps are the collaborators of the Service.
type ServiceDeps struct {
	Store  *store.Store
	Access *access.Authorizer
	Bus    *events.Bus
	Box    *secrets.Box
	Kinds  *Registry
	// Runner is told about created, changed and deleted connectors; nil runs nothing.
	Runner Runner
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// Service manages connectors for users: CRUD, enable/disable and sharing through grants
// (AD-005). Secrets are stored only in secrets_enc (SEC-04) and never returned (CON-03).
type Service struct {
	d     ServiceDeps
	audit *audit.Log
}

// NewService returns a Service.
func NewService(d ServiceDeps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d, audit: audit.New(d.Store, d.Now)}
}

// Kinds returns the supported kinds.
func (s *Service) Kinds() []Kind { return s.d.Kinds.Kinds() }

// Kind returns the named kind, or a kind without fields when it is not registered (a
// connector created by a build that supported it).
func (s *Service) Kind(name string) Kind {
	if k, ok := s.d.Kinds.Lookup(name); ok {
		return k
	}
	return Kind{Name: name, Label: name}
}

// View is a connector as seen by one user.
type View struct {
	db.Connector
	OwnerUsername    string
	OwnerDisplayName string
	// Level is manage for the owner and use for grantees.
	Level access.Level
	// Config has secrets masked (CON-03).
	Config map[string]any
	// InboundHook is copied from the kind.
	InboundHook bool
}

// CreateInput is a new connector.
type CreateInput struct {
	Kind    string
	Name    string
	Config  Config
	Enabled *bool // nil = true
}

// UpdateInput changes a connector; nil fields are unchanged.
type UpdateInput struct {
	Name    *string
	Config  Config // nil = unchanged; merged into the stored config (Kind.Merge)
	Enabled *bool
}

// authorize requires the user to hold want on the connector: no access is
// access.ErrNotFound, use without manage is access.ErrForbidden.
func (s *Service) authorize(ctx context.Context, p authn.Principal, id string, want access.Level) (access.Level, error) {
	if p.Kind != authn.KindUser {
		return access.None, access.ErrForbidden
	}
	l, err := s.d.Access.ConnectorLevel(ctx, p.UserID, id)
	if err != nil {
		return access.None, err
	}
	if !access.AtLeast(l, dto.LevelUse) {
		return access.None, access.ErrNotFound
	}
	if !access.AtLeast(l, want) {
		return l, access.ErrForbidden
	}
	return l, nil
}

// Require authorizes the user for want on the connector up front.
func (s *Service) Require(ctx context.Context, p authn.Principal, id string, want access.Level) error {
	_, err := s.authorize(ctx, p, id, want)
	return err
}

func validName(name string) bool {
	n := utf8.RuneCountInString(name)
	return n > 0 && n <= MaxNameLength
}

// validate checks the kind, name and config; config errors are named config.<field>.
func (s *Service) validate(kindKnown bool, name string, cfg Config, k Kind) (Config, error) {
	check := validation.Check{}
	if !kindKnown {
		check.Add("kind", "unknown connector kind")
	}
	if !validName(name) {
		check.Add("name", "must have 1 to 100 characters")
	}
	var out Config
	if kindKnown {
		var errs map[string]string
		out, errs = k.Validate(cfg)
		for field, reason := range errs {
			check.Add("config."+field, reason)
		}
	}
	return out, check.Err()
}

// Create adds a connector owned by the caller and starts it when enabled (CON-04).
func (s *Service) Create(ctx context.Context, p authn.Principal, in CreateInput) (View, error) {
	if p.Kind != authn.KindUser {
		return View{}, access.ErrForbidden
	}
	k, known := s.d.Kinds.Lookup(in.Kind)
	name := strings.TrimSpace(in.Name)
	cfg, err := s.validate(known, name, in.Config, k)
	if err != nil {
		return View{}, err
	}
	public, sealed, err := k.Encode(s.d.Box, cfg)
	if err != nil {
		return View{}, err
	}
	enabled := in.Enabled == nil || *in.Enabled
	id := store.NewID()
	now := s.d.Now().UnixMilli()
	err = s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.CreateConnector(ctx, db.CreateConnectorParams{
			ID: id, OwnerID: p.UserID, Kind: k.Name, Name: name, ConfigJson: public, SecretsEnc: sealed,
			Enabled: enabled, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, audit.Entry{
			ActorID: p.UserID, Action: audit.ActionConnectorCreate, TargetKind: audit.TargetConnector, TargetID: id,
			Detail: map[string]any{"kind": k.Name, "name": name, "enabled": enabled},
		})
	})
	if err != nil {
		return View{}, err
	}
	if enabled && s.d.Runner != nil {
		s.d.Runner.Apply(id)
	}
	return s.Get(ctx, p, id)
}

// Get returns a connector the user owns or may use.
func (s *Service) Get(ctx context.Context, p authn.Principal, id string) (View, error) {
	l, err := s.authorize(ctx, p, id, dto.LevelUse)
	if err != nil {
		return View{}, err
	}
	row, err := s.d.Store.R().GetConnectorWithOwner(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return View{}, access.ErrNotFound
	}
	if err != nil {
		return View{}, err
	}
	return s.view(row.Connector, row.OwnerUsername, row.OwnerDisplayName, l)
}

func (s *Service) view(c db.Connector, ownerUsername, ownerDisplay string, l access.Level) (View, error) {
	k := s.Kind(c.Kind)
	public, err := Decode(s.d.Box, c.ConfigJson, nil)
	if err != nil {
		return View{}, err
	}
	sealed, err := Decode(s.d.Box, "", c.SecretsEnc)
	if err != nil {
		return View{}, err
	}
	masked := k.Masked(public)
	for name := range sealed { // every sealed value is a secret, even of an unregistered kind
		masked[name] = dto.SecretMask
	}
	return View{
		Connector: c, OwnerUsername: ownerUsername, OwnerDisplayName: ownerDisplay, Level: l,
		Config: masked, InboundHook: k.InboundHook,
	}, nil
}

// List returns the connectors the user owns plus those shared with them, by name.
func (s *Service) List(ctx context.Context, p authn.Principal) ([]View, error) {
	if p.Kind != authn.KindUser {
		return nil, access.ErrForbidden
	}
	rows, err := s.d.Store.R().ListConnectorsForUser(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(rows))
	for _, row := range rows {
		l := dto.LevelUse
		if row.Connector.OwnerID == p.UserID {
			l = dto.LevelManage
		}
		v, err := s.view(row.Connector, row.OwnerUsername, row.OwnerDisplayName, l)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Update changes name, config and enabled flag; requires ownership (manage). A config change
// or enabling restarts the connector, disabling stops it (CON-04, CON-05).
func (s *Service) Update(ctx context.Context, p authn.Principal, id string, in UpdateInput) (View, error) {
	if _, err := s.authorize(ctx, p, id, dto.LevelManage); err != nil {
		return View{}, err
	}
	c, err := s.d.Store.R().GetConnector(ctx, id)
	if err != nil {
		return View{}, err
	}
	k, known := s.d.Kinds.Lookup(c.Kind)
	name, public, sealed, enabled := c.Name, c.ConfigJson, c.SecretsEnc, c.Enabled
	var changed []string
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
		if !validName(name) {
			return View{}, &validation.Error{Fields: map[string]string{"name": "must have 1 to 100 characters"}}
		}
		if name != c.Name {
			changed = append(changed, "name")
		}
	}
	if in.Config != nil {
		stored, err := Decode(s.d.Box, c.ConfigJson, c.SecretsEnc)
		if err != nil {
			return View{}, err
		}
		cfg, err := s.validate(known, name, k.Merge(stored, in.Config), k)
		if err != nil {
			return View{}, err
		}
		if public, sealed, err = k.Encode(s.d.Box, cfg); err != nil {
			return View{}, err
		}
		changed = append(changed, "config")
	}
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	toggled := enabled != c.Enabled
	err = s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.UpdateConnector(ctx, db.UpdateConnectorParams{
			Name: name, ConfigJson: public, SecretsEnc: sealed, Enabled: enabled, UpdatedAt: s.d.Now().UnixMilli(), ID: id,
		}); err != nil {
			return err
		}
		if len(changed) > 0 {
			if err := s.audit.Record(ctx, q, audit.Entry{
				ActorID: p.UserID, Action: audit.ActionConnectorUpdate, TargetKind: audit.TargetConnector, TargetID: id,
				Detail: map[string]any{"kind": c.Kind, "name": name, "changed": changed},
			}); err != nil {
				return err
			}
		}
		if !toggled {
			return nil
		}
		action := audit.ActionConnectorDisable
		if enabled {
			action = audit.ActionConnectorEnable
		}
		return s.audit.Record(ctx, q, audit.Entry{
			ActorID: p.UserID, Action: action, TargetKind: audit.TargetConnector, TargetID: id,
			Detail: map[string]any{"kind": c.Kind, "name": name},
		})
	})
	if err != nil {
		return View{}, err
	}
	if s.d.Runner != nil {
		switch {
		case !enabled && toggled:
			s.d.Runner.Stop(id)
		case enabled && (toggled || in.Config != nil):
			s.d.Runner.Apply(id)
		}
	}
	return s.Get(ctx, p, id)
}

// Delete stops and removes the connector with its chats (and their room memberships and
// outbox entries) and grants; requires ownership (manage).
func (s *Service) Delete(ctx context.Context, p authn.Principal, id string) error {
	if _, err := s.authorize(ctx, p, id, dto.LevelManage); err != nil {
		return err
	}
	c, err := s.d.Store.R().GetConnector(ctx, id)
	if err != nil {
		return err
	}
	if s.d.Runner != nil {
		s.d.Runner.Stop(id)
	}
	var roomIDs []string
	err = s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		var err error
		if roomIDs, err = q.DeleteConnectorChatMemberships(ctx, id); err != nil {
			return err
		}
		if err := q.DeleteGrants(ctx, db.DeleteGrantsParams{ResourceKind: string(access.KindConnector), ResourceID: id}); err != nil {
			return err
		}
		if err := q.DeleteConnector(ctx, id); err != nil { // chats and their outbox entries cascade
			return err
		}
		return s.audit.Record(ctx, q, audit.Entry{
			ActorID: p.UserID, Action: audit.ActionConnectorDelete, TargetKind: audit.TargetConnector, TargetID: id,
			Detail: map[string]any{"kind": c.Kind, "name": c.Name},
		})
	})
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, r := range roomIDs {
		if !seen[r] {
			seen[r] = true
			s.d.Bus.Publish(events.MembersChanged{RoomID: r})
		}
	}
	return nil
}

// Sharing returns the connector's visibility preset; requires use.
func (s *Service) Sharing(ctx context.Context, p authn.Principal, id string) (access.Visibility, error) {
	if _, err := s.authorize(ctx, p, id, dto.LevelUse); err != nil {
		return access.Visibility{}, err
	}
	return s.d.Access.Sharing(ctx, access.KindConnector, id)
}

// SetSharing replaces the connector's use grants with a visibility preset (ACL-07); requires
// ownership (manage). The change is audited as grants.set.
func (s *Service) SetSharing(ctx context.Context, p authn.Principal, id string, v access.Visibility) (access.Visibility, error) {
	if _, err := s.authorize(ctx, p, id, dto.LevelManage); err != nil {
		return access.Visibility{}, err
	}
	c, err := s.d.Store.R().GetConnector(ctx, id)
	if err != nil {
		return access.Visibility{}, err
	}
	if err := s.d.Access.SetVisibility(ctx, p.UserID, access.KindConnector, id, c.OwnerID, v); err != nil {
		return access.Visibility{}, err
	}
	return s.d.Access.Sharing(ctx, access.KindConnector, id)
}
