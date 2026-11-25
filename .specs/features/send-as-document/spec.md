# Send a File as a Document Specification

## Problem Statement

An AI that operates a connection sends a local file to one of its chats with `send_file`
(`connection` and `chat`, CID-32). The connector picks how the platform takes the file from its MIME
type: WhatsApp's `/send/media` gets `image`, `video` or `audio` for those types and `document` for the
rest (`internal/connectors/whatsapp/connector.go` `mediaType`), and Telegram uses `sendPhoto` for a
small JPEG, PNG or WebP and `sendDocument` otherwise (TG-03). A photo or a video sent as media is
recompressed by the platform. A person who asks for a file "as a document" (a photographer who wants
the original photos and videos) gets a smaller, lossy copy, and today the only way around it is to zip
the file.

## Goals

- [ ] An AI can send a photo or a video through a connection so that the person receives the same
      bytes, named as the file was.
- [ ] Nothing changes for a send that does not ask for it.

## Out of Scope

| Feature | Reason |
| --- | --- |
| A document mode for rooms | A room keeps every file as it was uploaded; there is nothing to keep |
| A per-connection or per-chat default | One flag on the send is enough for "the person asked for it" |
| Files through a webhook connection | A webhook connection takes no files (unchanged) |
| Persisting the flag on the send | A file is sent once and never retried (CID-32), so the flag lives for that one attempt |

## Requirements

### AD-01 — `as_document` through a connection

`send_file` with `connection` and `chat` takes `as_document` (boolean, default false), and so does the
agent API `POST /agent/connections/{id}/chats/{chat}/messages` (`SendToChatRequest.as_document`, in a
JSON body with `upload_ids` or in the `message` part of a multipart send).

- WHEN `as_document` is true THEN the file SHALL go to the platform as a document whatever its MIME
  type, with its file name:
  - WhatsApp: `/send/media` with `type: "document"` and the `filename`;
  - Telegram: `sendDocument` (never `sendPhoto`), with `disable_content_type_detection` so that the
    Bot API does not turn a video into a video message;
  - Discord: unchanged, every file is already an attachment kept as it is; the flag is accepted;
  - webhook: files stay `unsupported`.
- WHEN `as_document` is false or left out THEN the connector SHALL pick the type from the MIME type
  as before.
- WHEN a send through a connection carries `as_document` and no file THEN the server SHALL answer
  422 `invalid` on `as_document`.

### AD-02 — rooms refuse `as_document`

- WHEN `send_file` has a `room` target and `as_document` is true THEN the tool SHALL fail before it
  opens or uploads the file, saying that `as_document` applies to a connection and that a room keeps
  the file as it is. `as_document: false` with a room is accepted (models that send every parameter).
- The room API has no `as_document` field; decision: the refusal lives in the tool, which is the only
  client that could mistake a room for a chat (as for `reply_to` with a connection), so no new room
  field or 422 is added to the room routes.

### AD-03 — when to use it

- The `send_file` tool description, `docs/agents.md`, `docs/api.md`, `README.md` and
  `README.pt-BR.md` SHALL say that `as_document` keeps the original quality, for photos and videos a
  person asked for "as a document", and that it applies only through a connection.

## Traceability

| Requirement | Code | Tests |
| --- | --- | --- |
| AD-01 | `internal/channel/tools.go`, `internal/channel/sendfile.go`, `internal/api/dto/connectors.go`, `internal/api/agent_handlers.go`, `internal/connectors/sends.go`, `internal/connectors/connector.go`, `internal/connectors/whatsapp/connector.go`, `internal/connectors/telegram/connector.go` | `TestSendMediaAsDocument` (whatsapp), `TestSendFilesAsDocument` (telegram), `TestSendFileAsDocument*` (channel), `TestAgentConnectionFileAsDocument*` (api), `TestSendFileAsDocument` (connectors) |
| AD-02 | `internal/channel/sendfile.go` | `TestSendFileAsDocumentRoomRefused` (channel) |
| AD-03 | `internal/channel/tools.go`, docs | `TestSendFileToolSchemaAsDocument` (channel) |
