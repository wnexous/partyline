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
