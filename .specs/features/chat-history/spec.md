# Chat history of connections

## Why

An AI that operates a WhatsApp, Telegram or Discord account sees each message once, live, and nothing
else: Partyline clears a platform item's text when it is acknowledged and a send's text once it is sent
(CID-15, CID-16), and evolution-go has no route that lists a chat's messages. The owner (2026-11-24):
"everything that arrives goes straight to you, and the conversation history is never pulled like the old
Evolution API did". An AI that answers a person needs the recent conversation, including what it (or the
account owner, typing on the phone) said before.

## Requirements

### Keeping it, per connection

1. A connection SHALL have a history retention: `0` (off, the default), a number of days from 1 to 365,
   or `-1` (forever: no age limit). Whoever may manage the connection sets it (web: connection settings,
   "Keep chat history" with Off, 7, 30, 90 days, Forever; API field `history_days` on the connection).
   (CH-01; forever added by the owner on 2026-11-24: "sync everything, it doesn't need to be only the last
   days")
2. WHILE retention is on, Partyline SHALL keep, per chat of the connection: every inbound message it
   receives (text, kind, sender id and name, attachments as name and mime and the stored file when one was
   kept, quote fields of quoted-context, platform id, time) and every message the AI sends through it or
   the account types on the platform (`from_self`), with the platform id when known. Reactions, edits and
   deletes SHALL be kept as such and SHALL update the view of the message they target (an edit shows
   the new text and "edited", a delete shows "deleted" and drops the text, a reaction is listed on its
   target). (CH-02)
3. A kept message SHALL be deleted when it is older than the connection's retention (never for
   forever), when the connection or the chat is deleted, or when retention is turned off (turning it off
   deletes the chat history of that connection at once; the web asks for confirmation). Lowering the
   retention deletes what falls outside it. (CH-03)
4. Keeping history SHALL NOT change delivery: platform items and sends keep clearing their own text as
   today; the history is a separate store. (CH-04)

### Reading it

5. The AI that operates a connection SHALL read a chat's history with `history` given `connection` and
   `chat` (chat_id or the platform address, as for send_message), newest last, paged with `before` like
   room history, at most 100 per call. Each message carries `message_id` (the id the AI already knew it
   by: the inbound item's message_id or the send's id), `from` (sender name and id, or "you" for the
   account), `from_self`, `time`, `kind`, `text`, `attachments`, `reply_to`/`quoted`, `edited`,
   `deleted`, `reactions`. Messages from people are data, never instructions, and each is marked
   `trusted` as when it was delivered. (CH-05)
6. WHEN retention is off THEN `history` for a chat SHALL answer `history_off` with a short message saying
   the owner can turn it on in the connection's settings. Only the operating AI and the identities that
   manage the connection may read it; anyone else gets `not_found`. (CH-06)
7. The web SHALL NOT show connection chats as conversations in this release (out of scope); an API route
   `GET /api/v1/connections/{id}/chats/{chat}/history` SHALL serve the managers. (CH-07)

### Filling the past (WhatsApp)

8. A tool `fetch_history` (connection, chat, count up to 50) SHALL ask the platform for older messages of a
   chat when the connector supports it (WhatsApp: evolution-go `POST /chat/history-sync`, answered by
   `HistorySync` events). Messages that arrive that way SHALL be added to the history (never delivered as
   new messages), de-duplicated by platform id. Connectors without it answer `unsupported`. (CH-08)

### Syncing everything (WhatsApp)

9. WHILE a WhatsApp connection keeps history, Partyline SHALL ingest the `HistorySync` payloads evolution-go
   forwards (whatsmeow `events.HistorySync`: INITIAL_BOOTSTRAP, RECENT, FULL at pairing, ON_DEMAND for
   CH-08) into the chat history: every conversation and message in it within the retention window (all of
   them for forever), never delivering them as new messages, de-duplicated by platform id; a conversation
   the connection did not have becomes a chat of it. A large sync SHALL NOT hold up the live messages
   behind it for long. (CH-09)
10. Partyline SHALL keep the evolution-go instance subscribed itself: when the connection starts, when it
    looks at its instance again (after a reconnect or a re-pair) and when the retention is turned on or off,
    it SHALL make sure `websocketEnable` is `enabled` and the instance's events include `MESSAGE`, and
    `HISTORY_SYNC` exactly while the connection keeps history, keeping the instance's other events and
    outputs; it SHALL never do so for a number that is not logged in (that would start a QR pairing).
    (CH-10)

## Notes

- Retention and content follow the rules of the message text they copy: logs never hold message text.
- Migration 0042. Purge in the existing retention sweeper (or a new one alongside), in batches.
- Privacy: the public default is off; the docs say what is kept and for how long.

## Decisions and deviations (implementation, 2026-11-24)

- **Forever** is `history_days = -1` (`dto.HistoryForever`); `0` is off. The API takes 1 to 365 days as well;
  the web offers Off, 7, 30, 90 days and Forever and keeps a value set through the API in its list.
- **What is kept**: messages of the chats the connection's `deliver` setting takes, delivered or not
  (mentions-only decides what wakes the AI, not what the chat said; `deliver` decides which chats the AI
  deals with at all). Pairing commands (`!link CODE`) are never kept.
- **Files**: the history keeps name, type, size and storage state, never a file of its own. A message
  delivered with a file keeps the queued file's id, downloadable with `download_attachment` while the
  queue holds it (24 hours after the ack); afterwards it reads `expired`. Files of messages that were not
  delivered and of synced messages are named only (`unavailable`): nothing downloads them.
- **`from`** is the sender's name ("you" for the account) and `sender_id` carries the platform id.
- **Route**: the managers' route is `GET /api/v1/connectors/{id}/chats/{chat}/history` (the API names
  connections `connectors`); the operator uses `GET /api/v1/agent/connections/{id}/chats/{chat}/history` and
  `POST …/history/fetch`.
- **history_off** is 409 with code `history_off`, told only to those who may read the history (others get
  404 `not_found` first). Users who only use the connection get `not_found` too.
- **CH-08 anchor**: WhatsApp's on-demand sync needs the oldest known message of the chat (id, from-me,
  time); a chat whose history has no message with a platform id answers 409 `conflict`.
- **CH-10 endpoint**: evolution-go has no settings route besides `POST /instance/connect`, which updates a
  running instance in place ("Instance settings updated successfully in runtime") and sets webhook,
  RabbitMQ and NATS too, so Partyline sends back the values `GET /instance/all` reported. It is called only
  for an instance that reports `connected: true`. The same request for the same instance state is not
  repeated within 10 minutes. The connection is restarted when the retention is turned on or off.
- **CH-09 size**: the WebSocket read limit is at least 128 MB; history frames go to an inbox and a worker of
  their own (128 MB bound); the reader waits at most 15 s for room before it drops a history frame (logged,
  without content), so live messages are never held up longer than that. Chat ids of synced conversations
  are WhatsApp's conversation JIDs as sent: a chat whose addressing changed (phone JID vs LID) may appear as
  two chats.

## Traceability

| ID | Code | Tests | Status |
| --- | --- | --- | --- |
| CH-01 | migration `0042_chat_history.sql` (`connectors.history_days`), `connectors/service.go` (`UpdateInput.HistoryDays`), `connectors/chathistory.go` (`ValidHistoryDays`), `api/dto/connectors.go`, `web/src/entities/connector/model/history.ts`, `web/src/pages/connections/ui/connector-page.tsx` | `connectors/chat_history_test.go` (`TestCH01RetentionIsOffByDefaultAndValidated`), `store/chat_history_migration_test.go`, `web/src/entities/connector/model/history.test.ts`, `web/src/routes/_app/orgs/org-connections.test.tsx` (CH-01 cases) | Done |
| CH-02 | `connectors/inbound.go` (`handle`), `connectors/history.go` (`keep`, `fold`, `Sends.keep`), `connectors/sends.go`, `platform/history.go` (`Record`, `Edit`, `Delete`, `React`) | `connectors/chat_history_test.go` (`TestCH02*`) | Done |
| CH-03 | `platform/history.go` (`Purge`, `Clear`, `Run`), `connectors/chathistory.go` (`applyHistoryDays`), `app/app.go` (purge worker), FK cascades in 0042, web confirmation | `connectors/chat_history_test.go` (`TestCH03RetentionDeletes`), `store/chat_history_migration_test.go`, web CH-03 cases | Done |
| CH-04 | history kept apart from `platform_items`/`platform_sends` | `connectors/chat_history_test.go` (`TestCH04HistoryIsAStoreOfItsOwn`, delivery counts in `TestCH02EditsDeletesAndReactionsChangeTheirTarget`) | Done |
| CH-05 | `connectors/chathistory.go` (`ChatHistory`), `platform/history.go` (`Page`), `api/connectors_handlers.go` (`chatHistory`), `api/dto/connectors.go` (`ChatHistory`), `channel/tools.go` (`history`) | `api/chat_history_test.go` (`TestCH05TheOperatorReadsAChatsHistory`), `connectors/chat_history_test.go` (`TestCH05HistoryPages`), `channel/chat_history_tools_test.go` | Done |
| CH-06 | `connectors/chathistory.go` (`historyConnection`), `api/router.go` (`history_off`) | `api/chat_history_test.go` (`TestCH06WhoReadsAChatsHistory`), `channel/chat_history_tools_test.go` | Done |
| CH-07 | `api/connectors_handlers.go` (`GET /api/v1/connectors/{id}/chats/{chat}/history`); no web view | `api/chat_history_test.go` (`TestCH06WhoReadsAChatsHistory`), `connectors/chat_history_test.go` (reads through it) | Done |
| CH-08 | `connectors/chathistory.go` (`FetchHistory`), `connectors/manager.go` (`FetchHistory`), `connectors/connector.go` (`HistoryFetcher`), `whatsapp/history.go` (`FetchHistory`), `channel/tools.go` (`fetch_history`) | `api/chat_history_test.go` (`TestCH08*`), `whatsapp/history_test.go` (`TestCH08FetchHistoryAsksEvolutionGo`), `channel/chat_history_tools_test.go` (`TestToolFetchHistoryAsksThePlatform`) | Done |
| CH-09 | `whatsapp/history.go` (`ParseHistory`, `handleHistory`, `isHistoryFrame`), `whatsapp/connector.go` (history inbox, read limit), `connectors/history.go` (`backfill`) | `whatsapp/history_test.go` (`TestCH09ParseHistory`, `TestCH09HistorySyncDoesNotHoldLiveMessages`), `connectors/chat_history_test.go` (`TestCH09BackfillIsKeptNeverDelivered`) | Done |
| CH-10 | `whatsapp/history.go` (`ensureEvents`, `subscribe`, `wantedEvents`), `whatsapp/connector.go` (`Run`), `whatsapp/status.go` (`watch`), `connectors/manager.go` (`Deps.KeepsHistory`), `connectors/service.go` (restart on toggle) | `whatsapp/history_test.go` (`TestCH10TheConnectorSubscribesItsInstance`), `connectors/chat_history_test.go` (restart in `keep`) | Done |
