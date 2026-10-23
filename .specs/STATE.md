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
- **Status**: active

### AD-006
- **Decision**: Message content is data, never instructions. Every message delivered to an agent carries a server-computed, recipient-relative `trusted` flag (true only when the author resolves to the recipient agent's owner: the owner in the web app, the owner's agents, or the owner's verified chat identities). Partyline never declares the `claude/channel/permission` capability.
- **Reason**: Channels inject text into a live coding session; anyone in a bridged group could attempt prompt injection. Trust must be explicit and not forgeable by message text.
- **Trade-off**: Teammates' messages show as untrusted to your agents unless a later feature adds per-user trust.
- **Scope**: core-platform, agent-channel, connectors.
- **Date**: 2026-10-19
- **Status**: active

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
- **Status**: active

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
- **Status**: active
- **Amends**: AD-011 (who views a DM); PEOP-04 and DM-08 of the intelligences spec now say it.
- **Context**: Verifier iteration 1 of the intelligences feature (gap 3): PEOP-04 ("nobody else") and DM-08 ("use grantees view") contradicted each other. Pinned by `TestUseGranteeOfAnAIReadsItsPlatformDMsAndPeople`.

### AD-014
- **Decision**: A message written from a connector's own account on the platform itself (typed on its phone or web client, not the echo of a Partyline send, ACC-05) is trusted by the owner's AIs as the owner's only when the connector's operator is the owner, a user. When an AI operates the account the message is authored by that AI, carries no user and is never trusted as the owner's. Echoes of Partyline's own sends are still dropped, and AIs of other users never trust it.
- **Reason**: The owner's own account is the owner's: what is typed on it is the owner. An AI's account may be held by anyone with its phone or session, so the text proves nothing about who wrote it, and AD-006 makes trust depend on a resolved owner.
- **Trade-off**: The owner who types on an AI's account is not recognised as the owner by that AI (or the owner's other AIs) and must write from the web app or from a verified chat identity. A catch-all connector now stores these messages where CON-12 dropped them.
- **Scope**: connectors (ACC-05), delivery (`trusted`), agent-channel.
- **Date**: 2026-10-22 (decided by André)
- **Status**: active
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

## Handoff

- **Feature**: intelligences
- **Phase / Task**: Tasks written (T1-T54, 9 phases); awaiting approval and André's answers to design Spec issues 2, 4, 5, 6
- **Completed**: spec (`be30f10`), design (`c48b9c1`), tasks
- **In-progress** (file:line): none
- **Next step**: approve tasks, then Execute Phase 1 (T1)
- **Blockers**: Spec issues 2, 4, 5, 6 need confirmation (tasks implement the recommended fixes)
- **Uncommitted files**: none
- **Branch**: main
