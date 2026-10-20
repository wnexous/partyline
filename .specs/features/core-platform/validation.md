# Core Platform Validation

**Date**: 2026-10-19
**Spec**: `.specs/features/core-platform/spec.md` (50 requirements)
**Diff range**: `bba5e2d..534bf61` (60 commits; core-platform surface = `internal/`, `cmd/`, `Dockerfile`, `docker-compose.yml`, `.github/workflows/`)
**Verifier**: independent sub-agent (author ≠ verifier), evidence-or-zero

## Validation verdict: FAIL ❌

Most of the platform is solid: 40 of 50 ACs are covered by tests whose assertions target the outcome the spec defines, and 23 of 24 injected mutants were killed. The verdict is FAIL for four reasons:

1. **ROOM-01 is violated.** A probe showed that an outbox entry queued before a chat member was removed is still sent after the removal.
2. **AUTH-05 can be bypassed with concurrent requests.** A probe sent 30 parallel wrong-password attempts from one IP. All 30 had their password checked and none got 429.
3. **One mutant survived (M17).** No test covers the hub's own Origin check against an Origin with the same host but a different scheme.
4. **One spec edge case has no evidence.** "Last manager of a room is deleted → delete the room" cannot be tested, because there is no way to delete a user.

The first gate run failed on a timing-sensitive test. The re-run passed.

---

## Task Completion

| Task | Status | Notes |
| ---- | ------ | ----- |
| T1-T27 | ✅ Done (all `[x]` in tasks.md) | T27's Docker build runs only in CI (no local Docker). Nothing in this check exercised it. |

---

## Spec-Anchored Acceptance Criteria

Legend: ✅ the assertion targets the spec-defined outcome · ⚠️ spec-precision or scope gap · ❌ gap (outcome violated or no evidence)

### P1: First-run setup and accounts

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| AUTH-01 | Setup token printed to the log while there are zero users; setup accepted only with that token | `internal/cli/serve_test.go:282-286` `findLog(..., msgSetupToken)` with `setup_token` non-empty at WARN; `:321` the line is absent once a user exists; `internal/users/auth_test.go:82-84` wrong or empty token → `ErrBadSetupToken`, user count 0; `internal/api/auth_handlers_test.go:39-40` → 401 | ✅ |
| AUTH-02 | Setup after a user exists → 409, nothing created | `internal/users/auth_test.go:107-109` `ErrorIs(err, ErrSetupDone)`, `EqualValues(1, userCount)`; `internal/api/auth_handlers_test.go:59-60` `RequireError(409, conflict)` | ✅ |
| AUTH-03 | Cookie is HttpOnly, SameSite=Lax, valid 30 days | `internal/api/auth_handlers_test.go:22-24` `True(c.HttpOnly)`, `Equal(SameSiteLaxMode)`, `Equal(thirtyDays, c.MaxAge)`; sliding at `:137-157`; `internal/users/auth_test.go:147,211-229` | ✅ |
| AUTH-04 | 401 with the same body for an unknown user and a wrong password | `internal/api/auth_handlers_test.go:94-96` both `RequireError(401)` and `Equal(unknown.Body, wrong.Body)`; `internal/users/auth_test.go:134` | ✅ |
| AUTH-05 | More than 5 failures per IP within 1 minute → 429 for the rest of that minute | `internal/users/auth_test.go:171-191` (5 failures, then `ErrRateLimited` until the window closes, other IP unaffected); `internal/api/auth_handlers_test.go:106-111` 6th attempt → `429 rate_limited`. **Concurrency probe** (scratch): 30 parallel wrong-password logins from one IP gave 30×401 and 0×429. `users/auth.go:95-100` checks `Blocked` before the argon2 verify and records `Fail` after it, so a whole burst passes the check. **Precision:** the spec says "more than 5"; the code blocks the 6th attempt (stricter, the safe direction). | ❌ GAP (concurrency) + ⚠️ precision |
| AUTH-06 | Single-use link that expires after 7 days | `internal/users/invites_test.go:53,56-66` expiry = now+7d (valid at 7d−1ms, gone at 7d); `:96-99` reuse → `ErrInviteGone`; `internal/api/admin_handlers_test.go:312-314` → 410 | ✅ |
| AUTH-07 | Valid username and password ≥10 → account created, invite marked used | `internal/users/invites_test.go:77-93` (account + `InviteStatusUsed`, `UsedBy`); `:152-171` (pattern/length rejections, boundary 3 chars / 10 chars accepted); `internal/api/admin_handlers_test.go:331-336` → 422 naming the field | ✅ |
| AUTH-08 | Passwords stored only as argon2id, tokens only as SHA-256 | `internal/authn/authn_test.go:17-18` prefix `$argon2id$v=19$m=19456,t=2,p=1$`; `:85-86` hex sha256; `internal/users/tokens_test.go:307,316-317` stored `TokenHash == HashToken(secret)`, length 64; `internal/users/auth_test.go:149-151` session stored by hash | ✅ |
| AUTH-09 | After logout or session revoke, the cookie → 401 | `internal/api/auth_handlers_test.go:131` `RequireError(401)` after logout; `:239-240` revoked session → 401; `internal/users/auth_test.go:206-207` | ✅ |
| AUTH-10 | Disabled user's sessions and tokens → 401 | `internal/api/admin_handlers_test.go:375-376` cookie and token → 401; `internal/users/tokens_test.go:429-430`; `internal/users/invites_test.go:212-213` | ✅ |

### P1: Tokens

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| TOK-01 | Secret shown once with prefix `pl_agent_`/`pl_user_`; afterwards only the 12-char prefix | `internal/users/tokens_test.go:303-305` `HasPrefix(secret,"pl_"+kind+"_")`, `Len(tok.Prefix,12)`; `internal/api/admin_handlers_test.go:432-448` list body `NotContains(secret)`, `Len(prefix,12)` | ✅ |
| TOK-02 | Revoked, expired or unknown token → 401 | `internal/users/tokens_test.go:382-401` (`ErrUnauthenticated` for each, expiry boundary −1ms valid); `internal/api/admin_handlers_test.go:461-463`; `internal/api/middleware_test.go:202` | ✅ |
| TOK-03 | Agent token → 403 everywhere except agent endpoints | `internal/api/middleware_test.go:188-196` `RequireError(403, forbidden)` on `/api/v1/rooms`, passes on `/api/v1/agent/rooms`; `internal/api/admin_handlers_test.go:452-453` | ✅ |
| TOK-04 | `last_used_at` written at most once per minute | `internal/users/tokens_test.go:450-467` (unchanged at +59 s, updated at +60 s) | ✅ |

### P1: Rooms, members and sharing

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| ACL-01 | Creator is owner with `manage` | `internal/rooms/rooms_test.go:129-130` `Equal(alice.UserID, r.OwnerID)`, `Equal(LevelManage, r.Level)`; `internal/api/rooms_handlers_test.go:274-276` | ✅ |
| ACL-02 | view<post<manage; level = max(ownership, direct grant, `*` grant) | `internal/access/access_test.go:127-158` table, including `own view, everyone post → post`; `:170-178` order | ✅ |
| ACL-03 | No `view` → 404 for the room, its messages and its members | `internal/api/rooms_handlers_test.go:198-239` matrix stranger/admin = 404 on get, members, messages, attachments (`RequireError(404, not_found)` via `authmatrix_test.go:86-87`); `internal/rooms/rooms_test.go:178-191` | ✅ |
| ACL-04 | `view` without `post` posting → 403 | `internal/api/rooms_handlers_test.go:228-233` viewer=403; `:305`; `internal/rooms/messages_test.go:142-143` `ErrForbidden` | ✅ |
| ACL-05 | Adding an agent needs ownership or a `use` grant, else 403 | `internal/rooms/rooms_test.go:324-331` `ErrorIs(ErrForbidden)` then OK after the grant; `internal/api/rooms_handlers_test.go:336` → 403 | ✅ |
| ACL-06 | Adding a chat needs ownership of its connector or a `use` grant, else 403 | `internal/rooms/rooms_test.go:358-365`; `internal/api/rooms_handlers_test.go:337` → 403 | ✅ |
| ACL-07 | When **the owner** sets visibility, grants are replaced: private = none; shared = listed users at their level; public = everyone at view/post | `internal/access/access_test.go:340-370` exact grant rows per preset (`[]string{"*=post"}`, empty for private); `internal/api/rooms_handlers_test.go:300-315`. **Deviation:** a `manage` grantee can also set sharing (`internal/rooms/rooms_test.go:297-300`, documented in contract-notes "rooms: manage, not owner-only"). The spec text says "owner". | ⚠️ spec deviation |
| ACL-08 | Admins get no implicit access | `internal/access/access_test.go:189-206` `Equal(None, l)` on room, agent and connector; matrix admin column 404 `internal/api/rooms_handlers_test.go:198-239` | ✅ |
| ROOM-01 | Removed member gets no future deliveries in that room | `internal/rooms/messages_test.go:211-212` new posts exclude the removed chat; `internal/api/rooms_handlers_test.go:356-357`. **Probe (scratch) failed:** an entry queued before the removal was sent by `OutboxWorker.Flush` after it. `RemoveMember` (`internal/rooms/members.go:166-178`) leaves the pending/retrying outbox rows, and `ListDueOutbox` (`internal/store/queries/outbox.sql:1-6`) does not check membership. | ❌ GAP |
| ROOM-02 | Room delete removes messages, attachments, cursors and outbox entries | `internal/rooms/rooms_test.go:454-458` all row counts 0, other room untouched. **Precision:** blob files stay on disk until the retention orphan sweep (≤24 h + 1 h grace; contract-notes "blob files are left to the retention orphan sweep"). | ⚠️ precision (attachment files deleted later, not with the room) |

### P1: Messages and delivery rules

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| MSG-01 | Next per-room seq, starting at 1, no gaps | `internal/rooms/messages_test.go:92-96` 50 concurrent posts → `Equal(int64(i+1), s)`; `:119` rejected posts use no seq; `internal/api/rooms_handlers_test.go:371` | ✅ |
| MSG-02 | Text >16,000 chars, or empty text without attachments → 422 | `internal/api/rooms_handlers_test.go:379-383` `RequireError(422, invalid)` for "", "   ", 16,001 chars, and 16,000 accepted; `:435-436` file only OK, nothing at all 422; `internal/rooms/messages_test.go:110-118` (multibyte runes) | ✅ |
| DLV-01 | Never delivered back to the author | `internal/delivery/rules_test.go:240,244-245` exhaustive property `Equal(!isAuthor, agents[m.ID])` over 560 combinations | ✅ |
| DLV-02 | Delivered to every agent member except the author | `internal/delivery/rules_test.go:167,174,240` (Plan.AgentIDs). **Scope:** core has only the Plan; nothing dispatches to agents yet (agent-channel feature). No end-to-end evidence. | ⚠️ scope (rule level only) |
| DLV-03 | No recipients → queued for every chat with `outbound` on | `internal/delivery/rules_test.go:167,252-253`; `internal/rooms/messages_test.go:180-187` `Equal(sorted(c1,c3), outboxChats(m))`, status pending | ✅ |
| DLV-04 | Explicit recipients → only the listed chats | `internal/delivery/rules_test.go:170-172,250-251`; `internal/rooms/messages_test.go:190-193` `Equal([]string{c2}, ...)` (outbound off but listed) | ✅ |
| DLV-05 | `bridge_chats` on → chat posts reach other outbound chats, otherwise none | `internal/delivery/rules_test.go:179-182,246-247`; `internal/rooms/messages_test.go:201-208` (no bridge → empty; bridge → `[c3]`) | ✅ |
| DLV-06 | `trusted=true` only when the author resolves to the recipient agent's owner (user, owner's agent, owner's verified identity) | `internal/delivery/rules_test.go:288-296` `Trusted` table; `internal/rooms/messages_test.go:230-243` `author_user_id` = user / agent's owner / verified sender's user / empty. **Scope:** no agent dispatcher sets the flag in core; identity verification belongs to the connectors feature. | ⚠️ scope (rule + input level) |
| DLV-07 | Exponential backoff from 2 s, at most 5 attempts, then `failed` | `internal/delivery/outbox_test.go:199-218` `Equal(now.Add(wait), NextAttemptAt)` for 2/4/8/16 s, `Equal("failed")`, `Equal(5, Attempts)`, never retried after; restart `:237-255` | ✅ |
| DLV-08 | 25 consecutive agent messages → pause agent delivery and post a system message | `internal/delivery/rules_test.go:303-311` pauses exactly at 25; `internal/rooms/messages_test.go:317-334` `True(AgentsPaused)`, system notice with seq+1, not sent to chats. **Scope:** the "pause delivery to agents" part is a flag; the agent dispatcher that must honour it is not in core. | ⚠️ scope |
| DLV-09 | `manage` resume resets the counter and resumes | `internal/rooms/messages_test.go:341-353` (post-level 403, stranger 404, manager → `False(AgentsPaused)`, streak 0); `internal/api/rooms_handlers_test.go:467-468` | ✅ |
| DLV-10 | User or chat message resets the counter to 0 | `internal/rooms/messages_test.go:299-306` `Equal(0, streak())` after a user post and after a chat post; `internal/delivery/rules_test.go:327-329` | ✅ |

### P1: Live updates

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| LIVE-01 | `room.message` pushed to every live connection of users with `view` | `internal/api/live_test.go:48-55` two alice connections + bob(view) get it, payload id/room/text/seq asserted | ✅ |
| LIVE-02 | No event about a resource to a user without access | `internal/api/live_test.go:56` `carolLive.ExpectNone`; `:89-101` revoked → `room.deleted` then nothing; `:148-149` outbox events withheld from a connector grantee without room view | ✅ |
| LIVE-03 | Upgrade with Origin ≠ public URL → 403 | `internal/api/live_test.go:157-160` `Equal(403, resp.Status)` for a foreign origin and `null`. M17 survived (see sensor): no test for same host with a different scheme. | ✅ (assertion matches; mutant gap below) |
| LIVE-04 | `agent.presence`, `connector.status`, `outbox.status` pushed to users with access | `internal/api/live_test.go:124-149` payloads asserted by value for alice and the bob grantee; carol `ExpectNone` | ✅ |

### P1: Secure, observable deployment

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| SEC-01 | CSP, `nosniff`, `Referrer-Policy: same-origin`, `X-Frame-Options: DENY` on every HTML response | `internal/api/webui_test.go:181-186` exact values on the SPA route; `internal/webui/webui_test.go:236,240-248`; API responses `internal/api/middleware_test.go:319-322` | ✅ |
| SEC-02 | Cookie-authenticated state change with a foreign Origin or Sec-Fetch-Site → 403 | `internal/api/middleware_test.go:240-250` `RequireError(403, forbidden)` for a foreign Origin, `cross-site`, `same-site`, and a missing proof; same origin allowed `:252-256` | ✅ |
| SEC-03 | Body >1 MiB JSON or > upload limit multipart → 413 | `internal/api/middleware_test.go:333-348` (MaxJSONBody+1 → 413 `too_large`, chunked too, multipart limit); `internal/api/rooms_handlers_test.go:438-442` file of 1 MiB+1 with a 1 MB limit → 413 and no message stored | ✅ |
| SEC-04 | Connector secrets sealed with AES-256-GCM using `PARTYLINE_SECRET_KEY`; generated key persisted 0600 | `internal/secrets/box_test.go:31-71` round trip, tamper and wrong key fail; `:103-109` `Equal(FileMode(0o600))` and reuse; `internal/secrets/box.go:19` `KeySize = 32`. **Scope:** no connector stores secrets yet (connectors feature). | ⚠️ scope (primitive only) |
| SEC-05 | Message text, passwords and token secrets never logged | `internal/cli/serve_test.go:315-317` `NotContains(logs, secret)` for the password, wrong password, message text, token, cookie and bad bearer during a real `serve`; `:398-402` redaction | ✅ |
| OPS-01 | `/healthz` 200 unauthenticated; `/readyz` 503 until migrated | `internal/app/app_test.go:48-57` | ✅ |
| OPS-02 | Daily job deletes messages and attachments past retention (default 90, 0 = forever) | `internal/rooms/retention_test.go:347-375` (7-day boundary, 0 keeps, orphan blob removed, shared kept); `internal/api/rooms_handlers_test.go:278` default 90; `internal/app/app_test.go:118-137` runs at start | ✅ |
| OPS-03 | Image runs as non-root and stores all state under `/data` | `Dockerfile:31,37` distroless `nonroot`, `USER 65532:65532`; `Dockerfile:36,39` `PARTYLINE_DATA_DIR=/data`, `VOLUME /data`; `internal/cli/healthcheck_test.go:39-48`. Not built or run in this check (CI only). | ⚠️ inspection only |

### P2: Audit trail

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| AUD-01 | Login, logout, invite, token, grant, user-state and connector changes logged with actor/action/target/time, no secrets | `internal/api/audit_test.go:66-119` actor/target per action by value; `:121-123` time set; `:126-129` `NotContains` for passwords, token, invite token and sessions. **Scope:** connector changes are not in core. | ⚠️ scope (connector actions untested) |
| AUD-02 | Newest first, 50 per page | `internal/api/audit_test.go:140-154` `Len(50)`, `t57` first, `HasMore`, second page 10; non-admin 403 `:157` | ✅ |

**Status**: ❌ 2 gaps (ROOM-01, AUTH-05 under concurrency) · ⚠️ 8 precision/scope gaps (ACL-07, ROOM-02, DLV-02, DLV-06, DLV-08, SEC-04, OPS-03, AUD-01) · ✅ 40/50 matched the spec outcome.

Payload/conjunction rule: the outbox, live-frame, audit and Delivery payloads are asserted by value (for example `internal/delivery/outbox_test.go:160` asserts the whole `Delivery{}` struct, and `internal/api/live_test.go:128,135` assert the whole frame). No call-only assertions were found on critical paths.

---

## Edge Cases

- [x] Concurrent posts get distinct consecutive seqs (MSG-01): `internal/rooms/messages_test.go:67-100`
- [x] Restart resumes queued and interrupted sends (DLV-07): `internal/delivery/outbox_test.go:237-255`
- [ ] Last manager of a room deleted → room deleted (ROOM-02): **no evidence.** There is no user deletion (no API, CLI or service). The only mechanism is the FK `rooms.owner_id ... ON DELETE CASCADE` (`internal/store/migrations/0001_init.sql:93`), which would delete the room when the *owner* is deleted, even if other managers remain. That does not match "last manager".
- [x] Grant to a disabled user is kept but denies access (AUTH-10): `internal/access/access_test.go:227-250`

---

## Discrimination Sensor

Expanded tier (auth, security and delivery are critical paths). The scratch was a detached worktree `git worktree add --detach /tmp/claude-1001/pl-mutants 534bf61`. Each mutation was applied, the covering tests were run (`go test -count=1 -run …`), and the file was restored with `git checkout` inside the scratch. The scratch was then removed with `git worktree remove --force`. Verify-tree `git status --porcelain` was empty before and after the sensor (diff identical).

| # | File:line | Mutation | Killed? |
| - | --------- | -------- | ------- |
| M1 | `internal/api/middleware.go:247` | CSRF ignores a foreign `Origin` (only Sec-Fetch-Site checked) | ✅ Killed (TestCSRFCookieWrites, TestCSRFAnonymousForeignOrigin) |
| M2 | `internal/api/middleware.go:224` | Agent-token restriction widened to all of `/api/v1/` | ✅ Killed (TestAgentTokenRestrictedToAgentRoutes, TestTokenEndpoints) |
| M3 | `internal/access/access.go:188` | Admin role gets implicit `manage` | ✅ Killed (TestAdminWithoutGrantHasNoAccess, TestStrangerAndAdminGetNotFound, TestRoomRoutesAuthMatrix) |
| M4 | `internal/access/access.go:57` | `maxLevel` keeps the lowest non-empty level | ✅ Killed (TestRoomLevelIsHighestOfOwnershipAndGrants) |
| M5 | `internal/delivery/rules.go:55` | Plan no longer excludes the author | ✅ Killed (TestPlanTable) |
| M6 | `internal/delivery/rules.go:80` | Agent/user posts ignore the `outbound` flag | ✅ Killed (TestPlanTable) |
| M7 | `internal/delivery/rules.go:82` | Bridged chat posts ignore `outbound` | ✅ Killed (TestPlanTable, TestPlanExhaustive) |
| M8 | `internal/delivery/rules.go:91` | `Trusted` accepts an empty author user id | ✅ Killed (TestTrusted) |
| M9 | `internal/delivery/rules.go:123` | LoopGuard `>=` → `>` (pauses at 26) | ✅ Killed (TestLoopGuardPausesAtTwentyFifthAgentMessage, TestLoopGuardResets) |
| M10 | `internal/delivery/rules.go:127` | Chat messages no longer reset the streak | ✅ Killed (TestLoopGuardResets, TestLoopGuardPauseAndResume) |
| M11 | `internal/users/users.go:143` | Login limiter `>=` → `>` (allows a 6th failure) | ✅ Killed (TestLoginRateLimitPerIP, TestLoginRateLimited) |
| M12 | `internal/users/users.go:143` | Limiter window `<` → `<=` | ✅ Killed (TestLoginRateLimitPerIP) |
| M13 | `internal/api/middleware.go:144` | JSON body limit doubled to 2 MiB | ✅ Killed (TestBodyLimit) |
| M14 | `internal/api/middleware.go:146` | Multipart allowance dropped | ✅ Killed (TestBodyLimit) |
| M15 | `internal/live/hub.go:340` | `canViewRoom` always true (member/outbox events leak) | ✅ Killed (TestLiveRoomEventsFollowAccess, TestLiveStatusEventsReachUsersWithAccess) |
| M16 | `internal/live/hub.go:222` | `room.message` frame sent to users without view | ✅ Killed (TestLiveRoomMessageReachesViewersOnly, TestLiveRoomEventsFollowAccess) |
| M17 | `internal/live/hub.go:105` | Hub's own Origin check removed (the websocket library's host-only `OriginPatterns` remains) | ❌ **Survived.** A probe with the same host and an `https` scheme gets 101 under the mutant and 403 on the original, so the check matters but no test covers it. |
| M18 | `internal/delivery/outbox.go:21` | Backoff exponent off by one (starts at 4 s) | ✅ Killed (TestOutboxRetryBackoffThenFailed, TestBackoff) |
| M19 | `internal/delivery/outbox.go:186` | `failed` after 6 attempts instead of 5 | ✅ Killed (TestOutboxRetryBackoffThenFailed) |
| M20 | `internal/delivery/outbox.go:96` | Interrupted `sending` rows not requeued at start | ✅ Killed (TestOutboxResumesAfterRestart) |
| M21 | `internal/api/middleware.go:101` | `X-Frame-Options` dropped | ✅ Killed (TestSecurityHeaders, TestWebUIServedWithStrictHeaders) |
| M22 | `internal/rooms/messages.go:131` | Text limit `>` → `>=` | ✅ Killed (TestPostValidation, TestRoomMessagesEndpoint) |
| M23 | `internal/store/migrations/0001_init.sql:170` | Outbox FK cascade removed (room delete keeps outbox) | ✅ Killed (TestDeleteRoomRemovesEverything) |
| M24 | `internal/users/tokens.go:116` | Disabled user's tokens still authenticate | ✅ Killed (TestDisabledUserTokensFail, TestDisabledUserIsLoggedOut) |

Extra probes (behavior checks against the spec, not mutants; run in the same scratch, then discarded):

| Probe | Expectation from spec | Outcome |
| ----- | --------------------- | ------- |
| P1 ROOM-01: queue a message to chat C, remove C, `OutboxWorker.Flush` | No delivery to C | ❌ C received the delivery |
| P2 AUTH-05: 30 concurrent wrong-password logins from one IP | At most 5 evaluated, the rest 429 | ❌ 30 evaluated, 0 rate-limited |
| P3 LIVE-03: same host, other scheme Origin | 403 | ✅ 403 on HEAD (mutant M17 → 101) |

**Sensor depth**: P0 expanded (24 mutants across every listed area)
**Sensor outcome**: 23/24 killed, 1 survived (M17) - FAIL ❌

---

## Code Quality

| Principle | Status |
| --------- | ------ |
| Minimum code | ✅ |
| Surgical changes | ✅ |
| No scope creep | ✅ |
| Matches patterns | ✅ (one sentinel-error set mapped centrally in `api.mapError`, sqlc rows → DTO converters) |
| Spec-anchored outcome check (asserted values match spec) | ✅ for 40 ACs; gaps listed above |
| Per-layer Coverage Expectation met (domain 1:1 ACs; routes happy+edge+error) | ✅ room routes have the full owner/viewer/poster/stranger/admin matrix (`internal/api/rooms_handlers_test.go:194-254`) |
| Every test maps to a spec requirement - no unclaimed tests | ✅ (test comments cite AC ids; helper tests map to Done-when items) |
| Documented guidelines followed: `AGENTS.md`, `.specs/features/core-platform/contract-notes.md` | ✅ |

---

## Gate Check

- **Gate command**: `make check` (sqlc+tygo drift, golangci-lint, go vet, web lint+typecheck, `go test -race ./...`, web tests, web build, `go build`)
- **Run 1 (machine under heavy parallel load)**: exit 2. sqlc/tygo drift clean, golangci-lint `0 issues`, vet clean, web lint and typecheck clean. One test failed: `--- FAIL: TestStartRunsRetention` at `internal/app/app_test.go:134` (`Eventually` 5 s window, "Condition never satisfied"; the test took 12.25 s). Web tests did not run because of that failure.
- **Run 2**: exit 0. All 15 Go packages `ok`; web `Test Files 24 passed (24)`, `Tests 193 passed (193)`; web build and `go build` OK.
- **Flake check**: the failing test passed 8/8 alone and 24/24 under 6× parallel stress. The cause was not proven. The test waits on a background job with a fixed 5 s `Eventually`, and a failed first `Cleanup` would wait 24 h (`internal/rooms/retention.go:58-74`), so the test depends on load.
- **Go tests**: 305 passed (172 top-level `Test*` functions plus subtests), 0 failed, 0 skipped (`go test -json ./internal/...`)
- **Test count before feature**: 0 (base `bba5e2d` holds only specs)
- **Test count after feature**: 172 Go test functions (305 including subtests) + 193 web tests
- **Skipped tests**: none

---

## Fix Plans

### Fix 1: ROOM-01 - removed chat still gets queued deliveries (Major)
- **Root cause**: `RemoveMember` (`internal/rooms/members.go:166-178`) deletes only the `room_members` row. Pending or backoff outbox rows for that chat in that room remain, and `ListDueOutbox` (`internal/store/queries/outbox.sql:1-6`) sends them.
- **Fix task**: in the same tx as the member delete, mark that chat's `pending` outbox rows for messages of the room as `failed` (or delete them), or join `room_members` in `ListDueOutbox`. Publish `OutboxChanged`. Add a test that queues a message, removes the chat, flushes the worker, and asserts no send happens.

### Fix 2: AUTH-05 - concurrent login burst bypasses the limiter (Major, security)
- **Root cause**: `users/auth.go:95-100` checks the limit, runs the slow argon2 verify, and only then records the failure. Concurrent attempts all pass the check.
- **Fix task**: reserve the attempt atomically before verifying, for example `limiter.Begin(ip)` that counts in-flight attempts plus failures, and release it on success. Add a test with N≫5 parallel wrong logins that asserts at most 5 password checks and the rest `ErrRateLimited`.

### Fix 3: M17 - LIVE-03 scheme mismatch untested (Minor)
- **Fix task**: add `alice.DialLive(<same host, other scheme>)` → 403 in `internal/api/live_test.go` (the probe P3 body).

### Fix 4: Edge case "last manager deleted" (Minor, spec)
- **Fix task**: either add user deletion that deletes rooms with no remaining manager, or move the edge case to Out of Scope until user deletion exists. Also decide whether the owner-cascade FK should transfer the room when other managers remain.

### Fix 5: Flaky `TestStartRunsRetention` (Minor)
- **Fix task**: drive the retention job deterministically (run `Cleanup` synchronously in Start, or expose a hook) or widen the wait. Log `Cleanup` errors to the test logger so failures show their cause.

---

## Security Notes (found in passing; gaps, not fixed)

1. Login rate-limit concurrency bypass (Fix 2). Each attempt also costs an argon2id hash (19 MiB), so a burst is a cheap CPU and memory amplifier.
2. Live WebSockets outlive logout and session revocation. `internal/live/hub.go:99-136` authenticates only at upgrade and re-checks the user's *level* per event, not the session. A disabled user stops receiving events (the level resolves to none), but a logged-out or revoked session keeps streaming until it disconnects.
3. Removed chat members still get messages queued earlier (Fix 1).
4. Manage grantees can change sharing (make a room public, grant `manage` to others). The spec says the owner. This was a deliberate design choice, but it widens who can expose a private room.
5. Room deletion leaves attachment blob files on disk for up to about 25 h (until the orphan sweep).
6. `AgentRoomLevel` (`internal/access/access.go:108-120`) trusts `p.AgentID` without checking that the agent belongs to `p.UserID`. Safe today because nothing sets `AgentID` yet. The agent registry must enforce ownership when it lands.
7. `failureLimiter` prunes only when its map has ≥10k keys and only removes expired windows (`internal/users/users.go:146-166`). With more than 10k IPs active in one window the map grows without bound for that minute. Minor.

---

## Requirement Traceability Update

| Requirement | Previous Status | New Status |
| ----------- | --------------- | ---------- |
| AUTH-01..04, AUTH-06..10, TOK-01..04, ACL-01..06, ACL-08, MSG-01, MSG-02, DLV-01, DLV-03, DLV-04, DLV-05, DLV-07, DLV-09, DLV-10, LIVE-01..04, SEC-01, SEC-02, SEC-03, SEC-05, OPS-01, OPS-02, AUD-02 | Implementing | ✅ Verified |
| AUTH-05, ROOM-01 | Implementing | ❌ Needs Fix |
| ACL-07, ROOM-02, DLV-02, DLV-06, DLV-08, SEC-04, OPS-03, AUD-01 | Implementing | ⚠️ Verified with noted precision/scope gap |

---

## Summary

**Overall**: ❌ Not Ready. Two AC gaps, one surviving mutant, one edge case without evidence, and a load-sensitive gate test.

**Spec-anchored check**: 40/50 ACs matched the spec outcome; 2 gaps; 8 precision/scope gaps
**Sensor**: 23/24 mutations killed (M17 survived); 2 of 3 probes found behavior gaps
**Gate**: run 2 green (305 Go + 193 web tests); run 1 red on flaky `TestStartRunsRetention`

**What works**: the authentication, CSRF, token-restriction and ACL resolution paths, the delivery Plan, trust rule and loop guard, the outbox backoff and restart, live access filtering, headers, body limits, retention and audit. All are discriminating under mutation.

**Next steps**: run Fix 1 and Fix 2 as fix tasks (security and correctness), then Fix 3 to Fix 5, then re-verify (iteration 1 of 3).
