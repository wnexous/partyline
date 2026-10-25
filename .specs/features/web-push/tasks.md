# Web Push Notifications Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Spec**: `.specs/features/web-push/spec.md`
**Design**: `.specs/features/web-push/design.md`
**Status**: Draft, awaiting approval

The product owner's brief fixes the behaviour (see the spec's Problem Statement). The design settles the
ten questions the brief left to it: the sealed VAPID file, the standard-library RFC 8291 implementation,
migration `0025`, the app-layer bus worker, the client-side focus rule, the payload, privacy, the web
service worker/manifest and every route's authorization.

Each task is one commit, at most about 300 changed lines and about 5 hand-written files, under an hour,
and is independently testable. Tests are written first and derive from the spec's acceptance criteria.
Generated files (`internal/store/db/*`, `web/src/lib/api/types.gen.ts`) are never hand-edited.

## Test Coverage Matrix

> Generated from the codebase, the project guidelines and the spec. Guidelines found: `AGENTS.md` (tests
> derive from the spec's ACs; `make check` before every commit; no message text or secrets in logs;
> synthetic fixtures), `.specs/LESSONS.md`, `Makefile`, `scripts/check.sh`, `web/vite.config.ts`,
> `web/playwright.config.ts`, existing tests (`internal/api/auth_handlers_test.go`,
> `internal/store/message_edit_migration_test.go`, `internal/webui/webui_test.go`,
> `web/src/routes/_app/settings.test.tsx`).

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| Pure rules (`Decide`, `Mentions`, `Preview`) | unit | Every branch of the matrix; one case per column that excludes; mention boundaries; 120-rune preview | `internal/webpush/decide_test.go` | `go test -count=1 ./internal/webpush/...` |
| Crypto, VAPID, client | unit | RFC 8291 section 5 vector; invalid point; JWT header/claims/ES256; request headers; 2xx/404/410/5xx | `internal/webpush/{encrypt,jwt,client}_test.go` | `go test -count=1 ./internal/webpush/...` |
| Store, migration `0025` | integration (real SQLite) | Every column/index/FK; the `mentions` default for an account that existed before; upsert; session/account cascade; target queries and read cursors | `internal/store/push_migration_test.go` | `go test -count=1 ./internal/store/...` |
| Notifier, worker, delivery | integration (real SQLite + fake sender) | DM/group/mentions/off/own/system/read/multi-device/rate; queue bound; panic; shutdown; 404/410 delete; log privacy | `internal/webpush/{notifier,worker,delivery}_test.go` | `make test` |
| HTTP routes and setting | e2e (testkit, real app on `httptest`) | 401/403/422/404/409/429/503 per route; upsert; ownership; `PATCH /me` persists `all`/`off` | `internal/api/push_handlers_test.go` | `make test` |
| Web Settings | unit (Vitest + Testing Library + vitest-axe) | Enable/deny/unsupported, test, disable, group setting, re-register; mocked `PushManager` and `Notification` | `web/src/routes/_app/settings.test.tsx` | `cd web && bun run test` |
| Service worker, manifest, registration | unit (happy-dom + `?raw`) | show/click/focus suppression; malformed payload; registration scope; manifest fields and icons | `web/src/lib/push/*.test.ts` | `cd web && bun run test` |
| Web end-to-end | e2e (Playwright, real binary, 1280 + 360 px) | Settings flow with a stubbed `PushManager`; manifest/SW/icons served; no horizontal scroll at 360 px | `web/e2e/push.spec.ts` | `make e2e` |
| Docs | none (build gate) | `docs/api.md` and `README.md` name every route, error and the key file | `docs/api.md`, `README.md` | `make check` |

## Gate Check Commands

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick | While iterating on a Go package | `go test -count=1 ./internal/webpush/...` or `./internal/store/...` |
| Quick (web) | While iterating on the web app | `cd web && bun run test` |
| Full | Before any integration/e2e change is done | `make test` |
| Build | **Before every commit** (AGENTS.md, L-029) | `make check` |
| E2E | T12 | `make e2e` |

Run `make generate` after touching SQL queries, migrations or `internal/api/dto`; commit the generated
files with the change. `make check` builds the web app, so `web/public/sw.js` and the manifest are copied
into `internal/webui/dist` for the build gate.

## Execution Plan

The phases are ordered. Tasks inside a phase run in order on one worker; phases inside one group are
independent and run in parallel worktrees. Each phase merges back before the next group starts.

| Group | Phases | Starts after |
| --- | --- | --- |
| G0 | Phase 1 (T1 and T2 in parallel) | — |
| G1 | Phase 2 (T3 and T4 in parallel) | G0 merged |
| G2 | Phase 3 (T5 → T6) | G1 merged |
| G3 | Phase 4 (T7) | G2 merged |
| G4 | Phase 5 (T8, T9 and T10 in parallel) | G3 merged |
| G5 | Phase 6 (T11 and T12 in parallel) | G4 merged |

**Merge rule for parallel worktrees.** Never hand-merge generated files (`internal/store/db/*`,
`web/src/lib/api/types.gen.ts`). After merging, run `make generate`, commit the result with the merge,
then run `make check`.

Dependency graph (every arrow is a `Depends on` of the task it points to):

```
T1 -> T5
T1 -> T7
T2 -> T3
T2 -> T4
T2 -> T5
T2 -> T7
T3 -> T5
T3 -> T6
T4 -> T5
T5 -> T6
T5 -> T10
T6 -> T7
T7 -> T8
T7 -> T9
T7 -> T11
T8 -> T11
T8 -> T12
T9 -> T11
T9 -> T12
T10 -> T11
T10 -> T12
```

### Phase 1: Store and keys

T1 and T2 share no hand-written file; they run in parallel worktrees. T1 lands the schema and queries
that T5 and T7 read; T2 creates the `internal/webpush` package and the layer-table entry that every
later Go task needs.

### Phase 2: Cryptography and decision

T3 (encryption, VAPID and the HTTP client) and T4 (the pure decision) both depend only on T2 and do not
depend on each other. They add different files to `internal/webpush`; merge both before Phase 3.

### Phase 3: The notifier and the send path

T5 adds the queue, limiter, guard and notifier (with an injected `Delivery`) and its integration tests;
T6 adds the real `Deliverer` (status handling, `last_ok_at`, 404/410, log privacy) and wires the notifier
into `app.New`/`app.Start`, with `app.Options.DisablePush` for tests.

### Phase 4: Routes and the account setting

T7 adds `POST /push/subscriptions`, `DELETE`, `GET /push/key`, `POST /push/test`, the `webpush.Service`
validation/authorization, and the `push_groups` field on `Me`/`PATCH /me`. It also adds the route tests.

### Phase 5: Web

T8 (Settings), T9 (manifest, icons, registration, `webui` MIME type) and T10 (service worker) can run in
parallel: T8 and T9 depend on T7, T10 depends on T5's payload shape. T9 and T10 touch disjoint web files.

### Phase 6: Docs and end-to-end

T11 documents the routes, the key file and the decision (docs are independent of the e2e). T12 drives the
real binary with Playwright: the Settings flow with a stubbed `PushManager`, the manifest/worker/icons
served from the root, and a 360 px layout.

---

## Task Breakdown

### Phase 1: Store and keys

#### T1: Push subscriptions and the group notification setting [store]

**What**: migration `0025_push_subscriptions.sql` with the `push_subscriptions` table, its three indexes
and `users.push_groups` exactly as in the design's [Data model](../web-push/design.md#data-model); the
sqlc queries in `internal/store/queries/push.sql` (`UpsertPushSubscription`,
`DeletePushSubscription`, `DeletePushSubscriptionByID`, `ListPushSubscriptionsByUser`,
`MarkPushSubscriptionDelivered`, `ListGroupPushTargets`, `ListDMPushTargets`) and `SetUserPushGroups` in
`internal/store/queries/users.sql`. Regenerate the sqlc output.

**Where**: `internal/store/migrations/0025_push_subscriptions.sql`
**Also**: `internal/store/queries/push.sql`, `internal/store/queries/users.sql`,
`internal/store/push_migration_test.go`
**Depends on**: None
**Requirement**: PUSH-03, PUSH-06, PUSH-07, PUSH-09
**Tests first** (`internal/store/push_migration_test.go`):
- `TestMigration0025AddsPushSubscriptionsAndTheSetting`: open before `0025`
  (`openMigratedBefore(t, "0025")`), insert a user and its intelligence, apply
  `0025_push_subscriptions.sql`; the user's `push_groups` is `"mentions"`; `push_subscriptions` has
  exactly `id, user_id, session_id, endpoint, p256dh, auth, created_at, last_ok_at`;
  `push_subscriptions_endpoint` refuses a second row for one endpoint; `UPDATE users SET push_groups =
  'none'` fails the CHECK, `'all'` and `'off'` succeed.
- `TestPushSubscriptionUpsertMovesTheEndpointToTheLatestUser`: two users and two sessions; the first
  upsert returns a row with `created_at = 100`; the second upsert of the same endpoint for the other
  user/keys returns the **same id**, one row, `user_id` of the second user, the new `p256dh`, and
  `last_ok_at` NULL.
- `TestPushSubscriptionsGoWithTheirSessionAndUser`: deleting the registering session removes the row;
  inserting it again and deleting the user removes it too.
- `TestGroupAndDMPushTargetsFollowTheReadCursor`: a group with users `u1`, `u2`, an AI `a1` and
  subscriptions for `u1`/`u2`; message `seq = 3`; read cursors `u1 = 2`, `u2 = 3`; the target rows are
  `u1` with `unread = 1` and `u2` with `unread = 0`, no AI row. Remove `u2`'s subscription: no `u2` row.
  Disable `u2`: no `u2` row. A DM `u1`/`u3`: two rows with their handles, both `unread` from their own
  cursors.
- `TestPushTargetsSkipUsersWithoutASubscription` and the `push_groups` value is returned on every row.

**Done when**:
- [x] the migration test above passes on a database that existed before `0025` and on a fresh one
- [x] `make generate` ran and the sqlc output is committed
- [x] no Go hook is added (the migration is pure SQL)
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(store): add push subscriptions and the group notification setting`

#### T2: Generate and seal the VAPID key pair [webpush]

**What**: the new `internal/webpush` package (app layer), `VAPIDKeyFile = "vapid.key"`,
`LoadOrCreateVAPID(dataDir string, box *secrets.Box) (*VAPID, error)` and the base64url public key, as in
the design's [VAPID keys](../web-push/design.md#vapid-keys). Add `"webpush"` to the `app` list in the
architecture table, load the key in `app.New` next to the secret key, and store it on `App.VAPID`. The
private key is PKCS#8 DER sealed with the instance box, written base64 with mode 0600 and `O_EXCL`;
an existing valid file is opened and parsed, never regenerated; a wrong-curve, corrupt or unreadable file
is logged at warn (no content), replaced with a new pair, and every `push_subscriptions` row is deleted;
startup always continues (PUSH-26). Test: a corrupt file → a new valid key, the rows gone, no error.

**Where**: `internal/webpush/vapid.go`
**Also**: `internal/webpush/vapid_test.go`, `internal/archtest/layers_test.go`, `internal/app/app.go`
**Depends on**: None
**Requirement**: PUSH-01, PUSH-26
**Tests first** (`internal/webpush/vapid_test.go`, `package webpush_test`: it uses `testkit` and only the
exported API is needed):
- `TestVAPIDKeyIsGeneratedOnceAndSealed`: two calls with the same directory return the same public key;
  the file exists with mode `0600`; decoding the file and opening it with the same box yields a
  `*ecdsa.PrivateKey` on P-256; the raw file bytes do not contain the PKCS#8 DER or the base64 of the
  private scalar.
- `TestVAPIDPublicKeyIsAnUncompressedPoint`: base64url-decoding `Public` yields 65 bytes with the first
  byte `0x04` and `ecdh.P256().NewPublicKey` accepts it.
- `TestVAPIDReplacesACorruptFileAndDropsSubscriptions`: writing `"not-base64"` (or sealing a P-384 key)
  with a stored subscription makes the loader return a new valid key, replace the file and delete the
  subscription row; it never stops startup (PUSH-26).
- `TestTheAppLoadsTheVAPIDKey`: `testkit.New(t)` starts and `env.App.VAPID.Public` decodes to 65 bytes;
  `<data dir>/vapid.key` exists. Restarting the env (`env.Restart()`) returns the same public key.
**Done when**:
- [x] `internal/archtest` passes with `webpush` in the app layer and the allow-list untouched
- [x] a corrupt key file replaces the pair and drops the subscriptions instead of stopping `app.New`
      (the tests cover it)
- [x] Gate check passes: `make check`
**Tests**: unit + integration
**Gate**: build
**Commit**: `feat(webpush): generate and seal the VAPID key pair`

### Phase 2: Cryptography and decision

#### T3: Encrypt payloads and sign VAPID requests [webpush]

**What**: RFC 8291 encryption (`Encrypt` with injected key/salt for the vector and `EncryptRandom` for
production), RFC 8292 JWT/`Authorization`, and the HTTP push client, exactly as in the design's
[Encryption and VAPID](../web-push/design.md#encryption-and-vapid). `Subscription{ID, Endpoint, P256dh,
Auth}` and the `Sender` interface live in `client.go`; `Client.Send` returns the HTTP status. Every
payload is at most 4096 octets; the request has `TTL: 3600` and `Urgency: normal`. The production
requester is `safehttp.NewClient` (`internal/connectors/safehttp`) with a 10 s timeout and a 4 KiB
response cap: private/loopback destinations and redirects are refused (CON-13's rule, no SSRF); tests
inject a `Doer`.

**Where**: `internal/webpush/encrypt.go`
**Also**: `internal/webpush/jwt.go`, `internal/webpush/client.go`, `internal/webpush/encrypt_test.go`,
`internal/webpush/jwt_test.go`, `internal/webpush/client_test.go`
**Depends on**: T2
**Requirement**: PUSH-11
**Tests first**:
- `TestEncryptMatchesRFC8291Section5`: the exact inputs and expected body from the design's pinned vector
  table (UA public `BCVx…`, auth `BTBZ…`, AS private `yfWP…`, salt `DGv6…`, plaintext "When I grow up, I
  want to be a watermelon"); `base64.RawURLEncoding.EncodeToString(body)` equals the 145-octet expected
  string. This must fail before the implementation.
- `TestEncryptRejectsAnOffCurveOrWrongLengthKey`: 65 zero bytes and a 64-byte key both return an error,
  and the error text contains no key bytes.
- `TestVAPIDJWTCarriesTheHeaderClaimsAndSignature`: header decodes to `{"typ":"JWT","alg":"ES256"}`;
  claims `aud = "https://push.example.net"`, `sub = "https://partyline.example.com"`, `exp - now = 12h`;
  `ecdsa.Verify` with the public key accepts the raw `r||s` signature.
- `TestVAPIDAuthorizationHeader`: `Authorization` is exactly `vapid t=<jwt>,k=<public>`; the `k` value is
  the 65-byte public point.
- `TestClientSendsTheRFC8291Request`: an `httptest.Server` (through an injected `Doer`, so the
  test does not need the private-network exception) records `POST`, `Content-Encoding: aes128gcm`,
  `TTL: 3600`, `Urgency: normal`, an `Authorization` starting `vapid t=` and containing `,k=<public>`,
  and a non-empty body; the client returns 201.
- `TestClientReturnsThePushServiceStatus`: 404, 410 and 500 are returned as those codes without an
  error; a closed server returns a non-nil error.
**Done when**:
- [x] the RFC vector test passes with the exact expected string (no library call, no golden file)
- [x] no dependency is added to `go.mod`
- [x] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(webpush): encrypt payloads and sign VAPID requests`

#### T4: Decide who gets a notification [webpush]

**What**: the pure `Decide(mode, kind, authorIID, userID, unread, mentioned)`, `Mentions(text, handle)`
and `Preview(text)` from the design's [Who gets it](../web-push/design.md#who-gets-it), with the full
matrix. No I/O, no clock, no store.

**Where**: `internal/webpush/decide.go`
**Also**: `internal/webpush/decide_test.go`
**Depends on**: T2
**Requirement**: PUSH-08, PUSH-09, PUSH-10
**Tests first** (`internal/webpush/decide_test.go`):
- `TestDecideMatrix`: the design's table row by row: DM + `off` + unread → true; DM whose author is the
  user → false; group + `all` → true; group + `mentions` + mention → true; group + `mentions` + no
  mention → false; group + `off` + mention → false; unread false → false in every room; unknown mode →
  the `mentions` result.
- `TestMentions`: `"@ana"` → true; `"hi @ANA"` → true; `"(@ana)"` → true; `"@ana-2"` → false for
  `ana`; `"@anax"` → false; `"x@ana"` → false; `"@ana,"` → true; `""` handle → false.
- `TestPreview`: 120 `a`s stay 120 `a`s; 121 `a`s become 119 `a`s + `…`; `"a\n\nb\t c"` becomes
  `"a b c"`; leading/trailing whitespace is trimmed; `""` stays `""`.
**Done when**:
- [x] every row of the matrix is a case and each excluding column has a case where only it excludes
- [x] no import of `store`, `events`, `api` or `net/http` in the file
- [x] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(webpush): decide which unread messages notify`

### Phase 3: The notifier and the send path

#### T5: Run the notifier on the message bus [webpush]

**What**: the bounded worker (`worker.go`: queue 256, 4 goroutines, non-blocking `Submit`, panic guard,
stop on context cancel), the sliding-window `limiter.go` (10/min notifications), `guard.go` (connector
style, never logs the panic value), and `notifier.go` (subscribe to `events.MessageCreated`; load room,
`message_views` and the target rows; skip system notices, own messages and read messages; apply
`Decide`/`Mentions`/`Preview`; rate-limit once per user; build the payload; submit one job per
subscription through an injected `Delivery`; `EnqueueTest` for the route). Add `app.Options.DisablePush`
and `testkit.WithoutPush()` so tests can keep the app's future notifier stopped.

**Where**: `internal/webpush/notifier.go`
**Also**: `internal/webpush/worker.go`, `internal/webpush/limiter.go`, `internal/webpush/guard.go`,
`internal/webpush/notifier_test.go`, `internal/webpush/worker_test.go`, `internal/webpush/limiter_test.go`,
`internal/app/app.go`, `internal/testkit/testkit.go`
**Depends on**: T1, T2, T3, T4
**Requirement**: PUSH-08, PUSH-09, PUSH-10, PUSH-13, PUSH-14, PUSH-23, PUSH-24, PUSH-27
**Tests first** (`internal/webpush/notifier_test.go` in `package webpush_test` — it uses `testkit` — and
`worker_test.go`/`limiter_test.go` in `package webpush` for the unexported worker/limiter; the notifier
test constructs it on `env.App.Store`/`env.App.Bus` with a fake `Delivery` that records `(Subscription,
payload)`):
- `TestNotifierNotifiesADM`: Alice DMs Bob; both subscribed; Alice posts "hi"; exactly one delivery, to
  Bob, with `title = "Alice"`, `author = "Alice"`, `preview = "hi"`, `url =
  <public>/rooms/<id>`, `tag = "room:<id>"`, `v = 1`; Alice's own message produces none.
- `TestGroupModes`: group with Alice, Bob and an AI; Bob `mentions`: "hello" → 0, "@bob hello" → 1;
  Bob `all`: "hello" → 1; Bob `off`: "@bob hello" → 0; a DM to Bob still → 1 with `off`.
- `TestReadCursorSuppresses`: construct the notifier (it subscribes), post, `MarkRead`, then start
  `Run`; 0 deliveries.
- `TestSystemNoticeIsSkipped`: a `MessageCreated` of a system message produces 0 deliveries.
- `TestDeletedMessageIsSkipped`: construct the notifier, post, delete the message, then `Run`; 0
  deliveries (the event's view has `deleted_at`).
- `TestRateLimitPerUser`: 11 messages in the same minute → 10 deliveries; the 11th is dropped.
- `TestMultipleDevicesShareOneRateSlot`: Bob has 2 subscriptions; one message → 2 deliveries; messages
  2..10 → 18 more; the 11th → 0 more (20 total).
- `TestEnqueueTest`: two subscriptions → `EnqueueTest` returns 2 and records the fixed test payload
  (`title "partyline"`, `preview "Notifications are on"`, `tag "test"`).
- `TestNotifierIgnoresLagged`: publishing `events.Lagged{}` does not panic and does not deliver.
- `TestWorkerQueueIsBounded`, `TestWorkerRecoversAPanicAndKeepsRunning` (panic value `"secret text"`
  never appears in the log), `TestWorkerStopsOnCancel`, `TestLimiterSlidingWindow` (10 allowed, 11th
  false, 60 s later true; another key unaffected).
**Done when**:
- [x] the bus is subscribed in the constructor, before `Run` (a message posted between the two is not
      missed); `Close()` cancels it even when `Run` never starts
- [x] `Submit` never blocks the bus consumer; a full queue is counted and dropped without content
- [x] `app.Options.DisablePush` is wired through `testkit.WithoutPush()` (the app's own notifier starts
      only in T6)
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(webpush): run the notifier on the message bus`

#### T6: Deliver jobs and wire the notifier into the app [webpush]

**What**: `delivery.go` (`Deliverer` implements the notifier's `Delivery`): call the `Sender`; on 2xx
set `last_ok_at`; on 404/410 delete the row; on anything else keep it; log only `endpoint_host` and
`status`, and map network errors to `network`/`timeout`/`canceled` (never the raw `*url.Error`, which
contains the endpoint). Wire it in `app.New`/`app.Start`/`Shutdown` exactly as the design says
(`App.Push`, `a.background(bg, "webpush", …)`, `DisablePush` checked in `Start`). Add
`Options.PushQueueSize` (default 256) and `testkit.WithPushQueueSize(n)`. The `Client` uses the 10 s
`safehttp` client.

**Where**: `internal/webpush/delivery.go`
**Also**: `internal/webpush/delivery_test.go`, `internal/app/app.go`, `internal/testkit/testkit.go`
**Depends on**: T3, T5
**Requirement**: PUSH-12, PUSH-14, PUSH-27
**Tests first**:
- `TestDelivererMarksLastOKOn2xx` (real store, one row, fake sender returns 201): `last_ok_at` is the
  injected clock's milliseconds, the row still exists.
- `TestDelivererDeletesOn404And410`: 404 deletes the row; 410 deletes the row; `last_ok_at` stays NULL.
- `TestDelivererKeepsOnOtherFailures`: 429, 500 and a network error keep the row and leave `last_ok_at`
  NULL.
- `TestDelivererLogsOnlyStatusAndHost`: capture `slog` in a buffer; endpoint
  `https://push.example.test/send/very-secret-token`, payload `"a secret message"`; the log contains
  `push.example.test` and `410`, and contains neither `very-secret-token` nor `a secret message` nor
  `https://`.
- `TestAppStartsThePushNotifier`: `testkit.New(t)` gives `env.App.Push != nil`; posting a message with no
  subscription causes no send and no error. `testkit.New(t, testkit.WithoutPush())` leaves the notifier
  constructed but not running (no delivery after a post).
**Done when**:
- [ ] `app.New` loads the VAPID key and builds the notifier (subscribed before the server accepts
      requests); `app.Start` runs it; `app.Shutdown` cancels and waits
- [ ] `DisablePush` is honoured (the app does not run it, but the field is usable by the routes' tests)
- [ ] `Notifier.Close()` cancels the bus subscription and is called from `App.Shutdown`
- [ ] `PushQueueSize` reaches `webpush.NewNotifier` and `testkit.WithPushQueueSize(1)` makes the queue
      hold one test job (used by T7's 503 case)
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(webpush): deliver push jobs and run the notifier`

### Phase 4: Routes and the account setting

#### T7: Serve the push routes and the group setting [api]

**What**: `webpush.Service` (`Key`, `Subscribe`, `Unsubscribe`, `Test` with validation and the 3/min test
limiter; stored on `App.PushService`, while `App.Push` stays the notifier), the DTOs in `internal/api/dto/push.go` (`PushKey`, `PushSubscriptionRequest`,
`PushSubscription`, `PushUnsubscribeRequest`, `PushTest`), the handlers in `internal/api/push_handlers.go`,
`pushRoutes` in `router.go`, `api.Deps.Push`, and the `push_groups` field on `Me`/`UpdateMeRequest` with
its users-service validation and `PATCH /me` wiring. Map `ErrNoSubscriptions` to 409, `ErrRateLimited` to
429 and `ErrBusy` to 503 in `mapError`. Regenerate the TypeScript types.

**Where**: `internal/webpush/service.go`
**Also**: `internal/webpush/service_test.go`, `internal/api/push_handlers.go`,
`internal/api/push_handlers_test.go`, `internal/api/dto/push.go`, `internal/api/dto/users.go`,
`internal/api/router.go`, `internal/api/auth_handlers.go`, `internal/users/profile.go`
**Depends on**: T1, T2, T6
**Requirement**: PUSH-02, PUSH-03, PUSH-04, PUSH-05, PUSH-06, PUSH-15
**Tests first** (`internal/api/push_handlers_test.go`, `testkit.New(t, testkit.WithoutPush())` so the
queued test sends never leave the process):
- `TestPushKey`: anonymous → 401; agent token → 403; Bob → 200 and `public_key` base64url-decodes to 65
  bytes starting `0x04`.
- `TestSubscribeStoresAndUpserts`: Alice's first POST → 200 with an id; the same endpoint again with new
  keys → 200, same id, one row; the stored `p256dh` is the new value.
- `TestSubscribeValidation`: `http://…` → 422 `invalid` with field `endpoint`; `p256dh` with 64 bytes →
  422 on `p256dh`; 65 zero bytes (off-curve) → 422; `auth` with 15 bytes → 422 on `auth`; no value is
  echoed back; a padded base64url key is accepted and the stored value has no `=` padding.
- `TestUnsubscribeOnlyTheOwners`: Alice stores an endpoint; Bob's DELETE → 404 and Alice's row remains;
  Alice's DELETE → 204 and the row is gone; a second Alice DELETE → 404.
- `TestPushRoutesNeedAUser`: every route → 401 anonymous, 403 with an agent token.
- `TestTestPush`: Alice with 2 subscriptions → 202 `{"queued":2}`; Alice with 0 → 409 `conflict`;
  the 4th call in a minute → 429 `rate_limited`.
- `TestServiceTestRouteStates` (`internal/webpush/service_test.go`): a real store with one subscription
  and a fake queuer returning 0 → `ErrBusy` (the route's 503); a fake returning 2 → 2; a user without a
  subscription → `ErrNoSubscriptions`; the 4th call in a minute → `ErrRateLimited`.
- `TestTestPushQueueFull` (`testkit.New(t, testkit.WithoutPush(), testkit.WithPushQueueSize(1))`):
  Alice with one subscription; the first `POST /push/test` → 202 `{"queued":1}` (the job stays in the
  queue because the worker is stopped); the second → 503 `unavailable`. This is the HTTP-level test of
  the sentinel error (L-017).
- `TestGroupPushSetting`: `GET /me` has `push_groups: "mentions"`; `PATCH /me {"push_groups":"off"}` →
  200 and the value persists; `"none"` → 422 on `push_groups`; an agent token PATCH → 403.
**Done when**:
- [ ] every route's success, 401, 403, 422/404/409/429/503 is asserted at its status and code
- [ ] the DELETE body is used (not a path parameter) and only the caller's row is removed
- [ ] `make generate` ran and the TypeScript types are committed
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): serve the push routes and the group notification setting`

### Phase 5: Web

#### T8: Add "Notifications on this device" to Settings [web]

**What**: the Settings section from the design's [Settings](../web-push/design.md#settings): state line,
Enable (permission only on click, key fetch, `pushManager.subscribe`, POST), Send test, Disable (local
`unsubscribe` + DELETE), the group radio group wired to `PATCH /me`, unsupported/denied states and i18n
keys in both dictionaries. Helpers in `web/src/lib/push/subscription.ts`; TanStack Query hooks in
`web/src/lib/api/queries.ts` (`usePushKey`, `useSubscribePush`, `useUnsubscribePush`, `useTestPush`).
The `PushManager`, `Notification` and `serviceWorker` are read behind small functions so Vitest can stub
them.

**Where**: `web/src/routes/_app/settings.tsx`
**Also**: `web/src/lib/push/subscription.ts`, `web/src/lib/api/queries.ts`, `web/src/lib/i18n/en.ts`,
`web/src/lib/i18n/pt-BR.ts`, `web/src/routes/_app/settings.test.tsx`, `web/src/test/push.ts`
**Depends on**: T7
**Requirement**: PUSH-18, PUSH-21, PUSH-22
**Tests first** (`web/src/routes/_app/settings.test.tsx` with `mockApi` and the new `web/src/test/push.ts`
fake):
- mount: "Mentions only" is checked (from `me.push_groups`), Enable is visible, `requestPermission` was
  not called.
- Enable with a granted permission: `requestPermission` called once; `subscribe` called with
  `{userVisibleOnly:true, applicationServerKey}` whose bytes decode from `/push/key`; `POST
  /push/subscriptions` body `{endpoint:"https://push.example.test/e2e", p256dh:"BCVx…", auth:"BTBZ…"}`;
  the state reads "On for this device".
- denied permission: the blocked sentence appears; no POST; Enable stays disabled.
- Test: `POST /push/test`; 202 → "Test notification queued"; 409 → the "enable first" sentence.
- a subscription POST that fails (500) calls `unsubscribe()` on the just-created local subscription and
  shows the error, so the browser and the server agree.
- Disable: `subscription.unsubscribe()` called; `DELETE /push/subscriptions` body `{endpoint}`; state
  back to not enabled.
- group setting: clicking "Off" sends `PATCH /me {"push_groups":"off"}` and keeps "Off" checked after the
  rerender; a failed PATCH shows an error and restores the previous radio.
- unsupported (no `PushManager`): explanation, no request, the rest of Settings still renders.
- the existing `a11y.test.tsx` route audit passes.
**Done when**:
- [ ] no permission prompt on mount (asserted)
- [ ] the disabled/denied/unsupported states each have a sentence and no broken action
- [ ] `bun run lint` and `bun run typecheck` are clean
- [ ] Gate check passes: `make check`
**Tests**: web unit
**Gate**: build
**Commit**: `feat(web): add notifications on this device to settings`

#### T9: Publish the PWA manifest and register the worker [web]

**What**: `web/public/manifest.webmanifest` and the two PNG icons from the design's
[Manifest, icons and installation](../web-push/design.md#manifest-icons-and-installation); the icon
generator `scripts/genicons/main.go` (standard-library drawing of the favicon mark, run once, not part of
the build); the manifest/Apple links in `web/index.html`; `web/src/lib/push/register.ts`
(`registerServiceWorker` from `main.tsx`, `ensurePushSubscription` from `_app.tsx`, which re-POSTs an
existing subscription only when permission is already granted and never prompts); and
`mime.AddExtensionType(".webmanifest", "application/manifest+json")` in `internal/webui` with a test for
the served header. The CSP is unchanged.

**Where**: `web/public/manifest.webmanifest`
**Also**: `web/public/icons/icon-192.png`, `web/public/icons/icon-512.png`, `scripts/genicons/main.go`,
`web/index.html`, `web/src/lib/push/register.ts`, `web/src/main.tsx`, `web/src/routes/_app.tsx`,
`internal/webui/webui.go`, `internal/webui/webui_test.go`, `web/src/lib/push/register.test.ts`
**Depends on**: T7
**Requirement**: PUSH-19, PUSH-20, PUSH-28
**Tests first**:
- `register.test.ts`: `registerServiceWorker` calls
  `navigator.serviceWorker.register("/sw.js", {scope:"/"})` once; a rejected registration is swallowed
  (no unhandled rejection); no call when `serviceWorker` is absent.
- `ensurePushSubscription`: permission `"granted"` + an existing fake subscription → one POST with its
  endpoint/keys; permission `"default"`/`"denied"` → no POST and `requestPermission` not called; no
  subscription → no POST; an existing subscription whose `applicationServerKey` differs from `GET /push/key` →
  `unsubscribe` then `subscribe` with the new key, one POST of the new subscription, `requestPermission` not
  called (PUSH-28).
- `manifest` (same test file, `?raw` import): parsing `web/public/manifest.webmanifest` gives `name
  "partyline"`, `start_url "/"`, `scope "/"`, `display "standalone"`, `theme_color "#fafafa"`, and icons
  with sizes `192x192` and `512x512` under `/icons/`.
- `internal/webui/webui_test.go`: a `webui` handler over a `fstest.MapFS` with `manifest.webmanifest`
  answers `Content-Type: application/manifest+json` and `/sw.js` answers `text/javascript` with
  `Cache-Control: no-cache`.
**Done when**:
- [ ] the two PNGs are committed and their dimensions are 192 and 512 (the generator prints/asserts them)
- [ ] the manifest is linked in `index.html` and the worker is registered with scope `/`
- [ ] no Go CSP change is needed (a test asserts the existing policy still serves HTML)
- [ ] Gate check passes: `make check`
**Tests**: web unit + Go unit
**Gate**: build
**Commit**: `feat(web): publish the PWA manifest and register the service worker`

#### T10: Show and open notifications from the service worker [web]

**What**: `web/public/sw.js`, a classic worker with `install`/`activate`/`push`/`notificationclick` as in
the design's [Service worker](../web-push/design.md#service-worker): parse the payload, suppress when a
focused window's path is the conversation, `showNotification` otherwise, and focus+navigate or
`openWindow` on click. No `fetch` handler, no cache, no inline code, no HTML rendering. Malformed or
missing payload data falls back to a generic notification and `/`.

**Where**: `web/public/sw.js`
**Also**: `web/src/lib/push/sw.test.ts`
**Depends on**: T5
**Requirement**: PUSH-16, PUSH-17
**Tests first** (`web/src/lib/push/sw.test.ts` imports the real file with `?raw` and executes it with a
fake `self` that captures the registered listeners; the test awaits the `waitUntil` promise):
- `push` with `{title:"Design", author:"Ana", preview:"green", url:"https://partyline.example/rooms/r1",
  tag:"room:r1"}` and no clients → `showNotification("Design", {body:"Ana: green", tag:"room:r1", …})`.
- a file-only message (`author:"Ana"`, `preview:""`) → body `"Ana"`.
- one client `{focused:true, url:"https://partyline.example/rooms/r1"}` → `showNotification` called once with `silent: true`, then that notification closed (never zero notifications for a push, PUSH-17).
- one focused client in `/rooms/r2` → shown (only the exact conversation is suppressed).
- a client in `/rooms/r1` but `focused:false` → shown.
- `click` with a client → `notification.close()`, `client.focus()`, `client.navigate(url)`; with no
  client → `clients.openWindow(url)`.
- `event.data = null` or invalid JSON → a notification with title `"partyline"` and url `"/"`, no throw.
- the worker source contains no `localStorage`, `indexedDB` or `caches` write (PUSH-22: the payload lives
  in memory for `showNotification` only).
**Done when**:
- [ ] the worker is a plain script (no imports, no `export`, no inline HTML) and passes `bun run lint`;
      the test's `new Function` carries the `biome-ignore lint/security/noGlobalEval` comment
- [ ] the suppression uses `client.focused` and the URL **path**, not the whole URL
- [ ] Gate check passes: `make check`
**Tests**: web unit
**Gate**: build
**Commit**: `feat(web): show push notifications in the service worker`

### Phase 6: Docs and end-to-end

#### T11: Document the push routes, keys and the decision [docs]

**What**: a "Web push notifications" section in `docs/api.md` (the five routes, caller/401/403/422/404/409/
429/503, the payload fields, the 120-character preview, the group setting, the privacy rule); `README.md`
names `vapid.key` next to `secret.key` in the data directory and lists the feature; `.specs/STATE.md`
records the proposed AD-027 (app-layer `internal/webpush`, sealed VAPID file, session-scoped
subscriptions, worker-enforced focus rule). No behaviour changes.

**Where**: `docs/api.md`
**Also**: `README.md`, `.specs/STATE.md`
**Depends on**: T7, T8, T9, T10
**Requirement**: PUSH-25
**Tests first**: none (documentation; `make check` is the gate). Verify by hand that every path and error
code in `internal/api/router.go` and `mapError` appears in `docs/api.md`, and that `README.md`'s data-dir
row mentions both key files.
**Done when**:
- [ ] every new route has its caller, success and error codes in `docs/api.md`
- [ ] `README.md` names `vapid.key` and the backup warning
- [ ] `.specs/STATE.md` gains AD-027 without changing any other decision
- [ ] Gate check passes: `make check`
**Tests**: docs (no code; `make check` only)
**Gate**: build
**Commit**: `docs(web-push): document the routes, the key file and the decision`

#### T12: Cover the push flow end to end [web]

**What**: `web/e2e/push.spec.ts` against the real binary: a desktop 1280 context and a 360 px context,
with `PushManager` stubbed (a fake subscription using the RFC 8291 UA key/auth and
`endpoint https://push.example.test/e2e`), permission granted, and the existing fixtures/helpers
(`startInstance`, `loginAs`, `apiCall`, `expectNoSidewaysScroll`). It drives the Settings section (enable
→ POST 200, group setting persists after reload, disable), fetches `/manifest.webmanifest`, `/sw.js`,
`/icons/icon-192.png` and `/icons/icon-512.png` directly, and checks the service worker is registered with
scope `/`.

**Where**: `web/e2e/push.spec.ts`
**Also**: none (reuse `web/e2e/fixtures.ts` and `web/e2e/helpers.ts`; a new helper belongs in this file if
it is only used here)
**Depends on**: T8, T9, T10
**Requirement**: PUSH-16, PUSH-17, PUSH-18, PUSH-19, PUSH-20, PUSH-21
**Tests first**:
- desktop: log in, open `/settings`, "Notifications on this device" is visible, "Mentions only" is
  checked; click Enable and wait for `POST /api/v1/push/subscriptions` → 200 and "On for this device";
  click "Off" and wait for `PATCH /api/v1/me`; reload and "Off" is still checked.
- files: `request.get("/manifest.webmanifest")` → 200 and JSON `name "partyline"` with 192/512 icons;
  `/sw.js` → 200, `text/javascript`, body contains `addEventListener("push"`; both PNGs → 200
  `image/png`; `navigator.serviceWorker.getRegistration()` → scope ends with `/`.
- 360 px: open Settings, `expectNoSidewaysScroll(page)`.
**Done when**:
- [ ] `make e2e` passes for both projects (desktop-1280 and mobile-360)
- [ ] the test makes no request to a real push service (the stubbed subscription is only stored)
- [ ] no existing e2e test is weakened or skipped
- [ ] `make check` still passes
**Tests**: e2e
**Gate**: e2e
**Commit**: `test(e2e): cover the push settings flow and the PWA files`
