# Connectors Validation

**Date**: 2026-10-20 (iteration 2)
**Spec**: `.specs/features/connectors/spec.md` (36 requirements, 5 edge cases, after the T21 amendments)
**Diff range**: `17b8c85..f4147c9` (fix tasks T12-T21; `f4147c9` also merges the core-platform and agent-channel fix iterations, which are out of scope here). Surface: `internal/connectors/**` (new `safehttp`), `internal/identities`, `internal/store/migrations/0003_identities_per_connector.sql`, `internal/store/queries/identities.sql`, `internal/api/{hooks,chats,identities}_handlers*`, `internal/api/router.go`, `internal/config`.
**Verifier**: independent sub-agent (author ≠ verifier), evidence-or-zero. Worked in the detached worktree `partyline-verify-conn`. Probes and mutants ran in the scratch worktree `/tmp/claude-1001/pl-conn-mutants2` and never in the real tree.

## Validation verdict: FAIL ❌ (minor gaps only)

All three iteration-1 blockers are fixed and pass my own checks:
- **Identity forgery (S1) is closed.** Identities now belong to a connector instance. My independent Alice/Mallory probe found that Mallory's webhook cannot resolve Alice's sender id, and the forged message reaches Alice's shared agent with `trusted=false`.
- **SSRF (S2) is closed.** Every probed private destination was refused at dial time.
- **The webhook rate limit (S3) and replay (S4) are fixed.** A flood of unauthenticated requests leaves the authenticated budget untouched, and a replay gets 409.
- **M04 and M31 are killed.** The new tests catch both.
- **All 36 ACs match the spec's outcomes, and `make check` is green.**

The verdict stays FAIL because the sensor found 4 non-equivalent survivors. Each needs only a small test, and none is a security hole in the current code:
- **N22:** CON-14 counts code points, but no test separates code points from bytes.
- **N29 and N36:** each WhatsApp client (REST and WebSocket) can lose the private-network check alone without any test failing.
- **N35:** a replay with a whitespace-padded signature header is not tested.

A probe also found one literal deviation from HOOK-05: after a connector restart, a replay is accepted again. Two residual security weaknesses are listed below (S5, S6).

### Iteration 1 (history)

FAIL. 31/33 mutants were killed; M04 (pre-download dedupe) and M31 (per-chat quote) survived. The security review raised:
- **S1 (High):** identities were keyed by connector kind, so they were forgeable across connectors.
- **S2:** SSRF.
- **S3:** the webhook rate limit was counted before the signature check.
- **S4:** signed requests could be replayed.

Minor notes: Discord titles, a long-text drop and an opaque refresh 500. These led to fix tasks T12-T21, AD-008 and migration `0003`.

---

## Task Completion

| Task | Status | Notes |
| --- | --- | --- |
| T1-T11 | ✅ Done | verified in iteration 1 |
| T12-T21 | ✅ Done (all `[x]`, 0 open boxes) | commits `37ac7b1`, `102d202`, `8ab5200`, `b03d9b9`, `cd96805`, `ddd1182`, `799a86e`, `7f8cf66`, `8553182`, `50804e6`, `613458b` |

---

## Spec-Anchored Acceptance Criteria

ACs with no code change since iteration 1 keep that report's evidence, re-checked at current line numbers. New or changed ACs are shown in **bold**.

### P1: Manage connectors

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| CON-01 | field schema per kind | `internal/api/connectors_handlers_test.go:92` - `Equal([]dto.ConnectorField{...}, bot.Fields)` | ✅ |
| CON-02 | 422 naming the field | `internal/api/connectors_handlers_test.go:110-117` | ✅ |
| CON-03 | `"********"`, never the value | `internal/api/connectors_handlers_test.go:156-169,192-196` | ✅ |
| CON-04 | starts at once; `connector.status` events | `internal/api/connectors_runtime_test.go:39,44,47` | ✅ |
| CON-05 | stopped and no sends | `internal/api/connectors_runtime_test.go:53-63`, `internal/connectors/manager_test.go:181-182` | ✅ |
| CON-06 | `error` + redacted text, backoff 2 s to 120 s | `internal/connectors/manager_test.go:198-206`; Telegram text without the description `internal/connectors/telegram/connector_test.go:388` - `Equal("sendMessage: error 400", err.Error())` | ✅ |
| **CON-13** | private destinations refused (incl. via DNS); no redirects; bounded bodies; no platform error text; flag off by default | `internal/connectors/safehttp/safehttp_test.go:31,34` (`Blocked` table, incl. CGNAT, mapped, NAT64), `:57` - `ErrorIs(err, ErrBlockedAddress, u)` for IP and `localhost`, request + stream clients, `:60` - `Zero(hits)`; `:86` - `Equal(http.StatusFound, resp.StatusCode)` (not followed); `:108` - `ErrorIs(err, ErrResponseTooLarge)`; `internal/config/config_test.go:27` - `False(cfg.AllowPrivateNetworks)`; end-to-end `internal/api/hooks_handlers_test.go:376` - `Equal("outbound: destination address not allowed", item.LastError)`; adapters `telegram/connector_test.go:423`, `whatsapp/connector_test.go:382`, `discord/connector_test.go:315` | ✅ (see N29/N36) |

### P1: Chats and inbound routing

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| CON-07 | chat record, no message | `internal/connectors/inbound_test.go:116,121` | ✅ |
| CON-08 | once per room, author kind chat + sender fields | `internal/connectors/inbound_test.go:139,145` | ✅ |
| **CON-09** | refresh; 409 when not running or the kind cannot list; **502 `unavailable`, fixed text** | `internal/api/chats_handlers_test.go:87,92` (409); `:107-110` - `RequireError(502, unavailable)`, `Equal("the platform did not answer the request", apiErr.Message)`, `NotContains(..., "10.0.0.5")` | ✅ |
| **CON-10** | stored once; repeat downloads nothing | `internal/connectors/inbound_test.go:139`; `:229` - `Equal(int32(1), opens.Load())` | ✅ |
| **CON-11** | stored attachment / `too_large` | `internal/connectors/inbound_test.go:204,207`, `:229-233` | ✅ |
| CON-12 | own messages dropped | `internal/connectors/inbound_test.go:165-169` | ✅ |
| **CON-14** | cut to 16,000 code points ending `…[truncated]`; 16,000 unchanged | `internal/connectors/inbound_test.go:250` - `Equal(exact, msgs[0].Text)`, `:252-253` - `Equal(16000, RuneCount)`, `Equal(strings.Repeat("é", 16000-12)+"…[truncated]", got)` | ✅ · ⚠️ N22: no case falls between the byte and code-point limits |

### P1: WhatsApp / Telegram / Discord

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| WA-01 | broadcast WS with global key; allowed instances | `internal/connectors/whatsapp/connector_test.go:203` | ✅ |
| WA-02 | group JID / bare DM JID | `internal/connectors/whatsapp/parse_test.go:66,83,89` | ✅ |
| **WA-03** | `/send/text`, `quoted` only for the chat's own message | `internal/connectors/whatsapp/connector_test.go:299,310`; sender side `internal/connectors/sender_test.go:113-117` - `Equal("B1", byChat[otherID].ReplyToID)`, `Empty(byChat[groupID].ReplyToID)` | ✅ (M31 killed as N26) |
| WA-04 | `/send/media` base64 + type | `internal/connectors/whatsapp/connector_test.go:329` | ✅ |
| WA-05 | `/group/list` per allowed instance | `internal/connectors/whatsapp/connector_test.go:358-369` | ✅ |
| TG-01 | offset+1; message + channel_post | `internal/connectors/telegram/connector_test.go:231` | ✅ |
| **TG-02** | ≤ 4,096 **code points**, ordered, reply on first | `internal/connectors/telegram/connector_test.go:330` - `LessOrEqual(len([]rune(part)), MaxText)`, `:340`; `internal/connectors/text_test.go:17-18` - `"limits count characters, not bytes"` (é) | ✅ (iteration-1 spec-precision gap closed) |
| TG-03 | sendPhoto / sendDocument | `internal/connectors/telegram/connector_test.go:366` | ✅ |
| TG-04 | getFile | `internal/connectors/telegram/connector_test.go:297` | ✅ |
| **DC-01** | non-bot; external id = channel id; title `<guild> #<channel>` | `internal/connectors/discord/connector_test.go:176,179` - `Equal("Test Guild #general", in.ChatTitle)` | ✅ |
| **DC-02** | ≤ 2,000 code points; reference on first | `internal/connectors/discord/connector_test.go:241` (runes), `:244-249` | ✅ |
| DC-03 | text channels of every guild | `internal/connectors/discord/connector_test.go:288` | ✅ |

### P1: Generic webhook

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| HOOK-01 | 202 + stored | `internal/api/hooks_handlers_test.go:169-185` | ✅ |
| HOOK-02 | 401, nothing stored | `internal/api/hooks_handlers_test.go:190-209` | ✅ |
| HOOK-03 | signed POST; non-2xx = failure | `internal/api/hooks_handlers_test.go:279-345` | ✅ |
| **HOOK-04** | check before budget; 60/min authenticated; 30/min/IP invalid then 429 without audit | `internal/connectors/webhook/connector_test.go:88` - `ErrorIs(..., ErrRateLimited)` on the 61st; `:104-117` per-IP budget, the valid request still passes; `internal/api/hooks_handlers_test.go:216-238` - 100 bad → 401×30, 429×70, then 202, `Equal(webhook.InvalidRateLimit, rejected)` | ✅ |
| **HOOK-05** | repeated signature within 10 min → 409, nothing stored | `internal/api/hooks_handlers_test.go:251-255` - 202 then `RequireError(409, conflict)`, `Len(msgs, 1)`; `internal/connectors/webhook/connector_test.go:132-137` (expiry at the window edge) | ✅ in-process · ❌ probe: accepted again after a restart (gap 3) |

### P1: Verified identities

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| IDN-01 | 6 chars A-Z/2-9, 10 min | `internal/api/identities_handlers_test.go:39-40` | ✅ |
| **IDN-02** | link (connector id, sender, alt); consume; not routed; resolves only on that connector | `internal/connectors/inbound_test.go:287,291-295` - `Equal(e.conn.ID, ids[0].ConnectorID)`; `:329` - `Empty(msgs[0].AuthorUserID)` on another connector of the same kind; `internal/identities/identities_test.go:249` (per connector) | ✅ |
| IDN-03 | unknown/expired ignored | `internal/connectors/inbound_test.go:282-283`, `internal/identities/identities_test.go:151` | ✅ |
| **IDN-04** | identity or connector deleted → no user → `trusted=false` | `internal/api/identities_handlers_test.go:67` - `Empty(user, "IDN-04...")`; `internal/identities/identities_test.go:287` - `Empty(list)` after `DeleteConnector`; trusted=false end to end `internal/api/hooks_handlers_test.go:443-444` | ✅ |
| **IDN-05** | webhook links only the owner's codes; a foreign code links nothing and stays unused | `internal/api/hooks_handlers_test.go:425` - `Len(identitiesOf(e.alice), 1, "mallory's connector linked nothing")`, `:463` - the code still links; `internal/identities/identities_test.go:263,270` | ✅ |

**Status**: ✅ 36/36 ACs covered with the spec's outcome. 0 spec-precision gaps (TG-02/DC-02 now say code points). 1 behavioural deviation found by a probe (HOOK-05 after a restart).

---

## Independent probes (scratch only, not kept)

| Probe | Result |
| --- | --- |
| Alice links `alice-id` through her webhook. Mallory, on her own webhook, links her own code to `alice-id`, routes her chat to Alice's shared agent and posts as `alice-id` | Alice still has 1 identity. The forged message reaches Alice's agent with `author_user_id` = **Mallory** (her own link), not Alice, and `trusted=false`. Deleting Alice's connector empties her identities. ✅ |
| SSRF, default config: `127.0.0.1`, `localhost`, `0.0.0.0`, `[::ffff:127.0.0.1]`, `10/8`, `172.16/12`, `192.168/16`, `100.64/10`, `169.254.169.254`, `[::1]`, `ip6-localhost`, `[fc00::1]`, `[fd00::1]`, `[fe80::1]`, `224.0.0.1`, and the DNS names `127.0.0.1.nip.io` and `localtest.me` (both resolve to loopback) | All blocked with `ErrBlockedAddress` at dial time; 0 hits on the loopback servers. `127.1`, `2130706433` and `0` do not resolve in Go (no connection). A redirect to another server is not followed (307 returned, target hits 0). The stream client also blocks. ✅ |
| Webhook flood: 2,000 bad signatures from 2,000 IPs, then 50 stale timestamps from one IP | The stale ones get 401×30, then 429. Then all 60 authenticated requests pass and the 61st gets 429. Replays (×100) do not consume the budget. A whitespace-padded signature gets 409, an upper-cased one 401. ✅ |
| Replay at the API level | 202 then 409 ✅. **After disable → enable of the connector, the same signed request gets 202** ❌ (the cache is in memory, per instance) |
| Alice's code arrives through Mallory's *non-webhook* connector (fake kind) | Alice's identity is linked **on Mallory's connector** (S5 below) |

---

## Discrimination Sensor

P0 expanded depth: 37 mutants in the scratch worktree, running each affected package's tests (API tests filtered to `Identit|Webhook|Chat`).

| # | File | Mutation | Killed? |
| --- | --- | --- | --- |
| N01 | `internal/store/db/identities.sql.go:159` | resolve by connector **kind** again | ✅ `TestResolveSenderAndDelete`, `TestLinkIsPerConnector` |
| N02 | same | resolve ignores the connector | ✅ same |
| N03 | `identities.sql.go:68` | Link replaces the sender on every connector | ✅ `TestLinkIsPerConnector` |
| N04 | `internal/connectors/inbound.go:76` | owner-only check skipped | ✅ `TestWebhookIdentityCannotBeForgedAcrossConnectors` |
| N05 | `webhook/connector.go:61` | webhook kind not `LinkOwnerOnly` | ✅ same |
| N06 | `internal/identities/identities.go:128` | a refused foreign code is consumed | ✅ `TestLinkOnlyForRequiredUser` |
| N07 | `identities.go:127` | Link ignores the required user | ✅ same |
| N08 | `safehttp/safehttp.go:115` | skip the dial-time check | ✅ `TestClientRefusesPrivateDestinations` |
| N09 | `safehttp.go:130` | request client follows redirects | ✅ `TestClientDoesNotFollowRedirects` |
| N10 | `safehttp.go:137` | stream client follows redirects | ✅ same |
| N11 | `safehttp.go:44` | CGNAT not blocked | ✅ `TestBlocked` |
| N12 | `safehttp.go:75` | drop `IsLinkLocalUnicast` | ➖ Survived, **equivalent** (169.254/16 and fe80::/10 are also listed as prefixes) |
| N12b | `safehttp.go:56` | drop the `fe80::/10` prefix | ➖ Survived, **equivalent** (`IsLinkLocalUnicast` still blocks it) |
| N13 | `safehttp.go:70` | NAT64-embedded IPv4 not checked | ✅ `TestBlocked` |
| N14 | `safehttp.go:128` | response cap +1 | ✅ `TestClientBoundsResponses` |
| N15 | `telegram/connector.go:451` | description back in the error text | ✅ `TestErrorsNeverContainTheToken` |
| N16 | `webhook/connector.go:181` | count every request before the signature | ✅ `TestRateLimit`, `TestInvalidRequestsHaveTheirOwnBudget` |
| N17 | `webhook/connector.go:237` | replay cache off | ✅ `TestReplayIsRefused` |
| N18 | `webhook/connector.go:51` | replay window 5 min | ✅ same |
| N19 | `webhook/connector.go:265` | no per-IP invalid budget | ✅ `TestInvalidRequestsHaveTheirOwnBudget` (first form did not compile; rewritten) |
| N20 | `inbound.go:146` | keep 16,000 + marker | ✅ `TestInboundLongTextIsTruncated` |
| N21 | `inbound.go:143` | `<=` → `<` (16,000 truncated) | ✅ same |
| **N22** | `inbound.go:143` | limit counted in **bytes** (`len(text)`) | ❌ **Survived** |
| N23 | `internal/api/router.go:184-185` | `ErrPlatform` unmapped (500) | ✅ `TestRefreshChatsPlatformFailure` |
| N24 | `router.go:185` | 502 message carries the platform text | ✅ same |
| N25 | `discord/connector.go:199` | inbound title `#channel` | ✅ `TestBotMessagesAreIgnored` |
| N26 | `internal/connectors/sender.go:87` | **M31 re-applied** (quote another chat's message) | ✅ `TestReplyQuotesOnlyInTheOriginChat` |
| N27 | `inbound.go:152` | **M04 re-applied** (no pre-download dedupe) | ✅ `TestInboundRepeatDownloadsNothing` |
| N28 | `migrations/0003_identities_per_connector.sql:12` | no `ON DELETE CASCADE` | ✅ `TestIdentitiesFollowTheirConnector` |
| **N29** | `whatsapp/connector.go:113` | WebSocket dial with a plain `http.Client` | ❌ **Survived** |
| N30 | `telegram/connector.go:88` | client ignores the setting | ✅ `TestPrivateAPIBaseIsRefusedByDefault` (by the 15 min test timeout: `Run` no longer returns) |
| N31 | `webhook/connector.go:146` | outbound plain client | ✅ `TestWebhookOutboundToPrivateAddressIsRefusedByDefault` |
| N32 | `discord/connector.go:102` | media client ignores the setting | ✅ `TestPrivateAttachmentURLIsRefusedByDefault` |
| N33 | `internal/config/config.go:69` | flag defaults to true | ✅ `TestLoadDefaults` |
| N34 | `internal/app/app.go:175` | app always allows private networks | ✅ `TestWebhookOutboundToPrivateAddressIsRefusedByDefault` |
| **N35** | `webhook/connector.go:188` | replay cache keyed by the raw header (a padded copy bypasses it) | ❌ **Survived** |
| **N36** | `whatsapp/connector.go:101` | REST client ignores the setting | ❌ **Survived** (`Run` then fails at the WebSocket dial, so the test still sees `ErrBlockedAddress`) |

**Isolation**: the real tree's `git status --porcelain` was empty before and after. The scratch tree's porcelain was empty after every mutant (`git checkout -- .` in scratch only). No stash was used.
**Result**: 31 killed, 2 equivalent, **4 survived (N22, N29, N35, N36)**. **FAIL ❌**

---

## Code Quality

| Principle | Status |
| --- | --- |
| Minimum code / surgical / no scope creep | ✅ (`safehttp` is one small package; the webhook keeps its own state) |
| Matches patterns | ✅ (`WithTx`, `validation`, testkit, `mapError`) |
| Spec-anchored outcome check | ✅ |
| Per-layer coverage | ✅ unit (`safehttp`, `webhook`, `identities`) plus e2e (`hooks_handlers_test`) |
| Every test maps to an AC, edge case or Done-when | ✅ |
| Documented guidelines (`AGENTS.md`, `contract-notes.md`, README env table) | ✅ `README.md:129`, `README.pt-BR.md:130` |

---

## Edge Cases

- [x] evolution-go 401 → `unauthorized` + backoff: `whatsapp/connector_test.go:233`, `manager_test.go:198`
- [x] Telegram `migrate_to_chat_id`: `telegram/connector_test.go:269`, `inbound_test.go:349`
- [x] Outbound target missing → retry: `telegram/connector_test.go:384-388`, `sender_test.go:151`
- [x] Linked sender through another connector of the same kind → no user: `inbound_test.go:329`, `identities_test.go:249`
- [x] Migration `0003` drops old identities: `internal/store/store_test.go:189` - `Zero(n)`, plus the `connector_id` column check

---

## Gate Check

- **Gate command**: `make check` (generate drift, golangci-lint, vet, web lint/typecheck, `go test -race -count=1 -timeout 20m ./...`, web tests, build)
- **Run 1**: exit 2. Every failure was `TempDir: ... no space left on device`, because the host disk was at 100% (154 MB free). This was environmental, not a code failure.
- **Run 2** (after space was freed): **exit 0**. golangci-lint reported 0 issues. All 27 Go packages `ok` (api 462 s, connectors 180 s on a loaded host). Web: 27 files, 216 tests passed.
- **Test count**: 363 → 404 Go `Test*` functions (the range includes the merged core and channel fixes). In `*_test.go` files, +1,347 / −119 lines. The deleted lines are rewritten assertions (the outbox helper was extracted, `#general` became `Test Guild #general`, the rate-limit test uses `numbered`). No test was removed or weakened.
- **Skipped**: none.

---

## Security Review (remaining; not fixed)

| # | Severity | Weakness | Evidence |
| --- | --- | --- | --- |
| S5 | Medium (residual of S1) | A pairing code is honoured by **any** non-webhook connector. Say Alice sends `/link CODE` in a group where another user's bot also listens, or her code leaks. If Mallory's connector handles it first, Alice's identity is linked on **Mallory's** connector. Mallory can later point that connector's `api_base`/`base_url` at her own server and send Alice's sender id as trusted. AD-008 accepts the trade-off "linking through someone else's connector means trusting its owner", but here the link can happen without Alice choosing it. Changing a connector's endpoint keeps its identities. | Probe 5; `internal/connectors/inbound.go:74-85` (only `LinkOwnerOnly` kinds check the owner); `internal/connectors/service.go:295-305` (config change keeps identities) |
| S6 | Low | `PARTYLINE_ALLOW_PRIVATE_NETWORKS` is all or nothing. The common setup (evolution-go on `localhost:8088`) needs it on, which removes SSRF protection for every user's connectors. | `internal/config/config.go:69`; `safehttp.Transport` |
| S7 | Low | Replay cache is in memory per connector instance. A captured signed request replays once more after a restart: connector disable/enable, config edit or server restart. Without a `message_id` it stores a duplicate. | Probe 4; `internal/connectors/webhook/connector.go:110,147` |
| S8 | Low | The default response cap is 256 MiB. A hostile `api_base` can make the server buffer up to that much per Bot API call (`json.Decoder` into `RawMessage`). | `safehttp.go:29`; `telegram/connector.go:439` |

What is solid now:
- Dial-time address checks run after DNS, so rebinding cannot bypass them.
- IPv4-mapped, NAT64, 6to4 and Teredo forms are covered.
- Proxies are ignored while blocking is on, and redirects are never followed.
- Telegram error descriptions are dropped.
- 502 responses use fixed text.
- The webhook checks the signature before any budget; the invalid-request budget is bounded per IP and capped at 10k tracked IPs.
- The replay cache key is the computed signature, and the cache is bounded at about 600 entries.
- Identities are deleted with their connector (FK cascade, `foreign_keys(1)`).

---

## Fix Plans

1. **N22 (CON-14, Minor):** add to `TestInboundLongTextIsTruncated` a text of 10,000 `é` (20,000 bytes, under 16,000 code points) and assert it is stored unchanged. With a byte count, `truncate` would even slice past the end of the rune slice.
2. **N29 + N36 (CON-13, Minor):** test each WhatsApp client on its own. For example, `ListChats`/`Send` against a private `base_url` → `ErrBlockedAddress`, and a WebSocket dial helper called directly → `ErrBlockedAddress` (or a fake with REST public-allowed and WS private).
3. **HOOK-05 after restart (S7, Minor):** keep accepted signatures across restarts with expiry (table `webhook_seen(connector_id, sig, until)`). Alternatively, amend HOOK-05 to "within the running connector instance". Add an e2e test: accept, disable, enable, replay → 409.
4. **N35 (HOOK-05, Cosmetic):** extend `TestReplayIsRefused` with a whitespace-padded copy of the signature → `ErrReplayed`.
5. **S5 (hardening, decide first):** possible fixes:
   - treat connectors with a non-default `api_base`/`base_url` as `LinkOwnerOnly`;
   - drop a connector's identities when its endpoint or credentials change;
   - show the linking connector's owner on the identities page.

   Record the choice in AD-008.
6. **S6/S8 (optional):** a per-host allowlist instead of the global flag, and smaller per-call caps for JSON APIs.

---

## Requirement Traceability Update

| Requirement | Previous Status | New Status |
| --- | --- | --- |
| CON-06, CON-09, CON-10, CON-11, WA-03, TG-02, DC-01, DC-02, DC-03, HOOK-01, HOOK-02, HOOK-04, IDN-02, IDN-04, IDN-05 | Implemented, re-verify | ✅ Verified |
| CON-13 | Implemented, re-verify | ⚠️ Verified behaviour; test gap N29/N36 |
| CON-14 | Implemented, re-verify | ⚠️ Verified behaviour; test gap N22 |
| HOOK-05 | Implemented, re-verify | ❌ Needs fix (replay after restart; N35) |
| All others | Verified | ✅ Verified |

---

## Summary

**Overall**: ❌ Not Ready. The gaps are minor; security blockers S1-S4 are closed.

**Spec-anchored check**: 36/36 ACs match the spec's outcome; 0 spec-precision gaps; 1 probe deviation (HOOK-05 after a restart).
**Sensor**: 31/37 killed, 2 equivalent, 4 survived (N22, N29, N35, N36).
**Gate**: `make check` exit 0 on run 2 (run 1 failed on a full disk). 27/27 Go packages ok; 216 web tests passed.

**Next steps**: fix iteration 2 (fix plans 1-4, plus a decision on S5), then re-verify. This is iteration 3 of 3, the last before escalating.
