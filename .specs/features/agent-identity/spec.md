# Agent Identity Specification

## Problem Statement

Agents are named after the working directory, so a session started in the home folder shows up as `dev`, names cannot be edited, and a user with many projects and several sessions per project gets an unreadable flat list. Rooms point at individual sessions, which come and go, so routing breaks every time a session is replaced. Reported 2026-10-21.

## Goals

- [ ] Every agent has a readable name (project · harness) that the owner can rename.
- [ ] The agents page stays organized with dozens of projects: grouped by project, temporary sessions archived automatically.
- [ ] A room can target a project, so messages keep flowing when sessions are restarted or replaced.

## Out of Scope

| Feature | Reason |
| --- | --- |
| Moving an agent between projects | Rare; delete and reconnect covers it |
| Load-balancing a project's messages across several sessions | One active session per project keeps replies single |
| Cross-owner projects | Sharing a project uses the existing grants model |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| Project of a session | Git repository root basename; else the working directory basename; the CLI flag `--project` overrides | Matches how users think about their work | y |
| Home folder or `/` | Project `home`; the CLI prints a warning suggesting `--project` or `--name` | Avoids `dev`-style accidental names | y |
| Harness label | From the launcher (`claude`, `opencode`, `pi`, `omp`, `codex`, `gemini`), else `agent` | Known at launch | y |
| Temporary vs fixed | Sessions without `--name` are temporary; `--name <n>` creates/uses a fixed agent | Users name what they want to keep | y |
| Temporary display name | `<Project> · <Harness label>`, plus ` #N` when several are registered at once | Readable at a glance | y |
| Archiving | Temporary agents offline for 24 h move to Archived (hidden by default); a reconnect restores them; fixed agents are never archived | Keeps the list short | y |
| Project member delivery | Messages go to the project's active session: the most recently connected online agent of that project; if none is online, they wait (project cursor) and the next session of the project receives them as backlog | Survives restarts without duplicate replies | y |
| Machine | The CLI sends the hostname; shown in the UI and used as a filter | Users run several machines | y |
| Sessions without a live connection (precision for ID-01 and PROJ-07, fix iteration 1) | Tools-only sessions (Codex, Gemini, the tools half of pi/OpenCode, the turn-end hooks) are never online, so they never become a project's active session and never receive a project member's messages. They are told apart by the session key (`X-Partyline-Session`, from `PARTYLINE_SESSION`): each key gets its own temporary agent (` #N` while another one is held) and the same key gets it back. Without a key the server keeps the old rule (the lowest offline temporary agent) | Presence cannot tell two sessions that never connect live apart; the key the launcher already shares between a session's processes can | y |

**Open questions:** none - all resolved or logged above.

---

## User Stories

### P1: Readable, editable agent names ⭐ MVP

**User Story**: As a user, I want agents named after project and harness, and to rename them, so that I recognize each one.

**Why P1**: Reported confusion (`dev`).

**Acceptance Criteria**:

1. WHEN a session connects without `--name` THEN the server SHALL register a temporary agent in the derived project with display name `<Project> · <Harness label>` (and ` #N`, N ≥ 2, while another agent of the same project and harness exists). (ID-01)
2. WHEN a session connects with `--name <n>` THEN the server SHALL use the fixed agent `<n>` of that owner, creating it if needed, recording its project, harness and machine. (ID-02)
3. WHEN the working directory is the user's home folder or `/` and no `--project`/`--name` is given THEN the CLI SHALL use project `home` and print one warning line suggesting `--project` or `--name`. (ID-03)
4. WHEN the owner renames an agent (`PATCH /api/v1/agents/{id}` `display_name`, 1–64 characters) THEN the new display name SHALL appear everywhere (timeline, members, outbound signature, mentions). (ID-04)
5. WHEN the owner promotes a temporary agent to fixed (`PATCH … {"fixed": true}`) THEN it SHALL no longer be archived. (ID-05)

**Independent Test**: Start a session in `~/proj` and one in `~`; the first is "Proj · Claude Code", the second warns and joins project `home`; rename works.

---

### P1: Projects and organization ⭐ MVP

**User Story**: As a user with many projects, I want agents grouped by project with old sessions archived so that the list stays manageable.

**Why P1**: Scale concern (several agents per project).

**Acceptance Criteria**:

1. The server SHALL keep one project per (owner, project slug) and return it on each agent (`project{id, slug, name}`), plus `harness`, `machine`, `kind` (`fixed|temporary`) and `archived`. (PROJ-01)
2. WHEN a temporary agent has been offline for 24 hours THEN the daily job SHALL mark it archived; WHEN it reconnects THEN it SHALL be unarchived. (PROJ-02)
3. WHEN `GET /api/v1/agents` is called without `include_archived=true` THEN archived agents SHALL be omitted. (PROJ-03)
4. The agents page SHALL group agents by project (projects with an online session first), show harness, machine and online state, filter by machine and harness, and hide archived agents behind a toggle. (PROJ-04)
5. WHEN the owner renames a project THEN its name SHALL change everywhere; the slug stays stable. (PROJ-05)

**Independent Test**: Three sessions across two projects render as two groups; an offline temporary agent disappears after 24 h (fake clock).

---

### P1: Rooms that target a project ⭐ MVP

**User Story**: As a room manager, I want to add a project to a room so that whichever session of that project is open receives the messages.

**Why P1**: Sessions are ephemeral; rooms must not break.

**Acceptance Criteria**:

1. WHEN a manager adds a project member (`kind: "project"`) THEN the system SHALL require the manager to own the project or hold `use` on it, else 403. (PROJ-06)
2. WHEN a message is delivered to a project member THEN the system SHALL push it only to the project's active session (most recently connected online agent of the project); IF none is online THEN it SHALL wait. (PROJ-07)
3. WHEN a session of the project connects THEN it SHALL receive, as backlog, the project's undelivered messages for each room the project belongs to (same 50-per-room cap and notice as agents). (PROJ-08)
4. WHEN the active session replies THEN the reply SHALL be authored by that agent and SHALL NOT be delivered through the project member to any session of the same project (posts of any session of a project never reach that project member); a session of the same project that is itself a direct agent member of the room SHALL still receive it as that member, since direct membership is an explicit subscription (AD-009). (PROJ-09)
5. The system SHALL compute `trusted` for project deliveries exactly as for agents (owner of the project). (PROJ-10)

**Independent Test**: Room with project "Atlas"; post while offline; open an Atlas session → it receives the message; restart it → the new session keeps receiving.

---

### P2: CLI flags and listing

**User Story**: As a user, I want `--name`, `--project` and a list of my agents from the CLI.

**Why P2**: Convenience.

**Acceptance Criteria**:

1. `partyline <harness> --name <n> --project <p>` SHALL pass name and project to the session. (CLI-01)
2. `partyline agents` SHALL list the owner's agents grouped by project with online state. (CLI-02)

**Independent Test**: CLI flags reach the welcome frame; `partyline agents` output matches the API.

---

## Edge Cases

- IF two sessions connect at the same instant for the same project and harness THEN they SHALL get distinct numbers (ID-01).
- IF two tools-only sessions of the same project and harness send different session keys THEN they SHALL get distinct temporary agents, and a session that registers again with its key (its tools or its turn-end hook) SHALL get its own agent back (ID-01).
- WHEN the active session disconnects while another session of the project is online THEN the next most recent online session SHALL become active for new deliveries (PROJ-07).
- IF a project member and one of its agents are both members of a room THEN that agent SHALL receive each message once (PROJ-07).

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| ID-01 | P1: Names | Execute | Verified |
| ID-02 | P1: Names | Execute | Verified |
| ID-03 | P1: Names | Execute | Verified |
| ID-04 | P1: Names | Execute | Verified |
| ID-05 | P1: Names | Execute | Verified |
| PROJ-01 | P1: Organization | Execute | Verified |
| PROJ-02 | P1: Organization | Execute | Verified |
| PROJ-03 | P1: Organization | Execute | Verified |
| PROJ-04 | P1: Organization | Execute | Verified |
| PROJ-05 | P1: Organization | Execute | Verified |
| PROJ-06 | P1: Project rooms | Execute | Verified |
| PROJ-07 | P1: Project rooms | Execute | Verified |
| PROJ-08 | P1: Project rooms | Execute | Verified |
| PROJ-09 | P1: Project rooms | Execute | Verified |
| PROJ-10 | P1: Project rooms | Execute | Verified |
| CLI-01 | P2: CLI | Execute | Verified |
| CLI-02 | P2: CLI | Execute | Verified |

**Coverage:** 17 total, mapped in tasks.md

---

## Success Criteria

- [ ] André's agents page shows his projects as groups with readable names and no `dev`.
- [ ] A room targeting a project keeps working across session restarts (manual UAT).
