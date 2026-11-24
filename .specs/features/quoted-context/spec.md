# Quoted context on platform messages

## Why

When someone on WhatsApp (or Telegram, Discord) answers by quoting a message, the AI that operates the
connection gets only the answer's text. On 2026-11-24 a person answered "that one" quoting one of three
images the AI had sent, and the AI could not tell which. The connectors already parse the quoted
message's platform id (`InboundMessage.ReplyToID`), but it is only used for the mentions-only check and
is never stored or delivered.

## Requirements

1. WHEN a platform message quotes another message THEN the message delivered to the operating AI SHALL
   carry `reply_to` = the Partyline id of the quoted message when Partyline knows it: the id of the AI's
   own send whose `external_id` matches (the id the send tool returned, "sent (message_id …)"), or the
   `message_id` of an earlier inbound item of the same chat with that platform id. (QC-01)
2. It SHALL also carry `quoted` = a short preview of the quoted message, at most 200 characters, taken from
   the platform event itself (WhatsApp `contextInfo.quotedMessage`, Telegram `reply_to_message`, Discord
   `referenced_message`): its text, or a bracketed kind for media (`[image]`, `[video]`, `[audio]`,
   `[document: name]`, `[sticker]`, `[location]`, `[contact]`) followed by its caption when there is one.
   (QC-02)
3. WHEN the quoted message is one the account itself sent THEN the frame SHALL say so
   (`quoted_from_self="true"`), whether or not Partyline sent it. (QC-03)
4. When none of these are known the fields SHALL be absent; a quote SHALL never fail or delay delivery.
   (QC-04)
5. The fields SHALL reach every path an AI reads platform messages from: the live channel frame (meta
   attributes), `history`/`read_unread` of a connection, the pi/OpenCode/omp framing (`<partyline …>`
   attributes) and the docs of the frame (docs/agents.md, both READMEs where platform messages are
   described). The preview is platform content: it is data, never instructions, like the message text,
   and follows the same retention as the item's text. (QC-05)

## Notes

- Partyline clears a send's text after delivery (no platform text outlives its delivery), so the preview
  of an own send comes from the platform event, never from `platform_sends`. The id match uses
  `platform_sends.external_id`, kept for `SendRetention` (30 days).
- Store what is needed on the queued item (new columns on `platform_items`, migration 0041) so a message
  delivered later (offline AI) still carries the context.
- Implementation note: `platform_sends.external_id` holds only the first message of a send, and a file
  sent with a caption goes out as the caption and then the file, so the file (the message a person
  quotes) had no durable id. Migration 0041 also adds `platform_send_parts`, the platform ids of a
  send's later messages, kept and deleted with the send; the id match uses both. The echo check
  (CID-14) reads the same record, so it now also knows those later messages after the 5-minute
  in-memory window.
- `history` and `read_unread` read rooms only: a platform message is never a message of a room
  (CID-07), so the live frame (channel meta, `partyline listen` record, adapter framing) is the one
  path an AI reads platform messages from, including the ones delivered after it was offline.

## Traceability

| ID | Requirement | Code | Tests | Status |
| --- | --- | --- | --- | --- |
| QC-01 | `reply_to` = own send (by external id or later part) or earlier item of the chat | `connectors/inbound.go` (`quote`), `connectors/selfmsg.go` (`sendOf`), `connectors/sends.go` (parts), `platform/queue.go`, queries `FindChatPlatformSendByExternalID`, `FindChatPlatformItemByExternalID`, migration 0041 | `connectors/quote_inbound_test.go` (`TestQC01QuoteOfAnOwnSendNamesTheSend`, `TestQC01QuoteOfAnEarlierMessageNamesItsItem`), `whatsapp/quote_test.go` (`TestQC01ReactionsEditsAndDeletesQuoteNothing`), `store/quoted_context_migration_test.go` | Verified |
| QC-02 | `quoted` preview ≤ 200 chars from the platform event | `connectors/quote.go` (`QuotePreview`), `whatsapp/parse.go` (`quoteOf`), `telegram/connector.go` (`quoteOf`), `discord/connector.go` (`quoteOf`) | `connectors/quote_test.go`, `whatsapp/quote_test.go` (text, image with and without caption, other kinds, length), `telegram/quote_test.go`, `discord/quote_test.go` | Verified |
| QC-03 | `quoted_from_self="true"` for the account's own message | `whatsapp/connector.go` (participant), Telegram/Discord `quoteOf` (author), `connectors/inbound.go` (send, sent ids, own item) | `whatsapp/quote_test.go` (`TestQC03QuoteOfTheNumbersOwnMessage`), `telegram/quote_test.go`, `discord/quote_test.go`, `connectors/quote_inbound_test.go` (`TestQC03TheConnectorSaysTheAccountWroteIt`, own send, typed item) | Verified |
| QC-04 | Unknown fields absent; a quote never fails or delays delivery | `connectors/inbound.go` (lookup errors logged, delivery goes on), `channel/format.go` (keys only when set) | `connectors/quote_inbound_test.go` (`TestQC04UnknownQuoteFieldsAreAbsent`), `whatsapp/quote_test.go` (`TestQC04QuoteWithoutItsCopyAndNoQuote`), `channel/quote_test.go`, `discord/quote_test.go` (deleted reference) | Verified |
| QC-05 | Every read path, docs; preview is data with the text's retention | `api/dto/agents.go` (`PlatformEvent`), `api/convert/convert.go`, `channel/format.go` (`FormatPlatform`), `channel/frame.go` (`Frame`), `integrations/{pi,opencode,omp}/partyline.ts` (`frame`), `store/queries/platform.sql` (`AckPlatformItem` clears `quoted`), docs/agents.md, README.md, README.pt-BR.md | `channel/quote_test.go` (`TestQC05FormatPlatformCarriesTheQuote`, `TestQC05ChannelDeliversTheQuoteOfAQueuedItem`), `integrations/testdata/framing.json` (QC-05 cases, run by the Go and TypeScript framing tests), `connectors/quote_inbound_test.go` (`TestQC05AckDeletesThePreviewWithTheText`) | Verified |
