# Connection-Down Push Alerts Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Spec**: `.specs/features/connection-alert/spec.md`
**Design**: `.specs/features/connection-alert/design.md`
**Status**: Draft, awaiting approval

The brief fixes the behaviour (spec's Problem Statement). The design settles the questions it leaves to
the code: the existing `events.ConnectorStatus` is the trigger (no new event, no migration), the
responsible users are the organization's admins plus the operator AI's host, the outage state is memory
seeded from `connectors.status`, the payload is the message notifier's with a fixed reason and
`tag: conn:<id>`, and the web app needs no change.

Each task is one commit, at most about 300 changed lines and about 5 hand-written files, under an hour,
and is independently testable. Tests are written first and derive from the spec's acceptance criteria.
Generated files (`internal/store/db/*`) are never hand-edited.

## Test Coverage Matrix

> Generated from the codebase, the project guidelines and the spec. Guidelines found: `AGENTS.md`
> (tests derive from the spec's ACs; `make check` before every commit; no message text or secrets in
> logs; synthetic fixtures), `.specs/LESSONS.md`, `Makefile`, `scripts/check.sh`, existing tests
> (`internal/store/push_migration_test.go`, `internal/webpush/notifier_test.go`,
> `internal/webpush/delivery_test.go`, `internal/testkit`).

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| Store queries (`ListConnectorAlertTargets`, `ListConnectorStatuses`) | integration (real SQLite) | admins ∪ operator host; distinct users; devices per user; disabled admin, member, guest and AI admin excluded; connector without org or operator; stored statuses | `internal/store/push_alert_targets_test.go` | `go test -count=1 ./internal/store/...` |
| Pure rules (`alertReason`, `alertPayload`, `alertRule.observe`) | unit | every reason branch; the exact payload JSON; the whole transition table including a connection never seen healthy and the 30-minute edge | `internal/webpush/alertrule_test.go` | `go test -count=1 ./internal/webpush/...` |
| Alerter (bus, seed, map, queue) | integration (real SQLite + bus + fake `Delivery`) | one alert per transition; admins + host; dedupe; no repeat; recovery; cooldown; stopped/disabled; seeded restart; unknown/deleted; log privacy; full queue; `Lagged`; `Close` | `internal/webpush/alerts_test.go` | `make test` |
| App wiring (seed before the connections, `DisablePush`) | integration (testkit, real `App`) | `App.Alerts` built; a running app alerts; `WithoutPush` does not; a connection stored `error` sends nothing after `Restart`; one stored `connected` alerts on its boot error | `internal/webpush/alerts_test.go` | `make test` |
| Docs | none (build gate) | `README.md`, `docs/agents.md` and `.specs/STATE.md` state the audience, the reasons, the rule and the log line | `README.md`, `docs/agents.md` | `make check` |

## Gate Check Commands

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick (store) | While iterating on the queries | `go test -count=1 ./internal/store/...` |
| Quick (webpush) | While iterating on the alerter | `go test -count=1 ./internal/webpush/...` |
| Full | Before any app-level change is done | `make test` |
| Build | **Before every commit** (AGENTS.md, L-029) | `make check` |

Run `make generate` after changing `internal/store/queries/push.sql`; commit the generated files with
the change (T1). No migration, route or DTO changes this feature.

## Execution Plan

The phases are ordered. Tasks inside a phase run in parallel worktrees; phases run in order and each
merges back before the next starts.

| Group | Phases | Starts after |
| --- | --- | --- |
| G0 | Phase 1 (T1 and T2 in parallel) | — |
| G1 | Phase 2 (T3) | G0 merged |
| G2 | Phase 3 (T4) | G1 merged |

Dependency graph (every arrow is a `Depends on` of the task it points to):

```
T1 -> T3
T2 -> T3
T3 -> T4
```

- **T1 and T2 in parallel.** T1 touches `internal/store` only; T2 creates
  `internal/webpush/alertrule.go` and its test. They share no file, so no merge conflict. T1 is the only
  task that regenerates sqlc output; after it merges, `make generate` must leave the tree clean.
- **T3 after both.** It reads the two queries from T1 and the rules from T2. It subscribes to the bus in
  the constructor, so it must not be constructed by the app before T4.
- **T4 last.** It wires the alerter into `app.Start` in the right order (seed before the connections
  start) and documents the feature. It changes the same test file T3 created, which is why it is not
  parallel to T3.

### Phase 1: Inputs

T1 (store) and T2 (pure rules) are independent and run in parallel worktrees.

### Phase 2: The alerter

T3 adds the `Alerter` on the bus with its integration tests, on top of T1 and T2.

### Phase 3: Wiring and documentation

T4 runs the alerter in the app, proves the seed order with `Restart`, and documents the feature.

---

## Task Breakdown

### Phase 1: Inputs

#### T1: List the users responsible for a connection [store]

**What**: two read-only sqlc queries in `internal/store/queries/push.sql`, exactly as the design's
[Who is responsible](../connection-alert/design.md#who-is-responsible) shows:
`ListConnectorAlertTargets(connector_id)` — the `WITH responsible … UNION …` query that returns one row
per (responsible enabled user, device) for the admins of the connector's organization and the host user
of its operator; and `ListConnectorStatuses` — `SELECT id, status FROM connectors ORDER BY id` for the
alerter's start state. Regenerate the sqlc output. No migration, no schema change, no Go hook.

**Where**: `internal/store/queries/push.sql`
**Also**: `internal/store/push_alert_targets_test.go`, `internal/store/db/push.sql.go`,
`internal/store/db/querier.go` (generated)
**Depends on**: None
**Requirement**: ALERT-02, ALERT-10, ALERT-13
**Parallel**: runs in parallel with T2
**Size**: about 220 changed lines, 4 files (two generated)
**Tests first** (`internal/store/push_alert_targets_test.go`, `package store`, `newSchemaEnv(t)` with the
existing `user`, `org`, `member`, `pushSubscription` and `intelligence` helpers; the `connectors` and
`agents` rows are raw `INSERT`s):
- `TestConnectorAlertTargetsAreTheAdminsAndTheOperatorsHost`: organization `o1` with `u1` admin (devices
  `p1`, `p2`), `u2` admin (no device), `u6` admin disabled (device `p6`), `u3` member (device `p3`),
  `u4` guest (device `p4`), the AI `a1` with admin permission and no device, and the AI `a2` owned by
  `u5` (member, device `p5`) as the connector's operator. The rows are exactly `[u1/p1, u1/p2, u5/p5]`,
  with `u1`'s two devices in `created_at` order; `u2`, `u3`, `u4`, `u6` and `a1` have none. Setting
  `u1`'s `push_groups` to `off` leaves the rows unchanged (the alert ignores the group setting,
  ALERT-13).
- `TestConnectorAlertTargetsNotifyAnAdminWhoAlsoHostsTheOperatorOnce`: make `u1` the host of `a2`; the
  rows stay `[u1/p1, u1/p2]` (the `UNION` dedupes the user, not the devices).
- `TestConnectorAlertTargetsWithoutAnOrganizationOrOperator`: a connector with `org_id` NULL and
  operator `a2` → `[u5/p5]`; a connector with `org_id` `o1` and `operator_iid` NULL → `[u1/p1, u1/p2]`;
  a connector with both NULL → no rows.
- `TestListConnectorStatusesReturnsEveryStoredStatus`: three connectors with `error`, `stopped` and
  `connected`; the rows are those three pairs in id order, `stopped` included; deleting one leaves two.
- `TestConnectorAlertTargetsOfADeletedConnector`: deleting `k1` and querying its id returns zero rows.
**Done when**:
- [x] the four tests pass; the `UNION` dedupes a user and every device of a responsible user is a row
- [x] `make generate` ran and the sqlc output is committed; a second `make generate` is clean
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(store): list the users responsible for a connection`

#### T2: Decide and write a connection alert [webpush]

**What**: `internal/webpush/alertrule.go`, the pure part of the feature, exactly as the design's
[Payload and reason](../connection-alert/design.md#payload-and-reason) and
[The alert state](../connection-alert/design.md#the-alert-state) describe:
- constants `alertGap = 30 * time.Minute`, `alertTitlePrefix = "Connection down: "`,
  `alertTagPrefix = "conn:"`, `kindWhatsApp = "whatsapp"` and the four fixed reason sentences;
- `alertReason(kind, detail string) string` — lower-case `strings.Contains` matching in the order
  WhatsApp not-connected, `dto.ConnectorNoOperator`, credential failures, generic;
- `alertPayload(public *url.URL, id, name, kind, detail string) ([]byte, error)` — the shared
  `Payload` with `V: payloadVersion`, the title, empty `Author`, the reason as `Preview`, the absolute
  connection URL and `conn:<id>`;
- `alertRule{down bool, seenUp bool, last time.Time}` and `observe(status string, now time.Time) bool`
  with the design's transition table; statuses come from `dto.ConnectorStatus*`.
No I/O, no clock of its own (the caller passes `now`), no store or bus import.

**Where**: `internal/webpush/alertrule.go`
**Also**: `internal/webpush/alertrule_test.go`
**Depends on**: None
**Requirement**: ALERT-03, ALERT-04, ALERT-05, ALERT-06, ALERT-07, ALERT-08, ALERT-11
**Parallel**: runs in parallel with T1
**Size**: about 200 changed lines, 2 files
**Tests first** (`internal/webpush/alertrule_test.go`, `package webpush`):
- `TestAlertReason` (table): `("whatsapp", "the WhatsApp number is not connected (Reconnecting); reconnect
  it in evolution-go")` → `The WhatsApp number was disconnected`; the same detail without parentheses →
  the same; `("telegram", "unauthorized")` → `Login expired`; `("discord", "invalid bot token")` →
  `Login expired`; `("discord", "websocket: close 4004: Authentication failed")` → `Login expired`;
  `("telegram", "dial tcp: connection refused")` → `Cannot reach the service`; `("whatsapp",
  dto.ConnectorNoOperator)` → `The connection has no AI operating it`; `("webhook", "")` →
  `Cannot reach the service`; `("telegram", "401 token abc123")` → `Cannot reach the service` and the
  result does not contain `abc123`.
- `TestAlertPayload`: `alertPayload(public("https://partyline.example.test/base"), "k1", "ops",
  "whatsapp", whatsappDetail)` unmarshals to exactly `Payload{V: 1, Title: "Connection down: ops",
  Author: "", Preview: "The WhatsApp number was disconnected", URL:
  "https://partyline.example.test/base/connectors/k1", Tag: "conn:k1"}`, and the JSON object has exactly
  six keys; a generic detail gives `Cannot reach the service`.
- `TestAlertRuleTransitions` (table, fixed `t0 = 2026-10-25T12:00:00Z`): a zero rule with `error` →
  `false`, `down` true and still unseen; a zero rule with `connected` → `false`, `seenUp` true;
  `connecting` on a failing rule → `false` and still failing; `connected` on a failing rule → `false`
  and cleared; `stopped` on a failing rule → `false` and cleared; `error` on a never-seen-healthy rule
  (`stopped` then `error`) → `false`; `error` on a failing rule → `false`; `connected` then `error` →
  `true` with `last = now`; a second `error` → `false`; `connected` then `error` at `t0+29m` → `false`;
  `connected` then `error` at `t0+30m` → `true`; an unknown status on a healthy rule → `false` and
  unchanged.
**Done when**:
- [x] every row of the design's transition table is a case, including the exact 30-minute edge
- [x] `alertrule.go` imports only `encoding/json`, `net/url`, `strings`, `time` and `internal/api/dto`;
      no `store`, `events` or `net/http`
- [x] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(webpush): decide and write a connection alert`

### Phase 2: The alerter

#### T3: Run the connection alerter on the status bus [webpush]

**What**: `internal/webpush/alerts.go`, the consumer of the design's
[The alert consumer](../connection-alert/design.md#the-alert-consumer) and
[The alert state](../connection-alert/design.md#the-alert-state): `AlertDeps` and `Alerter`;
`NewAlerter` subscribes to `events.ConnectorStatus` with a buffer of 256 and builds the existing
`worker` (queue 64, 2 goroutines, non-blocking submit); `Seed(ctx)` fills the per-connection
`alertRule` map from `ListConnectorStatuses` (a read failure warns and leaves it empty); `Run(ctx)`
handles `ConnectorStatus` and `Lagged`, returns nil on cancel or `Close`, and waits for its
goroutines; for an `error`, `handle` first reads `GetConnector` (skip `sql.ErrNoRows`, `!Enabled` and
a read failure), then applies `observe` (a connection never seen healthy only records a baseline); on
an alert it builds the payload with `alertPayload`, reads `ListConnectorAlertTargets`, and submits one
job per device through the shared `Delivery`. Logs carry `connector_id` and `status` only. No app
wiring yet (T4).

**Where**: `internal/webpush/alerts.go`
**Also**: `internal/webpush/alerts_test.go`
**Depends on**: T1, T2
**Requirement**: ALERT-01, ALERT-02, ALERT-06, ALERT-09, ALERT-10, ALERT-12, ALERT-14, ALERT-15
**Size**: about 300 changed lines, 2 files
**Tests first** (`internal/webpush/alerts_test.go`, `package webpush_test`, `testkit.New(t,
testkit.WithoutPush())`, a `testkit.NewClock`, the notifier test's `recorder`, `subscribe`,
`notifierWait` and `noMore`, and a helper that inserts an organization, an operator AI, a connector row,
the devices and its stored status — `connected` unless a test says otherwise, so `Seed` marks the
connection as seen healthy):
- `TestAlerterAlertsOnAnError`: Acme with admin `alice` (2 devices), member `bob` (1), the operator AI
  owned by `carol` (1), and a disabled admin `dan`; `Seed`, then `Run`; publishing `ConnectorStatus{ID:
  "k1", Status: "error", Detail: whatsappDetail}` gives exactly 3 jobs, to Alice's two devices and
  Carol's one, each payload exactly `Payload{V: 1, Title: "Connection down: ops", Author: "",
  Preview: "The WhatsApp number was disconnected", URL: <public>/connectors/k1, Tag: "conn:k1"}`; Bob and
  Dan get none; `noMore`.
- `TestAlerterNotifiesEachUserOnce`: Alice is an admin and also hosts the operator → exactly 2 jobs (her
  two devices), not 3.
- `TestAlerterStaysQuietWhileFailing`: `error`, then `error` with the detail `the WhatsApp number is not
  connected (logged out from another device); …`, then `error` again → 1 job; `connected` → 0 more;
  `error` at `t0+31m` → 2 jobs in total.
- `TestAlerterCooldown`: `error` at `t0` → 1; `connected`; `error` at `t0+29m` → still 1; `connected`;
  `error` at `t0+30m` → 2.
- `TestAlerterStoppedConnection`: an `error` at `t0` → 1; `stopped` → 0 more; `connected`; `error` at
  `t0+30m` → 2 (the stop cleared the rule, the cooldown still applies).
- `TestAlerterSkipsADisabledConnection`: a connector with `enabled = 0` in the store, seeded from a
  stored `connected`, an `error` → 0 jobs and no rule change; after `enabled = 1` in the store the next
  `error` alerts (no alert was recorded, so no cooldown applies).
- `TestAlerterSeedsTheStoredStatus`: connector `k1` stored `error` → `Seed` → publish `error` → 0;
  `connected` then `error` at `t0+31m` → 1. Connector `k2` stored `connected` → publish `error` → 1.
- `TestAlerterIgnoresUnknownAndDeletedConnections`: an `error` for an id with no row → 0 jobs and no
  panic; deleting `k1` after `Seed` and publishing its `error` → 0.
- `TestAlerterANewConnectionIsABaseline`: `Seed` before the connector exists, then insert it with an
  admin device; `error` → 0; `connected`; `error` at `t0+31m` → 1 (a connection under this process's
  eyes had to be healthy first).
- `TestAlerterLogsOnlyTheIdAndStatus`: a connector named `payments-bot`; capture the app logger; the
  `connection alert` line contains `connector_id=k1` and `status=error`, and no line contains
  `payments-bot`, `logged out`, `WhatsApp number` or `/connectors/`.
- `TestAlerterDropsOnAFullQueue`: `QueueSize: 1`, no `Run`; a `Seed`ed connector with an admin who has 2
  devices; publishing the `error` returns (never blocks) and the log contains
  `webpush: connection alert queue full`; no delivery happens because no worker runs.
- `TestAlerterIgnoresLaggedAndClosesWithoutRun`: `Lagged` → no job and no panic; `Close()` without
  `Run` does not panic and a later publish delivers nothing.
**Done when**:
- [ ] the bus is subscribed in `NewAlerter`, before `Seed`/`Run`, and `Close()` cancels it
- [ ] `Submit` never blocks the bus consumer; a full queue is logged with the id and status only
- [ ] no log line carries the connection name, the reason, the platform detail, the URL or a device
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(webpush): alert connections that go down`

### Phase 3: Wiring and documentation

#### T4: Run the alerts in the app and document them [app]

**What**: `App.Alerts *webpush.Alerter` built in `app.New` from the same `pushDelivery` as `a.Push`, and
wired into `app.Start` exactly as the design's
[Lifecycle and wiring](../connection-alert/design.md#lifecycle-and-wiring) says: after the migrations,
when `a.pushWorker` (`!opts.DisablePush && vapid != nil`), call `a.Alerts.Seed(bg)` **synchronously**
and then `a.background(bg, "webpush-alerts", a.Alerts.Run)`, both before
`a.ConnectorManager.Start(bg)`; `app.Shutdown` calls `a.Alerts.Close()` next to `a.Push.Close()`. No new
option: `DisablePush` and a nil VAPID keep every push off. Docs: the "Connection alerts" paragraph in
`README.md`'s Web push section (audience, the four reasons, the 30-minute rule, silence on disable and
recovery, the log line), the operator-facing paragraph in `docs/agents.md`'s Connections section, and
AD-028 in `.specs/STATE.md` with the decision the design records.

**Where**: `internal/app/app.go`
**Also**: `internal/webpush/alerts_test.go`, `README.md`, `docs/agents.md`, `.specs/STATE.md`
**Depends on**: T3
**Requirement**: ALERT-01, ALERT-06, ALERT-09, ALERT-10, ALERT-11, ALERT-14
**Size**: about 140 changed lines, 5 files
**Tests first** (appended to `internal/webpush/alerts_test.go`, `package webpush_test`, with
`testkit.WithLogger(testLogger(&buf))`):
- `TestAppStartsTheConnectionAlerter`: `testkit.New(t, testkit.WithLogger(…))` returns a non-nil
  `env.App.Alerts`; after the app started, insert the connector row (enabled) so the manager never applies
  it, give the admin a device with the endpoint `not a push endpoint`, publish `connected` and then the
  `error` event (the app seeded before the connector existed, so it must be seen healthy first); the
  buffer eventually contains `webpush: connection alert queued` with `connector_id=` and `status=error`
  and not `WhatsApp number`, and then `endpoint_host=invalid` from the shared `Deliverer`.
- `TestWithoutPushStopsTheConnectionAlerter`: `testkit.New(t, testkit.WithoutPush(), testkit.WithLogger(…))`
  still has `App.Alerts != nil`, but publishing the same event produces no `connection alert` line for
  300 ms.
- `TestAppSeedsTheAlertStateBeforeTheConnectionsStart`: after the app started, insert an enabled
  connector of an unknown kind with an operator AI (the running manager never applies it) and an admin
  device, then set its stored status to `error`; `env.Restart()` starts a new manager, which persists
  its `unknown connector kind` error during `Start`; no `connection alert` line appears. Control: the
  same setup with the stored status `connected` shows the alert for that boot error.
**Done when**:
- [ ] `Seed` runs before `ConnectorManager.Start` (the `Restart` test proves it), and `DisablePush`
      covers the alerter
- [ ] `app.Shutdown` closes the subscription even when `Run` never started
- [ ] `README.md`, `docs/agents.md` and `.specs/STATE.md` are updated (AD-028 recorded)
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(app): run connection alerts and document them`
