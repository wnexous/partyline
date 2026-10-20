# Core Platform Validation (iteration 3, final + escalation closure)

**Date**: 2026-10-20
**Spec**: `.specs/features/core-platform/spec.md` (50 requirements + 12 edge cases, current wording incl. the fix-iteration-2 edge cases: token-revocation close, fail-closed re-check, success keeps failures, /64 keying and full-limiter rule)
**Diff range**: fix iteration 2 = `9e5e513..68e43da` (T45-T58, plus agent-channel fix commits `178d7f6..68e43da` that the core evidence now relies on for agent sockets)
**Verifier**: independent sub-agent (author ≠ verifier), evidence-or-zero, iteration 3 of 3

## Validation verdict: PASS ✅ (closure: sensor 6/6 at `bdca043`; gate green at `f4147c9`)

> **Gate re-run (orchestrator, 2026-10-20)**: `make check` at `f4147c9` (main = `bdca043` merged with main + the comment-only lint fix) exited 0: generate drift clean, golangci-lint 0 issues, vet clean, all Go packages pass under -race, web 216/216, build OK. This is the condition the closure verifier set for PASS.

### Closure verdict before the gate re-run (kept for history): FAIL ❌ (gate red on lint only)

> **Closure verdict (2026-10-20, `68e43da..bdca043`)**: F6 and F18 are killed and 3 fresh mutants (plus one extra) on the same paths are killed, so the sensor is clean. The verdict stays FAIL only because `make check` exits 2 at `bdca043`: golangci-lint reports 5 revive issues, 2 of them introduced by `bdca043` itself (a misplaced doc comment in `internal/live/hub.go:101-105`) and 3 inherited from `main` via merge `38c1625` (`internal/connectors/safehttp`). Both are already fixed on `main` (`f4147c9` and main's safehttp rename). With those comment/rename-only fixes applied in a scratch, lint reports `0 issues`, and every other gate step is green at `bdca043`. Merge/rebase onto `main` (or cherry-pick `f4147c9` + the safehttp fix), re-run `make check`, and this becomes PASS with no further verifier work. See "Escalation closure (post iteration 3)".

### Iteration-3 verdict (kept for history): FAIL ❌ (minor; escalate to the user)

Every iteration-2 finding is closed. **All 50 ACs match the spec outcome.** The AUTH-05 success-reset probe now gets 429: four rounds of 4 wrong + 1 right from one IP produce 5 failures, 14×429 and 1 success. All 7 iteration-2 survivors (N3, N6, N9, N12, N16, N17, N18) are killed, and the gate is green.

The verdict stays FAIL only because the skill's rule says a surviving mutant blocks "done". **2 of 40 valid mutants survived.** Both are minor test-precision gaps on edge cases, and no behavior defect was found:

1. **F6.** The limiter prunes expired windows only once per window instead of whenever it is full. The tests still pass. The full-limiter edge case says that a new network gets 429 only "until windows expire", but with this mutant it can be refused for up to one more minute while expired windows sit in the map.
2. **F18.** The app wiring can override the 60 s re-check default without failing a test. Only the hub default is asserted (`internal/live/hub_test.go:16`). Production passes 0 today, so the hub default applies.

This is the last loop iteration, so these two go to the user to accept or to schedule as one small test task (Fix 1 below).


## Escalation closure (post iteration 3)

**Range**: `68e43da..bdca043` (merge of `main` `38c1625`, then `dbf2ad9` test(users), `bdca043` test(app)). **Verifier**: fresh independent sub-agent, evidence-or-zero.

### Gate (`make check` at `bdca043`, run twice)

- **Run 1 and run 2: exit 2**, identical: `check-generate` clean (sqlc + tygo, no drift), then `golangci-lint run ./...` → `5 issues: revive: 5`:
  - `internal/live/hub.go:101` `exported: comment on exported method Hub.SessionCheckInterval should be of the form "SessionCheckInterval ..."` and `internal/live/hub.go:105` `exported method Hub.Close should have comment`. Cause: `bdca043` inserted the new method between `Close`'s doc comment and `Close`. Comment placement only; fixed on `main` by `f4147c9 style(live): keep the close comment on its method` (the two comment lines swapped).
  - `internal/connectors/safehttp/safehttp.go:123,125` (`redefines-builtin-id: max`) and `internal/connectors/safehttp/safehttp_test.go:51` (`unused-parameter: w`). Inherited from `main` (`102d202`) through merge `38c1625`; not core-platform code. Fixed on `main` (rename `max` → `limit`, `w` → `_`).
- **Scratch lint check** (`/tmp/claude-1001/pl-lint4`, removed): `bdca043` + `f4147c9`'s hub.go swap → `3 issues` (safehttp only); + main's safehttp diff → `0 issues`.
- **Remaining gate steps run individually at `bdca043`** (real verify worktree, no edits): `go vet ./...` exit 0; `make web-check` exit 0; `make test` exit 0: 27 Go packages `ok` (`internal/api` 334 s, `internal/users` 89 s, `internal/app` 45 s, `internal/live` 12 s), web `Test Files 27 passed (27)`, `Tests 216 passed (216)`; `make build` exit 0.
- **Gate note**: per the coordinator, the hub.go lint failure is a comment-placement-only issue already fixed on `main`; the gate is red at `bdca043` and green once that commit (and main's safehttp fix) is on the branch.

### New tests: spec-anchored and discriminating

| Test | Spec anchor | Assertion | Verdict |
| --- | --- | --- | --- |
| `internal/users/auth_test.go:308` `TestFullLimiterAcceptsNewKeysOnceAWindowExpires` | `spec.md:199` AUTH-05 edge case: when full, 429 to untracked addresses "until windows expire", never drop an active window | `require.True(t, l.Begin("e", at(75*time.Second)), ...)` 14 s after the last prune (b's window expired at 70 s); `require.NotContains(t, l.byKey, "b")`; `require.Contains(t, l.byKey, "c", "an active window is never evicted")` | ✅ Asserts the spec outcome (accept once a window expired, keep active ones). Lands between two once-per-window prunes, so it discriminates F6. |
| `internal/app/app_test.go:73` `TestLiveSessionCheckDefaultsToOneMinute` | `spec.md:195` edge case: re-check every live socket's session at least every 60 s | `require.Equal(t, time.Minute, a.Live.SessionCheckInterval())` on a default `app.New` (zero `LiveSessionCheck`) | ✅ Asserts the production wiring at the spec bound. The accessor (`internal/live/hub.go:103`) returns `h.d.SessionCheckInterval`, the same field the re-check ticker uses (`internal/live/hub.go:205`), so it reflects real behavior. Ticker use is covered separately by `internal/api/live_test.go:264` (M3 below). |

The accessor `Hub.SessionCheckInterval()` is a minimal read-only addition used only by the test; acceptable. Its only defect is the doc-comment placement that breaks lint (above).

### Discrimination sensor (scratch `/tmp/claude-1001/pl-mutants4` at `bdca043`, removed)

Scratch baseline (unmutated `./internal/live/ ./internal/app/ ./internal/api/`): all `ok`.

| Mutation | File:line | Description | Killed? | Killing test |
| --- | --- | --- | --- | --- |
| F6 (re-applied) | `internal/users/users.go:213` | Prune only once per window: dropped `\|\| len(l.byKey) >= l.maxKeys` | ✅ Killed | `TestFullLimiterAcceptsNewKeysOnceAWindowExpires` |
| F18 (re-applied) | `internal/app/app.go:187` | App wires 5 min when `LiveSessionCheck` is 0 (test overrides still honored) | ✅ Killed | `TestLiveSessionCheckDefaultsToOneMinute` |
| M1 (fresh) | `internal/users/users.go:221` | Cap off-by-one: `len(l.byKey) < l.maxKeys` → `<=` | ✅ Killed | `TestFailureLimiterIsBounded` |
| M2 (fresh) | `internal/users/users.go:216` | Evict active windows when full: `\|\| len(l.byKey) >= l.maxKeys` added to the expiry check | ✅ Killed | `TestFailureLimiterIsBounded` |
| M3 (fresh) | `internal/live/hub.go:205` | Re-check ticker 30× the configured interval | ✅ Killed | `internal/api` `TestLiveRechecksSessionPeriodically` |
| M4 (extra) | `internal/live/hub.go:37` | `DefaultSessionCheckInterval` 1 min → 2 min | ✅ Killed | `TestDefaultSessionCheckIsOneMinute` (live), `TestLiveSessionCheckDefaultsToOneMinute` (app) |

**Result**: 6/6 killed. Isolation: verify-worktree `git status --porcelain` identical before and after (only ` M .specs/features/core-platform/validation.md`, which predates this run); both scratch worktrees removed and pruned.

**Load note**: during the M2 run, which overlapped the real-tree `make test`, three unrelated `internal/api` tests failed once (`TestMultipartUploadAndDownload`, `TestResumeEndpoint`, `TestWebUIServedWithStrictHeaders`). They passed in the scratch baseline, the M3/M4 runs and both gate test runs; M2's kill is attributed to the deterministic `internal/users` test only. Not reproduced, so not a flake finding.

### Test integrity since `68e43da`

`git diff 68e43da bdca043 --stat`: 72 files, +8821/−87, nearly all the connectors feature merged from `main`. Test function names: 288 → 402; `comm` over `^func Test` names finds **no removed test**. Removed assertion lines in `*_test.go`: **0**. The only removed test-file lines are map-literal realignments in `internal/config/config_test.go` (same keys and values, plus `PARTYLINE_ALLOW_PRIVATE_NETWORKS`). `internal/testkit/testkit.go` now passes `DisableOutbox: !o.outbox`, which keeps the pre-merge default (no outbox worker in testkit apps), so no core test lost coverage.

### Closure outcome

- F6 and F18: **closed** (killed by spec-anchored tests).
- Gap 1 (Minor, blocks the gate): `bdca043` breaks lint at `internal/live/hub.go:101-105`; fix = `f4147c9`. Inherited lint at `internal/connectors/safehttp/safehttp.go:123,125` and `safehttp_test.go:51`; fix = main's rename. Bring `main` into the branch and re-run `make check`.
- Carried optional items (DLV-04 HTTP 422 test, OPS-03 Docker check) unchanged.

---

## History

| Iteration | Range | Verdict | Main findings |
| --- | --- | --- | --- |
| 1 (2026-10-19) | `bba5e2d..534bf61` | FAIL. 40/50 ACs, 23/24 mutants killed | ROOM-01 queued sends after removal; AUTH-05 concurrent burst; M17 scheme; last-manager edge case; flaky retention test. Fixed by T28-T44. |
| 2 (2026-10-20) | `534bf61..9efe452` | FAIL. 49/50 ACs, 37/44 killed | AUTH-05 success reset (16 failures, 0×429); survivors N3, N6, N9, N12, N16, N17, N18; ROOM-01 agent side was probe-only. Fixed by T45-T58. |
| 3 | `9e5e513..68e43da` | FAIL (minor). 50/50 ACs, 38/40 killed | Survivors F6, F18 (edge-case test precision) |
| Closure (2026-10-20) | `68e43da..bdca043` | FAIL (gate: lint only). Sensor 6/6 killed | F6, F18 killed; `bdca043` misplaced the `Close` doc comment (fixed on main `f4147c9`); safehttp lint inherited from main (fixed on main) |

| Iteration-2 finding | Fix | Iteration-3 result |
| --- | --- | --- |
| AUTH-05 success reset | T46 | ✅ `Login` calls `Release` on success (`internal/users/auth.go:113`). `TestLoginSuccessKeepsFailures` checks the 6th failure → 429 (`internal/users/auth_test.go:253`) and the right password → 429 after the limit (`:256`). The HTTP probe: 5×401, 14×429, 1×200. F7 (success clears) and F8 (success counted as failure) are killed. |
| N3 reserve after check | T47 | ✅ Killed. `checks.Load() == 5` (`internal/users/auth_test.go:226`) |
| N16 default cap | T48 | ✅ Killed (`internal/users/auth_test.go:312`) |
| N17 session-owner check | T49 | ✅ Killed, along with N17b (token owner) (`internal/users/tokens_test.go:239-240`) |
| N6 60 s default | T50 | ✅ Killed, along with N6b (`internal/live/hub_test.go:16`). ⚠️ App-level override F18 survives. |
| N18 lagged re-check | T50 | ✅ Killed (`internal/api/live_test.go:278-299`, closes 1008 within 10 s while the periodic interval is 60 s) |
| Live check fail-open | T51 | ✅ Closes with 1013 (`internal/live/hub_check_test.go:67`). F10 and F11 are killed. |
| Token sockets close only on the periodic re-check | T52 | ✅ Close within 1 s (`internal/api/live_test.go:319,326`); agent sockets too (`internal/api/agent_ws_test.go:310,314`). F12-F17 are killed. |
| N9 blob grace | T53 | ✅ Killed, along with N9b (2 min) (`internal/rooms/retention_test.go:130-131`: 59 s kept, 61 s removed) |
| N12 last-admin 409 over HTTP | T54 | ✅ Killed (`internal/api/admin_guard_test.go:58,65`) |
| ROOM-01 agent side, probe only | T55 | ✅ Suite test `internal/api/agent_dispatch_test.go:109-131` |
| Plan sends to listen-only recipients | T56 | ✅ `internal/delivery/rules.go:80`. F19 is killed by `TestPlanTable` and `TestPlanExhaustive`. |
| Unbounded or evicting limiter | T48 | ✅ Active windows are never evicted. F4 (evict oldest) and F5 (no cap) are killed. ⚠️ F6 survives. |

---

## Task Completion

| Task | Status | Notes |
| ---- | ------ | ----- |
| T1-T44 | ✅ Done | Unchanged |
| T45-T58 | ✅ Done (all `[x]`) | T57's `-count=10` claim was not re-run here; both target tests passed in the gate |

---

## Spec-Anchored Acceptance Criteria

Legend: ✅ the assertion targets the spec-defined outcome · ⚠️ precision or scope note · ❌ gap

### P1: First-run setup and accounts

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| AUTH-01 | Setup token logged while zero users; setup only with it | `internal/cli/serve_test.go:160` `findLog(..., msgSetupToken)`; `internal/users/auth_test.go:85-86,89-90` wrong/empty token → `ErrBadSetupToken`, zero users; `internal/api/auth_handlers_test.go:40` → 401 | ✅ |
| AUTH-02 | Setup after a user exists → 409, nothing created | `internal/users/auth_test.go:111-112` `ErrorIs(ErrSetupDone)`, 1 user; `internal/api/auth_handlers_test.go:60` `RequireError(409, conflict)` | ✅ |
| AUTH-03 | HttpOnly, SameSite=Lax, 30 days | `internal/api/auth_handlers_test.go:22-24`; `internal/users/auth_test.go:150` | ✅ |
| AUTH-04 | 401 with the same body for both failure kinds | `internal/api/auth_handlers_test.go:94-96` `Equal(unknown.Body, wrong.Body)` | ✅ |
| AUTH-05 | More than 5 failures per IP in 1 min → 429 for the rest of the minute | Sequential `internal/users/auth_test.go:169-196` (429 until the minute ends, then open); HTTP `internal/api/auth_handlers_test.go:106-111`; burst `:226-228` (5 checks, 5×401, 25×429); success keeps failures `:234-275` (`:253` 6th → `ErrRateLimited`, `:256` right password → 429, `:268-272` window boundary); /64 and IPv4 keying `:317-338`; cap `:278-305`, `:312`. HTTP probe: 4×(4 wrong + 1 right) gives 5×401, 14×429. **Precision (carried):** the 6th attempt is refused after 5 failures, which is stricter by one than "more than 5". | ✅ |
| AUTH-06 | Single use, 7 days | `internal/users/invites_test.go:54,63-66,98-100` | ✅ |
| AUTH-07 | Valid username + password ≥10 → account, invite used | `internal/users/invites_test.go:80-94,142-171`; `internal/api/admin_handlers_test.go:82-99` | ✅ |
| AUTH-08 | argon2id and SHA-256 only | `internal/authn/authn_test.go:17-18`; `internal/users/tokens_test.go:49,58-59` | ✅ |
| AUTH-09 | Logout or revoked session → 401 | `internal/api/auth_handlers_test.go:131,240`; `internal/users/auth_test.go:351-352`; live `internal/api/live_test.go:219-260` (close 1008 within 1 s, `:211-213`) | ✅ |
| AUTH-10 | Disabled user's sessions and tokens → 401 | `internal/api/admin_handlers_test.go:132-133`; `internal/users/tokens_test.go:172`; live sockets `internal/api/live_test.go:326-327` | ✅ |

### P1: Tokens

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| TOK-01 | Secret once, `pl_agent_`/`pl_user_`, then the 12-char prefix | `internal/users/tokens_test.go:45-47`; `internal/api/admin_handlers_test.go:207,214,217-218,222` | ✅ |
| TOK-02 | Revoked, expired or unknown → 401 | `internal/users/tokens_test.go:126,135,140,143`; `internal/api/admin_handlers_test.go:236,238`; revoked token's live socket closes `internal/api/live_test.go:319` | ✅ |
| TOK-03 | Agent token → 403 outside agent endpoints | `internal/api/middleware_test.go:188-189`; `internal/api/admin_handlers_test.go:227-228` | ✅ |
| TOK-04 | `last_used_at` at most once per minute | `internal/users/tokens_test.go:197,203,209` | ✅ |

### P1: Rooms, members and sharing

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| ACL-01 | Creator is owner with `manage` | `internal/rooms/rooms_test.go:129-130`; `internal/api/rooms_handlers_test.go:185,187` | ✅ |
| ACL-02 | view<post<manage; max of ownership, grant, `*` | `internal/access/access_test.go:154,158,171-178` | ✅ |
| ACL-03 | No `view` → 404 on room, messages, members | `internal/api/rooms_handlers_test.go:105-151`; `internal/rooms/rooms_test.go:180-193` | ✅ |
| ACL-04 | `view` without `post` → 403 | `internal/api/rooms_handlers_test.go:140-142,216`; `internal/rooms/messages_test.go:145` | ✅ |
| ACL-05 | Agent member needs ownership or `use`, else 403 | `internal/rooms/rooms_test.go:326,328,333`; `internal/api/rooms_handlers_test.go:247` | ✅ |
| ACL-06 | Chat member needs ownership or `use` of its connector, else 403 | `internal/rooms/rooms_test.go:360,362`; `internal/api/rooms_handlers_test.go:248` | ✅ |
| ACL-07 | `manage` sets private/shared/public → exact grant sets | `internal/access/access_test.go:341,352,359,362,366`; `internal/rooms/rooms_test.go:298-301`; `internal/api/rooms_handlers_test.go:219-226` | ✅ |
| ACL-08 | No implicit admin access | `internal/access/access_test.go:191,194,197` `Equal(None, l)`; `internal/api/activity_test.go:57-60` | ✅ |
| ROOM-01 | A removed member gets no future deliveries | Chats `internal/rooms/messages_test.go:456-467`, `internal/delivery/outbox_test.go:296-297`; agents `internal/api/agent_dispatch_test.go:121-130` (the remaining agent gets the post; the removed one gets `ExpectNone` live and on its reconnect backlog) | ✅ (agent side now in the suite) |
| ROOM-02 | Delete removes messages, attachments, cursors, outbox | `internal/rooms/rooms_test.go:455-456` all counts 0; blobs `internal/rooms/retention_test.go:127-131` | ✅ |

### P1: Messages and delivery rules

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| MSG-01 | Next per-room seq from 1, no gaps | `internal/rooms/messages_test.go:96,98,101,121` | ✅ |
| MSG-02 | >16,000 chars or empty without attachments → 422 | `internal/api/rooms_handlers_test.go:304-308`; `internal/rooms/messages_test.go:112-120` | ✅ |
| DLV-01 | Never back to the author | `internal/delivery/rules_test.go:112` exhaustive; `internal/api/agent_dispatch_test.go:77` | ✅ |
| DLV-02 | Every agent member except the author | `internal/delivery/rules_test.go:61,112`; `internal/api/agent_dispatch_test.go:48-63` | ✅ |
| DLV-03 | No recipients → every outbound chat | `internal/rooms/messages_test.go:183,189` | ✅ |
| DLV-04 | Only listed chats; a listed outbound-off chat → 422 `recipients`=`listen-only` | `internal/rooms/messages_test.go:195-203` (`Equal("listen-only", verr.Fields["recipients"])`); Plan drops listen-only `internal/delivery/rules_test.go:40-42` and exhaustive `:122-123`; HTTP 422 for a removed chat `internal/api/rooms_handlers_test.go:283-284`. **Precision (carried):** no HTTP test posts to a listen-only chat. The 422 relies on the generic validation→422 mapping. | ✅ ⚠️ |
| DLV-05 | `bridge_chats` on → other outbound chats; else none | `internal/rooms/messages_test.go:209,214` | ✅ |
| DLV-06 | `trusted=true` only for the recipient agent's owner | `internal/delivery/rules_test.go:167`; `internal/rooms/messages_test.go:237-250`; `internal/api/agent_dispatch_test.go:53,56,61,64` | ✅ |
| DLV-07 | Backoff from 2 s, 5 attempts, then `failed` | `internal/delivery/outbox_test.go:209,222-224`; restart `:242-262` | ✅ |
| DLV-08 | 25 consecutive agent messages → pause + system message | `internal/delivery/rules_test.go:182-183`; `internal/rooms/messages_test.go:326-335`; `internal/api/agent_dispatch_test.go:90-104` | ✅ |
| DLV-09 | `manage` resume resets and resumes | `internal/rooms/messages_test.go:348,350,358-359`; `internal/api/rooms_handlers_test.go:392-393` | ✅ |
| DLV-10 | User or chat message resets the counter | `internal/rooms/messages_test.go:306,312`; `internal/delivery/rules_test.go:201` | ✅ |

### P1: Live updates

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| LIVE-01 | `room.message` to every live connection with `view` | `internal/api/live_test.go:53-59` (id, room, text, seq on 3 connections) | ✅ |
| LIVE-02 | No event without access | `internal/api/live_test.go:61,153-154` `ExpectNone` | ✅ |
| LIVE-03 | Origin ≠ public URL → 403 | `internal/api/live_test.go:163,165,170` (foreign, `null`, other scheme) | ✅ |
| LIVE-04 | Presence, connector and outbox status to users with access | `internal/api/live_test.go:132,139,145-150` whole payloads | ✅ |

### P1: Secure, observable deployment

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| SEC-01 | CSP, nosniff, `same-origin`, `DENY` on HTML | `internal/api/webui_test.go:24-27`; `internal/webui/webui_test.go:45,49-60` | ✅ |
| SEC-02 | Foreign Origin or Sec-Fetch-Site on a cookie write → 403 | `internal/api/middleware_test.go:143,145,147,149` | ✅ |
| SEC-03 | >1 MiB JSON or > upload limit → 413 | `internal/api/middleware_test.go:237,241,250`; `internal/api/rooms_handlers_test.go:365-367` | ✅ |
| SEC-04 | AES-256-GCM, generated key 0600 | `internal/secrets/box_test.go:96-110`; `internal/connectors/kind_test.go:149-170`; `internal/api/connectors_handlers_test.go:186` | ✅ |
| SEC-05 | No message text, passwords or token secrets in logs | `internal/cli/serve_test.go:194,277` | ✅ |
| OPS-01 | `/healthz` 200 unauth; `/readyz` 503 until migrated | `internal/app/app_test.go:53,56,59,62` | ✅ |
| OPS-02 | Retention deletes past N days (default 90, 0 = forever) | `internal/rooms/retention_test.go:77-86,92-94`; `internal/api/rooms_handlers_test.go:189`; `internal/app/app_test.go:142` | ✅ |
| OPS-03 | Non-root image, state under `/data` | `Dockerfile:37,42,43,45` | ⚠️ inspection only (no Docker on host) |

### P2: Audit trail

| AC | Spec-defined outcome | `file:line` + assertion | Result |
| -- | -------------------- | ----------------------- | ------ |
| AUD-01 | Security events with actor/action/target/time, no secrets | `internal/api/audit_test.go:117-128`; `internal/api/connectors_handlers_test.go:309-319` | ✅ |
| AUD-02 | Newest first, 50 per page | `internal/api/audit_test.go:141-154` | ✅ |

**Status**: ✅ 50/50 ACs match the spec outcome. ⚠️ 1 AC is inspection-only (OPS-03), and 2 carried precision notes remain (AUTH-05 off-by-one strictness, DLV-04 HTTP listen-only).

Payload/conjunction rule: the new tests assert values. They check close codes 1008 and 1013, the exact count of password checks, the exact limiter key membership, the role and active state after a 409, and the per-agent message id.

---

## Edge Cases

- [x] Concurrent posts → distinct consecutive seqs: `internal/rooms/messages_test.go:69-101`
- [x] Restart resumes queued sends: `internal/delivery/outbox_test.go:242-262`
- [x] Owner removal deletes their rooms (no API or CLI user deletion): `internal/store/store_test.go:149-168`
- [x] A grant to a disabled user is kept and denies access: `internal/access/access_test.go:229-250`
- [x] Self or last-admin demote/disable → 409, nothing changes: self `internal/api/admin_handlers_test.go:172-174`; last admin over HTTP `internal/api/admin_guard_test.go:27-68`; service, including under concurrency, `internal/users/invites_test.go:271-303`
- [x] Session end closes its live sockets within 1 s; re-check at least every 60 s: `internal/api/live_test.go:219-260`, periodic `:264-275`, default `internal/live/hub_test.go:16-17`, lagged `internal/api/live_test.go:278-299`. ⚠️ An app-level override of the default is not caught (F18).
- [x] Token revoked or user disabled → token sockets close within 1 s: `internal/api/live_test.go:301-329`; agent sockets `internal/api/agent_ws_test.go:282-315`
- [x] Re-check cannot run → socket closed: `internal/live/hub_check_test.go:33-69` (1013)
- [x] A successful login keeps the minute's failures: `internal/users/auth_test.go:234-275`
- [x] IPv4 address or IPv6 /64 counts as one IP; a full limiter refuses untracked networks and never drops an active window: `internal/users/auth_test.go:278-305,317-338`. ⚠️ The "until windows expire" side is tested only at a time when the once-per-window prune runs anyway, so F6 survives.
- [x] A password change ends the other sessions and keeps the current one: `internal/api/live_test.go:239-245`
- [x] Room delete removes unshared blobs right away and leaves blobs under a minute old: `internal/rooms/retention_test.go:100-132` (59 s kept, 61 s removed)

---

## Discrimination Sensor

P0 expanded tier. The scratch was `git worktree add --detach /tmp/claude-1001/pl-mutants3 68e43da`. Each mutant was an exact-string replacement with its anchor count checked to be 1. The covering tests ran with `go test -count=1 [-run …]`, and the file was restored with `git checkout` in the scratch. Three first attempts were invalid (F17 and M15 did not build, and M22 targeted the wrong package). They were made valid and re-run as F17v, M15v and M22v, and only the valid runs are counted. The probes ran in the same scratch and were deleted before the mutants. The scratch was removed with `git worktree remove --force`. Verify-tree `git status --porcelain` was empty before and after. Nothing was stashed.

### Iteration-2 survivors re-applied

| # | File | Mutation | Killed? |
| - | ---- | -------- | ------- |
| N3 | `internal/users/auth.go:95-99` | Reserve after the argon2 check | ✅ TestLoginRateLimitConcurrentBurst |
| N6 | `internal/live/hub.go:37` | Default re-check 60 s → 10 min | ✅ TestDefaultSessionCheckIsOneMinute |
| N6b | `internal/live/hub.go:95` | Hub applies 10× the default | ✅ TestDefaultSessionCheckIsOneMinute |
| N9 | `internal/rooms/retention.go:18` | Delete grace 1 min → 0 | ✅ TestDeleteRoomRemovesUnsharedBlobs |
| N9b | `internal/rooms/retention.go:18` | Delete grace 1 min → 2 min | ✅ TestDeleteRoomRemovesUnsharedBlobs |
| N12 | `internal/api/router.go:160` | `ErrLastAdmin` unmapped | ✅ TestLastAdminConflictOverHTTP |
| N16 | `internal/users/users.go:172` | Cap 10,000 → 2^30 | ✅ TestLoginLimiterDefaults |
| N17 | `internal/users/auth.go:277` | Session-owner check dropped | ✅ TestPrincipalActive |
| N17b | `internal/users/auth.go:288` | Token-owner check dropped | ✅ TestPrincipalActive |
| N18 | `internal/live/hub.go:251-252` | No re-check after a lagged bus | ✅ TestLiveRechecksSessionAfterLaggedBus |
| N10 | `internal/rooms/messages.go:217-218` | Listen-only 422 removed | ✅ TestOutboxFollowsDeliveryPlan |

### Fresh mutants against the fixes

| # | File | Mutation | Killed? |
| - | ---- | -------- | ------- |
| F1 | `internal/users/users.go:236` | IPv6 keyed per address (no /64) | ✅ TestLoginRateLimitPerNetwork |
| F2 | `internal/users/users.go:236` | IPv6 keyed per /48 | ✅ TestLoginRateLimitPerNetwork |
| F3 | `internal/users/users.go:232` | IPv4-mapped addresses not unmapped | ✅ TestLoginRateLimitPerNetwork |
| F4 | `internal/users/users.go:221` | Full limiter evicts the oldest active window again | ✅ TestFailureLimiterIsBounded |
| F5 | `internal/users/users.go:192` | Full limiter accepts new keys (no cap) | ✅ TestFailureLimiterIsBounded |
| F6 | `internal/users/users.go:213` | Prune only once per window, not when full | ❌ **Survived** |
| F7 | `internal/users/auth.go:113` | Success deletes the window (iteration-2 bug) | ✅ TestLoginSuccessKeepsFailures |
| F8 | `internal/users/auth.go:113` | Success not released (counted as a failure) | ✅ TestLoginSuccessKeepsFailures |
| F9 | `internal/users/users.go:186` | Limit `>=` → `>` | ✅ 4 limiter tests |
| F10 | `internal/live/hub.go:269` | Check error → stay open (fail open) | ✅ TestLiveClosesSocketWhenSessionCheckFails |
| F11 | `internal/live/hub.go:157` | 1013 → 1008 on check failure | ✅ TestLiveClosesSocketWhenSessionCheckFails |
| F12 | `internal/events/bus.go:79-80` | `Revokes` ignores token ids | ✅ TestLiveClosesRevokedTokens |
| F13 | `internal/events/bus.go:75` | Any user's disable closes every socket | ✅ TestLiveClosesRevokedSessions, TestLiveClosesRevokedTokens |
| F14 | `internal/events/bus.go:75` | `UserDisabled` ignored | ✅ TestLiveClosesRevokedTokens |
| F15 | `internal/users/tokens.go:99` | `RevokeToken` publishes nothing | ✅ TestLiveClosesRevokedTokens, TestAgentSocketClosesOnTokenRevocation |
| F16 | `internal/users/invites.go:290` | Disable publishes sessions only | ✅ TestLiveClosesRevokedTokens, TestAgentSocketClosesOnTokenRevocation |
| F17v | `internal/agents/gateway.go:216` | Agent gateway ignores revocations | ✅ TestAgentSocketClosesOnTokenRevocation |
| F18 | `internal/app/app.go:151` | App wires 10 min when `LiveSessionCheck` is 0 | ❌ **Survived** |
| F19 | `internal/delivery/rules.go:80` | Plan sends to listed listen-only chats (T56 reverted) | ✅ TestPlanTable, TestPlanExhaustive |
| F20 | `internal/attachments/store.go:129` | `DeleteIfOlder` ignores the cutoff | ✅ TestDeleteRoomRemovesUnsharedBlobs |
| F21 | `internal/rooms/retention.go:89` | Blob referenced once elsewhere is deleted | ✅ TestDeleteRoomRemovesUnsharedBlobs |

### Sample of iteration-1 mutants

| # | File | Mutation | Killed? |
| - | ---- | -------- | ------- |
| M1 | `internal/api/middleware.go:247` | CSRF ignores a foreign Origin | ✅ TestCSRFCookieWrites, TestCSRFAnonymousForeignOrigin |
| M3 | `internal/access/access.go:188` | Everyone gets `manage` | ✅ 4 access tests |
| M5 | `internal/delivery/rules.go:59` | Author not excluded | ✅ TestPlanTable, TestPlanExhaustive |
| M9 | `internal/delivery/rules.go:123` | Loop guard `>=` → `>` | ✅ 3 loop-guard tests |
| M15v | `internal/live/hub.go:411-415` | `canViewRoom` always true | ✅ TestLiveRoomEventsFollowAccess, TestLiveStatusEventsReachUsersWithAccess |
| M19 | `internal/delivery/outbox.go:186` | `failed` after 6 attempts | ✅ TestOutboxRetryBackoffThenFailed |
| M22v | `internal/rooms/messages.go:131` | Text limit `>` → `>=` | ✅ TestPostValidation |
| M24 | `internal/users/tokens.go:122-123` | A disabled user's token authenticates | ✅ TestDisabledUserTokensFail, TestDisabledUserIsLoggedOut |

### Probes (scratch only)

| Probe | Expectation from spec | Outcome |
| ----- | --------------------- | ------- |
| P1 AUTH-05 over HTTP: 4 rounds of (4 wrong + 1 right), one IP, one minute | 429 once more than 5 failures | ✅ 5×401, 14×429, 1×200 |
| P2 limiter with cap 3: three networks log in successfully, then a fourth network logs in with the right password | (security note) | The fourth gets 429 `too many failed logins`. Windows opened by a success alone (count 0) hold cap slots for their minute. After 61 s it succeeds. |

**Sensor depth**: P0 expanded (40 valid mutants plus 2 probes)
**Sensor outcome**: 38/40 killed, 2 survived (F6, F18): FAIL ❌ (minor)

---

## Code Quality

| Principle | Status |
| --------- | ------ |
| Minimum code | ✅ |
| Surgical changes | ✅ Each fix stays in its files. `verifyPassword` is a field injected only for the burst test, which is a small and justified seam. |
| No scope creep | ✅ The success-reset rule is gone. The /64 keying and the full-limiter rule are now spec edge cases. |
| Matches patterns | ✅ Bus events published after commit, sentinel errors, injected clock |
| Spec-anchored outcome check | ✅ 50/50 |
| Per-layer coverage | ✅ Unit (limiter, Plan, hub default), integration (users, rooms), e2e (live, agent sockets, admin 409) |
| Every test maps to a requirement | ✅ The test comments cite AC ids or edge cases |
| Documented guidelines: `AGENTS.md`, `contract-notes.md` | ✅ The contract notes match the code (`Release` on success, no `Succeed`, 1013 fail-closed, `Revokes`) |

---

## Gate Check

- **Gate command**: `make check` (sqlc+tygo drift, golangci-lint, go vet, web lint+typecheck, `go test -race -count=1 -timeout 20m ./...`, web tests, web build, `go build`). Before it, `web/` needed `bun install --frozen-lockfile`.
- **Run 1** (load 10-15, sensor running in parallel; 459 s): **exit 0**. Drift clean, golangci-lint `0 issues`, vet clean. All 21 Go packages with tests are `ok` (`internal/api` 406 s, `internal/channel` 172 s). Web: `Test Files 27 passed (27)`, `Tests 216 passed (216)`. Web build and `go build` OK.
- **Run 2** (after the sensor; 336 s): **exit 0**. Same results: drift, lint, vet and web checks clean; 21/21 Go packages `ok` (`internal/api` 310 s); 216/216 web tests; builds OK.
- **Go test functions**: 288 (272 at `9efe452`, +16). The only removed name is `TestLoginSuccessClearsFailures`, which asserted the spec-violating behavior and was replaced by `TestLoginSuccessKeepsFailures`.
- **Skipped tests**: none
- **Flakes**: none observed in this iteration

---

## Fix Plans (for the user's decision; the loop bound is reached)

### Fix 1: Edge-case test precision (Minor, test only)
- **F6**: in `TestFailureLimiterIsBounded`, make the "until windows expire" case land between two once-per-window prunes. For example, run one prune at 61 s with a new key, then at 75 s (after b's window, opened at 10 s, has expired) expect a new key to be accepted. With the mutant it is refused.
- **F18**: assert the production wiring, for example that `app.New` with a zero `LiveSessionCheck` leaves the hub at 60 s. One way is an exported read-only accessor, or a test in `internal/app` that checks the hub's interval. The other way is to accept this as low risk: production passes 0, and the hub default is asserted.

### Carried, optional
- DLV-04: one HTTP test that posts to a listen-only chat and expects 422 `recipients`=`listen-only`.
- OPS-03: run the image check (`docker run`, `id -u` ≠ 0, state in `/data`) on a host with Docker.

---

## Security Notes (remaining; not fixed)

1. **Full-limiter lockout (DoS, accepted by the spec).** The limiter refuses new networks while 10,000 active windows exist. Someone with 10,000 IPv4 addresses or /64s can fill it every minute, and one IPv6 /48 already holds 65,536 /64s. Every login from a network not yet tracked then gets 429. Windows opened only by a successful login (count 0) also hold slots (probe P2). The 429 message says "too many failed logins" even to clients that never failed. Mitigations to consider: do not keep count-0 windows, add a per-username limiter, or reserve capacity for previously successful networks.
2. **Agent sockets have no periodic re-check** (agent-channel scope, by inspection). `internal/agents/gateway.go:188-234` closes a socket on a revocation event, on a disable event or after a lagged bus. It never re-checks on a timer, so an agent socket whose token reaches its **expiry** stays open until it disconnects. `/live` sockets do re-check every 60 s (`internal/live/hub.go:202`).
3. Under a database outage, the fail-closed re-check closes every live socket with 1013 once per interval. Clients then reconnect, and the upgrade also fails while the store is down. This is the intended behavior, but it is a reconnect storm.
4. A user with a `manage` grant can change a room's sharing (ACL-07, by spec). That widens who can make a private room public.
5. `access.AgentRoomLevel` trusts `p.AgentID`. This is safe while its only setter, `agentOnly` (`internal/api/agent_handlers.go:57-62`), resolves the agent from the token owner.
6. A success's `Release` decrements whichever window holds the key. If the window expired and was reopened between `Begin` and `Release`, the success refunds one attempt in the new window. The effect is negligible: at most one extra guess.

Closed since iteration 2: the success reset, the fail-open check, token sockets that lived up to 60 s after revocation, `Plan` targeting listen-only chats, and active-window eviction.

---

## Requirement Traceability Update

| Requirement | Previous Status | New Status |
| ----------- | --------------- | ---------- |
| AUTH-05, AUTH-09, AUTH-10, ROOM-01, ROOM-02, DLV-04 | Fixed, re-verify | ✅ Verified (iteration 3) |
| OPS-03 | Verified by inspection | ⚠️ Verified by inspection |
| all others | Verified | ✅ Verified (re-cited above) |

---

## Summary

**Closure (post iteration 3)**: ❌ FAIL on the gate only. Sensor 6/6 killed (F6, F18 closed). `make check` exits 2 at `bdca043` on 5 revive lint issues, all comment/naming only and all already fixed on `main` (`f4147c9` + safehttp rename); with them applied, lint is `0 issues` and every other gate step is green. Next step: merge `main` into the branch, re-run `make check`, record PASS.

### Iteration 3 summary (kept)

**Overall**: ❌ Not Ready by the strict sensor rule. In substance the feature is ready. All 50 ACs hold, with no behavior defect and no AC gap. The 2 surviving mutants are minor test-precision gaps on edge cases.

**Spec-anchored check**: 50/50 ACs matched. OPS-03 is inspection only; 2 carried precision notes.
**Sensor**: 38/40 killed (F6 and F18 survived). Probes: AUTH-05 success reset ✅. The limiter-cap probe gave a security note.
**Gate**: runs 1 and 2 both green (21/21 Go packages, 216/216 web tests), with no flakes.

**Next steps**: the 3-iteration loop is exhausted, so this goes to the user. Either accept F6 and F18 as known minor gaps, or run Fix 1 (one small test-only task) and validate again.
