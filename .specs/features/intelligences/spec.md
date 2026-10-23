# Intelligences Specification

## Problem Statement

Partyline treats agents, people and chats as different kinds of things. Agents are named per session, so resuming a session (`partyline opencode -s <id>`) creates a new agent, and the two processes of one OpenCode session can split into two agents. There is no way to talk privately with an agent, no view of which machine and folder an agent lives in, and setup walks through every harness one by one. Reported 2026-10-21 after v0.3.0.

The product premise changes: Partyline is a messenger for **intelligences**. A human and an AI are the same kind of participant. Each intelligence has one identity and speaks through one or more **apps**. For an AI, the identity is the folder it works in on a machine, and its apps are harnesses (Claude Code, OpenCode, …). For a human, the identity is the person, and the apps are the web app, WhatsApp, Telegram, Discord and so on. Intelligences talk in private conversations (DMs) and groups, like a messenger.

## Goals

- [ ] Every AI is exactly one identity per (machine, folder): resuming, restarting or switching harness in that folder keeps the same identity, history and groups.
- [ ] One identity is open in at most one live session; a second session in the same folder asks before taking over.
- [ ] The owner sees intelligences as Machine › folder tree › AI, and people separately.
- [ ] Anyone can open a private conversation with any intelligence they can see, and groups keep working (existing rooms become groups without loss).
- [ ] People who write through WhatsApp, Telegram or Discord appear as intelligences (contacts), not as anonymous chat members.
- [ ] A platform account (a WhatsApp number, a Discord bot) belongs to one intelligence: talking to that account is talking to that intelligence.
- [ ] `partyline login` approves a machine; `partyline setup` opens a checklist menu of harnesses.
- [ ] Every intelligence (a user's own account, an AI, an external person) is one row of one identity model with the same profile (photo, display name, @handle, bio), editable by whoever owns it - including the AI itself.
- [ ] The web app is a messenger: a left sidebar lists the caller's conversations; any intelligence can start a DM with any intelligence it can see; users and AIs can create groups.

## Out of Scope

| Feature | Reason |
| --- | --- |
| Giving AIs their own phone numbers or bot accounts automatically | Accounts are created by the user on each platform; Partyline only connects them |
| Federation between Partyline instances | Single-instance product (AD-001) |
| Read receipts and typing indicators sent to external platforms | Platform-specific and not requested |
| Voice/video calls | Not requested |
| Detecting file conflicts between two harness sessions in one folder | Outside Partyline's control; the takeover prompt is the guard |
| Moving history between instances | Not requested |
| An AI starting a brand-new external conversation with a person who never wrote (cold outreach) | Spam risk; AIs reach people through existing conversations |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| What defines an AI identity | (owner, machine, canonical folder): absolute path with symlinks resolved; the machine is the hostname the CLI sends | "The folder and its context files are the intelligence" (André) | y |
| Harness role | A harness is an app of the AI; it is shown as the identity's current app, never part of its name | Same AI can be opened in Claude Code or OpenCode | y |
| Second live session in the same folder | The launcher asks "<name> is open in another session (<harness>, since <time>). Take over? [y/N]"; yes → the new session becomes the identity's live session and the old one is disconnected with a notice | André chose "ask and take over" | y |
| Second session without a terminal (no TTY) or without the launcher | The live connection is refused with `identity_busy` unless `--take-over` / `PARTYLINE_TAKEOVER=1` is given; the session works without push and its tool results say why | Never take over silently | n |
| Tools-only processes (Codex, Gemini, the tools half of pi/OpenCode) | Act as the identity of their folder on their machine; they are not exclusive (no live connection to own) | The folder is the identity; removes the fragile session-key allocation of v0.3 | n |
| Default name | Display name = folder base name with the first letter upper-cased (`discord` → "Discord"); handle = slug of the base name, unique across the instance (`-2`, `-3` on collision) | Readable and editable | n |
| Home folder or `/` | Allowed like any folder (identity of `~`), display name = the folder base name; no warning | Consistent with "folder = identity" | n |
| Renamed or moved folder | Becomes a new identity; the owner can merge the old identity into the new one (history, groups and settings move, the old one is deleted) | Paths are the identity; merge covers moves | n |
| Legacy agents (created before this feature, no folder) | Kept as unlinked identities. The first session from a folder adopts the owner's unlinked identity whose handle equals the folder's handle, or the one named by `PARTYLINE_AGENT`/`--as`; the rest stay listed under "Unlinked" for merge or delete | Existing rooms (e.g. production routes) keep working without manual steps | n |
| v0.3 projects | Removed. A project room member becomes a direct member of the project's most recently connected agent | Projects are superseded by folders | n |
| v0.3 temporary/fixed and archiving | Removed: every identity is permanent until deleted; legacy temporaries become unlinked identities | Identity is the folder, not the session | n |
| Identity model | One `intelligences` table for every participant: kind `human` (a user account or an external person) or `ai`; same profile fields for all (display name, @handle, avatar, bio); users, AIs and people point to their intelligence | "Everything in the same table" (André) | y |
| Handles | Unique across the instance and case-insensitive (`@andre`, `@atlas`); defaults from username, folder base name or platform name, with `-2`, `-3` on collision; external people get handles too | One mention namespace for humans and AIs | n |
| Avatars | PNG, JPEG or WebP up to 2 MB, center-cropped and resized to 256×256 PNG on the server; initials on a colored background when none | Simple and consistent | n |
| Who edits a profile | A user edits their own; an AI's owner edits it; an AI edits its own profile with the `update_profile` tool; an external person's profile is edited by users who own or may `use` its connector (local to the instance) | "They personalise their identity like I do mine" | y |
| Impersonation by display name | Display names are not unique; the UI always shows the kind badge (AI / person + platform) and the @handle next to names; trust never depends on names (AD-006) | An AI could name itself like its owner | n |
| AI-initiated conversations | An AI may open a DM with any intelligence its owner can see (users of the instance, its owner, other visible AIs, people only through ACC rules) and may create groups with members its owner could add | "It can start a conversation with me without me ever talking to it" | y |
| Web layout | Messenger layout: a left sidebar with the conversation list (search, unread badges, "New conversation" and "New group"), the open conversation on the right; the current navigation moves to a compact menu | André asked for a left sidebar of conversations | y |
| Rooms | Rooms become **groups** in the UI; a DM is a room of kind `dm` with exactly two intelligences; API keeps `/rooms` with a `kind` field | AD-002 (room is the only routing primitive) stays | n |
| External person identity | Each external account seen by a connector (sender) is a person intelligence keyed per connector (AD-008); linking it to a user (existing pairing) makes that user the person | Keeps AD-008 trust boundaries | n |
| Platform account ownership | Each connector has an operator intelligence: the owner (default) or one of the owner's AIs | Atlas's Discord bot is Atlas's app | n |
| Platform DM chats | A DM chat on a connector is the DM between the person and the connector's operator; group chats are bridged into groups whose participants include each person seen | Messenger semantics | n |
| Messages from the operator's own account on the platform | Shown as written by the operator (e.g. André typing on his phone in WhatsApp) | The account is the operator's app | n |
| AI writing through a human operator's account | Allowed in groups/DMs where the AI is a participant; the outbound text is signed with the AI's name (existing signature setting) | Recipients must know an AI wrote it | n |
| AI writing through its own account (operator = that AI) | Sent unsigned | It is the AI's own app | n |
| Unread counts | Per user, per conversation; an AI's unread state is its delivery cursor (existing) | Messenger list needs badges | n |
| Notify a human on another app | A user may pick one of their linked app accounts to receive DMs written to them by AIs (copy of the message, link back) | "Agent DMs me on WhatsApp" use case | n |
| CLI | `partyline login` (device approval, `--token-stdin` fallback) and `partyline setup` (checklist TUI); `setup --browser` keeps working as `login` + `setup` | André's request; backwards compatible | y |
| TUI library | Plain ANSI raw-mode menu inside the binary, no new heavy dependency; non-TTY falls back to flags (`--harness`, `--yes`) | Single small binary (AD-001) | n |

**Open questions:** none - all resolved or logged above.

---

## User Stories

### P1: One identity per folder ⭐ MVP

**User Story**: As a user, I want each folder I run an AI in to be one stable identity, so that restarting, resuming or switching harness keeps who it is.

**Why P1**: Reported bug (`-s` resume created a new agent) and the core of the new premise.

**Acceptance Criteria**:

1. WHEN a session connects with machine M and folder F (canonical) for owner O THEN the system SHALL resolve the identity (O, M, F), creating it on first sight with the default display name and handle. (AID-01)
2. WHEN the same folder connects again from any harness THEN the system SHALL resolve the same identity id, and its `app` SHALL become that harness. (AID-02)
3. WHEN two different folders have the same base name THEN the system SHALL give them distinct handles (`name`, `name-2`) and the same default display name. (AID-03)
4. WHEN a tools-only process (Codex, Gemini, tools half of pi/OpenCode) calls a tool with machine M and folder F THEN the system SHALL act as identity (O, M, F). (AID-04)
5. IF the CLI cannot determine a folder THEN the system SHALL reject the connection with 422 `folder_required`. (AID-05)
6. WHEN the owner renames an identity (display name 1–64 characters, handle 1–40 of `a-z0-9-` unique across the instance) THEN the new names SHALL appear in timelines, member lists, mentions and outbound signatures. (AID-06)
7. The CLI SHALL send the canonical folder (absolute, symlinks resolved, no trailing slash) and the hostname with every connection and tool call. (AID-07)

**Independent Test**: Start `partyline opencode` in `~/x`, exit, run `partyline opencode -s <id>` and then `partyline claude` in `~/x`: one identity, app changes, groups and history kept.

---

### P1: One live session per identity ⭐ MVP

**User Story**: As a user, I want a second session in the same folder to ask before taking over, so that two sessions never act as one intelligence by accident.

**Why P1**: "Two intelligences cannot occupy the same identity."

**Acceptance Criteria**:

1. WHEN the launcher starts in folder F whose identity has a live session THEN it SHALL print "<display name> is open in another session (<harness>, since <HH:MM>). Take over? [y/N]" and wait for an answer. (SES-01)
2. WHEN the user answers yes THEN the launcher SHALL start the harness with takeover set, and the server SHALL make the new live connection the identity's session and close the old one with reason `taken_over`. (SES-02)
3. WHEN the user answers no or presses enter THEN the launcher SHALL exit with code 1 without starting the harness. (SES-03)
4. IF a live connection arrives for an identity that already has a live session and takeover is not set THEN the server SHALL refuse it with `identity_busy`, and the channel SHALL answer every tool call with a message saying the identity is open elsewhere and how to take over. (SES-04)
5. WHEN a session is taken over THEN the old channel SHALL tell its harness once "This intelligence was opened in another session; Partyline is disconnected here." and stop delivering messages. (SES-05)
6. WHEN the live session ends, because its connection closes or is dropped, THEN the identity SHALL be offline within 5 seconds and the next session SHALL connect without a prompt; a connection that goes silent without closing (network loss) is dropped by the server's ping, sent every 25 seconds and answered within a 10-second write timeout, so the identity is offline within 35 seconds. (SES-06)
7. WHERE the launcher runs without a terminal THEN it SHALL not prompt and SHALL start the harness without takeover unless `--take-over` or `PARTYLINE_TAKEOVER=1` is given. (SES-07)
8. WHILE an identity has no live session the system SHALL keep its undelivered messages and deliver them as backlog to the next session (existing 50-per-conversation cap and notice). (SES-08)

**Independent Test**: Open `partyline claude` in `~/x`; open a second one in `~/x`: prompt; "n" exits; "y" takes over and the first prints the notice.

---

### P1: Machine › folder tree ⭐ MVP

**User Story**: As a user with several machines and many folders, I want to see my AIs as a tree of machines and folders, so that I know where each one lives and which is inside which.

**Why P1**: Requested.

**Acceptance Criteria**:

1. The contacts page SHALL show AIs grouped by machine, and within a machine as a folder tree where an identity whose folder is inside another identity's folder is shown as its child. (TREE-01)
2. The tree SHALL collapse folder chains that contain no identity into one path label shown before the row's name (e.g. `projects/acme/agents › Discord`) and SHALL write the home folder as `~`: the node of the home folder is named `~` and the folder column writes folders under it as `~/…` (`~/atlas`). (TREE-02)
3. Each identity row SHALL show display name, online state, current app (harness) and last-seen time; machines with an online identity SHALL sort first, then by name. (TREE-03)
4. WHEN the owner renames a machine (display label, 1–64 characters) THEN the label SHALL replace the hostname in the tree and filters, keyed by hostname. (TREE-04)
5. The page SHALL offer a text filter that matches display name, handle, folder or machine and keeps the matching rows' ancestors visible. (TREE-05)
6. Identities without a folder (legacy) SHALL appear in an "Unlinked" section with merge and delete actions. (TREE-06)
7. `partyline agents` SHALL print the same machine › folder tree in the terminal with online markers. (TREE-07)

**Independent Test**: Identities in `~/a`, `~/a/b`, `~/c/d/e` on two machines render as two machine groups, `b` nested under `a`, `c/d › e` collapsed.

---

### P1: Contacts and private conversations ⭐ MVP

**User Story**: As a user, I want a contact list of every intelligence I can reach and to talk privately with any of them, like a messenger.

**Why P1**: New premise ("WhatsApp for AI").

**Acceptance Criteria**:

1. The contacts page SHALL list, for the caller, their own AIs (tree), AIs shared with them, other users of the instance, and the people their connectors have seen (by connector). (CONT-01)
2. WHEN a user opens "Message" on a contact THEN the system SHALL open the existing DM between the user and that intelligence, creating it if none exists (exactly one DM per pair). (DM-01)
3. A DM SHALL have exactly two intelligences; IF a caller who can view the DM tries to add, change or remove its members THEN the system SHALL respond 422 `dm_members_fixed`; a caller who cannot view it gets 404, so a stranger cannot tell that the DM exists. (DM-02)
4. WHEN a user writes in a DM with an AI THEN the AI's live session SHALL receive it with `conversation="dm"` and the sender, and the AI's `reply` SHALL appear in the DM. (DM-03)
5. WHEN an AI calls `send_message` with `to: <@handle or intelligence id>` THEN the system SHALL post in the DM between that AI and the target, creating it even if they never talked, IF the AI's owner can see the target; otherwise it SHALL answer `contact_not_found`. (DM-04)
6. IF the target of a DM is a person (external) THEN the message SHALL go out through the connector account that person wrote to, only when that account's operator is the sender intelligence; otherwise the system SHALL answer `no_route_to_person`. (DM-05)
7. The conversations page SHALL list DMs and groups together, newest activity first, with the caller's unread count per conversation. (DM-06)
8. WHEN a user opens a conversation THEN the system SHALL mark it read up to its newest message for that user. (DM-07)
9. A DM's visibility SHALL be limited to its two intelligences (for an AI: its owner and users granted `use` on it by name, also for its DMs with external people, AD-013; a public `use` grant does not give view); grants on a DM SHALL be refused with 422. (DM-08)

**Independent Test**: Open a DM with an AI from the web, write "oi", the session receives it and replies; the AI DMs the user back with `send_message to:`.

---

### P1: Groups ⭐ MVP

**User Story**: As a user, I want my existing rooms to work as groups of intelligences, with people from bridged chats shown as participants.

**Why P1**: Keeps everything that works today.

**Acceptance Criteria**:

1. The system SHALL present every existing room as a group, keeping members, settings, history, bridges and routing unchanged. (GRP-01)
2. A group's participant list SHALL show intelligences (AIs, users) and, for each bridged chat, the people seen in that chat with their app (platform). (GRP-02)
3. WHEN a person writes in a bridged chat THEN the message SHALL be authored by that person's intelligence (display name, platform badge) instead of the chat. (GRP-03)
4. The system SHALL keep per-member delivery settings (all, mentions, trusted), keywords, signatures and broadcast for AIs in groups. (GRP-04)

**Independent Test**: After migration, an existing room with a WhatsApp group and an AI still delivers both ways, and the timeline shows the person's name with a WhatsApp badge.

---

### P1: People as intelligences ⭐ MVP

**User Story**: As a user, I want people who write through WhatsApp, Telegram or Discord to be contacts, so that humans and AIs are the same kind of participant.

**Why P1**: Symmetric premise (human = intelligence with apps).

**Acceptance Criteria**:

1. WHEN a message from an external account not seen before is routed into a conversation THEN the system SHALL create a person intelligence for (connector, external id) with the platform's display name; a message from a chat that is routed nowhere records the chat and no person (CON-07). (PEOP-01)
2. WHEN that external account is linked to a user (existing pairing, AD-008) THEN messages from it SHALL be authored by the user's intelligence, and the person entry SHALL show as that user's app. (PEOP-02)
3. The person page SHALL show the person's name, apps (platform accounts) and conversations the caller can see. (PEOP-03)
4. A person's visibility SHALL follow the connector's: users who own or may `use` the connector see its people, the account behind each and may edit them. A user who can view a conversation where the person was seen or wrote also reads the person's name, platform, avatar and the conversations they can view, without the account and without editing: that includes a user granted `use` on an AI that operates a connector account, who reads the AI's DMs with the people who wrote to it, without any access to the connector (AD-013). Nobody else sees a person, admins included. (PEOP-04)
5. WHEN a person's platform display name changes THEN the system SHALL update the person's name on the next message, unless the user renamed the contact. (PEOP-05)
6. The system SHALL compute `trusted` exactly as before: true only when the author resolves to the recipient AI's owner (user or the owner's AIs). (PEOP-06)

**Independent Test**: A new WhatsApp sender in a bridged group appears as a contact; after pairing, their messages show as the user.

---

### P1: Platform accounts belong to an intelligence ⭐ MVP

**User Story**: As a user, I want a connector account to be the app of one intelligence, so that DMing Atlas's Discord bot is DMing Atlas.

**Why P1**: The motivating use case (Atlas on Discord).

**Acceptance Criteria**:

1. Each connector SHALL have an operator: its owner (default) or one AI identity the owner owns; WHEN the owner changes it THEN the system SHALL record an audit entry. (ACC-01)
2. WHERE the connector's auto-route includes DMs, WHEN a person writes to the connector account in a platform DM THEN the system SHALL deliver it in the DM between that person and the operator, creating the DM. (ACC-02)
3. WHEN the operator is an AI and it replies in that DM THEN the message SHALL go out through the account without signature. (ACC-03)
4. WHEN the operator is a user and an AI participant writes in a bridged conversation THEN the outbound text SHALL carry the AI's signature per its member setting. (ACC-04)
5. WHEN the operator writes from the platform itself (own account, e.g. phone) THEN the message SHALL be authored by the operator, and trusted by the owner's AIs as the owner's own only when the operator is the owner, never when it is an AI (AD-014). (ACC-05)
6. WHERE the connector's auto-route includes groups THEN each new group chat SHALL become a group with the operator as member and the chat bridged; WHERE it includes DMs THEN rule ACC-02 SHALL apply. (ACC-06)
7. The connector's apps SHALL appear on the operator's contact page ("Discord: @bot"). (ACC-07)

**Independent Test**: Set the Discord connector's operator to Atlas; a Discord DM to the bot reaches Atlas's session as a DM and its reply goes back unsigned.

---

### P1: One identity model and profiles ⭐ MVP

**User Story**: As any intelligence (a user, an AI, a person), I want one kind of profile - photo, name, @handle, bio - that whoever owns it can personalise, so that humans and AIs are the same kind of participant.

**Why P1**: Core of the unified premise.

**Acceptance Criteria**:

1. The system SHALL represent every user account, AI and external person as exactly one intelligence with id, kind (`human` or `ai`), display name, @handle, avatar and bio. (PROF-01)
2. The system SHALL keep @handles unique across the instance, case-insensitive, 1-40 characters of `a-z0-9-` starting with a letter. (PROF-02)
3. WHEN a user updates their own profile THEN the new display name (1-64), handle, bio (0-280) and avatar SHALL appear everywhere the user appears (sidebar, timelines, member lists, mentions, signatures). (PROF-03)
4. WHEN an AI calls `update_profile` with any of display name, handle, bio or an avatar file path THEN the system SHALL apply the same rules as PROF-03 to that AI and return the updated profile. (PROF-04)
5. WHEN an AI's owner edits the AI's profile in the web app THEN the same rules SHALL apply. (PROF-05)
6. IF an avatar is not PNG, JPEG or WebP, is larger than 2 MB, or cannot be decoded THEN the system SHALL respond 422 `avatar_invalid` and keep the old avatar. (PROF-06)
7. WHEN an avatar is accepted THEN the system SHALL store it center-cropped and resized to 256x256 PNG and serve it to users who can see the intelligence. (PROF-07)
8. WHERE an intelligence has no avatar the UI SHALL show its initials on a color derived from its id. (PROF-08)
9. The UI SHALL show, next to every display name, the @handle on hover/detail and a kind badge (AI, or the person's platform), so that an AI named like a human is still marked as an AI. (PROF-09)
10. IF a handle is taken THEN the system SHALL respond 409 `handle_taken`. (PROF-10)
11. WHEN users who may `use` a connector edit a person's profile THEN the edit SHALL win over later platform name changes (PEOP-05). (PROF-11)

**Independent Test**: Upload a photo for your account and for an AI; the AI renames itself with `update_profile`; both show everywhere with badges.

---

### P1: Messenger layout ⭐ MVP

**User Story**: As a user, I want a left sidebar with my conversations and to start DMs and groups from it, like a messenger.

**Why P1**: Requested.

**Acceptance Criteria**:

1. The web app SHALL show a left sidebar listing the caller's DMs and groups with avatar, name, last message preview (first 80 characters, sender prefix in groups), time and unread count, newest activity first. (LAY-01)
2. WHEN a new message arrives live THEN its conversation SHALL move to the top and its unread count SHALL increase unless it is open. (LAY-02)
3. The sidebar SHALL offer search over conversation names and members, "New conversation" (pick any visible intelligence → DM-01) and "New group" (name + members). (LAY-03)
4. WHEN a user creates a group THEN the system SHALL create it with the user as owner and the chosen members, applying existing add-member rules per member. (LAY-04)
5. On screens narrower than 768 px the sidebar SHALL be the first screen and opening a conversation SHALL show it full screen with a back button. (LAY-05)
6. Contacts, Connections (apps), Settings and the admin pages SHALL stay reachable from a menu in the sidebar header. (LAY-06)

**Independent Test**: The sidebar lists a DM with an AI and a group; a live message bumps the group to the top with a badge; on mobile the conversation opens full screen.

---

### P2: AIs create groups

**User Story**: As an AI, I want to create a group with intelligences I can reach, so that I can coordinate several participants.

**Why P2**: Requested ("can create groups") for users; AI creation completes the symmetry.

**Acceptance Criteria**:

1. WHEN an AI calls `create_group` with a name and members THEN the system SHALL create the group owned by the AI's owner with the AI and the members, applying the add-member rules of the AI's owner, and return its id. (AGRP-01)
2. IF any member cannot be added THEN the system SHALL create nothing and answer with the members it refused. (AGRP-02)

**Independent Test**: An AI creates a group with its owner and another AI; the group appears in the owner's sidebar.

---

### P1: Login and setup menu ⭐ MVP

**User Story**: As a user, I want `partyline login` to approve my machine and `partyline setup` to show a menu of harnesses, so that I pick only the ones I use.

**Why P1**: Requested; setup today walks harness by harness.

**Acceptance Criteria**:

1. `partyline login` SHALL run the device approval flow (code + `<instance>/devices`), save the instance and token, and print "Logged in as @<user> on <instance> (machine <hostname>)". (CLI-01)
2. WHEN `partyline login` finishes on a terminal and no harness is registered THEN it SHALL open the setup menu. (CLI-02)
3. `partyline setup` on a terminal SHALL show a checklist of all supported harnesses with state (installed on the machine or not, registered or not); installed or registered ones pre-selected, so enter changes nothing for them; arrows/j/k move, space toggles, `a` selects every installed harness (and clears them when all are selected), enter applies, q/esc cancels without changes. (CLI-03)
4. WHEN the user applies THEN setup SHALL register the selected harnesses, remove Partyline from deselected registered ones, show one result line per harness of the menu (changed or not) and exit non-zero when a change failed, and change nothing else. (CLI-04)
5. IF setup runs without a terminal THEN it SHALL not show the menu and SHALL use `--harness a,b` / `--all-detected` / `--remove a,b`, failing with exit 2 and a hint when none is given. (CLI-05)
6. IF the machine is not logged in THEN setup SHALL print "Run partyline login first" and exit 1. (CLI-06)
7. `partyline setup --browser` SHALL keep working (login, then the menu). (CLI-07)

**Independent Test**: `partyline login` approves, the menu opens with detected harnesses checked, deselect two, enter: only the selected are registered.

---

### P1: Migration ⭐ MVP

**User Story**: As an existing user, I want the upgrade to keep my rooms, routes and history working.

**Why P1**: Production data (WhatsApp routes) must not break.

**Acceptance Criteria**:

1. WHEN the server upgrades THEN rooms SHALL become groups, DMs SHALL be empty, and every message, member, grant, bridge and cursor SHALL remain. (MIG-01)
2. WHEN a session connects from a folder for an owner with an unlinked identity whose handle equals the folder's handle (or named by `PARTYLINE_AGENT`/`--as`) THEN the system SHALL link that identity to (machine, folder) instead of creating one, at most once. (MIG-02)
3. WHEN the server upgrades THEN each v0.3 project room member SHALL become a direct member for that project's most recently connected agent, and projects SHALL be removed. (MIG-03)
4. WHEN the server upgrades THEN chat-authored messages with a sender id SHALL be attributed to person intelligences created from (connector, sender id). (MIG-04)
5. The migration SHALL run in one transaction and SHALL leave the database unchanged if it fails. (MIG-05)

**Independent Test**: Migrate a copy of a v0.3 database with rooms, chats, agents and projects; routes deliver; old agent adopted on first connect.

---

### P2: Merge identities

**User Story**: As a user who moved or renamed a folder, I want to merge the old identity into the new one.

**Why P2**: Folder moves are occasional.

**Acceptance Criteria**:

1. WHEN the owner merges identity A into B (both theirs, A offline) THEN the system SHALL move A's group memberships (B's settings win on conflict), DMs (merged into B's DM with the same counterpart), cursors (max), grants and authored messages to B, delete A, and record an audit entry. (MRG-01)
2. IF A is online THEN the system SHALL respond 409 `identity_online`. (MRG-02)

**Independent Test**: Merge an unlinked identity into a folder identity; its groups and history appear under the new one.

---

### P2: DMs reach me on my other apps

**User Story**: As a user, I want DMs that AIs send me to reach my phone through one of my apps.

**Why P2**: Useful but not required for the premise.

**Acceptance Criteria**:

1. A user SHALL be able to choose one of their linked app accounts (or none) as "notify me here", among the accounts on connections the user operates (amended by connection-identity, AD-015). (NOTI-01)
2. WHEN an AI posts in a DM with a user who chose an app THEN the system SHALL send "<AI name>: <text>" to that account through its connector, only when the user operates that connector (amended by connection-identity, AD-015: no message leaves a connection on behalf of anyone but its operator), attachments as links to the instance, and mark the outbox like other outbound messages. (NOTI-02)
3. IF the chosen account's connector is down THEN the system SHALL retry like other outbound messages and the DM SHALL still be readable on the web. (NOTI-03)

**Independent Test**: Choose Telegram; an AI DMs you; the text arrives on Telegram.

---

## Edge Cases

- IF two sessions connect at the same instant for one folder with takeover THEN exactly one SHALL end up live and the other SHALL be closed with `taken_over` (SES-02).
- IF the folder path is longer than 1024 bytes or contains a NUL THEN the system SHALL reject it with 422 `folder_invalid` (AID-05).
- IF the same folder path exists on two machines THEN they SHALL be two identities (AID-01).
- WHEN an AI is deleted THEN its DMs SHALL be deleted and its group memberships removed (DM-08).
- IF a DM counterpart user is disabled THEN the DM SHALL stay readable and refuse new posts with 403 (DM-02).
- WHEN a person writes in a DM chat to a connector whose operator AI is deleted THEN the DM SHALL be with the connector owner (ACC-02).
- IF the setup menu receives an unknown key THEN it SHALL ignore it (CLI-03).
- IF the terminal is narrower than 40 columns THEN the menu SHALL still render one harness per line, truncating titles (CLI-03).

---

## Implicit-requirement sweep

| Dimension | Coverage |
| --- | --- |
| Input validation & bounds | AID-05, AID-06, TREE-04, folder edge cases |
| Failure / partial-failure | MIG-05, NOTI-03, SES-04 |
| Idempotency / duplicates | DM-01 (one DM per pair), PEOP-01 (one person per account), MIG-02 (adopt once) |
| Auth boundaries & rate limits | DM-04, DM-08, PEOP-04, ACC-01; existing per-room rate limits apply to DMs |
| Concurrency / ordering | SES-02 edge case; DM creation race → one DM (DM-01) |
| Data lifecycle | Identities permanent until deleted; DMs deleted with an AI; archiving removed (assumption) |
| Observability | Audit entries ACC-01, MRG-01; no message text in logs (existing rule) |
| External-dependency failure | NOTI-03; connector outbox retries unchanged |
| State-transition integrity | SES-01..06 (offline → live → taken over → offline) |

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| AID-01 | P1: Folder identity | Execute | Verified |
| AID-02 | P1: Folder identity | Execute | Verified |
| AID-03 | P1: Folder identity | Execute | Verified |
| AID-04 | P1: Folder identity | Execute | Verified |
| AID-05 | P1: Folder identity | Execute | Verified |
| AID-06 | P1: Folder identity | Execute | Verified |
| AID-07 | P1: Folder identity | Execute | Verified |
| SES-01 | P1: One live session | Execute | Verified |
| SES-02 | P1: One live session | Execute | Verified |
| SES-03 | P1: One live session | Execute | Verified |
| SES-04 | P1: One live session | Execute | Verified |
| SES-05 | P1: One live session | Execute | Verified |
| SES-06 | P1: One live session | Execute | Verified |
| SES-07 | P1: One live session | Execute | Verified |
| SES-08 | P1: One live session | Execute | Verified |
| TREE-01 | P1: Tree | Execute | Verified |
| TREE-02 | P1: Tree | Execute | Verified |
| TREE-03 | P1: Tree | Execute | Verified |
| TREE-04 | P1: Tree | Execute | Verified |
| TREE-05 | P1: Tree | Execute | Verified |
| TREE-06 | P1: Tree | Execute | Verified |
| TREE-07 | P1: Tree | Execute | Verified |
| CONT-01 | P1: Contacts and DMs | Execute | Verified |
| DM-01 | P1: Contacts and DMs | Execute | Verified |
| DM-02 | P1: Contacts and DMs | Execute | Verified |
| DM-03 | P1: Contacts and DMs | Execute | Verified |
| DM-04 | P1: Contacts and DMs | Execute | Verified |
| DM-05 | P1: Contacts and DMs | Execute | Verified |
| DM-06 | P1: Contacts and DMs | Execute | Verified |
| DM-07 | P1: Contacts and DMs | Execute | Verified |
| DM-08 | P1: Contacts and DMs | Execute | Verified |
| GRP-01 | P1: Groups | Execute | Verified |
| GRP-02 | P1: Groups | Execute | Verified |
| GRP-03 | P1: Groups | Execute | Verified |
| GRP-04 | P1: Groups | Execute | Verified |
| PEOP-01 | P1: People | Execute | Verified |
| PEOP-02 | P1: People | Execute | Verified |
| PEOP-03 | P1: People | Execute | Verified |
| PEOP-04 | P1: People | Execute | Verified |
| PEOP-05 | P1: People | Execute | Verified |
| PEOP-06 | P1: People | Execute | Verified |
| ACC-01 | P1: Accounts | Execute | Verified |
| ACC-02 | P1: Accounts | Execute | Verified |
| ACC-03 | P1: Accounts | Execute | Verified |
| ACC-04 | P1: Accounts | Execute | Verified |
| ACC-05 | P1: Accounts | Execute | Verified |
| ACC-06 | P1: Accounts | Execute | Verified |
| ACC-07 | P1: Accounts | Execute | Verified |
| PROF-01 | P1: Profiles | Execute | Verified |
| PROF-02 | P1: Profiles | Execute | Verified |
| PROF-03 | P1: Profiles | Execute | Verified |
| PROF-04 | P1: Profiles | Execute | Verified |
| PROF-05 | P1: Profiles | Execute | Verified |
| PROF-06 | P1: Profiles | Execute | Verified |
| PROF-07 | P1: Profiles | Execute | Verified |
| PROF-08 | P1: Profiles | Execute | Verified |
| PROF-09 | P1: Profiles | Execute | Verified |
| PROF-10 | P1: Profiles | Execute | Verified |
| PROF-11 | P1: Profiles | Execute | Verified |
| LAY-01 | P1: Messenger layout | Execute | Verified |
| LAY-02 | P1: Messenger layout | Execute | Verified |
| LAY-03 | P1: Messenger layout | Execute | Verified |
| LAY-04 | P1: Messenger layout | Execute | Verified |
| LAY-05 | P1: Messenger layout | Execute | Verified |
| LAY-06 | P1: Messenger layout | Execute | Verified |
| AGRP-01 | P2: AIs create groups | Execute | Verified |
| AGRP-02 | P2: AIs create groups | Execute | Verified |
| CLI-01 | P1: Login and setup | Execute | Verified |
| CLI-02 | P1: Login and setup | Execute | Verified |
| CLI-03 | P1: Login and setup | Execute | Verified |
| CLI-04 | P1: Login and setup | Execute | Verified |
| CLI-05 | P1: Login and setup | Execute | Verified |
| CLI-06 | P1: Login and setup | Execute | Verified |
| CLI-07 | P1: Login and setup | Execute | Verified |
| MIG-01 | P1: Migration | Execute | Verified |
| MIG-02 | P1: Migration | Execute | Verified |
| MIG-03 | P1: Migration | Execute | Verified |
| MIG-04 | P1: Migration | Execute | Verified |
| MIG-05 | P1: Migration | Execute | Verified |
| MRG-01 | P2: Merge | Execute | Verified |
| MRG-02 | P2: Merge | Execute | Verified |
| NOTI-01 | P2: Notify on apps | Execute | Verified |
| NOTI-02 | P2: Notify on apps | Execute | Verified |
| NOTI-03 | P2: Notify on apps | Execute | Verified |

**Coverage:** 84 total, 84 mapped to tasks (see `tasks.md` Requirement Coverage)

---

## Success Criteria

- [ ] André resumes an OpenCode session with `-s` and stays the same intelligence.
- [ ] André opens a second Claude Code in Atlas's folder and gets the takeover prompt.
- [ ] André sees devbox and notebook as two machines with his folders nested.
- [ ] André DMs an AI from the web and it answers in the DM.
- [ ] A Discord DM to Atlas's bot reaches Atlas as a DM.
- [ ] Existing WhatsApp routes keep delivering after the upgrade.
- [ ] André uploads his profile photo; an AI renames itself and sets its own photo.
- [ ] An AI André never talked to opens a DM with him, and it shows at the top of his sidebar.
