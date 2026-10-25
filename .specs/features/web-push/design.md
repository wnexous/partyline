# Web Push Notifications Design

**Feature**: `web-push` · **Spec**: [spec.md](spec.md) · **Tasks**: [tasks.md](tasks.md)
**Status**: Draft for approval · **Base**: `main` at `fbc5c7d` (v0.7.0)

## Answers to the brief's design questions

| Question | Answer |
| --- | --- |
| Where VAPID keys live | `<data dir>/vapid.key`: the PKCS#8 private key sealed with the instance `secrets.Box` (the box built from `<data dir>/secret.key`), base64 with mode 0600, generated once on first start. `GET /api/v1/push/key` returns the public point. → [VAPID keys](#vapid-keys) |
| Which library | None: a small standard-library implementation of RFC 8291 + RFC 8292 (`crypto/ecdh`, `crypto/hkdf`, `crypto/ecdsa`), pinned to the RFC 8291 section 5 vector. `github.com/SherClockHolmes/webpush-go` v1.4.0 (MIT) was evaluated and rejected (reasons below). → [Encryption and VAPID](#encryption-and-vapid) |
| Data model | Migration `0025_push_subscriptions.sql`: `push_subscriptions` (one row per endpoint, tied to the registering session) and `users.push_groups` (`all`/`mentions`/`off`, default `mentions`). → [Data model](#data-model) |
| Where the send hook goes | The notifier subscribes to `events.MessageCreated` in `internal/webpush` (app layer), resolves the unread users and their subscriptions, and hands payload jobs to a bounded worker. Nothing is sent from the request path. → [Event flow](#event-flow) and [The worker](#the-worker) |
| "Conversation open and focused" | The server does not know the focused room: `internal/live/hub.go:168` uses `ws.CloseRead`, so client frames are discarded. The rule is kept **client-side**: the service worker suppresses the notification when a focused window's URL path is the conversation. No server protocol change. → [Focus rule](#focus-rule) |
| Privacy | Payload encrypted to the browser; no message text, endpoint path, key or token in any log, status or error string. Push-service outcomes are logged as status + endpoint hostname only. → [Privacy](#privacy) |
| Web | `web/public/sw.js` (classic worker, copied to `dist/` by Vite and served by `internal/webui` from `/` with `no-cache`), a `NotificationsSection` in `web/src/routes/_app/settings.tsx`, and `web/public/manifest.webmanifest` + generated 192/512 icons. The CSP needs no change. → [Web](#web) |
| Every new route | `GET /api/v1/push/key`, `POST`/`DELETE /api/v1/push/subscriptions`, `POST /api/v1/push/test` (all user-only) and `PATCH /api/v1/me` with `push_groups`. Agent tokens get 403, anonymous callers 401; `DELETE` only touches the caller's rows. → [HTTP API](#http-api) |

## Overview

```
POST /rooms/{id}/messages
        │ (transaction commits)
        ▼
events.MessageCreated  (internal/events/bus.go)
        │ non-blocking publish (internal/rooms/messages.go:212-263)
        ▼
webpush.Notifier.Run ──► room + message + targets (2 SQL queries)
        │                    │ Decide(mode, kind, author, user, unread, mentioned)
        │                    │ limiter.allow(user)
        │                    ▼
        │            payload JSON per recipient
        │                    │
        │            bounded queue (256, 4 workers, panic guard)
        ▼                    ▼
webpush.Deliverer ──► RFC 8291 encrypt + VAPID JWT ──► push service
        │                                              │
        │ 201 ◄──────────────────────────────────────┘
        │   ├─ 2xx: last_ok_at = now
        │   ├─ 404/410: DELETE the subscription
        │   └─ other: keep; log status + endpoint host
        ▼
browser service worker ──► notification ──► click: focus + open /rooms/{id}
```

The HTTP request that stores the message only publishes the event; it never waits for a push. This mirrors the existing agent dispatcher (`internal/delivery/dispatcher.go:39-90`), which also subscribes to the bus and reads the room in its own loop.

## VAPID keys

**Storage.** The server already has one generated secret file, `<data dir>/secret.key` (base64 of 32 random bytes, mode 0600), loaded or created by `secrets.LoadOrCreateKey` (`internal/secrets/box.go:73-107`). The `*secrets.Box` built from it seals connector secrets into `connectors.secrets_enc` (`internal/connectors/kind.go:325`) and pairing secrets (`internal/devices/devices.go:334`). The VAPID private key follows the same rule:

- file: `<data dir>/vapid.key` (`webpush.VAPIDKeyFile = "vapid.key"`);
- content: `base64.StdEncoding(Box.Seal(x509.MarshalPKCS8PrivateKey(priv)))` + `"\n"`;
- mode 0600, created with `O_CREATE|O_EXCL`; if another process created it first, the file is read again (single writer is guaranteed by `datalock`, `internal/cli/serve.go:43`, but the read-again path keeps a test restart safe);
- generated once with `ecdsa.GenerateKey(elliptic.P256(), rand.Reader)`; the public key is `priv.PublicKey.ECDH().Bytes()` (65-byte uncompressed point, `0x04` first);
- corrupt/wrong-curve/unreadable file → warn (no content), replace it with a new pair and delete every `push_subscriptions` row in the same startup (they were bound to the old key); the server always starts (PUSH-26). Browsers heal themselves on their next signed-in load: `ensurePushSubscription` sees a different `applicationServerKey` and subscribes again (PUSH-28).

`app.New` loads it next to the secret key (`internal/app/app.go:141-150`). Unlike the secret key, a bad VAPID file never fails startup (see above); only an I/O error writing the new file does. The public key is served as `base64.RawURLEncoding` without padding.

No environment override in v1: the brief says "generated once and stored under the data dir", and an env override would make the file and the process disagree. Backup guidance: keep `vapid.key` with `secret.key` (design, Operations).

## Encryption and VAPID

### Library decision

The most established maintained Go Web Push library compatible with the module's Go version is **`github.com/SherClockHolmes/webpush-go` v1.4.0** (MIT; latest commit 2026-04-22; `go.mod` `go 1.13`; it would add `github.com/golang-jwt/jwt/v5` v5.2.1 and use the existing `golang.org/x/crypto`). It was rejected for three reasons:

1. **Test pinning.** Its RFC 8291 encryption is unexported; its tests only assert an HTTP status (`webpush_test.go`), so a test of ours cannot reproduce the RFC vector against the library. The brief asks for a vector or the library's, and the library has none.
2. **Crypto API.** It uses the deprecated non-constant-time `crypto/elliptic` `ScalarMult`/`GenerateKey` API for ECDH; Go's `crypto/ecdh` is the constant-time implementation and is in the standard library since 1.20.
3. **Dependency budget.** AD-004 already chose a hand-written MCP server over an SDK; this is ~180 lines of well-specified crypto with an official vector, and it adds no dependency.

The standard-library implementation is therefore chosen. It uses `crypto/ecdh` (P-256 ECDH), `crypto/hkdf` (Go 1.24+, `go.mod` says Go 1.27), `crypto/aes` + `crypto/cipher` (AES-128-GCM) and `crypto/ecdsa` (ES256).

### RFC 8291 (payload)

`webpush.Encrypt(payload, uaPublic, authSecret []byte, asPrivate *ecdh.PrivateKey, salt []byte) ([]byte, error)`:

```
uaPublic   = 65-byte uncompressed P-256 point (subscription p256dh)
authSecret = 16 bytes (subscription auth)
as         = ephemeral P-256 key pair (generated per send in production)
ecdh       = ECDH(as.private, uaPublic)                       // 32 bytes
key_info   = "WebPush: info\x00" || uaPublic || as.public      // 0x00 after the ASCII
prk_key    = HKDF-Extract(sha256, authSecret, ecdh)
ikm        = HKDF-Expand(sha256, prk_key, key_info, 32)
prk        = HKDF-Extract(sha256, salt, ikm)
cek        = HKDF-Expand(sha256, prk, "Content-Encoding: aes128gcm\x00", 16)
nonce      = HKDF-Expand(sha256, prk, "Content-Encoding: nonce\x00", 12)
ciphertext = AES-128-GCM(cek, nonce, payload || 0x02)         // 0x02 = last record, no padding
body       = salt(16) || 0x00 0x00 0x10 0x00 (4096) || 0x41 (65) || as.public || ciphertext
```

`0x02` is the RFC 8188 padding delimiter for the final record; the record size is 4096 (`0x00001000`). The header is 86 octets. The payload is at most ~1 KiB, far below the 3993-octet plaintext limit.

**Pinned vector** (RFC 8291 §5 / Appendix A), written in the test as base64url without whitespace:

| Input | Value |
| --- | --- |
| UA public (`p256dh`) | `BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4` |
| auth secret | `BTBZMqHH6r4Tts7J_aSIgg` |
| AS private | `yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw` |
| salt | `DGv6ra1nlYgDCS1FRnbzlw` |
| plaintext | `When I grow up, I want to be a watermelon` |
| expected body | `DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN` |

The production entry point `EncryptRandom(payload, uaPublic, authSecret)` generates a fresh ephemeral key and a 16-byte salt with `crypto/rand`; the test calls `Encrypt` with the injected key and salt. This was verified against the RFC during the design (the table above is the output of the algorithm).

The browser public key is validated with `ecdh.P256().NewPublicKey`; an off-curve or wrong-length point is a 422 on subscribe. If a stored row is corrupt (tampering), `Encrypt` returns an error, the `Deliverer` logs it without content and keeps the row; the worker continues with the next job.

### RFC 8292 (VAPID)

`webpush.VAPID` holds the `*ecdsa.PrivateKey` and the base64url public key.

- JWT header: `{"typ":"JWT","alg":"ES256"}` (base64url, no padding).
- Claims: `aud` = the push endpoint origin (`scheme://host`, not the path), `exp` = now + 12 h (RFC 8292 caps it at 24 h), `sub` = `cfg.PublicURL.String()` (an `https:` URI, which RFC 8292 allows; production must set `PARTYLINE_PUBLIC_URL` to https).
- Signature: SHA-256 over `header.payload`, ECDSA P-256, raw `r || s` (32 + 32 bytes), base64url.
- Header on the push request: `Authorization: vapid t=<jwt>,k=<public-key>`.
- A JWT is generated per send (one ECDSA signature; cheaper than a cache and immune to clock skew across a long-lived cache).

No personal email is invented for `sub`; the instance URL is the application server's stable identity. The public key in `k` is the same 65-byte point the browser passes as `applicationServerKey`.

### HTTP client

`webpush.Client` (in `client.go`) implements `Sender`:

```go
type Subscription struct { ID, Endpoint, P256dh, Auth string } // ID is the stored row; the client ignores it
type Sender interface { Send(ctx context.Context, sub Subscription, payload []byte) (int, error) }
```

Request:

- `POST <endpoint>` with `http.NewRequestWithContext`;
- `Content-Encoding: aes128gcm`, `Content-Type: application/octet-stream`, `TTL: 3600`, `Urgency: normal`;
- `Authorization: vapid t=…,k=…`;
- body = `EncryptRandom(payload, p256dh, auth)`;
- the requester is `safehttp.NewClient(safehttp.Options{Timeout: 10 * time.Second, MaxResponseBytes: 4096})` (`internal/connectors/safehttp`, the client connectors already use for CON-13): it refuses loopback, private, link-local and other non-public destinations at dial time, so a forged subscription cannot turn the server into an SSRF proxy, and it never follows a redirect, so the VAPID JWT is not sent to a redirect target. Tests inject a `Doer` (the `httptest` server's client), so they do not need `AllowPrivate`;
- the response body is drained and closed (the safe client caps it at 4 KiB); the body is never read into a log or an error;
- returns the status code; a network error is returned to the caller, which logs only a sanitized kind (`network`, `timeout`, `canceled`).

## Data model

### Migration `0025_push_subscriptions.sql`

```sql
-- Web push notifications (web-push: PUSH-03, PUSH-06, PUSH-07, PUSH-09).
-- One row per browser push subscription. endpoint is the browser's capability URL and is
-- unique: a second POST for it updates the row (the browser moved to another account, the
-- keys rotated, or the user enabled it again after logging in). session_id ties the row to
-- the session that registered it, so logout / a revoked device / a password change removes
-- it (ON DELETE CASCADE); an account delete removes every row of the user. last_ok_at is
-- written only after a 2xx from the push service.
CREATE TABLE push_subscriptions (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    session_id TEXT REFERENCES sessions (id) ON DELETE CASCADE,
    endpoint   TEXT NOT NULL,
    p256dh     TEXT NOT NULL,
    auth       TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    last_ok_at INTEGER
);
CREATE UNIQUE INDEX push_subscriptions_endpoint ON push_subscriptions (endpoint);
CREATE INDEX push_subscriptions_user ON push_subscriptions (user_id);
CREATE INDEX push_subscriptions_session ON push_subscriptions (session_id) WHERE session_id IS NOT NULL;

-- How group messages reach an account (PUSH-06). DMs ignore it. Existing accounts start
-- with mentions, as the brief requires.
ALTER TABLE users ADD COLUMN push_groups TEXT NOT NULL DEFAULT 'mentions'
    CHECK (push_groups IN ('all', 'mentions', 'off'));
```

AD-012 is respected: a new table plus one `ADD COLUMN` with a default; no table is rebuilt. There is no Go hook.

### Queries (`internal/store/queries/push.sql`, plus `users.sql`)

- `UpsertPushSubscription` — `INSERT … ON CONFLICT (endpoint) DO UPDATE SET user_id, session_id, p256dh, auth, last_ok_at = NULL RETURNING *`. `created_at` is kept from the first registration (history of when this endpoint appeared).
- `DeletePushSubscription` — `DELETE … WHERE endpoint = ? AND user_id = ?`, returns rows affected (the ownership check is in the SQL, so another user's endpoint is 404).
- `DeletePushSubscriptionByID` — used by the 404/410 path.
- `ListPushSubscriptionsByUser` — for `POST /push/test`.
- `MarkPushSubscriptionDelivered` — `UPDATE … SET last_ok_at = ? WHERE id = ?`.
- `SetUserPushGroups` — `UPDATE users SET push_groups = ? WHERE id = ?` (next to `SetUserLocale`, `internal/store/queries/users.sql:23`).
- `ListGroupPushTargets(room_id, seq)` — one row per (user, subscription) of a **group**:
  - join `room_members` (`kind = 'user'`) → `users` (`disabled_at IS NULL`) → `intelligences` (the handle for mentions) → `push_subscriptions`;
  - `LEFT JOIN read_cursors` on (room, user);
  - `unread = CASE WHEN seq > COALESCE(c.seq, 0) AND seq > r.read_baseline THEN 1 ELSE 0 END` — the same rule as `CountUnreadByRoom` (`internal/store/queries/conversations.sql:53-63`);
  - no row for a user without a subscription or for an AI member.
- `ListDMPushTargets(room_id, seq)` — the same shape for `rooms.kind = 'dm'`, joining `intelligences i ON i.id IN (r.dm_a, r.dm_b)` → `users` → subscriptions; a DM with an AI and a user has one row (the user), a DM between two AIs has none.

Both target queries return `user_id, handle, push_groups, subscription_id, endpoint, p256dh, auth, unread`. Grouping by user (and the rate limit) happens in Go.

### Lifecycle

- **Subscribe**: `app.New` request → validation → upsert. `session_id` is the principal's session (`authn.Principal.SessionID`), `NULL` for a user API token (there is no session to revoke; the user can delete the endpoint explicitly).
- **Session ends**: `DeleteUserSession`/`DeleteSessionsByUser` already delete session rows (`internal/store/queries/sessions.sql`); the FK cascade removes the subscriptions of that session. A password change deletes the other sessions (`internal/users/profile.go`), so a lost device stops receiving previews.
- **Account ends**: `users` cascade removes all subscriptions.
- **Device stops**: the browser calls `unsubscribe()` and `DELETE /push/subscriptions`; a 404 is treated as already gone.
- **Push service says 404/410**: the worker deletes the row; no retry.
- **Repeat subscribe**: the endpoint is unique; the row moves to the latest user/session and the keys are replaced, `last_ok_at` is reset.

## Event flow

### Where the event comes from

`rooms.Service.Post` and `post` commit in one transaction and then publish `events.MessageCreated{RoomID, MessageID, Seq}` (`internal/rooms/messages.go:212-263`, `insert` at `:288-320`). The bus never blocks a publisher (`internal/events/bus.go`, `Publish`): a slow subscriber misses events and gets `Lagged`. The push notifier must therefore never be in that path. The agent dispatcher is the precedent: it subscribes in `NewAgentDispatcher` before `Run` (`internal/delivery/dispatcher.go:39-64`) so no message stored after construction is missed.

`MessageCreated` is the only event the notifier handles. Edits/deletes (`events.MessageUpdated`) and reactions (`events.ReactionsChanged`) are ignored (PUSH-23). System notices are `MessageCreated` too (the loop guard inserts one in the same transaction, `internal/rooms/messages.go:249-257`); the notifier skips `author_kind = 'system'` (PUSH-08).

### Who gets it

For each `MessageCreated`:

1. `GetRoom(roomID)` and `GetMessageView(messageID)`; a missing room or message is ignored, and so is a message whose `deleted_at` is set (a post deleted before its event was handled). `message_views` gives `text`, `author_iid`, `author_name` (the per-room name, AD-024) and `author_kind`.
2. If `author_kind == 'system'`, stop.
3. Load the targets with `ListGroupPushTargets` (group) or `ListDMPushTargets` (DM), passing `seq`.
4. Group the rows by `user_id`. For each user:
   - skip when `user_id == view.author_iid` (own message);
   - `mentioned := Mentions(view.text, handle)`;
   - `if !Decide(mode, room.kind, view.author_iid, user_id, unread != 0, mentioned) { continue }`;
   - skip when the user's read cursor already covers the message (the `unread` column, PUSH-09);
   - `if !limiter.allow(userID) { continue }` (PUSH-13);
   - build the payload (the title is the group name; for a DM the author is the other participant, so the title is `author_name`; the group/DM branch is the only difference);
   - for each subscription of that user, submit one job (`Submit` is non-blocking; a full queue drops it, PUSH-27).

`Decide` is a pure function in `internal/webpush/decide.go`:

| Room | Setting | Mention | Unread | Author is the user | Send |
| --- | --- | --- | --- | --- | --- |
| DM | any | any | yes | no | **yes** |
| DM | any | any | yes | yes | no |
| group | `all` | any | yes | no | **yes** |
| group | `mentions` (default) | yes | yes | no | **yes** |
| group | `mentions` | no | yes | no | no |
| group | `off` | yes | yes | no | no |
| group | `off` | no | yes | no | no |
| any | any | any | no | any | no |
| any | any | any | any | system notice | no |

`Mentions(text, handle)` scans case-insensitively for `@handle` with no letter/digit/underscore immediately before and no `a-z`, `0-9` or `-` immediately after — the same boundary rule as the AI mentions in `internal/delivery/wants.go:102-134`, applied to the handle only. An empty handle is false.

`Preview(text)` collapses every run of whitespace (newlines included) to one space, trims, and keeps at most 120 runes; the 120th rune of a longer text is `…` (so the preview is never 121). File-only messages have an empty preview.

The setting is `users.push_groups`, loaded with the target rows. Unknown values (a corrupted row) behave as `mentions` (the safe default), never as `all`. The loop guard's `agents_paused` flag is about AI delivery (DLV-08), not about a user's unread list, so it does not suppress a user's notification.

## The worker

### Package and architecture test

`internal/webpush` is a new package in the **app** layer, next to `live` (`internal/archtest/layers_test.go:34-38`), because push notifications are a web-messenger concern: it reads rooms and users through `store/db`, which the whole application shares (AD-025's trade-off), and it never becomes a dependency of `connectors`, `platform`, `agents` or `channel`. The core → app allow-list (`internal/archtest/layers_test.go:60-67`) is untouched. The new package must be added to `packagesOf` in the same commit that creates it, or `TestTheLayerTableMatchesThePackages` fails.

Files:

| File | Responsibility |
| --- | --- |
| `vapid.go` | load/create the sealed VAPID key; public key |
| `encrypt.go` | RFC 8291 `Encrypt`/`EncryptRandom` |
| `jwt.go` | RFC 8292 JWT and `Authorization` header |
| `client.go` | `Subscription`, `Sender`, HTTP push client |
| `decide.go` | `Decide`, `Mentions`, `Preview` (pure) |
| `worker.go` | bounded queue, 4 goroutines, panic guard, stop on cancel |
| `limiter.go` | sliding-window limiter (10/min notifications, 3/min test) |
| `notifier.go` | bus subscription, target resolution, decision, payload, enqueue, `EnqueueTest` |
| `delivery.go` | send a job: client, status handling, `last_ok_at`, 404/410 delete, logs |
| `service.go` | push routes' use cases: key, subscribe, unsubscribe, test; validation |

### Lifecycle and shutdown

- `app.New` builds the VAPID key, the `Client`, the `Notifier` (which subscribes to the bus **immediately**, buffer 512) and the `Service`; `app.Start` runs the worker through `a.background(bg, "webpush", notifier.Run)` (`internal/app/app.go:280-310`). The subscription exists before the HTTP server accepts requests, so a message posted in the first seconds is not missed.
- `app.Shutdown` cancels the context (`a.stop()`), calls `Notifier.Close()` (which cancels the bus subscription, also when `Run` never started because of `DisablePush`), and `Run` waits for its 4 goroutines. In-flight HTTP requests use that context and return promptly; queued jobs are dropped.
- `app.Options.DisablePush` (like `DisableOutbox`) keeps `Start` from running the notifier; `testkit` exposes it so tests that drive their own notifier do not get a second one. `app.Options.PushQueueSize` (default 256, test-only) lets the route test fill the queue and observe the 503 of PUSH-27.
- The notifier ignores `Lagged` (notifications are ephemeral; refetching would notify about old messages) and logs it at debug.

### Queue, rate limit, panic guard

- Queue: `jobs chan func(context.Context)` with capacity **256** (`app.Options.PushQueueSize` in tests); `Submit` uses a non-blocking send and returns false when full. A dropped message job is logged at warn (`webpush: queue full`, count only, no ids/text); a dropped test job makes the route answer 503 (PUSH-27).
- Workers: **4** goroutines, each runs jobs until the context ends; a job's panic is recovered by `guard.go`, which logs `panic of type %T` and the stack but never the panic value (the connectors' `Guard` rule, `internal/connectors/guard.go:38-62`). The worker survives and keeps processing.
- Rate limit: a mutex-guarded sliding window, `10` notifications / `time.Minute` per user id, modeled on `internal/agents/limit.go:31-61` (copied into `webpush` because it is 30 lines and the `agents` limiter is unexported). The notification limit is checked once per user per event, before enqueuing their devices, so a user with three devices still gets one slot (PUSH-24). A separate limiter in the service limits the test route to `3`/minute. The limiter is in memory: a restart resets it, which is fine for a rate limit.
- HTTP timeout: 10 s per request (`safehttp.Options.Timeout`), so a slow push service cannot hold a worker forever.

### Delivery and status handling

`delivery.go` `Deliver(ctx, sub, payload)`:

| Outcome | Action | Log |
| --- | --- | --- |
| 200/201/202/204 | `MarkPushSubscriptionDelivered(sub.ID, now)` | debug, `endpoint_host`, `status` |
| 404/410 | `DeletePushSubscriptionByID(sub.ID)` | info, `endpoint_host`, `status` |
| 429/5xx/other non-2xx | keep the row | warn, `endpoint_host`, `status` |
| network error/timeout | keep the row | warn, `endpoint_host`, `err` ∈ {`network`, `timeout`, `canceled`} |

`endpointHost` parses the endpoint and returns `u.Hostname()`; an unparsable endpoint (it was validated on subscribe) returns `"invalid"`. The error from `http.Client` is **never** logged raw: a `*url.Error` contains the full endpoint, including the capability token in its path. `sendErrorKind` maps the error to one of three words. The message text and the payload never appear in a log, an error or a status (PUSH-12, PUSH-22). No payload is ever logged.

`MarkPushSubscriptionDelivered` runs after the response; a failure to write it is logged at warn without content and does not affect the other subscriptions.

## HTTP API

All routes are under the existing middleware chain (`internal/api/middleware.go`): the session cookie needs same-origin for a state-changing request (CSRF, `:257-283`); an agent token is refused outside `/api/v1/agent/` (`agentRestriction`, `:239-250`) with 403. Handlers call `webpush.Service`; authorization is in the service.

| Route | Caller | Request | Success | Errors |
| --- | --- | --- | --- | --- |
| `GET /api/v1/push/key` | logged-in user | — | 200 `{"public_key":"…"}` | 401 anonymous; 403 agent token |
| `POST /api/v1/push/subscriptions` | logged-in user | `{"endpoint","p256dh","auth"}` | 200 `PushSubscription` (id, endpoint, created_at, last_ok_at) | 422 `invalid` with fields; 401; 403 |
| `DELETE /api/v1/push/subscriptions` | logged-in user | `{"endpoint"}` | 204 | 404 not owned or unknown; 401; 403 |
| `POST /api/v1/push/test` | logged-in user | — | 202 `{"queued":n}` | 409 `conflict` no subscription; 429 `rate_limited`; 503 `unavailable` queue full; 401; 403 |
| `PATCH /api/v1/me` (existing) | logged-in user | `{"push_groups":"all"\|"mentions"\|"off"}` | 200 `Me` with `push_groups` | 422 `invalid` on `push_groups`; 401; 403 |

Notes:

- **POST is an upsert and answers 200**, not 201: the same browser sending the same endpoint again is normal (re-enable, account switch, re-register on load). The answer carries the row so the client can show its id.
- **DELETE in the body, not in the path**: the endpoint is a URL with slashes and a long token; a path parameter would need double encoding. DELETE with a JSON body is supported by `net/http` and the web client. The service answers 404 for another user's endpoint (it cannot see it), and the web treats 404 on disable as "already off".
- **Validation** (in `webpush.Service`, not the handler): endpoint is an absolute `https` URL with a host and at most 2048 characters; `p256dh` base64url decodes to 65 bytes, starts with `0x04` and is a point on P-256; `auth` base64url decodes to exactly 16 bytes. Padded and unpadded base64url are both accepted; the stored value is the canonical unpadded form (`base64.RawURLEncoding`), which is what the client decodes. Errors use the existing `validation.Error` → 422 `invalid` with the field name, never the supplied value.
- **Test route**: `Service.Test` lists the caller's subscriptions (`ListPushSubscriptionsByUser`); none → `ErrNoSubscriptions` (409); the test limiter → `ErrRateLimited` (429); `Notifier.EnqueueTest` returns how many jobs it queued; 0 → `ErrBusy` (503). The response is 202; the actual push happens in the worker (never in the request path).
- **Me**: `dto.Me` gains `push_groups`; `dto.UpdateMeRequest` gains `*string PushGroups`; `users.ProfileUpdate` gains `PushGroups *string` and validates the enum; `toMe` copies the field (`internal/api/auth_handlers.go:31-36`, `:123-132`). This is the same pattern as `locale` (`internal/users/profile.go:38-88`).
- **New DTO file** `internal/api/dto/push.go`; `make generate` regenerates `internal/store/db` and `web/src/lib/api/types.gen.ts` (AD-003).

## Payload

```json
{
  "v": 1,
  "title": "Design",
  "author": "Ana",
  "preview": "the deploy is green",
  "url": "https://partyline.example.com/rooms/0192f0…",
  "tag": "room:0192f0…"
}
```

- **title**: the group's name; in a DM the other participant's display name (which is the author for a user or AI message).
- **author**: `message_views.author_name` (the member display name for an AI in that room; AD-024 keeps the name only).
- **preview**: `Preview(text)`, at most 120 characters, whitespace collapsed, plain text; the worker passes it to the notification as-is and never interprets HTML.
- **url**: `cfg.PublicURL.JoinPath("rooms", room_id)` — the route is `web/src/routes/_app/rooms/$roomId.tsx:58` (the `_app` layout is pathless), and `JoinPath` keeps a sub-path deployment and a trailing slash correct. Absolute so `clients.openWindow` works from the worker.
- **tag**: `room:{room_id}` (the id is in the URL; the tag is only a key) collapses notifications of the same conversation on the OS instead of stacking every message.
- **v**: lets a later payload add fields without breaking installed workers; an unknown `v` is still displayed with the fields present. The payload carries no message id, no attachment and no participant list.

The encrypted body is ~300-800 bytes. Nothing else is included: no message id, no attachment, no participant list, no token, no server secret.

## Privacy

- **On the wire**: the payload is encrypted to the browser's subscription key (RFC 8291); the push service sees the endpoint (and its token), the VAPID public key, the JWT, TTL, urgency and the ciphertext. That is inherent to Web Push and is accepted; the brief requires the text preview, and it is protected end to end to the browser.
- **At rest**: only the subscription material (`endpoint`, `p256dh`, `auth`, timestamps) is stored. No message text, no payload, no JWT, no VAPID private key outside the sealed file.
- **In logs**: no `text`, `preview`, `title`, `author`, endpoint path, `p256dh`, `auth`, JWT or private key. Push outcomes use `endpoint_host` + `status`; network errors are reduced to `network`/`timeout`/`canceled`. The notifier's error paths log room/message ids at most (metadata, not content), matching the existing dispatcher style.
- **In errors**: `Encrypt` returns `errors.New("invalid subscription key")`/`"cannot encrypt payload"` without values; the API's validation errors name the field, not the value; the HTTP client's error is sanitized before logging.
- **In the browser**: the page sends the subscription to the API and keeps no message text in `localStorage`/IndexedDB; the worker holds the payload only in memory for `showNotification`. The operating system's lock-screen settings decide whether the preview is visible when locked.
- **Docs**: `docs/api.md` and `README.md` state the above (PUSH-25).

## Web

### Service worker

`web/public/sw.js` is a classic (non-module) worker, kept out of the Vite graph so it needs no build step and cannot import hashed chunks. Vite copies `web/public/*` to `web/dist/*`; `internal/webui` embeds `dist` and serves a real file at the root, with `Cache-Control: no-cache` for anything outside `assets/` (`internal/webui/webui.go:48-92`), so a new worker is picked up on the next load. `/sw.js` at the root means the default scope is `/`; registration asks for `{ scope: "/" }` explicitly.

The worker has no `fetch` handler and no cache (this is not an offline PWA):

```
install   → skipWaiting()
activate  → clients.claim()
push      → parse event.data.json(); showNotification(...)
click     → focus/navigate or openWindow(url)
```

`show(data)`:

1. `const focused = await clients.matchAll({ type: "window", includeUncontrolled: true })`;
2. if a client has `focused === true` and `new URL(client.url).pathname === new URL(data.url).pathname`, show it with `silent: true` and close it right away (`registration.getNotifications({tag})` → `close()`), never return without a notification: Safari revokes a subscription that gets pushes without one, and Chrome shows a generic one (PUSH-17);
3. `registration.showNotification(data.title || "partyline", { body: data.preview ? (data.author ? data.author + ": " + data.preview : data.preview) : (data.author || "New message"), tag: data.tag, data: { url: data.url }, icon: "/icons/icon-192.png" })`.

`click`:

1. `notification.close()`;
2. `clients.matchAll({type:"window", includeUncontrolled:true})`; find a same-origin client; `focus()` it and `navigate(data.url)` when its path differs (a `navigate` that fails still leaves it focused);
3. otherwise `clients.openWindow(data.url)`.

Malformed or missing `event.data` never throws: the worker falls back to `{title: "partyline", preview: "", url: "/"}` and still opens the app. The notification's title and body are plain strings; the worker never interprets HTML and never touches the DOM.

### Focus rule

The live socket cannot answer "which room is focused": `internal/live/hub.go:168` calls `ws.CloseRead(ctx)`, which discards client frames (the socket is server → client only today; the only client messages are protocol control frames). The web *does* know the open conversation — `openRoomId` in `web/src/lib/live/store.ts:43`, set by `useOpenConversation` (`web/src/hooks/use-open-conversation.ts:9-16`) from the route plus `document.visibilityState` — but it never tells the server.

Keeping the rule server-side would mean replacing `CloseRead` with a read loop plus a per-user, per-connection focus registry in the hub (~100 lines and its own concurrency tests) and a new client frame. The brief allows dropping the rule. Instead, the worker enforces it exactly where the decision is visible: at display time, using the focused window's URL (the conversation route is `/rooms/{id}`). This has no server protocol change, works for every device, and is testable with a fake `clients` object. The trade-off is a race: if the tab gains or loses focus between the push service delivering and the worker checking, the decision can be one event stale; and a browser that does not report `focused` shows the notification (the safe direction). A push for a *different* conversation while the app is focused is still shown, because only the matching path is suppressed.

### Registration and re-subscription

- `web/src/lib/push/register.ts`:
  - `registerServiceWorker()`: if `serviceWorker` is in `navigator`, `navigator.serviceWorker.register("/sw.js", { scope: "/" })`; failures are swallowed (a browser without it is handled by Settings). Called once from `web/src/main.tsx`.
  - `ensurePushSubscription()`: if `Notification.permission === "granted"` and `navigator.serviceWorker` exists, wait for `ready`, `getSubscription()`; when a subscription exists, compare its `options.applicationServerKey` with `GET /push/key`: equal → `POST /api/v1/push/subscriptions` with its keys; different → `unsubscribe()`, `subscribe` with the current key (permission is already granted, no prompt) and POST the new one (PUSH-28). It never calls `requestPermission`. Called from `web/src/routes/_app.tsx` (only after login, so no 401 on the login page). This restores the server row after a session was revoked or the database was restored; it is idempotent (upsert).
- `web/src/lib/push/subscription.ts` (helpers used by Settings and its tests): `pushSupported()`, `permissionState()`, `currentSubscription()`, `subscribe(publicKey)`, `unsubscribe()`, `subscriptionJSON(sub)` (endpoint + `toJSON().keys`), `applicationServerKey(base64url)` (base64url → `Uint8Array`). `register.ts` (above) keeps only the two registration functions, so the Settings task and the PWA task touch different files.

### Settings

`web/src/routes/_app/settings.tsx` adds `NotificationsSection` after `AppearanceSection` (the section list is at `:69-74`):

- Title "Notifications on this device", description "DMs always notify. Group messages follow the setting below."
- State line (one of): unsupported browser; notifications blocked in the browser; not enabled on this device; on for this device (with the group setting below).
- **Enable**: asks `Notification.requestPermission()` only on the click; denied → the blocked explanation, no subscription; granted → fetch the key (`GET /push/key`), `pushManager.subscribe({ userVisibleOnly: true, applicationServerKey })`, `POST /push/subscriptions`, then show "On for this device".
- **Send test**: `POST /push/test`; 202 → "Test notification queued"; 409 → "Enable notifications first"; 429 → "Too many tests, try again in a minute".
- **Disable**: `subscription.unsubscribe()`, then `DELETE /push/subscriptions` with the endpoint; a 404 is success; the state returns to "not enabled".
- **Group setting** (`fieldset`/`legend`, radio group): All group messages / Mentions only / Off; the checked value is `me.push_groups ?? "mentions"`; changing it calls the existing `useUpdateMe` mutation with `push_groups` (the server answers the account and the cached `Me` updates).
- Accessibility: a real `fieldset`/`legend`, buttons with text, `role="status"` for the state line, no permission prompt on mount, keyboard reachable; the existing route a11y test covers the page.
- i18n: keys in `web/src/lib/i18n/en.ts` and `pt-BR.ts` (`push.*`), so the language parity test stays green.

### Manifest, icons and installation

- `web/public/manifest.webmanifest`:

```json
{
  "name": "partyline",
  "short_name": "partyline",
  "description": "Conversations with people and AIs",
  "start_url": "/",
  "scope": "/",
  "display": "standalone",
  "background_color": "#fafafa",
  "theme_color": "#fafafa",
  "icons": [
    { "src": "/icons/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any" },
    { "src": "/icons/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any" }
  ]
}
```

  The theme/background are the light `--background` (`#fafafa`, DESIGN.md Colour tokens); a manifest has one theme colour and the page's two `theme-color` metas (light `#fafafa`, dark `#0f0f0f`) stay as they are. The icon is the favicon's mark: indigo `#465097` rounded square with three `#fafafa` rules (DESIGN.md rule 6). 192 and 512 satisfy the install criteria (a maskable variant is a later refinement, not needed for installation).
- `web/index.html`: `<link rel="manifest" href="/manifest.webmanifest" />` and `<link rel="apple-touch-icon" href="/icons/icon-192.png" />` next to the existing favicon/theme metas.
- Icons are committed PNGs produced by `scripts/genicons/main.go`, a standard-library drawing of the same mark (no rasterizer is assumed on the build machine). The generator is run once; it is not part of `make check` and not imported by the application.
- **Serving and CSP**: `internal/webui.New` serves `dist/` as-is. Go's `mime` table does not know `.webmanifest`, so `internal/webui` calls `mime.AddExtensionType(".webmanifest", "application/manifest+json")` once (an `init` in `webui.go`) and a `webui` test asserts the header on `GET /manifest.webmanifest`; `http.ServeFileFS` then serves the icons as `image/png` and `sw.js` as `text/javascript`. The CSP (`internal/webui/webui.go:30-36`) has `default-src 'self'`, which covers the `worker-src` and `manifest-src` fallbacks; no new directive is needed. Static JS under `/` is served without CSP headers, so `register("/sw.js")` works. No `script-src` change: the worker is a same-origin script, not inline.

## Tests

The tests derive from the spec's acceptance criteria; they are written before the code. The case values below are the expected outcomes.

| Layer | Tests | Expected values |
| --- | --- | --- |
| Migration/store (`internal/store/push_migration_test.go`) | 0025 columns, default `mentions` for an account that existed before 0025, unique endpoint, session cascade, account cascade, CHECK on `push_groups`, target queries with read cursors | `push_groups == "mentions"`; one row after two upserts of the same endpoint; 0 after the session/user is deleted; `unread` 1/0 per cursor; no row for an AI or a user without a subscription |
| VAPID (`internal/webpush/vapid_test.go`) | generate once, sealed file, mode, same public key on reload, corrupt file fails, wrong curve fails | public key 65 bytes, first `0x04`; file mode `0600`; the file does not contain the PKCS#8 DER; reload returns the same key |
| Crypto (`internal/webpush/encrypt_test.go`, `jwt_test.go`) | RFC 8291 vector; invalid point; JWT header/claims/signature; VAPID authorization | the exact expected body in this design; `exp-now == 12h`; `aud == "https://push.example.net"`; `k` == public key; ES256 verification passes |
| Client (`internal/webpush/client_test.go`) | request shape, statuses, network error | `POST`, `Content-Encoding: aes128gcm`, `TTL: 3600`, `Authorization` starts `vapid t=` and contains `,k=<public>`; returns 201/410/500; a closed server yields an error |
| Decision (`internal/webpush/decide_test.go`) | the matrix of the design; mention boundaries; preview limits; unknown mode | DM+off → true; group+mentions+mention → true; group+mentions+no mention → false; group+off+mention → false; own → false; unread=false → false; `@ana-2` does not mention `ana`; 121 a's → 119 a's + `…` |
| Worker (`internal/webpush/worker_test.go`) | queue bound, panic recovery, stop on cancel, limiter | `Submit` false when the queue is full; the job after a panic runs; `Run` returns nil on cancel; 10 allowed, 11th false, 60 s later true |
| Notifier (`internal/webpush/notifier_test.go`, `package webpush_test`, testkit) | DM, group all/mentions/off, own, system, read cursor, multiple devices, rate limit, `EnqueueTest`, `Lagged` | first DM → 1 send (Bob), own → 0; group default "hello" → 0, "@bob hello" → 1; off + mention → 0; 11 messages → 10 sends; 2 devices → 2 sends for one slot; read before `Run` → 0 |
| Delivery (`internal/webpush/delivery_test.go`) | 201/404/410/500/network; log privacy | 201 sets `last_ok_at`; 404 and 410 delete; 500 keeps; the log has the host and status, never the endpoint path, text or keys |
| Routes (`internal/api/push_handlers_test.go`) | key, subscribe, unsubscribe, test, setting; auth matrix; validation | 401 anonymous, 403 agent token, 200/204/202, 422 fields, 404 not-owned, 409 no subscription, 429 after 3 tests, `PATCH /me` persists `off` |
| Web (`web/src/routes/_app/settings.test.tsx`, `web/src/lib/push/*.test.ts`) | enable/deny/unsupported, test, disable, group setting, re-register, SW helpers, manifest | `requestPermission` once; `POST` body with endpoint + keys; `DELETE` on disable; `PATCH /me {push_groups:"off"}`; no request when permission is denied; manifest has 192/512 icons |
| Service worker (`web/src/lib/push/sw.test.ts`) | push/show/click; focused same path; focused other path; no client; malformed data | for the focused same conversation `showNotification` is called with `silent: true` and the notification closed at once (never zero notifications, PUSH-17); a normal one otherwise; click focuses+navigates, or opens a window |
| E2E (`web/e2e/push.spec.ts`) | settings flow, manifest/SW served, 360 px | 200 responses from the subscription and setting routes; the group setting survives a reload; no horizontal scroll at 360 px |

The push service in tests is an `httptest.Server` (or the injectable `Sender` for the notifier) that records requests and returns the status under test. The notifier integration tests use `testkit.New` with `WithoutPush` (`app.Options.DisablePush`) so the app does not run its own notifier; the test constructs one on the app's store/bus. No test talks to a real push service.

## Code Reuse Analysis

| Existing component | Location | How the feature uses it |
| --- | --- | --- |
| `secrets.LoadOrCreateKey` / `Box.Seal` | `internal/secrets/box.go:39-107` | The VAPID private key is sealed with the same box and written to a sibling file under the data dir, with the same 0600/O_EXCL pattern as `secret.key` |
| `events.Bus` and `MessageCreated` | `internal/events/bus.go` | The notifier subscribes with a buffer of 512 and consumes only `MessageCreated`; `Lagged` is ignored |
| The agent dispatcher | `internal/delivery/dispatcher.go:39-90` | The subscribe-before-`Run` pattern, the one-loop listener and the per-event DB read are copied; the push notifier adds the device/to-user fan-out and the queue |
| The agents' sliding-window limiter | `internal/agents/limit.go:31-61` | Copied into `webpush` (unexported, 30 lines) with 10/min notifications and 3/min test requests |
| The connectors' panic guard | `internal/connectors/guard.go:38-62` | Copied as `webpush/guard.go` with push wording: a panic is logged as its type and stack, never its value |
| `safehttp.NewClient` | `internal/connectors/safehttp/safehttp.go:136` | The push HTTP client: private/loopback destinations refused at dial time, no redirects, 4 KiB response cap, 10 s timeout |
| sqlc + migrations | `internal/store/migrations`, `internal/store/queries` | Migration `0025` and `push.sql`; the generated `db` package is the only data access |
| `testkit.New` and its options | `internal/testkit/testkit.go:137` | Migration/route/notifier tests use the real app on `httptest`; `WithoutPush` and `WithPushQueueSize` keep the background sender out of their way |
| `internal/webui` static serving | `internal/webui/webui.go:30-92` | Serves `sw.js`, the manifest and the icons from the embedded build with `no-cache` and the existing CSP; the only change is the `.webmanifest` MIME registration |
| Settings building blocks | `web/src/routes/_app/settings.tsx:60-75` | `Section`, `PageHeader`, `useUpdateMe`, the radio/fieldset pattern, `toast`, `mockApi`/`renderRoute` and the route a11y test |
| Vite `public/` and Playwright fixtures | `web/vite.config.ts`, `web/e2e/*` | The worker/manifest/icons are copied without a build step; the e2e reuses `startInstance`, `loginAs` and `expectNoSidewaysScroll` |

**Integration points**: the HTTP router/middleware (`internal/api`), the SQLite store (`internal/store`), the event bus (`internal/events`), the web app (`web/`) and the Playwright suite. The live socket is deliberately not an integration point (see Focus rule).

## Error Handling Strategy

| Scenario | Handling | What the user sees |
| --- | --- | --- |
| Invalid subscription body | 422 `invalid` with `endpoint`/`p256dh`/`auth`; nothing stored | The field is named; Settings shows a generic error |
| Endpoint of another account | 404 `not_found`; the row is untouched | "Already off" on disable (404 is treated as success) |
| No subscription for Test | 409 `conflict` | "Enable notifications on this device first" |
| More than 3 tests a minute | 429 `rate_limited` | "Too many tests, try again in a minute" |
| Queue full for Test | 503 `unavailable` | "Try again in a moment" |
| Queue full for a message | Drop the job, log at warn with a count only | Nothing; the message post is unaffected |
| Push service 404/410 | Delete the subscription, log status + host | The device stops receiving until enabled again |
| Push service 429/5xx/network | Keep the subscription, log status + host or the sanitized kind | Nothing; the next message may succeed |
| Corrupt stored subscription key | Log the error without content, keep the row, continue with the next job | Nothing |
| Corrupt/unreadable VAPID key file | New pair, stored subscriptions deleted, server starts (PUSH-26); browsers re-subscribe (PUSH-28) | Notifications pause until the next page load |
| Permission denied / unsupported browser | No subscription; Settings explains; no prompt loop | "Notifications are blocked" or "This browser cannot show notifications" |
| Service worker registration fails | Swallowed; Settings reports unsupported | The rest of the app works |
| Key fetch or subscription POST fails | The page unsubscribes the just-created local subscription and shows the error, so browser and server agree | "Could not turn on notifications; try again" |
| Database error | The existing 500 envelope (`internal/api/httpx`) | "Something went wrong" |

## Risks & Concerns

| Concern | Location (file:line) | Impact | Mitigation |
| --- | --- | --- | --- |
| The live socket cannot report the focused room | `internal/live/hub.go:168` (`ws.CloseRead`) | A notification may appear for the conversation the user is reading | Keep the rule in the service worker (focused window + exact path), which needs no protocol change; the race is documented in PUSH-17 |
| The bus drops events for a slow subscriber | `internal/events/bus.go` (`Lagged`) | A notification can be missed | Notifications are ephemeral; the notifier logs `Lagged` at debug and does not refetch old messages; the unread list is still correct |
| `store/db` is shared by all layers | `internal/store/db` | A future split of the store by layer would move these queries | The push SQL lives only in `push.sql`; the notifier reads no room service, so the move is mechanical |
| A subscription endpoint is user-controlled outbound traffic | `internal/webpush/client.go` | SSRF against the instance's network | `safehttp.NewClient` refuses private/loopback/link-local addresses at dial time and does not follow redirects |
| `http.Client` errors embed the endpoint URL | `internal/webpush/delivery.go` | The capability token in the path could reach a log | Never log the error; `sendErrorKind` reduces it to `network`/`timeout`/`canceled`, and the host is the only endpoint part logged |
| Losing `vapid.key` invalidates every subscription | `<data dir>/vapid.key` | Every stored subscription is lost | Back up the file with `secret.key`; a bad file is replaced and the subscriptions dropped (PUSH-26); browsers re-subscribe by themselves (PUSH-28) |
| Session cascade removes a device's subscription | `push_subscriptions.session_id` | After a logout the device is off until enabled again | `ensurePushSubscription` re-POSTs an existing browser subscription on the next signed-in load (no prompt); the user can also press Enable |
| iOS Safari requires an installed PWA and 16.4+ | Web platform | A user may see no notifications on an old iPhone | The manifest makes installation possible; Settings explains the unsupported state; the design does not promise iOS delivery |
| The preview is visible on a locked screen | OS notification settings | Anyone holding the phone can read 120 characters | The payload is the product decision; the OS owns lock-screen visibility; a per-device hide setting is out of scope |
| E2E cannot reach a real push service | `web/e2e` | The last mile (FCM/Mozilla) is not exercised in CI | The RFC vector, the fake push service and the client tests cover the protocol; the e2e covers the browser/server flow and the served files |
| `users.push_groups` is on a kernel table | `internal/store/migrations/0025` | A push setting on a shared table; one more column | AD-012 allows the `ADD COLUMN`; `users` is the kernel's account table, like `locale`; no table rebuild |
| Abandoned endpoints without a push attempt | `push_subscriptions` | A row may live until the session ends | The session cascade and explicit DELETE bound it; a retention sweep is not needed for a per-session row |

## Alternatives considered

| Alternative | Why not |
| --- | --- |
| `SherClockHolmes/webpush-go` v1.4.0 (MIT) | Maintained and compatible, but adds `golang-jwt/jwt/v5`, uses the deprecated `crypto/elliptic` ECDH, and hides encryption so our suite cannot pin the RFC vector (details above) |
| A server-side focus frame | Requires replacing `ws.CloseRead` and a per-user registry; the brief allows dropping the server rule, and the worker's URL check is exact and smaller |
| One notification per user, no devices | A user with a phone and a desktop wants both; the row is per endpoint by design |
| Tying subscriptions to the user only | A revoked/lost session must stop the previews; session cascade is the security answer |
| A durable outbox with retries | A notification is ephemeral; a dropped one is better than a stale one minutes later |
| Reusing `delivery.Wants` | It encodes AI names, handles, keywords and trust; users are mentioned by `@handle` only |
| A `push_preferences` table | One enum per account is one column on `users`, like `locale` |
| The `Topic` header instead of a `tag` | `Topic` collapses at the push service and can drop the *newest*; the notification `tag` collapses at the OS, after delivery |
| A local `showNotification` for Test | It proves nothing about encryption, VAPID or the push service; the test route exercises the real path |
| `vite-plugin-pwa` | A new build dependency for a 20-line classic worker and a static manifest; Vite already copies `public/` |

## Operations

- **Backup**: `<data dir>/vapid.key` must be backed up with `secret.key` and the database. Without it a new pair is made and the stored subscriptions are dropped (PUSH-26); browsers subscribe again with the new key on their next signed-in load (PUSH-28).
- **One instance**: the worker and the rate limiter are in-process; a second instance on the same database is not supported (AD-001 and `datalock`).
- **Logs**: `make check` does not run a real push; the worker's logs in production are `webpush: push sent/failed/expired` with `endpoint_host` and `status`.
- **Removal**: deleting a subscription is `DELETE`; deleting an account is the existing `DELETE /api/v1/me`, which cascades. There is no admin view of other users' subscriptions.

## Decision record (proposed)

When the plan is approved, record one AD in `.specs/STATE.md` (AD-027, the next free number): *web push is an app-layer feature (`internal/webpush`), the VAPID key is a sealed file under the data dir, subscriptions belong to a session, and the focus rule is enforced by the service worker, not the live socket.* The docs task (T11) adds it.

## Open questions

See [spec.md](spec.md#open-questions-for-the-product-owner): the three `Confirmed? n` defaults (group `off` vs mentions, `@handle` only, and the end-to-end Test) are the only ones awaiting the owner.
