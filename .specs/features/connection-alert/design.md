# Connection-Down Push Alerts Design

**Feature**: `connection-alert` · **Spec**: [spec.md](spec.md) · **Tasks**: [tasks.md](tasks.md)
**Status**: Draft for approval · **Base**: `main` at `fca32dd` (v0.8.0)

## Answers to the brief's design questions

| Question | Answer |
| --- | --- |
| Where a connection's status change is decided and persisted | Each kind decides it and reports it through `Handler.Status(status, detail)` (`internal/connectors/connector.go:65-78`, `internal/connectors/manager.go:655-662`); the manager writes it in `Manager.persist` (`internal/connectors/manager.go:571-580`) and `Manager.report` is the redacting wrapper (`:563-569`). The WhatsApp check is `whatsapp.state`/`disconnectedDetail` (`internal/connectors/whatsapp/status.go:26-50`), reported from `watch` (`:63-78`) and at start (`internal/connectors/whatsapp/connector.go:200`). → [Where the status comes from](#where-the-status-comes-from) |
| Is there an event on the bus; if not, the smallest one to add | There already is one: `persist` ends with `m.d.Bus.Publish(events.ConnectorStatus{ConnectorID, Status, Detail})`, after the store commit. `UpdateConnectorStatus` is called from nowhere else in the tree, so **every** persisted status change publishes exactly one event. The smallest addition is therefore **no event**, no change to `connectors` and no migration. → [Where the status comes from](#where-the-status-comes-from) |
| Where the consumer lives (`internal/archtest`) | `internal/webpush/alerts.go`, inside the existing app-layer package (`internal/archtest/layers_test.go:34-38`), next to the notifier. It imports `internal/events`, `internal/store/db` and `internal/api/dto` (all kernel/infra); it imports no core package, creates no package, and leaves the core → app allow-list untouched (`internal/archtest/layers_test.go:60-67`). → [The alert consumer](#the-alert-consumer) |
| Who is responsible | `UNION` of the admins of `connectors.org_id` through `org_members` (`permission = 'admin'`, users only) and `agents.owner_id` of `connectors.operator_iid`; disabled accounts and users without a subscription are dropped; every distinct user once. `connectors.owner_id` is **not** used: it is a dead anchor that gives nothing since AD-019 and the organization is the owner. → [Who is responsible](#who-is-responsible) |
| The "once per outage, max 1 per 30 min" state | One in-memory `alertRule{down, seenUp, last}` per connection id, seeded at start from `connectors.status`; a connection never seen healthy in this process is a silent baseline; `connecting` never resets; `connected`/`stopped` clear `down`; `error` alerts only from `down = false`, `seenUp = true` and when `last` is 30 minutes old. A restart cannot flood: a connection stored in `error` starts as `down`, an empty map (seed failed) starts every connection unseen, and the boot burst (`connecting`, then `error`) is silent in both cases. → [The alert state](#the-alert-state) |
| The payload | The existing `webpush.Payload` (`internal/webpush/notifier.go:38-45`): `v = 1`, `title = "Connection down: <name>"`, `author = ""`, `preview = <fixed reason>`, `url = <public URL>/connectors/<id>`, `tag = "conn:<id>"`. `web/public/sw.js:56-59` already renders `preview` as the body when `author` is empty, and its click handler opens `data.url`; **no web change is needed**. → [Payload and reason](#payload-and-reason) |
| Every log line | `connector_id` and `status` only; a store failure adds its error as the notifier's error paths already do. Never the name, the reason, the platform detail, the URL, an endpoint or a key. → [Privacy](#privacy) |

## Overview

```
whatsapp / telegram / discord / webhook
   │  h.Status(error, detail)                       internal/connectors/connector.go:68
   ▼
connectors.Manager.persist                          manager.go:571-580
   ├─ store: connectors.status, connectors.last_error
   └─ events.ConnectorStatus{ConnectorID, Status, Detail}        events/bus.go:85-89
   ▼
webpush.Alerter.Run                                 internal/webpush/alerts.go
   ├─ GetConnector(id)            only for error: name, kind, enabled, operator (one row)
   ├─ alertRule.observe(status)   down? seen healthy? 30 min?   (memory)
   ├─ ListConnectorAlertTargets   org admins ∪ operator's host × devices
   ├─ alertPayload                title / reason / url / tag      (fixed JSON)
   └─ bounded worker (queue 64, 2 goroutines)
        ▼
webpush.Deliverer               internal/webpush/delivery.go:52-71
   ├─ 2xx     → last_ok_at
   ├─ 404/410 → DELETE the subscription
   └─ other   → keep it, no retry
        ▼
service worker → notification "Connection down: ops" → click → /connectors/<id>
```

Nothing in the manager or in the HTTP request path waits for the alert: `persist` publishes after its
commit and returns, and the consumer's `submit` never blocks (`internal/webpush/worker.go:44-53`). This
is the same shape as the message notifier (`internal/webpush/notifier.go:70-150`), whose bus
subscription already runs on the app.

## Where the status comes from

**The funnel.** A connector kind observes its platform and calls `Handler.Status`:

- `internal/connectors/connector.go:81-89` defines `stopped`, `connecting`, `connected` and `error`;
  the values match `dto.ConnectorStatus` (`internal/api/dto/connectors.go:41-50`). There is **no
  `disconnected` value**: the WhatsApp "number disconnected" state is `error` with the detail
  `the WhatsApp number is not connected (<disconnect_reason>); reconnect it in evolution-go`
  (`internal/connectors/whatsapp/status.go:11-50`, CID-28).
- `handler.Status` (`internal/connectors/manager.go:655-662`) forwards to `Manager.report`, which
  redacts the kind's secrets and refuses a stale supervisor (`:563-569`).
- `Manager.persist` (`:571-580`) is the only writer of `connectors.status`/`last_error`
  (`UpdateConnectorStatus`, `internal/store/queries/connectors.sql:43-45`); after the write it
  publishes `events.ConnectorStatus` (`internal/events/bus.go:85-89`).
- The supervisor reports `connecting` before every `Run` and `error` with `Run`'s failure after it
  (`internal/connectors/manager.go:496-519`), so one outage produces a `connecting`/`error` pair per
  backoff round (2 s to 120 s), plus an `error` for each failed start (`:334-368`) and a `stopped`
  when the owner disables the connection (`Stop`, `:379-385`, called by the service at
  `internal/connectors/service.go:610-622`).

**Why no new event is needed.** The consumer only needs the id, the status and the detail, which is
exactly the event. It reads the name, `kind`, `enabled` and `operator_iid` from `GetConnector`
(`internal/store/queries/connectors.sql:6-7`) with one query per alert decision (not per event). The
write happens before the publish, so the row and the event agree; the `enabled` re-check closes the
race with a disable that lands between them.

**Why not publish from the connectors package to an app callback.** That would make the core know the
messenger and break AD-025. The bus is the seam that already exists.

## Who is responsible

The brief names "the connection's owner and the user who owns the AI that operates it". In this code:

- **The owner is the organization.** `connectors.org_id` is the owning organization (ORG-02); `owner_id`
  is documented as "the creator and a dead anchor since AD-019"
  (`internal/store/queries/connectors.sql:1-5`), it "gives nothing"
  (`internal/api/dto/connectors.go:66-70`), and the web app deliberately names no owner
  (`web/src/routes/_app/connectors/connectors.test.tsx:700`). Only an admin of the organization adds,
  changes and disables a connection (`docs/agents.md:523-527`,
  `internal/connectors/service.go:285-292`); the admins are the people who can fix an outage.
- **The operator's host** is `agents.owner_id`, the user whose computer and agent token run the AI
  (AD-019; `internal/connectors.operator` validates the AI's home organization at
  `internal/connectors/service.go:625-648`; the operator column is
  `internal/store/migrations/0015_intelligences.sql:81-82`).

One query resolves both into devices (`internal/store/queries/push.sql`, new
`ListConnectorAlertTargets`):

```sql
WITH responsible (user_id) AS (
    SELECT m.iid
    FROM connectors c
    JOIN org_members m ON m.org_id = c.org_id AND m.permission = 'admin'
    WHERE c.id = sqlc.arg(connector_id)
    UNION
    SELECT a.owner_id
    FROM connectors c
    JOIN agents a ON a.id = c.operator_iid
    WHERE c.id = sqlc.arg(connector_id)
)
SELECT r.user_id, s.id AS subscription_id, s.endpoint, s.p256dh, s.auth
FROM responsible r
JOIN users u ON u.id = r.user_id AND u.disabled_at IS NULL
JOIN push_subscriptions s ON s.user_id = r.user_id
ORDER BY r.user_id, s.created_at, s.id;
```

- `UNION` (not `UNION ALL`) is "every distinct user once": an admin who also hosts the operator AI
  appears once.
- `JOIN users` keeps `org_members` rows that belong to AIs out (a user id is a `users` id); it also
  drops a disabled account, whose devices must not receive anything.
- `JOIN push_subscriptions` means a user with no device produces no row; every device of a
  responsible user produces one.
- `users.push_groups` is **not** joined: the alert follows the devices, not the group setting
  (ALERT-13). There is no new setting anywhere.
- A connector with `org_id` NULL (a row written before organizations) yields no admin; one with
  `operator_iid` NULL yields no host. Both are legitimate partial audiences.

A second query, `ListConnectorStatuses` (`SELECT id, status FROM connectors ORDER BY id`), gives the
consumer its start state; it is read once per start.

## The alert state

One `alertRule` per connection id, kept in a mutex-guarded map in the `Alerter`. It is memory only:
no table, no column, no migration.

```go
type alertRule struct {
    down   bool      // the connection is known to be failing
    seenUp bool      // it has been observed healthy in this process (or seeded healthy)
    last   time.Time // when the last alert was sent for it; zero = never
}
```

`observe(status string, now time.Time) bool` applies one event and reports whether to send; a
connection's first event runs on the zero value (never seen healthy, not failing):

| Status | Rule before | Result |
| --- | --- | --- |
| `connecting` | any | no change, no alert (the supervisor reports it before every `Run`, including every restart) |
| `connected` | any | `down = false`, `seenUp = true`, no alert (ALERT-03: no "restored" push) |
| `stopped` | any | `down = false`, no alert (ALERT-06: disabled on purpose) |
| `error` | `down = true` | nothing (ALERT-04: still the same outage) |
| `error` | `down = false`, `seenUp = false` | `down = true`, no alert (never seen healthy here: a baseline, not an outage this process witnessed) |
| `error` | `down = false`, `seenUp = true`, `last` less than 30 min ago | `down = true`, no alert (ALERT-05: a flapping connection stays quiet) |
| `error` | `down = false`, `seenUp = true`, `last` zero or 30 min ago or more | `down = true`, `last = now`, **alert** (ALERT-01, ALERT-11) |
| anything else | any | no change |

Constants: `alertGap = 30 * time.Minute`; the statuses come from `dto.ConnectorStatus*`, not from
string literals, so the alerter and the API name the same values.

**Seeding, and why a restart cannot flood.** `app.Start` calls `Alerter.Seed(ctx)` after the
migrations and **before** `a.ConnectorManager.Start(bg)`, so the map already knows every stored
status when the first event is published:

- a connection stored as `error` starts with `down = true`; its boot burst (`connecting`, then
  `error`, once per backoff) is silent, which is exactly "none while it keeps failing";
- a connection stored as `connected`, `connecting` or `stopped` starts `seenUp = true`, so its first
  `error` is a real new outage and alerts (ALERT-11) — including the case where it died while the
  server was restarting;
- an empty map (the seed read failed) or a connection created after start is unseen
  (`seenUp = false`), so its first `error` only records `down` and nothing is pushed until the
  connection reports `connected` at least once in this process. That is what keeps a failed seed from
  turning the boot burst into one push per failing connection.

The worst case of a restart is therefore **zero** alerts, never one per failing connection. The
30-minute stamp is memory too: it is lost on restart, but a connection that alerted shortly before a
restart is still `down` in the store and stays silent until it recovers; a second alert can only
follow a real `connected` → `error` transition after the restart, which is not a flood. A DB table
would preserve the stamp across restarts, but it would add a schema change for one timestamp whose
worst case is one extra push after two genuine transitions — the memory state plus the seed is the
smaller, sufficient answer.

The map grows with the number of connections and is never pruned; a deleted connection leaves one
small entry until the next restart (Risks).

## Payload and reason

The payload is the message notifier's struct, unmodified:

```json
{
  "v": 1,
  "title": "Connection down: ops",
  "author": "",
  "preview": "The WhatsApp number was disconnected",
  "url": "https://partyline.example.com/connectors/0192f0…",
  "tag": "conn:0192f0…"
}
```

- **title**: `Connection down: ` + `connectors.name`. The name is already validated to 1-100
  characters (`internal/connectors/service.go`, `validName`), so no truncation is needed and nothing
  user-typed is added.
- **author**: empty. `body()` in `web/public/sw.js:56-59` then uses `preview` alone as the body; the
  connection has no author to name.
- **preview**: one of four constants, never the platform's detail (see the table below).
- **url**: `publicURL.JoinPath("connectors", id)`. The route is `/connectors/$connectorId`
  (`web/src/routes/_app/connectors/$connectorId.tsx:67`; `_app` is pathless), and `JoinPath` keeps a
  sub-path deployment correct. Absolute, so `clients.openWindow` works from the worker.
- **tag**: `conn:<id>` as the brief requires. It collapses repeated notifications of the same
  connection on the operating system, and it is what `getNotifications({tag})` uses for the focused
  window's silent close (`web/public/sw.js:90-102`).
- **v**: the notifier's `payloadVersion = 1` (`internal/webpush/notifier.go:27-28`); an installed
  worker that already understands the message payload understands this one.

**The reason function** (`internal/webpush/alertrule.go`, pure):

| Kind and detail (stored, already redacted) | Reason |
| --- | --- |
| `whatsapp` and the detail contains `not connected` (`internal/connectors/whatsapp/status.go:17`, `:44-49`) | `The WhatsApp number was disconnected` |
| the detail is exactly `dto.ConnectorNoOperator` = `choose the AI that operates this connection` (`internal/api/dto/connectors.go:141-143`) | `The connection has no AI operating it` |
| the detail contains `unauthorized` (Telegram: `ErrUnauthorized`, `internal/connectors/telegram/connector.go:41-42`, reported by `supervise` at `manager.go:519`), `invalid bot token` (Discord construction, `internal/connectors/discord/connector.go:89-92`) or `authentication failed` (Discord gateway) | `Login expired` |
| anything else (a network failure, evolution-go unreachable, an unknown kind, an unreadable config) | `Cannot reach the service` |

Matching is lower-case `strings.Contains` on the detail and the kind equality; the detail itself is
never copied. The WhatsApp reason in parentheses (`Reconnecting`, `logged out from another device`,
CID-28) is platform data and is deliberately **not** echoed: the fixed sentence covers all of them, so
no branch can leak a phone number or a server message. A new platform detail falls into the generic
sentence until a rule for it is added; the reason set is the fixed contract of ALERT-08.

## The alert consumer

Files, all inside the app-layer `internal/webpush`:

| File | Responsibility |
| --- | --- |
| `alerts.go` | `Alerter`: bus subscription, `Seed`, `Run`, `Close`, the per-connection map, recipient lookup, job submission |
| `alertrule.go` | Pure rules: `alertReason`, `alertPayload`, `alertRule.observe` |

Types:

```go
type AlertDeps struct {
    Store     *store.Store
    Bus       *events.Bus
    Delivery  Delivery   // the same interface and Deliverer the notifier uses
    PublicURL *url.URL
    Logger    *slog.Logger
    Now       func() time.Time
    QueueSize int        // zero means 64; tests lower it
}

type Alerter struct {
    store     *store.Store
    bus       *events.Bus
    delivery  Delivery
    publicURL *url.URL
    logger    *slog.Logger
    now       func() time.Time
    worker    *worker

    mu      sync.Mutex
    rules   map[string]*alertRule
    events  <-chan events.Event
    cancel  func()
    closed  chan struct{}
    closeOnce sync.Once
}
```

- `NewAlerter` subscribes to the bus (buffer **256**) in the constructor, like `NewNotifier`, and
  builds `newWorker(deps.QueueSize, alertWorkers, logger)` — the existing bounded worker
  (`internal/webpush/worker.go:10-38`: non-blocking `submit`, panic guard, stop on cancel). It
  defaults `deps.QueueSize` to `alertQueueSize = 64` itself, because `newWorker`'s own zero default is
  the notifier's 256; `alertWorkers = 2`. It does no I/O, so it can run in `app.New` before the
  database is migrated.
- `Seed(ctx)` reads `ListConnectorStatuses` and fills
  `rules[id] = &alertRule{down: status == "error", seenUp: status != "error"}`. A read failure logs a
  warning with the error and leaves the map empty: every connection is then unseen, and its first
  `error` is a baseline instead of a flood (ALERT-10).
- `Run(ctx)` consumes the subscription: `events.ConnectorStatus` → `handle`; `events.Lagged` → debug
  (alerts are ephemeral; a missed event only costs one alert), and returns nil on cancel or `Close`,
  waiting for its two goroutines. It is the notifier's loop (`notifier.go:112-142`) without the message
  path; the ~25 duplicated lines are deliberate, so a message burst and an alert burst never share a
  queue.
- `handle(ctx, e)`:
  1. an `error` first loads the connection (`GetConnector`): `sql.ErrNoRows` means it is gone → return
     (ALERT-12); `!c.Enabled` → return (ALERT-06, closing the disable race); any other read failure is
     logged with `connector_id` and the error and **leaves the rule untouched**, so the next error event
     retries instead of losing the outage;
  2. `observe(e)` takes the lock, looks up `rules[e.ConnectorID]` (a missing entry is the zero
     `alertRule`), calls `rule.observe(e.Status, now)`, stores the rule and returns whether to alert; a
     connection never seen healthy only records its baseline and never alerts;
  3. a status that is not `error` (`connected`, `stopped`, `connecting`, anything else) follows the same
     `observe` path and can never alert;
  4. when `observe` says no, log at debug with `connector_id` and `status` and return;
  5. otherwise `send(ctx, c, e.Detail)`.
- `send(ctx, c, detail)` builds the payload with `alertPayload(publicURL, c.ID, c.Name, c.Kind, detail)`,
  reads `ListConnectorAlertTargets(c.ID)`, and submits one job per row:
  `Subscription{ID, Endpoint, P256dh, Auth}` — the same struct the notifier and `Deliverer` use. It
  logs `webpush: connection alert queued` with `connector_id` and `status`; an empty target list logs
  at debug with the same two fields.
- `submit` mirrors `Notifier.submit` (`notifier.go:182-192`): a full queue is counted and logged at
  warn with `connector_id` and `status`, never content (ALERT-15).
- `Close()` cancels the bus subscription, also when `Run` never started (`DisablePush`).

The reason the consumer is a second type and not a case inside `Notifier`: different trigger,
different state (per connection, not per user), different audience, and a queue that a message storm
cannot fill. Both share `Payload`, `Subscription`, `Delivery`, the `worker` and the `Deliverer`, which
is where the actual reuse is.

## Lifecycle and wiring

- `app.New`: `a.Alerts = webpush.NewAlerter(AlertDeps{Store: st, Bus: bus, Delivery: pushDelivery,
  PublicURL: cfg.PublicURL, Logger: opts.Logger, Now: opts.Now})`, next to `a.Push`
  (`internal/app/app.go:270-282`). It subscribes here, before `Start` can accept requests.
- `app.Start` (`internal/app/app.go:320-356`), after the migrations and the new-VAPID subscription
  reset, when `a.pushWorker` (one switch for every push: `!opts.DisablePush && vapid != nil`,
  `internal/app/app.go:280`):
  1. `a.Alerts.Seed(bg)` — synchronously, before any connector can report;
  2. `a.background(bg, "webpush-alerts", a.Alerts.Run)`;
  3. `a.ConnectorManager.Start(bg)` (`:356`) publishes the first statuses afterwards.
  No connector status can be published between `Seed` and the manager's start: the HTTP server is not
  serving and `Start` has not launched the connectors.
- `app.Shutdown` (`internal/app/app.go:382-397`): `a.Alerts.Close()` next to `a.Push.Close()`, before
  `a.bg.Wait()`.
- `app.Options.DisablePush` (`internal/app/app.go:62-66`) keeps both the notifier and the alerter from
  running, so `testkit.WithoutPush()` (`internal/testkit/testkit.go:112-114`) still means "this test
  drives its own push". No new option.

## Privacy

- **On the wire**: the same RFC 8291-encrypted payload as a message notification; the push service sees
  the endpoint, VAPID and ciphertext only. The alert adds no new metadata.
- **In the payload**: the connection name (already visible to the organization's members), a fixed
  reason, the connection id in the URL and in the tag. No `last_error`, no `disconnect_reason`, no
  platform error, no config value, no token, no chat or message content.
- **In the logs**: `webpush: connection alert queued` / `... suppressed` / `... queue full` with
  `connector_id` and `status`; the store-error paths add `err` as the existing notifier does. The
  connection name, the reason, the URL, an endpoint, `p256dh` and `auth` never appear.
- **In errors**: the payload builder returns an error without values; `Deliverer` already reduces a
  push failure to the endpoint host and the status, and a network error to `network`/`timeout`/
  `canceled` (`internal/webpush/delivery.go:52-126`).

## Tests

The tests derive from the spec's acceptance criteria and are written before the code. Values are
expected outcomes.

| Layer | Tests | Expected values |
| --- | --- | --- |
| Store (`internal/store/push_alert_targets_test.go`, real SQLite) | the target query with an org, admins, members, guests, an AI admin, an operator host, devices, a disabled admin, a connector without org/operator; the status query | rows exactly `u1`×2 devices and `u5`×1 for the standard fixture; zero rows for the member, the guest, the AI admin and the disabled admin; `ListConnectorStatuses` returns every id with its status |
| Rules (`internal/webpush/alertrule_test.go`, `package webpush`) | the reason table; the exact payload; the transition table | `("whatsapp", "the WhatsApp number is not connected (Reconnecting); reconnect it in evolution-go")` → `The WhatsApp number was disconnected`; `("telegram","unauthorized")` → `Login expired`; `("discord","invalid bot token")` → `Login expired`; `("telegram","dial tcp: connection refused")` → `Cannot reach the service`; `("whatsapp", dto.ConnectorNoOperator)` → `The connection has no AI operating it`; payload `{v:1,title:"Connection down: ops",author:"",preview:"The WhatsApp number was disconnected",url:"<public>/connectors/k1",tag:"conn:k1"}`; `connecting` on a failing rule → false and still failing; `connected` → false and cleared; `stopped` → false and cleared; `error` after `error` → false; `error` from up → true; 30 min exactly → true; `error` on a rule never seen healthy → false with `down` set; `connected` on a zero rule → false |
| Alerter (`internal/webpush/alerts_test.go`, `package webpush_test`, testkit + fake `Delivery`) | alert on a transition; admins + operator host; dedupe; no repeat; recovery and cooldown; stop; disabled row; seed; a connection created after start; unknown/deleted; logs; full queue | a WhatsApp `error` event → 3 jobs for the fixture (admin with 2 devices + operator host with 1), payload exact, the member/guest/disabled get none; repeating the detail → still 1; `connected` then `error` 29 min later → still 1, at 30 min → 2; `stopped` → no job; `enabled = 0` → no job; a connection seeded `error` → no job for its boot error, seeded `connected` → 1 job; an unseen connection's `error` → no job until it reports `connected`; an id with no row → no job and no panic; the log has `connector_id`, `status` and no name/reason/detail; `QueueSize: 1` with no worker → the second submit is dropped and logged |
| App (`internal/webpush/alerts_test.go`, app test) | the app builds, seeds and runs the alerter | `testkit.New(t).App.Alerts != nil`; with `WithoutPush()` no alert line appears; a running app logs `webpush: connection alert queued` for a connection status published after `Start`, with `connector_id` and `status` and without the detail; a connection stored `error` before `Restart` logs no alert for the boot error |
| Docs | none (build gate) | `README.md`, `docs/agents.md` and `.specs/STATE.md` state who receives, the reasons, the 30-minute rule and the log rule |

The push service is never contacted: the alerter tests inject a recorder exactly like
`internal/webpush/notifier_test.go` (`recorder`, `subscribe`, `noMore`), and the app test uses a
bogus endpoint so the real `Deliverer` fails before any network I/O, the way
`internal/webpush/delivery_test.go:196-208` does.

## Code Reuse Analysis

| Existing component | Location | How the feature uses it |
| --- | --- | --- |
| `events.ConnectorStatus` | `internal/events/bus.go:85-89` | The trigger; already published by every status write, so no event is added |
| `Manager.persist` / `handler.Status` | `internal/connectors/manager.go:571-580`, `:655-662` | The producer; the consumer only reads its event and the row it wrote |
| `whatsapp.disconnectedDetail` | `internal/connectors/whatsapp/status.go:11-50` | The source of the `not connected` detail the reason function recognises |
| `webpush.Payload`, `payloadVersion`, `Subscription`, `Delivery` | `internal/webpush/notifier.go:27-45`, `client.go` | The alert reuses the message payload shape and the delivery interface |
| `webpush.Deliverer` | `internal/webpush/delivery.go:52-126` | 2xx / 404/410 / other handling, `last_ok_at`, endpoint-host-only logs |
| `webpush.worker` and `guard.go` | `internal/webpush/worker.go:10-83` | The bounded queue, two send goroutines, a recovered panic per job, stop on cancel |
| `webpush.Notifier`'s loop and `submit` | `internal/webpush/notifier.go:112-142`, `:182-192` | The subscribe-in-constructor pattern and the non-blocking submit, copied with alert wording |
| `store.Store`, sqlc, `GetConnector` | `internal/store`, `internal/store/queries/connectors.sql:6-7` | One row read per alert and two new queries; generated code only |
| `push_subscriptions` | migration `0025` | The devices, joined directly so no group setting applies |
| `testkit` (`WithoutPush`, `OrgRoles`, `NewAIIn`, `NewUser`, `Restart`) | `internal/testkit` | The alerter and app tests build a real org, AI, connector and devices |
| `web/public/sw.js` | `web/public/sw.js:56-130` | Shows the payload and opens the URL; also suppresses the focused connection page (PUSH-17) |

**Integration points**: the event bus (`internal/events`), the shared SQLite store (`internal/store`),
the app wiring (`internal/app`) and the push delivery already built (`internal/webpush`). No API route,
no web app change, no schema change.

## Error Handling Strategy

| Scenario | Handling | What the user sees |
| --- | --- | --- |
| Connection row missing (deleted) | Return; the event is not logged (it is a normal race with a deletion) | Nothing |
| Connection disabled between write and read | `!Enabled` → return, rule untouched | Nothing (it was disabled on purpose) |
| Store read fails for the connector | Warn with `connector_id` and `err`; the rule is untouched, so the next error event retries | Nothing; a later error of the same outage alerts normally |
| No recipients | Debug with `connector_id` and `status` | Nothing |
| Payload cannot be built (impossible with these constants) | Warn with `connector_id`; return | Nothing |
| Queue full | Count, warn with `connector_id` and `status`, drop | Nothing; the connection's status is already stored and published |
| Push service 404/410 | `Deliverer` deletes the subscription | The device stops receiving until enabled again |
| Push service 429/5xx/network | `Deliverer` keeps the subscription, logs host + status or the sanitized kind | Nothing; no retry |
| Seeding fails at start | Warn with the error; empty map; every connection is unseen, so its first error is a baseline | No flood; the next `connected` then `error` alerts normally |
| Alerter panic in a job | `guardJob` recovers and logs the type and stack, never the value; the worker continues | The other devices still get the alert |

## Risks & Concerns

| Concern | Location | Impact | Mitigation |
| --- | --- | --- | --- |
| The alert state is memory | `internal/webpush/alerts.go` | A restart loses `last` and the per-outage state | The seed from `connectors.status` makes a boot burst silent; the worst case is one extra push after two genuine transitions, never a flood |
| `connectors.owner_id` is a dead anchor | `internal/store/migrations/0001_init.sql:62-77`, AD-019 | Notifying the original creator could reach someone who left the organization and cannot see the connection | Use the organization's admins and the operator's host (spec assumption, open question 1) |
| A connector row with `org_id` NULL (pre-0019) | `internal/store/migrations/0019_organizations.sql:70` | Only the operator's host is notified | Acceptable for a legacy row; the operator still exists or nobody operates it |
| `connecting` repeats on every restart | `internal/connectors/manager.go:496-519` | A rule that reset on `connecting` would alert after every boot | `connecting` never mutates the rule; the seed decides the boot state |
| The WhatsApp detail is platform data | `internal/connectors/whatsapp/status.go:44-50` | Echoing it could leak a number or a server message | The reason function maps it to a constant and never copies it; a test asserts the detail is absent from the payload and the log |
| The state map is never pruned | `internal/webpush/alerts.go` | One entry per connection id, including deleted ones | Bounded by the number of connections; entries are small; a restart clears them |
| Every failed start publishes an `error` | `internal/connectors/manager.go:334-368` | A connection that cannot even be built (bad config) alerts once | Intended: it is a real outage; the 30-minute rule bounds it |
| The alert shares `push_subscriptions` with message pushes | migration `0025` | A 404/410 while delivering an alert removes the device from message pushes too | Correct: the endpoint is gone for every payload |
| No new audit entry | — | Nobody can see who was alerted | The connection page keeps `last_error`; the log has the id and status; an audit of operations is not asked for |

## Alternatives considered

| Alternative | Why not |
| --- | --- |
| A new `events.ConnectorAlert` | `ConnectorStatus` already carries the id, the status and the detail after the commit; a second event would duplicate the seam and could disagree with the row |
| A callback from `connectors` into the app | The core would import the messenger (AD-025); the bus exists for this |
| Persisting the alert state in a table or a column | A schema change (and a migration) for one timestamp; the seed from `connectors.status` gives the same no-flood guarantee |
| Alerting `connectors.owner_id` | It is a dead anchor since AD-019 and gives nothing; the organization's admins are the ones who can fix the connection |
| Alerting every member of the organization | Noise, and the members cannot change the connection; the brief names the owner and the operator's owner |
| A per-user rate limiter like `webpush.limiter` | The brief bounds the alert per connection per 30 minutes; a per-user limiter would let a user with ten connections receive ten alerts and still be "in limit" |
| A "connection restored" push | Out of scope in the brief; recovery is visible on the connection page |
| Extending `Notifier` with a `ConnectorStatus` case | Different state and audience, and a shared queue would let a message storm drop an alert; a sibling type shares the parts that matter (`Payload`, `Delivery`, worker, `Deliverer`) |
| A service-worker change to render a different body | The existing `body()` already uses `preview` alone when `author` is empty; a change would need a worker version dance |
| A test button for alerts | `POST /api/v1/push/test` already proves the device path |
| A new route to read/send alerts | No user action is needed; the connection page already shows the status |

## Operations

- **No new state on disk**: no migration, no file, no key. The alert state is memory; the connection
  status and `last_error` are already stored by the connectors.
- **One instance**: the consumer is in-process like the notifier (AD-001, `datalock`); a second
  instance on the same database is not supported.
- **Logs**: `webpush: connection alert queued` (info), `... suppressed` (debug), `... queue full`
  (warn) with `connector_id` and `status`; a delivery keeps the `webpush: notification delivered/
  failed/dropped the subscription` lines of the shared `Deliverer` with `endpoint_host` and `status`.
- **Restart**: nothing to do; the seed keeps a burst silent and a connection that died during the
  downtime alerts on its first error after the restart.
- **Disable a connection**: it reports `stopped`, the rule is cleared and no alert is sent; re-enabling
  it starts a fresh rule, still under the 30-minute rule from the last alert.
- **Removal**: deleting a connection removes its row; the admins' and the host's devices stay
  registered for their other connections, and the in-memory rule disappears on the next restart.

## Decision record (proposed)

When the plan is approved, record one AD in `.specs/STATE.md` (AD-028, the next free number):

> Connection-down alerts are an app-layer consumer of `events.ConnectorStatus` inside
> `internal/webpush`; the responsible users are the admins of the connection's organization and the host
> user of the operator AI; one alert per outage with a 30-minute cooldown, the state in memory seeded
> from `connectors.status` at start; the body is one of four fixed sentences, never the platform's own
> error.

The docs task (T4) adds it.

## Open questions

See [spec.md](spec.md#open-questions-for-the-product-owner): the two `Confirmed? n` defaults (the
organization's admins as "the owner", and the reason for a connection with no operator) are the only
ones awaiting the owner.
