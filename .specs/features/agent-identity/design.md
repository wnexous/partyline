# Agent Identity Design

**Spec**: `.specs/features/agent-identity/spec.md`
**Status**: Approved (user: "faz o que tu acha mais adequado")

---

## Data model (next migrations after the latest on main)

```sql
CREATE TABLE projects (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  slug TEXT NOT NULL, name TEXT NOT NULL, created_at INTEGER NOT NULL, UNIQUE(owner_id, slug));
ALTER TABLE agents ADD COLUMN project_id TEXT NULL REFERENCES projects(id) ON DELETE SET NULL;
ALTER TABLE agents ADD COLUMN harness TEXT NOT NULL DEFAULT 'agent';
ALTER TABLE agents ADD COLUMN machine TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN kind TEXT NOT NULL DEFAULT 'fixed' CHECK (kind IN ('fixed','temporary'));
ALTER TABLE agents ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN archived_at INTEGER NULL;
ALTER TABLE agents ADD COLUMN connected_at INTEGER NULL;   -- last connect, picks the active session
CREATE TABLE project_cursors (project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE, seq INTEGER NOT NULL, PRIMARY KEY(project_id, room_id));
-- room_members.kind gains 'project'; grants.resource_kind gains 'project' (SQLite: rebuild both tables with the new CHECK).
```
Existing agents migrate as `fixed`, project = their name's project when derivable, else NULL ("No project").

## Identity on connect
Channel/listen/tools-only send: `X-Partyline-Agent` (explicit name, only with `--name`), `X-Partyline-Project` (slug source), `X-Partyline-Harness`, `X-Partyline-Machine`. Without an explicit name the gateway registers a temporary agent: internal name `<project>-<harness>[-N]` (unique per owner; reuses the lowest free N whose agent is offline and temporary), display name `<Project> · <Harness label> [#N]`. Explicit names keep "newest wins". Unique-mode headers from harness-onboarding are replaced by this scheme (the name-sharing file for tools-only sessions keeps working with the assigned internal name).

## Delivery to project members
`delivery.Plan` returns `ProjectIDs` besides `AgentIDs`. The dispatcher resolves each project to its active session (online agent of that project with the latest `connected_at`), skips it if that agent is the author or already a direct member target (dedupe), applies the agent member's settings if the agent is also a direct member else the project member's settings (display name/signature/delivery live on the room member row as for agents). Acks from a session for a message delivered via a project advance `project_cursors`. Backlog on connect merges direct-agent cursors and project cursors (project rooms the agent's project belongs to).

## Archiving
Retention job: `UPDATE agents SET archived_at = now WHERE kind='temporary' AND archived_at IS NULL AND last_seen_at < now-24h AND NOT online`. Connect clears `archived_at`.

## API
- `GET /agents?include_archived=` → `Agent{…, project, harness, machine, kind, display_name, archived}`; `PATCH /agents/{id}` `{display_name?, fixed?}`.
- `GET /projects`, `PATCH /projects/{id}` `{name}`, `GET/PUT /projects/{id}/sharing` (use grants).
- Room members accept `kind: "project"`.

## CLI
`--name`, `--project` on `partyline <harness>`, `channel`, `listen`; home-folder warning; `partyline agents` (table grouped by project).

## Web
Agents page grouped by project; project rename; agent rename/promote; archived toggle; machine/harness filters; member picker lists projects ("Atlas (any session)") and fixed agents first.
