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

## T18 - Busy and taken-over sessions explain themselves (SES-04, SES-05)

- **Busy text** (every tool, `link.Err()` while `stateBusy`): `<name> is open in another session
  (<app label>, since HH:MM). Partyline is not connected here. To take over, restart with
  "partyline <harness> --take-over" or set PARTYLINE_TAKEOVER=1.` Name, app label and time come from
  the 409's details (`BusyDetail*`) and follow the latest refusal, so a retry that finds another
  session names that one; HH:MM is 24-hour in the channel process's local zone. `<harness>` is the
  channel's own harness id (the launcher id). A socket the server only closed `4003 identity_busy`
  tells nothing: the text then reads `This intelligence is open in another session. Partyline is not
  connected here. …`. The `--take-over` launcher flag arrives with T36; `PARTYLINE_TAKEOVER=1` works
  from T17.
- **Taken-over text** is the SES-05 sentence, exactly: `This intelligence was opened in another
  session; Partyline is disconnected here.` Every tool answers it, and `channel.Run` sends it once
  as a channel notification when the link enters `stateTakenOver` (the link never reconnects, so it
  is once). Frames queued but not yet delivered when the close arrives are dropped and stay
  unacknowledged for the new session's backlog: nothing is delivered after the notice.
- **Connection notice** (`connectionNotice`): a `Notification` whose meta is `type=notice`,
  `reason=busy|taken_over`, `author=Partyline`, `author_kind=system`, `sender_name=""`,
  `trusted=false`, `kind=system` and no room keys. `type` is how adapters tell it from a room
  message; server notices (skipped backlog, room instructions) keep their shape and stay messages.
- **Link**: `link.onLost(reason, text)` is called once from `run` when the link gives up for
  another session: for busy after the last retry, for taken over at once. The link itself prints
  nothing for these two states. `Run`'s handler writes the text to stderr and notifies for taken
  over (the busy channel stays silent and keeps answering its tools). `Listen`'s handler writes the
  notice as one JSON line (the notification's meta plus `content`), cancels the link and `Listen`
  returns `channel.ErrSessionLost`; `partyline listen` turns that into exit code **3** with nothing
  more on stderr (`exitSessionLost`). Busy waits for the 2 s, 4 s, 4 s retries first, so `listen`
  of a busy AI ends about 10 s after it starts. `Options.zone` / `ListenOptions.zone` are test seams
  for the person's zone.
- **Adapters**: the pi and OpenCode supervision block (identical in both) has
  `EXIT_SESSION_LOST = 3` and does not restart after it, whatever else it was doing (every other
  code, `null` included, still restarts with the backoff). A `type: "notice"` line is shown through
  the harness's notification path (`ctx.ui.notify`/stderr in pi, the OpenCode log) and never framed
  or sent to the model. The omp extension does the same for a `mcp_notification` whose meta says
  `type: "notice"`, via `ui.notify`. Shared fixtures: `integrations/testdata/framing.ts` `notices`.

## T43 - Avatar, kind badge and name components (PROF-08, PROF-09)

`web/src/components/intelligence/` holds the profile pieces every later page reuses. They take
the DTO's `IntelligenceRef` as a prop, so they do not depend on the T42 data layer.

- `IntelligenceAvatar({ intelligence, size?, className? })`: `ui/avatar` with `AvatarImage` when
  `avatar_url` is set (alt = display name, 24/32/40 px by `size`), else `AvatarFallback` with the
  initials of the first and last word of the display name (one-word name → one letter, empty →
  `?`). The fallback background is one of eight token utilities (`primary`, `tape`, `secondary`,
  `accent`, `success`, `warning`, `info`, `destructive`) picked by a hash of the intelligence
  id: the same id always gets the same colour (also after a rename) and different ids spread
  over the palette. `initials()` and `avatarTone()` are exported for the conversation list.
- `KindBadge({ intelligence, viewerId?, className? })`: "You" when `viewerId` is the
  intelligence id, else "AI" for kind `ai` regardless of the display name, else the platform
  label (`WhatsApp`, `Telegram`, …) for a `person`, else nothing for a user. Text comes from
  `intelligence.you` / `intelligence.ai` (pt-BR "Você" / "IA"); platform names stay untranslated
  brand labels.
- `IntelligenceName({ intelligence, viewerId?, showHandle?, className? })`: display name +
  `KindBadge`; in the default form the name is a button whose tooltip shows `@handle` on hover
  and on keyboard focus (it is in the tab order and has a focus ring). `showHandle` renders the
  handle inline for detail rows instead. An empty handle renders no tooltip and no inline text.
- Tests: `intelligence.test.tsx` renders the three components from synthetic intelligences and
  stubs the image loader (happy-dom never loads images) for the avatar-image case.

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

## T36 - Takeover prompt in the launcher (SES-01, SES-02, SES-03, SES-07)

- **Flags**: leading `--take-over` (bare, anywhere among the leading flags) is consent to take
  the AI's live session over; it is stripped from the harness's arguments. `--as` (and
  `--name`, unchanged alias) and the ignored `--project` keep their T15 behaviour, including
  the `--project is ignored: an AI is identified by its folder` line; `--name` gains no notice
  (the T15 contract only documents the alias). `partyline --help` names `--take-over`.
- **Folder**: the lookup asks about the folder the session is being started in, i.e. the
  launcher's canonical working directory. An inherited `PARTYLINE_FOLDER` is not used (as for
  the folder the launcher passes to the harness). The request sends the same folder headers as
  every process (`X-Partyline-Machine/-Folder/-Home/-Harness/-Adopt`, folder and home
  `url.PathEscape`d) with the `Authorization: Bearer` token; the harness header is the launcher
  id.
- **Credentials**: `cliconfig.Resolve` (no flags: `PARTYLINE_URL`/`PARTYLINE_TOKEN`, else the
  saved config). Without a URL or token, or when the config cannot be read, the launcher asks
  nothing and starts the harness; the channel then reports whatever it finds.
- **Lookup**: `GET /api/v1/agent/session` with a 2 s timeout (`launcherSessionTimeout`). Any
  failure (unreachable, timeout, non-200, invalid JSON) starts the harness without asking.
- **Prompt** (SES-01..03): only for harnesses that open a live session (`claude`, `opencode`,
  `pi`, `omp`), only when the AI is online and **both** stdin (fd 0) and stderr (fd 2) are
  terminals. Text, on stderr, with no trailing newline:
  `<display name> is open in another session (<app label>, since <HH:MM>). Take over? [y/N] `.
  The app label is `agents.HarnessLabel` of `SessionState.App` (the same label the busy text
  uses, not the harness id); HH:MM is `connected_at` in the launcher's local zone (`stdio.now`'s
  location; `time.Local` in production), and `time.Now` only if the server omitted it. The
  display name falls back to `This intelligence` when the ref is absent.
- **Answer** (SES-02, SES-03): `y`/`yes` in any case → start with `PARTYLINE_TAKEOVER=1`;
  every other answer, an empty line or EOF → exit **1** and start nothing. Terminal check:
  `stdio.isTerminal(0) && stdio.isTerminal(2)` (nil: `term.IsTerminal`).
- **Consent without asking** (SES-07): `--take-over` or `PARTYLINE_TAKEOVER=1` skips both the
  lookup and the prompt and sets `PARTYLINE_TAKEOVER=1`. Without a terminal the launcher does
  not ask and starts without takeover; a live session then makes the channel report busy.
  Without consent an inherited `PARTYLINE_TAKEOVER` is dropped from the harness environment
  (a restart never takes over by accident).
- **Codex and Gemini** (design Spec issue 6, confirmed): they never take over, and
  `--take-over`/`PARTYLINE_TAKEOVER` have no effect on them (the environment variable is
  dropped). When the lookup says the AI is online they print one line to stderr and start:
  `<display name> is also open in another session (<app label>, since <HH:MM>); this session
  uses tools only`.
- **Seams**: `stdio.isTerminal`, `stdio.stdin`, `stdio.now` (zone) and `stdio.sessionHTTP`;
  tests never exec a real harness (`stdio.exec`) and use `testkit` for the end-to-end takeover.
  The environment still carries `PARTYLINE_FOLDER`, `PARTYLINE_HARNESS`,
  `PARTYLINE_TAKEOVER` (only when taking over) and `PARTYLINE_AGENT`.

## T19 - Access for DMs and group members (DM-08, AD-011)

- **`access.UserRoomLevel`** (and `RoomLevel` for user principals), after the active-user check
  (AUTH-10: disabled and unknown users resolve to none):
  - *group*: the highest of ownership (manage), the user's and everyone's grants and, as a `user`
    member, **post**. A `post` or `manage` from grants/ownership short-circuits the membership
    lookup.
  - *DM*: **post** for a participant (`rooms.dm_a`/`dm_b` equals the user); **view** when a
    participant is an AI the user owns or was granted `use` on **by name**; none otherwise. Only
    the two participant columns are read: member rows, the row's `owner_id` and grants are not.
    A grant row on a DM (only possible by writing around the API) gives nothing, and admins get
    nothing (AD-005).
  - **Clarification of DM-08 / Spec issue 5:** the everyone grant (`*`, "public use") on an AI does
    **not** make its DMs viewable by everyone. "Users granted `use`" reads as users named in a
    grant; otherwise sharing an AI with the whole instance would let every user read every other
    user's DM with it. The AI's owner and named `use` grantees view (`access.CountSubjectGrants`).
  - A DM between two AIs is viewed by both owners and both AIs' named grantees; nobody posts
    through the web (only the AIs, through `AgentRoomLevel`: an agent member posts).
- **`AgentRoomLevel`** is unchanged (an agent member posts), which covers the AI side of a DM.
- **`GrantedRoomLevels`** now reads `ListUserRoomLevels` (it replaces `ListGrantLevelsByKind`):
  grants on **groups** (own or everyone), `user` membership of a group (post) and DMs the user
  takes part in (post), highest wins. Ownership stays out, and so do DMs viewed only through an
  AI and grants on DMs. `ListRoomsForUser` selects the same candidates (owned, granted and member
  groups, own DMs) and **`rooms.Service.List` no longer turns `owner_id` into manage for a DM**
  (a DM has no owner to manage it, DM-02). So `GET /rooms`, `/activity` and the live hub's
  initial visible set include a user's DMs and member groups; the DMs an AI owner only views are
  reachable by id and are not listed (Spec issue 5).
- **Sharing**: `Authorizer.SetVisibility` on a DM returns `access.ErrDMNoSharing` before it
  validates or writes anything (no grant, no audit entry, no `GrantsChanged`). `rooms.Service.
  SetSharing` checks view first (stranger: 404), then the DM (**422 `dm_no_sharing`**,
  `dto.ErrorCodeDMNoSharing`, for anyone who can view the DM), then manage (403): so a DM
  participant gets 422, not 403, on `PUT /rooms/{id}/sharing`.
- **Live**: adding or removing a `user` member publishes `events.GrantsChanged{room}` after
  `MembersChanged`, so the hub sends the new member `room.changed` (with their level) plus
  `access.changed`, and the removed member `room.deleted` (membership is an access source next
  to grants). Message frames follow `view`: the owner of an AI and its named grantees receive
  `room.message` for the AI's DMs, strangers nothing.
- **`CanSee` consequence**: a user member of a group now "shares a viewable conversation" with
  the AIs in it, so they can read those AIs' profiles (not edit).
- `dto.ConversationKind` (`group` | `dm`, `ConversationKindGroup`/`DM`) is the room kind type;
  `Room.kind` itself arrives with T20.
- Tests: `internal/access/access_test.go` (DM matrix, owner column and grants ignored, public AI,
  AI-to-AI and user-to-user DMs, disabled users, group membership, `GrantedRoomLevels`,
  `SetVisibility` on a DM) and `internal/api/room_membership_access_test.go` (the DM auth matrix
  over every room route, 422 `dm_no_sharing`, the room list, member posting over HTTP, live
  frames). DMs are stored by the test helper `storeDM` until T20 adds `OpenDM`.

## T20 - Direct messages (DM-01, DM-02, DM-08)

- **`conversations.Service`** (`app.App.Conversations`; `conversations.New(Deps{Store, Rooms,
  Intelligences, Bus, AllowCreate, Now})`) is new. `OpenDM(ctx, p, withID) (rooms.Room, created bool,
  error)` acts as the user of a user principal and as the AI (`p.AgentID`) of an agent principal,
  whose visibility is its owner's (`p.UserID`). Order of the checks: principal kind → empty `with`
  (422 `invalid`, field `with`) → `with` is the caller (`ErrDMSelf`) → `Intelligences.CanSee`
  (`ErrContactNotFound`; an unknown id and an invisible one are the same answer, even in the
  message) → role (user and AI go on; a person is `ErrNoRouteToPerson`; an intelligence without an
  account is `ErrContactNotFound`) → the pair's existing DM, returned with `created` false and
  **without asking the limit** → for an AI that has to create one, `Deps.AllowCreate(agentID)`
  (`ErrRateLimited`; nil allows; the registry's 10 per hour arrives with T24) → creation.
- **Creation** is one write transaction: `InsertDMRoom … ON CONFLICT DO NOTHING` on the sorted pair
  (`dm_a < dm_b`, partial unique index `rooms_dm_pair`) plus one member row per participant. A caller
  that lost the race gets the winner's room and `created` false, so exactly one of any number of
  concurrent opens (from either side) is a 201. An AI that loses the race has still used one slot of
  its limit. The room: `kind dm`, `name ''`, `owner_id` = `p.UserID` (the user who opened it, the
  AI's owner for an AI), `broadcast 1`, `bridge_chats 0`, retention and everything else the table
  defaults (90 days); members `user` (users) or `agent` (AIs, `delivery all`, outbound on,
  `added_by` = `p.UserID`). The owner column gives no access (T19). After the commit:
  `RoomChanged` and `MembersChanged`, so the other participant's sockets (and an AI owner's, who
  views it) get `room.changed` with their level.
- **DM-05 (this task's part):** a person target is always 422 `no_route_to_person`, since a person
  is reached through a platform conversation only. T30 adds that route (a DM chat of the person on a
  connector whose operator is the caller) in front of this refusal. Nothing else refuses a disabled
  target at open time: such a DM can be opened and is read-only (`counterpart_disabled`).
- **`POST /api/v1/conversations/dm`** `dto.OpenDMRequest{with}` (an intelligence id) → `dto.Room`:
  **201** when created, **200** for the existing DM. Sessions and user tokens only (an agent token is
  403 like on every non-agent route; the AI's own route is T24's `POST /agent/dms`). Errors: 422
  `dm_self`, 404 `contact_not_found` (`dto.ErrorCodeContactNotFound`), 422 `no_route_to_person`, 422
  `invalid` for a missing `with`, 400 for a malformed body, 429 `rate_limited` (AIs only, T24).
- **`dto.Room.kind`** (`group` | `dm`) and **`participants`** (the two `IntelligenceRef`s of a DM in
  id order with their `role`/`platform`; always an array, empty for a group) come from
  `rooms.Room.Participants` (`ListRoomParticipants`, loaded by `rooms.view` for DMs only). A DM has
  an empty `name` (the other participant titles it in the UI) and `member_count` 2.
- **DM guards in `rooms`** (new sentinels `rooms.ErrDMMembersFixed`, `rooms.ErrCounterpartDisabled`):
  - `AddMember`, `UpdateMember` and `RemoveMember` check view first (404 for a caller without it),
    then the DM rule, then manage: whoever can view a DM — a participant, an AI's owner or grantee —
    gets **422 `dm_members_fixed`**, never 403. Nothing is written. (Same order as T19's
    `dm_no_sharing`.)
  - `PATCH` and `DELETE /rooms/{id}` and `resume` stay 403 for everybody who can view a DM, since
    nobody has manage (T19).
  - `Service.Post` (every path that posts as a user or an AI: the web, `reply`, `react`,
    `send_message`) refuses a DM whose other participant is a **disabled user** with **403
    `counterpart_disabled`**; reading, listing and the member list keep working. A counterpart that
    is an AI or a person never blocks. System and chat-authored messages do not go through this
    check.
- **Lifecycle (DM-08 edge case):** deleting an AI deletes its intelligence (trigger) and so its DMs
  with their members, messages, attachments, outbox rows and read cursors (foreign keys).
  `Registry.Delete` now publishes `RoomChanged{Deleted}` for the member rooms that no longer exist
  (their sockets hear `room.deleted`) and `MembersChanged` for the others; a DM the AI's owner only
  viewed was never in that socket's visible set and sends nothing. Blob files of the deleted DMs
  are swept by the retention job like any unreferenced blob. Deleting a user cascades the same way
  without events (as before for owned rooms).
- sqlc: `InsertDMRoom` (`:execrows`), `ListRoomParticipants`. Error codes with their statuses:
  `dm_members_fixed` 422, `dm_self` 422, `contact_not_found` 404, `no_route_to_person` 422,
  `counterpart_disabled` 403 (all in `dto/common.go`, all observed at their status by tests).
- Known interaction, fixed by T23: AIs are members of their DMs, so `GET /agent/rooms` already lists
  them (with an empty name) and `resolveRoom`'s name match must not treat that name as a room name.
- Tests: `internal/api/direct_messages_test.go` (open/reopen from both sides, 12 concurrent opens,
  AI target and the owner's view, refusals, person, authorization matrix with owner / use grantee /
  stranger / admin / agent token / anonymous, members fixed for participant and AI owner,
  counterpart disabled and re-enabled, AI deletion with live `room.deleted`, live announcement) and
  `internal/conversations/dm_test.go` (the agent-principal rules and the `AllowCreate` hook).

## T21 - Groups with members (LAY-04)

- **`POST /api/v1/groups`** `dto.CreateGroupRequest{name, description?, members[]}` → **201** `dto.Room`
  (kind `group`, `my_level manage`). `members` are intelligence ids (users and AIs; the web sends the
  ones it picked, T24's tool resolves handles first). Sessions and user tokens, any user of the
  instance (an agent token is 403 on this path, an anonymous caller 401). The group has the defaults
  of `POST /rooms`: broadcast on, bridge off, 90 days of retention, no instructions. The room's
  `updated_by` is the owner (INS-06).
- **`conversations.Service.CreateGroup(ctx, p, GroupInput{Name, Description, Members})`** serves
  both principals: the owner is `p.UserID` (the AI's owner for an AI, whose own id is added as the
  **first** `agent` member and counts towards the limit). Order: principal → name and description
  (`rooms.ValidateCreate`, 1-100 / 0-1000 code points) → member ids (a blank id, or more than
  `dto.MaxGroupMembers` = **50** distinct ids → 422 `invalid`, field `members`) → every member →
  `AllowCreate` (AIs only) → creation. Validation errors therefore win over refused members, and a
  request that creates nothing never uses an AI's allowance. Repeated ids count once. The owner is a
  member **only when listed** (as `POST /rooms`; an AI's group lists the owner explicitly, AGRP-01).
  The result is `Rooms.Get` for the caller (an AI sees it at `post`).
- **Member rules** are the owner's add-member rules: id → `GetIntelligenceWithRole`; a **user** becomes a
  `user` member when `rooms.CheckMember` accepts it (exists and enabled; a user may list
  themselves), an **AI** an `agent` member when the owner owns it or may use it (a public `*` use grant
  counts); an external **person** is refused (`person`: people join groups through bridged chats,
  never as members) to a caller who can see them (`Intelligences.CanSee`, which follows the
  connector), and as `not_found` to anyone else, so that no caller learns which handles or ids are
  people of connectors they cannot reach; anything else is not found. New in `rooms`: `Service.CheckMember(ctx, ownerID,
  MemberInput) (ok, err)` (the rules of `AddMember`, as a question), `Service.CreateWithMembers(ctx,
  ownerID, CreateInput, []MemberInput) (id, err)` (the one transaction `Create` now uses too;
  callers check the members first) and `rooms.ValidateCreate`. Members get `outbound` on and AIs
  `delivery all`, like `AddMember`; `added_by` is the owner. After the commit: `RoomChanged` (the
  members' sockets learn the group at their level) and `MembersChanged`.
- **All-or-nothing refusal (AGRP-02):** any member the owner cannot add makes the whole request
  **422 `members_refused`** (`dto.ErrorCodeMembersRefused`) and creates nothing: `error.refused` is
  the new list `[{id, reason}]` (`dto.Error.Refused`, `httpx.Error.Refused`, `conversations.
  MembersRefusedError`), **every** refused member in the order asked, valid ones not listed.
  Reasons (`dto.RefusedReason`): `not_found` for an unknown id, a disabled user and an AI the owner
  may not use (it must not be told apart from an id that does not exist — the answers are
  identical) and, for a caller who cannot see them, an external person; `person` for an external
  person the caller can see.
- Rate limit: the hook of T20 is asked once per group an AI actually creates (design: "10 new
  conversations per hour per AI" covers DMs and groups); T24 wires the registry limiter and the
  HTTP route.
- Tests: `internal/api/groups_test.go` (members and what they see, the AI's own room list, owners of
  member AIs and strangers excluded, each refusal reason alone and all together, validation order and
  the 50-member boundary with repeated ids, authorization of the route, live announcement) and
  `internal/conversations/groups_test.go` (an AI's group, its refusals, the allowance).

## T22 - Read state and the conversation list (DM-06, DM-07, LAY-01)

- **`GET /api/v1/conversations`** → `dto.List[dto.Conversation]`, users only (an agent token is 403,
  anonymous 401): the caller's groups (owned, granted or user-member, `Rooms.List`) and the DMs they
  take part in; a DM an AI's owner or grantee only views is **not** listed (T19), a stranger's list
  is `{"items": []}`. Newest activity first (`activity_at` desc, then title case-insensitive, then
  id). `Conversation`: `id` (the room id), `kind`, `title` (the group's name; for a DM the other
  participant's display name), `counterpart` (`IntelligenceRef` with role and platform, DMs only),
  `my_level`, `last_message?`, `unread`, `activity_at` (the last message's time, else the room's
  creation), `member_names`.
- **`last_message`** is the newest message that still exists in the room (retention may have purged the
  latest ones; with none left the field is absent, `unread` 0 and `activity_at` the creation).
  `MessagePreview{seq, author_id, author_name, text, created_at}`: `text` is the first
  `dto.MaxPreviewLength` = **80 code points** of the message (bytes preserved, no ellipsis, no
  whitespace normalisation); a message whose text is blank and that has files is `"[<first file
  name>]"` (files in creation order); text wins over files. `author_id` is the author's intelligence
  (`author_iid`, else the user/AI of an older row; empty for system messages and chat senders
  without a person). **`author_name` is filled in groups only** (LAY-01: the sender prefix is a group
  matter) and is empty in a DM; reactions are messages like any other.
- **`member_names`**: up to `dto.MaxConversationMemberNames` = 20 `"Display name @handle"` of the users
  and AIs that are members, in the order they were added (a bridged chat is not an intelligence and
  adds none until T27); for a DM the other participant alone; always an array.
- **Unread (DM-06)** per user: messages with `seq > MAX(read position, rooms.read_baseline)` whose
  `author_iid` is not the user (so the user's own messages, also the ones typed on their linked
  platform account, never count; system messages, AIs, other users and unattributed chat messages
  do). The baseline is per room (v0.3 history counts as read) and applies to everybody; a read
  position behind it changes nothing, one ahead of it wins. One grouped query for all rooms
  (`CountUnreadByRoom`), `ListLastMessages` and `ListMemberNames` likewise.
- **`PUT /api/v1/rooms/{id}/read`** `dto.MarkReadRequest{seq?}` → **200** `dto.ReadState{room_id, seq,
  unread}` (the position that holds and what is unread after it). View is enough (404 without it,
  also for an AI owner marking the DM they view); users only. `seq` absent → up to the newest
  message; `seq` past the newest → the newest (so a later message is unread); `seq` lower than the
  held position or 0 → the held position stays; negative → 422 `invalid` (`seq`); a non-number → 400.
  The position is `read_cursors.seq` (`UpsertReadCursor` keeps the maximum); an empty room stores
  nothing and answers `seq 0`.
- **Live (DM-07)**: every `MarkRead` publishes `events.ReadChanged{UserID, RoomID, Seq}` — also when
  the position did not move, with the position that holds — and the hub sends **`room.read`**
  (`dto.LiveRoomRead`, data `dto.ReadData{room_id, seq}`) to the sockets of that user only, every
  tab included. Other users, even in the same room, receive nothing.
- `conversations.Service.List(ctx, p)` and `MarkRead(ctx, p, roomID, seq *int64)` implement it
  (`list.go`); `List` reuses `Rooms.List` for the visible set and the levels, so it follows T19.
- Tests: `internal/api/conversations_list_test.go` (order and fields, whose list holds what, unread per
  user with own messages / AI messages / baseline / positions, monotonic and clamped marking,
  authorization of both routes, `room.read` to the reader's tabs only, 79/80/81 code points with
  multi-byte and emoji text, file previews, member names and the 20 cap, retention).

## T23 - AIs receive and see DMs (DM-03, GRP-04)

- **Room naming for an AI.** In every frame an AI receives, a DM's `dto.RoomRef` is `{id, name:
  <display name of the other participant, relative to that AI>, kind: "dm"}`; a group keeps
  `{id, name}` with **no `kind`** (`dto.RoomRef.Kind` is `omitempty`, so every group frame is byte for
  byte what it was). One DM is therefore named differently for its two participants when both are
  AIs. `delivery.RoomRef(id, name, kind)` builds it; the live dispatcher resolves the names of a DM
  once per message (`ListRoomParticipants`) and gives each recipient its own; the backlog, the
  pending path, `history` and `room_info` take the name from the room state, where
  `ListAgentRoomStates` now returns `name` (the counterpart's display name for a DM), `kind`,
  `with_id` and `with_handle` (`agents.RoomState.Kind/WithID/WithHandle`; `ListProjectRoomStates`
  returns the same columns, constant for groups).
- **Delivery rule.** A message posted in a DM passes `all` and `mentions` (it is addressed to the AI)
  and is held back by `trusted` unless the owner wrote it: `delivery.Delivered.DM` /
  `Facts.DM`, set by `LoadFacts(ctx, q, view, roomKind)` — the room kind is a required argument
  now, so the dispatcher, the connect backlog and `GET /agent/pending` all apply it. Keywords,
  mentions by name/handle and replies are untouched in groups.
- **Channel notification meta (`channel.Format`).** New keys: `conversation` (`dm` for a room whose
  ref has kind dm, else `group`, on every message), `author_id` and `author_handle` (the
  intelligence of `Message.Author`, only when it has an id; the handle only when it has one). For a DM
  `room_name` is the counterpart. Room notices (`FormatNotice`) keep their shape (no
  `conversation`), but name a DM by the counterpart through their `RoomRef`. The history tool's
  records, `partyline listen`'s JSON lines, the hooks' frames and `wait_for_messages` are built from
  the same record, so they carry the keys too.
- **Frame.** `channel.Frame` (and `frame()` in the omp, pi and OpenCode adapters, still identical
  copies) puts `conversation="…"` right after `room` when the record has one; records without it
  render as before. `integrations/testdata/framing.json` gained three cases (a DM from the owner, a
  DM from another user with markup in names and text, a group message from a chat); the Go and TS
  suites read the same file. Every existing test that asserted a whole meta map or a framed text was
  extended with the new keys and nothing else.
- **Agent routes.** `GET /agent/rooms` returns `dto.AgentRoom{id, kind, name, description, with?,
  members}` for groups and DMs (`with` is the other participant's `IntelligenceRef`, DMs only; a DM's
  `name` is the counterpart's display name). `resolveRoom` finds, in this order, a room by **id**, a
  DM by **`@handle`** of the other participant (case-insensitive, the AI's own DMs only), else a
  **group** by name (case-insensitive or slug). A DM is never found by a name: its title is not
  unique, and an empty room name used to slug to `agent`. Anything else is the old 403 "not a member
  of room <ref>"; a name shared by two groups is still 409. `room_info` answers the DM's ref with
  `kind`. `history`, `send_message`, `send_file`, `reply` and `react` work on a DM like on a group.
- **Channel tools.** `list_rooms` entries have `kind`, and `with{id, name, handle, kind}` for a DM
  (omitted for a group); the `room` argument descriptions now say "room id, room name, or @handle of
  the intelligence you have a DM with". `Instructions`, `ToolsOnlyInstructions` and `OmpInstructions`
  explain `conversation` (the channel's in meta keys with `author_handle`, the framed ones in
  attributes and `list_rooms`); no existing sentence changed.
- Docs: `docs/agents.md` shows the new keys and the DM addressing.
- Tests: `internal/api/dm_agent_test.go` (a user's DM to a live AI with the reply in the same DM and
  another user's DM kept apart, groups marked `group`, `all`/`mentions`/`trusted` in DMs with a
  group control, DM backlog and pending under `mentions`, `GET /agent/rooms`, resolution by id /
  handle / name), `internal/channel/dm_test.go` (the real channel: notification, tools by `@handle`,
  `list_rooms`, the instructions), `internal/channel/format_test.go`, `internal/delivery/
  wants_dm_test.go`, and the shared framing cases (Go `TestFrameMatchesTheAdapters`, `bun test`).

## T24 - AI-initiated DMs and groups (DM-04, AGRP-01, AGRP-02)

- **`POST /api/v1/agent/dms`** `dto.AgentDMRequest{to, text, chat_ids?}` → **201** `dto.Message` (the
  posted message; its `room_id` is the DM), agent token acting as the AI of the folder headers
  (`agentOnly`: a session, a user token, a missing folder and an anonymous caller are 403 / 403 /
  422 `folder_required` / 401). `to` is `"@handle"`, a bare `handle` (case-insensitive) or an
  intelligence id: `conversations.Service.Contact` tries the handle first and otherwise hands the
  reference on unchanged, so a handle that exists but is not visible answers exactly like one that
  does not. The DM is the T20 `OpenDM` of the AI principal, with everything it says: the target must
  be visible to the **AI's owner** (404 `contact_not_found` for an unknown or invisible target, the
  same body), 422 `dm_self`, 422 `no_route_to_person` for a person (T30 refines it), and the room is
  owned by the owner. Writing to an existing DM reopens it silently; the message goes to the same DM
  whatever form of `to` names it. Visibility is the owner's, so it changes with what the owner
  gets: an AI that wrote to a user becomes visible to that user (T7: a shared conversation), who can
  then write to it; an AI the owner may `use` is a target, the one of an owner who shared nothing is
  not.
- **Order and cost.** Validation first (`conversations.ValidateDM`: `to` present and not just "@";
  text non-blank and within 16 000 code points; `chat_ids` count) → 422 `invalid` with fields `to`
  and/or `text`, **before** anything is spent or created → `Agents.AllowPost` (the 30 messages a
  minute) → `SendDM` → `OpenDM` (the allowance below, only for a DM it creates) → `Rooms.Post`. A
  DM this very call created that then refuses the post (for example a `chat_ids` entry that is no
  chat of a new DM, or a disabled counterpart) is **deleted again** if nobody wrote in it
  (`DeleteEmptyDMRoom`, `next_seq = 1`), with `room.deleted` for the sockets that heard of it, so
  no empty DM is left; the creation slot it used stays spent.
- **`POST /api/v1/agent/groups`** `dto.CreateGroupRequest{name, description?, members[]}` where each member
  is `"@handle"`, a handle or an id → **201** `dto.CreatedGroup{room, name}`. It is T21's
  `CreateGroup` for the AI principal (`CreateGroupNamed` resolves the names first): owner = the
  AI's owner, the AI the first member, every member checked by the owner's add-member rules, all
  or nothing. A refusal is 422 `members_refused` whose `refused[].id` is **the reference as the AI
  wrote it** (`"@sage"`, not the id it resolved to; the first reference when two name one
  intelligence), reasons as in T21, valid members left out. Validation errors win over refusals;
  blank entries and more than 50 members are 422 `invalid`.
- **Allowance (DM-04, AGRP-01):** `agents.Registry.AllowCreate(agentID)` is a sliding window of
  `agents.CreateLimit` = **10** new conversations per `CreateWindow` = **1 hour** per AI, DMs and
  groups together, in memory like the post limit (a restart resets it; it follows the app's clock).
  It is asked only when a conversation is about to be created, after every other check: a reopened
  DM, an unknown or invisible target, a refused group and an invalid request cost nothing. The 11th
  is **429 `rate_limited`** ("too many new conversations") and creates nothing; another AI of the
  same owner has its own window. `app.go` wires it as `conversations.Deps.AllowCreate`.
- sqlc: `DeleteEmptyDMRoom` (`:execrows`). `rooms.ValidatePost(PostInput)` is the exported part of
  the post validation.
- Tests: `internal/api/agent_conversations_test.go` (a first DM to the owner seen in their list and
  live, three forms of `to`, visible and invisible targets with identical refusals, self / person /
  validation with 35 invalid requests not spending the post limit, no empty DM after a refused post,
  an AI reaching a live AI of another owner untrusted, group creation with mixed references and the AI
  posting in it, refusals echoed as named, the 10-per-hour window on a manual clock with reopen /
  refusal / other-AI / one hour later, authorization of both routes) and the `AllowCreate` tests of
  T20 and T21 in `internal/conversations`.

## T26 - People from inbound messages (PEOP-01, PEOP-02, PEOP-05, PEOP-06, GRP-03)

- **`connectors.People`** (`people.go`; `Pipeline` builds one from its deps unless
  `PipelineDeps.People` is set). `Seen(ctx, Sighting{ConnectorID, ConnectorKind, Chat, SenderID, AltID,
  Name, At}) (db.Person, error)` runs for every inbound message **that reaches at least one room**:
  after the chat is recorded and routed (the catch-all may route it) and the repeats are dropped,
  before the message is posted. An unrouted chat, a chat refused by a catch-all cap (ROUTE-11), a
  pairing command, an empty message and a repeat record nobody: CON-07 keeps nothing of unrouted
  chats, and a sender id is a phone number or an account id. A message with neither a sender id nor an
  alternative id has no person (the zero `db.Person`, `author_iid` empty). T30 will call it earlier
  for the platform DMs it routes.
- **Who is a person**: the person of (connector, id) found by `GetPersonByExternalID` for the sender id
  and then the alternative id (a match on `external_id` before one on `alt_external_id`), so the same
  account seen under its phone or its LID is one person, in either order; a person of another connector
  never matches (AD-008). Concurrent first messages of one sender create one person: the write
  transaction decides again under the single-writer lock. A sender id of `""` with an alternative id
  uses the alternative id, as `ResolveSender` does.
- **Creation (PEOP-01)**: `intelligences.Create` (`handles.Person`, kind `human`) with the platform name
  cleaned by `CleanDisplayName`, else `"<Platform> contact"` (`connectors.PlatformLabel`: `WhatsApp
  contact`); the handle derives from that name across the instance's one namespace (`customer`,
  `customer-2`, `alice-2` when the user has `@alice`). `people`: `external_id` = the sender id,
  `alt_external_id` = the other id (or ''), `platform_name` = the cleaned platform name ('' when it gave
  none), `created_at` = now, `last_seen_at` = the message time.
- **Alternative id**: `external_id` never changes; `alt_external_id` is the id of the message that is not
  the external id, kept while the message still reports it and replaced by the latest other id otherwise
  (a person first seen without an alt learns it from a later sighting; a message without ids keeps it).
- **Name (PEOP-05)**: a non-empty cleaned platform name that differs from `platform_name` is stored in
  `platform_name`; the intelligence's display name follows it unless `name_locked` (then only
  `platform_name` moves). The handle never changes by itself. An empty or blank name changes nothing,
  and a person first seen without a name takes the first name it shows. A display-name change publishes
  `events.IntelligenceChanged` (live `intelligence.changed` to the users who can see the person);
  creating a person publishes nothing.
- **Lock (PROF-11, brought forward from T27)**: `intelligences.Service.Update` sets
  `people.name_locked` in the same transaction when the display name of a **person** changes (not for a
  bio or handle edit, nor for a rename to the name it already has), so PEOP-05's "unless the user
  renamed the contact" holds end to end. T27 adds visibility and the profile on top of it.
- **Chat links**: `chat_people` gets a row for (chat, person) on first sight, and `chats.person_id` names
  the person for chats of kind `dm` (the counterpart; the latest person to write in the chat wins; a
  person known from a group becomes the counterpart of a DM chat it later writes in). Group and channel
  chats keep `person_id` NULL.
- **Throttle**: `people.last_seen_at` and `chat_people.last_seen_at` are written when missing or when the
  message time is at least `connectors.SeenInterval` (10 minutes) after the stored one, so they follow
  the message's time, never move back, and a message that changes nothing is answered from the read pool
  without a write. A message time in the future (a phone with a wrong clock) or zero counts as now.
- **Author (PEOP-02, GRP-03, PEOP-06)**: `rooms.ChatMessage.AuthorIID` is stored as `messages.author_iid`:
  the user a verified sender is linked to (`ResolveSender`, AD-008 unchanged), else the person.
  `author_kind` stays `chat` and `author_id` the chat; `author_user_id` and therefore `trusted` are
  exactly what they were (a person, a sender named like the owner, a senderless message and a sender
  verified as another user are never trusted). The timeline, the live frame and `author_name` show the
  person's current display name, with `author.platform` = the connector kind; a chat message without
  any sender id still shows the chat's title and has no author id. History keeps the person after a
  sender is linked, and the person row stays (T27 shows it as an app of the user).
- sqlc added: `GetChatPerson`.
- Tests (`internal/connectors/people_test.go`): a new sender (profile, live frame, unrouted chat), a
  nameless sender, `PlatformLabel`, people keyed by sender (two chats, same name twice, handle namespace),
  per connector, phone/LID folding in both orders and learning the alt id, a linked sender, trust for six
  author cases through a live agent, platform rename (+ live `intelligence.changed`, blank names, first
  name later), the user's rename lock (bio and handle edits do not lock), senderless messages, DM
  counterparts (new and known person), the 10-minute throttle and the future clamp, concurrent first
  messages.

## T27 - People visibility, person profiles and group participants (PEOP-03, PEOP-04, PROF-11, GRP-02)

- **Visibility** (`Service.rights`, so `CanSee`, `CanEdit`, `Get`, `Update`, avatars and the live
  `intelligence.changed` filter): a person is seen **and edited** by the users who own or may `use` its
  connector, whatever else they can view (a user principal edits; an agent principal never does). A user
  without that access still **sees** it (read-only: PATCH and avatar writes are 403) when they can view,
  at `access.UserRoomLevel` view or more, a room that involves it: one with a chat member where the person
  was seen (`chat_people`) or where it wrote a message (`messages.author_iid`, so it still counts after
  the chat left the room). Everybody else gets 404, admins included (AD-005). **Reconciliation:** PEOP-04
  ("nobody else does") is the rule of the contacts and of editing (T28 lists a connector's people for its
  users only); the design's security section lets a viewer of a conversation open the profile of the
  people they read in its timeline, so the profile link of a message author never 404s. Their profile
  carries the summary only (no `person`, apps without the account).
- **`dto.IntelligenceProfile.person`** (`PersonDetail{connector: ConnectorRef{id, kind, name},
  external_id, linked_user?, name_locked}`) is set for the users who own or may use the connector
  (`PersonInfo.CanUse`). `linked_user` is the user whose verified identity (AD-008, `ResolveIdentityUser`,
  enabled users only) is the account (PEOP-02). `apps` is `[{kind: <connector kind>, label: "<Platform>:
  <external id>", connector_id}]` for them and `[{kind, label: "<Platform>"}]` (no account, no connector)
  for a viewer of a conversation. `intelligences.Profile.Person *PersonInfo{db.Person, Connector, CanUse,
  LinkedUser}` carries it; `Profile.Platform` is the connector kind as before.
- **`conversations`** of a person (`Service.Conversations`, rendered by `writeProfile`, also on the PATCH
  and avatar answers): the rooms of `ListPersonRoomIDs` that the caller can view, most recently active
  first (the newest existing message, else the room's creation; ties by name, then id), at most
  `intelligences.MaxConversations` = 100 of the viewable ones. `RoomRef{id, name}` (no `kind` for groups;
  `kind: dm` is reserved for the platform DMs of T30). The other kinds list none (the AI DMs of Spec issue
  5 are not filled by this task).
- **Name lock** (PROF-11): the lock itself landed with T26 (`Service.Update` sets `people.name_locked`
  when a person's display name changes); `PersonDetail.name_locked` shows it to the connector's users.
- **Group participants** (GRP-02): `GET /rooms/{id}/members` adds `people` (`[]IntelligenceRef`, role
  `person`, `platform` = the connector kind) to each **chat** member: the people of `chat_people`, most
  recently seen first (ties by person id), at most `dto.MaxChatPeople` = 200 per chat. Omitted for agent
  and user members and for a chat nobody was seen in. A person seen in two chats is listed under both;
  a sender linked to a user stays a person here (the timeline names the user, the profile's `linked_user`
  names the link). View on the room is enough. Only this endpoint fills it: the add/update member answers
  and the agent tools (`list_rooms`, `room_info`) are unchanged, so an AI's context does not carry up to
  200 people per chat. `Service.ChatPeople(ctx, chatIDs)` does not authorize; the handler already did.
- sqlc added: `ListChatPeopleRecent` (one query per chat member: sqlc cannot express the per-chat window
  as a single query), `ListPersonRoomIDs`, `ListRoomActivity`. DTOs: `ConnectorRef`, `PersonDetail`,
  `IntelligenceProfile.person`, `RoomMember.people`, `MaxChatPeople`; tygo regenerated.
- Tests (`internal/api/people_test.go`): the profile matrix (owner, use grantee with no room, viewer of a
  room, stranger, admin, anonymous, agent token; PATCH, avatar PUT/DELETE/GET and the live frame), the
  conversation rules one by one (room view toggled, a room where it wrote after the chat left, a room
  whose chat saw it), the person page (account, conversations by caller, order, the 100 cap), the rename
  lock through a grantee, the linked user, and the members list (order, two chats, the 200 cap, other
  member kinds).

## T28 - Contacts API (CONT-01, TREE-03, TREE-04, TREE-06)

- **`GET /api/v1/contacts`** (sessions and user tokens; an agent token is 403, anonymous 401) →
  `dto.Contacts{me, machines, unlinked, shared, users, people}`, every list always an array:
  - `machines`: the caller's own AIs that have a folder, grouped by `agents.machine` into
    `MachineTree{hostname, label, online, nodes}` built by `intelligences.BuildMachines` (T25): machines with
    an online AI first, then by label (the owner's label, else the hostname) case-insensitively. Only
    machines with at least one such AI appear. `TreeNode{ai, path, name, children}` as the design's tree
    section; `AIContact{intelligence, owner?, machine, folder, folder_label, app, online, last_seen_at?}`.
    `folder_label` is `~`/`~/x`/absolute by the machine's reported home; `app` is the harness id;
    `last_seen_at` is `agents.last_seen_at` (when the AI last went offline; none for an AI never
    connected). Own AIs carry no `owner`.
  - `unlinked`: the caller's own AIs with no folder (TREE-06), the most recently seen first (never seen
    last), then by name.
  - `shared`: the AIs of other users that the caller may use (a `use` grant to the user or to everyone),
    by name, each with its `owner` and its folder labelled with the **owner's** home for that machine.
  - `users`: the other enabled users, by name (case-insensitively), as `IntelligenceRef` role `user`.
  - `people`: one `PeopleGroup{connector: ConnectorRef, count}` for **each connector the caller owns or may
    use** (also with a count of 0), in the connectors list order (name). The count leaves out people whose
    account an enabled user verified (PEOP-02: that account is an app of the user, see below); an account of
    a disabled user counts as a person again. Same match as `ResolveSender`.
- **`GET /api/v1/contacts/people?connector_id=&q=&after=&limit=`** → `dto.Page[IntelligenceRef]` (role
  `person`, `platform` = the connector kind), in name order (`lower(display_name)`, then id), without the
  accounts verified as a user. `limit` 1..200 (default 50, `dto.DefaultPeoplePage`/`MaxPeoplePage`);
  `has_more` tells whether a further person exists; `after` is the id of the last person of the previous
  page (a keyset cursor: no gaps or repeats across equal names, and a person renamed meanwhile only moves
  itself); `q` (at most 100 code points, trimmed, blank = no search) matches a substring of the display
  name, the handle or the account id, **case-insensitively for ASCII only** (SQLite's `lower()`; `%` and
  `_` are ordinary characters). Errors: **404 `not_found`** for a connector the caller neither owns nor may
  use (also an unknown id), **422 `invalid`** with the field `connector_id` (missing), `limit` (not a
  number, <1 or >200), `q` (>100) or `after` (not a person of that connector, e.g. a user). Owner and use
  grantee may read; strangers, other members and admins get 404.
- **`PUT /api/v1/machines/{hostname}`** `dto.SetMachineLabelRequest{label}` → `dto.Machine{hostname, label}`
  (TREE-04). The machine is the caller's own `machines` row; another owner's machine, a hostname the
  caller has no machine of and a hostname nobody has are all **404**. The label is trimmed and stripped of
  control characters; at most 64 code points (**422 `invalid`**, field `label`, nothing changed); empty or
  blank resets it to the hostname. Two owners may have a machine of the same hostname and label their
  own. It shows as `MachineTree.label` and as `AIDetail.machine_label`. No event is published.
- **`GET /api/v1/agent/contacts/tree`** (agent token, no folder, no AI needed; a session or user token is
  403 "agent token required") → `dto.AgentContactsTree{machines, unlinked}`: the token owner's own AIs
  only, built like `contacts` (T41 prints it).
- **Linked accounts are apps (PEOP-02)**: `IntelligenceProfile.apps` of a **user** is the web app plus
  `{kind: <connector kind>, label: "<Platform>: <external id>", connector_id}` for each chat account the
  user verified, shown to the user and to the users who own or may use the connector it was linked
  through (they can already read `person.linked_user`); nobody else sees them.
- Service: `intelligences.Service.OwnAIs(ctx, userID)`, `Contacts(ctx, userID)`, `People(ctx, userID,
  PeopleQuery)`, `SetMachineLabel(ctx, userID, hostname, label)`; `intelligences.Deps.Online` (the
  presence, wired in `app.go`) says which AIs are online. sqlc added: `ListAgentsByOwner`,
  `ListContactUsers`, `CountConnectorPeople`, `GetConnectorContactSortName`, `ListConnectorContacts`.
  DTOs in `dto/contacts.go` (`Contacts`, `AgentContactsTree`, `MachineTree`, `TreeNode`, `AIContact`,
  `PeopleGroup`, `Machine`, `SetMachineLabelRequest`, the paging constants).
- Tests (`internal/api/contacts_test.go`, `people_test.go`): the whole contacts of a user with two machines
  (tree shape, online, apps, last seen, unlinked, shared with a public AI, users, people), each user's own
  data and the agent/anonymous refusals, disabled users and emptied machines, machine and unlinked order,
  the machine label (trim, 64 boundary, reset, 404s, same hostname for two owners, refusals), the agent
  tree, people paging (page sizes 1-8 walk the same list, exact-end `has_more`, search by name, handle and
  account id, wildcard characters), the default and largest page, the people refusals field by field, the
  linked-account exclusion (link, second user, disabled user, unlink) and the linked accounts as apps.

## T29 - Connector operators and account labels (ACC-01, ACC-07)

- **Operator** (`connectors.operator_iid`, NULL = the owner): every connector view (`GET /connectors`,
  `GET /connectors/{id}`, create/update answers) carries `dto.Connector.operator` (`IntelligenceRef`, role
  `user` for the owner, `ai` for an AI) and `account_label`. Both are optional in the TypeScript type
  (`operator?`, `account_label?`: pointer / `omitempty`) because the web fixtures predate them; the server
  always sets `operator` and omits an empty label. `connectors.View.Operator`/`OperatorAI` is the data,
  filled in one query for lists.
- **`PATCH /connectors/{id}` `operator_id`** (`UpdateConnectorRequest.OperatorID`, `UpdateInput.OperatorID`):
  absent leaves it; `""` or the owner's own user id sets the owner (NULL); an agent id sets that AI **when
  the owner owns it**. Anything else - an AI of another owner (also one only shared with the owner), another
  user, a person, an unknown id - is **422 `invalid`** with field `operator_id` ("must be you or an AI you
  own"), before anything is written, so a request that also renames the connector changes nothing. Rights
  are those of the whole PATCH: manage = the owner; a `use` grantee gets 403, a stranger or an admin 404,
  an agent token 403, anonymous 401. A change of the effective operator (the owner's id when NULL) writes
  `audit.ActionConnectorOperator` = **`connector.operator`** in the same transaction with detail `{kind,
  name, from, to}` (`from`/`to` are intelligence ids, the owner's own id for the owner); setting what is
  already set, or the owner's id for the owner, changes and audits nothing. The generic `connector.update`
  entry is not written for an operator-only request, and the connector is not restarted. Deleting the
  operator AI sets the column NULL (foreign key) so the owner operates again; no event is published.
- **Account label** (`Handler.Account(label string)`, new on `connectors.Handler`; `connectortest.Recorder`
  has `NextAccount`/`NoAccount`, `connectortest.Instance.Account(label)` reports one from a test):
  the manager stores it in `connectors.account_label` when the reporting run is still the current one,
  trimmed, without control characters and cut to `connectors.MaxAccountLabel` = 100 code points; an
  empty label clears it. Adapters call it once connected, before they report `connected`:
  - Telegram: `@<username>` of `getMe` (nothing for a bot without a username);
  - Discord: `@<username>` of the gateway's READY event (`onReady`; reported again after a reconnect, nothing
    when the event names no bot);
  - WhatsApp: the masked phone number (`+•••• 1234`, `maskNumber`) of each instance the connector uses
    (the allowlist, else those with WebSocket events), from the `jid` evolution-go 0.7.2 reports
    (`<number>:<device>@s.whatsapp.net`, device suffix removed), each number once, joined by `, ` in the
    order of `/instance/all`; a jid that is no phone number (a LID, none) adds nothing, so an older
    evolution-go reports an empty label;
  - the webhook reports none.
  No live frame carries the label: clients read it with the connector.
- **Apps of the operator (ACC-07)**: `IntelligenceProfile.apps` of a **user** (after the web app and the
  verified chat accounts) and of an **AI** (after its harness) ends with one `{kind: <connector kind>,
  label: "<Platform>: <account label>", connector_id}` for each connector it operates; the connector's
  name replaces the label until the platform names the account (`Fake: wa`, `Discord: @bot`). A user
  operates the connectors they own that no AI operates. Only the viewers who own or may use the connector
  see its app (`Profile.Operated`, `Service.operatedConnectors`): a user who can use the AI but not the
  connector, and anyone who sees a user's profile, do not learn that the connector exists. Moving the
  operator moves the app.
- sqlc added: `ListConnectorsOperatedBy`, `ListConnectorsOperatedByOwner`.
- Tests: `internal/api/connector_operator_test.go` (default operator, set/reset/repeat with the audit
  entries, every refusal with its status and nothing changed, deleting the AI, the account label cleaned
  and cut, the apps by viewer), and per adapter `TestRunReportsTheBotAccount` (telegram),
  `TestReadyReportsTheBotAccount` (discord) and `TestRunReportsTheMaskedNumbersOfItsInstances` (whatsapp,
  fixtures in the evolution-go 0.7.2 shape with `jid`).

## T30 - Platform DMs reach the operator (ACC-02, ACC-06, DM-05)

- **Per-chat conversations mode** = `auto_route_scope` other than `off` **and no catch-all room**
  (`auto_route_room_id` empty). A connector with a catch-all room keeps the v0.3 behaviour whatever its
  operator is (spec issue 2). The owner switches by clearing the room (`PATCH {auto_route_room_id: ""}`);
  the scope is kept. **Deviation from ROUTE-04's old rule:** "a scope other than off needs a room" (422
  `auto_route_room_id`) is gone, because a cleared room with a scope on is how per-chat mode is chosen;
  `TestCatchAllSettings` asserts the new rule (clearing keeps the scope and answers 200). The scope is
  still validated, the room still needs manage.
- **Deviation (ROUTE-03, deleted catch-all room):** with an empty room now meaning per-chat mode, a room
  that is deleted must not silently turn its connectors into per-chat ones. `rooms.Service.Delete` therefore
  stops the connectors whose catch-all it is, in its own transaction (`StopCatchAllConnectors`): scope `off`
  and `last_error` = `auto-route room unavailable` (`dto.AutoRouteUnavailable`; `dto.AutoRouteLimited` is
  the other detail, `connectors.AutoRouteUnavailable`/`Limited` alias them), and publishes
  `connector.status` with that detail. v0.3 reported it lazily at the next unrouted message; it is now
  immediate, and the scope goes off with it. Losing manage on the room is unchanged (reported at the
  next message). The no-room branch of the catch-all path no longer sets a detail: it only records the chat.
- **DM chats** (`Chat.Kind` dm, scope `dms` or `all`, no catch-all room), `Pipeline.route` → `platformDM`:
  the message goes to the DM between its author and the connector's operator
  (`conversations.Service.EnsurePlatformDM`, `PlatformDM{OwnerID, ChatID, Author, Operator}`).
  - *Which chats*: those in no room, or only in DM rooms (the routing keeps them); a chat the owner put
    in any other room stays there and gets no DM (ROUTE-02's spirit).
  - *Author*: the user the sender is verified as (AD-008), else its person (recorded by `People.Seen`
    only once the message is routed). *Operator*: the connector's `operator_iid` (an `agent` member), else
    the owner (a `user` member). The sender **is** the operator (the owner's own verified account writing
    to the owner-operated account) → no DM with oneself: the chat is recorded, nothing is stored and no
    person is created. A sender without any id has nobody to make a DM with: not routed.
  - *The DM*: one per pair of intelligences (`rooms_dm_pair`), `owner_id` = the connector's owner,
    members: the operator, the author when it is a user (not a person: it takes part through the chat) and
    the chat with **outbound on**. The pair is shared with the web: a user who writes from their phone to
    the AI that operates the account joins the DM they already have with it on the web (their chat is added
    to that room), and the AI's replies in that DM then reach the phone too. Access is the DM rules of T19:
    a person-AI DM is read-only for the AI's owner; with the owner as operator the owner posts in it (the
    post goes out through the chat). Created rooms publish `room.changed` and `room.members`.
  - *Re-homing*: when the operator changed, the chat's next message makes `EnsurePlatformDM` take the chat
    out of its other DMs (cancelling what was queued for it there, ROOM-01; the history stays, no event
    but `room.members`) and into the DM with the current operator, reusing an existing one; deleting the
    operator AI deletes its DMs, so the next message makes the DM with the owner (spec edge case).
  - *Cap*: a route that makes the chat a member of a DM (new DM, joined DM, move) counts for the 60 per
    hour per connector of the catch-all (`routeState`), serialized per connector; at the cap the message
    is not stored, the connector's detail is `auto-route limit reached` (cleared by the next successful
    route), a **new** chat is not recorded and a known one is. A chat already in the right DM is
    never counted nor blocked (`placed`).
  - *Concurrency*: first messages of one new chat at once create one DM and lose no message; the chat's
    rooms and their kinds are read in one query (`ListChatRooms`), because two reads could disagree about
    a DM made in between.
  - sqlc: `ListChatRooms`, `ListChatDMRoomIDs`, `ListRoomChatConnectors`, `StopCatchAllConnectors`.
    `conversations.Service.PlatformDMRoom(authorID, operatorID)` finds the pair's DM; `People.Find` looks a
    sender's person up without recording it. `PipelineDeps.Conversations` (wired in `app.go`) enables it.
- **DM-05**: `conversations.OpenDM` with a person target (users and AIs): after the visibility check
  (404 `contact_not_found`) it returns the existing DM between the caller and the person **only when a
  connector whose chat is in that DM names the caller as operator now** (the owner counts when no AI is
  named), with `created` false; otherwise 422 `no_route_to_person`. It never creates a DM with a person, so
  nobody is written to who did not write first. `send_message to:` (`POST /agent/dms`) and
  `POST /conversations/dm` use it; the message posted in that DM is queued for the chat. After the operator
  changes the old operator has no route, the new one has.
- Tests (`internal/connectors/platform_dm_test.go`, `internal/api/catch_all_settings_test.go`): a DM to
  the operator AI (frame, room, members, the owner's view, stranger 404, more messages), the concurrent
  burst, scopes and hand-routed chats, a connector with a catch-all room, following the operator
  (owner, back to the AI, deleted AI) with the cancelled outbox, no DM with oneself and the linked owner
  trusted by her AI, the 60-per-hour cap, DM-05 for the operator AI, another AI, another owner's AI with
  and without connector access, a never-routed person, strangers and the owner before/after the change;
  clearing the room keeps the scope; deleting the catch-all room stops routing at once with a live
  status.

## T31 - New group chats become groups (ACC-06)

- **Group and channel chats** of a connector in per-chat mode (scope `groups` or `all`, no catch-all
  room, see T30) that belong to no room get **a group of their own** on their first message
  (`Pipeline.autoGroup`, `route`): `rooms.CreateWithMembers(owner = the connector's owner, name =
  roomName(chat, kind))` (the chat title cut to 100 characters, else "<Platform> chat"), broadcast on,
  chat bridging off, 90 days of retention, no instructions, with the **operator** as a member (the owner's
  AI as an `agent` member with delivery `all`, or the owner as a `user` member) and the **chat** with
  outbound on. The operator therefore receives the chat's messages (trusted only when the sender is
  verified as the AI's owner) and an AI's reply is queued for the chat (broadcast); the owner manages the
  group by ownership. Members are checked with the owner's add-member rules first; if the owner can no
  longer add one of them the connector reports `auto-route room unavailable` and the message is not
  stored. `room.changed`/`room.members` are published by `CreateWithMembers`.
- A chat that already belongs to **any** room is left as it is (the owner routed it, or it has its
  group). The operator is a member of the group **as of its creation**: changing the operator later does
  not edit existing groups (DMs follow the operator, T30; groups are the owner's to manage).
- **Cap and serialization**: groups and DMs share the connector's 60 new routes per hour
  (`routeState`); at the cap the message is not stored, the detail is `auto-route limit reached`, a new
  chat is not recorded, a known one is, and the next successful route clears the detail. The connector's
  routes are serialized and the chat and its rooms are read again under the lock (`ListChatRooms`), so
  concurrent first messages make one group. The `routeHook` test seam runs before the chat is recorded.
- **Waiting after a removal (ROUTE-02's edge case, extended)**: a chat taken out of a room of a
  per-chat connector (`rooms.skipAutoRoute`: the catch-all room as before, or any room when the connector
  has a scope on and no catch-all room) is not given a group for 24 hours (`auto_route_skips`); a manager
  may add it back at once. Without it the next message would have made another group immediately. DMs
  cannot lose their chat (members are fixed) and re-homing deletes the member row directly, so they are
  never skipped. `GetChatAutoRoute` also returns the scope.
- Code layout: the whole automatic routing (catch-all, per-chat DMs and groups, the status details and
  the caps) now lives in `internal/connectors/autoroute.go`; `inbound.go` keeps the pipeline and the
  author resolution. Nothing else moved.
- Tests (`internal/connectors/auto_groups_test.go`): a group chat becomes a group (name, owner, members,
  broadcast, the AI's frame and reply), channel chats, owner as operator, untitled and over-long titles,
  scopes dms/off, a chat in a room by hand, a catch-all connector, the concurrent burst, the cap shared
  with DMs (30 + 30), and the 24-hour wait after a removal with a manager adding the chat back.

## T32 - Operator signatures (ACC-03, ACC-04)

- `Sender.Send` marks a message **`Outbound.Unsigned`** when its author intelligence (`messages.author_iid`)
  is the chat's connector's operator (`operator_iid`, else the owner): an AI that operates the account
  writes through its own account, and so does the owner from the web when no AI operates it. Every other
  author is named as before: an AI per its member setting (`sign_outbound`, the name set for it in the
  room: MEM-01, MEM-02), a user who is not the operator, and bridged chat messages (their author is the
  person, or the linked user). The operator rule wins over `sign_outbound`: an operator AI with its
  signature on is not signed, and as soon as it stops being the operator it signs again.
- **`connectors.Outbound.Signature()`** is the author line to put in the text: `AuthorName`, or "" when
  `Unsigned`. The Telegram, Discord and WhatsApp adapters build their text with it. **`AuthorName` still
  names the author**, because the webhook carries it as data in its payload (`message.author_name`, HOOK-03)
  and its signed requests would otherwise lose who wrote an operator's message; reactions keep it too. A
  member with its signature off still has an empty `AuthorName` (MEM-01, unchanged), so a webhook payload
  of such an agent has none, as before.
- **Visible change:** the owner's own posts from the web, which used to reach her chats as "alice: text",
  now go out as plain text from her account unless an AI is the operator; every other poster is still
  named. `TestOutboundAuthorFollowsMemberSettings` keeps its `AuthorName` assertion for her post and
  adds that its signature is empty.
- Tests (`internal/connectors/operator_signature_test.go`, adapter tests): an operator AI unsigned with
  the signature on and a name set, signed again once not the operator; an AI through the owner's account
  signs on, with its room name, off; a user operator unsigned, a poster named, the owner named while an
  AI operates; and each adapter leaving the line out.

## T33 - Messages from the operator's own account (ACC-05)

- **`Inbound.FromSelf` is no longer dropped** (it was, by CON-12, for every chat). `Pipeline.handle` hands
  such a message to `handleSelf` (`internal/connectors/selfmsg.go`). It **creates nothing**: no chat
  (`UpsertChat` is not called), no conversation, no person, no route; a chat that is unknown to the
  connector, or sits in no room, drops it. In a chat that is a member of rooms it is stored once per room
  (`Pipeline.post`, the loop `handle` already had, now shared; a repeat is stored once and downloads no media
  again), with its replied message, text and media, **authored by the connector's operator**:
  `author_kind chat`, `author_id` the chat, `author_iid` the operator (`operator_iid`, else the owner, read
  from the connector when the message is handled, so a change applies at once), `author_user_id` the
  connector's **owner**. The owner's own account is the owner's, so the owner's AIs get it **trusted**
  whoever the operator is (AD-006); AIs of other users do not. `sender_id`, `sender_alt_id` and
  `sender_name` stay what the platform gave (the account), which replies need for quoting.
- **Rendering:** a chat message authored by an AI (`author_iid` of kind `ai`, i.e. an AI operating the
  account) now has `author.role = ai` (`convert.messageAuthor`); before, a chat message was only a `user`
  (linked sender) or a `person`. The timeline name is the operator's display name (the message view takes
  it from `author_iid`).
- **Its own echoes are dropped.** Partyline's sends come back from WhatsApp and Discord as messages of the
  account, three layers decide that one is an echo and not typed by the operator:
  1. **Hold:** the message waits while the outbox has a `sending` row for its chat (`CountSendingOutboxForChat`),
     polling every 50 ms, **at most `connectors.SelfHold` = 10 s** on the pipeline's clock; the platform can
     report a message before the call that created it returns, when no id is recorded yet. After the limit
     the message is handled as it is: an echo of a send stuck for more than 10 s is stored as typed by the
     operator (accepted: the alternative is to lose the operator's real messages behind a stuck send).
  2. **Sent ids** (`connectors.SentIDs`, in memory, 5 minutes, per chat): every adapter tells
     **`Outbound.Created(id)`** the id of each message a send created, text parts and files, also when the
     send fails afterwards (the parts that exist will echo, and a retry sends them again). `Sender` records
     them. The outbox keeps the first id only.
  3. **Outbox id** (`CountChatOutboxExternalID`): the id of a `sent` row of that chat. It survives a restart
     and the five minutes. It reads the `sent` rows through the `outbox_due` prefix (status) and filters
     them; self messages are rare and the memory registry answers first for the ids it knows, so no index
     was added (none could be, 0016 is reserved).
  An id belongs to its chat: the same id in another chat is another message.
- **A pairing command from the account is ignored** (not executed, not stored): `!link CODE` typed from the
  account's own phone would link the account and keep the code in a room's history. The code stays
  usable.
- **Delivery is the usual one:** the message is a chat message of the room, so it is bridged to the room's
  other outbound chats like any other (signed per the operator rule of T32 for the target chat), delivered
  to the room's AIs per their delivery mode, and an AI that is the operator and a member of the room
  receives it as a message of its account (delivery excludes only the origin chat and an agent's own posts).
- **Adapters:** Discord keeps the bot's user id from READY and emits the bot's own messages with
  `FromSelf` (a DM's title is not set from it); other bots stay ignored, and so does any bot message
  before READY. WhatsApp already set `FromSelf` and now reports every created id. Telegram bots never
  receive their own messages and are unchanged. The webhook kind has no account of its own.
- **CON-12 is refined, not repealed:** "drop messages sent by the connector's own account" still holds for
  chats in no room and for echoes. `TestInboundDropsOwnMessages` is now `TestInboundDropsOwnMessagesOfChatsInNoRoom`
  and no longer asserts that a routed chat drops a phone-typed message (ACC-05 says it is stored).
- **Test seams:** `PipelineDeps.Sent` and `.Wait`, `app.Options.InboundWait` / `testkit.WithInboundWait`
  (the sleep of the hold, to advance a manual clock), and on the fake connector `HoldSends` /
  `ReleaseSends` / `WaitSending` (a platform call that is still running), `SetSendParts` (a send that
  creates several messages) and `QuietSends` (a connector that reports no ids).
- Tests (`internal/connectors/selfmsg_test.go`, `discord` and `whatsapp` adapter tests): operator owner and
  operator AI as author, trusted, reply kept, stored once; media; echo dropped after the send, from the
  outbox alone, in its own chat only, before the send returned (held, then dropped), for every part;
  a typed message during a send stored after it; the 10 s limit with a stuck send on a manual clock;
  no hold without a send; nothing created in unknown or unrouted chats; a pairing command ignored; the
  registry's memory and pruning; Discord own/other bot/early messages; reported ids with a failure midway.

## T34 - Notify me on another app (NOTI-01, NOTI-02, NOTI-03)

- **`GET /api/v1/me/notify-options`** (sessions and user tokens; an agent token is 403, anonymous 401) →
  `dto.List[dto.NotifyOption]{chat_id, title, connector: ConnectorRef, account}`, always an array: the **DM
  chats** (`chats.kind = dm`) of the **accounts the caller linked** (an `identities` row of theirs on the
  chat's connector matching the chat's person, `chats.person_id`, by external id or alternative id like
  `ResolveIdentityUser`), on the connectors the caller **owns or may `use`** (a grant to them or to everyone).
  `account` is the identity's display, else its external id. A group or channel chat, an account nobody
  linked, an account linked through another connector (AD-008) and another user's account are no options.
  Ordered by connector name, chat title, id. `conversations.Service.NotifyOptions(ctx, userID)`; sqlc
  `ListNotifyOptions`.
- **`PUT /api/v1/me/notify`** `dto.SetNotifyRequest{chat_id}` (`string | null`) → 200 `dto.Me`.
  `chat_id` must be the id of one of the caller's options; null (or absent) clears. Any other value (an
  unknown id, `""`, a chat of a group, of a DM nobody linked, of another user, a room id) is **422
  `invalid`** with the field `chat_id` and changes nothing; choosing the chat already chosen is a no-op 200.
  The check and the write are one transaction (`Service.SetNotify`). `dto.Me.notify_chat_id` (optional, absent
  when none) is set by `GET/PATCH /me` and the answer of `PUT /me/notify`; it is the stored choice, whether
  or not the chat is still an option (the list is the authority).
- **The copy** (`rooms.Service.insert` → `notifyChat`, same transaction as the message): an **AI's text post
  in a DM whose other participant is an enabled user with `notify_chat_id` set** queues one outbox row
  `purpose notify` (`CreateNotifyOutbox`) for that chat. No copy for: a user's own posts, another user's
  (user-to-user DMs included), system messages, **reactions**, groups, DMs with another AI or a person, a
  disabled user, nothing chosen. **A chat that already receives the message as an outbound member of the
  room is not sent it twice** (a user who writes to the AI that operates the account from that very phone
  has the chat in the DM): the ordinary delivery stands and no copy is queued. A DM to a disabled user is
  refused before (`counterpart_disabled`).
- **Worker**: `ListDueOutbox` returns `purpose` and no longer filters notify rows by room membership (the
  user's app is no member); `delivery.Delivery.Purpose` (`dto.OutboxPurpose`, `room` or `notify`) tells the
  sender. Retries, backoff (2, 4, 8, 16 s), `failed` after 5 attempts, `outbox.status` frames and the
  startup requeue are the ones of every outbox row. **`delivery.Permanent(err)`** (new, `IsPermanent`) marks
  an error retrying cannot fix: the worker marks the entry `failed` at once (attempts = that attempt) with
  the error's text. `DeletePendingRoomChatOutbox` (ROOM-01: a chat leaving a room cancels what waits for it)
  now deletes only `purpose = room` rows: a notify copy does not depend on membership.
- **Sender** (`connectors.Sender.Send` → `sendNotify`): text = `"<AI display name>: <text>"` (the name the
  timeline shows, the profile's in a DM), a blank text gives `"<AI display name>:"`, then **one line per
  stored attachment** `<public URL>/api/v1/attachments/<id>` (`connectors.AttachmentLinkPath`; the link opens
  for the user once logged in; no files are uploaded); no author line (`AuthorName` empty), no quote.
  `NewSender` takes the instance's public URL (`cfg.PublicURL`). Reported ids go to `SentIDs` like any send,
  so the echo of a copy is not mistaken for a message typed on the account (ACC-05). **Usability check at
  send time** (`Sender.notifyTarget`): the room is a DM, the user exists and is enabled, **still has this
  chat as `notify_chat_id`** and the chat **still is one of their options** (`conversations.NotifyUsable`);
  otherwise the send fails **permanently** with `notify target no longer available` before touching the
  connector (the user cleared or changed the choice, unlinked the account, lost the connector, was
  disabled). A connector that is down (`connector is not running`) or a platform error is retried (NOTI-03).
- **`dto.OutboxItem.purpose`** (`notify`; omitted for ordinary deliveries, `dto.OutboxPurpose`) is in
  `Message.outbox` and in the live `outbox.status` frame, so everyone who can view the DM (the AI's owner and
  `use` grantees included) sees the copy's delivery and its failure; the chat id is opaque to them (the chat
  is no member of the room). Web types regenerated.
- Tests: `internal/connectors/notify_test.go` (the options by caller / group / unlinked / second connector /
  alternative ids / connector access, choosing and its 422s and null, the authorization of both routes, the
  copy with name prefix / no signature / live frame / rename, what is not copied, attachment links, no
  duplicate for a member chat, the retry schedule to `failed` after 5, a platform error then success, a
  target taken away by clearing / another choice / unlinking / losing the connector / being disabled, ROOM-01
  leaving notify rows alone) and `internal/delivery/outbox_test.go` (notify rows outside the room, permanent
  errors).

