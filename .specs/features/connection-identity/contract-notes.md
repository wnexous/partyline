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

## Fix after the T2-T7 review: the 7 days start on arrival (CID-10)

The pipeline queues an item with `ReceivedAt` = the time partyline got the message, not the platform's timestamp: a message synced late with an old timestamp would otherwise expire within minutes, and one with a future timestamp would outlive 7 days. The chat's last activity still uses the platform time.

## T8: Telegram mentions (CID-12)

- **Detection**: `Connector.handle` sets `Inbound.MentionsAccount` when the message's `entities`
  hold a `mention` whose covered text equals the bot's `@username` from `getMe`
  (case-insensitive), or a `text_mention` whose `user.id` is the bot's id. A bot without a
  username matches only `text_mention`; entity offsets/lengths are UTF-16 code units, so the
  covered text is decoded from UTF-16 before comparing.
- **Captions** (refinement of the design's row, which names the message text only): media messages
  carry their text as `caption` with `caption_entities`; those entities are checked against the
  caption, so a photo whose caption mentions the bot counts. Without it a media mention would
  never pass mentions-only.
- **Not mentions**: another bot's `mention`, a `text_mention` of another user, an entity of any
  other type and a message without entities. `@everyone` is not a Telegram concept.

## T9: Discord mentions (CID-12)

- **Detection**: `Connector.onMessage` sets `Inbound.MentionsAccount` when the bot's user id, known
  from the gateway's READY event, is in the message's `mentions`. The check is by id, not by
  username, so a renamed bot still matches and a user with the same name does not.
- **Not mentions**: another user's mention, `@everyone`/`@here` (`MentionEveryone` carries no user
  in `mentions`) and a role mention; before READY the bot id is unknown and every message is
  unmentioned.

## T10: WhatsApp mentions (CID-12)

- **Detection**: `Parse` exposes the jids of the content node's `contextInfo.mentionedJid` as
  `Parsed.MentionedJIDs` (it also finds them in a media caption). The connector sets
  `Inbound.MentionsAccount` when one of them equals its own account, comparing bare jids
  (`BareJID`, which drops the device suffix on both sides).
- **LID**: evolution-go reports the instance's jid in `/instance/all`; for a newer account that
  may be its LID instead of a phone jid, and a group mention then carries the bare LID. The bare
  comparison matches both forms. There is no mapping from the phone jid to a LID in the instance
  payload, so a mention that is the account's *other* form (LID mention while evolution reports
  the phone jid) cannot be recognised; the phone form is what groups send in that case.
- **Not mentions**: another number's jid, a mention list without the account, and a message
  without a `contextInfo` (a plain conversation).

## T11: Webhook `mentions_account` (CID-12)

- **Schema**: `dto.HookInboundRequest` gains `mentions_account` (bool, omitted when false), so the
  generated TS type documents it; the connector now decodes that DTO instead of keeping a second
  copy of the payload struct, so the documented schema and the accepted body cannot drift. A
  webhook chat is a `channel`, so under the default `groups_mentions_only` a payload without the
  field is accepted, records its chat and is not delivered (T7); `true` delivers.
- **Invalid**: a non-boolean `mentions_account` is a 422 whose `fields` name
  `mentions_account: "must be a boolean"` and nothing is stored (not even the chat). Other
  malformed bodies keep the old `body: must be a JSON object`.
- **Authorization** is unchanged and still the hook's HMAC signature/timestamp: the e2e test sends
  a `mentions_account: true` body with a forged signature and gets 401, so the new field never
  bypasses the route's only credential (operator/owner/use-grantee/other-AI tokens do not apply to
  this unauthenticated-by-design route).

## T12: Gateway pushes platform events (CID-06, CID-08, CID-09, CID-10, CID-25)

- **Opt-in**: the agent WebSocket request header `X-Partyline-Features` (`dto.HeaderFeatures`) lists,
  separated by commas, the optional features of the client; the item `platform`
  (`dto.FeaturePlatform`) opts in to platform events. Spaces around an item are ignored; the match
  is exact (`platforms` and `non-platform` do not opt in). A session that did not opt in receives
  no platform frame and no dropped notice, and it touches neither the queue nor the dropped count:
  its items wait for a session that did (design refinement 4).
- **Order on connect**: welcome (with `connections`), the room backlog, the dropped notice, then the
  platform items. Live room messages and platform items interleave afterwards.
- **Frames** (`dto`): `platform` (`AgentPlatformFrame{type, event}`, server to channel) and
  `platform_ack` (`AgentPlatformAckFrame{type, item_id}`, channel to server). `PlatformEvent` is
  the design's: `id` (the id the AI answers with), `platform`, `connection_id`, `chat_id`
  (Partyline's), `chat_title`, `chat_kind`, `sender_name`, `sender_id`, `trusted`, `from_self`,
  `kind`, `text`, `attachments` (`[]`, never null), `received_at` (RFC 3339, UTC). The platform's own
  message id (`external_id`) and the blob hashes never leave the server. `convert.PlatformEvent`
  renders a `platform.Event`; T13 reuses it.
- **Window**: at most `agents.PlatformWindow = 50` frames are in flight per connection, in flight
  being sent on this connection and not yet acknowledged by it. A slot is freed by the session's
  `platform_ack` of that frame **whatever became of the item meanwhile** (expired, dropped by the
  bound or moved to another AI): the channel acknowledges every frame it receives, and an item the
  server deleted must not hold a slot for the life of the connection. An acknowledgement that
  fails to record (a store error) keeps the slot and the item unacknowledged; the next connection
  delivers it again.
- **No cursor** (refinement of the design, which names `Unacked(afterSeq)`): the connection
  remembers only the ids in flight, and every wake-up reads the AI's unacknowledged items from the
  start of the queue (`Unacked(agent, 0, room + in flight)`), skips the ones in flight and sends the
  oldest `room` of the rest. Wake-ups: connect, a freed slot, `events.PlatformQueued` for the AI
  (with or without an `ItemID`) and `events.Lagged`. So:
  - the items an operator change moved to the AI, which are older than what its session holds, are
    delivered (CID-05): T21 only publishes `PlatformQueued{AgentID: new}` after its commit;
  - a session that took the AI over receives every unacknowledged item, those in flight to the
    old session included (CID-25);
  - a lagged bus that dropped an event costs nothing.
  Delivery is at least once: if the old session's acknowledgement lands between the new session's
  read and write of an item, both sessions have it (the channel's `seenBefore` drops repeats within
  one process).
- **Dropped notice** (CID-10): an `AgentNoticeFrame` with an empty room and the text
  `N platform messages were dropped (older than 7 days or over 5,000 queued)` (`agents.DroppedNotice`;
  the same wording for N = 1, like `SkippedNotice`). `TakeDropped` reads and zeroes the count right
  before the write, once per connection; a connection that dies between the two loses the notice.
- **Welcome** (`AgentWelcomeFrame.connections`, `[]AgentConnection{id, platform, account_label}`):
  every connection the AI operates, enabled or not, ordered by connection name then id; always
  present, `[]` when the AI operates none. `platform` is the connector kind and `account_label`
  the connection's stored label (empty until it is known). Sent to every session, platform-capable
  or not.
- **Wiring**: `GatewayDeps.Platform` (the app passes `App.Platform`). Without a `Bus` a platform
  session reads its queue on connect and on acknowledgements only (no live push). `watchRevocations`
  became `watchBus` and also wakes the feed.
- **Authorization**: nothing new to authorize. The AI of a session is still the token owner's AI of
  the machine and folder it names, so another AI of the owner, another user's AI of the same
  folder (use grantee or stranger) and a user token or cookie (403) never see an item; an
  acknowledgement of another AI's item is a no-op (`Queue.Ack`).
- **Testkit**: `Folder.Features`, `Folder.WithPlatform()`, `Client.DialPlatform(name)`,
  `AgentConn.Platform(timeout)`, `AgentConn.AckPlatform(itemID)`.

## T13: Pending endpoint returns platform events (CID-06, CID-08, CID-09)

- **Request**: `GET /api/v1/agent/pending?platform=1`. `platform` is `1` (on) or `0` or empty or
  absent (off); anything else, or the parameter twice, is a 422 `invalid` with `fields.platform`
  ("must be 1 or 0") and takes nothing. `wait` and the 409 for an AI that holds the WebSocket
  (`messages are pushed to this agent's session; there is nothing to wait for`) are unchanged and
  apply to `platform=1` too.
- **Answer**: without the parameter the answer is what it always was, `{"items": [...]}`, with no
  `platform` key, and the platform queue is neither read nor changed. With it, `dto.AgentPending`:
  `{"items": [...], "platform": [...]}`, both always present (`[]` when empty). `platform` holds
  `PlatformEvent`s (the WebSocket frame's event, `convert.PlatformEvent`), oldest first.
- **Limit** (refinement of "within the existing limit"): each list has the existing limit of 20 per
  call. The room messages are the same with and without the parameter, which only adds the platform
  list; the rest of a longer queue comes with the next call.
- **Taken** (CID-09): the events count as delivered once returned, "as room messages do".
  `platform.Queue.Take(agent, limit)` lists the oldest unacknowledged items and acknowledges them in
  one write transaction (their text is deleted, the events returned still carry it); a failure
  acknowledges none. An empty queue is polled with a read only. The room messages are read first
  (their cursors move), then the platform events are taken: a store error between the two loses what
  the first step returned, like a failed response write after the cursors moved.
- **Wait**: `wait=N&platform=1` ends at the first room message or platform event, polling every
  500 ms like the room side; without `platform=1` a platform event neither ends the wait nor is taken.
- **Files of platform messages** (not assigned to a task by the plan, needed by CID-09 "attachments
  stay downloadable for 24 hours"; the design's data-model note): `GET /api/v1/agent/attachments/{id}`
  serves the attachment of a room the AI can view and, when there is none, a file of an item the AI
  holds, acknowledged or not (`Queue.Attachment`). Any other id is the same 404 as before: unknown,
  another AI's, not stored (`too_large`, `unavailable`), purged (24 hours after the ack). Images
  show inline and everything else downloads under its name, as for room files (`serveBlob` is
  shared). The user route `GET /api/v1/attachments/{id}` is unchanged and never serves a platform
  file. Agent tokens only (user credentials 403, anonymous 401). The purge runner (T4) deletes the
  blob once nothing references it.
- **Authorization** (agent tokens only): the AI is the token owner's AI of the machine and folder in
  the headers, so another AI of the owner, the same-named AI of a user granted use of the operator
  and the same-named AI of a stranger see only their own queue; user tokens and sessions get 403,
  anonymous callers 401.
- **Wiring**: `api.Deps.Platform` (the app passes `App.Platform`).

## T14: Channel and listen show platform events (CID-06, CID-25)

- **Opt-in**: every WebSocket connection of `partyline channel` and of `partyline listen` sends
  `X-Partyline-Features: platform`, so a current CLI always receives platform events. The REST calls
  of the tools send no such header; the hooks and `wait_for_messages` ask with `?platform=1` (T15).
- **Notification** (`channel.FormatPlatform`; `listen` prints the same keys plus `content`):
  - content: the text, then one `[attachment <id>: <name>, <mime>]` line per file, as for a message
    (`download_attachment` takes the id, T13);
  - meta, all strings:
    - `type` = `platform`, `author_kind` = `platform`;
    - `room_name` = `<Platform> · <chat title>` (the platform alone when the chat has no title);
      the platform reads WhatsApp, Telegram, Discord or Webhook, other kinds are capitalized and an
      empty one is `Chat`;
    - `message_id` = the event id, which `reply` takes;
    - `author` and `sender_name` = the sender's name, `sender_id` = the sender's id on the platform;
    - `trusted` (CID-13), `from_self` (CID-14), `kind`;
    - `platform` (the connector kind), `connection_id`, `chat_id` (Partyline's: what `send_message`
      takes together with `connection_id`, T19/T20), `chat_title`, `chat_kind`.
  - There is no `room`, `conversation`, `author_id`, `author_handle` or `reply_to`: a platform event
    belongs to no room or conversation, and the platform's own message ids never reach the session.
- **Acknowledgement**: after the notification is written the link sends `platform_ack {item_id}`.
  Messages and platform events share one path (`notifyThenAck`) and one dedupe window of 1,000 ids
  (`seenBefore`): a repeated id, which the gateway sends again after a reconnect or a takeover
  (CID-25), is acknowledged without a second notification. A platform frame without an event id is
  dropped. The message path behaves as before.
- **Identity notice**: `You are X (@x). Your owner is Y (@y). You operate: WhatsApp +55 11 *****-0000
  (connection <id>), Telegram (connection <id>).` The last sentence appears only when the welcome
  lists connections (T12); an empty account label leaves the platform alone. The notice is still
  sent once per process, and `listen` still prints none.
- **Adapters**: no change. The shared framing shows a platform record with the room name, the chat
  title, the platform, the sender (`from`), `trusted`, `message_id` and `chat_id`; `from_self`,
  `connection_id` and `kind` are not part of it (T16 pins this framing).

## T15: Hooks and wait tool include platform events (CID-06)

- **Request**: `channel.Pending` (the turn-end hooks) calls `GET /agent/pending?platform=1`, and the
  `wait_for_messages` tool calls it with `wait=N&platform=1`. Both now return `dto.AgentPending`
  (`Items` and `Platform`) instead of the room messages alone (`channel.Pending`'s signature
  changed; its only caller is `partyline hook`). A server from before platform events ignores the
  parameter and answers `{"items": [...]}`, which decodes to an empty `Platform`.
- **Taken**: the server acknowledges the events it returns (T13), so a hook or a tool call that
  returns them has delivered them.
- **Framing**: `FrameAll(msgs, events...)` frames every message and every event with the same frame
  and the trust line of its own flag (`trusted="true"` gets the owner line, everything else the
  data line), separated by a blank line. `channel.PlatformRecord(e)` is the record of an event (the
  keys of its notification plus `content`), which `Frame` renders as `<partyline room="<Platform> ·
  <chat title>" chat=… platform=… from=… trusted=… message_id=… chat_id=…>text</partyline>`. The
  two lists are merged oldest first by `Message.CreatedAt` and `PlatformEvent.ReceivedAt`; a message
  goes before an event of the same time. The messages alone frame exactly as before.
- **Hooks**: `codex-stop` (decision `block`) and `gemini-after-agent` (`deny`) continue with the
  framed text as the reason when either list has something, and allow the stop (`{}`) when both are
  empty. As for room messages, what the hook returns is delivered: an error after the server took
  the events loses them, and the hook then allows the stop and reports on stderr.
- **Tool**: `wait_for_messages` returns the framed text, ends its wait at the first room message or
  platform event, and answers `no new messages in N seconds` only when both lists are empty. Its
  description now says it covers the connections the AI operates.
- **Authorization**: nothing new; the hook and the tool act as the AI of their folder (T13), so
  another AI of the owner and another user's AI of the same folder name neither see nor take the
  events.

## T16: Adapter framing covers platform records (CID-06)

- **Where**: the shared cases live in `integrations/testdata/framing.json` (`framing.ts` imports
  them and `TestFrameMatchesTheAdapters` reads the same file on the Go side), so the four new cases
  run for pi, OpenCode and omp (12 tests) and for the Go framing that `wait_for_messages` and the
  turn-end hooks use. `framing.ts` itself did not change.
- **Cases**: a WhatsApp group event with an attachment line (untrusted: data line), a Telegram
  direct event from the owner's verified account (trusted: owner line), a direct event typed on the
  account itself (`from_self`, untrusted: data line) and one whose chat title and sender name carry
  quotes and angle brackets (the title and the name are third-party text, escaped in the room, chat
  and from attributes). Their records are what `FormatPlatform` produces plus `content`; the
  expected texts were written from the PUSH-06 frame format, not from the code's output.
- **No adapter changed**: they frame from `room_name`, `chat_title`, `platform`, `sender_name` or
  `author`, `trusted`, `message_id`, `chat_id` and `content`, and treat only `type` `notice` apart.
- **Known limitation, for T19/T20**: the shared framing does not show `from_self`, `connection_id`
  or `kind`. A harness fed by `listen` or the hooks can always answer with `reply` (the message id);
  naming a connection for `send_message` needs its id, which only the full channel's identity
  notice lists (T14), so T20 has to decide how such a harness names its connection.


## T17: Platform sends with retries (CID-14, CID-15, CID-16, CID-18)

- **Service**: `connectors.Sends` (`internal/connectors/sends.go`) is built by `connectors.NewSends(SendsDeps{Store,
  Transport, Sent, Now, Logger, AttemptTimeout, PollInterval})`; `Transport` is the Manager. The app builds it as
  `App.Sends`, shares its `SentIDs` registry with the inbound pipeline and runs its worker (`platform-sends`) unless
  `Options.DisableOutbox` is set (the tests that inspect pending sends, `testkit.WithOutbox` turns it on again).
- **`Send(ctx, SendInput{AgentID, ConnectorID, ChatID, Text, ReplyTo}) (dto.PlatformSendResult, error)`** refines the
  design's five positional strings into a struct (all strings, easy to swap) and returns the DTO. Checks, in this order:
  the connection exists (`access.ErrNotFound`), the AI is its operator (`ErrNotOperator`, also while nobody is), the
  chat is one of its chats (`ErrUnknownChat`, also for a chat id nobody has), the text has 1 to 16,000 characters and is
  not blank (`*validation.Error` on `text`, MSG-02). A refusal stores and sends nothing. T19 maps the errors to HTTP and
  decides who may learn that a connection exists (design refinement 2); `Send` itself does not look at the caller's owner.
- **Row lifecycle** (`platform_sends`): the row is inserted `sending`, claimed by the synchronous attempt, so the worker
  (which only takes `pending`) never sends it twice. Then:
  - `sent`: `attempts` + 1, `external_id` = the platform's id of the first message created, `last_error` and `text`
    cleared;
  - `pending`: the text stays for the retry, `last_error` says why, `next_attempt_at` = now + `delivery.Backoff(n)`
    (2 s, 4 s, 8 s, 16 s after attempts 1 to 4);
  - `failed`: after attempt `delivery.MaxAttempts` (5), or when retrying cannot help; `text` cleared.
  `Backoff` and `MaxAttempts` are the room outbox's, imported from `delivery`: T33 must keep them (or move them).
- **Attempt**: one at a time per call, bounded by `SendAttemptTimeout` (15 s, also for each retry). A platform that does
  not answer gives the reason `the platform did not answer within 15s`; any other failure keeps the connector's own
  (already redacted) error, cut to 500 bytes. The reason is stored and shown to the AI (`PlatformSendResult.error`), and
  never logged: the log carries ids, the attempt number and the status only. The attempt is not abandoned when the
  caller's context ends (the caller going away does not unsend a message in flight), and its outcome is recorded even
  when the worker's context ends mid-send.
- **What the connector gets**: `Unsigned` always true; `ChatID` the platform's id of the chat; `ReplyToID` the platform
  id of the message quoted and `ReplyToSenderID` the sender of the queued item with that id in the same chat (looked up
  at each attempt, empty when the item is gone or has no id), because WhatsApp needs the participant to quote in a group;
  `MessageID` the send's id; `RoomID` empty (a send belongs to no room); `AuthorName` the AI's display name, which only
  platforms that carry the author as data (the webhook payload) show: `Unsigned` keeps it out of the text.
- **Echo (CID-14)**: every id a send creates goes to `SentIDs` (each part through `Outbound.Created`, and the id
  `Send` returned for a connector that tells nothing else). The pipeline reads the sends too: `settle` holds an
  own-account message while a send of its chat is `sending`, and `sentByPartyline` (also behind `repliesToAccount`,
  CID-12) asks `SentIDs`, the room outbox and `platform_sends.external_id`, the first part's id, which survives a
  restart. A message without an id is never an echo, and an id belongs to the chat it was sent to. The room outbox reads
  stay until T33.
- **Worker**: `Sends.Run` makes the rows a previous process left `sending` due again, flushes and then polls every second
  (`SendsDeps.PollInterval`); `Flush` and `Running` are exported for tests. A retry first checks that the AI is still the
  connection's operator: if the owner chose another AI meanwhile the send fails at once with `the AI no longer operates
  this connection`, without a platform call, with its attempts unchanged and its text deleted (CID-03, CID-05). A deleted
  operator AI, chat or connection takes its rows with it (foreign key cascade).
- **Lifecycle of the table** (the point T4 left to this task): a finished row (`sent` or `failed`) keeps its metadata, no
  text, for `SendRetention` = 30 days from its last change, and the daily retention job deletes it (`Sends.Purge`, batches
  of 1,000, the exact age is deleted and a millisecond less is kept); a `pending` or `sending` row is never purged. The
  number is this task's choice, since neither the spec nor the design gives one: long enough for a person to reply to an
  old message of the account, short enough that the table is no log of what the AI sends. After 30 days a reply to such a
  message no longer counts as a reply to the account under mentions-only (CID-12).
- **DTO** (introduced here because the service's result has this shape; T18 and T19 answer with it): `PlatformSendStatus`
  (`pending|sending|sent|failed`) and `PlatformSendResult{id, status, error?}`.
- **sqlc**: `queries/platform_sends.sql` (insert, the attempt's context, due list, claim, the three outcomes, requeue, the
  two echo counts, batched purge).
- **Tests**: `internal/connectors/sends_test.go` (service and worker, on a manager fixture with a fake clock),
  `sends_echo_test.go` (the pipeline through the app) and `internal/app/platform_sends_test.go` (the retention wiring).

## T18: `reply` to a platform message (CID-15, CID-18)

- **Route**: `POST /api/v1/agent/messages/{id}/reply` keeps its path and body (`dto.ReplyRequest`). It looks for a room
  message of the AI first (`agentTarget`, unchanged, rate limit and all); when there is none it looks in the AI's own
  platform queue (`Queue.Get`, acknowledged or not, until the record is purged: 24 hours after the acknowledgement, 7 days
  for a message nobody acknowledged). An id that is in neither, another AI's queue item included (another AI of the owner,
  the same-named AI of a use grantee, of a stranger or of an admin), is the same `404 not_found` "message not found" as an
  unknown id: nothing about other AIs' queues is revealed.
- **Answer for a platform message**: `dto.PlatformSendResult{id, status, error?}`, where `id` is the id of the send (not
  something a later `reply` or `react` can name). `201` when the platform took the text (`sent`, no `error` key); `202`
  when it did not yet (`pending`, `error` = the reason, Partyline retries, CID-18). A room reply is still `201` with the
  `dto.Message`.
- **What goes out**: `Sends.Send` with the item's connection and chat, `ReplyTo` = the item's platform message id (empty
  when the platform gave none: no quote) and the sender of that item as the participant to quote in a group (T17).
- **Rate limit**: a reply through a connection counts against the AI's 30 posts a minute (`Registry.AllowPost`, TOOL-08),
  like every other post of an AI: a runaway AI cannot flood a platform account. It is checked after the item is found and
  before the send, as for a room reply, so a refused request for an unknown message does not use it up.
- **Errors**: `connectors.ErrNotOperator` is `403 not_operator` (message `only the AI that operates the connection can send
  through it`) and `ErrUnknownChat` is `404 unknown_chat` (message `the chat does not belong to this connection`):
  `dto.ErrorCodeNotOperator` and `dto.ErrorCodeUnknownChat` sit in the one `ErrorCode` block of `dto/common.go` (tygo emits one
  `ErrorCode` union per block, so a second block in another file breaks the generated TypeScript), and `mapError` maps them. For a
  reply `not_operator` is possible only in the gap between an operator change and the queue following it (the item is still
  the old operator's); T19 gives both their own route. Text errors are the 422 on `text` of T17.
- **Channel `reply` tool** (a refinement of the design's "the tool does not change": its arguments do not, its answer
  must): the tool reads the answer as a room message or as a send result. A send result says `sent`, or `not sent yet:
  <reason>; partyline retries` (CID-18) as a normal tool result, not an error: the message is not lost and sending it again
  would duplicate it; `failed: <reason>` is the unreachable case of a send that has already failed. The tool description
  says both targets. `platformSent` is shared with T20's `send_message`.
- **Wiring**: `api.Deps.Sends` (the app passes `App.Sends`). Without `Platform` or `Sends` a reply to anything but a room
  message is 404, as before.
- **Tests**: `internal/api/agent_reply_platform_test.go` (e2e, including the authorization matrix and the refused,
  stopped-connection and acknowledged-then-purged cases) and `internal/channel/connection_tools_test.go` (the tool; its
  fixture `connectionChan` is the one T20 uses).

## T19: Send to a connection chat (CID-03, CID-16, CID-17)

- **Route**: `POST /api/v1/agent/connections/{id}/chats/{chat}/messages` with `dto.SendToChatRequest{text}`. `{id}` is the
  connection's id and `{chat}` Partyline's id of the chat, the two ids a platform event carries (`connection_id`,
  `chat_id`). Agent tokens only (`agentOnly`): a user, with a session or a token, is 403 `forbidden`, anonymous 401.
  The answer is a `dto.PlatformSendResult`: `201` when the platform took the text, `202` with the reason when it did
  not yet and Partyline retries (T17, CID-18).
- **Order of the checks**, which fixes what each caller learns:
  1. the body is JSON (400);
  2. the AI's **owner** (the token's owner) owns the connection or may use it, else `404 not_found`, the very answer for a
     connection that does not exist (design refinement 2): the AI of a stranger or of an admin learns nothing, not even
     that the connection exists (admins have no implicit access, AD-005);
  3. `Sends.Check`: the AI is the connection's operator, else `403 not_operator` (also while nobody operates it, and for
     another AI of the owner or of a use grantee); the chat is one of the connection's chats, else `404 unknown_chat`
     (also for a chat id nobody has); the text has 1 to 16,000 characters, else 422 on `text`. The operator check comes
     before the chat check, so a caller that is not the operator learns nothing about the connection's chats;
  4. the AI's 30 posts a minute (`Registry.AllowPost`, 429), then `Sends.Send`.
- **Refusals do not count against the limit** (decision; it also applies to T18's `reply`): `Sends.Check` is a new method
  that refuses without storing or sending anything, and the handlers call it before the limiter. An AI that keeps naming a
  wrong connection or chat would otherwise use up its 30 posts and then see `rate limited` for a minute instead of the
  refusal that tells it what is wrong. The shared handler code is `sendOnConnection`; `reply` (T18) goes through it too.
- **Ids in the answer**: `PlatformSendResult.id` is the id of the send.
- **Tests**: `internal/api/agent_connection_send_test.go` (the matrix with the operator, another AI of the owner, a use
  grantee's AI, a stranger's, an admin's, users with sessions and tokens and anonymous callers, each refusal with an
  otherwise valid request and its own code; the identical answers of a hidden and a missing connection; the limit).
