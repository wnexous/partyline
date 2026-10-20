# Core Platform Validation (iteration 2)

**Date**: 2026-10-20
**Spec**: `.specs/features/core-platform/spec.md` (50 requirements + 8 edge cases, current wording: ACL-07 "a user with `manage`", DLV-04 listen-only 422, owner-cascade and session edge cases)
**Diff range**: fix iteration 1 = `534bf61..9efe452` (T28-T44; the tree also holds the agent-channel and first connectors tasks, which are verified only where they now provide evidence for a core AC)
**Verifier**: independent sub-agent (author ≠ verifier), evidence-or-zero, iteration 2 of 3

## Validation verdict: FAIL ❌

The fix iteration closed almost all of the iteration-1 findings. ROOM-01 now holds for chats and agents. The concurrent login burst runs only 5 password checks. M17 is killed. The last-manager edge case was replaced by an owner-cascade edge case that a test covers. Live sockets now close when their session ends. Blobs are removed when a room is deleted. 37 of 44 mutants were killed.

The verdict is FAIL for two reasons:

1. **AUTH-05 is violated in a new way.** T29 added a rule that a successful login clears the IP's failure count. The spec has no such rule. A probe ran 4 wrong logins and then 1 correct login, four times in a row, from one IP. That produced **16 failed logins within the same minute and 0 × 429** (the spec requires 429 after more than 5 failures). Anyone with a valid account on the instance can therefore guess another account's password without limit from the same IP. `TestLoginSuccessClearsFailures` (`internal/users/auth_test.go:226-242`) asserts this behavior.
2. **7 mutants survived.** The most important one is N3: `TestLoginRateLimitConcurrentBurst` counts the 401 and 429 answers, not the password checks. Moving the reservation to after the argon2 check (the iteration-1 bug pattern) passes every test, even though the burst then runs 30 hashes. The other survivors are test-precision gaps in edge cases and contract notes: the 60-s re-check default, the one-minute blob grace, the 409 for the last admin over HTTP, the limiter's default cap, the session-owner check, and the re-check after a lagged bus.

---

## Iteration 1 summary (2026-10-19, `bba5e2d..534bf61`)

FAIL: 40/50 ACs matched, 23/24 mutants killed. Gaps were: ROOM-01 (a removed chat still got its queued sends), AUTH-05 (a concurrent burst bypassed the limiter), M17 (no test for an Origin with the same host and another scheme), the "last manager deleted" edge case (no such operation), and a load-flaky `TestStartRunsRetention`. Security notes were: live sockets outliving logout, `AgentRoomLevel` trusting `p.AgentID`, an unbounded limiter map, manage grantees changing sharing, and blobs kept about 25 h after a room delete. Fix tasks T28-T44 were created.

| Iteration-1 finding | Fix | Iteration-2 result |
| --- | --- | --- |
| ROOM-01 queued sends after removal | T28 | ✅ Fixed. Pending rows are deleted in the same tx (`internal/rooms/members.go:181`) and `ListDueOutbox` filters on membership (`internal/store/queries/outbox.sql:6`). Tests and N1/N2/N20 kill. A probe confirms removed **agents** get no push and no backlog. |
| AUTH-05 concurrent burst | T29 | ✅ Concurrency fixed: a probe shows 5 password checks in a 30-request burst. ❌ **New gap**: success clears failures (see verdict). ⚠️ N3 survives. |
| M17 scheme mismatch | T30 | ✅ Killed (`internal/api/live_test.go:164-168`) |
| Last-manager edge case | T34 | ✅ Edge case reworded to the owner cascade. Covered by `internal/store/store_test.go:127-168`. |
| Flaky retention test | T32 | ✅ Passed in both gate runs. |
| Live sockets after logout | T31 | ✅ They close within 1 s (`internal/api/live_test.go:217-260`). |
| Blob files kept after room delete | T33 | ✅ Removed at once (`internal/rooms/retention_test.go:100-129`). ⚠️ N9 survives. |
| `AgentRoomLevel` trusts `p.AgentID` | (agent-channel) | ✅ Mitigated. The only setter is `agentOnly` (`internal/api/agent_handlers.go:57-62`), which looks the agent up by the token owner and the agent name. |
| Unbounded limiter map | T29 | ✅ Capped at 10,000 keys. ⚠️ N16 survives (the default cap is not asserted). |

---

## Task Completion

| Task | Status | Notes |
| ---- | ------ | ----- |
| T1-T27 | ✅ Done | Unchanged from iteration 1 |
| T28-T44 | ✅ Done (all `[x]`) | T29 brings the new AUTH-05 deviation (success clears failures). T39's hadolint and image run were not repeated here (no Docker or hadolint on this host). |

---

## Spec-Anchored Acceptance Criteria

Legend: ✅ the assertion targets the spec-defined outcome · ⚠️ spec-precision or scope note · ❌ gap

### P1: First-run setup and accounts

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| AUTH-01 | Setup token logged while zero users; setup only with it | `internal/cli/serve_test.go:160` `findLog(..., msgSetupToken)`, `:199` absent once a user exists; `internal/users/auth_test.go:85-86,89-90` wrong/empty token → `ErrBadSetupToken`, `Zero(userCount)`; `internal/api/auth_handlers_test.go:40` → 401 | ✅ |
| AUTH-02 | Setup after a user exists → 409, nothing created | `internal/users/auth_test.go:111-112` `ErrorIs(ErrSetupDone)`, `EqualValues(1, userCount)`; `internal/api/auth_handlers_test.go:60` `RequireError(409, conflict)` | ✅ |
| AUTH-03 | HttpOnly, SameSite=Lax, 30 days | `internal/api/auth_handlers_test.go:22-24` `True(c.HttpOnly)`, `Equal(SameSiteLaxMode)`, `Equal(thirtyDays, c.MaxAge)`; `internal/users/auth_test.go:150` expiry = now+30 d | ✅ |
| AUTH-04 | 401 with the same body for an unknown user and a wrong password | `internal/api/auth_handlers_test.go:94-96` both 401 and `Equal(unknown.Body, wrong.Body)` | ✅ |
| AUTH-05 | More than 5 failed logins per IP within 1 min → 429 for the rest of the minute | Sequential: `internal/users/auth_test.go:176-190`; HTTP `internal/api/auth_handlers_test.go:108-111` → `429 rate_limited`. Burst: `internal/users/auth_test.go:220-221` (5×401, 25×429); probe with instrumented `checkPassword` = **5 checks**. **Violation:** `Succeed` deletes the IP's window (`internal/users/users.go:184-188`, called at `internal/users/auth.go:111`). Probe: four rounds of 4 wrong + 1 correct → **16 failures in one minute, 0×429**. `TestLoginSuccessClearsFailures` (`internal/users/auth_test.go:226-242`) asserts 9 failures in the minute before a 429. **Precision (carried over):** the 6th attempt is blocked after 5 failures (stricter than "more than 5"). | ❌ GAP |
| AUTH-06 | Single-use link, expires after 7 days | `internal/users/invites_test.go:54` expiry = now+7 d, `:63-66` gone at 7 d; `:98-100` reuse → `ErrInviteGone` | ✅ |
| AUTH-07 | Valid username + password ≥10 → account created, invite used | `internal/users/invites_test.go:80-94` (`InviteStatusUsed`, `UsedBy`); `:142-171` validation; `internal/api/admin_handlers_test.go:82-99` → 422 | ✅ |
| AUTH-08 | Passwords as argon2id only; tokens as SHA-256 only | `internal/authn/authn_test.go:17-18` `$argon2id$v=19$m=19456,t=2,p=1$`; `internal/users/tokens_test.go:49,58-59` `Equal(HashToken(secret), TokenHash)`, len 64 | ✅ |
| AUTH-09 | Logout / revoked session → 401 | `internal/api/auth_handlers_test.go:131` after logout; `:240` revoked session → 401; `internal/users/auth_test.go:277`. Live sockets: `internal/api/live_test.go:227,233` `requireRevoked` (close 1008 within 1 s) | ✅ |
| AUTH-10 | Disabled user's sessions and tokens → 401 | `internal/api/admin_handlers_test.go:132-133` cookie and token → 401; `internal/users/tokens_test.go:172`; live `internal/api/live_test.go:255-257` | ✅ |

### P1: Tokens

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| TOK-01 | Secret once, `pl_agent_`/`pl_user_`, then the 12-char prefix only | `internal/users/tokens_test.go:45-47`; `internal/api/admin_handlers_test.go:207,214,217-218,222` | ✅ |
| TOK-02 | Revoked, expired or unknown → 401 | `internal/users/tokens_test.go:126,135,140,143`; `internal/api/admin_handlers_test.go:236,238` | ✅ |
| TOK-03 | Agent token → 403 outside agent endpoints | `internal/api/middleware_test.go:188-189` `403 forbidden`; `internal/api/admin_handlers_test.go:227-228` | ✅ |
| TOK-04 | `last_used_at` at most once per minute | `internal/users/tokens_test.go:197,203,209` (unchanged at +59 s, updated at +60 s) | ✅ |

### P1: Rooms, members and sharing

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| ACL-01 | Creator is owner with `manage` | `internal/rooms/rooms_test.go:129-130`; `internal/api/rooms_handlers_test.go:185,187` | ✅ |
| ACL-02 | view<post<manage; max of ownership, grant, `*` | `internal/access/access_test.go:154,158` table; `:171-178` order | ✅ |
| ACL-03 | No `view` → 404 on room, messages, members | `internal/api/rooms_handlers_test.go:105-151` matrix (stranger/admin 404 on get, members, messages, attachment); `internal/rooms/rooms_test.go:180-193` | ✅ |
| ACL-04 | `view` without `post` posting → 403 | `internal/api/rooms_handlers_test.go:140-142` viewer 403; `:216`; `internal/rooms/messages_test.go:145` | ✅ |
| ACL-05 | Agent member needs ownership or `use`, else 403 | `internal/rooms/rooms_test.go:326,328,333`; `internal/api/rooms_handlers_test.go:247` | ✅ |
| ACL-06 | Chat member needs ownership of its connector or `use`, else 403 | `internal/rooms/rooms_test.go:360,362`; `internal/api/rooms_handlers_test.go:248` | ✅ |
| ACL-07 | A user with `manage` sets private/shared/public → grants replaced: none / listed users / everyone at view or post | `internal/access/access_test.go:341,352,359,362,366` exact grant rows per preset; manager grantee: `internal/rooms/rooms_test.go:298-301` (bob with a `manage` grant sets public/post); HTTP `internal/api/rooms_handlers_test.go:219-226` | ✅ (spec reworded, deviation closed) |
| ACL-08 | No implicit admin access | `internal/access/access_test.go:191,194,197` `Equal(None, l)` on room, agent, connector; matrix admin column 404; activity feed `internal/api/activity_test.go:57-60` | ✅ |
| ROOM-01 | A removed member gets no future deliveries in that room | Chats: `internal/rooms/messages_test.go:456-467` (no pending rows left, worker sends only to the other chat and room, `Len(calls, 3)`); worker filter `internal/delivery/outbox_test.go:296-297`; `internal/api/rooms_handlers_test.go:282`. Agents: dispatcher reads current members (`internal/delivery/dispatcher.go:98`), backlog joins `room_members` (`internal/store/queries/agents.sql` `ListAgentRoomStates`). Scratch probe: removed agent got no push and no backlog. **No suite test covers agent removal.** | ✅ (⚠️ agent side probe-only) |
| ROOM-02 | Delete removes messages, attachments, cursors, outbox | `internal/rooms/rooms_test.go:455-456` all counts 0 for the room, other room intact; blobs `internal/rooms/retention_test.go:125-128` | ✅ |

### P1: Messages and delivery rules

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| MSG-01 | Next per-room seq from 1, no gaps | `internal/rooms/messages_test.go:96,98,101` (50 concurrent posts); `:121` rejected posts use no seq | ✅ |
| MSG-02 | >16,000 chars, or empty without attachments → 422 | `internal/api/rooms_handlers_test.go:305-308` (422 `invalid`, field `text`; 16,000 accepted); `:360-361` | ✅ |
| DLV-01 | Never back to the author | `internal/delivery/rules_test.go:109` exhaustive `Equal(!isAuthor, agents[m.ID])`; end to end `internal/api/agent_dispatch_test.go:77` `builder.ExpectNone` | ✅ |
| DLV-02 | Every agent member except the author | `internal/delivery/rules_test.go:58,109`; end to end `internal/api/agent_dispatch_test.go:48-63,71-75` (both agents receive by id) | ✅ (scope gap closed by agent-channel) |
| DLV-03 | No recipients → every chat with `outbound` on | `internal/rooms/messages_test.go:183,189` `Equal(sorted(c1,c3), outboxChats)` | ✅ |
| DLV-04 | Explicit recipients → only those; a listed outbound-off chat → 422 `recipients` = `listen-only` | `internal/rooms/messages_test.go:195-196` only c3; `:199-200` `Equal("listen-only", verr.Fields["recipients"])`; unknown chat `:202-203`; HTTP 422 for a removed chat `internal/api/rooms_handlers_test.go:283-284`. **Precision:** the HTTP 422 for `listen-only` relies on the generic validation→422 mapping. No HTTP test posts to a listen-only chat. | ✅ |
| DLV-05 | `bridge_chats` on → chat posts reach other outbound chats; else none | `internal/rooms/messages_test.go:209` no bridge → empty; `:214` bridge → `[c3]` (c2 outbound off excluded) | ✅ |
| DLV-06 | `trusted=true` only when the author resolves to the recipient agent's owner | `internal/delivery/rules_test.go:164`; `internal/rooms/messages_test.go:237-250`; end to end `internal/api/agent_dispatch_test.go:53,56,61,64` (alice→alice's agent true, →bob's false, and the reverse) | ✅ (scope gap closed) |
| DLV-07 | Backoff from 2 s, at most 5 attempts, then `failed` | `internal/delivery/outbox_test.go:209` `Equal(now.Add(wait))` 2/4/8/16 s; `:222-224` failed, 5 attempts, 5 sends; restart `:242-262` | ✅ |
| DLV-08 | 25 consecutive agent messages → pause agent delivery + system message | `internal/delivery/rules_test.go:179-180`; `internal/rooms/messages_test.go:326-335`; agent delivery paused end to end `internal/api/agent_dispatch_test.go:90-104` | ✅ (scope gap closed) |
| DLV-09 | `manage` resume resets and resumes | `internal/rooms/messages_test.go:348,350,358-359`; `internal/api/rooms_handlers_test.go:392-393` | ✅ |
| DLV-10 | User or chat message resets the counter to 0 | `internal/rooms/messages_test.go:306,312`; `internal/delivery/rules_test.go:198` | ✅ |

### P1: Live updates

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| LIVE-01 | `room.message` to every live connection of users with `view` | `internal/api/live_test.go:53-57` payload id/room/text/seq | ✅ |
| LIVE-02 | No event to users without access | `internal/api/live_test.go:59,97,103-104,151-152` `ExpectNone` | ✅ |
| LIVE-03 | Upgrade with Origin ≠ public URL → 403 | `internal/api/live_test.go:161,163,168` (foreign, `null`, same host other scheme) | ✅ |
| LIVE-04 | `agent.presence`, `connector.status`, `outbox.status` to users with access | `internal/api/live_test.go:131,138,145-149` whole payloads | ✅ |

### P1: Secure, observable deployment

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| SEC-01 | CSP, nosniff, `Referrer-Policy: same-origin`, `X-Frame-Options: DENY` on HTML | `internal/api/webui_test.go:24-27` exact values on the SPA route; `internal/webui/webui_test.go:45,49-60` (CSP incl. `font-src 'self' data:`) | ✅ |
| SEC-02 | Cookie write with foreign Origin / Sec-Fetch-Site → 403 | `internal/api/middleware_test.go:143,145,147,149` | ✅ |
| SEC-03 | >1 MiB JSON / > upload multipart → 413 | `internal/api/middleware_test.go:237,241,250`; `internal/api/rooms_handlers_test.go:365-367` | ✅ |
| SEC-04 | Connector secrets sealed with AES-256-GCM; generated key 0600 | `internal/secrets/box_test.go:96-110` mode 0600 + reuse; `internal/connectors/kind_test.go:149-170` sealed bytes hold no clear secret, wrong key fails; `internal/api/connectors_handlers_test.go:186` raw row holds no clear secret | ✅ (scope gap closed by connectors) |
| SEC-05 | Never log message text, passwords, token secrets | `internal/cli/serve_test.go:194` `NotContains(logs, secret)` over a real `serve`; `:277` redaction | ✅ |
| OPS-01 | `/healthz` 200 unauth; `/readyz` 503 until migrated | `internal/app/app_test.go:53,56,59,62` | ✅ |
| OPS-02 | Daily job deletes past retention (default 90, 0 = forever) | `internal/rooms/retention_test.go:77-86,92-94`; default 90 `internal/api/rooms_handlers_test.go:189`; `internal/app/app_test.go:142` | ✅ |
| OPS-03 | Non-root image, all state under `/data` | `Dockerfile:37` distroless `nonroot`, `:43` `USER 65532:65532`, `:42` `PARTYLINE_DATA_DIR=/data`, `:45` `VOLUME /data` | ⚠️ inspection only (no Docker on host) |

### P2: Audit trail

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| AUD-01 | Login, logout, invite, token, grant, user-state, connector changes logged with actor/action/target/time, no secrets | `internal/api/audit_test.go:117-128`; connectors `internal/api/connectors_handlers_test.go:309-319` (actor, target, action per change, no config values) | ✅ (scope gap closed) |
| AUD-02 | Newest first, 50 per page | `internal/api/audit_test.go:141-154` | ✅ |

**Status**: ❌ 1 gap (AUTH-05) · ⚠️ 1 inspection-only (OPS-03) and 2 precision notes (ROOM-01 agent side, DLV-04 HTTP) · ✅ 49/50 match the spec outcome.

Payload/conjunction rule: the new tests assert values (close status 1008, exact outbox counts per chat/room, whole blob existence per hash, `verr.Fields["recipients"] == "listen-only"`), not just calls.

---

## Edge Cases

- [x] Concurrent posts get distinct consecutive seqs (MSG-01): `internal/rooms/messages_test.go:69-101`
- [x] Restart resumes queued sends (DLV-07): `internal/delivery/outbox_test.go:242-262`
- [x] Removing an owner account deletes their rooms (ROOM-02): `internal/store/store_test.go:149-168` (rooms, members, messages, attachments, outbox, cursors; other owner's room kept). There is no user deletion in the API or CLI, consistent with the spec.
- [x] A grant to a disabled user is kept but denies access (AUTH-10): `internal/access/access_test.go:229-250`
- [x] Self or last-admin demote/disable → 409 `conflict`, nothing changes: self over HTTP `internal/api/admin_handlers_test.go:172-174`; last admin `internal/users/invites_test.go:271,273,286,299-303` (also under concurrency). ⚠️ The last-admin 409 over HTTP is untested (N12 survived). It is reachable only through two concurrent demotions.
- [x] A session end closes its live sockets within 1 s, and live sockets are re-checked at least every 60 s: `internal/api/live_test.go:217-260` (logout, session delete, password change, reset, disable; others stay open), periodic `:262-271` with a 100 ms interval. ⚠️ The 60 s default is not asserted (N6 survived).
- [x] A password change ends the other sessions and keeps the current one: `internal/api/live_test.go:239-242` (other socket closed, own socket open; N19 killed)
- [x] Room delete removes unshared blobs, keeps shared ones and those under a minute old: `internal/rooms/retention_test.go:125-128`. ⚠️ The "fresh" blob is age 0, so a grace of 0 also passes (N9 survived).

---

## Discrimination Sensor

Expanded tier. The scratch was a detached worktree: `git worktree add --detach /tmp/claude-1001/pl-mutants2 9efe452`. Each mutant was applied by an exact-string replacement (anchor count checked = 1), the covering tests were run without `-race` (`go test -count=1 [-run …]`), and the file was restored with `git checkout` inside the scratch. Two mutants (M15, N7) first failed to build. They were made valid and re-run, and their kills below come from the valid runs. Probes ran in a second scratch, `/tmp/claude-1001/pl-probes2`. Both worktrees were removed with `git worktree remove --force`. Verify-tree `git status --porcelain` was empty before and after.

### Iteration-1 mutants re-applied (M1-M24)

| # | File | Mutation | Killed? |
| - | ---- | -------- | ------- |
| M1 | `internal/api/middleware.go:247` | CSRF ignores a foreign Origin | ✅ TestCSRFCookieWrites, TestCSRFAnonymousForeignOrigin |
| M2 | `internal/api/middleware.go:224` | Agent restriction widened to `/api/v1/` | ✅ TestAgentTokenRestrictedToAgentRoutes, TestTokenEndpoints |
| M3 | `internal/access/access.go:188` | Admin role gets implicit `manage` | ✅ TestAdminWithoutGrantHasNoAccess |
| M4 | `internal/access/access.go:57` | `maxLevel` keeps the lowest level | ✅ TestRoomLevelIsHighestOfOwnershipAndGrants |
| M5 | `internal/delivery/rules.go` Plan | Author not excluded | ✅ TestPlanTable |
| M6 | `internal/delivery/rules.go` chatReceives | Ignore `outbound` for agent/user posts | ✅ TestPlanTable |
| M7 | `internal/delivery/rules.go` chatReceives | Bridged chat posts ignore `outbound` | ✅ TestPlanTable, TestPlanExhaustive |
| M8 | `internal/delivery/rules.go:91` | `Trusted` accepts an empty author | ✅ TestTrusted |
| M9 | `internal/delivery/rules.go:123` | Loop guard `>=` → `>` | ✅ TestLoopGuardPausesAtTwentyFifthAgentMessage |
| M10 | `internal/delivery/rules.go:127` | Chat posts no longer reset the streak | ✅ TestLoopGuardResets |
| M11 | `internal/users/users.go:170` | Limiter `>=` → `>` | ✅ TestLoginRateLimitPerIP (+ burst) |
| M12 | `internal/users/users.go:169` | Window `<` → `<=` | ✅ TestLoginRateLimitPerIP |
| M13 | `internal/api/middleware.go` bodyLimit | JSON limit doubled | ✅ TestBodyLimit |
| M14 | `internal/api/middleware.go` bodyLimit | Multipart allowance dropped | ✅ TestBodyLimit |
| M15 | `internal/live/hub.go:402-407` | `canViewRoom` always true | ✅ TestLiveRoomEventsFollowAccess, TestLiveStatusEventsReachUsersWithAccess |
| M16 | `internal/live/hub.go:413` | `ifLevel` ignores `None` (adapted from iteration 1) | ✅ TestLiveStatusEventsReachUsersWithAccess |
| M17 | `internal/live/hub.go:126` | Hub Origin check removed | ✅ **now killed** (TestLiveUpgradeChecks) |
| M18 | `internal/delivery/outbox.go:21` | Backoff exponent off by one | ✅ TestOutboxRetryBackoffThenFailed, TestBackoff |
| M19 | `internal/delivery/outbox.go:186` | `failed` after 6 attempts | ✅ TestOutboxRetryBackoffThenFailed |
| M20 | `internal/delivery/outbox.go:96` | No requeue of `sending` at start | ✅ TestOutboxResumesAfterRestart |
| M21 | `internal/api/middleware.go:101` | `X-Frame-Options` dropped | ✅ TestSecurityHeaders, TestWebUIServedWithStrictHeaders |
| M22 | `internal/rooms/messages.go:131` | Text limit `>` → `>=` | ✅ TestPostValidation |
| M23 | `internal/store/migrations/0001_init.sql:170` | Outbox FK cascade removed | ✅ TestDeleteRoomRemovesEverything |
| M24 | `internal/users/tokens.go:116` | Disabled user's tokens authenticate | ✅ TestDisabledUserTokensFail |

### New mutants against the fixes (N1-N20)

| # | File | Mutation | Killed? |
| - | ---- | -------- | ------- |
| N1 | `internal/store/db/outbox.sql.go` (ListDueOutbox) | Membership `EXISTS` filter removed | ✅ TestOutboxSkipsChatsNoLongerMembers |
| N2 | `internal/rooms/members.go:180` | RemoveMember skips deleting pending rows | ✅ TestRemoveMemberCancelsQueuedDeliveries |
| N20 | `internal/store/db/outbox.sql.go` (DeletePendingRoomChatOutbox) | Also deletes `sent` history | ✅ TestRemoveMemberCancelsQueuedDeliveries |
| N3 | `internal/users/auth.go:95-98` | Reserve **after** the argon2 check (30 checks in a burst) | ❌ **Survived.** The burst test counts answers, not checks |
| N15 | `internal/users/users.go:184-188` | Success no longer clears the IP | ✅ TestLoginSuccessClearsFailures (that test asserts the spec-violating behavior, see AUTH-05) |
| N16 | `internal/users/users.go:160` | Default key cap 10,000 → 2^30 | ❌ **Survived.** The test overrides `maxKeys`, so the default is not asserted |
| N4 | `internal/users/auth.go:258` | Logout publishes no `SessionsRevoked` | ✅ TestLiveClosesRevokedSessions |
| N5 | `internal/live/hub.go:241` | Hub ignores `SessionsRevoked` | ✅ TestLiveClosesRevokedSessions |
| N6 | `internal/live/hub.go:38` | Default re-check 60 s → 10 min | ❌ **Survived** (edge case "at least every 60 seconds" not asserted) |
| N14 | `internal/users/auth.go:286` | `PrincipalActive` ignores token revocation | ✅ TestPrincipalActive |
| N17 | `internal/users/auth.go:275` | `PrincipalActive` drops the session-owner check | ❌ **Survived.** The "session of another user" case uses a nonexistent user id, so `activeUser` rejects it anyway |
| N18 | `internal/live/hub.go:244-245` | No principal re-check after a lagged bus | ❌ **Survived** (contract-notes behavior, untested) |
| N19 | `internal/users/profile.go:104` | Password change ends **all** sessions | ✅ TestLiveClosesRevokedSessions |
| N7 | `internal/rooms/retention.go:89` | Room delete removes shared blobs too | ✅ TestDeleteRoomRemovesUnsharedBlobs |
| N8 | `internal/rooms/rooms.go:276` | No blob removal after delete | ✅ TestDeleteRoomRemovesUnsharedBlobs |
| N9 | `internal/rooms/retention.go:18` | Delete grace 1 min → 0 | ❌ **Survived.** The test's fresh blob has age 0, so the one-minute boundary is not exercised |
| N10 | `internal/rooms/messages.go:217-218` | Listen-only check removed | ✅ TestOutboxFollowsDeliveryPlan |
| N11 | `internal/users/invites.go` | Last-admin `n <= 1` → `n < 1` | ✅ TestLastActiveAdminIsKept |
| N12 | `internal/api/router.go:160` | `ErrLastAdmin` no longer mapped to 409 | ❌ **Survived** (no HTTP test reaches it) |
| N13 | `internal/users/invites.go:252` | Self-change guard disabled | ✅ TestAdminCannotChangeSelf |

### Probes (behavior against the spec; scratch only)

| Probe | Expectation from spec | Outcome |
| ----- | --------------------- | ------- |
| P1 AUTH-05 burst, `checkPassword` instrumented | At most 5 evaluated | ✅ 5 checks, 5×401, 25×429 |
| P2 AUTH-05, 4 rounds of (4 wrong + 1 correct) from one IP in one minute | 429 once more than 5 failures | ❌ 16 failures, 0×429 |
| P3 ROOM-01 agent: connected agent removed, then post, then reconnect | No push, no backlog | ✅ nothing received in either case |

**Sensor depth**: P0 expanded (44 mutants)
**Sensor outcome**: 37/44 killed, 7 survived (N3, N6, N9, N12, N16, N17, N18) - FAIL ❌

---

## Code Quality

| Principle | Status |
| --------- | ------ |
| Minimum code | ✅ |
| Surgical changes | ✅ fix commits stay in their files. T37/T38 (member count, activity feed) are feature additions requested alongside the fixes. |
| No scope creep | ⚠️ T29's "success clears failures" adds behavior the spec does not allow (AUTH-05) |
| Matches patterns | ✅ (sentinel errors mapped in `api.mapError`, post-commit bus events, sqlc queries) |
| Spec-anchored outcome check | ✅ 49/50; AUTH-05 gap |
| Per-layer coverage | ✅ services + e2e for every fix; live socket closes tested end to end |
| Every test maps to a requirement | ✅ (test comments cite AC ids or edge cases) |
| Documented guidelines: `AGENTS.md`, `contract-notes.md` | ✅ |

---

## Gate Check

- **Gate command**: `make check` (sqlc+tygo drift, golangci-lint, go vet, web lint+typecheck, `go test -race ./...`, web tests, web build, `go build`)
- **Run 1** (host load ~11-13, mutant run in parallel): exit 2. Drift clean, golangci-lint `0 issues`, vet clean. Go: every core package `ok` (`internal/api` 356.7 s, under the 10 min default). Two **agent-channel** tests failed on their 10 s `Eventually` (`internal/channel/env_test.go:24`): `TestLinkDeliversBacklogOnConnect` (`internal/channel/link_test.go:48`) and `TestChannelEndToEnd` (`internal/cli/channel_test.go:116`). Web tests did not run. Re-run of both packages alone: `ok` (channel 110 s, cli 7.9 s). These are outside core-platform. Infra note: the same short-deadline pattern as iteration-1 L-005, now in the agent-channel tests.
- **Run 2** (after the sensor; 3 m 31 s): **exit 0**. Drift, lint (`0 issues`), vet, web lint and typecheck clean; all 20 Go packages with tests `ok` (`internal/api` 186.5 s); web `Test Files 27 passed (27)`, `Tests 216 passed (216)`; web build and `go build` OK.
- **Go test functions**: 272 (was 172 at `534bf61`); none removed (diff of `func Test` names is empty)
- **Skipped tests**: none

---

## Fix Plans

### Fix 1: AUTH-05 - success must not reset the failure count (Major, security)
- **Root cause**: `failureLimiter.Succeed` deletes the IP's window (`internal/users/users.go:184-188`). An attacker with any valid account on the same IP resets the counter every 4 guesses.
- **Fix task**: on success, return only the success's own reservation (like `Release`) instead of deleting the window. A window then counts failures and in-flight attempts only. Replace `TestLoginSuccessClearsFailures` with a test where 4 failures, 1 success and 2 more failures within the minute end in `ErrRateLimited` (more than 5 failures). Keep the burst test.

### Fix 2: N3 - burst test must count password checks (Major, test)
- **Fix task**: count `checkPassword`/`VerifyPassword` calls (for example a test hook or counter on the service) and assert `== 5` in `TestLoginRateLimitConcurrentBurst`. This kills the reserve-after-verify mutant.

### Fix 3: Edge-case precision tests (Minor)
- N6: assert `live.DefaultSessionCheckInterval <= time.Minute` (or that the app wires 60 s when `LiveSessionCheck` is 0).
- N9: in `TestDeleteRoomRemovesUnsharedBlobs`, write the fresh blob at `t0.Add(-30*time.Second)` (inside the minute) and keep one at `-61 s` that is removed.
- N12: an HTTP test where two admins demote each other concurrently and the loser gets `409 conflict`, or a `mapError` unit test for `ErrLastAdmin`.

### Fix 4: Contract-note behaviors without a test (Minor)
- N16: assert the default limiter cap (`newFailureLimiter(...).maxKeys == 10000`).
- N17: in `TestPrincipalActive`, use another **existing active** user's id with bob's session id.
- N18: publish `events.Lagged` after deleting the session in the DB and expect a 1008 close well before the periodic interval.
- ROOM-01 (agent side): add a suite test for the probe P3 (removed agent: no push, no backlog).

---

## Security Notes (remaining; not fixed)

1. **AUTH-05 success reset** (Fix 1): unlimited password guessing for anyone holding a valid account behind the same IP (shared NAT, the same container network, or a malicious member).
2. The per-IP limiter (10,000 keys, oldest evicted) can be flushed by an attacker who controls more than 10,000 addresses (IPv6 /64). The attacker can then evict their own window. Per-IP limiting is weak against IPv6 in general; consider also limiting per username or per /64.
3. The live session check fails open: a DB error in `PrincipalActive` is logged and the socket stays open (`internal/live/hub.go:251-266`). This is acceptable as best effort, but worth knowing.
4. Live sockets authenticated by a **token** close only on the periodic re-check (≤60 s) when the token is revoked or the user is disabled. `SessionsRevoked` names sessions only. This is within the spec (token revocation is not a "session end"), but it leaves a window of up to one minute.
5. Manage grantees can change sharing (now in the spec, ACL-07). This is a deliberate widening of who can make a private room public.
6. `access.AgentRoomLevel` still trusts `p.AgentID`. It is safe because the only setter resolves the agent by the token owner (`internal/api/agent_handlers.go:57-62`). Keep that invariant.
7. `delivery.Plan` still returns a listed chat whose outbound is off. Only `checkRefs` (`internal/rooms/messages.go:217`) stops it. Any future caller that builds a Plan without `checkRefs` would send to a listen-only chat.

---

## Requirement Traceability Update

| Requirement | Previous Status | New Status |
| ----------- | --------------- | ---------- |
| ROOM-01, AUTH-09, AUTH-10, ACL-07, ROOM-02, DLV-04, DLV-07, LIVE-03, SEC-01, OPS-02 | Fixed, re-verify | ✅ Verified |
| DLV-02, DLV-06, DLV-08, SEC-04, AUD-01 | Verified with scope gap | ✅ Verified (agent-channel / connectors evidence) |
| OPS-03 | Fixed, re-verify | ⚠️ Verified by inspection (no Docker locally) |
| AUTH-05 | Fixed, re-verify | ❌ Needs Fix (success reset; N3) |
| all others | Verified (iteration 1) | ✅ Verified (re-cited above) |

---

## Summary

**Overall**: ❌ Not Ready. There is one AC gap (AUTH-05, introduced by the fix) and 7 surviving mutants, 1 of them major (N3).

**Spec-anchored check**: 49/50 ACs matched the spec outcome; 1 gap; OPS-03 inspection only
**Sensor**: 37/44 killed; probes: burst ✅, success reset ❌, agent removal ✅
**Gate**: run 2 green (all Go packages ok, 216 web tests); run 1 red only on two load-sensitive agent-channel tests, which passed when re-run

**What works**: ROOM-01 for chats and agents, the burst limiter (5 checks), session-bound live sockets, blob cleanup, listen-only recipients, the self and last-admin guards (including under concurrency), the owner cascade, the CSP font fix, and end-to-end agent dispatch with trust and pause.

**Next steps**: Fix 1 and Fix 2 (auth), then Fixes 3-4, then re-verify (iteration 3 of 3; escalate to the user if gaps remain).
