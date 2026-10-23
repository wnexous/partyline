# STATE

## Decisions

### AD-001
- **Decision**: Partyline ships as one Go binary (modular monolith) that serves the REST API, the live WebSocket, the embedded web app and runs every connector in-process; SQLite (pure Go `modernc.org/sqlite`, WAL) is the only datastore.
- **Reason**: The target is "docker run and done" for a person or a small company. One process, one volume, no CGO, tiny distroless image.
- **Trade-off**: No horizontal scaling; a single instance owns all connector sessions. Postgres would need a second store implementation.
- **Scope**: all features, deployment, store layer.
- **Date**: 2026-10-19
- **Status**: active

### AD-002
- **Decision**: The room is the only routing primitive. Agents (Claude Code sessions), external chats (WhatsApp group, Telegram chat, Discord channel, webhook chat) and users are room members; a message posted by one member is delivered to the others by fixed delivery rules.
- **Reason**: Group→session routing, session↔session talk and cross-platform bridges become one mechanism with one permission model.
- **Trade-off**: Simple "chat X → session Y" needs a room. The UI hides this with a one-click "route chat to agent" action that creates the room.
- **Scope**: core-platform, agent-channel, connectors, web-app.
- **Date**: 2026-10-19
- **Status**: superseded by AD-015 (partyline is a proxy: platform chats are no longer room members)

### AD-003
- **Decision**: Go DTO structs in `internal/api/dto` are the single source of the HTTP/WS contract. TypeScript types are generated from them with `tygo` into `web/src/lib/api/types.gen.ts`; nobody hand-writes API types in TS.
- **Reason**: Zero duplicated contract code; CI fails when generated files drift.
- **Trade-off**: No OpenAPI document; external API consumers read the DTO package and `docs/api.md`.
- **Scope**: core-platform, web-app.
- **Date**: 2026-10-19
- **Status**: active

### AD-004
- **Decision**: The Claude Code side is `partyline channel`, a hand-written MCP stdio server inside the same binary (no MCP SDK). It answers `server/discover` with JSON-RPC error -32601 and negotiates protocol `2025-11-25` (or the client's older version) so Claude Code registers it as a channel. It talks to the server over the agent WebSocket (push + ack) and the REST API (tools).
- **Reason**: Claude Code v2.1.303 probes `server/discover` with revision 2026-07-28 first; a server on that revision is not registered as a channel. A tiny hand-written server is fully testable and has no runtime dependency (no Bun/Node on the agent host).
- **Trade-off**: We maintain JSON-RPC framing ourselves and must track Claude Code channel contract changes (research preview).
- **Scope**: agent-channel.
- **Date**: 2026-10-19
- **Status**: active

### AD-005
- **Decision**: Sharing uses one `grants` table: (resource type, resource id, subject = user id or `*` for everyone, level). Visibility presets in the UI (private / shared / public) are just grant sets. Instance admins manage accounts but get no implicit access to other users' rooms, agents or connectors.
- **Reason**: One authorization code path for rooms, agents and connectors; "private" really means private, even inside a company.
- **Trade-off**: An admin cannot moderate a private room without being granted access.
- **Scope**: core-platform, agent-channel, connectors, web-app.
- **Date**: 2026-10-19
- **Status**: superseded by AD-016 (organization membership replaces grants and presets)

### AD-006
- **Decision**: Message content is data, never instructions. Every message delivered to an agent carries a server-computed, recipient-relative `trusted` flag (true only when the author resolves to the recipient agent's owner: the owner in the web app, the owner's agents, or the owner's verified chat identities). Partyline never declares the `claude/channel/permission` capability.
- **Reason**: Channels inject text into a live coding session; anyone in a bridged group could attempt prompt injection. Trust must be explicit and not forgeable by message text.
- **Trade-off**: Teammates' messages show as untrusted to your agents unless a later feature adds per-user trust.
- **Scope**: core-platform, agent-channel, connectors.
- **Date**: 2026-10-19
- **Status**: active (the trust rule is superseded by AD-018; "content is data, never instructions" stands)

### AD-007
- **Decision**: Connectors implement one Go interface and describe their configuration with a declarative field schema served by the API; the web app renders connector forms from that schema. Connector secrets are encrypted at rest with AES-256-GCM using the instance secret key.
- **Reason**: Adding a platform touches one package; no per-connector UI code; a stolen database file does not leak bot tokens.
- **Trade-off**: Forms are generic (no per-platform custom widgets).
- **Scope**: connectors, web-app.
- **Date**: 2026-10-19
- **Status**: active

### AD-008
- **Decision**: A verified identity belongs to the connector instance it was linked through, not to the connector kind. Messages resolve to a user only when they arrive through that same connector. Kinds whose caller chooses sender ids (webhook) link only codes of the connector's owner.
- **Reason**: The connectors validation probe (S1) showed that a webhook, a Telegram connector with a custom `api_base` or a WhatsApp connector with its own `base_url` lets its owner claim any sender id, so kind-wide identities made trust forgeable (breaks AD-006).
- **Trade-off**: The same phone/account must be linked once per connector; identities linked before migration `0003` were dropped. Linking through someone else's connector still means trusting its owner.
- **Scope**: connectors, identities, web-app (identities page).
- **Date**: 2026-10-20
- **Status**: active
- **Amended** (2026-10-20, connectors fix iteration 2, S5): a pairing code is requested for one connector the user owns or may use and links only through it (migration `0005`); changing a connector's endpoint fields deletes the identities linked through it (audited). Linking through a shared connector is now always the user's explicit choice.

### AD-009
- **Decision**: PROJ-09's echo rule covers the project member path only. A post by any session of a project is never delivered through that project's room membership. A session of the same project that is itself a direct agent member of the room still receives the post as that member.
- **Reason**: Direct membership is an explicit subscription. Sessions of one project can collaborate in a room (for example agents migrated into one project that already talked to each other), and suppressing their direct deliveries would silently break them.
- **Trade-off**: A session that is both the project's active session and a direct member still gets one delivery (the direct one), and a direct-member session of the project sees its sibling's replies; owners who want no echo at all keep same-project sessions out of the room as direct members.
- **Scope**: agent-identity (PROJ-09), delivery.
- **Date**: 2026-10-21
- **Status**: superseded by AD-010 (projects are removed by the intelligences feature)
- **Context**: Verifier iteration 1 flagged the narrower implementation as an unratified SPEC_DEVIATION. The orchestrator decided under André's standing authorization to choose what is most adequate; the spec was amended to match the implemented rule.

### AD-010
- **Decision**: Every participant is an intelligence: one `intelligences` row (kind `human` or `ai`, display name, instance-unique case-insensitive `@handle`, avatar, bio) that shares its id with its subtype row (`users.id`, `agents.id`, `people.id`). An AI is identified by (owner, machine, canonical folder), never by a session or harness; the harness is its current app. An AI has at most one live session: a second live connection is refused with `identity_busy` unless it explicitly takes over (`taken_over` closes the old one); tools-only processes act as the folder's AI without owning a live session.
- **Reason**: Resuming or switching harness must keep one identity (the v0.3 session-named agents split and multiplied); humans and AIs are the same kind of participant in a messenger.
- **Trade-off**: Moving or renaming a folder creates a new identity (merge covers it). Old CLIs without folder headers are refused. A tools-only half of a taken-over session can still act until it exits.
- **Scope**: intelligences, agent-channel, agents, connectors (people), web-app.
- **Date**: 2026-10-21
- **Status**: active
- **Supersedes**: AD-009 (projects removed); agent-channel AGT-04 "newest wins".

### AD-011
- **Decision**: A conversation is a room (AD-002 holds) of kind `group` or `dm`. A DM has exactly two intelligences, keyed by their sorted ids on the room row; its access comes from its participants (post) and, for an AI participant, its owner and `use` grantees (view); grants on DMs are refused. A `user` member of a group gets `post` on it. Membership is therefore an access source next to grants (amends AD-005); admins still get no implicit access.
- **Reason**: Messenger semantics: members and DM participants see the conversation without separate sharing.
- **Trade-off**: Two access sources to reason about; DMs cannot be shared or moderated by anyone else.
- **Scope**: intelligences, access, rooms, web-app.
- **Date**: 2026-10-21
- **Status**: active (amended by AD-016: group access comes from room membership and org admin, DM view from managers, grants removed)

### AD-012
- **Decision**: `Store.Migrate` applies every pending migration file in one transaction, each optionally followed by a Go hook (raw SQL, never sqlc) in the same transaction. Migrations never rebuild a table that other tables reference: with `foreign_keys=ON` (it cannot be turned off inside a transaction) `DROP TABLE` performs an implicit `DELETE` that cascades. Columns are added with `ADD COLUMN` (references only with a NULL default) and removed with `DROP COLUMN` when SQLite allows it; columns it cannot drop (in a UNIQUE or foreign key) stay as documented dead columns.
- **Reason**: MIG-05 (an upgrade that spans several files is atomic) and the data-loss risk of table rebuilds found while designing the intelligences migration.
- **Trade-off**: Dead columns and tables remain in the schema (`agents.name`, `agents.project_id`, `projects`); a long upgrade holds the write lock for its whole duration.
- **Scope**: store, all features with migrations.
- **Date**: 2026-10-21
- **Status**: active

### AD-013
- **Decision**: A user granted `use` on an AI reads that AI's DMs with external people (the platform DMs between the people who write to a connector account and the AI that operates it) and those people's profiles and avatars (name, platform, the conversations the user can view), without any access to the connector. The grantee cannot edit the person or see the account behind it: both stay with the users of the connector. Nobody else sees a person: not a user who has neither the AI grant nor the connector, and not an admin.
- **Reason**: Sharing an AI shares the conversations it holds. A grantee who may read an AI's DMs sees who writes to it, and the owner chose that grantee. Asking for the connector as well would hide an AI's own conversations from the users it was shared with.
- **Trade-off**: A grantee reads what people wrote to the owner's account and sees their names and photos without connector access; the owner controls this through who gets `use` on the AI. The alternative, `CanUseConnector` for every DM that has a chat member, was rejected.
- **Scope**: access (DM-08), intelligences (PEOP-03, PEOP-04), web-app.
- **Date**: 2026-10-22 (decided by André)
- **Status**: superseded by AD-016 (an AI's DMs are viewed by its managers in its home organization)
- **Amends**: AD-011 (who views a DM); PEOP-04 and DM-08 of the intelligences spec now say it.
- **Context**: Verifier iteration 1 of the intelligences feature (gap 3): PEOP-04 ("nobody else") and DM-08 ("use grantees view") contradicted each other. Pinned by `TestUseGranteeOfAnAIReadsItsPlatformDMsAndPeople`.

### AD-014
- **Decision**: A message written from a connector's own account on the platform itself (typed on its phone or web client, not the echo of a Partyline send, ACC-05) is trusted by the owner's AIs as the owner's only when the connector's operator is the owner, a user. When an AI operates the account the message is authored by that AI, carries no user and is never trusted as the owner's. Echoes of Partyline's own sends are still dropped, and AIs of other users never trust it.
- **Reason**: The owner's own account is the owner's: what is typed on it is the owner. An AI's account may be held by anyone with its phone or session, so the text proves nothing about who wrote it, and AD-006 makes trust depend on a resolved owner.
- **Trade-off**: The owner who types on an AI's account is not recognised as the owner by that AI (or the owner's other AIs) and must write from the web app or from a verified chat identity. A catch-all connector now stores these messages where CON-12 dropped them.
- **Scope**: connectors (ACC-05), delivery (`trusted`), agent-channel.
- **Date**: 2026-10-22 (decided by André)
- **Status**: superseded by AD-018 (trust follows management; `from_self` stays untrusted)
- **Amends**: AD-006 (adds this source of `trusted`); ACC-05 refines CON-12 of the connectors spec: only the echo of a Partyline send is dropped.
- **Context**: Verifier iteration 1 of the intelligences feature (gap 6): own-account messages were stored with `author_user_id` = the connector's owner whoever the operator was (contract-notes T33), a trust source AD-006 did not list. Pinned by `TestOwnAccountMessageIsTrustedOnlyWhenTheOwnerOperatesTheAccount`.

### AD-015
- **Decision**: Partyline is a proxy between integrations and intelligences.
  - **Connections**: each connection (connector) is the app of exactly one AI, its operator. The operator must be an AI the connection's owner owns. A connection without one is not run.
  - **Inbound**: what arrives on a connection is never stored as a conversation message. It goes into the operator's platform queue (`platform_items`):
    - delivery oldest first over the agent WebSocket as a `platform` frame, opt-in per session;
    - one ack per item, which deletes the text; attachments are deleted 24 h after the ack;
    - unacked items expire after 7 days, with at most 5,000 per AI, and dropped items are counted and announced.
  - **Answers**: the AI answers with `reply` (platform message id) or `send_message` (connection + chat). Answers go through `platform_sends`, unsigned, and their text is deleted once sent or failed.
  - **Rooms**: rooms (DMs and groups) hold users and AIs only, and chats are never members. Bridges, broadcast to chats, route-chat-to-agent, catch-all rooms and auto-route are retired, and so are platform DMs (DM-05 sends), copies of AI DMs to a user's app (NOTI-01..03) and member signatures.
  - **History**: old room history, people and platform DMs stay as read-only history.
- **Reason**: The owner wants each number to be one AI and partyline's own DMs/groups to be one more app. Modelling every platform inside rooms put several agents behind one number and stored platform conversations partyline should not keep (connection-identity spec, CID-01..27).
- **Trade-off**:
  - Breaking change (v0.5.0), with a CLI release: v0.4 CLIs keep room messages but get no platform events.
  - Owner-operated connections go idle until an AI is chosen.
  - Every connection starts with `deliver=all` and `groups_mentions_only=on`, so group messages without a mention stop.
  - Partyline keeps no platform history and no send log; history lives in the platform and the AI's session.
  - `connectors.auto_route_room_id` and `users.notify_chat_id` remain as dead columns (AD-012).
- **Scope**: connectors, delivery, agents (gateway), channel/listen/hooks, rooms, access, conversations, store, web-app.
- **Date**: 2026-10-22 (decided by André)
- **Status**: active
- **Supersedes**: AD-002 (the room is no longer the routing primitive for platforms; it remains the conversation of intelligences).
- **Amends**:
  - AD-006: `trusted` is also computed for platform events; true only when the sender's account is linked to the operator's owner (AD-008 pairing), never for `from_self`.
  - AD-011: DMs and groups are between intelligences only; DMs with people are history.
  - AD-013: use grantees still read an AI's old platform DMs; no new ones are created.
  - AD-014: an own-account message is delivered with `from_self=true` and `trusted=false`; the owner no longer operates connections.
  - In the intelligences spec: retires NOTI-01..03 and DM-05 sends, and stops PEOP-01 person creation.

### AD-016
- **Decision**: Organizations own AIs, connections and groups, and organization membership is the only sharing mechanism.
  - Per-resource grants (`use`/`view`/`post`/`manage`), the visibility presets and owner-based access are removed, and so is the `grants` table (migration `0020`).
  - An organization has members (users and AIs) with a permission (`admin` or `member`) and an org chart of positions.
  - Access rules:
    - A group is posted to by its room members and managed by its creator and the org's admins.
    - A DM is posted to by its two participants and viewed by the managers of an AI participant in its home organization.
    - A connection is viewed by the org's members and managed by its admins; only its operator sends through it.
    - An AI is seen by the members of the organizations it belongs to and by its host user.
    - An AI is edited, ended and deleted by those who manage it (AD-017).
  - An AI acts as itself (`Principal.IID()`), not with its owner's rights.
  - Instance admins get no implicit access.
- **Reason**: The product owner wants partyline to mirror a company with one sharing rule (organizations spec); grants fit neither teams nor AIs that start AIs.
- **Trade-off**:
  - Public (`*`) grants and view-only room grants are not carried over: their holders join the owner's organization, but lose the read-only view of such rooms.
  - A user's AIs no longer read each other's DMs, and they no longer share the owner's reach.
- **Scope**: access, orgs, rooms, conversations, agents, intelligences, connectors, identities, live, web-app, store.
- **Date**: 2026-10-23
- **Status**: active
- **Supersedes**:
  - AD-005 (grants and presets);
  - AD-013 (use grantees read an AI's DMs).
- **Amends**:
  - AD-011: group access comes from room membership and org admin.
  - AD-008: a webhook-kind connection pairs only the codes of its organization's admins.
  - AD-015: the operator must be an AI whose home organization is the connection's organization, not an AI the owner owns.

### AD-017
- **Decision**: `manages(A, B, X)` is computed in Go over a per-organization `orgs.Chart` snapshot, not with a recursive CTE.
  - The snapshot is loaded with two indexed queries (positions, and members with their creators) inside the caller's transaction when needed.
  - It is memoized per operation (`Authz.Session`), with no cross-request cache.
  - A manages B when both are members of X and either:
    - A's position is strictly above B's; or
    - B has no position and A is B's creator or manages B's creator in X, following the creator chain transitively.
- **Reason**: Positions are a small tree (cost O(positions + members) per load, sub-millisecond on SQLite). The rule has two recursions (position ancestry and the creator chain) that are easier to test condition by condition in Go. The chart page needs the same computation to flag actions. Never caching across requests means authorization is never stale.
- **Trade-off**: A dispatch loads each involved home organization's chart once. If profiling shows a cost, a version-checked cache (`organizations.chart_version`) is the follow-up.
- **Scope**: orgs, access, delivery, agents, intelligences, web chart.
- **Date**: 2026-10-23
- **Status**: active

### AD-018
- **Decision**: `trusted=true` on anything delivered to an AI means its author manages that AI in the AI's home organization (POS-10).
  - Room messages and room instructions are evaluated at delivery time from `messages.author_iid`, the AI itself for AI posts.
  - Platform events are evaluated at enqueue time: the sender, resolved through the connection to a user (AD-008), must manage the operator AI.
  - `from_self` is never trusted.
- **Reason**: The identities an AI obeys are the ones above it. This replaces "the owner and the owner's AIs".
- **Trade-off**: Sibling AIs of one user and peers in the chart are untrusted; after the upgrade a user's AIs stop trusting each other. v0.5 CLIs still describe trust as "your owner" until updated.
- **Scope**: delivery, agents (gateway, pending, history), connectors (inbound), channel.
- **Date**: 2026-10-23
- **Status**: active
- **Supersedes**:
  - the trust rule of AD-006 (content stays data);
  - AD-014 (the owner's own account is no longer a trust source; `from_self` stays untrusted, as AD-015 already said).

### AD-019
- **Decision**: Ownership columns after organizations:
  - **`agents.owner_id`** remains the AI's host user: the user whose machine and agent token run it. It stays part of the identity key (owner, machine, folder) of AD-010, but grants nothing.
  - **`rooms.owner_id` and `connectors.owner_id`** become dead foreign-key anchors (AD-012 cannot drop them). `creator_iid` and `org_id` carry the meaning.
  - **Account deletion** (new: users were only disabled before) runs in one transaction:
    1. re-points those anchors to an heir instance admin, so that `ON DELETE CASCADE` cannot delete organization-owned groups and connections;
    2. makes that heir an admin of each organization the user was the last admin of (OUPG-02);
    3. deletes the AIs hosted on the user's machines;
    4. deletes the user row.
  - The last active instance admin cannot be deleted (`last_instance_admin`, 409).
- **Reason**: POS-06 deletes a person's account everywhere. The existing cascades would otherwise delete what organizations own.
- **Trade-off**: Dead anchor columns stay in the schema. Hosted AIs die with their host's account (question Q2 of the design).
- **Scope**: users, orgs, agents, rooms, connectors, store.
- **Date**: 2026-10-23
- **Status**: active

### AD-020
- **Decision**: Organization placement travels as optional session headers: `X-Partyline-Org` (slug or id), `X-Partyline-Position` (percent-encoded name) and `X-Partyline-Parent` (@handle or id).
  - They are set by `partyline <harness> --org/--position/--parent`, or `PARTYLINE_ORG/POSITION/PARENT`.
  - The launcher consumes those variables and hands them to the channel as `PARTYLINE_LAUNCH_*`, so nested launches do not inherit them.
  - Without them the server applies defaults: the creator is the host user, the home organization is the user's default, and there is no position. v0.5 CLIs therefore keep working without a version check.
  - The welcome frame adds `organization`, `position` and `managers` and keeps `owner` (host user, deprecated).
  - A manager ending a session uses close 4002, which v0.5 CLIs already obey.
  - The default organization is a user setting changed in the web app. Agent tokens stay limited to `/api/v1/agent/`, which only lists organizations.
- **Reason**: Additive, header-based features are how this protocol evolves (AD-010, `X-Partyline-Features`); old CLIs degrade without errors.
- **Trade-off**: v0.5 CLIs cannot choose an organization or position and show stale trust wording.
- **Scope**: cli, channel, agents, api.
- **Date**: 2026-10-23
- **Status**: active

### AD-021
- **Decision**: Every account has a personal organization: on upgrade (OUPG-01) and when an account is created.
  - It is named after the user, with the user as `admin` holding a root position "Admin", and it is the account's default organization.
  - Organizations have an instance-unique slug.
  - Position names are unique per organization.
- **Reason**: A start without `--org` needs a home for the AI (ORG-04). New users would otherwise be unable to start sessions until someone invited them. `--org` and `--position` need unambiguous names.
- **Trade-off**: An instance with many users has as many organizations; renaming is free, and empty ones can be deleted.
- **Scope**: users, orgs, cli.
- **Date**: 2026-10-23
- **Status**: active

## Handoff

- **Feature**: organizations
- **Phase / Task**: Design and Tasks written (T1-T54, 10 phases); awaiting approval and the owner's answers to design questions Q1-Q4
- **Completed**: spec (`6ed8e88`), design, tasks
- **In-progress** (file:line): none
- **Next step**: approve design and tasks, then Execute Phase 1 (T1)
- **Blockers**: Q1-Q4 of the design (tasks implement the stated defaults)
- **Uncommitted files**: `.specs/STATE.md`, `.specs/features/organizations/{spec,design,tasks}.md`
- **Branch**: main
