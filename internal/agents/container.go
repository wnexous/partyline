package agents

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/wnexous/partyline/internal/audit"
	"github.com/wnexous/partyline/internal/authn"
	"github.com/wnexous/partyline/internal/handles"
	"github.com/wnexous/partyline/internal/intelligences"
	"github.com/wnexous/partyline/internal/orgs"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
	"github.com/wnexous/partyline/internal/validation"
)

// Container machine and folder.
const (
	// ContainerMachinePrefix starts the machine of a container AI: container:<container name>
	// (CA-04). A runner runs many containers, so the machine is the container's own name and not the
	// runner's: two container AIs of one runner without a repository would otherwise share the
	// machine runner:<id> and the folder /workspace, and the agents_folder unique index would refuse
	// the second one. It is not a host any session reports, so a container AI never collides with
	// the AI of a folder.
	ContainerMachinePrefix = "container:"
	// ContainerRepoFolder and ContainerFolder are the folders of a container AI: the cloned
	// repository when it has one, else the bare workspace (CA-04, CA-05).
	ContainerRepoFolder = "/workspace/repo"
	ContainerRootFolder = "/workspace"
)

// ContainerIdentity describes the AI a container runs (CA-04): where it lives, its app and the
// profile its owner asked for.
type ContainerIdentity struct {
	// ID is the AI's id. The caller makes it before the transaction so that the container name and
	// so the machine of the AI are known before the row exists; empty takes a fresh one.
	ID string
	// ContainerName is the Docker name of the AI's container (pl-<short id>, unique among the
	// owner's containers) and the AI's machine: each container is an identity of its own.
	ContainerName string
	// Harness is the app the container runs (claude, pi, opencode, omp).
	Harness string
	// Repo reports whether the workspace is a cloned repository.
	Repo bool
	// DisplayName is the profile name; Handle is the requested @handle, which must be free.
	DisplayName, Handle string
}

// ContainerMachine is the machine of the AI a container runs (CA-04): its container name, so that
// every container of one runner is a distinct identity.
func ContainerMachine(containerName string) string { return ContainerMachinePrefix + containerName }

// ContainerFolderFor is the folder of a container AI: /workspace/repo with a repository,
// /workspace without one (CA-04).
func ContainerFolderFor(repo bool) string {
	if repo {
		return ContainerRepoFolder
	}
	return ContainerRootFolder
}

// CreateContainerIdentity creates the AI of a container inside the caller's write transaction, so
// that the AI, its token and its container_agents row commit together (CA-04): the machine is
// container:<container name>, the folder /workspace/repo or /workspace, the app the harness, and the
// profile the requested display name and handle. It joins an organization and gets a creator as the first
// session of a folder does (place, ORG-04), is audited like one, and stops at MaxAgentsPerOwner.
// A handle another intelligence has is intelligences.ErrHandleTaken (409) and an invalid one is a
// validation error; nothing is created then.
func (r *Registry) CreateContainerIdentity(ctx context.Context, q *db.Queries, ownerID string, in ContainerIdentity) (db.Agent, error) {
	handle, err := handles.Validate(in.Handle)
	if err != nil {
		return db.Agent{}, &validation.Error{Fields: map[string]string{"handle": err.Error()}}
	}
	display := intelligences.CleanDisplayName(in.DisplayName)
	if display == "" {
		return db.Agent{}, &validation.Error{Fields: map[string]string{"display_name": "must have 1 to 64 characters"}}
	}
	taken, err := q.CountIntelligenceHandle(ctx, handle)
	if err != nil {
		return db.Agent{}, err
	}
	if taken > 0 {
		return db.Agent{}, intelligences.ErrHandleTaken
	}
	count, err := q.CountAgentsByOwner(ctx, ownerID)
	if err != nil {
		return db.Agent{}, err
	}
	if count >= MaxAgentsPerOwner {
		return db.Agent{}, ErrAgentLimit
	}
	now := r.now()
	name := strings.TrimSpace(in.ContainerName)
	if name == "" {
		return db.Agent{}, errors.New("agents: a container identity needs its container name")
	}
	machine := ContainerMachine(name)
	folder := ContainerFolderFor(in.Repo)
	where, err := place(ctx, q, ownerID, FolderID{Machine: machine, Folder: folder, Harness: in.Harness})
	if err != nil {
		return db.Agent{}, err
	}
	id := in.ID
	if id == "" {
		id = store.NewID()
	}
	if err := q.InsertFolderAgent(ctx, db.InsertFolderAgentParams{
		ID: id, OwnerID: ownerID, Name: id, CreatedAt: now.UnixMilli(), Harness: HarnessID(in.Harness),
		Machine: machine, Folder: folder,
		OrgID: sql.NullString{String: where.orgID, Valid: true},
		CreatorIid: sql.NullString{
			String: where.creatorIID, Valid: where.creatorIID != "",
		},
	}); err != nil {
		return db.Agent{}, err
	}
	if _, err := intelligences.Create(ctx, q, intelligences.NewParams{
		ID: id, Kind: handles.AI, DisplayName: display, HandleBase: handle, Now: now,
	}); err != nil {
		return db.Agent{}, err
	}
	if err := orgs.AddMember(ctx, q, orgs.AddMemberInput{
		OrgID: where.orgID, IID: id, Permission: where.permission, PositionID: where.positionID,
		InviterIID: where.inviterIID, Now: now,
	}); err != nil {
		return db.Agent{}, err
	}
	if r.audit != nil {
		detail := map[string]any{"iid": id, "by": "container", "permission": string(where.permission)}
		if where.positionID != "" {
			detail["position_id"] = where.positionID
		}
		if err := r.audit.Record(ctx, q, audit.Entry{
			ActorID: ownerID, Action: audit.ActionOrgMemberJoin, TargetKind: audit.TargetOrganization,
			TargetID: where.orgID, Detail: detail,
		}); err != nil {
			return db.Agent{}, err
		}
	}
	return q.GetAgent(ctx, id)
}

// Manageable returns the AI when p manages it in an organization they share (POS-04): whoever
// cannot see it gets access.ErrNotFound, whoever sees it and does not manage it
// access.ErrForbidden. The container service decides every container operation with it (CA-07).
func (r *Registry) Manageable(ctx context.Context, p authn.Principal, agentID string) (db.Agent, error) {
	return r.managedAgent(ctx, p, agentID)
}
