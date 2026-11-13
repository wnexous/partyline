# Secret Requests Design

**Feature**: `secret-requests` · **Spec**: [spec.md](spec.md) · **Tasks**: [tasks.md](tasks.md)
**Base**: `main` at v0.31.2

## Decisions

| Question | Answer |
| --- | --- |
| Storage | No migration. A request is a message whose `interactive_json` is `{"v":1,"secret":{"name":NAME}}`; the answer is a reply whose `answers_json` is `{"v":1,"secret":{"name":NAME,"saved":true}}` (quick-replies columns, migration 0026). A new query `CountAnswers` (any author) makes it answered once; the unique index `messages_answer_once` still guards one answer per author. |
| Who asks | Only an AI author (`rooms.Post`, 422 on `secret` for a person, SR-03). The session that posted it is the message's `author_label`, which the API already resolves server-side from `X-Partyline-Live` (SESS-29): `""` is the principal. |
| Who gives | A user with post in the room (and a DM that is not history/disabled) for whom `orgs.Authz.Trusts(user, ai)` is true: the identities that manage the AI in its home organization, the rule of `trusted="true"` (POS-10). |
| Route | `POST /rooms/{id}/messages/{mid}/secret` `{"value"}`, 1..16384 bytes (`dto.MaxSecretValue`). `rooms.Service.GiveSecret` checks and records; the value is handed only to a `deliver` closure the API builds over `agents.Gateway.DeliverSecret`, so the rooms layer never sees it. |
| Once | `CountAnswers` before delivery and again inside the insert transaction; an in-memory claim per request (`secretFlights`) refuses a second save while one is in flight (409 `conflict`). |
| Delivery | `Gateway.DeliverSecret` finds the live session by label (`Presence.SessionByLabel`), refuses an offline one (409 `ai_offline`) and a client older than `MinSecretClientVersion` = v0.32.0 (409 `client_outdated`, `dev` current, missing/unparsable old, like `heartbeatCurrent`), writes one `secret` frame directly on the socket (coder/websocket writes are concurrency-safe) and waits on a per-connection pending map (`secretWaits`, keyed by request id, buffered channel) for `secret_ack`, the connection's end, or `GatewayDeps.SecretTimeout` (10 s). Any other outcome is 504 `not_saved` with a one-line reason that is dropped when it contains the value. |
| Frames | `dto.AgentSecretFrame {type:"secret", request_id, message_id, room_id, name, value}` and `dto.AgentSecretAckFrame {type:"secret_ack", request_id, ok, path, error}` in `internal/api/dto/secretrequests.go`, next to the frame type constants in `agents.go`. |
| Client | The read loop of `link.session` handles a `secret` frame at once (before the notification queue and whether or not the harness is initialized): `writeSecret` writes `<SecretsDir>/<ai id>/<NAME>` (CreateTemp 0600 + rename over the target; folders 0700, symlinked folder refused, wider modes tightened) and answers. `SecretsDir` is `cliconfig.SecretsDir` (the `secrets` folder next to `config.json`), passed by the CLI to `channel` and `listen`. The frame never becomes a notification or a `listen` line, and its bytes are cleared. |
| SR-09 | The notification of a message whose `answers.secret.saved` is true gets a sentence (NAME saved to PATH by WHO; read it, never print or paste it) and `secret_path` in the meta (`withSecretNote`). |
| Tool | `request_secret(room, name, text)`: refuses `connection`/`chat`, validates the name and 1..500 characters, posts `{text, secret:{name}}` and returns `{message_id, name, path, status}`; the path uses the message's `author_id` (the AI id), so a tools-only process knows it too. |
| Logging | No middleware logs bodies; `GiveSecretRequest` is decoded in the handler only; errors are fixed sentences. A test captures slog at debug level. |
| Web | Feature slice `features/give-secret` (`useGiveSecret` with `gcTime: 0` and `reset()` after settling so the mutation keeps no variables; `SecretRequestCard`). Whether the viewer manages the AI is `managesAI(profile, meId)` over the existing `IntelligenceProfile.ai.instructed_by` (the same `Trusts` set), fetched only when the viewer could give it. `PasswordInput` of `form-fields` is exported and extended (`toggleClassName`, labels). The saved state is the timeline's answer (any author), passed as `secretAnswer`. |

## Out of scope / known limits

- The web field is a single-line password input: a pasted multi-line value loses its newlines there
  (the API accepts newlines).
- A request posted by a tools-only call with no live session resolves to the principal label; the
  value then goes to whatever principal session is live when it is given.
- The secret frame carries the value as a JSON string; Go strings cannot be wiped, only the frame bytes.
