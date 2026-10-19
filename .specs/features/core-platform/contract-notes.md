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
