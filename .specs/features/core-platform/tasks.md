# Core Platform Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Design**: `.specs/features/core-platform/design.md`
**Status**: Approved

---

## Test Coverage Matrix

> Generated from project guidelines and spec. Guidelines found: none (new repo) - strong defaults applied; test types chosen by the agent under the user's delegation.

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| Pure domain (delivery rules, trust, loop guard, crypto, tokens, config) | unit | All branches; 1:1 to spec ACs; every listed edge case | `internal/<pkg>/*_test.go` | `go test -count=1 ./internal/<pkg>/...` |
| Services over SQLite (users, access, rooms, outbox, audit, retention) | integration | Key paths + every AC + error paths, real SQLite in `t.TempDir()` | `internal/<pkg>/*_test.go` | `go test -race -count=1 ./internal/<pkg>/...` |
| HTTP handlers, middleware, WS | e2e (httptest via `internal/testkit`) | Every route: happy + each listed edge + error paths; auth matrix owner/grantee/stranger/admin | `internal/api/*_test.go`, `internal/live/*_test.go` | `go test -race -count=1 ./internal/api/... ./internal/live/...` |
| DTOs, migrations SQL, Docker/CI config | none | build gate only | - | `make check` |

## Gate Check Commands

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick | Tasks with unit tests only | `go test -count=1 ./internal/...` |
| Full | Tasks with integration/e2e tests | `make test` (go test -race ./... + web tests when web/ exists) |
| Build | Last task of a phase; config-only tasks | `make check` (generate drift check, golangci-lint, go vet, all tests, build) |

---

## Execution Plan

### Phase 1: Foundation

```
T1 → T2 → T3 → T4 → T5 → T6 → T7
```

### Phase 2: Accounts and tokens

```
T8 → T9 → T10 → T11 → T12 → T13 → T14
```

### Phase 3: Rooms, access, messages

```
T15 → T16 → T17 → T18 → T19 → T20
```

### Phase 4: Delivery runtime, live, operations

```
T21 → T22 → T23 → T24 → T25 → T26 → T27
```

### Phase 5: Verifier fix iteration 1

```
T28 → T29 → T30 → T31 → T32 → T33 → T34 → T35 → T36 → T37 → T38 → T39 → T40 → T41 → T42
T35 → T43 → T44
```

---

## Task Breakdown

### Phase 1: Foundation

#### T1: Bootstrap module, CLI skeleton and tooling

**What**: Go module `github.com/wnexous/partyline`, `cmd/partyline/main.go` calling `internal/cli.Run` with `version` subcommand, Makefile (`generate`, `lint`, `test`, `check`, `build`), `.golangci.yml`, `.gitignore`, `LICENSE` (MIT, 2026 André Dalnegro / wnexous), `AGENTS.md` for contributors.
**Where**: `internal/cli/cli.go`
**Depends on**: None
**Reuses**: none
**Requirement**: OPS-03 (foundation)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] `go build ./...` and `partyline version` print version from ldflags (default `dev`)
- [x] Unknown subcommand exits 2 with usage
- [x] `make check` runs (golangci-lint installed via `go run` pinned version or documented)
**Tests**: unit
**Gate**: build
**Commit**: `build: bootstrap go module, cli skeleton and tooling`

#### T2: Configuration from environment

**What**: `config.Load(getenv)` with `PARTYLINE_ADDR` (`:8080`), `PARTYLINE_DATA_DIR` (`/data`), `PARTYLINE_PUBLIC_URL` (`http://localhost:8080`), `PARTYLINE_SECRET_KEY`, `PARTYLINE_MAX_UPLOAD_MB` (25), `PARTYLINE_LOG_LEVEL`, `PARTYLINE_LOG_FORMAT`, `PARTYLINE_TRUSTED_PROXIES`; validation errors name the variable.
**Where**: `internal/config/config.go`
**Depends on**: T1
**Reuses**: none
**Requirement**: SEC-03, SEC-04 (inputs)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Defaults applied; invalid URL/number/CIDR returns error naming the variable
- [x] `SecureCookies()` true iff public URL scheme is https
**Tests**: unit
**Gate**: quick
**Commit**: `feat(config): load and validate environment configuration`

#### T3: Store, migrations and sqlc

**What**: `store.Open` (read pool 4, write pool 1, pragmas from design), embedded migration runner (`schema_migrations`), `0001_init.sql` with every table of the design, `sqlc.yaml`, initial queries for users/sessions, generated `internal/store/db`.
**Where**: `internal/store/store.go`
**Depends on**: T2
**Reuses**: design Data Models
**Requirement**: MSG-01 (unique seq), CON-10 (dedupe index)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Migrate is idempotent; foreign keys enforced (insert with bad FK fails)
- [x] `WithTx` rolls back on error
- [x] `make generate` regenerates sqlc code without diff
**Tests**: integration
**Gate**: full
**Commit**: `feat(store): add sqlite store with embedded migrations and sqlc`

#### T4: Secret box and key management

**What**: `secrets.LoadOrCreateKey(cfg)` (env base64 32 bytes or `<data>/secret.key` created 0600), `Box.Seal/Open` AES-256-GCM with random nonce.
**Where**: `internal/secrets/box.go`
**Depends on**: T3
**Reuses**: none
**Requirement**: SEC-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Round trip works; tampered ciphertext fails; wrong key fails
- [x] Generated key file has mode 0600 and is reused on next load
**Tests**: unit
**Gate**: quick
**Commit**: `feat(secrets): add aes-gcm secret box with persisted key`

#### T5: Passwords, tokens and principal

**What**: argon2id hash/verify (PHC string), token generation `pl_<kind>_<base62>`, SHA-256 hashing, prefix (12 chars), constant-time compare, `Principal` type.
**Where**: `internal/authn/authn.go`
**Depends on**: T4
**Reuses**: none
**Requirement**: AUTH-08, TOK-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Hash never contains the password; verify true/false correct; PHC params m=19456,t=2,p=1
- [x] Token format and prefix length asserted; hash is hex sha256
**Tests**: unit
**Gate**: quick
**Commit**: `feat(authn): add argon2id passwords, api tokens and principal`

#### T6: Event bus

**What**: typed `events.Bus` with non-blocking fan-out, per-subscriber buffer, `Lagged` marker on overflow, all event types from design.
**Where**: `internal/events/bus.go`
**Depends on**: T5
**Reuses**: none
**Requirement**: LIVE-01 (transport)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Every subscriber receives published events in order
- [x] Full subscriber gets `Lagged` and publisher never blocks
**Tests**: unit
**Gate**: quick
**Commit**: `feat(events): add in-process event bus`

#### T7: HTTP helpers and full DTO contract

**What**: `httpx` (DecodeJSON with 1 MiB limit → 413, error envelope, `Invalid(fields)` → 422, pagination parsing); `internal/api/dto` with every request/response/event DTO for all four features plus constants `UsernamePattern`, `PasswordMinLength`, `MaxTextLength`; `tygo.yaml` and `make generate` writing `web/src/lib/api/types.gen.ts`.
**Where**: `internal/api/httpx/httpx.go`
**Depends on**: T6
**Reuses**: design endpoint tables of all features
**Requirement**: SEC-03, MSG-02 (constants)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Oversized JSON → 413 envelope `too_large`; malformed JSON → 400 `invalid`
- [x] `make generate` produces TS types with constants; drift check in `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(api): add http helpers and dto contract with ts generation`

### Phase 2: Accounts and tokens

#### T8: Setup, login and sessions service

**What**: `users.Service`: `NeedsSetup`, `Setup(token, input)` (setup token generated at start, held in memory), `Login` (generic 401), sessions create/validate (sliding 30 d)/revoke, login rate limiter 5/min/IP.
**Where**: `internal/users/auth.go`
**Depends on**: T7
**Reuses**: authn (T5), store (T3)
**Requirement**: AUTH-01, AUTH-02, AUTH-03, AUTH-04, AUTH-05, AUTH-09
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Setup only with correct token and zero users; second setup → conflict error
- [x] Unknown user and wrong password return the same error value
- [x] 6th failed login in a minute from one IP → rate-limited error
- [x] Revoked/expired session fails validation
**Tests**: integration
**Gate**: full
**Commit**: `feat(users): add setup, login and session management`

#### T9: Invites and user administration

**What**: create invite (single-use, 7 d), get by token, accept (username/password rules), revoke; admin list users, set role, disable/enable (disabling revokes sessions).
**Where**: `internal/users/invites.go`
**Depends on**: T8
**Reuses**: T8
**Requirement**: AUTH-06, AUTH-07, AUTH-10
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Used, revoked or expired invite → error; accepted invite marked used
- [x] Username outside `^[a-z0-9_-]{3,32}$` or password < 10 → validation error naming field
- [x] Disabled user's sessions and tokens fail validation (token half asserted with the tokens service in T10)
**Tests**: integration
**Gate**: full
**Commit**: `feat(users): add invites and user administration`

#### T10: API tokens service

**What**: create (kind agent|user, name, optional expiry), list (prefix only), revoke, authenticate (updates `last_used_at` at most once per minute).
**Where**: `internal/users/tokens.go`
**Depends on**: T9
**Reuses**: authn
**Requirement**: TOK-01, TOK-02, TOK-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Secret returned once; list never contains it
- [x] Revoked/expired/unknown → unauthorized
- [x] Two authentications within 60 s write `last_used_at` once
**Tests**: integration
**Gate**: full
**Commit**: `feat(users): add personal and agent api tokens`

#### T11: Middleware chain

**What**: recover, request id, security headers, body limit, principal resolution (cookie `pl_session` or `Authorization: Bearer`), CSRF/origin check for cookie writes, agent-token route restriction, client IP with trusted proxies.
**Where**: `internal/api/middleware.go`
**Depends on**: T10
**Reuses**: httpx (T7)
**Requirement**: SEC-01, SEC-02, SEC-03, TOK-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Cookie POST with foreign Origin → 403; bearer POST without Origin → allowed
- [x] Agent token on `/api/v1/rooms` → 403; on `/api/v1/agent/rooms` → passes
- [x] Panic → 500 envelope; headers present on responses
**Tests**: e2e
**Gate**: full
**Commit**: `feat(api): add security, auth and csrf middleware`

#### T12: App wiring, test kit and health

**What**: `app.New(cfg)` wiring store/services/bus/router, `Start/Shutdown`; `/healthz`, `/readyz`; `internal/testkit` booting the app on a temp dir with helpers (`SetupAdmin`, `LoginAs`, `NewUser`, typed JSON client).
**Where**: `internal/app/app.go`
**Depends on**: T11
**Reuses**: all previous
**Requirement**: OPS-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] `/healthz` 200 unauthenticated; `/readyz` 503 before migrations finish, 200 after
- [x] testkit used by a sample test that logs in
**Tests**: e2e
**Gate**: full
**Commit**: `feat(app): wire application and add test kit and health endpoints`

#### T13: Auth and profile endpoints

**What**: handlers for `setup/status`, `setup`, `auth/login`, `auth/logout`, `me` (GET/PATCH), `me/password`, `me/sessions` (list/delete).
**Where**: `internal/api/auth_handlers.go`
**Depends on**: T12
**Reuses**: users service
**Requirement**: AUTH-01, AUTH-02, AUTH-03, AUTH-04, AUTH-05, AUTH-09
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Login sets cookie HttpOnly, SameSite=Lax, Max-Age 30 d, Secure per config
- [x] Status codes: setup 201/409/401, login 200/401/429, logout 204, me 401 when logged out
**Tests**: e2e
**Gate**: full
**Commit**: `feat(api): add setup, login and profile endpoints`

#### T14: Invites, admin and token endpoints

**What**: handlers for public invite get/accept, admin users/invites, `/users` directory, `/tokens`.
**Where**: `internal/api/admin_handlers.go`
**Depends on**: T13
**Reuses**: users service
**Requirement**: AUTH-06, AUTH-07, AUTH-10, TOK-01, TOK-02, TOK-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Non-admin on admin routes → 403; invite accept 201 then 410 on reuse
- [x] Disabled user's cookie → 401 on next request
- [x] Token create returns secret once; list hides it
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): add invite, admin and token endpoints`

### Phase 3: Rooms, access, messages

#### T15: Authorizer and grants

**What**: `access.Authorizer` (RoomLevel for users and agents, CanUseAgent, CanUseConnector, ConnectorLevel), grants CRUD, `SetVisibility` presets, errors `ErrNotFound`/`ErrForbidden`.
**Where**: `internal/access/access.go`
**Depends on**: T14
**Reuses**: store
**Requirement**: ACL-02, ACL-07, ACL-08
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Level = max(ownership, user grant, `*` grant); admin without grant → none
- [x] Visibility private/shared/public replaces grants exactly as ACL-07
- [x] Disabled grantee resolves to none
**Tests**: integration
**Gate**: full
**Commit**: `feat(access): add authorizer, grants and visibility presets`

#### T16: Delivery rules, trust and loop guard (pure)

**What**: `delivery.Plan`, `delivery.Trusted`, `delivery.LoopGuard` per design.
**Where**: `internal/delivery/rules.go`
**Depends on**: T15
**Reuses**: none
**Requirement**: DLV-01, DLV-02, DLV-03, DLV-04, DLV-05, DLV-06, DLV-08, DLV-10
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Table tests per author kind × outbound flags × recipients × bridge_chats
- [x] Trusted true only for equal user ids, false for empty author user
- [x] 25th consecutive agent message pauses; user/chat message resets to 0
**Tests**: unit
**Gate**: quick
**Commit**: `feat(delivery): add delivery plan, trust and loop guard rules`

#### T17: Rooms and members service

**What**: rooms create/get/list(visible)/update/delete; members add/update/remove with use-permission checks; publishes `RoomChanged`/`MembersChanged`.
**Where**: `internal/rooms/rooms.go`
**Depends on**: T16
**Reuses**: access (T15)
**Requirement**: ACL-01, ACL-03, ACL-05, ACL-06, ROOM-01, ROOM-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Creator is owner/manage; stranger gets ErrNotFound
- [x] Adding agent/chat without use permission → ErrForbidden
- [x] Delete removes messages, attachments rows, cursors, outbox (count 0 after)
**Tests**: integration
**Gate**: full
**Commit**: `feat(rooms): add rooms and members service`

#### T18: Attachment blob store

**What**: `attachments.Store.Put(r io.Reader, limit) (sha256, size, err)` content-addressed under `<data>/blobs/aa/<sha>`, `Open(sha)`, `Delete` when unreferenced; `ErrTooLarge`.
**Where**: `internal/attachments/store.go`
**Depends on**: T17
**Reuses**: none
**Requirement**: SEC-03, OPS-02 (blob cleanup)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Same content stored once; limit+1 bytes → ErrTooLarge and no file left
- [x] Files 0600, dirs 0700
**Tests**: unit
**Gate**: quick
**Commit**: `feat(attachments): add content-addressed blob store`

#### T19: Messages: post, list, resume

**What**: `rooms.Post` (validation, seq, author_user_id, attachments, Plan → outbox rows, LoopGuard, dedupe), `PostFromChat`, `List` pages, `Resume`; publishes `MessageCreated`.
**Where**: `internal/rooms/messages.go`
**Depends on**: T18
**Reuses**: delivery (T16), attachments (T18)
**Requirement**: MSG-01, MSG-02, DLV-03, DLV-04, DLV-05, DLV-08, DLV-09, DLV-10, CON-10
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] 50 concurrent posts → seqs 1..50 without gaps
- [x] Empty text without files and 16,001 chars → validation error
- [x] Outbox rows match Plan; duplicate external id returns existing message, no event
- [x] Pause after 25 agent messages posts a system message; Resume resets
**Tests**: integration
**Gate**: full
**Commit**: `feat(rooms): add message posting, listing and loop guard`

#### T20: Rooms HTTP endpoints

**What**: handlers for rooms, sharing, members, messages (JSON + multipart), resume, attachments download, users directory.
**Where**: `internal/api/rooms_handlers.go`
**Depends on**: T19
**Reuses**: rooms, access
**Requirement**: ACL-01, ACL-03, ACL-04, ACL-07, ROOM-01, MSG-02, SEC-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Auth matrix owner/grantee(view)/grantee(post)/stranger/admin for each route with expected 200/201/403/404
- [x] Multipart over limit → 413; attachment of invisible room → 404; safe images inline, others `attachment`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): add room, member, sharing and message endpoints`

### Phase 4: Delivery runtime, live, operations

#### T21: Outbox worker

**What**: `delivery.OutboxWorker` with `Sender` interface, claim due rows, backoff `2s·2^(n-1)`, max 5 → failed, resume after restart, `OutboxChanged` events.
**Where**: `internal/delivery/outbox.go`
**Depends on**: T20
**Reuses**: store, events
**Requirement**: DLV-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Fake sender failing 5 times → status failed, attempts 5, next_attempt spacing 2/4/8/16 s (fake clock)
- [x] Pending rows from a previous process are sent after start
**Tests**: integration
**Gate**: full
**Commit**: `feat(delivery): add outbox worker with retry and backoff`

#### T22: Live WebSocket hub

**What**: `live.Hub` + `GET /api/v1/live`: cookie auth, Origin check, per-connection access filter, ping 25 s, `lagged` frame.
**Where**: `internal/live/hub.go`
**Depends on**: T21
**Reuses**: events, access
**Requirement**: LIVE-01, LIVE-02, LIVE-03, LIVE-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] User with view receives `room.message`; stranger receives nothing within 500 ms
- [x] Foreign Origin upgrade → 403
- [x] presence/connector/outbox events reach only users with access
**Tests**: e2e
**Gate**: full
**Commit**: `feat(live): add live websocket hub with access filtering`

#### T23: Audit log

**What**: `audit.Append` called from users/tokens/access/connectors services; `GET /api/v1/admin/audit?before=` 50 per page.
**Where**: `internal/audit/audit.go`
**Depends on**: T22
**Reuses**: store
**Requirement**: AUD-01, AUD-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Login, logout, invite, token, grant, user-state actions create entries without secrets
- [x] Pagination newest first, 50 per page; non-admin 403
**Tests**: integration
**Gate**: full
**Commit**: `feat(audit): add audit log and admin listing`

#### T24: Retention job

**What**: daily job deleting messages older than room retention (0 = never) and unreferenced blobs; runs once at start.
**Where**: `internal/rooms/retention.go`
**Depends on**: T23
**Reuses**: attachments
**Requirement**: OPS-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Messages older than N days deleted; retention 0 keeps all; orphan blobs removed, shared blobs kept
**Tests**: integration
**Gate**: full
**Commit**: `feat(rooms): add retention cleanup job`

#### T25: Embedded web UI serving

**What**: `webui.Handler` serving `web/dist` via `go:embed` (placeholder index until web-app), SPA fallback for non-API paths, immutable caching for hashed assets, CSP `default-src 'self'; img-src 'self' data: blob:; connect-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'`.
**Where**: `internal/webui/webui.go`
**Depends on**: T24
**Reuses**: middleware headers
**Requirement**: SEC-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] `/rooms/x` returns index.html with CSP and other SEC-01 headers; `/api/v1/nope` returns JSON 404
**Tests**: e2e
**Gate**: full
**Commit**: `feat(webui): serve embedded single page app with strict headers`

#### T26: Serve and admin commands

**What**: `partyline serve` (logs setup token when needed, graceful shutdown 10 s, slog with redaction), `partyline admin create-user|reset-password`.
**Where**: `internal/cli/serve.go`
**Depends on**: T25
**Reuses**: app
**Requirement**: AUTH-01, SEC-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Log captured during a test that posts a message and logs in contains neither message text nor password nor token
- [x] `admin create-user` creates an admin usable for login
**Tests**: e2e
**Gate**: full
**Commit**: `feat(cli): add serve and admin commands`

#### T27: Container, compose and CI

**What**: multi-stage `Dockerfile` (bun build web → go build with embed, CGO off → `gcr.io/distroless/static:nonroot`, `/data` volume, healthcheck via `partyline healthcheck`), `docker-compose.yml`, `.github/workflows/ci.yml` (lint, test, generate drift, docker build), `.github/workflows/release.yml` (GoReleaser binaries + GHCR image on tags), `.goreleaser.yaml`, README quickstart section.
**Where**: `Dockerfile`
**Depends on**: T26
**Reuses**: Makefile
**Requirement**: OPS-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Dockerfile builds in CI (no local Docker available); image runs as nonroot with `/data`
- [x] `partyline healthcheck` exits 0 when `/healthz` is 200 and 1 otherwise (unit test with httptest)
**Tests**: unit
**Gate**: build
**Commit**: `ci: add container image, compose file and ci workflows`

### Phase 5: Verifier fix iteration 1

#### T28: Cancel queued deliveries of a removed chat

**What**: `RemoveMember` deletes the chat's pending outbox rows of that room in the same tx; `ListDueOutbox` only returns rows whose chat is still a member of the message's room.
**Where**: `internal/rooms/members.go`
**Depends on**: None
**Reuses**: outbox queries
**Requirement**: ROOM-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] A message queued before the chat was removed is never sent; sent rows, other chats and other rooms keep theirs
- [x] The worker skips a pending row whose chat is no longer a member
**Tests**: integration
**Gate**: full
**Commit**: `fix(rooms): cancel queued deliveries when a chat member is removed`

#### T29: Atomic login attempt reservation and bounded limiter

**What**: Reserve the attempt before the argon2 check; success clears the IP's failures; the limiter map evicts expired windows and has a hard cap with oldest eviction.
**Where**: `internal/users/auth.go`
**Depends on**: T28
**Reuses**: failureLimiter
**Requirement**: AUTH-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] A parallel burst of 30 wrong logins from one IP runs at most 5 password checks, the rest get 429
- [x] Sequential behavior unchanged; success clears the failures
- [x] The limiter never holds more keys than its cap
**Tests**: integration
**Gate**: full
**Commit**: `fix(users): reserve login attempts before verifying the password`

#### T30: Live origin scheme mismatch test

**What**: Same host, different scheme Origin is rejected.
**Where**: `internal/api/live_test.go`
**Depends on**: T29
**Reuses**: testkit DialLive
**Requirement**: LIVE-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Dial with the public host and the other scheme returns 403
**Tests**: e2e
**Gate**: full
**Commit**: `test(live): reject same host origin with another scheme`

#### T31: Close live sockets of revoked sessions

**What**: Publish `SessionsRevoked` when sessions end (logout, session delete, password change/reset, user disabled); the hub closes that session's sockets and re-validates every connection's session at least every 60 s.
**Where**: `internal/live/hub.go`
**Depends on**: T30
**Reuses**: events bus
**Requirement**: AUTH-09, AUTH-10
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Logout closes the session's live socket within 1 s
- [x] Revoking another session, disabling the user and a password reset close the affected sockets; other sessions stay open
**Tests**: e2e
**Gate**: full
**Commit**: `fix(live): close live sockets when their session is revoked`

#### T32: Deterministic background job tests

**What**: Replace fixed short waits in app start tests with eventually-style polls with a generous deadline.
**Where**: `internal/app/app_test.go`
**Depends on**: T31
**Reuses**: none
**Requirement**: OPS-02, DLV-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] `TestStartRunsRetention` passes with `-count=20`
**Tests**: integration
**Gate**: full
**Commit**: `test(app): wait for background jobs with a generous deadline`

#### T33: Remove blobs of a deleted room

**What**: After the delete commits, remove the room's blobs that no other attachment references (best effort; the sweep stays as backstop).
**Where**: `internal/rooms/rooms.go`
**Depends on**: T32
**Reuses**: attachments.Store.Delete
**Requirement**: ROOM-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Deleting a room removes its unshared blob files at once and keeps blobs another room references
**Tests**: integration
**Gate**: full
**Commit**: `fix(rooms): remove unshared blobs right after deleting a room`

#### T34: ACL-07 managers and owner cascade edge case

**What**: Reword ACL-07 to managers; replace the last-manager edge case with the owner cascade and cover it with a test.
**Where**: `internal/store/store_test.go`
**Depends on**: T33
**Reuses**: FK cascade
**Requirement**: ACL-07, ROOM-02
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] A manager grantee changes sharing (test)
- [x] Deleting the owner account deletes its rooms (test)
**Tests**: integration
**Gate**: full
**Commit**: `docs(specs): let managers change sharing and delete rooms with their owner`

#### T35: Listen-only explicit recipient

**What**: An explicit recipient whose chat member has outbound off is rejected with 422 `recipients` = `listen-only`.
**Where**: `internal/rooms/messages.go`
**Depends on**: T34
**Reuses**: validation.Check
**Requirement**: DLV-04
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Posting to an outbound-off chat as recipient returns 422 with field `recipients` reason `listen-only`
**Tests**: integration
**Gate**: full
**Commit**: `fix(rooms): reject explicit recipients that are listen-only`

#### T36: Admin self and last-admin guard

**What**: An admin cannot demote or disable themselves; the last active admin cannot be demoted or disabled (409 `conflict`).
**Where**: `internal/users/admin.go`
**Depends on**: T35
**Reuses**: users service
**Requirement**: AUTH-10
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Self demote/disable → 409 conflict
- [x] Last active admin demote/disable → 409 conflict; with a second admin it works
**Tests**: e2e
**Gate**: full
**Commit**: `fix(users): keep at least one active admin`

#### T37: Room member count

**What**: `dto.Room.member_count` counted in the list/get queries; tygo regenerated.
**Where**: `internal/store/queries/rooms.sql`
**Depends on**: T36
**Reuses**: convert.Room
**Requirement**: ACL-03 (read model)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] List and get return the member count
**Tests**: e2e
**Gate**: full
**Commit**: `feat(rooms): expose member count`

#### T38: Activity feed

**What**: `GET /api/v1/activity?limit=` (default 20, max 50): latest messages across rooms the user can view, newest first, `List[ActivityItem{room{id,name}, message}]`; tygo regenerated.
**Where**: `internal/api/activity.go`
**Depends on**: T37
**Reuses**: rooms.Messages, convert.Message
**Requirement**: ACL-03, LIVE-02 (read model)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] Only rooms the caller can view; newest first; default 20; limit capped at 50; invalid limit 422
**Tests**: e2e
**Gate**: full
**Commit**: `feat(api): add activity feed endpoint`

#### T39: Image version build argument

**What**: `ARG VERSION=dev` passed into ldflags; CI and release pass git describe/sha; Dokploy build arg honored.
**Where**: `Dockerfile`
**Depends on**: T38
**Reuses**: Makefile LDFLAGS
**Requirement**: OPS-03
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] hadolint clean; `partyline version` in the image prints the build arg
**Tests**: none
**Gate**: build
**Commit**: `build(docker): stamp the image version from a build argument`

#### T40: Allow inlined fonts in the HTML CSP

**What**: The HTML policy adds `font-src 'self' data:` so the data: URI font Vite inlines loads; everything else stays strict.
**Where**: `internal/webui/webui.go`
**Depends on**: T39
**Reuses**: webui.CSP
**Requirement**: SEC-01
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] The HTML response CSP contains `font-src 'self' data:` and keeps `default-src 'self'`, `script-src 'self'` and `frame-ancestors 'none'`
**Tests**: e2e
**Gate**: full
**Commit**: `fix(webui): allow data uri fonts in the html content security policy`

#### T41: Robust serve start wait

**What**: `TestServeSetupTokenAndRedactedLogs` waits for the server with a generous deadline instead of a 10 s window that times out under load.
**Where**: `internal/cli/serve_test.go`
**Depends on**: T40
**Reuses**: none
**Requirement**: AUTH-01, SEC-05
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] The test passes with `-count=10` under load
**Tests**: e2e
**Gate**: full
**Commit**: `test(cli): wait for the server start with a generous deadline`

#### T42: Contract notes for fix iteration 1

**What**: Record the new APIs and traceability statuses.
**Where**: `contract-notes.md`
**Depends on**: T41
**Reuses**: none
**Requirement**: all
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [ ] contract-notes.md lists activity, member_count and the session revocation event
**Tests**: none
**Gate**: build
**Commit**: `docs(specs): record contract notes for fix iteration 1`

#### T43: Load-tolerant web async queries

**What**: The web test setup gives Testing Library async queries a 10 s timeout (and vitest a 30 s test timeout); `findBy*` failed at its 1 s default on a loaded machine.
**Where**: `web/src/test/setup.ts`
**Depends on**: T35
**Reuses**: none
**Requirement**: gate stability (no AC)
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Web tests pass during a parallel Go test run on a loaded machine
**Tests**: unit
**Gate**: full
**Commit**: `test(web): give async queries a load-tolerant timeout`

#### T44: Generous deadlines in outbox worker tests

**What**: Outbox worker tests poll for up to a minute instead of 15 s.
**Where**: `internal/delivery/outbox_test.go`
**Depends on**: T43
**Reuses**: none
**Requirement**: DLV-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] `TestOutboxResumesAfterRestart` and `TestOutboxWakesOnMessageCreated` pass under load with `-count=10`
**Tests**: integration
**Gate**: full
**Commit**: `test(delivery): wait for the outbox worker with a generous deadline`
