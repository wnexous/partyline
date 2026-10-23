# Connection Identity Contract Notes

Binding for later tasks, the web app and the Verifier. Each task adds its section to this file.
Where a section refines the design, it says so.

## T1 (T27 of the first design): WhatsApp connection uses exactly one instance (CID-04)

- **Config**: `instances` (string list) becomes `Required`. It is not renamed, so there is no
  config migration. Its help text is `exactly one instance: the number of this connection`.
  A create or update that names zero instances is refused by the schema with `required`.
- **Runtime shape**: `Connector` keeps one instance name (`instance`), not a set. The name is the
  single item of `instances`; it is empty when a stored config names zero or several instances.
  `New` always succeeds, so `Run` can report why the connection is idle.
- **Idle connection**: `Run` with zero or several instances reports
  `StatusError` with the exact detail `choose the instance (number) of this connection`. It makes
  no REST call, dials no WebSocket, receives nothing, and blocks until ctx ends (no error return,
  so the manager does not restart it with a backoff). With exactly one instance it connects as
  before.
- **Receive filter**: `handle` delivers a frame only when its `instanceName` is the connection's
  instance. Messages of other instances on the same evolution-go server are ignored.
- **Send**: `Send` returns `ErrOtherInstance` (`chat of another instance`) before opening files or
  calling the platform when the chat id's instance prefix is not the connection's instance. An
  ambiguous config (zero or several instances) refuses every chat. `React` and the internal
  token lookup use the same rule, so reactions never leave through another number.
- **Chat listing**: `ListChats` lists `GET /group/list` only for the connection's instance. With an
  ambiguous config it returns no chats.
- **Account label**: the label is the masked phone number of the connection's instance only. The
  device suffix is dropped; a LID or a missing jid yields an empty label.
- **Retired**: the empty allowlist meaning "use every instance with WebSocket events enabled",
  the aggregation of several numbers into one account label, and the `idleWarning` branch for a
  server with no WebSocket-enabled instance. Their tests are deleted with them.
  `idleWarning` survives only for `evolution-go has no instance named X`, which is not that
  retired branch: the rule is now about the one configured instance and is still shown with the
  connected status.

## T2: Migration 0017, the platform queue schema (CID-08, CID-26, CID-27)

- **Tables** (`0017_platform_queue.sql`, additive, no rebuild):
  - `platform_items`: `seq` (AUTOINCREMENT, never reused), `id` (unique), `agent_id`,
    `connector_id`, `chat_id` (all `ON DELETE CASCADE`), `external_id`, `sender_id`,
    `sender_name`, `trusted`, `from_self`, `kind`, `text`, `received_at`, `acked_at` (NULL while
    unacknowledged). Times are unix milliseconds.
  - `platform_attachments`: `id`, `item_id` (cascade), `sha256` (`''` = no blob), `name`, `mime`,
    `size`, `status` (`stored|too_large|unavailable`, as for room attachments).
  - `platform_sends`: the design's columns. `status` is checked against
    `pending|sending|sent|failed`. Rows go with their connection, chat and AI.
  - `agents.platform_dropped` (default 0), `connectors.deliver` (default `all`, checked against
    `dms|groups|all`) and `connectors.groups_mentions_only` (default 1).
- **Dedupe (CID-26)**: the unique index is `(connector_id, chat_id, external_id) WHERE
  external_id <> ''`. It does not name the AI, so moving a connection to another operator can never
  duplicate an item, and a platform message without an id never collides.
- **Indexes beyond the design**: `platform_items(connector_id, acked_at)` (the `queued` count and the
  move), `platform_items(chat_id)`, `platform_attachments(item_id)` and the `platform_sends` ones
  (`due`, `(chat_id, external_id)` for the echo check, `agent_id`, `connector_id`). They back the
  foreign-key cascades and the lookups of later tasks; they change no behaviour.
- **Defaults (CID-27)**: existing connections read `deliver=all` and `groups_mentions_only=1`
  whatever their old `auto_route_scope`; the column defaults are the single source (T6 makes
  `Create` rely on them).
- **sqlc queries** (`queries/platform.sql`), used by the queue service of T3:
  - `InsertPlatformItem` is `:one ... RETURNING seq` with `ON CONFLICT (connector_id, chat_id,
    external_id) WHERE external_id <> '' DO NOTHING`: a repeat returns `sql.ErrNoRows`, which the
    service reads as "already queued".
  - `ListUnackedPlatformItems` (oldest first, after a seq, joined with the chat and the connector
    kind as `platform`), `ListPlatformAttachmentsForItems`, `AckPlatformItem` (own AI only, once),
    `CountUnackedPlatformItems`, `DeleteUnackedPlatformItemsOverCap` (keeps the newest `cap`
    unacknowledged items; acknowledged ones are never counted), `AddPlatformDropped`,
    `GetPlatformDropped`, `ResetPlatformDropped` and `MovePlatformItems`.

## T3: Platform queue service (CID-08, CID-09, CID-11, CID-26)

- **Package** `internal/platform` (imports store, events and dto only):
  `platform.New(platform.Deps{Store, Bus, Now})` returns the `*Queue`. T7 wires it into the app.
- **Methods**:
  - `Enqueue(ctx, Item) (id string, queued bool, err error)`: inserts the item and its attachment
    rows in one transaction, trims the AI's queue, and publishes `events.PlatformQueued` after
    the commit. A repeat of the same `(connector, chat, platform id)` returns `("", false, nil)`
    and publishes nothing. The dedupe does not name the AI, and an acknowledged item still blocks
    a repeat until the purge deletes it (24 h after the ack). `Item.ChatID` is Partyline's chat
    id, which must belong to `Item.ConnectorID` (the caller guarantees it); an unknown AI, chat
    or connection is a foreign-key error and queues nothing.
  - `Unacked(ctx, agentID, afterSeq, limit) ([]Event, error)`: the AI's unacknowledged items with
    `seq > afterSeq`, oldest first, with their attachments.
  - `Get(ctx, agentID, itemID) (Event, error)`: an item of that AI, acknowledged or not
    (`platform.ErrNotFound` for an unknown id or another AI's item). Not in the design: `reply`
    (T18) needs the acknowledged items' metadata, and the Ack tests need to read them.
  - `Ack(ctx, agentID, itemID) error`: sets `acked_at` and clears `text`. Idempotent; an unknown
    item and another AI's item are no-ops and not errors; the first ack time is kept.
  - `TakeDropped(ctx, agentID) (int64, error)`: reads the AI's dropped counter and zeroes it in
    one transaction; 0 for an unknown AI.
  - `MoveConnector(ctx, tx *db.Queries, connectorID, toAgent) (moved int64, err)`: runs in the
    caller's transaction, moves every item of the connection (acked or not, from any AI) to
    `toAgent` and returns how many changed hands. It publishes nothing: the caller sends
    `events.PlatformQueued{AgentID: toAgent}` (empty `ItemID`) after it commits (T21).
- **`Event`** is the internal shape of a platform event: `Seq`, `ID` (the id the AI replies
  with), `Platform` (the connection's kind), `ConnectionID`, `ChatID` (Partyline's), `ChatTitle`,
  `ChatKind`, `ExternalID` (the platform's message id, never sent to the session), `SenderName`,
  `SenderID`, `Trusted`, `FromSelf`, `Kind`, `Text`, `Attachments`, `ReceivedAt` and `AckedAt`
  (zero while unacknowledged). T12 maps it to `dto.PlatformEvent`.
- **`events.PlatformQueued{AgentID, ItemID, Seq}`**: `Seq` is an addition to the design's
  `{AgentID, ItemID}`; the gateway uses it to skip items it already sent (L-050).
- **Bound** (`platform.MaxQueued = 5000`, CID-11): counts the AI's unacknowledged items only.
  Acknowledged items and other AIs' items do not use it up. Refinement of the design, which
  bounds `Enqueue` only: `MoveConnector` trims the receiving AI to the bound too (the oldest
  unacknowledged items go and are counted for that AI), so no operator change makes a queue
  unbounded. A dropped item's blobs are not deleted here: with no attachment row referencing
  them they fall to the blob sweep (T4).
- **sqlc**: `ListUnackedPlatformItems` and `GetPlatformItemForAgent` embed the `platform_items`
  row (`sqlc.embed`) next to `chat_title`, `chat_kind` and `platform`.

## T4: Purge job and blob references (CID-09, CID-10)

- **`Queue.Purge(ctx, now) (PurgeResult, error)`** (`internal/platform/purge.go`), in bounded
  transactions of 1,000 items:
  - an **unacknowledged** item is deleted when `received_at <= now - UnackedTTL` (7 days) and
    counted in its AI's dropped counter, per AI (`TakeDropped` reports it, CID-10);
  - an **acknowledged** item is deleted when `acked_at <= now - AckedTTL` (24 hours), so the
    24 hours run from the ack, not from the arrival. It is not counted as dropped (it was
    delivered), and an old acknowledged item is never expired by the 7 days. The file rows go
    with the item (foreign key cascade);
  - both boundaries are inclusive: one millisecond short keeps the item, the exact age deletes it.
  - `PurgeResult{Expired, Acked, Files}`: `Files` lists the blob hashes the deleted items' files
    used, each once.
- **Blobs**: `Purge` deletes rows only. `rooms.Service.ReleaseBlobs(ctx, shas)` (the former
  unexported `removeRoomBlobs`, now also used for platform files) deletes the listed blobs that no
  message attachment, platform file or avatar references and that were not written in the last
  minute. The daily retention sweep (`blobReferenced`) counts `platform_attachments` as
  references and stays the backstop for blobs nothing released: items dropped by the 5,000 bound,
  and queues deleted with an AI, a connection or a chat.
- **Runner (deviation from the design)**: `Queue.Run(ctx, interval, release, log)` purges once at
  start and then every interval, handing `Files` to `release`. The app runs it as its own
  background worker `platform-purge` every `platform.PurgeInterval` (10 minutes) with
  `Rooms.ReleaseBlobs`, instead of as a `RetentionTask` of the daily retention job: CID-09 and
  CID-10 say 24 hours and 7 days, and a daily pass deletes up to a day late (files 48 hours after
  the ack, items after 8 days) and would run after that day's blob sweep. The retention job is
  unchanged. Nothing but counts is logged.
- **`App.Platform`** (`*platform.Queue`) is built in `app.New` for the later tasks (pipeline,
  gateway, connector service).
- **Not covered here**: `platform_sends` rows are not purged (T17 owns that table's lifecycle;
  its text is cleared at `sent` or `failed`).

## T5: Operator must be an AI; no operator, no run (CID-01, CID-02)

- **`operator_id` (PATCH /connectors/{id})** accepts only the id of an AI the connection's owner
  owns. Everything else is a 422 with `fields: {operator_id: "must be an AI you own"}` and changes
  nothing in the same request: an empty or blank id, the owner's own id, another user, an AI of
  another owner (also one shared with the owner), a person, an id nobody has. Absent leaves the
  operator alone. The operator cannot be cleared through the API: it becomes NULL only when the
  operator AI is deleted (the foreign key sets it NULL; T22 completes that path).
- **Create** has no operator field: a new connection has none, so it is idle until the owner chooses
  one (the design's "creation may omit the operator").
- **DTO**: `Connector.operator` is absent while there is no operator and an AI (`role: ai`)
  otherwise; it never falls back to the owner. `connectors.View` has `HasOperator` instead of
  `OperatorAI`. Profiles: a user lists no operated connections; an AI's page lists the connections
  it operates, as before (`ListConnectorsOperatedByOwner` is removed).
- **Status detail** `dto.ConnectorNoOperator` (= `connectors.NoOperator`) is
  `choose the AI that operates this connection` (CID-02).
- **`Manager.Apply`**: an enabled connection with a NULL `operator_iid` is not started (no connector
  instance exists, so nothing is received and `Send` answers `ErrNotRunning`); it persists status
  `error` with that detail and publishes the `ConnectorStatus` event. A disabled connection keeps
  status `stopped` and an empty detail. `Start` applies every enabled connection the same way, and
  applying a running connection whose operator became NULL stops it and reports the detail.
- **`Service.Update`** applies the connection again (when enabled) the first time it gets an
  operator (NULL to an AI), which starts it. A change from one AI to another does not restart the
  connection: nothing the connector runs depends on the operator. This refines T21's "re-applies the
  connection"; T21 can still add the restart if it needs one.
- **Audit** `connector.operator`: `{kind, name, from, to}`, with `from` empty when there was none.
- **Retired with the owner as operator** (tests deleted or reduced to their AI cases, named here
  because the assertions they pinned no longer exist): `TestOwnAccountMessageIsTrustedOnlyWhenTheOwnerOperatesTheAccount`
  (AD-014's owner-operated trust), the owner-operated parts of `TestGroupChatsOfEveryKindAndOperator`,
  `TestPlatformDMFollowsTheOperator` ("a deleted operator AI means the owner"),
  `TestPlatformDMNeverMakesADMWithOneself` (now `TestPlatformDMFromTheOwnersLinkedAccountIsTrusted`),
  `TestSendToAPersonOnlyThroughTheOperator`, `TestOwnAccountMessageIsAuthoredByTheOperator`,
  `TestUsersAreSignedUnlessTheyOperateTheAccount`, `TestOperatorAIWritesUnsigned`,
  `TestMergeMovesTheOperatorOfConnectors`, `TestLinkedAccountsAreAppsOfTheUser` and the former
  `connector_operator_test.go` tests (owner default, owner picks themself, deleting the operator
  falls back to the owner). Their AI cases stay, with an AI as the default operator.
- **Test helpers**: `testkit.Client.NewAI(name)` and `Client.OperateConnector(connectorID, aiID)`;
  API tests `createFakeIdle` (no operator) and `createFake` (operated by the client's AI "Atlas");
  `connectors_test.newInboundEnv` operates its connector with the AI "relay"; the manager fixture's
  `insert` gives the connector an AI, `insertWithoutOperator` none.

## T6: Connection settings and queued count (CID-23, CID-27)

- **`dto.Connector`** gains `deliver` (`dms|groups|all`, type `dto.ConnectorDeliver`),
  `groups_mentions_only` (bool) and `queued` (integer). All three are always present. `queued` is
  the number of the connection's unacknowledged queue items, whichever AI holds them; acknowledged
  items and other connections' items do not count. The owner and the connection's use grantees see
  the same values in `GET /connectors/{id}`, `GET /connectors` and the answers of POST and PATCH.
  No connector view carries message text (there is no field for it); the items' text exists only in
  the queue until acknowledged.
- **`dto.UpdateConnectorRequest`** gains `deliver` and `groups_mentions_only` (both optional;
  absent leaves the stored value). A `deliver` other than the exact strings `dms`, `groups`, `all`
  (any other case, blank, padded) is a 422 `{deliver: "must be dms, groups or all"}` that changes
  nothing in the same request. Only the owner (manage) may change them: the same authorization as
  every other PATCH field (use grantee 403, stranger and admin 404, anonymous 401, AI tokens 403).
- **Create** takes no delivery fields: unknown JSON fields are ignored by the decoder, so a create
  body carrying `deliver`, `groups_mentions_only` or `queued` is accepted and they are not read. The
  column defaults of migration 0017 (`all`, on) are the single source (CID-27).
- **Audit**: a real change records `connector.update` with `changed` listing `deliver` and/or
  `groups_mentions_only` (in that order); a request that sets the stored values records nothing.
- **sqlc**: `UpdateConnectorDelivery`, `CountUnackedPlatformItemsByConnector` (one grouped query
  per list or view).
- **Web**: `web/src/test/fixtures.ts` `makeConnector` carries the three new fields so that the
  generated type still holds; the connection page itself is T29.

## T7: Pipeline delivers to the operator's queue (CID-02, CID-06, CID-07, CID-12, CID-13, CID-14, CID-26)

- **`Pipeline.Handle` order** (`internal/connectors/inbound.go`; deps are `Store, Bus, Blobs,
  Identities, Queue, Sent, Wait, MaxUploadBytes, Logger, Now`; `Rooms`, `Conversations`, `People` and
  `SetRouteHook` are gone):
  1. A pairing command (`!link CODE`, `/link CODE`) links the sender on this connector and stops.
     It records no chat. A message of the account itself is never a pairing command: it is ignored
     and not delivered.
  2. `UpsertChat` records every other message's chat (id, title, kind, last seen), delivered or not
     (`chat.discovered` goes out as before). A message with neither text nor media stops here.
  3. The connector row is read per message. No operator drops the message; so does an operator
     whose agent row vanished meanwhile (no error logged).
  4. A message of the account itself (`FromSelf`) first goes through `settle`: it waits up to
     `SelfHold` (10 s) while a send to the chat runs, and is dropped when its platform id is one
     Partyline sent (`SentIDs`, 5 minutes, or the outbox's recorded ids). Otherwise it goes on
     as the operator's own typing. T17 adds `platform_sends` to the ids `settle` and
     `repliesToAccount` read.
  5. `deliver`: `all` takes every chat (also one of unknown, empty kind), `dms` takes `dm`,
     `groups` takes `group` and `channel`.
  6. Mentions-only, when on, applies to `group` and `channel` chats only (direct chats and chats of
     unknown kind are exempt): the message is kept when `Inbound.MentionsAccount` is set or its
     `ReplyToID` is a platform id Partyline sent to that chat (an id belongs to its chat). The chat's
     kind is the stored one when the message gives none.
  7. A repeat is detected before anything is downloaded (`Queue.Has`: the connection, the chat and
     the platform id; an acknowledged item still counts until its purge); then `trusted` is
     decided, the media become blobs and `Queue.Enqueue` queues the item for the operator.
- **`trusted`** (CID-13) is true only when `Identities.ResolveSender(connection, sender id, alt id)`
  gives the user who owns the operator AI. A sender without an id, an account linked through
  another connection and another user's account are untrusted, and so is every `FromSelf` message.
- **Item content**: `SenderID` and `SenderName` are the message's (`SenderAltID` only serves the
  trust lookup), `Kind` defaults to text, `Text` is cut to 16,000 code points with the truncation
  marker (CON-14), `ReceivedAt` is the platform time (now when the message has none) and the files
  are `platform.Attachment`s: stored (blob hash), `too_large` or `unavailable`, with the
  attachment name defaulting to `file` and its type to `application/octet-stream`.
- **`Inbound.MentionsAccount`** is new. The adapters set it in T8 to T11. Until they do, only
  direct messages, replies to Partyline sends and (with mentions-only off) everything else pass; a
  webhook chat is a `channel`, so a webhook connection delivers nothing under the defaults until the
  owner turns mentions-only off (T11 adds the `mentions_account` payload field).
- **`platform.Queue.Has(connectorID, chatID, externalID)`** and its query `PlatformItemExists`.
- **Removed** with the pipeline's routing (CID-07, CID-22): room posting, `People` (the recorder of
  people and of who is seen in each chat; its logic lives on as the test-only
  `connectortest.History`, which the tests of the history that is still read use), DM creation,
  `autoGroup`, `autoRoute`, `platformDM`, the catch-all caps and `handleSelf`. `autoroute.go` keeps
  only `IsAutoRouteDetail` and the two detail constants, which `Service.Update` (ROUTE-09) still
  reads until T35. Still present, and retired later: the catch-all and `auto_route_*` settings, the
  room outbox and `Sender` (T33), `RouteChat` and the notify endpoints (T34),
  `conversations.EnsurePlatformDM` and `rooms.PostFromChat` (kept for the history tests).
- **Tests retired with the routing** (commit body names the IDs): `catch_all_test.go` (ROUTE-01..11),
  `auto_groups_test.go` (ACC-06), `platform_dm_test.go` (ACC-02, DM-05 sends), the person-creating
  tests of `people_test.go` (PEOP-01 creation, GRP-02 seen links, PEOP-05 following the platform
  name; `TestPlatformLabel` survives in `chats_test.go`) and `notify_test.go` (NOTI-01..03 and the
  tests of `GET /me/notify-options` and `PUT /me/notify`: every fixture built a linked phone
  through inbound DMs that no longer create persons or routes; T33 and T34 remove the code those
  tests covered). Rewritten against the queue: the inbound, own-account and echo tests, the webhook
  inbound tests, the people/contacts fixtures (history seeded through `connectortest.History`),
  the merge tests (platform DM history seeded with `EnsurePlatformDM`) and the v0.3 upgrade test
  (the agent adopts its folder and replies through the outbox; T37 pins the upgrade end to end).
