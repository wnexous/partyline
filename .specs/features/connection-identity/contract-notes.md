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
