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
  - An organization has members (users and AIs) with a permission and an org chart of positions. The permissions are:
    - `admin`: looks after the organization and may delete any identity of it;
    - `member`: takes part, and only an admin deletes it;
    - `guest`: sees the members, may DM anyone in the organization and take part in the groups it is added to, but cannot create groups.
  - A guest is the responsibility of its inviter, stored as `org_members.inviter_iid`. The inviter is whoever invited it, or, for an AI started with `--parent`, the AI that started it.
  - Any member or guest may invite guests (no position, no cap on count or depth), so responsibility forms a chain. Only admins invite with another permission or a position.
  - An AI started by another AI joins as `guest`; one started by a user joins as `member`.
  - Access rules:
    - A group is posted to by its room members and managed by its creator and the org's admins.
    - A DM is posted to by its two participants and viewed by the managers of an AI participant in its home organization.
    - A connection is viewed by the org's members and managed by its admins; only its operator sends through it.
    - An AI is seen by the members of the organizations it belongs to and by its host user.
    - An identity is edited, ended and moved below by those who manage it (AD-017).
    - Deleting B in organization X (POS-05) is allowed to an admin of X, or, when B is a guest of X, to B's inviter, anyone up its inviter chain, or anyone who manages one of them. What it does follows ownership (POS-06): an AI whose home is X is deleted for good, while a person or an AI of another organization is only removed from X. Nobody deletes another identity's account; a user may delete their own (AD-019).
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
    - B has no position and A is B's creator or manages B's creator in X, following the creator chain transitively; or
    - B is a guest and A is B's inviter or manages B's inviter in X, following the inviter chain.
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
- **Decision**: Ownership columns and account deletion after organizations:
  - **`agents.owner_id`** remains the AI's host user: the user whose computer and agent token run it. It stays part of the identity key (owner, machine, folder) of AD-010, but grants nothing.
  - **`rooms.owner_id` and `connectors.owner_id`** become dead foreign-key anchors (AD-012 cannot drop them). `creator_iid` and `org_id` carry the meaning.
  - **Deleting another identity** never deletes an account: a person is removed from the organization (AD-016).
  - **Self-deletion** (`DELETE /api/v1/me` with the password; users were only disabled before) runs in one transaction:
    1. the heir instance admin becomes an admin of each organization the user was the last admin of (OUPG-02);
    2. the anchors are re-pointed to the heir, so that `ON DELETE CASCADE` cannot delete organization-owned groups and connections;
    3. the AIs hosted on the user's computers stay in their organizations as unlinked identities owned by the heir (`folder = ''`, never adoptable). Deleting a person never touches AIs;
    4. the user row is deleted.
  - The last active instance admin cannot delete their account (`last_instance_admin`, 409).
- **Reason**: POS-06 as answered by the owner (2026-10-23): what "delete" removes follows ownership, and the account belongs to the person. The existing cascades would otherwise delete what organizations own.
- **Trade-off**: Dead anchor columns stay in the schema. A self-deleted user's AIs stay offline until an admin (or a manager, for a guest) deletes them.
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

### AD-022
- **Decision**: Machines stop being a visible concept.
  - A login links a computer to the user.
  - The server keeps the hostname only inside the AI identity key (AD-010), to tell the same folder on two computers apart.
  - The web contacts' machine › folder tree, the machine row of profiles, machine renaming (`PUT /api/v1/machines/{hostname}`, `machines.label`, now a dead column), `GET /api/v1/agent/contacts/tree` and the `partyline agents` machine tree are removed.
  - AIs are listed by organization instead.
- **Reason**: The owner (2026-10-23): organizations are now how AIs are found and grouped; the machine is an implementation detail.
- **Trade-off**: Two computers with the same folder show two AIs that differ only by folder label and history. v0.5 `partyline agents` prints "not found".
- **Scope**: intelligences (supersedes TREE-01, TREE-03, TREE-04 and the machine grouping of TREE-02; the `~` folder label stays), contacts, cli, web-app.
- **Date**: 2026-10-23
- **Status**: active

### AD-023
- **Decision**: Whoever can see a DM as a user resumes it after the loop guard paused it (DLV-09).
  - They are the DM's human participants and whoever manages an AI participant in that AI's home organization (POS-03).
  - A group is resumed as before, by `manage`: its creator and the admins of its organization.
  - An AI never resumes, in any room: agent tokens reach no user route, and the service refuses an AI principal as well.
  - `Room.can_resume` tells the caller whether `POST /rooms/{id}/resume` is theirs; the web app's paused banner offers Resume exactly then.
  - Nobody manages a DM and `dmLevel` is unchanged: updating, deleting or changing the members of a DM stay refused to everyone.
- **Reason**: v0.6.0 required `manage` to resume, and AD-016 gave nobody `manage` on a DM. A DM between two AIs that the guard paused stayed dead: the user who created both AIs, an admin of their organization, got 403, and the AIs cannot resume themselves without defeating the guard.
- **Trade-off**: Every manager of either AI of a DM may clear a pause the other manager may not agree with. Resuming is not audited, as before.
- **Scope**: access (`CanResume`), rooms (`Resume`, `Room.CanResume`), api (`dto.Room.can_resume`), web-app (paused banner).
- **Date**: 2026-10-24
- **Status**: active
- **Amends**: AD-016 (who acts on a DM) and DLV-09 of the core-platform spec.

### AD-024
- **Decision**: The web app does not mark an identity by its kind. An AI, a user and an external person look the same wherever a name appears:
  - **No badge**: no AI or person badge next to a name in the conversation list, the conversation header, the timeline, member and participant lists, pickers, mention suggestions, the command palette, the profile panel and page, the contacts and the org chart. The platform of an external person is a fact of its profile (the App row of the panel, the connector row of the page), not a badge. "You" stays on the viewer's own name: it says whose name it is, not what it is.
  - **One list**: a room's members are one list in the order they joined, and the pickers one list by name (AIs and users together); there are no "Agents" and "Users" sections. The screen-reader label "Agent" or "Person" before an author's name goes too.
  - **One palette**: an avatar fallback is tinted from the identity id alone, from the same eight soft tints (the warm family for people and the cool one for AIs are gone), so the same id has the same tint whatever its kind. The `--ai` colour token goes with the badge.
  - **No "Kind" row** in the profile panel and page. What is true of anyone stays: online status, the app it speaks from, bio, organizations.
  - **Neutral words** where a text only labelled identities ("Agents" and "AIs" in headings and pickers, "Users" in the contacts) say "Contacts", "Members" or "On this instance". Where the distinction is the function (start or connect an AI in a folder, merge or delete an AI, the CLI, trust, tokens, connections) the words stay.
  - **The @handle stays** next to or behind every name (a tooltip that opens on keyboard focus, or inline in detail views). It is what still prevents impersonation by display name, since trust never reads names (AD-006, AD-018).
  - **The server keeps `kind` and `role`** on every identity: the loop guard, delivery, presence (only an AI has a live session), the lifecycle actions of an AI and the Markdown rendering of an AI's messages (UI-09, content is data) need the distinction.
- **Reason**: The product owner (2026-10-24, after seeing the "IA" badge after names): partyline mixes humans and machines as equals, "everything a human can do, an AI can do too", so the interface must stop marking AIs.
- **Trade-off**:
  - A name that looks human no longer says "AI" at a glance; only the @handle (tooltip or detail view) and the facts of the profile (app, presence) do.
  - Lists, tints and labels no longer tell AIs from people; the visible differences left are the functional ones (presence and app for an AI, Markdown in its messages).
- **Scope**: web-app (every component that shows a name), intelligences (PROF-08, PROF-09, GRP-03 and its impersonation assumption), messenger-polish (MEN-01, PRF-03), organizations (CHART-01), web-app (UI-05).
- **Date**: 2026-10-24 (decided by the product owner)
- **Status**: active
- **Supersedes**: the kind badge that PROF-09 and the "Impersonation by display name" assumption of the intelligences spec required.
- **Amends**: PROF-08 (the tint comes from the id alone), PROF-09, GRP-03, MEN-01, PRF-03, CHART-01 and UI-05 (their text now says so). The intelligences design is amended where it names the badge; its task plan, contract notes and validation report stay as the record of what was built then.

### AD-025
- **Decision**: The core never imports the app. Partyline is a funnel with a messenger on top of it, and the funnel must work, and be testable, without the messenger.
  - **Core** (the funnel): integrations → per-AI queues → AI sessions → sends. `connectors` and its kinds (`whatsapp`, `telegram`, `discord`, `webhook`, `safehttp`, `connectortest`), `platform` (the per-AI queue), `agents` (registry, presence, the session gateway), `identities`, `channel` and `channel/mcp` (the session's end).
  - **App** (the messenger): `rooms`, `conversations`, `delivery` (the room → AI rules), `live`, `webui`.
  - **Shared kernel**: `orgs`, `access`, `intelligences`, `users`, `authn`, `audit`, `events`, `handles`, `validation`, `attachments`, `avatars`, `secrets`, `api/dto`, `api/httpx`. **Infra**: `store`, `store/db`, `config`, `app` (the wiring), `api`, `api/convert`, `cli`, `cliconfig`, `harness`, `devices`, `testkit` and the small process packages (`datalock`, `memlimit`). The kernel is what both sides share; infra builds, wires and exposes both.
  - "Never imports" counts the whole chain: a core package must not reach an app package through any other package, kernel or infra included. Test files do not count.
  - `internal/archtest` enforces it in `make check`, from a table that places every package of `internal/` in a layer (a new package is a decision, and fails the test until it is placed). A new core → app import fails with the chain of imports that leads to the app.
  - The imports that still break it are an explicit allow-list in that test: `agents` → `rooms`, `delivery` and `api/convert` (the session gateway builds the room backlog of an AI, checks its room membership and puts messages on the wire in the shape of the HTTP API). **The list only shrinks**: an entry whose import is gone, or that no longer leads to the app, fails the test until it is removed, and so does a list longer than its ceiling. An exception is never added: the code moves instead, into the app or behind an interface that the app implements and the wiring registers.
  - The way the exceptions go (architecture review, 2026-10-24): a feed seam in `agents` (the gateway loops over feeds; the core implements the platform feed and the app registers the room feed when it wires), to be done once the message-edit work that touches `delivery` has merged.
- **Reason**: The owner's rule, written down after the architecture review of 2026-10-24. AD-015 already made platform chats stop being room members, and the review found that only the session gateway still breaks the rule at import level. The rule can therefore be enforced now, and the last exceptions removed one step at a time, instead of drifting back with every feature.
- **Trade-off**:
  - The check sees imports, not data. All packages share one sqlc package (`store/db`), so a core package can still query the room tables: deleting a connection removes its room memberships, file links resolve through `GetAttachmentRoom`, and the AI registry lists room states and deletes room memberships when an AI is deleted. Splitting `db` by layer is a later step, and only then can the check see those.
  - `access` holds both the AI and connection rules (kernel) and the room levels (app), and is placed in the kernel, so the check cannot see a core package using room levels through it. Splitting it is a later step too.
  - A new package under `internal/` has to be placed in a layer before it merges.
- **Scope**: every package of `internal/` (its layer) and `internal/archtest`.
- **Date**: 2026-10-24
- **Status**: active
- **Builds on**: AD-015 (platform chats are no longer room members).

### AD-026
- **Decision**: A message can be edited by its author and deleted by its author or, in a group, by whoever manages it; the message is never removed from its conversation.
  - **Edit**: the text of a message written by a user or an AI is replaced in place (`messages.edited_at` is set); there is no time limit and no history of earlier texts. Files keep their attachments and only the caption changes.
  - **Delete**: a soft delete (`messages.deleted_at`). The text and the attachment rows are removed, the blobs nothing references any more are released as a deleted room's are, and the row stays as a "message deleted" placeholder with its id, sequence number, author, time and `reply_to`. Every read (history, pending, backlog, feed, conversation preview) returns the placeholder.
  - **Who**: an author edits and deletes their own (people and AIs alike; an AI acts as itself, never through its host). A group's creator and its organization's admins also delete any message but a system notice; nobody manages a DM (AD-016, AD-023), so there only the author deletes. A caller who cannot view the room gets 404, one who can and may not 403.
  - **Live**: one event, `events.MessageUpdated`, becomes the web frame `room.message_updated` (the message as it is now, under the room's view rule) and, for the online AI sessions of the other AI members whose delivery mode wants the message before or after the change, the agent frames `message_edited` and `message_deleted`. Agent frames are additive, never acknowledged and never replayed; clients that do not know a frame type ignore it (`link.handle`), which keeps v0.5 and v0.6 CLIs working.
  - **Tools**: `edit_message` and `delete_message`, on `PATCH` and `DELETE /api/v1/agent/messages/{id}`, for an AI's own messages, counted against its 30 posts a minute.
- **Reason**: The product owner (2026-10-24) asked to edit and delete messages like any messenger does. A soft delete keeps replies, order, unread counts and the read cursors of AIs valid with no renumbering, and the placeholder tells readers that something was there.
- **Trade-off**:
  - The earlier text of an edited message is not kept: nobody, a manager included, can see what it said.
  - A session that was offline for an edit or a delete never hears about it, and a polling harness (turn-end hooks, `wait_for_messages`) does not hear about the ones after it received the message; they read the current state later.
  - A deleted message remains in the room as a row until the room's retention removes it.
  - A web client that missed the live frame while its socket was down shows the old text until its timeline is reloaded.
- **Scope**: store (`0023`), rooms (`EditMessage`, `DeleteMessage`), events, delivery (dispatcher), agents (gateway frames), live, api (routes, DTOs), channel (frames, tools), conversations (preview), web-app (message menu, composer, placeholder), messenger-polish (EDT-01..EDT-10, DLT-01..DLT-10).
- **Date**: 2026-10-24 (decided by the product owner)
- **Status**: active
- **Amends**: AD-015 (rooms hold conversations that can now change after they were sent; platform messages and sends are not editable) and the agent channel protocol (two additive frame types).

### AD-027
- **Decision**: Web push notifications are an app-layer feature of the messenger, built on the standard Web Push stack (RFC 8291 payload encryption, RFC 8292 VAPID).
  - **Layer**: `internal/webpush` sits in the app layer, next to `live`, because it reads rooms and users through the shared `store/db`. It never becomes a dependency of `connectors`, `platform`, `agents`, `identities` or `channel` (AD-025), and the core → app allow-list is unchanged.
  - **Key file**: one VAPID P-256 key pair, generated on first start with its private key sealed by the instance `secrets.Box` into `<data dir>/vapid.key` (mode 0600), like `secret.key`; only the public key is served. A corrupt or unreadable file is replaced, every stored subscription is dropped (they were bound to the old key) and the server still starts (PUSH-26); it is backed up with `secret.key` and the database.
  - **Subscriptions**: one row per push endpoint, tied to the user and the session that registered it. A logout, a revoked device, a password change or an account deletion removes it (PUSH-07), and a push service answering 404/410 removes it too (PUSH-12); the browser re-registers its existing subscription on the next signed-in load (PUSH-28).
  - **Focus rule**: the service worker suppresses the notification for the conversation a focused window already shows, comparing the window's URL path; the live socket stays server → client only and gains no focus frame (PUSH-17).
- **Reason**: A phone and a desktop must hear about a message while the app is closed, and the browser is the only app Partyline owns (AD-015 retired the WhatsApp copies of AI DMs). The standard stack needs no push service of our own, and keeping the worker in the app layer keeps the funnel usable without the messenger.
- **Trade-off**:
  - The pair is one per instance and is not rotated; losing `vapid.key` invalidates every stored subscription (they are dropped, and browsers re-subscribe).
  - iPhones receive notifications only for a web app added to the Home Screen (iOS 16.4 or later) and only while `PARTYLINE_PUBLIC_URL` is https, because Apple's push service refuses a non-https VAPID subject.
  - Notifications are ephemeral: there is no outbox and no retry, so a full queue or a slow push service drops one instead of delaying the message post.
  - A subscription dies with its session, so a device is quiet after a logout until its next signed-in load registers it again.
- **Scope**: store (migration `0025`), webpush, api, app (wiring), webui (manifest, worker, icons), web-app (Settings, service worker, registration), docs.
- **Date**: 2026-10-25
- **Status**: active (the three `Confirmed? n` defaults of the web-push spec still await the product owner)

## Handoff

- **Feature**: organizations
- **Phase / Task**: Design and Tasks revised after the owner's answers and the guest refinement (T1-T58, 10 phases); awaiting approval
- **Completed**: spec (`6ed8e88` + owner's answers), design, tasks
- **In-progress** (file:line): none
- **Next step**: approve design and tasks, then Execute Phase 1 (T1)
- **Blockers**: none (no open question)
- **Uncommitted files**: `.specs/STATE.md`, `.specs/features/organizations/{spec,design,tasks}.md`
- **Branch**: main
