# Intelligences contract notes

Binding for later batches and the Verifier. One section per task.

## T1 - Single-transaction migration runner (MIG-05, AD-012)

- `Store.Migrate(ctx)` opens one write transaction, creates `schema_migrations` if missing,
  applies every embedded `*.sql` file not recorded there in name order, runs the file's Go hook
  right after it, records each version (`applied_at` = one timestamp for the run) and commits
  once. Any error rolls back every pending file and hook; a later `Migrate` retries from the
  same state.
- `store.Hook func(ctx context.Context, tx *sql.Tx) error`; `hooks map[string]Hook` keyed by
  version (file name without `.sql`). Hooks use raw SQL on `tx` only, never sqlc. A hook runs
  only when its file is pending.
- Errors: `migration <file>: …` (SQL), `migration <file> hook: …`, `record migration <file>: …`.
- Internal for tests: `(*Store).migrate(ctx, fsys fs.FS, hooks)` (files at the root of `fsys`),
  `migrationFiles(fsys)`, package var `migrations` (embedded files at the root). Test helpers:
  `applyMigration(ctx, name)` (one embedded file), `migrationsBefore(version)`, `snapshot(t, s)`
  (every schema object and row), `tableRows`, `versions`.

## T2 - Handle and default-name rules (PROF-02, AID-03)

Package `internal/handles` (leaf, no deps):

- `Kind`: `AI`, `User`, `Person` (default prefixes `ai`, `user`, `contact`).
- Constants: `MaxLen = 40`, `SlugLen = 36`, `MaxDisplayName = 64`.
- `Slug(s)`: lower-case, a fixed table folds common Latin letters with diacritics (á→a, ç→c,
  ñ→n, ü→u, ß→ss, æ→ae, œ→oe, ø→o …); every run of other characters (including any non-Latin
  rune) becomes one `-`; trimmed of `-`; cut to 36 bytes (then trimmed of `-` again).
- `Default(kind, base)`: `Slug(base)`; empty → `ai`/`user`/`contact`; starting with a non-letter →
  `ai-…`/`user-…`/`contact-…`.
- `Validate(h) (string, error)`: lower-cases, then `^[a-z][a-z0-9-]{0,39}$`; reserved
  `partyline`, `all`, `everyone`, `here`, `owner`. Errors wrap `handles.ErrInvalid` (the message
  says why: shape or reserved). Returns the lower-cased handle.
- `Allocate(taken func(string) bool, base) string`: lower-cases `base` (a base not shaped like a
  handle becomes `Default(AI, base)`); returns `base` unless reserved or taken, else the first
  free `base-2`, `base-3`, …; a suffix that would pass 40 characters cuts the base
  (`xxx…x-1000`). `taken` receives lower-case candidates; callers make it case-insensitive.
- `DisplayName(base)`: drops control characters and invalid UTF-8, upper-cases the first rune,
  cuts to 64 code points (`discord` → `Discord`, `/` → `/`, `~` → `~`).

## T3 - Schema 0015 (PROF-01, MIG-01)

- `0015_intelligences.sql` has every table, column, index and trigger of the design's
  "Migration 0015 (schema)" verbatim: `intelligences`, `agents.folder` + `agents_folder`,
  `machines`, `people` + `people_alt`, `chat_people`, `chats.person_id`, `rooms.kind/dm_a/dm_b/
  read_baseline` + `rooms_dm_pair`, `read_cursors`, `messages.author_iid` +
  `messages_author_iid`, `connectors.operator_iid/account_label`, `users.notify_chat_id`,
  `outbox.purpose`, triggers `users_/agents_/people_intelligence_delete`.
- Upgrade data in the SQL part: `rooms.read_baseline = next_seq - 1` for existing rooms; new rooms
  default to 0.
- **Deviation (view):** `message_views` keeps the v0.3 project-member join `pm` as a transitional
  fallback, because v0.3 project code still creates project members until T16 (PROJ-07 tests).
  Agent author name order: room member display name, project member display name,
  `i` (author_iid), `ia` (the agent's own intelligence), `agents.display_name`, `auto_name`,
  `name`. User: `i`, `iu`, `users.display_name`, `username`. Chat: `i`, `sender_name`, chat title.
  System: `Partyline`. New columns `author_iid`, `author_handle`, `author_ikind`, `author_avatar`
  (from `i`, then `ia`/`iu`; '' when none). 0016 drops `pm`, `ia`, `iu`, `a`, `u`.
- Consequence for T7+: once an agent or user has an intelligence, the view prefers the
  intelligence's display name over `agents.display_name`/`auto_name`/`users.display_name`; any v0.3
  rename path still in use must also write `intelligences.display_name`.
- sqlc queries (CRUD only; later tasks may add more):
  - `intelligences.sql`: `CreateIntelligence`, `GetIntelligence`, `GetIntelligenceByHandle`,
    `CountIntelligenceHandle` (NOCASE), `ListIntelligencesByIDs` (`sqlc.slice('ids')`),
    `UpdateIntelligenceProfile` (display_name, handle, bio, updated_at), `SetIntelligenceAvatar`,
    `CountIntelligenceAvatar`, `DeleteIntelligence`.
  - `people.sql`: `CreatePerson`, `GetPerson`, `GetPersonByExternalID` (matches `external_id` or a
    non-empty `alt_external_id`, an external match first; returns
    `GetPersonByExternalIDRow{Person, Exact}`), `ListConnectorPeople`, `UpdatePersonSeen`
    (alt id, platform_name, last_seen_at), `SetPersonNameLocked`, `DeletePerson`,
    `UpsertChatPerson` (last_seen_at = MAX), `ListChatPeople`, `SetChatPerson`.
  - `machines.sql`: `UpsertMachine` (home, last_seen_at), `GetMachine`, `ListMachines`,
    `SetMachineLabel`, `DeleteMachine`.
  - `conversations.sql`: `CreateDMRoom` (caller passes sorted `dm_a < dm_b`), `GetDMRoom`,
    `SetRoomReadBaseline`, `UpsertReadCursor` (seq = MAX), `GetReadCursor`, `DeleteReadCursor`.
  - `SetConnectorOperator`, `SetConnectorAccountLabel`, `SetUserNotifyChat`,
    `CreateNotifyOutbox` (purpose `notify`), `SetMessageAuthorIID`, `SetAgentFolder`,
    `GetAgentByFolder` (folder <> '').
- Nothing writes `author_iid` on insert yet (T11), nothing creates intelligences for new users or
  agents yet (T7): rows created after the upgrade have none until then.

## T4 - Backfill (0015 hook) (MIG-01, MIG-04)

`store.hooks["0015_intelligences"] = backfillIntelligences` (`intelligences_backfill.go`):

- Handles are allocated in this order, each `handles.Allocate` against every handle already
  used: users (by `created_at`, then id) with `Default(User, username)`; agents (same order) with
  `Default(AI, name)`; people (by first sighting, connector, external id) with
  `Default(Person, name)`.
- Users: kind `human`, display = `users.display_name`, else `username`. Agents: kind `ai`,
  display = first non-empty of `display_name`, `auto_name`, `name`. Display names are copied as
  they are (no `DisplayName` normalisation) so v0.3 names survive. `created_at = updated_at =`
  the row's `created_at`. Every v0.3 agent keeps `folder = ''` (unlinked).
- People come from `identities` and chat messages with a `sender_id` (joined to their chat for
  the connector). Ids reported together (a sender id with its alt id) fold into one person per
  connector. `external_id` = the sender id of the person's latest message (the identity's
  external id when never seen in a message); `alt_external_id` = the latest different alt id,
  else another folded id, else ''. `platform_name` = latest non-empty sender name, else the
  identity's display. Display name = `platform_name`, else `"<Platform> contact"` (`WhatsApp`,
  `Telegram`, `Discord`, `Webhook`, else `DisplayName(kind)`). `created_at` = first sighting;
  `last_seen_at` = latest message time (NULL for identity-only people). Linked users' accounts
  get a person too (PEOP-02 links it to the user).
- `messages.author_iid`: `user`/`agent` → `author_id` (when that intelligence exists); `chat` with
  `author_user_id` → that user; `chat` with a sender id → the person; else ''.
- `chat_people`: every (chat, person) of a chat message with a sender id (linked senders
  included), `last_seen_at` = latest message time. `chats.person_id` for `kind = 'dm'` chats =
  the person of the chat's latest such message; other chats stay NULL.
- The sender author update is set-based through a temporary table (`backfill_senders`, dropped
  at the end): about 6 s for 200 000 messages on the loaded devbox.

## T5 - v0.3 projects (MIG-03)

SQL part of `0015_intelligences.sql`, before the view:

- For each `room_members` row of kind `project` whose project has agents: `INSERT OR IGNORE` an
  `agent` member for the project's agent ordered by `connected_at IS NULL, connected_at DESC,
  id` (the v0.3 active-session order), copying `outbound`, `added_by`, `created_at`,
  `display_name`, `sign_outbound`, `delivery`, `keywords_json`. An existing direct membership
  keeps its own settings. New member ids are random UUIDv4-shaped strings (as in 0011).
- `project_cursors` are copied to `agent_cursors` of that agent with `seq = MAX(existing, project)`.
- Then all project members, project grants and `projects` rows are deleted (`agents.project_id`
  → NULL by `ON DELETE SET NULL`; `project_cursors` cascade). The `projects` and
  `project_cursors` tables stay (empty) until 0016; v0.3 project code keeps compiling and working
  on new data until T16.
- **Deviation (test scope):** `TestAgentIdentityMigrationKeepsDataAndAcceptsProjects` (0011 data
  migration) now migrates to 0014 only (`migrationsBefore("0015")`), since 0015 removes the
  projects it asserts. Its assertions are unchanged. Helper type `olderThan{FS, version}` lists
  the files older than a version.

## T6 - v0.3 upgrade fixture and production route (MIG-01, MIG-05, GRP-01)

- `internal/store/testdata/v03_fixture.sql`: a v0.3 (schema 0014) data set with readable ids
  (`u-alice`, `a-atlas`, `r-inbox`, `k-tg`, `c-tgdm`, …): users (one disabled), sessions, tokens,
  invites, device requests and attempts, audit log, projects (Proj with three sessions incl. a
  never-connected one, Empty with none, Atlas), agents (fixed, temporary, archived), four rooms
  (ops with instructions, catch-all `inbox`, dev, bob's), members of every kind, grants of every
  kind, a telegram connector with catch-all `r-inbox` (scope dms) and a whatsapp connector,
  chats, an identity, auto-route skip, webhook replay, pairing code, messages of every author
  case, attachments, outbox rows (sent, pending, failed), agent and project cursors.
- `Store.MigrateBefore(ctx, version)` (exported): applies the embedded migrations older than
  `version` in one transaction (builds older-schema databases for upgrade tests).
- `testkit.WithDatabase(path)`: the app starts on a copy of that database (and its `-wal`) in the
  temp data dir; `app.Start` migrates it.
- Store tests (`intelligences_migration_test.go`): every v0.3 row and column survives except the
  project conversion's intended changes; rooms are groups with `read_baseline` = last seq; a hook
  failure after the real hook ran leaves `snapshot` identical and a later `Migrate` succeeds.
- **Deviation (forced failure):** the design's failure case (a pre-inserted intelligence row) is
  impossible before 0015 creates the table and `Allocate` never collides, so the test wraps the
  real hook and fails after it.
- API test (`upgrade_test.go`): a fake kind renamed `telegram` serves the fixture's connector.
  After the upgrade the agent `atlas` (v0.3 `X-Partyline-Agent: atlas`) gets the instructions
  notice of ops, a DM from the existing chat and from a new chat (catch-all) in `r-inbox`, and its
  reply goes out to chat `100`; the fixture's pending outbox row is sent too.
- Not covered here (later tasks): design test-plan item 6 (0016, T51) and the timeline's
  person name with platform badge (GRP-03, T11).

## T7 - Intelligences service and creation hooks (PROF-01, PROF-02, PROF-10)

Package `internal/intelligences` (`service.go`; the tree builder of T25 lives next to it):

- `Create(ctx, q, NewParams{ID, Kind, DisplayName, HandleBase, Now}) (db.Intelligence, error)`
  inserts the intelligence in the caller's transaction. `Kind` is `handles.User`, `AI` or
  `Person` (human, ai, human; it picks the `user-`/`ai-`/`contact-` prefix of a default handle).
  Handle = first free `handles.Default(Kind, HandleBase)`, `-2`, `-3`, … across the whole
  instance (one namespace for users, AIs and people). Display name = `CleanDisplayName(DisplayName)`,
  else the cleaned `HandleBase`, else the handle. **Every transaction that inserts a user, an
  agent or a person must call it** (T13 `ResolveFolder` and T26 `People.Seen` still have to).
  Today it is called by `users.insertAccount` (first-run setup, invite accept, admin create;
  display name = the cleaned account display name, else the username, handle from the
  username), `Registry.Upsert` and `Registry.getOrCreate` (so `Resolve` and `Register`; handle
  from the agent name, display name = `delivery.AgentDisplayName`).
- `CleanDisplayName(s)`: drops control characters and invalid UTF-8, trims, cuts to 64 code
  points. Accounts store the cleaned name in `users.display_name` too.
- **The v0.3 columns `users.display_name` ('' = show the username) and `agents.display_name` are
  mirrors of `intelligences.display_name` until T51 drops them.** Every writer keeps both:
  `Service.Update` mirrors a display-name change into the subtype row; the v0.3 paths still in
  use write the intelligence through `SetDisplayName(ctx, q, id, name, now) (changed, err)`:
  `users.UpdateProfile` (an empty display name shows the username), `Registry.Update` (owner
  rename), `Registry.record` (the readable name of a temporary agent) and
  `Registry.RenameProject`. A row without an intelligence is left alone. `message_views` and the
  member lists read the intelligence first, so a new rename path must call it.
- `Announce(ctx, bus, q, id, members)` publishes `events.IntelligenceChanged{ID}` and, when
  `members`, `events.MembersChanged{RoomID}` for every room the intelligence is a direct member
  of (agent or user member). Call it after the transaction committed. The live hub does not
  render `IntelligenceChanged` yet (T9 adds the `intelligence.changed` frame).
- `Service` (`app.App.Intelligences`, `intelligences.New(Deps{Store, Access, Bus, Now})`):
  - `Get(ctx, p, id) (Profile, error)`: `Profile` embeds `db.Intelligence` and adds `Role`
    (`RoleUser`, `RoleAI`, `RolePerson`) and `CanEdit`. Not visible or unknown →
    `access.ErrNotFound`.
  - `CanSee(ctx, p, id)` / `CanEdit(ctx, p, id)`. Disabled and unknown users see and edit
    nothing (AUTH-10). Self: a user principal on their own intelligence, an agent principal on
    its own AI (see + edit). Users: every active user sees every user intelligence; only the
    user edits it. AIs: the owner (manage) sees and edits; a `use` grantee sees; anyone who can
    view (`access.UserRoomLevel`) a room where the AI is a direct member or wrote a message
    (`messages.author_iid`) sees; an agent principal sees what its owner sees but edits only
    itself; admins get nothing. People: users who own or may `use` the connector see and
    edit; an agent principal of such a user sees, never edits.
  - `Update(ctx, p, id, ProfileUpdate{DisplayName, Handle, Bio *string}) (Profile, error)`:
    404 `access.ErrNotFound` without view, 403 `access.ErrForbidden` with view but no edit,
    `*validation.Error` with fields `display_name` (1-64 code points after dropping control
    characters and trimming), `handle` (`handles.Validate` after trimming; lower-cased) and
    `bio` (0-280 code points; control characters except `\n` dropped, trimmed) with nothing
    applied, `ErrHandleTaken` when another intelligence has the handle (case-insensitive; the
    unique index is the last guard). Only changed values are written; a no-op update writes and
    announces nothing. A change announces `IntelligenceChanged`, plus `MembersChanged` when the
    display name or handle changed. T9 maps `ErrHandleTaken` to 409 `handle_taken`.
- sqlc added: `GetIntelligenceWithRole` (the row with `role`, `agent_owner_id`,
  `person_connector_id`), `SetIntelligenceDisplayName`, `ListMemberRoomIDs`,
  `ListIntelligenceRoomIDs` (member rooms ∪ authored rooms), `SetUserDisplayName`.
- Not in T7: a person edit does not set `people.name_locked` and person visibility has no
  shared-conversation rule yet (T27); avatars (T10); `Message.author` (T11).

## T9 - Profile API (PROF-03, PROF-05, PROF-10, AID-06)

- `GET /api/v1/intelligences/{id}` → `dto.IntelligenceProfile`: the `IntelligenceRef` fields
  (`id`, `kind` human|ai, `role` user|ai|person, `display_name`, `handle`, `avatar_url?`,
  `platform?`) flattened, plus `bio`, `can_edit`, `ai?` (`AIDetail`), `apps` and
  `conversations` (both always arrays; `conversations` stays empty until the conversation
  tasks fill it, `apps` is `[{kind:"web"}]` for users and `[{kind:<harness id>,label}]` for
  AIs). 404 `not_found` for what the caller cannot see (`Service.CanSee`), also for an unknown
  id.
- `PATCH /api/v1/intelligences/{id}` with `UpdateProfileRequest{display_name?, handle?, bio?}`
  → 200 with the stored profile. 404 not visible, 403 `forbidden` visible but not editable (an
  AI's use grantee, another user, an admin), 422 `invalid` with `fields` `display_name`,
  `handle` and/or `bio` and nothing applied, 409 **`handle_taken`**
  (`dto.ErrorCodeHandleTaken`, from `intelligences.ErrHandleTaken`), 400 for a malformed body.
  Sessions and user tokens may call it; agent tokens only reach `/agent/*` (403 elsewhere).
- `AIDetail`: `owner` (the owner's `IntelligenceRef`), `machine`, `machine_label` (the owner's
  label from `machines`, else the hostname), `folder` and `folder_label` (`''` while the AI is
  unlinked; `intelligences.FolderLabel(home, folder)` gives `~`, `~/x` or the absolute path),
  `app` (the harness id), `online`, `last_seen_at`, `connected_at`, `my_level` (`manage` owner,
  `use` grantee, absent when the AI is only visible through a shared conversation).
  `intelligences.Profile` carries the same data: `Platform` (a person's connector kind) and
  `Agent *AgentInfo`. `Service.Rows(ctx, ids)` returns intelligence rows by id without
  authorizing (for ids a handler took from data the caller can see).
- `dto.Me.intelligence` (always set) is returned by `GET/PATCH /me`, `POST /setup`,
  `POST /auth/login` and the invite accept; `dto.User.intelligence` (optional) is set by the
  `GET /users` directory (active accounts) and, from T11, by room owners. `PATCH /me` keeps
  its v0.3 behaviour (an empty display name shows the username) and writes the account's
  intelligence (T7).
- Live: `intelligence.changed` (`dto.LiveIntelligenceChanged`, data = `IntelligenceRef`) goes
  to the sockets of every user who can see the intelligence, for each
  `events.IntelligenceChanged`.
- Not in T9: `PersonDetail` and the person's apps/conversations (T27), conversations of an AI
  (T22+), avatars (T10).

## T10 - Avatar upload, serving and the agent profile endpoints (PROF-04, PROF-06, PROF-07)

- `PUT /api/v1/intelligences/{id}/avatar`: raw image body (any `Content-Type`; the content is
  sniffed), PNG, JPEG or WebP up to 2 MiB (`avatars.MaxBytes`), stored as a centre-cropped
  256×256 PNG in the blob store (`attachments.Store.Put`, content-addressed) and referenced by
  `intelligences.avatar_sha256`. 200 with the `IntelligenceProfile` (its `avatar_url` is
  `/api/v1/intelligences/{id}/avatar?v=<sha256 of the stored PNG>`, `convert.AvatarURL`).
  422 **`avatar_invalid`** (`dto.ErrorCodeAvatarInvalid`, from `avatars.ErrInvalid`) for another
  format, a body over 2 MiB, an image that cannot be decoded or has a side over 4096 px: the old
  avatar stays. The request body limit of avatar routes is the instance upload limit (at least
  2 MiB + 1), not 1 MiB, so a file over 2 MiB is 422, one over the upload limit 413.
  403 for a caller who can see but not edit, 404 for one who cannot see (checked before the
  image is read).
- `DELETE /api/v1/intelligences/{id}/avatar` → 204 (also when there is none); same rights.
- `GET /api/v1/intelligences/{id}/avatar` → the PNG (`Content-Type: image/png`,
  `Cache-Control: private, max-age=31536000, immutable`, `ETag: "<sha>"`, the API's
  `nosniff` and `default-src 'none'` headers) for callers who `CanSee` the intelligence; 404
  for everybody else and when there is no avatar.
- `PATCH /api/v1/agent/profile` (`UpdateProfileRequest`) and `PUT /api/v1/agent/profile/avatar`
  (raw image): the calling agent edits its own AI only (`agentOnly`: agent token + the agent
  identity of the request; sessions and user tokens get 403, anonymous 401). They answer with the
  `IntelligenceProfile` and use the same errors (409 `handle_taken`, 422).
- Service: `Service.SetAvatar(ctx, p, id, r)`, `ClearAvatar`, `Avatar` (returns the open blob and
  its sha); `intelligences.Deps.Blobs`. Both set and clear announce `IntelligenceChanged` and
  `MembersChanged` for the member rooms.
- Retention: `rooms.Service.Cleanup` and the room-delete blob cleanup count a blob as used when
  an attachment **or** an avatar references it (`blobReferenced`), so a replaced or removed
  avatar is swept after the one-hour grace period and the current one never is.

## T11 - Message and member authors as intelligences (AID-06, PROF-03, GRP-03)

- `rooms.insert` stores `messages.author_iid`: the user's id for user posts, the agent's id for
  agent posts (users and agents share their id with their intelligence); `''` for system
  messages and, **until T26 sets it (linked user, else person)**, for chat messages.
  `CreateMessage` takes `AuthorIid`; `rooms.draft.authorIID` is the field to fill.
- `dto.Message.author` (`IntelligenceRef`, always present) is built by `convert.Message` from the
  `message_views` author columns: user and agent authors are their own intelligence (an
  `author_iid`-less row of those kinds falls back to `author_id`); a chat message has an author
  only when `author_iid` is set: the person (`role person`, `platform` = the chat's connector
  kind) or, when it equals `author_user_id`, the linked user (`role user`, no platform). System
  messages and chat senders without an intelligence give `author = {display_name: <shown name>}`
  with an empty `id`, `kind`, `role` and `handle`. `author.display_name` is the name the
  timeline shows (`author_name`): a name set for an AI in the room (GRP-04) wins there, while
  `author.handle` and `author.avatar_url` are the profile's. `author_id`, `author_name`,
  `author_user_id` and the `sender_*` fields are unchanged.
- `dto.RoomMember.intelligence` (optional) is set for agent and user members: the profile's
  `IntelligenceRef` (`role` ai/user). `RoomMember.name` is the profile display name for them
  (the v0.3 names are the fallback); `RoomMember.display_name` stays the per-room agent setting.
  Chat and project members have none. The same `toMember` serves the agent tools
  (`list_rooms`, `room_info`).
- `dto.Room.owner.intelligence` (optional `IntelligenceRef` of the owner) in `GET /rooms/{id}`,
  `GET /rooms` and `room.changed` frames; `rooms.Room.OwnerIntelligence` is the data
  (`ID == ''` when the owner has none). `rooms.Member.Intelligence` likewise.
- sqlc: `ListRoomMembers` returns `intelligence_*` columns; `GetRoomWithOwner` and
  `ListRoomsForUser` return `owner_ikind/iname/handle/avatar`.
- Web fixtures `makeMessage`/`makeMe` carry the new required fields (`author`, `intelligence`).

## T12 - Mentions and signatures follow the profile (AID-06, PROF-03, GRP-04)

- `delivery.MemberPrefs` has `Names` (the name set for the member in the room and the AI's
  profile display name) and `Handle` (the profile handle). `delivery.Prefs(profile db.Intelligence,
  memberDisplayName, delivery, keywordsJSON)` builds it; `ProjectPrefs(profile, projectName, …)`
  adds the project's name (project members stay until T50); `AgentPrefs` is gone.
  `delivery.IntelligenceOf(ctx, q, agentID)` loads the profile (zero value for an agent without
  one, which then answers to its room name only). The internal agent `name` and `auto_name` are
  no mention any more. Callers: the dispatcher (live push), the gateway backlog and
  `GET /agent/pending`.
- Matching, case-insensitive: a name is mentioned as `@<name>` with no letter, digit or
  underscore right before the `@` and none of letter, digit, underscore or `-` right after the
  name (`@Atlas-2` is no mention of "Atlas"); the handle as `@<handle>` with the same rule
  before and no `[a-z0-9-]` right after it, so `@atlas` never reaches `@atlas-2`, an address such
  as `joe@atlas.example` mentions nobody, and after a rename the old handle stops matching.
  Keywords keep the word rule they had (a `-` after a keyword is still a boundary). Delivery
  modes, keywords and broadcast are unchanged. Two AIs that share a display name are both
  reached by `@<that name>`; handles are what tells them apart.
- Outbound signatures already read the view's author name (the profile display name, or the name
  set in the room), so they follow profile renames made by the owner (`PATCH
  /intelligences/{id}`) or by the AI (`PATCH /agent/profile`): covered by
  `TestOutboundAuthorFollowsProfileRename`.

## T13 - Resolve AIs by folder (AID-01, AID-02, AID-03, AID-05, MIG-02)

`internal/agents/folder.go`; nothing calls it from the API yet (T14).

- `Registry.ResolveFolder(ctx, ownerID, FolderID{Machine, Folder, Home, Harness, Adopt}, live)
  (db.Agent, error)`. Order: clean the machine (control characters dropped, trimmed, 64 characters)
  and validate (`ValidateFolder`) **before anything is written** → harness id (`HarnessID`; empty
  stays empty) and `Adopt` (trimmed, lower-cased) → `touchMachine` → lookup of (owner, machine,
  folder) → on a miss `linkOrCreate` → `relabel`.
- `ValidateFolder(machine, folder)`: empty/blank machine → 422 `folder_required` (field `machine`);
  empty folder → 422 `folder_required` (field `folder`); invalid UTF-8, a NUL, more than
  `MaxFolderLength` (1024) bytes, not absolute (`/…`, or `X:\…`/`X:/…`; UNC and `C:acme` are
  relative), a trailing separator other than the root, or a `.`/`..` segment → 422
  `folder_invalid` (field `folder`). Both are `*httpx.Error` with the new `dto.ErrorCodeFolderRequired`
  / `ErrorCodeFolderInvalid`; the `folder_required` messages end with "update partyline" (an old
  CLI sends nothing). On Unix `/` is the only separator (a backslash is a file name character);
  on a drive path both are. Empty segments (`//`) are not refused.
- Identity = (owner, machine, folder), the unique index `agents_folder` being the last guard. A
  hit returns the same agent for any harness. **App (harness)**: a live session sets it; a
  tools-only call (`live=false`) sets it only while `Presence.Online` is false, so a Codex call
  never relabels a live Claude Code session; an empty harness never changes it; an unknown one is
  the generic `agent` app.
- Adoption (MIG-02), only when the folder has no identity: the owner's **unlinked** agent
  (`folder = ''`) named by `Adopt` (its intelligence handle, case-insensitive, else its v0.3
  `agents.name`), else the one whose handle is `handles.Default(AI, base(folder))`. A missing or
  already linked name is ignored (no error); other owners' agents are never candidates.
  `AdoptAgent … WHERE folder = ''` sets machine, folder and harness (the stored harness when none
  was sent) and keeps id, name, profile, groups and history. It writes the audit entry
  `agent.adopt` (`audit.ActionAgentAdopt`, target kind `agent`, detail `{"by": "as"|"folder"}`,
  actor = the owner) in the same transaction, so a link is audited exactly once.
- Creation: `CountAgentsByOwner >= MaxAgentsPerOwner` → `ErrAgentLimit`; row `name = id`,
  `harness` = the session's, `kind` the column default; intelligence through `intelligences.Create`
  with `handles.DisplayName(base)` and handle base `base` (`base` = last path element; `/` is
  `/`, a drive root `C:`); `agents.display_name` is set to the profile's display name (the T7
  mirror), so `agents.DisplayName(a)` reads the folder's name and never the row's id. All of this runs in one write transaction under `allocMu`, after a
  re-check of the folder, so concurrent resolves of one new folder create one identity and
  concurrent adoptions link once.
- **`MaxAgentsPerOwner` is 200** (was 50) for every creation path; the limit error stays the
  existing 409 `conflict` "agent limit reached" (`ErrAgentLimit`), not a new `agent_limit` code,
  because the channel and the docs already know it. The v0.3 cap tests derive from the constant.
- `touchMachine`: the `machines` row (home, `last_seen_at`) is written at most once per
  `MachineTouchInterval` (1 minute) per (owner, machine) per process, and at once when the
  session reports a different home folder. A home that is not a valid folder is ignored. The
  `UpsertMachine` query now keeps the stored home when it is given an empty one; the owner's
  label is never touched.
- `agents.Deps.Audit` (optional) is the audit log; `app.go` builds it before the registry.
- sqlc added: `InsertFolderAgent`, `SetAgentHarness`, `GetUnlinkedAgentByHandle`,
  `GetUnlinkedAgentByName`, `AdoptAgent` (`:execrows`).

## T14 - Server accepts folder headers (AID-02, AID-04, SES-01)

- New headers (`dto/common.go`): `X-Partyline-Folder` (the canonical folder, `url.PathEscape`d, so
  `/` is `%2F`), `X-Partyline-Home` (same encoding), `X-Partyline-Adopt` (handle or v0.3 name),
  `X-Partyline-Live` and `X-Partyline-Takeover` (declared now, read by T17). `X-Partyline-Machine`
  and `-Harness` are the existing ones. A request **names a folder** when the folder header is
  present, even when empty (`agents.HasFolder`); `agents.FolderIDFromHeader(h)` decodes it: a folder
  that is not valid percent-encoding is 422 `folder_invalid`, a home that is not is ignored. The
  decoded `FolderID` is validated by `ResolveFolder` / `LookupFolder` (T13 rules).
- A request without the folder header still takes the **v0.3 path** (`X-Partyline-Agent`, named or
  temporary agents) until T16 makes the folder required; the new routes below have no v0.3 path.
- Routes that act as an AI resolve it with `ResolveFolder(live=false)` (`server.agentOf`): every
  `agentOnly` route (`/agent/rooms…`, `messages`, `attachments`), `POST /agent/register` (through
  `Registry.RegisterFolder`, which also returns the owner) and `GET /agent/pending`
  (`agentOnly` resolves; the handler reads the agent by `Principal.AgentID`, `Registry.ByID`). A call
  by a folder that has no AI creates it (AID-04); what it posts is authored by that AI. The agent
  WebSocket resolves with `live=true` and stamps `connected_at`; its app follows the harness.
- `GET /api/v1/agent/session` (agent token + folder headers, no AI created, no machine recorded,
  no app changed; missing folder → 422 `folder_required`) → `dto.SessionState{exists, online,
  intelligence?, app, connected_at?}`: `intelligence` is the AI's `IntelligenceRef`, `app` the
  stored harness id, `connected_at` the AI's `agents.connected_at` and **only while it is online**.
  Another owner's AI, another machine and another folder read `exists: false` (zero value).
  `Registry.LookupFolder` is the read-only lookup behind it (an unlinked v0.3 identity is not
  "the AI of the folder" until a session adopts it).
- `GET /api/v1/agent/whoami` (agent token, no folder, no AI needed) → the token owner's
  `dto.IntelligenceRef` (role `user`).
- `dto.AgentWelcomeFrame` gains `intelligence` (the AI's `IntelligenceRef`) and `owner` (its
  owner's); `agent.display_name` is the profile's display name. The refs come from the
  intelligences table; a row without one falls back to what the registry knows.
- `agents.Gateway` logs a refused connection that is a 4xx API error (a bad folder) at debug level
  with its code only, not as a registration failure; folder paths and hostnames are never logged.
- Testkit: `testkit.Folder{Machine, Path, Home, Harness, Adopt}` with `Headers()` (percent-encoded as
  the CLI sends them), `Client.AsFolder(f)` (a copy that sends them on every request) and
  `Client.DialFolder(f)`.

## T15 - CLI sends the canonical folder (AID-07, MIG-02)

- `channel.Identity{Machine, Folder, Home, Harness, Adopt}` is what every process of a session
  sends: `setHeaders` always sets `X-Partyline-Machine` (`unknown` when the hostname is unknown) and
  `X-Partyline-Folder` (`url.PathEscape`, so spaces, non-ASCII characters and `/` are escaped; an
  unknown folder is an *empty* header, which the server refuses with `folder_required`), and sets
  `X-Partyline-Home` (same encoding), `-Harness` and `-Adopt` when they are not empty. No process
  sends `X-Partyline-Agent`, `-Agent-Mode`, `-Project` or `-Session` any more. A WebSocket dial adds
  `X-Partyline-Live`, the process's `channel.NewLiveID()` (22 characters of `[A-Za-z0-9_-]`), the
  same on every reconnect of that process; REST calls carry none. `X-Partyline-Takeover` is not
  sent yet (T17/T36).
- `channel.CanonicalFolder(dir)`: `filepath.Abs` → `EvalSymlinks` → `Clean`; a directory that cannot
  be resolved (it does not exist) is returned absolute and cleaned, never an error.
- The CLI (`cli.sessionIdentity(adopt, io)`) gathers: folder = `PARTYLINE_FOLDER` (the launcher's)
  else the working directory (the hook: `PARTYLINE_FOLDER`, else the `cwd` of its stdin payload, else
  its own); home = `HOME`, else `USERPROFILE`, canonical; machine = the hostname (trimmed);
  harness = `PARTYLINE_HARNESS` (a `--harness` flag wins); adopt = `--as` (aliases `--name`,
  `--agent`), else `PARTYLINE_AGENT`. `--project` is accepted by `channel`, `listen` and the
  launchers and ignored with one stderr line ("--project is ignored: an AI is identified by its
  folder"). The v0.3 home-folder warning is gone (a home folder or `/` is an ordinary folder).
- `procenv`: the launcher variables recovered from the nearest launched ancestor are now
  `PARTYLINE_FOLDER`, `PARTYLINE_HARNESS`, `PARTYLINE_TAKEOVER` and `PARTYLINE_AGENT`; an ancestor
  counts as launched when its environment has `PARTYLINE_FOLDER`, and a process that has
  `PARTYLINE_FOLDER` itself takes nothing from an ancestor.
- Launcher (`partyline <harness>`): passes `PARTYLINE_FOLDER` (the canonical working directory) and
  `PARTYLINE_HARNESS`, replacing inherited values, and `PARTYLINE_AGENT` from a leading `--as` /
  `--name`; no `PARTYLINE_SESSION` or `PARTYLINE_PROJECT`.
- Removed on the client: session files (`<tmp>/partyline/.sessions`), the tools-only registration at
  start (a tools-only process creates its AI with its first call, AID-04), `DeriveProject`,
  `IsHomeDir`, the temporary-name state of the link. Processes started in one folder are one AI
  whatever their harness: `listen` + the tools-only server (pi, OpenCode), the tools-only server + the
  turn-end hook (Codex, Gemini) and the full channel (Claude Code, omp).
- Download directory: `<TempDir>/partyline/<handles.Slug of the folder's name>` (`agent` when the name
  has no letter or digit), so a hostile folder name cannot leave `<TempDir>/partyline/`.
- Link messages no longer name the agent: `partyline: replaced by another session of this agent`,
  `partyline: this agent was deleted; restart the session to register it again` (T17 and T18 replace
  the replaced state with the busy and taken-over ones).
- Server side of the same task: the name `Adopt` gives is also tried as a v0.3 name after
  `agents.Slug` (v0.3 stored slugs: `--name "Billing API!"` adopts `billing-api`), after the
  handle lookup (`handle` lower-cased) that comes first.

## T16 - Require the folder, drop the v0.3 naming (AID-05)

- **Every agent route that acts as an AI requires the folder.** The agent WebSocket and
  `agentOnly` routes (`rooms`, room `info`/`messages`/post, `reply`, `react`, `attachments`,
  `pending`, `profile`) plus `POST /agent/register` and `GET /agent/session` resolve the AI from the
  token's owner, `X-Partyline-Machine` and `X-Partyline-Folder`. No folder (header absent or empty)
  or no machine → 422 `folder_required` with "… update partyline" (fields `folder` / `machine`); a
  folder the server cannot use → 422 `folder_invalid`. A CLI from before folders (it sends
  `X-Partyline-Agent`, `-Project`, `-Session`, `-Agent-Mode` and no folder) therefore gets
  `folder_required` everywhere. `DELETE /agent/token`, `GET /agent/whoami` and `GET /agent/agents`
  need no folder. A first call from an unseen folder creates its AI instead of failing (AID-04).
- **Removed**: `dto.HeaderAgent`, `HeaderAgentMode`, `AgentModeUnique`/`Temporary`, `HeaderProject`,
  `HeaderSession` and `authn.Principal.AgentName` (the headers are ignored when sent); the
  temporary/fixed allocation (`Identity`, `Resolve`, `freeName`, `identityPlan`, `AutoName`,
  `ProjectFromName`, session holds), `Registry.Upsert`/`Register`/`Lookup`, `archive.go` and the
  `agent-archive` job in `app.go`; the sqlc queries `InsertAgentIfMissing`, `GetAgentByName`,
  `GetProjectBySlug`, `UpdateAgentIdentity`, `ListSessionAgents`, `HoldAgentForSession`,
  `ReleaseAgentSession`, `PromoteAgent`, `ListArchiveCandidates`, `ArchiveAgent`, `SetAgentAutoName`,
  `ListTemporaryProjectAgents` and `SetAgentFolder`; the `partyline agents --all` flag
  (`include_archived`). `MarkAgentConnected` only stamps `connected_at`.
- **Legacy DTO fields keep constant values until T51**: `Agent.kind` is always `fixed`,
  `Agent.archived` always `false`, `Agent.project` is set only for an agent a (test) fixture put in a
  project. `UpdateAgentRequest.fixed` stays: `true` is a no-op, `false` is 422. `Agent.name`
  (and `AgentInfo.name` in the welcome frame and the register answer) is now the AI's **@handle**
  (its row's name when it has no profile), and `display_name` is the profile's display name, so the
  pages and the CLI that still read them show readable names for AIs whose row is named by its id.
  `ListVisible` no longer filters archived agents: an agent v0.3 archived is listed.
- **Projects stay until T50**, but no session joins one any more. `RenameProject` only renames the
  project and refreshes the rooms that have it as a member. New queries `CreateProject` and
  `SetAgentProject` exist only so the tests can keep exercising project members; T50 deletes them
  with the rest. Testkit: `Env.NewAI(owner, name)` (the AI of `FolderOf(name)`, named `name`),
  `Env.NewLegacyAI` (unlinked, for adoption), `FolderOf`, `Client.AsAgent(name)` and
  `Client.DialAgent(name)` (a session of `FolderOf(name)`); `DialAgentUnique` is gone.
- `partyline setup` verifies a token with `GET /agent/whoami` (200 valid agent token, 401 rejected,
  403 not an agent token) instead of the v0.3 name probe on `/agent/rooms`.
- The pi and OpenCode adapters start `partyline listen` with `PARTYLINE_HARNESS` set to `pi` /
  `opencode` (the app label of a session started without the launcher) and no longer set
  `PARTYLINE_SESSION`.
- `delivery.AgentDisplayName` is the mirrored `display_name`, else the row's name (no
  `auto_name`); the DTO builders read the profile's display name first (`Agent.ProfileName`).


## T17 - One live session per AI with explicit takeover (SES-02, SES-04, SES-06, SES-08)

- **Headers on the agent WebSocket only**: `X-Partyline-Live` (the process id, `[A-Za-z0-9_-]{16,64}`;
  a missing or malformed one names no process) and `X-Partyline-Takeover: 1`. REST tool calls
  send neither and never take part in the live session.
- **`Presence.Attach(ctx, agent, session, liveID, takeover) (fresh bool, err error)`** swaps under
  Presence's mutex: nobody holds the AI → attach and publish `AgentPresence{Online:true}`; the same
  `liveID` holds it → the new socket replaces the old one silently (old closed `1000 reconnected`,
  no event, the session keeps its start time, `fresh` false); another process holds it → `ErrBusy`
  and nothing changes, unless `takeover`: the old session is closed `4002 taken_over`, the AI stays
  online (no offline/online events) and the session's start time is the new one's. `Presence.Busy(
  agentID, liveID)` answers the pre-upgrade check, `Presence.Live(agentID)` the live session
  (`Live.Since`). `Detach` of a replaced or taken-over socket does nothing, so only the live
  session's end announces the AI offline (SES-06, immediate on socket close). The `last_seen_at`
  stamp in `Attach` is best effort.
- **`Registry.Attach(ctx, agent, session, harness, liveID, takeover)`** is what the gateway calls:
  `Presence.Attach`, then for a fresh session the app (`harness`, normalized by `cleanHarness`) and
  `connected_at`. A refused attach changes nothing, not even the app. `appMu` serializes it with the
  check-and-write of a tools-only relabel (`relabel`), so a tools-only call never overwrites the
  app of a session attaching at that moment (AID-04).
- **Gateway order** (deviates from design.md, which resolved with `live=true` before the check and
  so relabeled a refused connection): authenticate → `ResolveFolder` with **no harness** (creates or
  adopts, relabels nothing) → unless takeover, `Presence.Busy` → **409 `identity_busy`** before the
  upgrade → `Accept` → `Registry.Attach` → welcome and backlog. `error.details` of the 409:
  `display_name`, `app` (harness id), `app_label` (readable, "Claude Code") and `connected_at`
  (RFC 3339, UTC) of the live session (`dto.BusyDetail*`; `app_label` is an addition to the design's
  list so the channel needs no label table). Losing the race between the check and the attach closes
  the new socket `4003 identity_busy` before any frame. `Gateway` has a `beforeAttach` seam for the
  test of that race. Close codes: 4001 `deleted`, 4002 `taken_over`, 4003 `identity_busy`; 4000
  `replaced`, `CloseReplaced` and `ReasonReplaced` are gone (AGT-04 is superseded).
- `dto.Error.Details` / `httpx.Error.Details` (`map[string]string`, omitted when empty) carry facts
  of a refusal that are not request fields. `GET /agent/session` reads `online` and `connected_at`
  (UTC) from the live session, so the launcher prompt and the busy answer agree on "since".
- **Channel link** (`internal/channel/link.go`): `stateBusy` (409 `identity_busy` or close 4003)
  retries after 2 s, 4 s and 4 s (`timing.busyRetries`; empty means none) and then stops; the retry
  counter restarts once a connection has been made. `stateTakenOver` (close 4002) never reconnects
  and prints one stderr line. `stateInvalid` (422) never retries and shows the server's reason in
  tool errors. A 409 with another code is still the agent limit; `replacedPause` became `limitPause`.
  `Options.Takeover` / `ListenOptions.Takeover` (from `PARTYLINE_TAKEOVER=1`, read by
  `resolveAgent`) make every dial carry `X-Partyline-Takeover: 1` until one succeeds, so a
  reconnect after a drop never takes over. The live id (`channel.NewLiveID`) is the same on every
  dial of a process.
- Tools answer `partyline: this intelligence is open in another session; Partyline is not connected
  here` while busy (T18 replaces it with the exact SES-04 text) and the SES-05 sentence once taken
  over. A tool call made before the first connection attempt has been answered goes through, as for
  any link that is still connecting.
- Testkit: `Folder.Live` and `Folder.Takeover`; `DialFolder` gives every dial its own live id
  (a dial is a process of its own) unless `Live` is set; `testkit.NewLiveID`. Removed with the
  superseded behavior: `TestAgentWSSecondConnectionReplacesFirst` (AGT-04),
  `TestPresenceReplaceAndOffline`, `TestLinkPausesAfterReplaced` and `TestToolsReportReplaced`.

## T38 - `partyline login` (CLI-01)

- `loginCmd` (`internal/cli/login.go`, `partyline login [--url] [--token-stdin]`): instance URL
  from `--url` → `PARTYLINE_URL` → the saved config → the prompt; normalized (no trailing
  slash) and checked with `GET /healthz`. The agent token comes from the first stdin line with
  `--token-stdin` (empty → exit 2), else from the unchanged browser approval flow
  (`approveInBrowser`: page + code, polls every 2 s, DEV-01..03/DEV-06 texts, the
  "Save this agent token of @…" confirmation and the `--yes` warning for setup). Denied and
  expired approvals print the existing messages on stderr and exit 1; nothing is saved.
- The token is verified with `GET /api/v1/agent/whoami` (`agentWhoAmI`, `dto.IntelligenceRef`)
  **before** `cliconfig.Save` writes `{url, token}` with mode 0600, and login prints
  `Logged in as @<handle> on <instance> (machine <hostname>)` from the whoami handle, not from
  the approver name. Exit codes: 0 saved, 1 denied/expired/rejected/unreachable, 2 usage.
- `loginFlags{token, yes, name}` is the seam setup reuses in T40: `name` prefixes messages
  (`partyline login:`/`partyline setup:`) and `yes` keeps setup's DEV-06 behaviour.
- Not in T38: opening the setup menu after login (CLI-02, wired in T40).

## T40 - `partyline setup` as a checklist (CLI-02..07)

- **Order**: parse and validate `--harness`/`--all-detected`/`--remove` (a mistyped name exits 2
  before any credential is written) → `--uninstall` → credentials. `--url/--token/
  --token-stdin/--browser` run `loginFlow` first (CLI-07; same texts and 0600 save as T38).
  With none of them, a saved `{url, token}` is required: otherwise setup prints
  `partyline setup: Run partyline login first` and exits 1 (CLI-06). Saved credentials are
  verified with `GET /agent/whoami` and setup prints `Using the saved agent token.` Exit codes:
  2 for usage/missing selection flag, 1 for login or harness failures.
- **Menu** (CLI-03/04): terminal = `stdio.isTerminal(0)` and `(1)` (design: stdin and stdout).
  Items are every `harness.All` registrar with `Installed = Detected()`, `Registered =
  len(Entries()) > 0` (an unreadable entry set counts as unregistered) and
  `Selected = Installed || Registered`, so detected unregistered harnesses are pre-selected
  and a plain Enter changes nothing. Apply: selected+unregistered → `Apply`, deselected+
  registered → `Remove`, everything else untouched; one line per changed or failed harness
  (`✓ <Title>: registered|removed` on stdout, `✗ <Title>: <err>` on stderr) and
  `Start a session with partyline <first registered id>`. Backups use the existing HAR-05
  settle rule. A cancel (`q`/lone ESC/**Ctrl+C, byte 0x03**) exits 0 without changes or calls.
  The menu is drawn by `cli.Menu` with `Width` from `stdio.termSize(1)` and raw mode from
  `stdio.rawTerm`.
- **No terminal** (CLI-05): without a selection flag setup prints
  `partyline setup: no terminal: pass --harness a,b, --all-detected or --remove a,b` and exits
  2. `--harness a,b` registers those only (an already registered one is untouched and still
  named first), `--all-detected` registers every `Detected()` harness, `--remove a,b` removes
  Partyline from them; `--yes` is accepted and ignored. `--harness none` (or
  `--all-detected` matching nothing) registers nothing and prints the existing
  "No supported harness found" hint.
- **CLI-02**: `partyline login` on a terminal, after a successful login, opens the same menu
  when no registrar reports entries; with any registered harness it returns without drawing
  the menu.
- Spec-faithful deviations: registered-but-not-detected harnesses start checked (the spec only
  names detected unregistered ones); `--remove` alone does not register the detected ones;
  `--harness` and `--all-detected` together are a usage error.
