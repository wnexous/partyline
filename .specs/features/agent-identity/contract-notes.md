# Contract Notes (from agent-identity Phase 1: T1-T4)

Binding for Phase 2 (project delivery, T5-T7), Phase 3 (CLI and web, T8-T11) and the Verifier.
Types live in `internal/api/dto` (AD-003); `web/src/lib/api/types.gen.ts` is regenerated.
Migration `0011_agent_identity.sql`; the next migration is `0012`.

## Schema (PROJ-01, T1)
- `projects(id, owner_id, slug, name, created_at)`, `UNIQUE(owner_id, slug)`; deleting a project
  sets its agents' `project_id` NULL and deletes its `project_cursors`.
- Agent columns: `project_id` (NULL = no project), `harness` (default `agent`), `machine` (''),
  `kind` (`fixed|temporary`, CHECK, default `fixed`), `display_name` (the owner's rename, '' =
  none), `auto_name` (server-computed readable name of a temporary agent, '' otherwise),
  `session_no` (the N of a temporary name), `archived_at`, `connected_at`. Index
  `agents_project`. `auto_name` and `session_no` are beyond the design: a project rename
  recomputes `auto_name` without parsing names.
- `project_cursors(project_id, room_id, seq)` exists for T6; nothing writes it yet.
- `room_members.kind` and `grants.resource_kind` accept `project`: both tables were rebuilt
  (data, `UNIQUE(room_id, kind, member_id)`, the rooms FK, `room_members_member`,
  `grants_subject` kept); `message_views` was dropped and recreated around the rebuild.
- Backfill: existing agents stay `fixed` and join the project of their name without a trailing
  `-N` (N 2-99, the old unique-mode suffix): `proj-2` → `proj`, `web-12` → `web`, `v-1` stays
  `v-1`. The name `agent` gets no project. One project per (owner, slug), named with an
  upper-case first letter (`proj` → `Proj`). Go mirror: `agents.ProjectFromName`.

## Display names (ID-01, ID-04)
- Effective agent display name = owner's rename, else `auto_name`, else the agent name
  (`delivery.AgentDisplayName`, `agents.DisplayName`). `message_views.author_name` for agents =
  the member's room display name, else that effective name. `ListRoomMembers` names agent
  members by the effective name (member `name`; the member `display_name` override is separate)
  and project members by the project name; `owner` is set for both.
- `auto_name` = `AutoName(project name, harness, N)` = `<Project> · <Harness label>` plus
  ` #N` for N ≥ 2. Labels (`agents.HarnessLabel`): claude "Claude Code", opencode "OpenCode",
  pi "pi", omp "oh-my-pi", codex "Codex", gemini "Gemini", agent "Agent".
- Mentions: `delivery.AgentPrefs(agent, memberDisplay, …)` matches `@<member display name>`,
  `@<agent name>` and `@<agent display name>` (case-insensitive, word boundaries). The
  dispatcher, backlog and pending use it.

## DTO
- `Agent` adds `display_name`, `project?: ProjectRef{id, slug, name}` (absent without a
  project), `harness`, `machine`, `kind` (`AgentKind` "fixed" | "temporary"), `archived`.
- `AgentInfo` (welcome frame, `POST /agent/register`) adds `display_name`.
- `Project{id, slug, name, owner, my_level, created_at}`; `UpdateProjectRequest{name}`;
  `UpdateAgentRequest` adds `display_name`, `fixed`. `MaxAgentDisplayName` 64,
  `MaxProjectName` 64. `MemberKindProject` "project".
- Headers: `HeaderProject` `X-Partyline-Project`, `HeaderHarness` `X-Partyline-Harness`,
  `HeaderMachine` `X-Partyline-Machine`; `AgentModeTemporary` "temporary" (new value of
  `X-Partyline-Agent-Mode`).

## Identity on connect (ID-01, ID-02, T2) — `agents.Registry.Resolve`
Same rules on the agent WebSocket and on `POST /agent/register` (tools-only; the agent stays
offline and gets no `connected_at`). Identity = `X-Partyline-Agent` (principal `AgentName`),
`-Agent-Mode`, `-Project`, `-Harness`, `-Machine`.
- Harness: `agents.HarnessID`: claude, opencode, pi, omp, codex, gemini (case-insensitive;
  `claudecode`/`claude-code` → claude); anything else or none → `agent`. Machine: control
  characters dropped, trimmed, cut to 64 characters; an empty value keeps the stored one.
- Project header present (current clients; the value is trimmed, control characters dropped,
  cut to 64): slug = `agents.Slug(value)`; a new project is named after the value with an
  upper-case first letter (`my app` → slug `my-app`, name `My app`). An existing project keeps
  its name (a rename is never undone by a session).
  - No agent name → temporary agent `<slug>-<harness>[-N]` (slug shortened so the base fits 48
    characters): the lowest N (1 = no suffix) whose name is new or an **offline temporary**
    agent (offline fixed agents of that name are skipped). Its kind is set to temporary,
    project/harness/machine recorded, `auto_name` computed.
  - Name + mode `temporary` → that agent again (newest wins over a stale socket, close 4000);
    it keeps its kind (a promoted agent stays fixed); if it is gone, a new temporary agent of
    that name with N parsed from the name. This is how T7's channel must reconnect after the
    welcome assigned a temporary name (do not send the name without the mode: that makes it
    fixed).
  - Name without mode → the fixed agent of that name (created when missing; an existing
    temporary agent becomes fixed and loses its `auto_name`); newest wins.
- No project header (clients from before this feature) — compatibility rule:
  - Mode `unique` (old derived names) keeps the harness-onboarding choice: the first of
    `<slug>`, `<slug>-2` … `<slug>-50` that is new or offline (any kind). A new agent is
    temporary in project `<slug>` (the requested name) with `auto_name` `<Project> · Agent
    [#N]`; an existing agent keeps its kind and gets project `<slug>` only when it had none.
    So an upgraded server keeps routing old clients to their old (migrated, fixed) agents.
  - Plain name (old explicit `--agent`, or an old reconnect) → that agent, newest wins; a new
    one is fixed in project `ProjectFromName(name)`; an existing one keeps its project and kind.
- Every resolve unarchives the agent; the WebSocket also stamps `connected_at` (the active
  session pick of T5) before attaching. The gateway lock (`uniqueMu`) covers name choice →
  attach whenever the server picks the name (no name + project, or unique mode), so concurrent
  sessions get distinct N. The 50-agent cap applies to every new agent (409 "agent limit
  reached", nothing registered).
- testkit: `Client.DialAgentHeaders(h)`; api helpers `sessionHeaders(project, harness,
  machine, name, mode)`, `dialSession`, `agentByName`, `listedAgent`, `setAgentKind`,
  `waitOffline`, `requireClosedWith`, `listAgents(c, query)`, `timelineAuthor`,
  `sessionProject(c, slug)`, `addProject`.

## Rename, promote, archive, list (ID-04, ID-05, PROJ-02, PROJ-03, PROJ-05, T3)
- `PATCH /agents/{id}` (owner only; grantee 403, others 404) `{description?, display_name?,
  fixed?}`: `display_name` trimmed, 1-64 code points, else 422 `display_name` "must have 1 to
  64 characters"; `fixed: true` sets kind fixed and clears `archived_at` (the readable name
  stays); `fixed: false` → 422 `fixed`. Any 422 applies nothing. A rename publishes
  `MembersChanged` for each room the agent is a member of. `Registry.UpdateDescription` is
  replaced by `Registry.Update(ctx, p, id, AgentUpdate)`.
- `GET /agents?include_archived=true` lists archived agents (`archived: true`); without it
  (or any other value) they are left out. `Registry.ListVisible(ctx, p, includeArchived)`.
- Archiving: `Registry.Archive(ctx)` archives temporary agents with `archived_at` NULL,
  `COALESCE(last_seen_at, created_at)` older than `ArchiveAfter` (24 h) and no live session.
  `Registry.RunArchive` runs it at start and every `ArchiveInterval` (24 h) as the app's
  background worker `agent-archive` — a job of its own, not inside `rooms` retention, so
  `rooms/retention.go` is unchanged. Unarchive: any resolve/connect, or promotion.
- Projects: `GET /projects` (owned + use grants, own or everyone; `my_level` manage/use; by
  name), `PATCH /projects/{id}` `{name}` (owner only; trimmed 1-64, else 422 `name` "must
  have 1 to 64 characters"; the slug never changes; recomputes `auto_name` of the project's
  temporary agents and publishes `MembersChanged` for their rooms), `GET /projects/{id}/sharing`
  (owner or use grantee), `PUT /projects/{id}/sharing` (owner; use grants like agents).
  `access.KindProject`, `Authorizer.ProjectLevel`, `CanUseProject`. The live hub sends no
  `access.changed` for project grants yet (T10 may need it).

## Project room members (PROJ-06, T4)
- `POST /rooms/{id}/members {kind: "project", member_id}`: room manage required; the caller
  must own the project or hold use on it, else 403 (unknown projects too); duplicate 409.
  Unknown kind → 422 `kind` "must be agent, chat, user or project".
- Project members carry the agent settings (`display_name`, `sign_outbound`, `delivery`,
  `keywords`) on their member row; PATCH validates them like agent members. Chat and user
  members still get 422 "only for agent members".
- Project members appear in `GET /rooms/{id}/members`, `GET /agent/rooms` (list_rooms) and
  `room_info` with `kind: "project"`, `name` = project name, `owner` = project owner; no
  `online` yet.
- Not done here (Phase 2): `delivery.Plan` ignores project members (no `ProjectIDs` yet), so a
  project member receives nothing until T5; `access.AgentRoomLevel` does not count a project
  membership, so an agent reaches a room's REST tools only as a direct member (T5/T6 must
  decide whether the active session of a member project may post/reply/history there).

## Notes for Phases 2-3
- `make check` takes ~10 minutes on the devbox; `TestLinkPausesAfterReplaced` failed once
  more under load (T2 gate) and passed on the rerun.
- T7 (channel): send `X-Partyline-Project/-Harness/-Machine` on WS, REST and register; no
  `X-Partyline-Agent` without `--name`; after a welcome, reconnect with the assigned name +
  `X-Partyline-Agent-Mode: temporary`; REST tool calls send the assigned name (agentOnly
  resolves by name). The listen/tools-only name file keeps working with the assigned name.
- T9/T10 (web): fixtures `makeAgent` now fill `display_name` (= name), `harness` "agent",
  `machine` "", `kind` "fixed", `archived` false; the members list has no group for
  `project` members yet.
