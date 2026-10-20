# Contract Notes (from core-platform Phase 1)

Binding conventions every later task and feature follows. Source of truth for types: `internal/api/dto` → `web/src/lib/api/types.gen.ts` (AD-003).

## CLI and build
- `cli.Run(args, stdout, stderr) int`; version via `-X github.com/wnexous/partyline/internal/cli.Version=`.
- Makefile tools from PATH via `GOLANGCI_LINT`, `SQLC`, `TYGO`, `BUN`; versions listed in `AGENTS.md`.
- Drift check compares against the git index: stage regenerated files before the gate.
- Web steps run when `web/package.json` exists and need scripts `test`, `lint`, `typecheck`, `build`.

## Store
- `store.Open(path)`, `Migrate`, `R()`, `WithTx`, `Close`. `0001_init.sql` has every table.
- Columns `NOT NULL DEFAULT ''` unless a null is meaningful. Nullable: `messages.external_id`, `disabled_at`, `last_used_at`, `expires_at`, `revoked_at`, `used_at`, agents/chats `last_seen_at`. Flags are `BOOLEAN` (Go `bool`).
- CHECKs: grants `resource_kind` room|agent|connector, `level` view|post|manage|use; outbox `status` pending|sending|sent|failed; attachment `status` default `stored`.

## Config and secrets
- `config.Load(getenv) (*Config, error)`: `PublicURL *url.URL`, `MaxUploadBytes`, `TrustedProxies []netip.Prefix` (bare IP accepted as /32 or /128).
- Key file `<data>/secret.key` holds base64 text; sealed format = nonce || ciphertext.

## Events
- `events.Bus` events carry ids only; consumers fetch what they need.

## DTO / HTTP contract
- JSON snake_case; times RFC 3339 strings.
- Collections `List[T]{items}`; paginated `Page[T]{items, has_more}` (`before_seq`/`after_seq` for messages, `before=<id>` for audit).
- Error codes: `invalid`, `unauthorized`, `forbidden`, `not_found`, `conflict`, `gone` (410 invite reuse), `too_large`, `rate_limited`, `internal`, `unavailable`.
- Multipart message post: part `message` (JSON `PostMessageRequest`) + one or more `file` parts.
- Live frames `{type, data}`; types: `hello`, `room.message`, `room.changed`, `room.deleted`, `room.members`, `access.changed`, `agent.presence`, `connector.status`, `chat.discovered`, `outbox.status`, `identity.linked`, `lagged`. TS has a `LiveEvent` discriminated union.
- Agent WS frames: `AgentWelcomeFrame`, `AgentMessageFrame{room, trusted, message}`, `AgentNoticeFrame`, `AgentAckFrame`. Agent history returns `Page[AgentMessage]`.
- `Message` includes `author_name`, `author_user_id`, `attachments`, `outbox[]`; `Room` and `Agent` include `my_level`.
- `ConnectorField` has `type` and `secret` bool; `SecretMask = "********"`.
- Header constants in dto: `X-Partyline-Agent`, `X-Partyline-Signature`, `X-Partyline-Timestamp`.

## tygo
- `enum_style: union` (Go const groups → TS consts + union type).
- Map fields need `tstype:"{ [key: string]: unknown }"`; a comma inside a `tstype` tag breaks output.
- `make check` does not type-check `types.gen.ts` until the web project exists (web `typecheck` covers it afterwards).

# Contract Notes (from core-platform Phase 2: accounts and tokens)

## IDs and time
- `store.NewID()` returns a UUIDv7 string; every service uses it.
- Services take an injectable clock (`Options.Now`); `app.Options.Now` and `testkit.WithClock` pass it down.

## users service (`internal/users`)
- Sentinel errors: `ErrSetupDone` 409, `ErrBadSetupToken` 401, `ErrInvalidCredentials` 401, `ErrUnauthenticated` 401,
  `ErrRateLimited` 429, `ErrNotFound` 404, `ErrInviteGone` 410, `ErrUsernameTaken` 409; `*ValidationError{Fields}` 422.
  `api.mapError` translates them; add new service errors there.
- Methods return sqlc rows (`db.User`, `db.ApiToken`, ...); handlers convert to DTOs (`toMe`, `toUser`, `toToken`, ...).
- `CreateUser(ctx, AccountInput, role)` creates accounts directly (CLI `admin create-user`, testkit).
- Disabling a user deletes its sessions; `ValidateSession`/`AuthenticateToken` reject disabled users.

## HTTP (`internal/api`)
- `api.New(Deps)` builds the mux and wraps it in `Middleware` (outermost first): recover → request id (`X-Request-Id`)
  → security headers (default CSP `default-src 'none'; frame-ancestors 'none'`, HTML handlers override it)
  → client IP → body limit → principal → agent-token restriction → CSRF.
- Principal: `authn.PrincipalFrom(ctx)`. Bearer → token principal (agent tokens get `AgentName` from `X-Partyline-Agent`,
  `AgentID` is filled later by the agent registry); bad bearer → 401 at once. Bad cookie → anonymous.
- Agent tokens reach only `/api/v1/agent/...` (403 elsewhere, including `/healthz`).
- CSRF: unsafe non-bearer requests need no foreign `Sec-Fetch-Site`/`Origin`; cookie-authenticated ones must send one of them.
- Request id and client IP: `httpx.RequestID(ctx)`, `httpx.ClientIP(ctx)`.
- Handlers: `s.handle(mux, "METHOD /api/v1/...", h)` with `h` an `apiFunc` returning `error`; wrap with
  `authed(...)` (user principal, else 401) or `adminOnly(...)` (403 for members). Register a feature's routes from `server.routes`.
- Session cookie `pl_session` (`api.SessionCookie`): Path `/`, HttpOnly, SameSite=Lax, Max-Age 30 d, Secure when the public URL is https.
  Setup and invite acceptance also log the new user in.
- Invite links: `<public URL>/invite/<token>`. `/users` lists active accounts only.

## app and testkit
- `app.New(cfg, Options)` → `Start(ctx)` migrates and flips `/readyz`; `Shutdown(ctx)`; `SetupToken()`; fields `Store`, `Bus`, `Users`, `Box`.
- `testkit.New(t, opts...)` → `Env{App, Server, URL, Origin}`; `Anon()`, `SetupAdmin()` (user `admin`), `NewUser(name)`,
  `NewAdmin(name)`, `LoginAs(name, pw)`, `SessionClient(secret)`; all accounts use `testkit.Password`.
- `Client`: `Get/Post/Put/Patch/Delete/Do/DoRaw`, `Token(kind, name)`, `WithBearer(secret)`, `WithHeader(k, v)`; fields `User`, `Session`.
  Requests send `Origin: Env.Origin`.
- `Response`: `RequireStatus`, `RequireError(status, code) dto.Error`, `Decode`, `Cookie(name)`; generic `testkit.Decode[T](resp)`.
- e2e tests importing testkit live in `package api_test` (in-package tests would import-cycle).

# Contract Notes (from core-platform Phase 3: rooms, access, messages)

## Sessions (AUTH-03 fix)
- Sessions slide at most once per hour (`users.SessionSlideInterval`); `users.CheckSession(ctx, secret) (Principal, renewed, error)`
  reports the slide and the principal middleware re-issues `pl_session` with a fresh 30-day Max-Age. `ValidateSession` wraps it.
- `api.Authenticator` now requires `CheckSession`. `testkit.NewClock(t0)` + `testkit.WithClock(clock.Now)` drive time in e2e tests.

## Validation
- `validation.Error{Fields}` (`internal/validation`) is the one 422 error; `users.ValidationError` is an alias.
  Build it with `validation.Check{}.Add(field, reason)` then `.Err()`.

## access (`internal/access`)
- `access.New(store, bus, Options{Now})` → `*Authorizer`. `Level = dto.Level`, `access.None = ""`, `access.Everyone = "*"`,
  `AtLeast(have, want)` (view < post < manage; use < manage).
- `RoomLevel(ctx, p, roomID)`: users get max(ownership=manage, own grant, `*` grant); agent principals get post when
  `p.AgentID` is a member, else None; disabled/unknown users and missing resources resolve to None. Admins get nothing implicit.
- `UserRoomLevel`, `AgentRoomLevel`, `GrantedRoomLevels(userID)`, `AgentLevel`, `ConnectorLevel` (owner = manage, grantee = use),
  `CanUseAgent`, `CanUseConnector`.
- `Sharing(ctx, kind, id) Visibility` and `SetVisibility(ctx, actorID, kind, id, ownerID, Visibility)`: replaces grants in one
  tx, skips the owner, validates levels per kind (rooms: shared view|post|manage, public view|post; agents/connectors: use,
  defaulted when empty), publishes `GrantsChanged`. Callers authorize the actor first (rooms: manage, not owner-only).
- Errors: `access.ErrNotFound` (404, no view) and `access.ErrForbidden` (403, view but not enough); mapped in `api.mapError`.

## delivery rules (`internal/delivery/rules.go`, pure)
- `Plan(RoomState{BridgeChats}, []Member{Kind, ID, Outbound}, Draft{AuthorKind, AuthorID, Recipients}) Targets{AgentIDs, ChatIDs}`.
  Never the author; every other agent; agent/user posts → explicit recipients only, else chats with outbound on; chat posts →
  every other chat with outbound on when bridging (DLV-05), else none; system posts → agents only.
  Plan does not look at the pause flag: the agent dispatcher must skip rooms with `agents_paused`.
- `Trusted(authorUserID, recipientOwnerID)`; `LoopGuard{Limit}.Next(LoopState{Streak, Paused}, authorKind) (state, pausedNow)`,
  `DefaultLoopLimit = 25`; user/chat messages reset the streak without unpausing; system messages change nothing.

## rooms (`internal/rooms`)
- `rooms.New(Deps{Store, Access, Bus, Now, LoopGuard})`. `Require(ctx, p, roomID, level)` authorizes up front.
- Rooms: `Create`, `Get`, `List` (user principals), `Update`, `Delete`, `Sharing`, `SetSharing`, `Resume`; `Room` embeds `db.Room`
  plus `Level`, `Visibility`, owner names, `LastSeq()`. Delete removes grants and cascades members, messages, attachments,
  cursors, outbox; blob files are left to the retention orphan sweep (T24).
- Members: `Members`, `AddMember(MemberInput{Kind, MemberID, Outbound *bool})` (agent → use on agent, chat → use on its
  connector, else ErrForbidden; user → must exist; duplicate → `rooms.ErrAlreadyMember` 409), `UpdateMember`, `RemoveMember`;
  member ids are `room_members.id` scoped to the room.
- Messages: `Post(ctx, p, roomID, PostInput{Text, ReplyTo, Recipients, Files})` (users need post, agents membership; agent
  author_user_id = owner), `PostFromChat(ctx, roomID, ChatMessage{ChatID, Sender*, SenderUserID, ExternalID, Kind, ...})`
  (chat must be a member, else ErrNotFound; dedupe returns the stored message without an event), `Messages(ctx, p, roomID,
  Page{BeforeSeq, AfterSeq, Limit})` → (items, hasMore), `GetMessage`, `Attachment(ctx, p, id)`.
- `Recipients` are chat ids (`RoomMember.member_id` of chat members); others → 422 `recipients`. `reply_to` must be a message of
  the room. `File{SHA256, Name, Mime, Size, Status}` refers to content already in the blob store.
- One write tx per post: dedupe → `NextRoomSeq` → message → attachments → outbox rows (pending, next_attempt_at = now) →
  loop guard; on pause a system message (`PauseNotice`) follows. After commit: `MessageCreated{RoomID, MessageID, Seq}` per
  stored message, plus `RoomChanged` when it paused. `Message` embeds `db.MessageView` (migration 0002 view with `author_name`:
  display name/username, agent name, sender name or chat title, "Partyline" for system).
- Paging: no cursor or `before_seq` → newest first; `after_seq` → oldest first.

## attachments (`internal/attachments`)
- `attachments.Open(dir)`; `Put(r, limit) (sha, size, err)` (`ErrTooLarge`), `Open(sha)` (`ErrNotFound`), `Delete(sha)`.
  The app keeps blobs in `<data>/blobs` (`app.BlobsDir`); `App.Access`, `App.Rooms`, `App.Blobs` are wired.

## HTTP
- `api.Deps` has `Rooms` and `Blobs`. Multipart posts check post before storing files; each file is limited to the upload limit.
- Downloads: `Content-Disposition: inline` + real type only for png/jpeg/gif/webp, otherwise `attachment` with
  `application/octet-stream`; invisible attachments are 404.
- Authorization matrix (`internal/api/authmatrix_test.go`): `newMatrix(t)` (clients owner, viewer, poster, stranger, admin),
  `m.as(t, role)` binds a client to a subtest, `statuses(owner, viewer, poster, stranger, admin)`, and
  `m.run(t, setup, []matrixRoute{{name, call, want}})` runs each route as each role on a fresh resource from `setup`.
  For agents/connectors use viewer as the use grantee. Test agents/chats are inserted with `CreateAgent`/`CreateConnector`/`CreateChat`.
