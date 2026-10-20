# Connectors Validation

**Date**: 2026-10-20
**Spec**: `.specs/features/connectors/spec.md` (31 requirements + 3 edge cases)
**Diff range**: `63a1da7..8f8e72b`. The feature commits run from `3e2b057` (T1) to `899b3ef` (T11 + contract notes), with `8f8e72b` ticking boxes. The range also contains the merged core-platform fix iteration (`f30556e`). Surface: `internal/connectors/**`, `internal/identities`, `internal/api/{connectors,chats,identities,hooks}_handlers*.go`, `internal/store/queries/{connectors,chats,identities}.sql`.
**Verifier**: independent sub-agent (author ≠ verifier), evidence-or-zero. Worked in a detached worktree. Mutants and probes ran in a separate scratch worktree, `/tmp/claude-1001/pl-conn-mutants`, which has since been removed.

## Validation verdict: FAIL ❌

The connectors are well built and well tested:
- All 31 ACs and all 3 edge cases have tests that assert the outcome the spec defines.
- `make check` is green.
- 31 of 33 injected mutants were killed.
- Secrets are masked on every read and kept when a PATCH sends the mask.
- Secrets are sealed at rest; the raw row holds no plaintext.
- Secrets are redacted from status and send errors, both raw and URL-escaped, including the Telegram token in URLs.
- Webhook signatures are checked with a constant-time compare.

The verdict is FAIL for three reasons:

1. **A verified identity can be forged across connectors (security; this breaks AD-006).** Identities are keyed only by `(connector kind, sender id)`. The sender id comes from the platform payload, and on some kinds the connector's owner controls that payload completely:
   - **Webhook:** `sender_id` is any value the caller sends.
   - **Telegram:** `api_base` can point at any server.
   - **WhatsApp:** `base_url` can point at any evolution-go server.

   So user A can link an identity, and then user B's own connector can send a message that the server resolves to A. A probe in the scratch worktree confirmed this: after Alice linked webhook sender `alice-id` through her connector, Mallory's webhook connector posted with `sender_id: "alice-id"`, and the stored message had `author_user_id` = Alice. Under DLV-06, Alice's agents receive such a message with `trusted=true` whenever it reaches them. It can reach them when Alice shares an agent with Mallory, since `/chats/{id}/route` accepts any agent the caller can use, or when Alice's agent shares a room with Mallory's chat. The flaw is in the spec (IDN-02 says "connector kind"), but it defeats the "trust is not forgeable" rule of AD-006.
2. **Mutant M31 survived.** Reply quoting is not checked to be per chat (`sender.go:231`).
3. **Mutant M04 survived.** The pipeline's pre-download dedupe step (`inbound.go:128-146`) is not covered by any test. It is redundant with `rooms.post` and the DB unique index.

---

## Task Completion

| Task | Status | Notes |
| ---- | ------ | ----- |
| T1-T11 | ✅ Done (all 34 `[x]` boxes ticked) | `tasks.md` repeats T1-T5 and the Phase 1/2 headers twice (lines 56-143 duplicate 144-221). This is a document defect only; `validate_tasks.py` reports 0 errors. |

---

## Spec-Anchored Acceptance Criteria

### P1: Manage connectors

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| CON-01 kinds with field schema | name, label, type, required, secret, help per field | `internal/api/connectors_handlers_test.go:92` - `require.Equal(t, []dto.ConnectorField{{Name: "bot_token", ..., Required: true, Secret: true, Help: ...}, ...}, bot.Fields)` | ✅ PASS |
| CON-02 missing/invalid field → 422 naming it | 422 + field named | `internal/api/connectors_handlers_test.go:110-117` - `RequireError(422, ...).Fields` = `{"config.bot_token": "required"}`, `{"config.api_base": "invalid url"}`, type errors; `internal/connectors/kind_test.go:71,74` | ✅ PASS |
| CON-03 secret never returned; `"********"` when set | mask on every read; PATCH with mask keeps the secret | `internal/api/connectors_handlers_test.go:156-157` - `requireNoSecret` + `Contains(body, "bot_token":"********")` for owner and grantee (GET and list); `:169` - `Equal(botToken, storedConfig(...)["bot_token"])` after a mask PATCH; at rest `:192-196` - `NotContains(row.ConfigJson, botToken)`, `False(bytes.Contains(row.SecretsEnc, botToken))` | ✅ PASS |
| CON-04 start within 2 s; `connector.status` events | running at once; connecting → connected events | `internal/api/connectors_runtime_test.go:39` - `NotNil(inst, "create starts the connector synchronously")`; `:44`, `:47` - `ConnectorStatusData{..., Status: connecting/connected}`; `internal/connectors/manager_test.go:171` - `Zero(waits.count(), "no backoff before the first run")` | ✅ PASS (the 2 s is checked structurally, not against wall time, as contract-notes says) |
| CON-05 disable/delete stops it and its sends | stopped + no sends | `internal/api/connectors_runtime_test.go:53-55` (WaitStop + `stopped` event), `:63`; `internal/connectors/manager_test.go:181-182` - `ErrorIs(err, ErrNotRunning)`, `Len(inst.Sent(), 1)`; `internal/connectors/sender_test.go:130-132` | ✅ PASS |
| CON-06 transport fails → `error` + redacted text + backoff | status error, secret → `[redacted]`, 2 s doubling to 120 s | `internal/connectors/manager_test.go:198-206` - `Equal([2s,4s,...,120s,120s], got)`, `Equal("Get \"https://api.example.test/bot[redacted]/getUpdates\": unauthorized", row.LastError)`; `:245` send errors; `:251` escaped forms | ✅ PASS |

### P1: Chats and inbound routing

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| CON-07 unknown chat → chat record, no message | chat (connector, external id, title, kind); message count unchanged | `internal/connectors/inbound_test.go:113-118` - `Equal(groupID, ExternalID)`, title, `ChatKindGroup`, `ConnectorID`, `Equal(before, messageCount)` | ✅ PASS |
| CON-08 routed → stored once per room with author kind chat, sender id/name, external id | 1 per room + those fields | `internal/connectors/inbound_test.go:136-150` - `Equal(before+2, count)`, `AuthorKindChat`, `SenderID`, `SenderName`, `ExternalID == "M2"` | ✅ PASS |
| CON-09 refresh via POST `.../chats/refresh` | listed chats recorded; 409 when not running or the kind cannot list | `internal/api/chats_handlers_test.go:75-82`, `:86`, `:91` | ✅ PASS |
| CON-10 duplicate external id stored once | still one per room | `internal/connectors/inbound_test.go:136` - `Equal(before+2, ...)` after emitting M2 twice | ✅ PASS (see M04) |
| CON-11 media within limit stored as attachment | stored attachment; oversize → `too_large`, no blob | `internal/connectors/inbound_test.go:196-206` - `AttachmentStatusStored`, body equality, `AttachmentStatusTooLarge`, `blobCount == 1` | ✅ PASS |
| CON-12 own-account messages dropped | nothing stored, no chat | `internal/connectors/inbound_test.go:166-167`; adapters set the flag: `whatsapp/parse_test.go:99`, `telegram/connector_test.go:262`, Discord bots `discord/connector_test.go:167` | ✅ PASS |

### P1: WhatsApp via evolution-go

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| WA-01 broadcast WS, Message events of allowed instances | `/ws?token=<global key>` without instanceId; non-allowed ignored | `internal/connectors/whatsapp/connector_test.go:83` (the fake refuses `instanceId`), `:202` - `Equal(globalKey, f.wsToken)`, `:209` - `Equal("ALLOWED", in.MessageID, ...)` | ✅ PASS |
| WA-02 group JID / bare DM JID | `<instance>\|<jid>`, `:NN` removed | `internal/connectors/whatsapp/parse_test.go:66`, `:83` - `"acme\|5511999990000@s.whatsapp.net"`, `:89` | ✅ PASS |
| WA-03 `/send/text` with instance token, `quoted` on reply | quoted `{messageId, participant}`; none without a reply | `internal/connectors/whatsapp/connector_test.go:298`, `:309` | ✅ PASS (see M31 for the sender-side chat check) |
| WA-04 `/send/media` base64 + type | image/video/audio/document, pure base64 | `internal/connectors/whatsapp/connector_test.go:333`, `:335` | ✅ PASS |
| WA-05 `GET /group/list` per allowed instance | groups of allowed instances (or websocket-enabled ones) | `internal/connectors/whatsapp/connector_test.go:362`, `:369` | ✅ PASS |

### P1: Telegram

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| TG-01 getUpdates offset+1, message + channel_post | offset advances; both update kinds | `internal/connectors/telegram/connector_test.go:230` - `allowed_updates == ["message","channel_post"]`, `:245-251` - offset reaches 12 after update 11, no redelivery | ✅ PASS |
| TG-02 split > 4,096, ordered, `reply_parameters` | parts ≤ 4,096, joined == text, reply on first only | `internal/connectors/telegram/connector_test.go:329`, `:332`, `:334`; `internal/connectors/text_test.go:16-31` | ✅ PASS · ⚠️ spec-precision: "characters" is not defined (runes are counted; Telegram and Discord limits are not specified in runes) |
| TG-03 sendPhoto images / sendDocument else | png → sendPhoto, pdf and > 10 MiB → sendDocument | `internal/connectors/telegram/connector_test.go:365-376` | ✅ PASS |
| TG-04 inbound media via getFile | getFile(largest) + file endpoint | `internal/connectors/telegram/connector_test.go:296` - `Equal("large", f.next("getFile").JSON["file_id"])` + content | ✅ PASS |

### P1: Discord

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| DC-01 non-bot messages, external id = channel id | bots ignored | `internal/connectors/discord/connector_test.go:167-168` | ✅ PASS |
| DC-02 split > 2,000, reference on reply | parts ≤ 2,000, reference on the first part only, mentions disabled | `internal/connectors/discord/connector_test.go:236`, `:238` (`Empty(AllowedMentions.Parse)`), `:241`, `:244` | ✅ PASS |
| DC-03 text channels of every guild | text + news channels, no voice | `internal/connectors/discord/connector_test.go:278` | ✅ PASS |

### P1: Generic webhook

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| HOOK-01 valid signature + timestamp → 202 | 202 and the payload is stored | `internal/api/hooks_handlers_test.go:156` - `Equal({"status":"accepted"}, ...RequireStatus(202))`, `:165-169`; signature formula `internal/connectors/webhook/connector_test.go:48` | ✅ PASS |
| HOOK-02 bad signature or timestamp > 5 min → 401, nothing stored | 401; no chat, no message | `internal/api/hooks_handlers_test.go:178-183` - 401 for wrong secret, -301 s, +301 s, unsigned; `Empty(e.chats(t))`; boundary `webhook/connector_test.go:55-58` (300 s accepted, 301 s refused) | ✅ PASS |
| HOOK-03 outbound POST signed the same way; non-2xx = failure | valid signature; 500 → pending retry | `internal/api/hooks_handlers_test.go:237` - `Equal(webhook.Sign(secret, ts, body), header)`, `:254-268`, `:292-293` - `OutboxStatusPending`, `"outbound: status 500"` | ✅ PASS |

### P1: Verified identities

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| IDN-01 6 chars (A-Z, 2-9), 10 min | `^[A-HJ-NP-Z2-9]{6}$`, expires = now + 10 min | `internal/identities/identities_test.go:77-78`; `internal/api/identities_handlers_test.go:34-35` | ✅ PASS |
| IDN-02 `!link`/`/link` valid → link (kind, sender, alt), consume, not routed | identity listed; message count unchanged; single use | `internal/connectors/inbound_test.go:240-246`; `internal/identities/identities_test.go:108` - `False(ok, "single use")` | ✅ PASS · 🔴 security gap S1 (identity scope) |
| IDN-03 unknown/expired → ignored, not routed | no identity, no message | `internal/connectors/inbound_test.go:236` (unknown, through the pipeline; count unchanged at `:240`); expired `internal/identities/identities_test.go:146` | ✅ PASS |
| IDN-04 deleted identity → no user → `trusted=false` | resolves to "" | `internal/api/identities_handlers_test.go:57`, `internal/identities/identities_test.go:194`; `Trusted("", owner) == false` at `internal/delivery/rules_test.go:160`; pipeline passes the resolved user at `internal/connectors/inbound_test.go:255` | ✅ PASS (evidence assembled from three tests; no single end-to-end test covers it) |

**Status**: ✅ All 31 ACs covered · ⚠️ 1 spec-precision gap (character unit for split limits)

---

## Discrimination Sensor

Expanded depth (P0: secrets, auth, trust). 33 behavior-level mutants, each run in the scratch worktree with the tests of the affected package.

| # | File:line | Mutation | Killed? |
| --- | --- | --- | --- |
| M01 | `internal/connectors/inbound.go:67` | drop the `FromSelf` filter | ✅ `TestInboundDropsOwnMessages` |
| M02 | `internal/connectors/inbound.go:72` | `!link` falls through to routing | ✅ `TestInboundPairingCommands` |
| M03 | `internal/identities/identities.go:107` | expiry check with 1 h grace | ✅ `TestLinkIgnoresUnknownAndExpiredCodes` |
| M04 | `internal/connectors/inbound.go:129` | `pending()` never filters duplicates | ❌ **Survived** (equivalent to the spec outcome: `rooms.post` and the `messages_dedupe` index still dedupe, but a repeat downloads media again) |
| M05 | `internal/connectors/kind.go:246` | `Masked` returns raw secrets | ✅ `TestMaskedHidesSecrets` |
| M06 | `internal/connectors/service.go:240-241` | API view returns sealed values | ✅ `TestConnectorSecretsAreMasked` |
| M07 | `internal/connectors/kind.go:230` | PATCH mask overwrites the secret | ✅ `TestMergeKeepsSecretSentAsMask`, `TestConnectorSecretsAreMasked` |
| M08 | `internal/connectors/kind.go:263-265` | secrets also written to `config_json` | ✅ `TestEncodeSealsSecretsAndDecodeRestoresThem` |
| M09 | `internal/connectors/manager.go:309` | status detail not redacted | ✅ `TestManagerRedactsErrorsAndRetriesWithBackoff` |
| M10 | `internal/connectors/manager.go:39` | backoff not capped at 120 s | ✅ same |
| M11 | `internal/connectors/webhook/connector.go:190` | future timestamps accepted | ✅ `TestTimestampWindow` |
| M12 | `internal/connectors/webhook/connector.go:190` | `<=` → `<` at the 300 s edge | ✅ `TestTimestampWindow` |
| M13 | `internal/connectors/webhook/connector.go:154-155` | signature check removed | ✅ `TestWebhookRejectsBadSignatureAndOldTimestamp` |
| M14 | `internal/api/hooks_handlers.go:49` | body limit doubled | ✅ `TestWebhookRequestErrors` |
| M15 | `internal/connectors/telegram/connector.go:341` | split limit 4,097 | ✅ `TestSendSplitsLongTextAndReplies` |
| M16 | `internal/connectors/telegram/connector.go:344-345` | `reply_parameters` on every part | ✅ same |
| M17 | `internal/connectors/discord/connector.go:126` | bot authors not ignored | ✅ `TestBotMessagesAreIgnored` |
| M18 | `internal/connectors/discord/connector.go:233` | `AllowedMentions` nil (pings allowed) | ✅ `TestSendSplitsWithReferenceOnFirst` |
| M19 | `internal/connectors/discord/connector.go:234` | reference on every part | ✅ same |
| M20 | `internal/connectors/whatsapp/parse.go:316` | device suffix kept | ✅ `TestParseGroupConversation` |
| M21 | `internal/connectors/whatsapp/connector.go:246` | `quoted` dropped | ✅ `TestSendTextQuotesReply` |
| M22 | `internal/connectors/whatsapp/connector.go:293` | video sent as document | ✅ `TestSendMediaTypes` |
| M23 | `internal/connectors/telegram/connector.go:462` | transport error keeps URL (token) | ✅ `TestErrorsNeverContainTheToken` |
| M24 | `internal/connectors/webhook/connector.go:273` | non-2xx treated as success | ✅ `TestWebhookOutboundFailureIsRetried` |
| M25 | `internal/connectors/whatsapp/connector.go:190` | allowlist ignored | ✅ `TestRunDeliversMessagesOfAllowedInstances` |
| M26 | `internal/connectors/inbound.go:112` | verified sender not resolved | ✅ `TestInboundPairingCommands` |
| M27 | `internal/connectors/inbound.go:24` | `/link` not recognised | ✅ `TestInboundPairingCommands` |
| M28 | `internal/connectors/telegram/connector.go:177` | offset not +1 | ✅ `TestRunPollsWithIncreasingOffset` |
| M29 | `internal/connectors/whatsapp/connector.go:261` | media sent raw, not base64 | ✅ `TestSendMediaTypes` |
| M30 | `internal/identities/identities.go:29` | code TTL 11 min | ✅ `TestCreatePairingReturnsShortLivedCode` |
| M31 | `internal/connectors/sender.go:231` | quote a chat message even when it came from another chat | ❌ **Survived**: nothing tests a reply to a message from chat B being delivered to chat A |
| M32 | `internal/connectors/webhook/connector.go:151` | rate limit removed | ✅ `TestRateLimit` |
| M33 | `internal/connectors/manager.go:246` | send error not redacted | ✅ `TestManagerRedactsSendErrors` |

M07's first form (`case false:`) did not compile. It was rewritten to keep the variable used, then re-run.

**Sensor depth**: P0 expanded (33 mutations).
**Isolation**: real-tree `git status --porcelain` was empty before and after the sensor; the scratch worktree was removed.
**Result**: 31/33 killed. **FAIL ❌** (2 survivors)

### Probe (security)

`TestVerifierProbeCrossConnectorIdentity` was run in scratch only and not kept. Steps:
- Alice links webhook sender `alice-id` through her own webhook connector.
- Mallory's separate webhook connector posts `sender_id: "alice-id"` into Mallory's room.

Result: `author_user_id` equals Alice's id (`PROBE author_user_id="01a157f0-…" alice="01a157f0-…"`). Cause:
- `internal/identities/identities.go:131-145` and the `ResolveIdentityUser` query (`internal/store/queries/identities.sql`) match only on `connector_kind`.
- `internal/connectors/webhook/connector.go:177-180` passes the caller's `sender_id` through.

---

## Code Quality

| Principle | Status |
| --- | --- |
| Minimum code | ✅ |
| Surgical changes | ✅ (the core-fix merge is in range but is not part of this feature) |
| No scope creep | ✅ (signed file links and rate limit come from the design) |
| Matches patterns | ✅ (Store.WithTx, validation.Check, testkit, authorization matrix) |
| Spec-anchored outcome check | ✅ except the spec-precision note on TG-02/DC-02 |
| Per-layer coverage (domain 1:1, routes happy + edge + error) | ✅ every route has an authorization-matrix row plus error paths |
| Every test maps to an AC, edge case or Done-when | ✅ |
| Documented guidelines: `AGENTS.md`, `contract-notes.md` | ✅ |

---

## Edge Cases

- [x] evolution-go 401 → `unauthorized` error and backoff retry: `whatsapp/connector_test.go:232` (`Equal("unauthorized", err.Error())`) plus the manager backoff at `manager_test.go:198`.
- [x] Telegram `migrate_to_chat_id` updates the chat id: `telegram/connector_test.go:275`; rooms kept at `inbound_test.go:282-292`.
- [x] Outbound target missing → send fails → retry: `telegram/connector_test.go:384-386` (400 → error), `whatsapp/connector_test.go:343-345`, outbox retry `sender_test.go:130-131`.

---

## Gate Check

- **Gate command**: `make check` (check-generate, golangci-lint, vet, web lint/typecheck, `go test -race -count=1 ./...`, web tests, build)
- **Result**: exit 0. Every Go package `ok` (connectors 143 s, identities 101 s, api 323 s on a loaded host). Web: 27 files, 216 tests passed. golangci-lint: 0 issues.
- **Flaky tests**: none seen. The single run passed, so no re-run was needed.
- **Test count before feature** (`63a1da7`): 195 Go `Test*` functions. **After** (`8f8e72b`): 363. The range includes the merged core-fix tests.
- **Delta**: +168. In the range, `*_test.go` files have 3 deleted lines and 5,724 added; no tests were removed or weakened.
- **Skipped tests**: none.

---

## Security Review (gaps; not fixed)

| # | Severity | Weakness | Evidence |
| --- | --- | --- | --- |
| S1 | **High (blocker)** | A verified identity can be forged across connectors and users. Sender ids are trusted per kind, but webhook, a custom Telegram `api_base` and a custom evolution-go `base_url` all let a connector's owner choose sender ids freely. A forged message is then `trusted=true` for the victim's agents (AD-006). | Probe above. `internal/identities/identities.go:131-145`, `internal/connectors/webhook/connector.go:177-180`, `internal/connectors/telegram/connector.go:49`, `internal/connectors/whatsapp/connector.go:44` |
| S2 | Medium | SSRF. Any user can make the server call any URL: `outbound_url` (POST), `api_base` and `base_url` (GET/POST and WebSocket). Nothing blocks loopback, private or link-local addresses. Redirects are followed (default `http.Client`). Some response data comes back to the user: the Telegram error `description` lands in the status text (`telegram/connector.go:448`) and `/group/list` names become chat titles. | `kind.go:212-215` (only the http/https scheme is checked) |
| S3 | Low-Med | The webhook rate limit is counted before the signature check. Anyone who knows the connector id can use up the 60/min budget and block the real sender. | `internal/connectors/webhook/connector.go:151-157` |
| S4 | Low | A signed request can be replayed within ±300 s. `message_id` is optional, so a replay without it stores a duplicate. | `internal/connectors/webhook/connector.go:150-181` |

What is solid: secrets are masked and sealed, redacted in raw, query-escaped and path-escaped forms, kept out of audit details, and transport errors drop URLs. Live `connector.status` events are scoped to users with access (`internal/live/hub.go:328`). Signed file links are sealed and expire after 10 minutes. The HMAC compare is constant-time. The 64 KiB body limit returns 413.

---

## Fix Plans

### Fix 1: Scope identities to their source (S1)
- **Root cause**: IDN-02 links `(connector kind, sender id)`. On kinds whose payload the connector's owner controls, that key is not proof of identity.
- **Fix task**: amend the spec, then the code. Options:
  - (a) Resolve identities only for messages from the connector the identity was linked through: store `connector_id` and match on it.
  - (b) Keep kind-wide matching, but only for connectors owned by the identity's user.
  - (c) Disable `!link` and resolution for the webhook kind and for non-default `api_base`.

  Add a test equal to the probe that asserts an empty `author_user_id`.
- **Priority**: Blocker

### Fix 2: Per-chat quote test (M31)
- **Fix task**: in `internal/connectors/sender_test.go`, route two chats into one room, reply to chat B's message with a delivery to chat A, and assert `ReplyToID == ""`.
- **Priority**: Major

### Fix 3: Dedupe before download (M04)
- **Fix task**: in `inbound_test.go`, emit the same `MessageID` with media twice and assert `Media.Open` runs once (or `blobCount` and the attachment count stay unchanged).
- **Priority**: Minor

### Fix 4 (hardening, optional): S2-S4
- Block private, loopback and link-local destinations, or put them behind an admin setting.
- Disable redirects.
- Check the signature before counting the request toward the rate limit.
- Consider a nonce or required `message_id` for replay protection.

### Minor notes
- Discord chat titles differ between inbound (`#name`, `discord/connector.go:192`) and ListChats (`Guild #name`, `:284`). Each flip rewrites the title and publishes `chat.discovered` again.
- Inbound text over 16,000 characters is rejected with only a log line (`inbound.go:117`). WhatsApp allows longer texts.
- Refresh failures caused by the platform return an opaque 500.
- `tasks.md` contains duplicated sections.

---

## Requirement Traceability Update

| Requirement | Previous Status | New Status |
| --- | --- | --- |
| CON-01..CON-12, WA-01..WA-05, TG-01..TG-04, DC-01..DC-03, HOOK-01..HOOK-03, IDN-01, IDN-03, IDN-04 | Implementing | ✅ Verified (WA-03 test gap M31) |
| IDN-02 | Implementing | ❌ Needs Fix (S1, spec amendment) |

---

## Summary

**Overall**: ❌ Not Ready

**Spec-anchored check**: 31/31 ACs match the spec outcome; 1 spec-precision gap.
**Sensor**: 31/33 mutations killed (M04, M31 survived).
**Gate**: `make check` exit 0, all Go packages ok, 216 web tests passed.

**What works**: connector CRUD with masking and sealing, supervisor with backoff and redaction, the inbound pipeline (unrouted, dedupe, media limits, self-drop, `!link`), and the four adapters behaving as specified.

**Issues found**:
1. S1 identity forgery: scope identities to their source.
2. M31: add a per-chat quote test.
3. M04: add a dedupe-before-download test.
4. Hardening S2-S4.

**Next steps**: amend IDN-02 in the spec (Fix 1), then route Fixes 1-3 to an implementer and re-verify (iteration 1 of 3).
