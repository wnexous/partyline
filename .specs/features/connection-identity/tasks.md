# Connection Identity Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Design**: `.specs/features/connection-identity/design.md`
**Status**: Draft

Retired behaviour (CID-22) is retired together with its tests. The task that removes a feature
deletes the tests that pin it, and its commit body names the retired requirement IDs (for
example ROUTE-01..11, DLV-05, ACC-02..06, DM-05, NOTI-01..03, UI-13, MEM-01). Tests of other
features that used a retired feature only as setup are rewritten to assert the new behaviour,
such as a queue item instead of a room message. Weakening an assertion is still forbidden.

**Worker tier**: **[small]** tasks are isolated and mechanical, so a cheaper worker fits.
**[core]** tasks carry the design's rules and need a high-reasoning worker. **[done]** tasks are
already implemented.

---

## Test Coverage Matrix

> Generated from codebase, project guidelines, and spec - confirm before Execute. Guidelines found: `AGENTS.md` (tests derive from ACs; `make check` before every commit; no message text or secrets in logs; synthetic fixtures), `.specs/LESSONS.md` (confirmed L-017, L-018, L-029, L-042), `Makefile`, `scripts/check.sh`, `web/vite.config.ts`, `.specs/features/web-app/conventions.md`, sampled tests in `internal/api/*_test.go`, `internal/agents/*_test.go`, `internal/channel/*_test.go`, `internal/connectors/*_test.go`, `internal/store/*_migration_test.go`, `integrations/*/partyline.test.ts`.

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| Queue service (`internal/platform`) | integration (real SQLite, fake clock) | All branches; 1:1 to CID-08..11, 26; boundaries 5,000/5,001 items, 7 d ± 1 ms, 24 h ± 1 ms | `internal/platform/*_test.go` | `go test -count=1 ./internal/platform/...` |
| Store, migrations | integration (real SQLite) | Every new table/column and every upgrade statement; rollback on failure | `internal/store/*_test.go` | `go test -count=1 ./internal/store/...` |
| Services (connectors pipeline/sends/manager, agents gateway, rooms, access) | integration (real SQLite, fake connector) | Every AC and error path; each filter condition alone (L-042) | `internal/**/*_test.go` | `make test` |
| HTTP routes, agent WebSocket, channel tools, listen, hook | e2e (testkit, real app on httptest) | Every changed route/frame/tool: happy + edge + error; each new error code observed at its HTTP status (L-017); negative tests isolate the guard (L-018) | `internal/api/*_test.go`, `internal/channel/*_test.go`, `internal/cli/*_test.go` | `make test` |
| Platform adapters | integration against fakes built from captured payloads | Mention detection per platform | `internal/connectors/<kind>/*_test.go` | `go test -count=1 ./internal/connectors/<kind>/...` |
| TS adapters (`integrations/`) | unit (bun) | Shared framing cases include a platform record | `integrations/**/*.test.ts` | `make integrations-test` |
| Web components/pages | unit (Vitest + Testing Library) | Each visible AC; `en` and `pt-BR` strings | `web/src/**/*.test.{ts,tsx}` | `cd web && bun run test` |
| Docs | none | build gate only | `README*.md`, `docs/*.md` | `make check` |

## Gate Check Commands

> Generated from codebase - confirm before Execute.

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick | While iterating on unit or single-package code | `go test -count=1 ./internal/<pkg>/...`, `cd web && bun run test`, `make integrations-test` |
| Full | While iterating on integration/e2e code | `make test` |
| Build | Before **every** commit (AGENTS.md, L-029) | `make check` |
| E2E | T37 and before UAT | `make e2e` |

Every task's commit gate is `make check` (lint, vet, generated-code drift, all Go/web/TS tests,
build). DTO tasks run `make generate` first.

---

## Execution Plan

Phases run in order, and tasks within a phase run in order. Arrows show the real dependencies.
A task with no incoming arrow inside its phase depends only on earlier phases.

### Phase 1: Done

```
T1
```

### Phase 2: Store and queue

```
T2 -> T3 -> T4
```

### Phase 3: Inbound to the queue

```
T5 -> T6 -> T7
T7 -> T8
T7 -> T9
T7 -> T10
T7 -> T11
```

### Phase 4: Delivery to the session

```
T12 -> T14 -> T16
T13 -> T15
```

### Phase 5: Answering through the connection

```
T17 -> T18
T17 -> T19 -> T20
```

### Phase 6: Lifecycle and guards

```
T21 -> T22
T23
T24
```

### Phase 7: Web

```
T25
T26
T27
T28
T29
T30
T31
T32
```

### Phase 8: Server retirement

```
T33 -> T35
T34 -> T35
```

### Phase 9: Upgrade and release

```
T36 -> T37 -> T38
```

---

## Task Breakdown

### Phase 1: Done

#### T1: WhatsApp connection uses exactly one instance [done]

**What**: `instances` is required. With zero or several instances the connection reports `StatusError` with the detail `choose the instance (number) of this connection`, dials nothing and waits for ctx. Sends, reactions and chat listings use only the configured instance.
**Where**: `internal/connectors/whatsapp/connector.go`
**Depends on**: None
**Reuses**: the fake evolution server
**Requirement**: CID-04
**Done when**:
- [x] Implemented and tested in commit `bebd8d1` on branch `wnexous/pl-cid-t27` (contract notes there); merge that branch before T2
- [x] Gate check passed: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(whatsapp): one instance per connection` (bebd8d1)

### Phase 2: Store and queue

#### T2: Migration 0017 for the platform queue [core]

**What**: `0017_platform_queue.sql` adds:
- `platform_items`, `platform_attachments` and `platform_sends` with the design's indexes;
- `agents.platform_dropped`;
- `connectors.deliver` (default `all`) and `connectors.groups_mentions_only` (default 1).

It also adds the sqlc queries the queue needs.
**Where**: `internal/store/migrations/0017_platform_queue.sql`
**Depends on**: None
**Reuses**: AD-012 `ADD COLUMN` pattern, `openMigratedBefore`
**Requirement**: CID-08, CID-26, CID-27
**Done when**:
- [x] Store test: the tables and columns exist; existing connectors read `deliver=all`, `groups_mentions_only=1` (CID-27); the dedupe index refuses a second `(connector, chat, external_id)` row and accepts empty external ids twice
- [x] `make generate` run
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(store): add the platform queue`

#### T3: Platform queue service [core]

**What**: New package `internal/platform` with:
- `Enqueue`: dedup, and the 5,000 cap with the dropped counter;
- `Unacked`: oldest first;
- `Ack`: clears the text and is idempotent;
- `TakeDropped`;
- `MoveConnector`: a tx helper;
- `events.PlatformQueued`.
**Where**: `internal/platform/queue.go`
**Depends on**: T2
**Reuses**: `store.WithTx`, `events.Bus`
**Requirement**: CID-08, CID-09, CID-11, CID-26
**Done when**:
- [x] Integration tests:
  - the same message twice → one item;
  - the 5,001st item drops the oldest, and the counter reads 1;
  - `Unacked` orders by seq;
  - `Ack` clears the text and keeps the metadata;
  - an ack by another agent is a no-op;
  - `MoveConnector` moves acked and unacked items of one connection only
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(platform): queue platform messages per AI`

### Phase 3: Inbound to the queue

```
T5 -> T6 -> T7
T7 -> T8
T7 -> T9
T7 -> T10
T7 -> T11
```

### Phase 4: Delivery to the session

```
T12 -> T14 -> T16
T13 -> T15
```

### Phase 5: Answering through the connection

```
T17 -> T18
T17 -> T19 -> T20
```

### Phase 6: Lifecycle and guards

```
T21 -> T22
T23
T24
```

### Phase 7: Web

```
T25
T26
T27
T28
T29
T30
T31
T32
```

### Phase 8: Server retirement

```
T33 -> T35
T34 -> T35
```

### Phase 9: Upgrade and release

```
T36 -> T37 -> T38
```

---

## Task Breakdown

### Phase 1: Done

#### T1: WhatsApp connection uses exactly one instance [done]

**What**: `instances` is required. With zero or several instances the connection reports `StatusError` with the detail `choose the instance (number) of this connection`, dials nothing and waits for ctx. Sends, reactions and chat listings use only the configured instance.
**Where**: `internal/connectors/whatsapp/connector.go`
**Depends on**: None
**Reuses**: the fake evolution server
**Requirement**: CID-04
**Done when**:
- [x] Implemented and tested in commit `bebd8d1` on branch `wnexous/pl-cid-t27` (contract notes there); merge that branch before T2
- [x] Gate check passed: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(whatsapp): one instance per connection` (bebd8d1)

### Phase 2: Store and queue

#### T2: Migration 0017 for the platform queue [core]

**What**: `0017_platform_queue.sql` adds:
- `platform_items`, `platform_attachments` and `platform_sends` with the design's indexes;
- `agents.platform_dropped`;
- `connectors.deliver` (default `all`) and `connectors.groups_mentions_only` (default 1).

It also adds the sqlc queries the queue needs.
**Where**: `internal/store/migrations/0017_platform_queue.sql`
**Depends on**: None
**Reuses**: AD-012 `ADD COLUMN` pattern, `openMigratedBefore`
**Requirement**: CID-08, CID-26, CID-27
**Done when**:
- [x] Store test: the tables and columns exist; existing connectors read `deliver=all`, `groups_mentions_only=1` (CID-27); the dedupe index refuses a second `(connector, chat, external_id)` row and accepts empty external ids twice
- [x] `make generate` run
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(store): add the platform queue`

#### T3: Platform queue service [core]

**What**: New package `internal/platform` with:
- `Enqueue`: dedup, and the 5,000 cap with the dropped counter;
- `Unacked`: oldest first;
- `Ack`: clears the text and is idempotent;
- `TakeDropped`;
- `MoveConnector`: a tx helper;
- `events.PlatformQueued`.
**Where**: `internal/platform/queue.go`
**Depends on**: T2
**Reuses**: `store.WithTx`, `events.Bus`
**Requirement**: CID-08, CID-09, CID-11, CID-26
**Done when**:
- [x] Integration tests:
  - the same message twice → one item;
  - the 5,001st item drops the oldest, and the counter reads 1;
  - `Unacked` orders by seq;
  - `Ack` clears the text and keeps the metadata;
  - an ack by another agent is a no-op;
  - `MoveConnector` moves acked and unacked items of one connection only
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(platform): queue platform messages per AI`

#### T4: Purge job and blob references [core]

**What**: `Queue.Purge`:
- deletes unacked items older than 7 days and counts them as dropped;
- deletes acked items older than 24 h with their attachments.

The retention ticker runs it, and the blob `Sweep` treats `platform_attachments` as references.
**Where**: `internal/rooms/retention.go`
**Depends on**: T3
**Reuses**: `attachments.Store.Sweep`
**Requirement**: CID-09, CID-10
**Done when**:
- [ ] Fake-clock tests:
  - an unacked item at 7 d − 1 ms stays, at 7 d it is deleted and counted;
  - an acked item at 24 h − 1 ms keeps its blob, at 24 h the row and the unshared blob go;
  - a blob shared with a room attachment survives
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(platform): expire queue items and their files`

### Phase 3: Inbound to the queue

#### T5: Operator must be an AI; no operator, no run [core]

**What**: `Service.operator` accepts only an AI the owner owns; any other value gets 422 on `operator_id`. `Manager.Apply` does not run a connection whose operator is NULL: it persists status `error` with the detail `choose the AI that operates this connection`, and setting the operator applies the connection again.
**Where**: `internal/connectors/manager.go`
**Depends on**: None
**Reuses**: `Manager.persist`, `operatorID`
**Requirement**: CID-01, CID-02
**Done when**:
- [x] e2e: the owner's AI is accepted; the owner's own id, "" and another user's AI → 422 `operator_id` (L-017)
- [x] Integration: NULL operator → not run, exact detail; PATCH to an AI → runs
- [x] ACC-01 owner-operator tests rewritten (owner operator retired)
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(connectors)!: a connection is operated by an AI`

#### T6: Connection settings and queued count [core]

**What**: The `Connector` DTO gains `deliver`, `groups_mentions_only` and `queued` (the unacked count). PATCH validates `deliver` (`dms|groups|all`). `Create` always stores the CID-27 defaults, because the create DTO has no such fields.
**Where**: `internal/connectors/service.go`
**Depends on**: T5
**Reuses**: `view`, `attachOperators`
**Requirement**: CID-23, CID-27
**Done when**:
- [x] e2e:
  - a new connection reads `deliver=all`, `groups_mentions_only=true`, even when the create body carries other values;
  - PATCH changes both, and an invalid `deliver` → 422;
  - `queued` counts unacked items only;
  - no field carries message text
- [x] `make generate` run
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(connectors): delivery settings and queued count`

#### T7: Pipeline delivers to the operator's queue [core]

**What**: `Pipeline.handle` runs the design's order:
1. pairing;
2. chat upsert;
3. no operator → drop;
4. echo or `from_self`;
5. `deliver` filter;
6. mentions-only filter (with the central `RepliesToAccount`);
7. trust, media to blobs, `Queue.Enqueue`.

Room posting, people and DM creation, `autoGroup`, `autoRoute` and `platformDM` are removed from the pipeline.
**Where**: `internal/connectors/inbound.go`
**Depends on**: T6
**Reuses**: `UpsertChat`, `saveMedia`, `truncate`, `SentIDs`, `identities.ResolveSender`
**Requirement**: CID-06, CID-07, CID-12, CID-13, CID-14, CID-26
**Done when**:
- [x] Integration:
  - group and DM events carry every CID-06 field;
  - no message, person or room is created (CID-07);
  - the `deliver` and mentions-only filters: each condition excludes on its own (L-042);
  - trust matrix (CID-13);
  - an echo is dropped, own typing is `from_self` (CID-14);
  - a repeat is queued once (CID-26)
- [x] Inbound/auto-group/catch-all/platform-DM tests deleted or rewritten (ROUTE-01..11, ACC-02, ACC-05, ACC-06, PEOP-01 creation retired)
- [x] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(connectors)!: deliver inbound messages to the operator's queue`

#### T8: Telegram mentions [small]

**What**: The Telegram adapter sets `Inbound.MentionsAccount` for a `mention` entity equal to the bot's `@username` or a `text_mention` of the bot id.
**Where**: `internal/connectors/telegram/connector.go`
**Depends on**: T7
**Reuses**: captured update payloads
**Requirement**: CID-12
**Done when**:
- [ ] Tests: mention, text_mention, another bot's mention (false), no entities (false)
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(telegram): report mentions of the bot`

#### T9: Discord mentions [small]

**What**: The Discord adapter sets `MentionsAccount` when the bot user id is in `mentions`.
**Where**: `internal/connectors/discord/connector.go`
**Depends on**: T7
**Reuses**: captured gateway payloads
**Requirement**: CID-12
**Done when**:
- [ ] Tests: bot mentioned, other user mentioned (false), `@everyone` only (false)
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(discord): report mentions of the bot`

#### T10: WhatsApp mentions [small]

**What**: The WhatsApp adapter sets `MentionsAccount` when the instance jid (or its LID) is in `contextInfo.mentionedJid`.
**Where**: `internal/connectors/whatsapp/connector.go`
**Depends on**: T7
**Reuses**: captured evolution-go frames, `instanceNumber`
**Requirement**: CID-12
**Done when**:
- [ ] Tests: own number mentioned, another number mentioned (false), no context (false)
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(whatsapp): report mentions of the number`

#### T11: Webhook mentions field [small]

**What**: The webhook inbound payload accepts the boolean `mentions_account` and maps it to `MentionsAccount`; the field is documented in its schema help.
**Where**: `internal/connectors/webhook/connector.go`
**Depends on**: T7
**Reuses**: webhook handler tests
**Requirement**: CID-12
**Done when**:
- [ ] e2e: the payload with `mentions_account` true or false reaches the pipeline flag; a non-boolean value → 422
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(webhook): accept mentions_account`

### Phase 4: Delivery to the session

#### T12: Gateway pushes platform events [core]

**What**: The gateway handles platform events:
- it reads the opt-in `X-Partyline-Features: platform` header;
- on connect, after the room backlog, it sends the dropped notice when `TakeDropped` > 0, then the unacked items oldest first, at most 50 in flight;
- live pushes come from `events.PlatformQueued`;
- `platform_ack` → `Queue.Ack`;
- the welcome lists the operated connections.

New frame DTOs are added.
**Where**: `internal/agents/gateway.go`
**Depends on**: None
**Reuses**: `run`, `backlog`, `readAcks`, takeover machinery
**Requirement**: CID-06, CID-08, CID-09, CID-10, CID-25
**Done when**:
- [x] e2e WebSocket tests:
  - offline, 3 items → on connect, 3 frames oldest first (CID-08);
  - an ack clears the text (CID-09);
  - the dropped notice carries N and is sent once (CID-10);
  - 60 items → at most 50 unacked frames in flight;
  - after a takeover the new session gets the unacked item (CID-25);
  - a session without the header gets no platform frames
- [x] `make generate` run
- [x] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(agents): push platform events to the operator's session`

#### T13: Pending endpoint returns platform events [core]

**What**: `GET /agent/pending?platform=1` adds a `platform` list of unacked events (oldest first, within the existing limit) and acknowledges them when it returns them. Without the parameter the response is unchanged.
**Where**: `internal/api/agent_handlers.go`
**Depends on**: None
**Reuses**: `agentPending`, `Queue.Unacked/Ack`
**Requirement**: CID-06, CID-08, CID-09
**Done when**:
- [ ] e2e: with `platform=1`, the events are returned and acked; without it they are neither returned nor acked; `wait` still long-polls
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): pending returns platform events on request`

#### T14: Channel and listen show platform events [core]

**What**: The channel link sends the feature header and turns a `platform` frame into one notification with the design's meta (`author`, `author_kind=platform`, `room_name`, `message_id`, `trusted`, `chat_id`, `chat_kind`, `connection_id`, `from_self`), then acks with `platform_ack`. `listen` writes the same record with `type=platform`. The identity notice lists the operated connections.
**Where**: `internal/channel/link.go`
**Depends on**: T12
**Reuses**: `handle`, `seenBefore`, `Format`, `lineWriter`
**Requirement**: CID-06, CID-25
**Done when**:
- [ ] Channel e2e: notification meta and content for a group and a DM event; ack after notify; a duplicate id is notified once
- [ ] listen e2e: the JSON line has the record
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(channel): deliver platform events to the harness`

#### T15: Hooks and wait tool include platform events [small]

**What**: `channel.Pending` asks for `platform=1`. `FrameAll` frames platform events with the same trust line, and `wait_for_messages` and `partyline hook` show them.
**Where**: `internal/channel/pending.go`
**Depends on**: T13
**Reuses**: `FrameAll`, hook tests
**Requirement**: CID-06
**Done when**:
- [ ] e2e: the `codex-stop` hook prints a block decision whose reason contains the platform event; `wait_for_messages` returns it
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(cli): hooks show platform events`

#### T16: Adapter framing covers platform records [small]

**What**: Add a platform record (trusted and untrusted) to `integrations/testdata/framing.ts`. Every adapter's test runs it. The adapters need no change, because they frame from `author`, `room_name`, `trusted` and `content`.
**Where**: `integrations/testdata/framing.ts`
**Depends on**: T14
**Reuses**: shared framing cases
**Requirement**: CID-06
**Done when**:
- [ ] `make integrations-test` passes with the new cases for pi, OpenCode and omp
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `test(integrations): frame platform events`

### Phase 5: Answering through the connection

#### T17: Platform sends with retries [core]

**What**: `connectors.Sends.Send` makes one synchronous attempt (15 s), always unsigned. A failure stays pending with its reason. A worker retries with the outbox backoff and marks `failed` at the limit. The text is cleared at `sent` or `failed`. A sent external id joins `SentIDs`, and the pipeline's echo check reads `platform_sends`.
**Where**: `internal/connectors/sends.go`
**Depends on**: None
**Reuses**: `delivery/outbox.go` claim/backoff pattern, `Manager.Send`
**Requirement**: CID-14, CID-15, CID-16, CID-18
**Done when**:
- [ ] Integration:
  - a success is sent unsigned with its id recorded;
  - a failure is retried until it succeeds, and the text is cleared;
  - after the attempt limit the send is `failed` and the text is cleared;
  - an echo of a send is not queued
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(connectors): send through a connection with retries`

#### T18: `reply` to a platform message [core]

**What**: `POST /agent/messages/{id}/reply` falls back to the caller's platform item and sends through `Sends.Send` with `replyTo` = the item's external id. It answers 201 when sent, 202 with an error when queued, 404 when unknown or expired.
**Where**: `internal/api/agent_handlers.go`
**Depends on**: T17
**Reuses**: `agentReply`
**Requirement**: CID-15, CID-18
**Done when**:
- [ ] e2e:
  - the fake connector gets the text, `Unsigned` and `ReplyToID`;
  - a failing platform → 202 with the reason;
  - another AI's item → 404;
  - room replies are unchanged
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): reply to platform messages`

#### T19: Send to a connection chat [core]

**What**: `POST /agent/connections/{id}/chats/{chat}/messages {text}` answers:
- 403 `not_operator` for an AI whose owner can see the connection;
- 404 for an AI that cannot see it;
- 404 `unknown_chat` for a chat of another connection;
- otherwise it calls `Sends.Send`.
**Where**: `internal/api/agent_handlers.go`
**Depends on**: T17
**Reuses**: `access.CanUseConnector`
**Requirement**: CID-03, CID-16, CID-17
**Done when**:
- [ ] e2e: operator sends (201, unsigned); another AI of the owner → 403 `not_operator`; another user's AI → 404; a foreign chat → 404 `unknown_chat`; nothing sent in the refusals (L-017, L-018)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api): send to a chat of the AI's connection`

#### T20: `send_message` names a connection chat [core]

**What**: The channel tool gains `connection` and `chat` and requires exactly one of `room`, `to` or (`connection` + `chat`). `chat_ids` leaves the schema and is still accepted and ignored. The instructions explain platform events and how to answer, and drop the chat_ids, broadcast and bridge sentences. `react` and `send_file` with a platform id answer `unsupported`.
**Where**: `internal/channel/tools.go`
**Depends on**: T19
**Reuses**: tool tests, instructions golden
**Requirement**: CID-16, CID-22
**Done when**:
- [ ] Channel e2e:
  - connection+chat is sent;
  - two targets → tool error;
  - a call with `chat_ids` still posts to the room;
  - the schema has no `chat_ids`;
  - the instructions golden is updated
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(channel): answer on a connection chat`

### Phase 6: Lifecycle and guards

#### T21: Operator change moves the queue [core]

**What**: When the operator changes, `Service.Update` runs `Queue.MoveConnector` and one `connector.operator` audit entry `{from, to, moved}` in one tx. After commit it publishes `PlatformQueued` for the new AI and re-applies the connection.
**Where**: `internal/connectors/service.go`
**Depends on**: None
**Reuses**: existing operator audit, T3
**Requirement**: CID-05
**Done when**:
- [ ] e2e: the new operator's session receives the moved items; the old one's acks are no-ops; exactly one audit entry; a forced failure leaves items and operator unchanged
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(connectors): move the queue with the operator`

#### T22: Deleted or merged operator [core]

**What**: `Registry.Delete` publishes `ConnectorsOrphaned` for the connections the AI operated, and the manager re-applies them; the queue goes through its FK cascade. A merge moves `platform_items` to the surviving AI.
**Where**: `internal/agents/registry.go`
**Depends on**: T21
**Reuses**: `ListConnectorsOperatedBy`, `ReassignConnectorOperator`
**Requirement**: CID-24, CID-05
**Done when**:
- [ ] Integration: delete → items gone, `operator_iid` NULL, the connection shows the CID-02 detail; merge → items belong to the surviving AI
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(agents): stop connections of a deleted operator`

#### T23: Chats are never members [small]

**What**: `AddMember`, `CreateWithMembers` and group creation refuse kind `chat` with `ErrChatsNotMembers`, which maps to 422 `chats_not_members`.
**Where**: `internal/rooms/members.go`
**Depends on**: None
**Reuses**: `ErrDMMembersFixed` pattern
**Requirement**: CID-19
**Done when**:
- [ ] e2e: `POST /rooms/{id}/members {kind: chat}` with an otherwise valid request → 422 `chats_not_members` (L-017, L-018)
- [ ] Chat-member tests deleted (ACL-06 chat branch retired)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(rooms): refuse chats as members`

#### T24: Platform DMs and people are history [small]

**What**: A post in a DM with a person participant gets `ErrHistoryOnly` (422 `history_only`), and so does an edit of a person's profile. `send_message to:@person` answers `no_route_to_person`.
**Where**: `internal/rooms/messages.go`
**Depends on**: None
**Reuses**: `checkCounterpart`, `intelligences.Service.Update`
**Requirement**: CID-21, CID-22
**Done when**:
- [ ] e2e: AI post in its old platform DM → 422 `history_only`; edit of a person → 422 `history_only`; DM to a person → `no_route_to_person`; reading history still works
- [ ] DM-05 and PEOP-05/PROF-11 person-edit tests rewritten (retired)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(rooms): platform DMs and people are read-only history`

### Phase 7: Web

#### T25: Member picker offers users and AIs only [small]

**What**: Remove the chats section and the `useChats` (GET /chats) dependency.
**Where**: `web/src/components/member-picker.tsx`
**Depends on**: None
**Reuses**: picker tests
**Requirement**: CID-20
**Done when**:
- [ ] Unit: users and AIs, no chat entries
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): add only users and AIs to groups`

#### T26: Member list and settings without chats [small]

**What**: The member list shows no chat rows. Member settings lose signature and outbound.
**Where**: `web/src/components/member-list.tsx`
**Depends on**: None
**Reuses**: `member-settings.tsx`
**Requirement**: CID-20, CID-22
**Done when**:
- [ ] Unit: no chat row, no signature/outbound controls
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): members are intelligences only`

#### T27: Composer without chat recipients [small]

**What**: Remove the chat recipients picker and its listen-only error mapping.
**Where**: `web/src/components/composer.tsx`
**Depends on**: None
**Reuses**: composer tests
**Requirement**: CID-22
**Done when**:
- [ ] Unit: no recipients control; post body carries no `recipients`
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): composer without chat recipients`

#### T28: Room settings without chat settings [small]

**What**: Remove the bridge and broadcast toggles and the catch-all notice.
**Where**: `web/src/components/room-settings-sheet.tsx`
**Depends on**: None
**Reuses**: settings tests
**Requirement**: CID-22
**Done when**:
- [ ] Unit: no bridge/broadcast fields; a save sends neither
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): drop chat settings from rooms`

#### T29: Connection page as an AI's app [small]

**What**: The connection page:
- shows an operator select (the owner's AIs only, with the CID-02 hint when none), `deliver`, `groups_mentions_only`, status and `queued`;
- removes `RouteChatDialog`, the catch-all and scope controls, and the per-chat route action;
- shows no message text.

Strings come in `en` and `pt-BR`.
**Where**: `web/src/routes/_app/connectors/$connectorId.tsx`
**Depends on**: None
**Reuses**: `connector-catch-all.tsx` (removed), `schema-form.tsx`
**Requirement**: CID-23, CID-27, CID-22
**Done when**:
- [ ] Unit: fields render and save; queued count shown; no route action; `route-chat-dialog.tsx` and its test removed (UI-13 retired)
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): connection page for an AI's app`

#### T30: Platform DM history banner [small]

**What**: A DM with a person shows "History: answer this person on {platform}" instead of the composer, and maps a `history_only` error to the same text.
**Where**: `web/src/routes/_app/rooms/$roomId.tsx`
**Depends on**: None
**Reuses**: `conversation-header.tsx`
**Requirement**: CID-21, CID-22
**Done when**:
- [ ] Unit: a person DM shows the banner and no input; an AI–user DM is unchanged
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): platform DMs read as history`

#### T31: Settings without app notifications [small]

**What**: Remove the "notify me here" choice and its queries.
**Where**: `web/src/routes/_app/settings.tsx`
**Depends on**: None
**Reuses**: settings tests
**Requirement**: CID-22
**Done when**:
- [ ] Unit: no notify control; no request to `/me/notify*`
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): retire app notification settings`

#### T32: Timeline without chat delivery status [small]

**What**: Message items stop reading `message.outbox` (delivery to chats).
**Where**: `web/src/components/message-item.tsx`
**Depends on**: None
**Reuses**: timeline tests
**Requirement**: CID-22
**Done when**:
- [ ] Unit: messages render without delivery badges; chat-authored history still shows its platform badge
- [ ] Gate check passes: `make check`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): drop chat delivery status from the timeline`

### Phase 8: Server retirement

#### T33: Rooms no longer deliver to chats [core]

**What**: `delivery.Plan` returns agents only, and `rooms.insert` creates no outbox. Removed: the room outbox worker, the `Sender` room path, notify copies, `applyNoBroadcast`, `NoBroadcastWarning` and recipients checks. `recipients` is still accepted and ignored.
**Where**: `internal/rooms/messages.go`
**Depends on**: None
**Reuses**: none
**Requirement**: CID-22
**Done when**:
- [ ] e2e: a post with `recipients` (an old CLI) succeeds and creates no outbox; agents still receive room messages
- [ ] Tests of DLV-03..05, ROUTE-06, ROUTE-10, NOTI-02/03, MEM-01 deleted (retired)
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(rooms)!: rooms no longer send to chats`

#### T34: Retire routing and notify endpoints [small]

**What**: Remove `POST /chats/{id}/route`, `GET /chats`, `GET /me/notify-options`, `PUT /me/notify` and their services.
**Where**: `internal/api/chats_handlers.go`
**Depends on**: None
**Reuses**: none
**Requirement**: CID-22
**Done when**:
- [ ] e2e: each route answers 404/405
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api)!: retire chat routing and app notifications`

#### T35: Retire chat-related DTO fields [small]

**What**: Remove these DTO fields:
- `bridge_chats`, `broadcast` and `catch_all` (Room and its requests);
- `sign_outbound` and member `outbound`;
- `Message.outbox`, `notify_chat_id` and `auto_route_*`.

A request carrying them changes nothing.
**Where**: `internal/api/dto/rooms.go`
**Depends on**: T33, T34
**Reuses**: none
**Requirement**: CID-22
**Done when**:
- [ ] e2e: GET responses lack the fields; PATCH with them changes nothing
- [ ] `make generate` run
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `feat(api)!: retire chat fields from the API`

### Phase 9: Upgrade and release

#### T36: Upgrade migration 0018 [core]

**What**: `0018_connection_proxy.sql`:
- deletes every `room_members` row of kind `chat`;
- clears `auto_route_room_id` and `notify_chat_id`;
- drops `outbox` and `auto_route_skips`;
- drops `connectors.auto_route_scope`, `rooms.bridge_chats` and `rooms.broadcast`.
**Where**: `internal/store/migrations/0018_connection_proxy.sql`
**Depends on**: None
**Reuses**: v0.4 fixture, `openMigratedBefore`
**Requirement**: CID-21, CID-27
**Done when**:
- [ ] Store test from the pre-0017 fixture:
  - chat members are gone;
  - messages, intelligences, settings, people and DM rooms are kept;
  - connections read `deliver=all`, `groups_mentions_only=1` whatever their old scope (CID-27);
  - NULL operators stay NULL;
  - the dropped tables and columns are absent;
  - a failure rolls back
- [ ] Gate check passes: `make check`
**Tests**: integration
**Gate**: build
**Commit**: `feat(store)!: upgrade to the connection proxy`

#### T37: Upgrade end to end [core]

**What**: An e2e test boots the app on the v0.4 fixture and checks the CID-21 independent test through the API.
**Where**: `internal/api/upgrade_test.go`
**Depends on**: T36
**Reuses**: upgrade e2e harness
**Requirement**: CID-21
**Done when**:
- [ ] The room that held a WhatsApp chat lists the owner and the two AIs and no chat
- [ ] After choosing the operator, the next message in that WhatsApp group (with a mention) reaches only the operator's session and no room
- [ ] `make e2e` passes
- [ ] Gate check passes: `make check`
**Tests**: e2e
**Gate**: build
**Commit**: `test(api): pin the connection proxy upgrade`

#### T38: Docs and v0.5.0 release notes [small]

**What**: Update the READMEs and `docs/agents.md` to the proxy model, and write the breaking-change notes:
- the operator must be an AI, and owner-operated connections go idle until one is chosen;
- the defaults `deliver=all` and `groups_mentions_only=on`;
- the retired features;
- the CLI upgrade needed for platform events;
- the end of app notifications (web push follows in v0.5.1).
**Where**: `README.md`
**Depends on**: T37
**Reuses**: none
**Requirement**: CID-22, CID-27
**Done when**:
- [ ] No doc mentions route-to-agent, catch-all rooms, bridges, `chat_ids` or notify copies; no private names
- [ ] Gate check passes: `make check`
**Tests**: none
**Gate**: build
**Commit**: `docs: partyline as a proxy (v0.5.0)`

---

## Phase Execution Map

```
Phase 1 (done) -> Phase 2 -> Phase 3 -> Phase 4 -> Phase 5 -> Phase 6 -> Phase 7 -> Phase 8 -> Phase 9
```

Batches (~7 tasks, whole phases): [P2 + P3] 10, [P4] 5, [P5 + P6] 8, [P7] 8, [P8 + P9] 6.

- **Cheaper worker**: Phase 7 (web) and the [small] adapter tasks T8-T11.
- **High-reasoning worker**: Phases 2-5, T21/T22, T33 and T36/T37.

The web phase runs before Phase 8, so the TypeScript build never references a removed DTO
field.

---

## Task Granularity Check

| Task | Scope | Status |
| --- | --- | --- |
| T1 | done | ✅ |
| T2-T4 | migration, service, purge job | ✅ |
| T5-T11 | one rule / one adapter each | ✅ |
| T12-T16 | gateway, endpoint, channel, hook, test data | ✅ |
| T17-T20 | send service, two endpoints, tool | ✅ |
| T21-T24 | one call site or guard each | ✅ |
| T25-T32 | one component or page each | ✅ |
| T33-T35 | one retired layer each | ✅ |
| T36-T38 | migration, e2e, docs | ✅ |

## Diagram-Definition Cross-Check

| Task | Depends On (task body) | Diagram Shows | Status |
| --- | --- | --- | --- |
| T1 | None | — | ✅ |
| T2 | None | — | ✅ |
| T3 | T2 | T2 -> T3 | ✅ |
| T4 | T3 | T3 -> T4 | ✅ |
| T5 | None | — | ✅ |
| T6 | T5 | T5 -> T6 | ✅ |
| T7 | T6 | T6 -> T7 | ✅ |
| T8, T9, T10, T11 | T7 | T7 -> each | ✅ |
| T12, T13 | None | — | ✅ |
| T14 | T12 | T12 -> T14 | ✅ |
| T15 | T13 | T13 -> T15 | ✅ |
| T16 | T14 | T14 -> T16 | ✅ |
| T17 | None | — | ✅ |
| T18 | T17 | T17 -> T18 | ✅ |
| T19 | T17 | T17 -> T19 | ✅ |
| T20 | T19 | T19 -> T20 | ✅ |
| T21 | None | — | ✅ |
| T22 | T21 | T21 -> T22 | ✅ |
| T23, T24 | None | — | ✅ |
| T25-T32 | None | — | ✅ |
| T33, T34 | None | — | ✅ |
| T35 | T33, T34 | T33 -> T35, T34 -> T35 | ✅ |
| T36 | None | — | ✅ |
| T37 | T36 | T36 -> T37 | ✅ |
| T38 | T37 | T37 -> T38 | ✅ |

## Test Co-location Validation

| Task | Code Layer Created/Modified | Matrix Requires | Task Says | Status |
| --- | --- | --- | --- | --- |
| T1 | platform adapter | integration | integration | ✅ |
| T2, T36 | store / migrations | integration | integration | ✅ |
| T3 | queue service | integration | integration | ✅ |
| T4, T7, T17, T22 | services | integration | integration | ✅ |
| T5, T6, T12, T13, T14, T15, T18, T19, T20, T21, T23, T24, T33, T34, T35, T37 | routes / WS / channel / CLI | e2e | e2e | ✅ |
| T8, T9, T10 | platform adapters | integration | integration | ✅ |
| T11 | webhook route | e2e | e2e | ✅ |
| T16 | TS adapters | unit | unit | ✅ |
| T25-T32 | web | unit | unit | ✅ |
| T38 | docs | none | none | ✅ |
