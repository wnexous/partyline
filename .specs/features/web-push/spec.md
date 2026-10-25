# Web Push Notifications Specification

## Problem Statement

Partyline retired copies of AI direct messages to the owner's WhatsApp in v0.5 (AD-015, intelligences NOTI-01..03). Since then nothing tells a user that a message arrived while the web app is closed or in the background: the conversation list is only right when it is open, and a phone shows nothing at all.

The product owner wants phone and desktop notifications from the partyline web app itself (asked on 2026-10-23 and again on 2026-10-24): a browser subscription, a push when a message would be unread, a notification with a short preview, and a click that opens the conversation. This feature delivers that with the standard Web Push stack (VAPID + RFC 8291), a background sender fed by the event bus, and a settings section that makes a device's state explicit.

The decisions below are fixed by the product owner's brief:

- VAPID keys are generated once and stored like the other server secrets (sealed, under the data dir); `GET /api/v1/push/key` publishes the public key.
- A subscription is stored per user session/device; expired ones are deleted when the push service answers 404/410.
- DMs and mentions always notify; group messages follow a per-user setting (`all` / `mentions` / `off`, default `mentions`); the user's own messages never notify.
- The payload carries the room name, the author's display name, a preview of at most 120 characters and a URL to the conversation; no secrets.
- Sending is rate-limited per user and never happens in the request path.
- The web gets a service worker that shows the notification and opens the conversation, "Notifications on this device" in Settings, and the PWA basics (manifest, icons) so Android can install the app.
- Tests are written first: subscribe/unsubscribe authorization, the send decision matrix, the RFC 8291 test vector, deletion on 410, and the web settings flow with a mocked `PushManager`.

## Goals

- [ ] A user enables notifications on a phone once and receives DMs and mentions with a short preview; tapping opens the conversation.
- [ ] Group notifications follow the account setting (`all`, `mentions`, `off`; default `mentions`); own messages and system notices never notify.
- [ ] Every push is encrypted for the browser (RFC 8291, `aes128gcm`) and signed with VAPID (RFC 8292); the server reproduces the RFC 8291 section 5 vector.
- [ ] The push sender is a background worker with a bounded queue, a per-user rate limit, a panic guard and a clean shutdown; a push failure never affects posting a message.
- [ ] Message text, endpoint tokens and keys never reach a log; push-service failures are recorded as status code and endpoint hostname only.
- [ ] Expired subscriptions disappear; the app installs as a PWA on Android and the service worker is served from the root scope.

## Out of Scope

| Feature | Reason |
| --- | --- |
| A per-device or per-room notification setting | The brief asks for one group setting per user; the device is only where a subscription lives |
| Quiet hours, do-not-disturb, sounds, vibration or notification badges | Not asked; the operating system owns those |
| Notifications for edits, deletes, reactions, typing or presence | The brief is about new messages; the rest is noise |
| Rich notifications (images, inline reply, action buttons) | v1 is a text notification whose click opens the conversation |
| Email, SMS or WhatsApp fallbacks | AD-015 retired WhatsApp copies; the brief asks for the web app |
| Encryption of metadata for the push service | Web Push encrypts the payload to the browser only; the service sees the endpoint and ciphertext by design |
| VAPID key rotation, several instances or a shared data dir | AD-001: one binary, one data directory, one writer |
| A durable outbox or retries for push | A notification is ephemeral; dropping an undeliverable one is correct |
| Notification previews hidden as a setting | The preview is part of the brief; the operating system's lock-screen setting covers the rest |
| An audit entry for subscribe/unsubscribe | It is a device setting, not an administrative action |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| What "mentions always" means | A DM always notifies; a group message notifies in `all` for every message, in `mentions` only when it contains the user's `@handle`, and never in `off` (a group mention included); DMs ignore the group setting | The brief says "DMs and mentions always" and also "group messages per a user setting (all / mentions only / off)"; making `off` still deliver group mentions would make `off` equal `mentions` | y (coordinator, delegated by the owner, 2026-10-25) |
| What counts as a mention | `@<handle>`, case-insensitive, on a word boundary; not the display name, not keyword lists, not a reply | The composer inserts the @handle when the user picks a mention (MEN-03); the handle is unique and resistant to impersonation (AD-024) | y (coordinator, delegated by the owner, 2026-10-25) |
| Which messages are excluded | The recipient's own messages and system notices (loop-guard pauses, membership notices) | "Own messages never notify" is in the brief; a notice has no author to name and no useful preview | y |
| "Unread" | The message's sequence number is above the user's `read_cursors` row and above the room's `read_baseline`, exactly as `CountUnreadByRoom` computes it | Reuses the unread definition of the conversation list instead of inventing a second one | y |
| When unread is evaluated | When the background worker handles the `MessageCreated` event, not when the HTTP post returns | The worker is asynchronous; a message the user read in the meantime must not produce a notification | y |
| Conversation open and focused | The server does not know the focused room (the live socket never reads client frames); the service worker suppresses the notification when a focused app window's URL path is the conversation's path | The brief allows dropping the rule; this keeps its intent without a server protocol change. See the design | y |
| VAPID key storage | One P-256 key pair generated on first start, the private key sealed with the instance `secrets.Box` in `<data dir>/vapid.key` (mode 0600) | Reuses `internal/secrets` like connector secrets and the generated `secret.key`; the box already protects the data directory | y |
| Crypto implementation | A small standard-library implementation (`crypto/ecdh`, `crypto/hkdf`, `crypto/ecdsa`) pinned to the RFC 8291 section 5 vector | `go.mod` is on Go 1.27, which has everything needed; the maintained alternative adds a JWT dependency and uses the deprecated non-constant-time elliptic API (design, Alternatives) | y |
| Payload shape | JSON: conversation title, author display name, preview (at most 120 runes), absolute conversation URL, a per-room collapse tag and a version | The brief names these fields; the tag keeps one notification per conversation, the version lets the payload evolve | y |
| Subscription lifetime | One row per endpoint, tied to the session that registered it; deleting that session (logout, revoked device, password change) deletes it; deleting the account deletes them all | A revoked session must stop showing previews on a lost device; the browser re-registers an existing subscription on the next load | y |
| Where the group setting lives | `users.push_groups` (`all`, `mentions`, `off`; default `mentions`), read and written through `PATCH /api/v1/me` | One value per account, no second table; follows how `locale` and `default_org_id` already live on `users` | y |
| Rate limits | At most 10 notifications per user in a rolling minute; at most 3 test notifications per user in a rolling minute | A runaway group must not flood a phone; the test button is for setup, not for paging | y |
| Worker shape | One background worker subscribed to `events.MessageCreated`, a queue of 256 jobs, 4 send goroutines, a 10 s HTTP timeout, a recovered panic per job and a stop on context cancellation | The agent dispatcher already has this shape (`internal/delivery/dispatcher.go`); the queue keeps a slow push service away from the bus | y |
| Push-service failures | 404 and 410 delete the subscription; every other failure keeps it; only the status code and the endpoint hostname are logged | RFC 8030 says 404/410 are gone for good; other errors may be transient | y |
| Who may call the push routes | A logged-in user (session or user token); an agent token gets 403 everywhere outside `/api/v1/agent/`; an anonymous caller gets 401; `DELETE` touches only the caller's rows | Same boundary as the other user routes (`internal/api/middleware.go`) | y |
| Test notification | `POST /api/v1/push/test` enqueues a real push through the worker to every subscription of the caller, 3 a minute | A local `showNotification` would not exercise encryption, VAPID or the push service | y (coordinator, delegated by the owner, 2026-10-25) |
| Platform coverage | Web Push capable browsers: Chromium, Firefox, and Safari 16.4+ for an installed PWA; other browsers get an explanation, not a broken button | The PWA basics are only there to make Android installation reliable | y |
| Browser support states | Unsupported, permission not asked, granted and subscribed, granted but not subscribed, denied | Each state needs its own sentence and action in Settings | y |

**Open questions:** none blocking; the three rows marked `n` are adopted defaults with the product owner's confirmation pending (see "Open questions for the product owner" at the end).

---

## User Stories

### P1: The server can reach a browser ⭐ MVP

**User Story**: As a user, I want to enable notifications on my phone or desktop so that I hear about a message without keeping the app open.

**Acceptance Criteria**:

1. WHEN the server starts and `<data dir>/vapid.key` does not exist THEN the system SHALL generate one VAPID P-256 key pair, store the private key sealed with the instance secret key in that file with mode 0600 and reuse that pair on every later start. (PUSH-01)
2. WHEN an authenticated user calls `GET /api/v1/push/key` THEN the system SHALL answer 200 with `{"public_key": "<base64url uncompressed P-256 point>"}`; an anonymous caller SHALL get 401 and an agent token SHALL get 403. (PUSH-02)
3. WHEN a user calls `POST /api/v1/push/subscriptions` with an `https` endpoint, a 65-byte uncompressed P-256 `p256dh` and a 16-byte `auth` THEN the system SHALL store one row for that endpoint for the caller and the caller's session and SHALL answer 200 with the subscription; a second POST for the same endpoint SHALL update that row, not add another. (PUSH-03)
4. IF the subscription body has an endpoint that is not `https`, a malformed or off-curve `p256dh`, or an `auth` that is not 16 bytes THEN the system SHALL refuse with 422 `invalid` naming the field; an anonymous caller SHALL get 401 and an agent token SHALL get 403. (PUSH-04)
5. WHEN a user calls `DELETE /api/v1/push/subscriptions` with an endpoint THEN the system SHALL delete that row only when it belongs to the caller and SHALL answer 204; IF the endpoint belongs to another user or does not exist THEN the system SHALL answer 404 and delete nothing. (PUSH-05)
6. The system SHALL store the group notification setting per user as `all`, `mentions` or `off`, defaulting to `mentions` for every existing and new account, and SHALL let the user change it through `PATCH /api/v1/me` with `push_groups` (200 with the account; an unknown value SHALL be 422 `invalid` on the `push_groups` field). (PUSH-06)
7. WHEN a session is deleted (logout, a device revoked in Settings, a password change) THEN the system SHALL delete the push subscriptions registered by that session; WHEN an account is deleted THEN the system SHALL delete all its subscriptions. (PUSH-07)
8. WHEN a user calls `POST /api/v1/push/test` THEN the system SHALL enqueue one test notification for each subscription of the caller and answer 202 `{"queued": n}`; IF the caller has no subscription THEN 409 `conflict`; IF the caller asked more than 3 times in a rolling minute THEN 429 `rate_limited`; an anonymous caller SHALL get 401 and an agent token SHALL get 403. (PUSH-15)

**Independent Test**: Alice enables notifications on her phone: `GET /push/key` returns a 65-byte key, the `POST` stores one row, and `POST /push/test` answers 202. Bob's `POST` for the same endpoint replaces the owner of that row; Bob's `DELETE` for an endpoint he does not own is 404 and leaves Alice's row.

---

### P1: Notify the right person ⭐ MVP

**User Story**: As a user, I want a push only for messages I would see as unread, so that my phone does not buzz for my own messages or for chatter I muted.

**Acceptance Criteria**:

1. WHEN a message is created and a user would see it as unread THEN the system SHALL notify that user when it is a DM of that user, or when it is a group message and the user's setting is `all`, or when the setting is `mentions` and the message contains the user's `@handle`; the system SHALL NOT notify the author and SHALL NOT notify for a system notice. (PUSH-08)
2. The system SHALL NOT notify a user whose read cursor already covers the message's sequence number, nor for a message of a room whose read baseline is at or above it. (PUSH-09)
3. WHEN the system sends a notification THEN the payload SHALL carry the conversation title (the group name, or the other participant's name in a DM), the author's display name, a preview of at most 120 characters and the absolute URL of the conversation, and SHALL carry no other message data. (PUSH-10)
4. The system SHALL encrypt every payload for its browser subscription with `aes128gcm` as RFC 8291 defines and SHALL authenticate the request with a VAPID JWT as RFC 8292 defines; the encryption SHALL reproduce the RFC 8291 section 5 test vector. (PUSH-11)
5. WHEN the push service answers 404 or 410 THEN the system SHALL delete that subscription; WHEN it answers any other non-2xx status or the request fails THEN the system SHALL keep the subscription and record only the status code and the endpoint hostname. (PUSH-12)
6. The system SHALL send at most 10 notifications to one user in a rolling minute and SHALL drop the excess without delaying or failing the message post. (PUSH-13)
7. The system SHALL deliver notifications from one background worker subscribed to `events.MessageCreated`, with a bounded queue, a recovered panic per job and a stop on context cancellation; a push failure SHALL never fail the request that posted the message. (PUSH-14)
8. WHEN a message is edited or deleted, or a reaction changes THEN the system SHALL send no notification. (PUSH-23)
9. WHEN a user has several subscriptions THEN the system SHALL count the notification once against the per-user limit and SHALL send it to every subscription of that user. (PUSH-24)

**Independent Test**: In a group with the default setting, Bob writes "hello" and Alice gets nothing; Bob writes "@alice deploy is green" and Alice's phone shows the preview. Alice's own message produces nothing. A DM to Alice always produces one. Eleven messages in a minute produce ten sends. A push service answering 410 removes the row, and the next message does not retry it.

---

### P1: Notifications on this device ⭐ MVP

**User Story**: As a user, I want one place in Settings to turn notifications on, test them and turn them off, so that I know what this device will do.

**Acceptance Criteria**:

1. WHEN a push arrives THEN the service worker SHALL show a notification with the payload's conversation title, author and preview and, when the user activates it, SHALL focus an open window of the app and open the conversation, or open a new window when none is open. (PUSH-16)
2. IF a window of the app is focused and its URL path is the conversation of the push THEN the service worker SHALL show the notification silently (`silent: true`, no sound or vibration) and close it at once, so that no push ever ends without a notification (Safari revokes a subscription that receives pushes without one, and Chrome then shows a generic one). (PUSH-17)
3. The Settings page SHALL offer "Notifications on this device" with enable (which asks for permission only after the user presses it), test, disable and the group setting (all, mentions only, off), and SHALL show the state of this device. (PUSH-18)
4. WHEN the app loads for a signed-in user THEN it SHALL register the service worker at `/sw.js` with scope `/` and, when the browser already holds a push subscription and permission is granted, SHALL register that subscription with the current session without asking permission again. (PUSH-19)
5. The app SHALL publish a web app manifest with the name, `start_url` `/`, `display` `standalone`, the theme colour from DESIGN.md and 192 px and 512 px icons, and the Go server SHALL serve the manifest, the icons and the service worker from the root path under the existing CSP. (PUSH-20)
6. WHEN the browser cannot push (no service worker, no `PushManager`, permission denied, or an insecure origin) THEN the page SHALL explain it and SHALL leave the other settings usable; it SHALL NOT ask for permission on load. (PUSH-21)
7. The browser SHALL send the subscription's endpoint, `p256dh` and `auth` to the server and SHALL NOT keep message text in local storage, IndexedDB or a log. (PUSH-22)

**Independent Test**: On a phone, Settings shows "Notifications on this device"; pressing Enable asks once, creates the subscription, stores it on the server and changes the button to Disable; Test answers 202; Disable removes both. With the conversation open and focused, a push for it shows no notification. `/manifest.webmanifest` and `/sw.js` answer 200 from the root.

---

### P2: Documentation

**User Story**: As an operator, I want the push routes, the key file and the privacy rules written down so that I can run and back up the feature.

**Acceptance Criteria**:

1. The repository SHALL document the push routes, their callers and errors, the group setting, the VAPID key file and the privacy rules in `docs/api.md` and `README.md`. (PUSH-25)

**Independent Test**: `docs/api.md` lists all five push paths with their status codes and `README.md` names `vapid.key` next to `secret.key`.

---

## Edge Cases

- WHEN `<data dir>/vapid.key` is unreadable, corrupt or holds a key that is not P-256 THEN the server SHALL keep starting: it SHALL log a warning without the file's content, replace the file with a new pair, and delete every stored push subscription (they were bound to the old key); browsers re-subscribe on their next signed-in load (PUSH-28). Notifications SHALL never stop the server from starting. (PUSH-26)
- WHEN a signed-in page loads with notification permission granted AND the browser's subscription was made with a different `applicationServerKey` than `GET /push/key` returns THEN the web SHALL unsubscribe it, subscribe again with the current key (no permission prompt, it is already granted) and POST the new subscription. (PUSH-28)
- IF the push queue is full THEN the system SHALL drop the notification without affecting the message; a test request that queues nothing SHALL answer 503 `unavailable`. (PUSH-27)

---

## Implicit-requirement sweep

| Dimension | Covered by |
| --- | --- |
| Input validation & bounds | PUSH-04 (endpoint scheme, P-256 point, 16-byte auth); PUSH-06 (setting enum); PUSH-10 (120-rune preview); the payload size stays far below the 4096-octet record |
| Failure / partial-failure states | PUSH-12 (404/410 vs other failures); PUSH-26 (bad key file); PUSH-27 (full queue); a send failure never fails the post (PUSH-14) |
| Idempotency / duplicates | PUSH-03 (one row per endpoint, upsert); PUSH-05 (delete twice: the second is 404 and harmless); PUSH-24 (one notification per user, every device) |
| Auth boundaries & rate limits | PUSH-02, PUSH-04, PUSH-15 (401/403); PUSH-05 (ownership); PUSH-13 (10/min); PUSH-15 (3/min test) |
| Concurrency / ordering | The worker consumes the bus in order; the rate limiter is mutex-guarded; two send goroutines may deliver out of order, which a notification tolerates; the read cursor is read when the event is handled (PUSH-09) |
| Data lifecycle | PUSH-07 (session/account cascade); PUSH-12 (410 deletes); the VAPID key is backed up with the data dir (design) |
| Observability | Only status + endpoint hostname (PUSH-12); no text, endpoint path or keys (PUSH-22); no metrics in v1 |
| External-dependency failure | The push service is unreachable or slow: 10 s timeout, subscription kept, notification dropped, message post unaffected (PUSH-12, PUSH-14) |
| State-transition integrity | permission → subscription → server row → delete; a denied permission never creates a row; a session end removes the row (PUSH-07, PUSH-18, PUSH-21) |

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| PUSH-01 | P1: The server can reach a browser | Execute | Pending |
| PUSH-02 | P1: The server can reach a browser | Execute | Pending |
| PUSH-03 | P1: The server can reach a browser | Execute | Pending |
| PUSH-04 | P1: The server can reach a browser | Execute | Pending |
| PUSH-05 | P1: The server can reach a browser | Execute | Pending |
| PUSH-06 | P1: The server can reach a browser | Execute | Pending |
| PUSH-07 | P1: The server can reach a browser | Execute | Pending |
| PUSH-08 | P1: Notify the right person | Execute | Pending |
| PUSH-09 | P1: Notify the right person | Execute | Pending |
| PUSH-10 | P1: Notify the right person | Execute | Pending |
| PUSH-11 | P1: Notify the right person | Execute | Pending |
| PUSH-12 | P1: Notify the right person | Execute | Pending |
| PUSH-13 | P1: Notify the right person | Execute | Pending |
| PUSH-14 | P1: Notify the right person | Execute | Pending |
| PUSH-15 | P1: The server can reach a browser | Execute | Pending |
| PUSH-16 | P1: Notifications on this device | Execute | Pending |
| PUSH-17 | P1: Notifications on this device | Execute | Pending |
| PUSH-18 | P1: Notifications on this device | Execute | Pending |
| PUSH-19 | P1: Notifications on this device | Execute | Pending |
| PUSH-20 | P1: Notifications on this device | Execute | Pending |
| PUSH-21 | P1: Notifications on this device | Execute | Pending |
| PUSH-22 | P1: Notifications on this device | Execute | Pending |
| PUSH-23 | P1: Notify the right person | Execute | Pending |
| PUSH-24 | P1: Notify the right person | Execute | Pending |
| PUSH-25 | P2: Documentation | Execute | Pending |
| PUSH-26 | Edge cases | Execute | Pending |
| PUSH-28 | Edge cases | Execute | Pending |
| PUSH-27 | Edge cases | Execute | Pending |

**Coverage:** 27 total, 27 mapped to tasks (`tasks.md` T1-T12), status Pending (the plan is not executed yet).

---

## Success Criteria

- [ ] Alice enables notifications on her phone; Bob DMs her; the phone shows "Bob: ..." and tapping it opens the DM.
- [ ] In a group with the default setting, a message without `@alice` does not notify; a message with `@alice` does.
- [ ] Setting "Off" stops group notifications, including group mentions; DMs still notify.
- [ ] A push service answering 410 removes the subscription, and the next message does not retry it.
- [ ] The server logs of a delivered, a failed and an expired push contain no message text, no endpoint path and no key.
- [ ] On Android, the app offers "Install" and the installed app receives a push; on a browser without Web Push, Settings says so and the rest of the page works.

---

## Open questions for the product owner

1. **"Off" and group mentions.** The brief says "DMs and mentions always" and also "group messages per a user setting (all / mentions only / off)". We read the setting as governing all group messages, so `off` silences a group mention too; DMs are unaffected. Confirm, or say that a group mention must always get through even with `off`.
2. **What a mention is.** We count only `@<handle>` (the composer inserts the handle). Display-name mentions, keyword lists and replies do not notify. Confirm, or say which of those should also notify.
3. **What "test" tests.** We made Test send a real push through the server (one per subscription, at most 3 a minute), so it proves encryption, VAPID and the push service end to end. A local notification would be simpler but would not prove the pipeline. Confirm the end-to-end choice, and whether 3 a minute is the right cap.
