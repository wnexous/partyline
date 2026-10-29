// Package ownersecrets stores the named credentials an owner gives their container AIs (CA-03).
// A value is write-only: it is sealed at rest with the instance key, listed and returned as a name
// and its dates only, and opened only by the container service that writes a container's env file.
// An error never carries the value (CA-08).
package ownersecrets

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/secrets"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// Limits and the pattern of a secret (design, "Data").
const (
	// MaxNameLength is the longest name in bytes; a name becomes an environment variable.
	MaxNameLength = 64
	// MinValueBytes and MaxValueBytes bound the value: an empty credential is a mistake, and a
	// longer string is not a credential.
	MinValueBytes = 1
	MaxValueBytes = 16384
	// NamePattern is what a name matches: the environment variable names the shell and the
	// harnesses accept, starting with a letter.
	NamePattern = `^[A-Za-z][A-Za-z0-9_-]{0,63}$`
)

var nameRE = regexp.MustCompile(NamePattern)

// ErrNotFound is a secret the owner does not have; another owner's secret is the same error (404).
var ErrNotFound = errors.New("ownersecrets: secret not found")

// ValidName reports whether name can be stored and exposed as an environment variable.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// Secret is a stored secret without its value: the id the container env mapping keeps, the name,
// and when it was created and last replaced (CA-03).
type Secret struct {
	ID        string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Deps are the collaborators of the Service.
type Deps struct {
	Store *store.Store
	// Box seals and opens the values with the instance key.
	Box *secrets.Box
	// Audit records secret.put and secret.delete; defaults to Log over Store and Now.
	Audit *audit.Log
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// Service is the use cases of an owner's secrets.
type Service struct {
	store *store.Store
	box   *secrets.Box
	audit *audit.Log
	now   func() time.Time
}

// New returns a Service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Audit == nil {
		d.Audit = audit.New(d.Store, d.Now)
	}
	return &Service{store: d.Store, box: d.Box, audit: d.Audit, now: d.Now}
}

// Put stores value under name for owner, replacing the previous value of that name in place: the
// row keeps its id (so the env mapping of every container that uses it keeps working) and its
// creation time. The value is sealed before it reaches the database (CA-03).
func (s *Service) Put(ctx context.Context, owner, name, value string) error {
	if err := validate(name, value); err != nil {
		return err
	}
	sealed, err := s.box.Seal([]byte(value))
	if err != nil {
		return fmt.Errorf("ownersecrets: seal value: %w", err)
	}
	now := s.now()
	return s.store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.UpsertOwnerSecret(ctx, db.UpsertOwnerSecretParams{
			ID: store.NewID(), OwnerID: owner, Name: name, ValueSealed: sealed,
			CreatedAt: ms(now), UpdatedAt: ms(now),
		}); err != nil {
			return err
		}
		// The audit log names the secret and never holds the value (CA-08).
		return s.audit.Record(ctx, q, audit.Entry{
			Action: audit.ActionSecretPut, TargetKind: audit.TargetSecret, TargetID: name,
		})
	})
}

// validate refuses a name or a value with a validation error that names the field and never
// echoes the value.
func validate(name, value string) error {
	fields := map[string]string{}
	if !ValidName(name) {
		fields["name"] = "must start with a letter and have 1 to 64 letters, digits, _ or -"
	}
	if n := len(value); n < MinValueBytes || n > MaxValueBytes {
		fields["value"] = "must have 1 to 16384 bytes"
	}
	if len(fields) > 0 {
		return &validation.Error{Fields: fields}
	}
	return nil
}

// ListNames returns the owner's secrets by name, without their values (CA-03, CA-08).
func (s *Service) ListNames(ctx context.Context, owner string) ([]Secret, error) {
	rows, err := s.store.R().ListOwnerSecretNames(ctx, owner)
	if err != nil {
		return nil, err
	}
	out := make([]Secret, len(rows))
	for i, row := range rows {
		out[i] = Secret{
			ID: row.ID, Name: row.Name,
			CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
		}
	}
	return out, nil
}

// Delete removes the owner's secret by name and audits the name only; another owner's secret is
// ErrNotFound.
func (s *Service) Delete(ctx context.Context, owner, name string) error {
	return s.store.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.DeleteOwnerSecret(ctx, db.DeleteOwnerSecretParams{OwnerID: owner, Name: name})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return s.audit.Record(ctx, q, audit.Entry{
			Action: audit.ActionSecretDelete, TargetKind: audit.TargetSecret, TargetID: name,
		})
	})
}

// Open returns the values of ids for owner, by secret id. It is only for the container service,
// which writes a container's env file; no route exposes it or its values. An id that is unknown or
// belongs to another owner is left out of the map (CA-07), and an empty ids returns an empty map.
func (s *Service) Open(ctx context.Context, owner string, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	rows, err := s.store.R().GetOwnerSecrets(ctx, db.GetOwnerSecretsParams{OwnerID: owner, Ids: ids})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		value, err := s.box.Open(row.ValueSealed)
		if err != nil {
			return nil, fmt.Errorf("ownersecrets: open %q: %w", row.Name, err)
		}
		out[row.ID] = string(value)
	}
	return out, nil
}

// ms converts a time to the unix milliseconds the store keeps.
func ms(t time.Time) int64 { return t.UnixMilli() }
