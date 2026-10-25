# Connection-Down Push Alerts Specification

## Problem Statement

A connection (a WhatsApp number, a Telegram or Discord bot, a webhook) is the own app of one AI
(AD-015): people write to that account and the AI answers as it. When the account stops working,
nothing tells the people who can fix it. The connection's page shows `error` with a detail, and the
WhatsApp check added in v0.6.2 notices a number that dropped — `the WhatsApp number is not connected
(reason); reconnect it in evolution-go`, where the reason is evolution-go's `disconnect_reason`
(CID-28, `internal/connectors/whatsapp/status.go:26-46`) — but that page is only read by someone who
already opened it. The messages simply stop arriving.

The web push pipeline exists since v0.8 (AD-027): browsers subscribe per device, the server encrypts
(RFC 8291) and signs (RFC 8292) a payload, and a background worker delivers it. This feature uses it
for the one notification an operator actually needs: **the connection went down**.

The decisions below are fixed by the product owner's brief:

- A connection that stops working — its status becomes `error` (the WhatsApp "number disconnected"
  state included, with its `disconnect_reason`) — sends a web push to the people responsible for it.
- The people responsible are the connection's owner and the user who owns the AI that operates it;
  every distinct user once.
- The title is `Connection down: <connection name>`, the body is a short human reason in English from
  a fixed set (for example `The WhatsApp number was disconnected`, `Login expired`, `Cannot reach the
  service`), never the raw platform error, a token or chat content. The URL is the connection's page.
- No spam: at most one alert per connection per 30 minutes, and none while it keeps failing; a new
  alert only after it was connected again and then went down. A connection coming back up sends
  nothing (no "connection restored" push in this version).
- The alert follows the user's devices like any push; it does **not** follow the group setting
  (`all` / `mentions` / `off`). There is no new user setting in this version.
- A connection that is disabled by its owner on purpose sends nothing.

## Goals

- [ ] The admins of a connection's organization and the host user of its operator AI hear about an
      outage on their phones and desktops within seconds, without opening the app.
- [ ] One outage produces one alert per device; repeated failures, a restart and a flapping connection
      add no noise.
- [ ] The text is a fixed sentence a person can act on; the platform's own error text, tokens and chat
      content never leave the server.
- [ ] A disabled connection and a recovered connection are silent; no new setting appears anywhere.
- [ ] The alert is delivered through the existing push pipeline (subscriptions, encryption, VAPID,
      worker, 404/410 deletion) with no second sender.
- [ ] Logs carry ids and status words only.

## Out of Scope

| Feature | Reason |
| --- | --- |
| A "connection restored" notification | The brief asks for the down direction only; a recovery is visible on the page |
| An alert history, inbox or audit entry | The push is an operational notice, not an administrative action; the connection's page keeps `last_error` |
| A per-user or per-connection alert setting (on/off, quiet hours, threshold) | "No new user setting in this version" is in the brief |
| Escalation, reminders while the connection stays down, or a snooze | Would need durable state and a scheduler; the brief asks for one alert per outage |
| Email, SMS or WhatsApp fallbacks | AD-015 retired WhatsApp copies; the brief asks for web push |
| Alerting on `connecting`, on the AI's presence, or on a growing platform queue | Those are not "the connection stopped working"; the brief names disconnected/error |
| Alerting the instance administrators or every organization member | The brief names the connection's owner and the operator AI's owner; a wider audience is noise and a privacy question |
| A "send test alert" button | The push feature already has a test notification (PUSH-15) |
| A change to the service worker, the payload version or the web app | The existing payload and worker already show a title, a body and open a URL (PUSH-16, PUSH-17) |
| A durable outbox, retries or delivery receipts for alerts | A notification is ephemeral (AD-027); a dropped alert is better than a stale one |
| A schema change | The alert state is memory (design) and every input already exists in `connectors`, `org_members`, `agents` and `push_subscriptions` |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| What "down" is | The `error` status. The WhatsApp "number disconnected" state is already an `error` with its detail; no `disconnected` status value exists today (`internal/connectors/connector.go:81-89`, `internal/connectors/whatsapp/status.go:36`) | The brief's "disconnected or error" maps to the one failing value the manager persists; a new value would be a second source of truth | y |
| Where the alert comes from | The existing `events.ConnectorStatus` event, published by `Manager.persist` after it stores the status (`internal/connectors/manager.go:571-580`) | Every persisted status change already publishes exactly this event, so no new event and no change to `connectors` are needed | y |
| Who "the connection's owner" is | The admins of the connection's organization (`org_members.permission = 'admin'`), not `connectors.owner_id` | The organization owns a connection (ORG-02) and `owner_id` is a dead anchor that gives nothing since AD-019; the web app deliberately names no owner (`web/src/routes/_app/connectors/connectors.test.tsx:700`), and only admins may change a connection (`docs/agents.md`, Connections and operators) | y (coordinator, delegated by the owner, 2026-10-25) |
| The other responsible user | The host user of the AI that operates the connection: `connectors.operator_iid` → `agents.owner_id` (`internal/store/migrations/0015_intelligences.sql:82`) | The brief names it; the AI's host is the person whose machine and session the account feeds | y |
| Duplicates and ineligible users | One alert per distinct user (a `UNION`), disabled accounts skipped, a user without a device gets nothing | "Every distinct user once" is in the brief; a device is the only way to receive a push | y |
| A connection with no operator | The `error` state `choose the AI that operates this connection` (`dto.ConnectorNoOperator`) alerts the organization's admins with the reason `The connection has no AI operating it` | The connection really stopped working (CID-02) and an admin must act; the brief's fixed set is "for example", and `Cannot reach the service` would be false | y (coordinator, delegated by the owner, 2026-10-25) |
| Where the "once per outage, 30 minutes" state lives | In memory in the alert consumer, seeded at start from `connectors.status` | The design shows why a restart cannot flood and why losing the 30-minute stamp across a restart is harmless; a table or a column would be a schema change for one timestamp | y |
| The reason set | Four fixed sentences: the WhatsApp number was disconnected, the login expired, the connection has no AI operating it, the service cannot be reached | The brief asks for a short human reason "from a fixed set"; the raw detail is data from the platform and stays in the database | y |
| The payload | The existing `webpush.Payload`: `v: 1`, `title`, empty `author`, the reason as `preview`, the absolute connection URL, `tag: conn:<id>` | The brief fixes the title, the body and the tag; the worker shows `preview` as the body when `author` is empty (`web/public/sw.js:56-59`) | y |
| The URL | `<public URL>/connectors/<id>`, the page the web app already serves (`web/src/routes/_app/connectors/$connectorId.tsx:67`) | The brief says "the connection's page in the web app" | y |
| How the alert is delivered | The existing `webpush.Deliverer` and its `Sender`: 2xx marks the device, 404/410 deletes the subscription, anything else keeps it and nothing retries | One push path, one set of rules; the brief says "using the web push feature already built" | y |
| Group setting and devices | The target query joins `push_subscriptions` directly and never reads `users.push_groups`; every device of a responsible user is notified | "It does not follow the group setting" is in the brief | y |
| A connection disabled on purpose | `stopped` clears the outage state and sends nothing; an alert also re-checks `connectors.enabled` before it is built | Disabling calls `Manager.Stop` → `stopped` (`internal/connectors/manager.go:384`); the enabled check closes the race | y |
| The service worker | Unchanged: it already shows the payload and opens its URL, and its focus rule suppresses a notification for the page already on screen (PUSH-17) | A connection page that is focused needs no notification; the rule is already tested | y |
| Logs | `connector_id` and `status` only; never the name, the reason, the detail, the URL or a device | The brief's rule; the detail can carry platform data | y |

**Open questions:** none blocking; the two rows marked `n` are adopted defaults with the product
owner's confirmation pending (see "Open questions for the product owner" at the end).

---

## User Stories

### P1: The people responsible learn that a connection went down ⭐ MVP

**User Story**: As an admin of the organization that owns a connection, or as the host of the AI that
operates it, I want a push on my phone when the account stops working so that I can reconnect it
before people notice that nobody answers.

**Acceptance Criteria**:

1. WHEN a connection reports `error`, the system holds it as healthy (it was seeded healthy, or it
   reported `connected` earlier in this process) and it does not already hold it as failing THEN the
   system SHALL send one web push notification to every device of every responsible user. (ALERT-01)
2. The system SHALL treat as responsible the users who are admins of the connection's organization and
   the host user of the AI that operates the connection, SHALL notify each distinct user once, and
   SHALL skip a disabled account and a user without a subscription. (ALERT-02)
3. WHEN a connection reports `connected` again THEN the system SHALL clear its failing state and SHALL
   send no notification for the recovery. (ALERT-03)
4. WHILE a connection keeps reporting `error` the system SHALL send no further notification for that
   outage. (ALERT-04)
5. The system SHALL send at most one alert per connection in any rolling 30 minutes. (ALERT-05)
6. WHEN a connection is disabled by its owner (status `stopped`) THEN the system SHALL clear its
   failing state and SHALL send nothing for it. (ALERT-06)
7. WHEN the system sends a connection alert THEN the payload SHALL carry the title
   `Connection down: <connection name>`, one reason of the fixed set as the body, the absolute URL of
   the connection's page, the tag `conn:<connection id>` and version 1, and SHALL carry no connection
   configuration, platform error, token or chat content. (ALERT-07)
8. The fixed reason set SHALL be `The WhatsApp number was disconnected`, `Login expired`,
   `The connection has no AI operating it` and `Cannot reach the service`, and a status detail that
   matches none of them SHALL map to `Cannot reach the service`. (ALERT-08)
9. The system SHALL deliver the alert through the existing web push delivery, so that a push service
   answering 404 or 410 deletes that subscription and any other failure keeps it and retries nothing.
   (ALERT-09)

**Independent Test**: Acme owns the WhatsApp connection `ops`; `alice` administers Acme and has two
devices, `carol` hosts the operator AI and has one. evolution-go reports the number disconnected; the
three devices each show `Connection down: ops` with `The WhatsApp number was disconnected`, and the
page `/connectors/<id>` opens on click. Repeating the error sends nothing. Disabling the connection
sends nothing. A Telegram connection whose bot token was revoked says `Login expired`.

---

### P1: Quiet after a restart and private in the logs ⭐ MVP

**User Story**: As an operator, I want the alerts to survive a deploy without a burst of pushes, and to
leave nothing sensitive in the logs, so that the feature is safe to run and to look at afterwards.

**Acceptance Criteria**:

1. WHEN the server starts THEN the system SHALL seed each connection's failing state from its stored
   `connectors.status` before it starts the connections, so that a connection already in `error` sends
   no alert for the outage it was already in. (ALERT-10)
2. WHEN the server starts and a connection's stored status is not `error` THEN the system SHALL hold
   that connection as healthy, so its first `error` after the start sends the alert (it is a new
   outage). (ALERT-11)
3. IF a status event names a connection the system cannot read (deleted, or created after start) THEN
   the system SHALL send nothing and SHALL NOT fail. (ALERT-12)
4. The system SHALL NOT consult `users.push_groups` for a connection alert and SHALL add no user
   setting for it. (ALERT-13)
5. WHEN a connection alert is logged THEN the log SHALL carry the connection id and the status only,
   and SHALL NOT carry the connection name, the reason, the platform detail, the URL, an endpoint or a
   key. (ALERT-14)
6. IF the push queue is full THEN the system SHALL drop the alert without blocking the connection's
   status change and without logging its content. (ALERT-15)

**Independent Test**: With a connection stored in `error` and an admin device, restarting the server
and letting the connection fail again produces no notification until the connection reports
`connected` and then fails. The alert's log lines name the connection id and `error`, and contain
neither the connection's name nor the WhatsApp reason.

---

## Edge Cases

- WHEN the connection is deleted between the status event and the alert THEN the system SHALL send
  nothing (the row is gone; the devices of its admins stay registered for other connections).
- WHEN a connection is created after the server started and its first status is `error` THEN the
  system SHALL only record the failing state: the person who just created it is looking at the form.
- WHEN the status detail changes while the connection stays in `error` (CID-28 reports a new reason)
  THEN the system SHALL send no second alert.
- WHEN the connection is disabled while an alert is queued THEN the alert MAY still be delivered once;
  the next error event after re-enabling is subject to the 30-minute rule.
- WHEN the connection page is the focused window THEN the service worker SHALL apply its existing focus
  rule: the push ends as a silent notification closed at once, never as zero notifications (PUSH-17).
- WHEN no responsible user has a subscription THEN the system SHALL send nothing and log the decision
  with the connection id and status only.
- WHEN the connection's row cannot be read THEN the system SHALL log the failure with the connection
  id and the error, send nothing for that event, and keep running (the next `error` of the same
  outage retries).
- WHEN the payload cannot be built THEN the system SHALL log it with the connection id, send nothing
  and keep running.
- WHEN the alert queue is full THEN the system SHALL count and drop the job as the message notifier
  does (PUSH-27), and the next status event is handled normally.

---

## Implicit-requirement sweep

| Dimension | Covered by |
| --- | --- |
| Input validation & bounds | The alert reuses stored, already-validated rows: no user input reaches it; the title is bounded by the connection name's 1-100 characters (`connectors.Service` validation); the reason is one of four constants; the tag is `conn` plus a store id |
| Failure / partial-failure states | ALERT-09 (404/410 vs other push failures, inherited), ALERT-12 (missing row), edge cases (unreadable row, payload failure, full queue) |
| Idempotency / duplicates | ALERT-02 (a user who is both admin and operator's host receives one alert), ALERT-04 and ALERT-05 (one alert per outage and per 30 minutes), ALERT-10 (restart) |
| Auth boundaries & rate limits | No route and no caller: the consumer reads the store and never accepts a request. The bound is per connection (ALERT-05); a user's other connections are independent |
| Concurrency / ordering | `persist` writes the status before it publishes, so the consumer reads a row that agrees with the event; the state is behind one mutex; the queue's `Submit` never blocks; several devices are independent jobs |
| Data lifecycle | A deleted connection's events find no row (ALERT-12); deleting an account cascades its subscriptions (PUSH-07) so it stops receiving; a `connecting` event never resets the outage |
| Observability | ALERT-14 (ids and status words only); the connection's page keeps the human detail (`last_error`); no metrics in v1 |
| External-dependency failure | The push service is slow, unreachable or answers 429/5xx: the subscription is kept and nothing retries (shared `Deliverer`, PUSH-12) |
| State-transition integrity | `error` → down (one alert), `connected`/`stopped` → up (no alert), `connecting`/unknown status → unchanged; a connection never seen healthy in the process is a baseline (ALERT-10, ALERT-12) |

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| ALERT-01 | P1: The people responsible learn that a connection went down | Execute | Pending |
| ALERT-02 | P1: The people responsible learn that a connection went down | Execute | Pending |
| ALERT-03 | P1: The people responsible learn that a connection went down | Execute | Pending |
| ALERT-04 | P1: The people responsible learn that a connection went down | Execute | Pending |
| ALERT-05 | P1: The people responsible learn that a connection went down | Execute | Pending |
| ALERT-06 | P1: The people responsible learn that a connection went down | Execute | Pending |
| ALERT-07 | P1: The people responsible learn that a connection went down | Execute | Pending |
| ALERT-08 | P1: The people responsible learn that a connection went down | Execute | Pending |
| ALERT-09 | P1: The people responsible learn that a connection went down | Execute | Pending |
| ALERT-10 | P1: Quiet after a restart and private in the logs | Execute | Pending |
| ALERT-11 | P1: Quiet after a restart and private in the logs | Execute | Pending |
| ALERT-12 | P1: Quiet after a restart and private in the logs | Execute | Pending |
| ALERT-13 | P1: Quiet after a restart and private in the logs | Execute | Pending |
| ALERT-14 | P1: Quiet after a restart and private in the logs | Execute | Pending |
| ALERT-15 | P1: Quiet after a restart and private in the logs | Execute | Pending |

**Coverage:** 15 total, 15 mapped to tasks (`tasks.md` T1-T4), status Pending (the plan is not executed
yet).

---

## Success Criteria

- [ ] A WhatsApp number is logged out; within seconds the organization's admins and the AI's host get
      `Connection down: ops` / `The WhatsApp number was disconnected` on every device, and the click
      opens `/connectors/<id>`.
- [ ] The same outage, a supervisor restart loop and a server restart produce no second push until the
      number is connected again and drops again.
- [ ] Disabling the connection silences it; re-enabling it and a new outage alerts again after the
      30-minute window.
- [ ] A Telegram bot token revoked the day before the outage says `Login expired`, and no log line
      contains the platform's error, the connection's name or a device endpoint.
- [ ] An account that turned group notifications `off` still receives its connection alerts; nothing in
      Settings changed.

---

## Open questions for the product owner

1. **Who "the connection's owner" is.** The brief names "the connection's owner". Since AD-019 the
   `connectors.owner_id` column is a dead anchor that gives nothing: the organization owns the
   connection and only its admins may change it. We therefore read "owner" as *the admins of the
   connection's organization*. Confirm, or say that the column's original creator (who may have left
   the organization) must still receive the alert.
2. **A connection with no operator.** When nobody operates the connection (CID-02) it is in `error`
   with the detail `choose the AI that operates this connection`. We alert its organization's admins
   with a fourth reason, `The connection has no AI operating it`, because the connection really
   stopped working and an admin must choose an operator. Confirm, or say that this configuration state
   must stay silent (the alert would then cover only platform failures).
