package agents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/store"
	"github.com/wnexous/partyline/internal/store/db"
)

// Limits of identity fields sent by sessions.
const (
	// MaxMachineLength is the longest machine name kept, in characters.
	MaxMachineLength = 64
	// MaxProjectNameLength is the longest project name, in characters (PROJ-05).
	MaxProjectNameLength = dto.MaxProjectName
	// DefaultHarness is the harness of sessions that name none, or an unknown one.
	DefaultHarness = "agent"
)

// harnessLabels are the readable names of the known harnesses (ID-01). The keys are the
// launcher ids (`partyline <harness>`).
var harnessLabels = map[string]string{
	"claude":   "Claude Code",
	"opencode": "OpenCode",
	"pi":       "pi",
	"omp":      "oh-my-pi",
	"codex":    "Codex",
	"gemini":   "Gemini",
	"agent":    "Agent",
}

// harnessAliases map other launcher names to their harness id.
var harnessAliases = map[string]string{"claudecode": "claude", "claude-code": "claude"}

// HarnessID returns the harness id for a header value: a known id or alias, else "agent".
func HarnessID(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if id, ok := harnessAliases[v]; ok {
		return id
	}
	if _, ok := harnessLabels[v]; ok {
		return v
	}
	return DefaultHarness
}

// HarnessLabel is the readable name of a harness id ("Claude Code"); unknown ids read "Agent".
func HarnessLabel(id string) string {
	if l, ok := harnessLabels[id]; ok {
		return l
	}
	return harnessLabels[DefaultHarness]
}

// AutoName is the readable name of a temporary agent: "<Project> · <Harness label>", plus
// " #N" from the second session of the same project and harness on (ID-01).
func AutoName(projectName, harness string, n int64) string {
	name := projectName + " · " + HarnessLabel(harness)
	if n >= 2 {
		name += fmt.Sprintf(" #%d", n)
	}
	return name
}

// cleanText trims s, drops control characters and cuts it to limit characters.
func cleanText(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > limit {
		s = strings.TrimSpace(string([]rune(s)[:limit]))
	}
	return s
}

// ProjectName is the name a new project gets from what the session sent: the text without
// control characters, cut to MaxProjectNameLength, with an upper-case first letter; the slug
// when nothing is left.
func ProjectName(raw, slug string) string {
	name := cleanText(raw, MaxProjectNameLength)
	if name == "" {
		name = slug
	}
	r, size := utf8.DecodeRuneInString(name)
	return string(unicode.ToUpper(r)) + name[size:]
}

// ProjectFromName is the project slug an agent name derives: the name without a trailing
// "-N" (N from 2 to 99) of the old unique mode; "" for the default name "agent" (the rule of
// migration 0011).
func ProjectFromName(name string) string {
	if name == DefaultName {
		return ""
	}
	if i := strings.LastIndexByte(name, '-'); i > 0 {
		if n, err := strconv.Atoi(name[i+1:]); err == nil && n >= 2 && n <= 99 && name[i+1] != '0' {
			return name[:i]
		}
	}
	return name
}

// Identity is what a session tells the server about itself on the agent WebSocket and on
// POST /agent/register.
type Identity struct {
	// Name is X-Partyline-Agent: an explicit name (--name), or the name the server assigned
	// earlier when Mode is temporary. Empty asks for a temporary name.
	Name string
	// Mode is X-Partyline-Agent-Mode: unique (old clients' derived names), temporary
	// (reclaiming an assigned temporary name) or empty.
	Mode string
	// Project, Harness and Machine are X-Partyline-Project, -Harness and -Machine.
	Project string
	Harness string
	Machine string
}

// IdentityFromHeader reads a session's identity headers.
func IdentityFromHeader(h http.Header) Identity {
	return Identity{
		Name: h.Get(dto.HeaderAgent), Mode: h.Get(dto.HeaderAgentMode), Project: h.Get(dto.HeaderProject),
		Harness: h.Get(dto.HeaderHarness), Machine: h.Get(dto.HeaderMachine),
	}
}

func (id Identity) project() string { return cleanText(id.Project, MaxProjectNameLength) }

// Allocates reports whether the server picks the agent name, so concurrent sessions must
// be serialized until the chosen agent is online.
func (id Identity) Allocates() bool {
	return id.Mode == dto.AgentModeUnique || (id.project() != "" && strings.TrimSpace(id.Name) == "")
}

// identityPlan is the agent a session resolves to and what to record on it.
type identityPlan struct {
	name string
	// kind of a new agent; forceKind, when set, also applies to an existing one.
	newKind, forceKind dto.AgentKind
	// sessionNo is the N of a temporary name (0 keeps the stored one); newSessionNo applies
	// only to a new agent.
	sessionNo, newSessionNo int64
	// projectSlug/projectRaw name the project; forceProject sets it on an existing agent that
	// already has one (new clients), else only agents without a project get it (old clients).
	projectSlug, projectRaw string
	forceProject            bool
}

// temporaryBase is "<project>-<harness>", the project shortened so it fits MaxNameLength.
func temporaryBase(projectSlug, harness string) string {
	if room := MaxNameLength - len(harness) - 1; len(projectSlug) > room {
		projectSlug = strings.TrimRight(projectSlug[:room], "-._")
	}
	return projectSlug + "-" + harness
}

// sessionNumber is N when name is suffixedName(base, N), else 1.
func sessionNumber(name, base string) int64 {
	for n := 2; n <= MaxAgentsPerOwner; n++ {
		if suffixedName(base, n) == name {
			return int64(n)
		}
	}
	return 1
}

// Resolve returns the owner's agent for a connecting session, creating it when needed (the
// per-owner cap applies to new agents), and records its project, harness and machine
// (ID-01, ID-02). It unarchives the agent. Callers that Allocate hold a lock until the agent
// is online, so two sessions never pick the same name.
//
// With X-Partyline-Project (current clients): no name → a temporary agent
// "<project>-<harness>[-N]", the lowest N that is new or an offline temporary agent; a name
// in temporary mode → that temporary agent again (created when it is gone); any other name →
// the fixed agent of that name. Without it (clients from before projects): unique mode keeps
// the old "<name>[-N]" choice (first new or offline name; new agents are temporary in project
// <name>), and a plain name keeps "newest wins" on that agent (a new one is fixed, in the
// project its name derives).
func (r *Registry) Resolve(ctx context.Context, ownerID string, id Identity) (db.Agent, error) {
	harness := HarnessID(id.Harness)
	plan := identityPlan{}
	project := id.project()
	switch {
	case project != "" && strings.TrimSpace(id.Name) == "":
		slug := Slug(project)
		base := temporaryBase(slug, harness)
		name, n, err := r.freeName(ctx, ownerID, base, true)
		if err != nil {
			return db.Agent{}, err
		}
		plan = identityPlan{name: name, newKind: dto.AgentKindTemporary, forceKind: dto.AgentKindTemporary, sessionNo: n,
			projectSlug: slug, projectRaw: project, forceProject: true}
	case project != "" && id.Mode == dto.AgentModeTemporary:
		slug := Slug(project)
		name := Slug(id.Name)
		plan = identityPlan{name: name, newKind: dto.AgentKindTemporary, newSessionNo: sessionNumber(name, temporaryBase(slug, harness)),
			projectSlug: slug, projectRaw: project, forceProject: true}
	case project != "":
		slug := Slug(project)
		plan = identityPlan{name: Slug(id.Name), newKind: dto.AgentKindFixed, forceKind: dto.AgentKindFixed,
			projectSlug: slug, projectRaw: project, forceProject: true}
	case id.Mode == dto.AgentModeUnique:
		base := Slug(id.Name)
		name, n, err := r.freeName(ctx, ownerID, base, false)
		if err != nil {
			return db.Agent{}, err
		}
		plan = identityPlan{name: name, newKind: dto.AgentKindTemporary, sessionNo: n}
		if base != DefaultName {
			plan.projectSlug, plan.projectRaw = base, base
		}
	default:
		name := Slug(id.Name)
		slug := ProjectFromName(name)
		plan = identityPlan{name: name, newKind: dto.AgentKindFixed, projectSlug: slug, projectRaw: slug}
	}
	var out db.Agent
	err := r.store.WithTx(ctx, func(q *db.Queries) error {
		a, created, err := r.getOrCreate(ctx, q, ownerID, plan.name)
		if err != nil {
			return err
		}
		out, err = r.record(ctx, q, a, created, plan, id)
		return err
	})
	return out, err
}

// getOrCreate returns the owner's agent named name, creating it unless the owner already has
// MaxAgentsPerOwner agents (ErrAgentLimit).
func (r *Registry) getOrCreate(ctx context.Context, q *db.Queries, ownerID, name string) (db.Agent, bool, error) {
	a, err := q.GetAgentByName(ctx, db.GetAgentByNameParams{OwnerID: ownerID, Name: name})
	if !errors.Is(err, sql.ErrNoRows) {
		return a, false, err
	}
	n, err := q.CountAgentsByOwner(ctx, ownerID)
	if err != nil {
		return db.Agent{}, false, err
	}
	if n >= MaxAgentsPerOwner {
		return db.Agent{}, false, ErrAgentLimit
	}
	if err := q.InsertAgentIfMissing(ctx, db.InsertAgentIfMissingParams{
		ID: store.NewID(), OwnerID: ownerID, Name: name, CreatedAt: r.now().UnixMilli(),
	}); err != nil {
		return db.Agent{}, false, err
	}
	a, err = q.GetAgentByName(ctx, db.GetAgentByNameParams{OwnerID: ownerID, Name: name})
	return a, true, err
}

// record stores the session's project, harness, machine and kind on the agent, recomputes the
// readable name of a temporary agent and unarchives it.
func (r *Registry) record(ctx context.Context, q *db.Queries, a db.Agent, created bool, plan identityPlan, id Identity) (db.Agent, error) {
	if created {
		a.Kind = string(plan.newKind)
	}
	if plan.forceKind != "" {
		a.Kind = string(plan.forceKind)
	}
	if created || strings.TrimSpace(id.Harness) != "" {
		a.Harness = HarnessID(id.Harness)
	}
	if m := cleanText(id.Machine, MaxMachineLength); m != "" {
		a.Machine = m
	}
	if plan.projectSlug != "" && (plan.forceProject || !a.ProjectID.Valid) {
		p, err := r.ensureProject(ctx, q, a.OwnerID, plan.projectSlug, plan.projectRaw)
		if err != nil {
			return db.Agent{}, err
		}
		a.ProjectID = sql.NullString{String: p.ID, Valid: true}
	}
	if plan.sessionNo > 0 {
		a.SessionNo = plan.sessionNo
	}
	if created && plan.newSessionNo > 0 {
		a.SessionNo = plan.newSessionNo
	}
	switch {
	case a.Kind == string(dto.AgentKindTemporary) && a.ProjectID.Valid:
		p, err := q.GetProject(ctx, a.ProjectID.String)
		if err != nil {
			return db.Agent{}, err
		}
		a.AutoName = AutoName(p.Name, a.Harness, a.SessionNo)
	case plan.forceKind == dto.AgentKindFixed:
		a.AutoName = "" // an explicit name is what people read
	}
	a.ArchivedAt = sql.NullInt64{}
	err := q.UpdateAgentIdentity(ctx, db.UpdateAgentIdentityParams{
		ProjectID: a.ProjectID, Harness: a.Harness, Machine: a.Machine, Kind: a.Kind,
		AutoName: a.AutoName, SessionNo: a.SessionNo, ID: a.ID,
	})
	return a, err
}

// ensureProject returns the owner's project with slug, creating it named after raw.
func (r *Registry) ensureProject(ctx context.Context, q *db.Queries, ownerID, slug, raw string) (db.Project, error) {
	if err := q.CreateProjectIfMissing(ctx, db.CreateProjectIfMissingParams{
		ID: store.NewID(), OwnerID: ownerID, Slug: slug, Name: ProjectName(raw, slug), CreatedAt: r.now().UnixMilli(),
	}); err != nil {
		return db.Project{}, err
	}
	return q.GetProjectBySlug(ctx, db.GetProjectBySlugParams{OwnerID: ownerID, Slug: slug})
}

// freeName returns the first of base, base-2 … base-MaxAgentsPerOwner that is new or an
// offline agent (an offline temporary agent when temporaryOnly), with its N. When none is,
// the owner has reached the agent cap (ErrAgentLimit).
func (r *Registry) freeName(ctx context.Context, ownerID, base string, temporaryOnly bool) (string, int64, error) {
	for n := 1; n <= MaxAgentsPerOwner; n++ {
		name := suffixedName(base, n)
		a, err := r.store.R().GetAgentByName(ctx, db.GetAgentByNameParams{OwnerID: ownerID, Name: name})
		if errors.Is(err, sql.ErrNoRows) {
			return name, int64(n), nil
		}
		if err != nil {
			return "", 0, err
		}
		if !r.presence.Online(a.ID) && (!temporaryOnly || a.Kind == string(dto.AgentKindTemporary)) {
			return name, int64(n), nil
		}
	}
	return "", 0, ErrAgentLimit
}

// suffixedName is base for n = 1, else base + "-n", with base shortened so the result fits
// MaxNameLength.
func suffixedName(base string, n int) string {
	if n <= 1 {
		return base
	}
	suffix := fmt.Sprintf("-%d", n)
	if len(base)+len(suffix) > MaxNameLength {
		base = strings.TrimRight(base[:MaxNameLength-len(suffix)], "-._")
	}
	return base + suffix
}

// MarkConnected stamps the agent's last connect (which picks a project's active session)
// and unarchives it (PROJ-02).
func (r *Registry) MarkConnected(ctx context.Context, agentID string) error {
	return r.store.WithTx(ctx, func(q *db.Queries) error {
		return q.MarkAgentConnected(ctx, db.MarkAgentConnectedParams{ConnectedAt: nullMillis(r.now()), ID: agentID})
	})
}
