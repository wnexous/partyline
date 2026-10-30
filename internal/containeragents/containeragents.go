// Package containeragents runs an AI in a container on one of the owner's runners (CA-04, CA-06).
// Creating one gives the AI its identity, its agent token and an environment of Partyline variables
// plus the owner's named secrets, and asks the runner to start the agent image; the lifecycle then
// follows what the runner reports. No secret value, token or key is ever logged, audited or
// returned (CA-08): only the environment variable names and the secret names are.
package containeragents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/access"
	"github.com/wnexous/partyline/internal/agents"
	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/events"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/ownersecrets"
	"github.com/wnexous/partyline/internal/runners"
	"github.com/wnexous/partyline/internal/runners/spec"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/users"
)

// ErrNotFound is a container that does not exist, or an AI the caller cannot see (404).
var ErrNotFound = errors.New("container not found")

// Limits, defaults and the environment contract of the agent image (deploy/agent/README.md).
const (
	// DefaultCPUs and DefaultMemoryMB are the limits of a create that asks for none.
	DefaultCPUs     = 1
	DefaultMemoryMB = 1024
	// MaxLogsTail is the most log lines one read returns; DefaultLogsTail is the default.
	MaxLogsTail     = 500
	DefaultLogsTail = spec.DefaultTail
	// Desired states of the row (container_agents.desired).
	DesiredRunning = "running"
	DesiredStopped = "stopped"
	// The environment Partyline sets for the container; a mapping may not override one (CA-08).
	EnvURL     = "PARTYLINE_URL"
	EnvToken   = "PARTYLINE_TOKEN"
	EnvHarness = "PARTYLINE_HARNESS"
	EnvRepo    = "PARTYLINE_REPO"
	EnvRepoRef = "PARTYLINE_REPO_REF"
	// AgentLabel is the Docker label that ties a container to its AI (CA-09).
	AgentLabel = "partyline.agent"
	// DefaultRepoRef is the branch cloned when a repository is given without one.
	DefaultRepoRef = "main"
	// shortIDLength is how many characters of an agent id name its container and volume.
	shortIDLength = 12
	// maxTokenName is the longest token name the account page accepts.
	maxTokenName = 64
)

// Audit actions of the container lifecycle (CA-07); the target is the AI the container runs.
const (
	ActionCreate  = "container.create"
	ActionStart   = "container.start"
	ActionStop    = "container.stop"
	ActionRestart = "container.restart"
	ActionLogs    = "container.logs"
	ActionDelete  = "container.delete"
)

// Requested actions of POST /api/v1/agents/{id}/container (CA-06).
const (
	RequestStart   = "start"
	RequestStop    = "stop"
	RequestRestart = "restart"
)

// harnesses are the apps a container may run (CA-04): the four with a live channel.
var harnesses = map[string]bool{"claude": true, "opencode": true, "pi": true, "omp": true}

// reservedEnv are the variables Partyline sets; a mapping of the owner may not replace them.
var reservedEnv = map[string]bool{
	EnvURL: true, EnvToken: true, EnvHarness: true, EnvRepo: true, EnvRepoRef: true,
}

// releaseVersion matches a release tag whose agent image exists (v1.2.3, 1.2.3-rc.1), and
// describeSuffix marks the git-describe form of a commit after a tag (v1.2.3-5-gabcdef) and a dirty
// tree: no image is built for those, so they run :latest.
var (
	releaseVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$`)
	describeSuffix = regexp.MustCompile(`-[0-9]+-g[0-9a-f]+`)
)

// ImageFor returns the agent image of a server build (CA-04): the release tag for a release build
// (v1.2.3 -> ghcr.io/wnexous/partyline-agent:1.2.3) and :latest for a development build.
func ImageFor(version string) string {
	tag := "latest"
	if releaseVersion.MatchString(version) && !describeSuffix.MatchString(version) && !strings.HasSuffix(version, "-dirty") {
		tag = strings.TrimPrefix(version, "v")
	}
	return spec.DefaultImageRepo + ":" + tag
}

// ContainerName is the Docker name of an agent's container: pl-<short agent id> (CA-09).
func ContainerName(agentID string) string { return "pl-" + shortID(agentID) }

// VolumeName is the named volume of an agent's workspace: pl-<short agent id>-ws (CA-05).
func VolumeName(agentID string) string { return ContainerName(agentID) + "-ws" }

// shortID is 12 characters of an agent id without its dashes. It takes the tail: an id is a UUIDv7,
// whose first 48 bits are the creation millisecond, so the head would collide for two AIs created
// in the same millisecond; the tail is random, and 48 bits keep a collision of two containers
// practically impossible while the name stays short.
func shortID(id string) string {
	hex := strings.ReplaceAll(id, "-", "")
	if len(hex) > shortIDLength {
		hex = hex[len(hex)-shortIDLength:]
	}
	return hex
}

// EnvVar is one environment variable of the container mapped to one of the owner's secrets
// (CA-03, CA-04). The value never leaves the sealed row except into the runner's env file.
type EnvVar struct {
	// Var is the name of the variable (^[A-Z_][A-Z0-9_]{0,63}$).
	Var string
	// SecretName is the owner's secret whose value it gets.
	SecretName string
}

// CreateInput is what POST /api/v1/container-agents asks for (CA-04).
type CreateInput struct {
	// RunnerID is the server of the owner that runs the container.
	RunnerID string
	// Harness is claude, opencode, pi or omp.
	Harness string
	// DisplayName and Handle are the profile of the new AI; the handle must be free.
	DisplayName, Handle string
	// RepoURL and RepoRef are an optional repository cloned into /workspace/repo (CA-05).
	RepoURL, RepoRef string
	// Env maps variables of the container to the owner's secrets.
	Env []EnvVar
	// CPUs and MemoryMB are the limits; zero takes the defaults.
	CPUs     float64
	MemoryMB int64
}

// Container is a container AI as the API shows it (CA-06): the AI it runs, where it runs, what it
// was created with, the desired state and the state the runner last reported.
type Container struct {
	AgentID       string
	RunnerID      string
	Harness       string
	Image         string
	RepoURL       string
	RepoRef       string
	Volume        string
	ContainerName string
	Env           []EnvVar
	CPUs          float64
	MemoryMB      int64
	Desired       string
	State         string
	LastError     string
	DisplayName   string
	Handle        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Deps are the collaborators of the container service.
type Deps struct {
	Store *store.Store
	// Agents creates the AI of a container and decides who may manage it (CA-04, CA-07).
	Agents *agents.Registry
	// Runners is the registry and hub of the servers that run containers (CA-01, CA-09).
	Runners *runners.Service
	// Secrets opens the owner's named secrets for the container's environment (CA-03, CA-08).
	Secrets *ownersecrets.Service
	// Users creates and revokes the AI's agent token.
	Users *users.Service
	Audit *audit.Log
	// Bus carries SessionsRevoked so a revoked container token closes at once; optional.
	Bus *events.Bus
	// PublicURL is the instance URL the CLI in the container logs in to (CA-04).
	PublicURL string
	// Images are the extra image repositories a run may use (CA-09).
	Images []string
	// Version is the server build version; the agent image tag follows it (CA-04).
	Version string
	Logger  *slog.Logger
	// Now returns the current time; defaults to time.Now.
	Now func() time.Time
}

// Service is the use cases of container AIs.
type Service struct {
	d Deps
}

// New returns a Service.
func New(d Deps) *Service {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Audit == nil {
		d.Audit = audit.New(d.Store, d.Now)
	}
	d.PublicURL = strings.TrimRight(d.PublicURL, "/")
	return &Service{d: d}
}

// Create creates a container AI for the owner of the runner (CA-04): it validates the request,
// resolves the named secrets and opens them, creates the AI with its token and the
// container_agents row in one transaction, and asks the runner to run the agent image. A runner
// of another owner is runners.ErrNotFound (404), an offline one runners.ErrOffline (409). A run
// the runner refuses keeps the row with last_error, so the owner can retry start or delete.
func (s *Service) Create(ctx context.Context, p authn.Principal, in CreateInput) (Container, error) {
	if p.Kind != authn.KindUser {
		return Container{}, access.ErrForbidden
	}
	in, err := validateCreate(in)
	if err != nil {
		return Container{}, err
	}
	runner, err := s.d.Store.R().GetRunner(ctx, db.GetRunnerParams{ID: in.RunnerID, OwnerID: p.UserID})
	if errors.Is(err, sql.ErrNoRows) {
		return Container{}, runners.ErrNotFound
	}
	if err != nil {
		return Container{}, err
	}
	if !s.d.Runners.Hub().Online(runner.ID) {
		return Container{}, runners.ErrOffline
	}
	mapped, ids, err := s.resolveSecrets(ctx, p.UserID, in.Env)
	if err != nil {
		return Container{}, err
	}
	// The mapped values are validated before anything is written: a value too long for the
	// protocol must not leave half a container behind.
	if err := validateMapped(mapped); err != nil {
		return Container{}, err
	}
	image := ImageFor(s.d.Version)
	// The id is made before the transaction: the container's name (and so the AI's machine) follows
	// from it, and the machine is part of the identity (CA-04).
	agentID := store.NewID()
	var (
		agent  db.Agent
		secret string
		row    db.ContainerAgent
	)
	now := s.d.Now()
	err = s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		agent, err = s.d.Agents.CreateContainerIdentity(ctx, q, p.UserID, agents.ContainerIdentity{
			ID: agentID, ContainerName: ContainerName(agentID), Harness: in.Harness,
			Repo: in.RepoURL != "", DisplayName: in.DisplayName, Handle: in.Handle,
		})
		if err != nil {
			return err
		}
		name := ContainerName(agent.ID)
		volume := VolumeName(agent.ID)
		tok, tokenSecret, err := s.d.Users.CreateTokenTx(ctx, q, p.UserID, users.TokenInput{
			Kind: dto.TokenKindAgent, Name: tokenName(in.DisplayName),
		})
		if err != nil {
			return err
		}
		secret = tokenSecret
		row = db.ContainerAgent{
			AgentID: agent.ID, RunnerID: runner.ID, Harness: in.Harness, Image: image,
			RepoUrl:       nullString(in.RepoURL),
			RepoRef:       nullString(in.RepoRef),
			Volume:        volume,
			ContainerName: name,
			Env:           marshalEnv(ids),
			Cpus:          in.CPUs,
			MemoryMb:      in.MemoryMB,
			Desired:       DesiredRunning,
			State:         string(spec.StateUnknown),
			TokenID:       sql.NullString{String: tok.ID, Valid: true},
			CreatedAt:     now.UnixMilli(),
			UpdatedAt:     now.UnixMilli(),
		}
		if err := q.CreateContainerAgent(ctx, db.CreateContainerAgentParams{
			AgentID: row.AgentID, RunnerID: row.RunnerID, Harness: row.Harness, Image: row.Image,
			RepoUrl: row.RepoUrl, RepoRef: row.RepoRef, Volume: row.Volume, ContainerName: row.ContainerName,
			Env: row.Env, Cpus: row.Cpus, MemoryMb: row.MemoryMb, Desired: row.Desired,
			TokenID: row.TokenID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}); err != nil {
			return err
		}
		return s.d.Audit.Record(ctx, q, audit.Entry{
			ActorID: p.UserID, Action: ActionCreate, TargetKind: audit.TargetAgent, TargetID: agent.ID,
			Detail: map[string]any{
				"runner_id": runner.ID, "harness": in.Harness, "image": image,
				"container": name, "volume": volume,
			},
		})
	})
	if err != nil {
		return Container{}, err
	}
	env := s.containerEnv(in, in.RepoRef, secret, mapped)
	run := spec.RunSpec{
		Name: row.ContainerName, Image: row.Image, Volume: row.Volume, CPUs: row.Cpus, MemoryMB: row.MemoryMb,
		Labels: map[string]string{AgentLabel: agent.ID}, Env: env,
	}
	s.announceOrg(agent)
	if err := spec.ValidateRun(run, s.d.Images); err != nil {
		s.setLastError(ctx, agent.ID, err)
		return Container{}, err
	}
	if _, err := s.d.Runners.Hub().Do(ctx, runner.ID, spec.Command{Op: spec.OpRun, Run: &run}); err != nil {
		s.setLastError(ctx, agent.ID, err)
		return Container{}, err
	}
	return s.view(ctx, row)
}

// Start asks the runner to start the AI's container, after making "running" its desired state
// (CA-06). A container the runner no longer has is created again with a fresh token and
// environment. Failures are stored as last_error and returned; the row stays.
func (s *Service) Start(ctx context.Context, p authn.Principal, agentID string) (Container, error) {
	return s.operate(ctx, p, agentID, spec.OpStart)
}

// Stop asks the runner to stop the AI's container and makes "stopped" its desired state (CA-06).
func (s *Service) Stop(ctx context.Context, p authn.Principal, agentID string) (Container, error) {
	return s.operate(ctx, p, agentID, spec.OpStop)
}

// Restart asks the runner to restart the AI's container (CA-06).
func (s *Service) Restart(ctx context.Context, p authn.Principal, agentID string) (Container, error) {
	return s.operate(ctx, p, agentID, spec.OpRestart)
}

// operate runs one lifecycle operation for a caller who manages the AI (CA-07) and writes what it
// implies: the desired state, the state after a success and, on a failure, last_error. Start on a
// missing container falls back to run with a rotated token, so a container removed on the host
// (or lost with a volume) comes back.
func (s *Service) operate(ctx context.Context, p authn.Principal, agentID string, op spec.Op) (Container, error) {
	row, err := s.load(ctx, p, agentID)
	if err != nil {
		return Container{}, err
	}
	desired := DesiredRunning
	if op == spec.OpStop {
		desired = DesiredStopped
	}
	if err := s.setDesired(ctx, row.AgentID, desired); err != nil {
		return Container{}, err
	}
	row.Desired = desired
	action := map[spec.Op]string{spec.OpStart: ActionStart, spec.OpStop: ActionStop, spec.OpRestart: ActionRestart}[op]
	_, err = s.d.Runners.Hub().Do(ctx, row.RunnerID, spec.Command{Op: op, Name: row.ContainerName})
	if err == nil {
		return s.succeeded(ctx, row, action, op)
	}
	if op != spec.OpStart || errors.Is(err, runners.ErrOffline) {
		s.setLastError(ctx, row.AgentID, err)
		return Container{}, err
	}
	// The runner could not start it: it may be gone. Recreating it needs a token, and the secret
	// of the old one is not stored (only its hash), so the AI gets a fresh token for the fresh
	// container; the old one is revoked.
	var secret string
	secret, row, err = s.rotateToken(ctx, row)
	if err != nil {
		s.setLastError(ctx, row.AgentID, err)
		return Container{}, err
	}
	env, err := s.freshEnv(ctx, row, secret)
	if err != nil {
		s.setLastError(ctx, row.AgentID, err)
		return Container{}, err
	}
	run := s.runSpec(row, env)
	if _, err := s.d.Runners.Hub().Do(ctx, row.RunnerID, spec.Command{Op: spec.OpRun, Run: &run}); err != nil {
		s.setLastError(ctx, row.AgentID, err)
		return Container{}, err
	}
	return s.succeeded(ctx, row, action, spec.OpRun)
}

// succeeded records the state an operation left the container in and clears last_error, auditing
// the action.
func (s *Service) succeeded(ctx context.Context, row db.ContainerAgent, action string, op spec.Op) (Container, error) {
	state := string(spec.StateRunning)
	if op == spec.OpStop {
		state = string(spec.StateStopped)
	}
	now := s.d.Now()
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		if err := q.SetContainerAgentState(ctx, db.SetContainerAgentStateParams{
			State: state, UpdatedAt: now.UnixMilli(), AgentID: row.AgentID,
		}); err != nil {
			return err
		}
		if row.LastError.Valid {
			if err := q.SetContainerAgentLastError(ctx, db.SetContainerAgentLastErrorParams{
				LastError: sql.NullString{}, UpdatedAt: now.UnixMilli(), AgentID: row.AgentID,
			}); err != nil {
				return err
			}
		}
		if err := s.d.Audit.Record(ctx, q, audit.Entry{
			ActorID: principalID(ctx), Action: action, TargetKind: audit.TargetAgent, TargetID: row.AgentID,
			Detail: map[string]any{
				"runner_id": row.RunnerID, "container": row.ContainerName, "state": state,
			},
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return Container{}, err
	}
	row.State = state
	row.LastError = sql.NullString{}
	row.UpdatedAt = now.UnixMilli()
	return s.view(ctx, row)
}

// State returns the container of an AI the caller manages, as the runner last reported it (CA-06).
func (s *Service) State(ctx context.Context, p authn.Principal, agentID string) (Container, error) {
	row, err := s.load(ctx, p, agentID)
	if err != nil {
		return Container{}, err
	}
	return s.view(ctx, row)
}

// Logs returns the last tail lines of the container's output (CA-06); tail is clamped to
// 1..MaxLogsTail and defaults to DefaultLogsTail. Reading a container's output is audited.
func (s *Service) Logs(ctx context.Context, p authn.Principal, agentID string, tail int) (string, error) {
	row, err := s.load(ctx, p, agentID)
	if err != nil {
		return "", err
	}
	if tail <= 0 {
		tail = DefaultLogsTail
	}
	if tail > MaxLogsTail {
		tail = MaxLogsTail
	}
	res, err := s.d.Runners.Hub().Do(ctx, row.RunnerID, spec.Command{Op: spec.OpLogs, Name: row.ContainerName, Tail: tail})
	if err != nil {
		return "", err
	}
	if err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		return s.d.Audit.Record(ctx, q, audit.Entry{
			ActorID: principalID(ctx), Action: ActionLogs, TargetKind: audit.TargetAgent, TargetID: row.AgentID,
			Detail: map[string]any{"runner_id": row.RunnerID, "container": row.ContainerName, "tail": tail},
		})
	}); err != nil {
		return "", err
	}
	return res.Logs, nil
}

// Delete removes the AI's container (and, with volume, its workspace volume), revokes the AI's
// agent token and deletes the container row (CA-06, CA-08). A runner that is offline or does not
// answer keeps the row, so the container is never left running without a row. Afterwards the AI is
// deleted too when the caller may delete it in its home organization (Agents.Delete, POS-05); a
// caller who may operate the container but not delete the AI keeps the AI, without a container.
func (s *Service) Delete(ctx context.Context, p authn.Principal, agentID string, volume bool) error {
	row, err := s.load(ctx, p, agentID)
	if err != nil {
		return err
	}
	if _, err := s.d.Runners.Hub().Do(ctx, row.RunnerID, spec.Command{
		Op: spec.OpRemove, Name: row.ContainerName, RemoveVolume: volume,
	}); err != nil {
		var opErr *runners.OpError
		if !errors.As(err, &opErr) {
			// Offline, timed out or invalid: the container may still be there, so the row stays.
			s.setLastError(ctx, row.AgentID, err)
			return err
		}
		// Docker says there is nothing to remove: the container is already gone.
	}
	ownerID, tokenID, err := s.removeRow(ctx, p, row, volume)
	if err != nil {
		return err
	}
	if tokenID != "" && s.d.Bus != nil {
		s.d.Bus.Publish(events.SessionsRevoked{UserID: ownerID, TokenIDs: []string{tokenID}})
	}
	if err := s.d.Agents.Delete(ctx, p, agentID); err != nil && !errors.Is(err, access.ErrForbidden) &&
		!errors.Is(err, access.ErrNotFound) {
		s.d.Logger.WarnContext(ctx, "container: the AI was kept after its container was deleted",
			"agent_id", agentID, "err", err)
	}
	return nil
}

// removeRow revokes the AI's token, deletes the container row and audits the delete, in one
// transaction. It returns the AI's owner and the revoked token id for the announcement after the
// commit.
func (s *Service) removeRow(ctx context.Context, p authn.Principal, row db.ContainerAgent, volume bool) (string, string, error) {
	var ownerID, tokenID string
	now := s.d.Now()
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		ai, err := q.GetAgent(ctx, row.AgentID)
		if err != nil {
			return err
		}
		ownerID = ai.OwnerID
		if row.TokenID.Valid {
			tokenID = row.TokenID.String
			if _, err := q.RevokeToken(ctx, db.RevokeTokenParams{
				RevokedAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, ID: tokenID, UserID: ownerID,
			}); err != nil {
				return err
			}
		}
		if err := q.DeleteContainerAgent(ctx, row.AgentID); err != nil {
			return err
		}
		return s.d.Audit.Record(ctx, q, audit.Entry{
			ActorID: p.IID(), Action: ActionDelete, TargetKind: audit.TargetAgent, TargetID: row.AgentID,
			Detail: map[string]any{
				"runner_id": row.RunnerID, "container": row.ContainerName, "volume": volume,
			},
		})
	})
	return ownerID, tokenID, err
}

// RemoveOwner removes every container of the owner's runners, best effort, before the account is
// deleted (CA-06): the account deletion cascades to the runners, whose RESTRICT to
// container_agents would otherwise refuse it. A runner that is offline or fails is logged; its
// row and token go too, so the account deletion always proceeds. The AIs themselves stay, as the
// account deletion rehomes them (POS-06).
func (s *Service) RemoveOwner(ctx context.Context, ownerID string) error {
	rows, err := s.d.Store.R().ListContainerAgentsByOwner(ctx, ownerID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := s.d.Runners.Hub().Do(ctx, row.RunnerID, spec.Command{
			Op: spec.OpRemove, Name: row.ContainerName, RemoveVolume: true,
		}); err != nil {
			s.d.Logger.WarnContext(ctx, "container: account deleted with a container left on its runner",
				"agent_id", row.AgentID, "runner_id", row.RunnerID, "err", err)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	now := s.d.Now()
	return s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		for _, row := range rows {
			ai, err := q.GetAgent(ctx, row.AgentID)
			if errors.Is(err, sql.ErrNoRows) {
				continue // the AI went with an earlier step; its row goes with it
			}
			if err != nil {
				return err
			}
			if row.TokenID.Valid {
				if _, err := q.RevokeToken(ctx, db.RevokeTokenParams{
					RevokedAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true},
					ID:        row.TokenID.String, UserID: ai.OwnerID,
				}); err != nil {
					return err
				}
			}
			if err := q.DeleteContainerAgent(ctx, row.AgentID); err != nil {
				return err
			}
			if err := s.d.Audit.Record(ctx, q, audit.Entry{
				ActorID: ownerID, Action: ActionDelete, TargetKind: audit.TargetAgent, TargetID: row.AgentID,
				Detail: map[string]any{
					"runner_id": row.RunnerID, "container": row.ContainerName, "volume": true, "by": "account_delete",
				},
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// load returns the container of an AI the caller manages (CA-07). An AI the caller cannot see is
// access.ErrNotFound, one they see but do not manage access.ErrForbidden, and a visible AI without
// a container ErrNotFound.
func (s *Service) load(ctx context.Context, p authn.Principal, agentID string) (db.ContainerAgent, error) {
	if _, err := s.d.Agents.Manageable(ctx, p, agentID); err != nil {
		return db.ContainerAgent{}, err
	}
	row, err := s.d.Store.R().GetContainerAgent(ctx, agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ContainerAgent{}, ErrNotFound
	}
	return row, err
}

// setDesired writes what the owner wants the container to be.
func (s *Service) setDesired(ctx context.Context, agentID, desired string) error {
	return s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetContainerAgentDesired(ctx, db.SetContainerAgentDesiredParams{
			Desired: desired, UpdatedAt: s.d.Now().UnixMilli(), AgentID: agentID,
		})
	})
}

// setLastError records why the last operation failed (CA-06). The message never carries a secret:
// the errors of the protocol name fields, ops and Docker's own reason only (CA-08).
func (s *Service) setLastError(ctx context.Context, agentID string, err error) {
	msg := errorText(err)
	if msg == "" {
		msg = "the operation failed"
	}
	writeErr := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		return q.SetContainerAgentLastError(ctx, db.SetContainerAgentLastErrorParams{
			LastError: sql.NullString{String: msg, Valid: true}, UpdatedAt: s.d.Now().UnixMilli(), AgentID: agentID,
		})
	})
	if writeErr != nil {
		s.d.Logger.WarnContext(ctx, "container: could not record last_error", "agent_id", agentID, "err", writeErr)
	}
}

// rotateToken replaces the AI's agent token with a fresh one and returns its secret, for a
// container that has to be created again (CA-04). The old token is revoked and its sessions are
// announced after the commit.
func (s *Service) rotateToken(ctx context.Context, row db.ContainerAgent) (string, db.ContainerAgent, error) {
	var (
		secret  string
		ownerID string
		oldID   string
	)
	now := s.d.Now()
	err := s.d.Store.WithTx(ctx, func(q *db.Queries) error {
		ai, err := q.GetAgent(ctx, row.AgentID)
		if err != nil {
			return err
		}
		ownerID = ai.OwnerID
		if row.TokenID.Valid {
			oldID = row.TokenID.String
			if _, err := q.RevokeToken(ctx, db.RevokeTokenParams{
				RevokedAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, ID: oldID, UserID: ownerID,
			}); err != nil {
				return err
			}
		}
		profile, err := q.GetIntelligence(ctx, row.AgentID)
		if err != nil {
			return err
		}
		tok, tokenSecret, err := s.d.Users.CreateTokenTx(ctx, q, ownerID, users.TokenInput{
			Kind: dto.TokenKindAgent, Name: tokenName(profile.DisplayName),
		})
		if err != nil {
			return err
		}
		secret = tokenSecret
		if err := q.SetContainerAgentToken(ctx, db.SetContainerAgentTokenParams{
			TokenID: sql.NullString{String: tok.ID, Valid: true}, UpdatedAt: now.UnixMilli(), AgentID: row.AgentID,
		}); err != nil {
			return err
		}
		row.TokenID = sql.NullString{String: tok.ID, Valid: true}
		return nil
	})
	if err != nil {
		return "", row, err
	}
	if oldID != "" && s.d.Bus != nil {
		s.d.Bus.Publish(events.SessionsRevoked{UserID: ownerID, TokenIDs: []string{oldID}})
	}
	return secret, row, nil
}

// freshEnv opens the container's secrets again and builds its environment, for a container that is
// created again (CA-08). A secret deleted since the create leaves out its variable, which the
// agent image reports as a missing credential instead of starting a session that cannot log in.
func (s *Service) freshEnv(ctx context.Context, row db.ContainerAgent, token string) (map[string]string, error) {
	env, err := s.storedEnv(row)
	if err != nil {
		return nil, err
	}
	ai, err := s.d.Store.R().GetAgent(ctx, row.AgentID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(env))
	for _, e := range env {
		ids = append(ids, e.SecretID)
	}
	values, err := s.d.Secrets.Open(ctx, ai.OwnerID, ids)
	if err != nil {
		return nil, err
	}
	mapped := make(map[string]string, len(env))
	for _, e := range env {
		if value, ok := values[e.SecretID]; ok {
			mapped[e.Var] = value
		}
	}
	repo, ref := row.RepoUrl.String, row.RepoRef.String
	in := CreateInput{Harness: row.Harness, RepoURL: repo, RepoRef: ref}
	return s.containerEnv(in, ref, token, mapped), nil
}

// containerEnv builds the environment of the container: the Partyline variables first, then the
// owner's mapped secrets (CA-04). A variable of a mapping never overrides one of Partyline's.
func (s *Service) containerEnv(in CreateInput, ref, token string, mapped map[string]string) map[string]string {
	env := map[string]string{
		EnvURL:     s.d.PublicURL,
		EnvToken:   token,
		EnvHarness: in.Harness,
	}
	if in.RepoURL != "" {
		if ref == "" {
			ref = DefaultRepoRef
		}
		env[EnvRepo] = in.RepoURL
		env[EnvRepoRef] = ref
	}
	for name, value := range mapped {
		if reservedEnv[name] {
			continue
		}
		env[name] = value
	}
	return env
}

// runSpec builds the run command of a container row and its environment.
func (s *Service) runSpec(row db.ContainerAgent, env map[string]string) spec.RunSpec {
	return spec.RunSpec{
		Name: row.ContainerName, Image: row.Image, Volume: row.Volume, CPUs: row.Cpus, MemoryMB: row.MemoryMb,
		Labels: map[string]string{AgentLabel: row.AgentID}, Env: env,
	}
}

// resolveSecrets turns the requested secret names into the owner's secret ids (CA-07: another
// owner's name is not found) and returns the values opened by id. The mapped values are returned
// separately for validation, and the ids for the stored row.
func (s *Service) resolveSecrets(ctx context.Context, ownerID string, env []EnvVar) (map[string]string, []EnvVar, error) {
	if len(env) == 0 {
		return map[string]string{}, nil, nil
	}
	names, err := s.d.Secrets.ListNames(ctx, ownerID)
	if err != nil {
		return nil, nil, err
	}
	byName := make(map[string]string, len(names))
	for _, sec := range names {
		byName[sec.Name] = sec.ID
	}
	fields := map[string]string{}
	ids := make([]EnvVar, len(env))
	idList := make([]string, 0, len(env))
	for i, e := range env {
		id, ok := byName[e.SecretName]
		if !ok {
			fields[fmt.Sprintf("env[%d].secret_name", i)] = "no secret of yours has that name"
			continue
		}
		ids[i] = EnvVar{Var: e.Var, SecretName: id}
		idList = append(idList, id)
	}
	if len(fields) > 0 {
		return nil, nil, &spec.ValidationError{Fields: fields}
	}
	values, err := s.d.Secrets.Open(ctx, ownerID, idList)
	if err != nil {
		return nil, nil, err
	}
	mapped := make(map[string]string, len(env))
	for i, e := range env {
		value, ok := values[ids[i].SecretName]
		if !ok {
			return nil, nil, &spec.ValidationError{Fields: map[string]string{
				fmt.Sprintf("env[%d].secret_name", i): "the secret is no longer available",
			}}
		}
		mapped[e.Var] = value
	}
	return mapped, ids, nil
}

// view reads the container as the API shows it: the row, the AI's profile name and handle, and
// the names of the secrets its environment maps (never their values, CA-08).
func (s *Service) view(ctx context.Context, row db.ContainerAgent) (Container, error) {
	// Read the row again: a state event may have arrived while the operation ran, and the answer
	// should show what the runner last reported.
	if fresh, err := s.d.Store.R().GetContainerAgent(ctx, row.AgentID); err == nil {
		row = fresh
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Container{}, err
	}
	out := Container{
		AgentID: row.AgentID, RunnerID: row.RunnerID, Harness: row.Harness, Image: row.Image,
		RepoURL: row.RepoUrl.String, RepoRef: row.RepoRef.String, Volume: row.Volume,
		ContainerName: row.ContainerName, CPUs: row.Cpus, MemoryMB: row.MemoryMb,
		Desired: row.Desired, State: row.State, LastError: row.LastError.String,
		CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
	}
	env, err := s.storedEnv(row)
	if err != nil {
		return Container{}, err
	}
	ai, err := s.d.Store.R().GetAgent(ctx, row.AgentID)
	if err != nil {
		return Container{}, err
	}
	names, err := s.d.Secrets.ListNames(ctx, ai.OwnerID)
	if err != nil {
		return Container{}, err
	}
	byID := make(map[string]string, len(names))
	for _, sec := range names {
		byID[sec.ID] = sec.Name
	}
	out.Env = make([]EnvVar, len(env))
	for i, e := range env {
		out.Env[i] = EnvVar{Var: e.Var, SecretName: byID[e.SecretID]}
	}
	profile, err := s.d.Store.R().GetIntelligence(ctx, row.AgentID)
	if err != nil {
		return Container{}, err
	}
	out.DisplayName, out.Handle = profile.DisplayName, profile.Handle
	return out, nil
}

// storedEnvVar is one entry of the row's env mapping: the variable and the id of the owner's
// secret it reads (CA-03). The value itself is never stored here.
type storedEnvVar struct {
	Var      string `json:"var"`
	SecretID string `json:"secret_id"`
}

// storedEnv decodes the [{var, secret_id}] mapping of the row.
func (s *Service) storedEnv(row db.ContainerAgent) ([]storedEnvVar, error) {
	if row.Env == "" || row.Env == "[]" {
		return nil, nil
	}
	var stored []storedEnvVar
	if err := json.Unmarshal([]byte(row.Env), &stored); err != nil {
		return nil, fmt.Errorf("container: decode env of %s: %w", row.AgentID, err)
	}
	return stored, nil
}

// marshalEnv encodes the [{var, secret_id}] mapping the row stores.
func marshalEnv(env []EnvVar) string {
	if len(env) == 0 {
		return "[]"
	}
	stored := make([]storedEnvVar, len(env))
	for i, e := range env {
		stored[i].Var, stored[i].SecretID = e.Var, e.SecretName
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return "[]" // strings and structs only: cannot fail
	}
	return string(data)
}

// announceOrg tells the app that an organization has a new AI, after the commit.
func (s *Service) announceOrg(agent db.Agent) {
	if s.d.Bus != nil && agent.OrgID.Valid {
		s.d.Bus.Publish(events.OrgChanged{OrgID: agent.OrgID.String})
	}
}

// validateCreate checks a create request with the validators of the runner protocol (CA-09) and
// fills the defaults; every error names its field and never a value (CA-08).
func validateCreate(in CreateInput) (CreateInput, error) {
	fields := map[string]string{}
	in.Harness = strings.ToLower(strings.TrimSpace(in.Harness))
	if !harnesses[in.Harness] {
		fields["harness"] = "must be one of claude, opencode, pi, omp"
	}
	in.DisplayName = intelligences.CleanDisplayName(in.DisplayName)
	if in.DisplayName == "" {
		fields["display_name"] = "must have 1 to 64 characters"
	}
	in.Handle = strings.TrimSpace(in.Handle)
	if handle, err := handles.Validate(in.Handle); err != nil {
		fields["handle"] = err.Error()
	} else {
		in.Handle = handle
	}
	in.RepoURL = strings.TrimSpace(in.RepoURL)
	in.RepoRef = strings.TrimSpace(in.RepoRef)
	if in.RepoURL == "" && in.RepoRef != "" {
		fields["repo_url"] = "is required when repo_ref is set"
	}
	if in.RepoURL != "" && !spec.ValidRepoURL(in.RepoURL) {
		fields["repo_url"] = "must be an https:// or ssh:// git repository URL"
	}
	if in.RepoURL != "" && in.RepoRef == "" {
		in.RepoRef = DefaultRepoRef
	}
	if in.RepoRef != "" && !spec.ValidRef(in.RepoRef) {
		fields["repo_ref"] = "must be a branch, tag or commit of the repository"
	}
	if in.CPUs == 0 {
		in.CPUs = DefaultCPUs
	}
	if in.MemoryMB == 0 {
		in.MemoryMB = DefaultMemoryMB
	}
	if !spec.ValidCPUs(in.CPUs) {
		fields["cpus"] = "must be between 0.1 and 64"
	}
	if !spec.ValidMemoryMB(in.MemoryMB) {
		fields["memory_mb"] = "must be between 64 and 65536"
	}
	if len(in.Env) > spec.MaxEnvVars {
		fields["env"] = fmt.Sprintf("at most %d environment variables", spec.MaxEnvVars)
	}
	seen := make(map[string]bool, len(in.Env))
	for i, e := range in.Env {
		e.Var = strings.TrimSpace(e.Var)
		if !spec.ValidEnvName(e.Var) {
			fields[fmt.Sprintf("env[%d].var", i)] = "must match " + spec.EnvNamePattern.String()
		}
		if reservedEnv[e.Var] {
			fields[fmt.Sprintf("env[%d].var", i)] = "is set by Partyline and cannot be mapped"
		}
		if seen[e.Var] {
			fields[fmt.Sprintf("env[%d].var", i)] = "is mapped twice"
		}
		seen[e.Var] = true
		e.SecretName = strings.TrimSpace(e.SecretName)
		if !ownersecrets.ValidName(e.SecretName) {
			fields[fmt.Sprintf("env[%d].secret_name", i)] = "must be the name of one of your secrets"
		}
		in.Env[i] = e
	}
	if len(fields) > 0 {
		return CreateInput{}, &spec.ValidationError{Fields: fields}
	}
	return in, nil
}

// validateMapped checks the values of the mapped secrets against the protocol limits, without
// echoing any value (CA-08).
func validateMapped(mapped map[string]string) error {
	fields := map[string]string{}
	for name, value := range mapped {
		if len(value) > spec.MaxEnvValue || strings.ContainsRune(value, 0) {
			fields["env"] = fmt.Sprintf("the value of %s must have at most %d bytes and no NUL", name, spec.MaxEnvValue)
			break
		}
	}
	if len(fields) > 0 {
		return &spec.ValidationError{Fields: fields}
	}
	return nil
}

// tokenName is the name the account's token list shows for an AI's token.
func tokenName(displayName string) string {
	name := "container: " + displayName
	if utf8.RuneCountInString(name) > maxTokenName {
		name = strings.TrimSpace(string([]rune(name)[:maxTokenName]))
	}
	return name
}

// errorText is err as last_error may store it: no control characters besides tab and line feed,
// at most spec.MaxMessage characters. Protocol errors name fields and Docker's reason, never an
// environment variable value (CA-08).
func errorText(err error) string {
	var verr *spec.ValidationError
	if errors.As(err, &verr) {
		return verr.Error()
	}
	s := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, err.Error())
	if utf8.RuneCountInString(s) > spec.MaxMessage {
		s = strings.TrimSpace(string([]rune(s)[:spec.MaxMessage]))
	}
	return s
}

// principalID is the acting identity of ctx, or "" outside a request.
func principalID(ctx context.Context) string {
	if p, ok := authn.PrincipalFrom(ctx); ok {
		return p.IID()
	}
	return ""
}

// nullString is s as the nullable column the store keeps.
func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
