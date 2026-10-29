// Package runners is the registry of the servers that run container AIs (CA-01) and the hub of
// their live connections (CA-02, CA-09). A runner logs in with a token of kind runner, registers
// once (idempotent per token), keeps an outbound WebSocket and executes only the operations of
// internal/runners/spec. Partyline never dials a runner: no SSH, no inbound port.
package runners

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/runners/spec"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// Errors of the runner registry; the API maps each to one status code.
var (
	// ErrNotFound is a runner that does not exist or belongs to another owner (404, CA-07).
	ErrNotFound = errors.New("runner not found")
	// ErrTokenRequired is a non-runner credential on a runner route (403).
	ErrTokenRequired = errors.New("runner token required")
	// ErrNotRegistered is a runner token that registered no runner yet (403).
	ErrNotRegistered = errors.New("runner is not registered")
	// ErrInUse is the refusal to delete a runner that still has containers (409, runner_in_use).
	ErrInUse = errors.New("runner has containers")
	// ErrOffline is a command for a runner with no live connection (409, runner_offline).
	ErrOffline = errors.New("runner offline")
	// ErrTimeout is a command a live runner did not answer in time.
	ErrTimeout = errors.New("runner did not answer in time")
	// ErrNameTaken is a rename to a name another runner of the owner has (409).
	ErrNameTaken = errors.New("runner name already taken")
)

// OpError is a command the runner answered with ok=false. Message is the runner's own error text
// about Docker; it never carries a secret (CA-08).
type OpError struct {
	Op      spec.Op
	Message string
}

func (e *OpError) Error() string { return fmt.Sprintf("%s failed: %s", e.Op, e.Message) }

// Names and machines.
const (
	// MaxNameLength is the longest runner name in characters (namePattern below).
	MaxNameLength = 64
	// MaxMachineLength is the longest accepted machine name in characters.
	MaxMachineLength = 64
	// Audit actions and the target kind of a runner.
	ActionRegister = "runner.register"
	ActionRename   = "runner.rename"
	ActionDelete   = "runner.delete"
	TargetRunner   = "runner"
)

// namePattern is the shape of a runner name: the hostname by default, renamed by the owner.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,63}$`)

// ValidName reports whether name is an accepted runner name.
func ValidName(name string) bool { return namePattern.MatchString(name) }

// validMachine reports whether machine is an accepted machine name: 1 to MaxMachineLength
// characters without control characters (a hostname, which the runner reports about itself).
func validMachine(machine string) bool {
	n := utf8.RuneCountInString(machine)
	if n == 0 || n > MaxMachineLength {
		return false
	}
	for _, r := range machine {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// Deps are the collaborators of the registry.
type Deps struct {
	Store *store.Store
	Audit *audit.Log
	// Bus carries SessionsRevoked: a connection whose token is revoked closes at once. Optional.
	Bus *events.Bus
	// Images are the extra image repositories a container may use (config, CA-09).
	Images []string
	Logger *slog.Logger
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
	// Timeouts overrides the per-operation timeout of the hub (tests shorten it).
	Timeouts map[spec.Op]time.Duration
}

// Service is the runner registry and its hub.
type Service struct {
	d   Deps
	hub *Hub
}

// New returns a Service.
func New(d Deps) *Service {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	s := &Service{d: d}
	s.hub = NewHub(HubDeps{
		Store: d.Store, Bus: d.Bus, Logger: d.Logger, Now: d.Now, Images: d.Images, Timeouts: d.Timeouts,
	})
	return s
}

// Hub returns the hub of live runner connections.
func (s *Service) Hub() *Hub { return s.hub }

// Runner is a runner as the owner's list shows it (CA-01): what it reported and whether it is
// connected now.
type Runner struct {
	ID             string
	Name           string
	Machine        string
	Online         bool
	DockerVersion  string
	DockerError    string
	LastSeenAt     *time.Time
	ContainerCount int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// RegisterInput is what `POST /api/v1/runner/register` reports.
type RegisterInput struct {
	// Name labels the runner in the owner's list; empty defaults to Machine.
	Name string
	// Machine is the runner's hostname.
	Machine string
}

// Register records the runner of a runner token, idempotently: a token always maps to the runner
// it registered first, and only the first call creates one (CA-01). The name is made unique per
// owner with -2, -3… when another runner already uses it.
func (s *Service) Register(ctx context.Context, p authn.Principal, in RegisterInput) (Runner, bool, error) {
	if p.Kind != authn.KindRunner || p.TokenID == "" {
		return Runner{}, false, ErrTokenRequired
	}
	machine := strings.TrimSpace(in.Machine)
	name := strings.TrimSpace(in.Name)
	fields := map[string]string{}
	if !validMachine(machine) {
		fields["machine"] = fmt.Sprintf("must have 1 to %d characters without control characters", MaxMachineLength)
	}
	if name != "" && !ValidName(name) {
		fields["name"] = "must match " + namePattern.String()
	}
	if len(fields) > 0 {
		return Runner{}, false, &spec.ValidationError{Fields: fields}
	}

	var row db.Runner
	created := false
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		// The registration is idempotent per token. Inside the write transaction, so two
		// registrations of the same token cannot both insert.
		existing, err := q.GetRunnerByToken(ctx, p.TokenID)
		switch {
		case err == nil:
			row = existing
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if name == "" {
			name = machine
		}
		rows, err := q.ListRunners(ctx, p.UserID)
		if err != nil {
			return err
		}
		taken := make(map[string]bool, len(rows))
		for _, r := range rows {
			taken[r.Name] = true
		}
		name = uniqueName(name, taken)
		now := s.now()
		row = db.Runner{
			ID: store.NewID(), OwnerID: p.UserID, Name: name, TokenID: p.TokenID, Machine: machine,
			CreatedAt: ms(now), UpdatedAt: ms(now),
		}
		if err := q.CreateRunner(ctx, db.CreateRunnerParams{
			ID: row.ID, OwnerID: row.OwnerID, Name: row.Name, TokenID: row.TokenID,
			Machine: row.Machine, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}); err != nil {
			return err
		}
		created = true
		return s.d.Audit.Record(ctx, q, audit.Entry{
			Action: ActionRegister, TargetKind: TargetRunner, TargetID: row.ID,
			Detail: map[string]any{"name": row.Name, "machine": row.Machine},
		})
	})
	if err != nil {
		return Runner{}, false, err
	}
	view, err := s.view(ctx, row)
	if err != nil {
		return Runner{}, false, err
	}
	return view, created, nil
}

// List returns the owner's runners with their online state and container count (CA-01).
func (s *Service) List(ctx context.Context, ownerID string) ([]Runner, error) {
	rows, err := s.d.Store.R().ListRunners(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	out := make([]Runner, 0, len(rows))
	for _, row := range rows {
		view, err := s.view(ctx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, nil
}

// Rename changes one of the owner's runner's names (CA-01). ErrNotFound for another owner's
// runner, ErrNameTaken when the name is another runner's.
func (s *Service) Rename(ctx context.Context, ownerID, id, name string) (Runner, error) {
	name = strings.TrimSpace(name)
	if !ValidName(name) {
		return Runner{}, &spec.ValidationError{Fields: map[string]string{"name": "must match " + namePattern.String()}}
	}
	var row db.Runner
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		var err error
		row, err = q.GetRunner(ctx, db.GetRunnerParams{ID: id, OwnerID: ownerID})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		rows, err := q.ListRunners(ctx, ownerID)
		if err != nil {
			return err
		}
		for _, other := range rows {
			if other.ID != id && other.Name == name {
				return ErrNameTaken
			}
		}
		now := s.now()
		if _, err := q.UpdateRunnerName(ctx, db.UpdateRunnerNameParams{
			Name: name, UpdatedAt: ms(now), ID: id, OwnerID: ownerID,
		}); err != nil {
			return err
		}
		row.Name = name
		row.UpdatedAt = ms(now)
		return s.d.Audit.Record(ctx, q, audit.Entry{
			Action: ActionRename, TargetKind: TargetRunner, TargetID: id, Detail: map[string]any{"name": name},
		})
	})
	if err != nil {
		return Runner{}, err
	}
	return s.view(ctx, row)
}

// Delete removes one of the owner's runners and revokes its token (CA-01). A runner that still
// has containers is refused with ErrInUse (409, runner_in_use): the containers would be left
// behind on the host (CA-06 deletes them first). ErrNotFound for another owner's runner.
func (s *Service) Delete(ctx context.Context, ownerID, id string) error {
	var row db.Runner
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		var err error
		row, err = q.GetRunner(ctx, db.GetRunnerParams{ID: id, OwnerID: ownerID})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		containers, err := q.ListContainerAgentsByRunner(ctx, id)
		if err != nil {
			return err
		}
		if len(containers) > 0 {
			return ErrInUse
		}
		now := s.now()
		if _, err := q.RevokeToken(ctx, db.RevokeTokenParams{
			RevokedAt: sql.NullInt64{Int64: ms(now), Valid: true}, ID: row.TokenID, UserID: ownerID,
		}); err != nil {
			return err
		}
		n, err := q.DeleteRunner(ctx, db.DeleteRunnerParams{ID: id, OwnerID: ownerID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return s.d.Audit.Record(ctx, q, audit.Entry{
			Action: ActionDelete, TargetKind: TargetRunner, TargetID: id,
			Detail: map[string]any{"name": row.Name, "machine": row.Machine},
		})
	})
	if err != nil {
		return err
	}
	// The live connection closes and its credential ends: the runner reconnects only to learn
	// that its token is refused.
	s.hub.Kick(id, "runner deleted")
	if s.d.Bus != nil {
		s.d.Bus.Publish(events.SessionsRevoked{UserID: ownerID, TokenIDs: []string{row.TokenID}})
	}
	return nil
}

// view reads what the list shows of a stored runner.
func (s *Service) view(ctx context.Context, row db.Runner) (Runner, error) {
	containers, err := s.d.Store.R().ListContainerAgentsByRunner(ctx, row.ID)
	if err != nil {
		return Runner{}, err
	}
	out := Runner{
		ID: row.ID, Name: row.Name, Machine: row.Machine, Online: s.hub.Online(row.ID),
		DockerVersion: row.DockerVersion.String, DockerError: row.DockerError.String,
		ContainerCount: len(containers), CreatedAt: fromMS(row.CreatedAt), UpdatedAt: fromMS(row.UpdatedAt),
	}
	if row.LastSeenAt.Valid {
		t := fromMS(row.LastSeenAt.Int64)
		out.LastSeenAt = &t
	}
	return out, nil
}

// RunnerByToken returns the runner a runner token registered, for the WebSocket handshake.
// ErrNotRegistered when the token has no runner yet.
func (s *Service) RunnerByToken(ctx context.Context, tokenID string) (db.Runner, error) {
	row, err := s.d.Store.R().GetRunnerByToken(ctx, tokenID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Runner{}, ErrNotRegistered
	}
	return row, err
}

// uniqueName returns base, or base-2, base-3… when another runner of the owner already has it.
// The result is cut so that the suffix still fits MaxNameLength and stays a valid name.
func uniqueName(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for i := 2; i < 1000; i++ {
		suffix := "-" + strconv.Itoa(i)
		prefix := base
		if utf8.RuneCountInString(prefix) > MaxNameLength-len(suffix) {
			prefix = string([]rune(prefix)[:MaxNameLength-len(suffix)])
		}
		// A cut can end on a space or a dot, which the pattern refuses at the end.
		prefix = strings.TrimRight(prefix, " .")
		candidate := prefix + suffix
		if !taken[candidate] && ValidName(candidate) {
			return candidate
		}
	}
	// Base and its numbered names are all taken (a thousand runners): a names still unique.
	for i := len(taken) + 2; ; i++ {
		candidate := fmt.Sprintf("runner-%d", i)
		if !taken[candidate] {
			return candidate
		}
	}
}

func (s *Service) now() time.Time { return s.d.Now() }

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }
