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
