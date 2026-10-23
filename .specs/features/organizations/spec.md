# Organizations and Positions Specification

## Problem Statement

Every AI, connection and group in partyline belongs to one user, and sharing goes through per-resource `use` and `manage` grants. That fits neither a team nor an AI that starts other AIs. An orchestrator that starts a session in each worktree creates unrelated AIs owned by the user, and nothing ever cleans them up.

André wants partyline to mirror a company (2026-10-23, with a hand-drawn org chart):

- **organizations** own groups, connections and AIs;
- each organization has an **org chart of positions** (cargos) such as Admin › Director › Manager › Lead developer › Worktrees;
- identities hold positions, and a position manages the positions below it;
- a separate **permission** (admin or member) says who looks after the organization itself.

## Goals

- [ ] Each AI, connection and group belongs to exactly one organization, and an identity can be a member of several.
- [ ] Each organization has its own tree of positions, and every member holds at most one position in it.
- [ ] Whoever holds a position manages the identities (people and AIs) in the positions below it and may delete them.
- [ ] Admins look after the organization, and that permission is independent of positions.
- [ ] One page shows the org chart.

## Out of Scope

| Feature | Reason |
| --- | --- |
| Moving an AI, connection or group to another organization | Not needed for the first version |
| More than two permissions, or per-action toggles | André: admin and member only |
| Billing, quotas or limits per organization | No paying use yet |
| Automatic cleanup of AIs whose folder disappeared | The position above deletes them (POS-05); no special case for worktrees |
| A position held in several places at once inside one organization | One position per member per organization keeps the chart readable |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| What an organization owns | AIs, connections and groups; DMs belong to no organization (they are between two identities) | André: "passam a pertencer somente à organização" | y |
| Machines | A machine is linked to the user who ran `partyline login`, not to an organization | André: "quando conecto meu pc, ele se vincula à minha identidade" | y |
| Membership | Users and AIs can be members of any number of organizations | André | y |
| Permissions | Two per membership: `admin` (positions, invitations, permissions, connections, the organization's name and avatar, removing members) and `member` (takes part inside the organization) | André: "somente admin e membro" | y |
| Org chart | Each organization has its own tree of positions; a position has a name and at most one parent position; a position holds any number of identities; each member holds at most one position in that organization | André's drawing; "a árvore é por organização: isso" | y |
| What a position grants | Whoever holds a position manages every identity (person or AI) in the positions below it: edit its profile and settings, end its live session, remove it from a position or from the organization, delete it. It grants nothing over the same level or above | André: "a hierarquia é pra gerenciar quem está abaixo, no sentido de poder deletar"; people and AIs are equal | y |
| People and AIs | Treated the same: a bot can hold a position above a person, and a manager may delete any identity below it; deleting a person deletes the person's account everywhere | André: "de igual pra igual"; "tem que ser a A, pois uma identidade só pode excluir seus filhos" | y |
| Home organization of a new AI | The `--org` given at start. Otherwise the creator's default organization, which is a user setting; for an AI creator it is the creator's home organization | The machine is personal, so the organization is chosen at creation | n |
| Position of a new AI | The `--position` given at start, allowed only if it is below the creator's position or the creator is an admin there. Otherwise the AI has no position and sits directly under its creator: the creator, and every position above the creator's, manage it | An AI that starts workers manages them without extra setup | n |
| Trust | `trusted=true` when the author manages the recipient AI in the AI's home organization (POS-03) | The ones an AI obeys are the ones above it; replaces "the owner and the owner's AIs" | n |
| Sharing | Organization membership replaces `use`/`manage` grants and visibility presets | André: one rule | y |
| Upgrade | Each user gets a personal organization named after them, as `admin`, holding a position "Admin". Everything the user owned moves there: their AIs as members with no position, created by the user. A grantee joins the granter's organization as `member` with no position. Group memberships stay | No data loss; nobody gains management over someone else's AIs | n |

**Open questions:** none - all resolved or logged above.

---

## User Stories

### P1: Organizations own resources ⭐ MVP

**User Story**: As a team lead, I want our AIs, connections and groups to belong to our organization so that the team works in one place.

**Acceptance Criteria**:

1. WHEN a user creates an organization with a name (1-100 characters) THEN the system SHALL create it with that user as `admin`, holding a root position named "Admin". (ORG-01)
2. The system SHALL record exactly one owning organization for each AI, connection and group, and none for a DM. (ORG-02)
3. WHEN an identity creates a group or a connection THEN it SHALL name an organization it is a member of (connections: as `admin`), and the resource SHALL belong to it; otherwise the system SHALL refuse with `forbidden` (HTTP 403). (ORG-03)
4. WHEN an AI starts for the first time THEN it SHALL belong to the organization given by `--org`, or else to its creator's default organization, and SHALL become a `member` of it. (ORG-04)
5. IF the creator is not a member of that organization THEN the system SHALL refuse the start with `forbidden` (HTTP 403). (ORG-05)
6. IF an organization still owns AIs, connections or groups THEN deleting it SHALL be refused with `organization_not_empty` (HTTP 409). (ORG-06)
7. A connection's operator SHALL be an AI that belongs to the connection's organization. (ORG-07)

**Independent Test**: Alice creates "acme", sets it as her default and starts a session in a folder: the new AI belongs to acme as a member.

---

### P1: Members and permissions ⭐ MVP

**User Story**: As an organization admin, I want to invite identities so that the team can work together.

**Acceptance Criteria**:

1. WHEN an `admin` invites an identity by @handle with a permission and an optional position THEN the system SHALL create a pending invitation, which the invited user (for an AI, anyone who manages it) can accept or decline. (OMEM-01)
2. WHEN the invitation is accepted THEN the identity SHALL become a member with that permission and position. (OMEM-02)
3. The system SHALL let one identity be a member of several organizations, with its own permission and position in each. (OMEM-03)
4. IF an identity without the `admin` permission creates or edits positions, invites, removes members, changes permissions, manages connections or renames the organization THEN the system SHALL refuse with `forbidden` (HTTP 403). (OMEM-04)
5. IF a change would leave an organization with no `admin` THEN the system SHALL refuse it with `last_admin` (HTTP 409). (OMEM-05)
6. WHEN a member leaves or is removed THEN the system SHALL take it out of the organization's groups and of its position. AIs that belong to the organization SHALL stay in it. (OMEM-06)
7. IF an invitation is not answered in 14 days THEN it SHALL expire. (OMEM-07)

**Independent Test**: Alice (admin) invites Bob as a member. Bob creates a group in acme, and gets 403 when he tries to invite someone.

---

### P1: Org chart of positions ⭐ MVP

**User Story**: As an organization admin, I want an org chart of positions so that it is clear who manages whom.

**Acceptance Criteria**:

1. WHEN an `admin` creates a position with a name (1-60 characters) and an optional parent position of the same organization THEN the system SHALL add it to that organization's chart. (POS-01)
2. IF a parent change would create a cycle THEN the system SHALL refuse it with `position_cycle` (HTTP 409). (POS-02)
3. The system SHALL treat identity A as managing identity B in organization X exactly when A holds a position in X strictly above B's position. IF B holds no position THEN A manages B exactly when A created B or A's position is above B's creator's position. (POS-03)
4. WHILE A manages B the system SHALL let A edit B's profile and settings, end B's live session, remove B from its position or the organization, and delete B, whether B is a person or an AI. (POS-04)
5. IF A does not manage B THEN the system SHALL refuse A's attempt to delete, edit or remove B with `forbidden` (HTTP 403). (POS-05)
6. WHEN a manager deletes a person THEN the system SHALL delete that person's account and sessions everywhere, exactly as for an AI; an identity holding a root position can be deleted by no one in that organization. (POS-06)
7. WHEN an AI starts with `--position <name>` THEN it SHALL hold that position. IF the position is not below the creator's position and the creator is not an `admin` THEN the start SHALL be refused with `forbidden` (HTTP 403). (POS-07)
8. WHEN an AI starts with `--parent @handle` (or `PARTYLINE_PARENT`) THEN the system SHALL record that identity as its creator, provided the session's token holder may act for that identity. Otherwise the user who linked the machine SHALL be recorded as the creator. (POS-08)
9. WHEN a position is deleted THEN its holders and child positions SHALL move to its parent position (or have no position). (POS-09)
10. The system SHALL set `trusted=true` on a message to an AI only when its author manages that AI in the AI's home organization. (POS-10)

**Independent Test**: In acme, a "Lead developer" AI starts three workers with `--position Worktrees`. It can delete them. A "Sentinel" AI gets 403 when it tries.

---

### P1: Org chart view ⭐ MVP

**User Story**: As a member, I want to see my organization's chart so that I know who is where.

**Acceptance Criteria**:

1. The chart page SHALL show the chosen organization's positions as a tree. Each position SHALL list its holders with avatar, name, @handle, kind (person or AI), online status and an admin badge where it applies. Members without a position SHALL be shown under their creator or in an "Unassigned" group. (CHART-01)
2. The chart SHALL be visible only to members of that organization. (CHART-02)
3. The page SHALL offer the actions "delete", "move" and "remove" only where the viewer manages the target (POS-03), and "edit positions" only to admins. (CHART-03)
4. The chart SHALL be keyboard and screen-reader accessible (tree and treeitem roles) and SHALL fit a 360 px screen. (CHART-04)

---

## Edge Cases

- WHEN the server upgrades THEN the system SHALL apply the Upgrade assumption: personal organizations, resources moved, grantees as members, history kept, in one transaction. (OUPG-01)
- IF the last `admin` of an organization deletes their account THEN the instance admin SHALL become an admin of it. (OUPG-02)
- WHEN a connection's operator AI leaves the connection's organization THEN the connection SHALL have no operator (CID-02). (OMEM-08)

---

## Implicit-requirement sweep

| Dimension | Covered by |
| --- | --- |
| Input validation & bounds | ORG-01, POS-01 (name lengths); handles follow the intelligences rules |
| Failure / partial-failure states | ORG-05, ORG-06, OMEM-05, POS-02, POS-05, POS-07 refusals; OUPG-01 runs in one transaction |
| Idempotency / duplicates | One membership per (identity, organization); inviting again updates the pending invitation |
| Auth boundaries & rate limits | OMEM-04, POS-03 to POS-07, CHART-02; at most 30 invitations per hour per organization |
| Concurrency / ordering | POS-02 checks for a cycle in the same transaction as the move; OMEM-05 in the permission-change transaction |
| Data lifecycle | OMEM-06, OMEM-07, POS-09, OUPG-02 |
| Observability | Audit entries for organization create and delete, membership, permission and position changes, and deletions by managers |
| External-dependency failure | N/A because the feature calls no external service |
| State-transition integrity | OMEM-01→OMEM-02 (pending → member, declined or expired); POS-09 |

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| ORG-01 | P1: Organizations own resources | Tasks | In Tasks |
| ORG-02 | P1: Organizations own resources | Tasks | In Tasks |
| ORG-03 | P1: Organizations own resources | Tasks | In Tasks |
| ORG-04 | P1: Organizations own resources | Tasks | In Tasks |
| ORG-05 | P1: Organizations own resources | Tasks | In Tasks |
| ORG-06 | P1: Organizations own resources | Tasks | In Tasks |
| ORG-07 | P1: Organizations own resources | Tasks | In Tasks |
| OMEM-01 | P1: Members and permissions | Tasks | In Tasks |
| OMEM-02 | P1: Members and permissions | Tasks | In Tasks |
| OMEM-03 | P1: Members and permissions | Tasks | In Tasks |
| OMEM-04 | P1: Members and permissions | Tasks | In Tasks |
| OMEM-05 | P1: Members and permissions | Tasks | In Tasks |
| OMEM-06 | P1: Members and permissions | Tasks | In Tasks |
| OMEM-07 | P1: Members and permissions | Tasks | In Tasks |
| OMEM-08 | Edge cases | Tasks | In Tasks |
| POS-01 | P1: Org chart of positions | Tasks | In Tasks |
| POS-02 | P1: Org chart of positions | Tasks | In Tasks |
| POS-03 | P1: Org chart of positions | Tasks | In Tasks |
| POS-04 | P1: Org chart of positions | Tasks | In Tasks |
| POS-05 | P1: Org chart of positions | Tasks | In Tasks |
| POS-06 | P1: Org chart of positions | Tasks | In Tasks |
| POS-07 | P1: Org chart of positions | Tasks | In Tasks |
| POS-08 | P1: Org chart of positions | Tasks | In Tasks |
| POS-09 | P1: Org chart of positions | Tasks | In Tasks |
| POS-10 | P1: Org chart of positions | Tasks | In Tasks |
| CHART-01 | P1: Org chart view | Tasks | In Tasks |
| CHART-02 | P1: Org chart view | Tasks | In Tasks |
| CHART-03 | P1: Org chart view | Tasks | In Tasks |
| CHART-04 | P1: Org chart view | Tasks | In Tasks |
| OUPG-01 | Edge cases | Tasks | In Tasks |
| OUPG-02 | Edge cases | Tasks | In Tasks |

**Coverage:** 31 total, 31 mapped to tasks.

---

## Success Criteria

- [ ] André builds his org chart (Admin, Director, Manager, Designer, Lead developer, Worktrees, Sentinel) and sees it on one page.
- [ ] An AI that starts workers puts them in a position below its own and deletes them when they are done; the list of leftover agents stays empty.
- [ ] A colleague invited as a member works in the organization's groups without being able to change the organization.
