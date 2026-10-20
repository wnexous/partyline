# Agent Channel Validation

**Date**: 2026-10-20
**Spec**: `.specs/features/agent-channel/spec.md`
**Diff range**: feature `14ceffc~1..c497608`, plus fix iteration 1 (T10–T18) `178d7f6..68e43da` (178d7f6, be99c36, 3bb31d3, 410add5, 91cb510, 694295b, 9f1706b, a1d32a3, 68e43da). Commits for other features (core-platform, connectors) in the same range are out of scope.
**Verifier**: independent sub-agent (author ≠ verifier), iteration 2 of the fix→re-verify loop, detached worktree at 68e43da

**Verdict**: FAIL ❌. The gate is green, all 22 ACs have spec-anchored evidence, and all five iteration-1 survivors (M15, M16, M17, M20b, M21) are now killed. But 3 of 14 fresh mutants on the new S1 hardening survived (N1, N3, N4). The worst is N1: the symlink-refusal test passes for the wrong reason, so the `Lstat` symlink check can regress unnoticed. These gaps need test-only fixes.

## History

- **Iteration 1** (c497608): FAIL. `make check` failed twice under load (fixed 5 s waits). 16/21 mutants killed; M15, M16, M17, M20b and M21 survived. Security notes S1–S6. Fix tasks T10–T18 followed.
- **Iteration 2** (68e43da, this report): FAIL. The gate is green, and all iteration-1 survivors are killed. 3 fresh mutants survived in the download-dir hardening, plus 2 minor ones.

---

## Task Completion

| Task | Status | Notes |
| ---- | ------ | ----- |
| T1–T9 | ✅ Done | Verified in iteration 1 |
| T10 Load-tolerant waits | ✅ Done | 178d7f6. `make check` is green under load average ~12–14 on 12 cores |
| T11 Foreign-room ack | ✅ Done | be99c36. Kills M16 |
| T12 Live/backlog overlap | ✅ Done | 3bb31d3. Kills M17 |
| T13 Revocation closes agent sockets | ✅ Done | 410add5. Kills N6, N7, N8 and N14. The pre-upgrade `active` check is untested (N9) |
| T14 Real instructions | ✅ Done | 91cb510. Kills M21 |
| T15 Link backoff | ✅ Done | 694295b. Kills M15 |
| T16 Slugified download dir | ✅ Done | 9f1706b. Kills M20b |
| T17 Private download dir | ⚠️ Partial | a1d32a3. The O_EXCL+rename and parent-check paths are discriminated (N2b, N5). The symlink check (N1), the group-bit check (N3) and the owner check (N4) are not |
| T18 Contract notes | ✅ Done | 68e43da |

---

## Spec-Anchored Acceptance Criteria

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --------- | -------------------- | ----------------------- | ------ |
| MCP-01 server/discover | -32601 | `internal/channel/mcp/server_test.go:241`, golden `server_test.go:166-232`, e2e `internal/cli/channel_test.go:141` - `require.Equal(t, float64(-32601), …["code"])` | ✅ PASS |
| MCP-02 initialize | exact caps, name `partyline`, instructions (data, trusted, tools), version negotiation | `mcp/server_test.go:260-263`; `internal/channel/format_test.go:132-138`; e2e `cli/channel_test.go:148-150` - exact capabilities map, `"partyline"`, `require.Equal(t, channel.Instructions, res["instructions"])` | ✅ PASS (M21 killed) |
| MCP-03 no permission capability | key absent | `mcp/server_test.go:274` `NotContains "claude/channel/permission"`; exact maps `:261`, `cli/channel_test.go:148` | ✅ PASS |
| AGT-01 connect → online | online | `internal/channel/link_test.go:29`; `internal/api/agent_ws_test.go:96` `require.True(t, list.Items[0].Online)`; e2e `cli/channel_test.go:150-163` | ✅ PASS |
| AGT-02 invalid token | 1 stderr line; MCP alive; tools `isError` + exact text | `link_test.go:176` `Count(stderr,"\n")==1`; `:181` ping answered; `tools_test.go:356-357` exact text for all six tools. Revocation while connected: `agent_ws_test.go:302,311,337` - close 1008 within 1 s, other token's socket stays open, lagged re-check closes it | ✅ PASS |
| AGT-03 reconnect backoff + offline | 1 s doubling to 30 s; offline after close | `link_test.go:333-335` - the `run` loop records waits `[1,2,4,8,16,30,30,30]s` and the state is `server unreachable`; `link_test.go:161` helper; `:140-149` reconnect; `agent_ws_test.go:212` `Online:false` | ✅ PASS (M15 killed) |
| AGT-04 replace | close reason `replaced` | `agent_ws_test.go:122-123` - 4000 / `"replaced"`; `:124` newer stays open | ✅ PASS |
| AGT-05 notification shape | content + attachment lines; exact meta keys | `format_test.go:82,97,103`; `link_test.go:33-36` | ✅ PASS |
| AGT-06 identifier keys | `^[A-Za-z0-9_]+$` | `format_test.go:126` `require.Regexp` | ✅ PASS |
| AGT-07 ack after write; cursor | ack only after the stdout write; cursor advances; foreign rooms ignored (contract) | `link_test.go:116-128`; `agent_ws_test.go:182-189` (never backwards); `agent_ws_test.go:251-258` - own room's cursor = `own.Seq`, foreign room `ErrorIs(sql.ErrNoRows)` | ✅ PASS (M16 killed) |
| AGT-08 backlog ≤50, oldest first | ordered, at most 50; no duplicate from the live overlap | `agent_ws_test.go:138-145`; `:268-277` - duplicates skipped, `m3` next, `ExpectNone` | ✅ PASS (M17 killed) |
| AGT-09 >50 pending | one notice with the skipped count; cursor jump | `agent_ws_test.go:163` `"10 older messages skipped; use the history tool"`; `:165` last 50; `:168` cursor = `posted[9].Seq` | ✅ PASS (N10 killed). ⚠️ The spec says "system message"; it ships as an unstored `notice` frame (accepted in the contract notes) |
| AGT-10 dedupe | re-ack without re-emit | `link_test.go:95-104` - one notification, two acks | ✅ PASS. ⚠️ The 1,000-id window (design) is untested (N11 survived) |
| TOOL-01..TOOL-05, TOOL-07..TOOL-09 | as in iteration 1 | Unchanged files and lines: `tools_test.go:90-93,113-123,141-147,164-166,187-195,263-269,285-286,312-328`; `agent_handlers_test.go:99,110-118,129-131,157-174,191-200,235-237` | ✅ PASS (re-checked; these test files did not change before line 209) |
| TOOL-06 download | under the agent temp dir, 0600, absolute; private dir | `tools_test.go:243-250` - `IsAbs`, dir `<TempDir>/partyline/builder`, `0o600`; `:408` slug dir `night-shift`; `tools_unix_test.go:25-29,41-45,66-76` - refusal + nothing written; planted symlink replaced, victim `"untouched"`, regular 0600 | ✅ PASS on the AC. ⚠️ Hardening gaps: N1, N3 and N4 survived |

**Focus re-checks**

- **Golden handshake**: unchanged (`mcp/server_test.go:166-232`; exact-length `matches`). N12 (ping unanswered) was killed by `TestPingAndUnknownMethods` and `TestLinkInvalidToken`.
- **Trust**: `agent_dispatch_test.go:53-64`, unchanged.
- **Identity scoping**: `agent_ws_test.go:229-234`.
- **Revocation scoping**: `agent_ws_test.go:311`. Another token's socket stays open, and N14 (any token revocation closes every socket) was killed.
- **Backlog cap and notice**: `:163-168`.
- **Replace**: `:122-125`.
- **Rate limit**: `agent_handlers_test.go:235-237`, `tools_test.go:285-286`.
- **Private download dir**:
  - Mode 0700 on create: N13 was killed. A 0750 mkdir makes the code refuse its own directory.
  - Owner check: not discriminated (N4).
  - O_EXCL plus rename: N5 was killed.
  - Symlinked parent: N2b was killed. The check is the parent's `Lstat`/mode, not the `Lstat` alone. See N1.

**Status**: ✅ 22/22 ACs covered with spec-anchored assertions. ⚠️ 2 spec-precision notes (AGT-09 notice frame, AGT-10 bounded window).

---

## Edge Cases

- [x] Stdin closes → exit 0 within 2 s: `link_test.go:263-266` (`require.Less(t, took, 2*time.Second)`); `cli/channel_test.go:187`
- [x] Notification held until `notifications/initialized`: `mcp/server_test.go:279-298`; `link_test.go:116-122`
- [x] Empty slug → `agent`: `internal/agents/registry_test.go:133-134`. The channel-side slug is now covered by `tools_test.go:408` (M20b killed)

---

## Discrimination Sensor

- **Scratch**: `git worktree add --detach /tmp/claude-1001/pl-agent-mutants2 68e43da`, plus `…/pl-agent-mutants2b` for re-runs.
- **Procedure**: each mutant was applied by an exact-match replacement (count == 1), run with `-race` on its covering packages, then reverted with `git checkout`.
- **Cleanup**: both scratches were removed (`git worktree remove --force`). The real tree's porcelain was empty before and after.

| # | File:line | Mutation | Killed? |
| - | --------- | -------- | ------- |
| M15 | `internal/channel/link.go:185` | Dial failure always waits `minBackoff` (no doubling) | ✅ Killed (TestLinkBacksOffWhileUnreachable). The first variant did not compile and was replaced |
| M16 | `internal/agents/gateway.go:413` | Ack ignores membership | ✅ Killed (TestAgentAckIgnoresForeignRooms) |
| M17 | `gateway.go:322` | Live dedupe `<=` → `<` | ✅ Killed (TestAgentLivePushSkipsBacklogOverlap) |
| M20b | `internal/channel/run.go:54` | No slug for the agent name | ✅ Killed (TestToolDownloadDirUsesAgentSlug) |
| M21 | `run.go:56` | Empty `Instructions` | ✅ Killed (TestChannelEndToEnd) |
| N1 | `internal/channel/tools.go:280` | `os.Lstat` → `os.Stat` (follow a symlinked `<tmp>/partyline`) | ❌ **Survived** |
| N2b | `tools.go:276-290` | Create the parent but skip its checks | ✅ Killed (RefusesSharedDirectory/symlinked). Plain N2 (drop the parent) was killed trivially by an mkdir error, so it was replaced |
| N3 | `internal/channel/private_unix.go:17` | Permission mask `0o077` → `0o007` (group-accessible dir accepted) | ❌ **Survived** |
| N4 | `private_unix.go:14` | Owner check disabled | ❌ **Survived** |
| N5 | `tools.go:248` | O_EXCL temp + rename → `OpenFile(path, O_TRUNC)` (follows a planted symlink) | ✅ Killed (TestToolDownloadReplacesPlantedSymlink) |
| N6 | `gateway.go:216` | Revocation ignores `TokenIDs` | ✅ Killed (TestAgentSocketClosesOnTokenRevocation) |
| N7 | `gateway.go:220` | No re-check after `Lagged` | ✅ Killed (TestAgentSocketRechecksTokenAfterLaggedBus) |
| N8 | `gateway.go:217` | Revocation close 1008 → 1000 | ✅ Killed |
| N9 | `gateway.go:147` | Pre-upgrade `active` check disabled | ❌ Survived (minor: a race guard behind the auth middleware) |
| N10 | `gateway.go:360` | Skipped count `+1` (S6 exact count) | ✅ Killed (TestAgentWSBacklogCapSkipsOlderMessages) |
| N11 | `link.go:30` | Dedupe window 1,000 → 1 | ❌ Survived (minor, AGT-10 window) |
| N12 | `internal/channel/mcp/server.go:227` | `ping` left unanswered | ✅ Killed (TestPingAndUnknownMethods, TestLinkInvalidToken) |
| N13 | `tools.go:277` | Mkdir 0700 → 0750 | ✅ Killed (TestToolDownloadAttachment, DirUsesAgentSlug) |
| N14 | `gateway.go:216` | Any token revocation closes every agent socket | ✅ Killed (helper must stay open) |

**Why N1 survives**: `TestToolDownloadRefusesSharedDirectory/symlinked partyline dir` (`tools_unix_test.go:17-30`) points the symlink at `t.TempDir()`. Go creates that directory with `os.Mkdir(dir, 0777)`, so under umask 022 it is 0755. With `Stat`, the code follows the link, and the **mode** check then refuses it, not the symlink check. Re-running the mutant under `umask 077` makes the test fail: the file is written through the symlink to `…/002/partyline/builder/<id>-report.txt`. On a real host, a symlink to one of the victim's own 0700 directories would be followed.

**Sensor depth**: expanded (P0: identity, revocation, trust, filesystem safety)
**Result**: 14/19 valid mutants killed. All 5 iteration-1 survivors are killed; 5 of 14 fresh mutants survived (3 of them in the S1 hardening). FAIL ❌

---

## Code Quality

| Principle | Status |
| --------- | ------ |
| Minimum code | ✅ The fixes are small (`gateway.go` +69, `tools.go` +52, `private_unix.go` 21 lines) |
| Surgical changes | ✅ `link.sleep` hook only; the wait constants only in test helpers |
| No scope creep | ✅ Revocation handling follows the core AUTH-10 contract |
| Matches patterns | ✅ Bus subscription mirrors `live.Hub` |
| Spec-anchored outcome check | ✅ Exact codes, texts and sequences |
| Per-layer coverage | ⚠️ The S1 symlink, group-bit and owner branches are not discriminated |
| Every test maps to a requirement | ✅ The test comments name AGT/TOOL/AUTH IDs |
| Documented guidelines | `AGENTS.md`, `tasks.md` coverage matrix, contract notes |

---

## Gate Check

- **Gate command**: `make check` (check-generate, golangci-lint, vet, web-check, `go test -race -count=1 -timeout 20m ./...`, web-test, web-build, go build)
- **Run 1**: EXIT 0 at a load average of ~12.6–13.7 on 12 cores, with other verifiers running concurrently.
  - golangci-lint: 0 issues
  - Go: all 21 packages ok (`internal/api` 415 s, `internal/channel` 180 s)
  - Web: 27 files and 216 tests passed
  - Build: ok
- **Run 2** (`go test -race -count=1 -json ./...`, a confirmation run): EXIT 0. 490 tests and subtests passed, 0 failed, 0 skipped
- **Flaky**: none observed. Run 1 was green, so no re-run was needed to rule out a flake
- **Test count**: Go `func Test` 241 (c497608) → 288 (68e43da), including other features' fixes. Agent-channel adds +8:
  - `TestAgentAckIgnoresForeignRooms`
  - `TestAgentLivePushSkipsBacklogOverlap`
  - `TestAgentSocketClosesOnTokenRevocation`
  - `TestAgentSocketRechecksTokenAfterLaggedBus`
  - `TestLinkBacksOffWhileUnreachable`
  - `TestToolDownloadDirUsesAgentSlug`
  - `TestToolDownloadRefusesSharedDirectory`
  - `TestToolDownloadReplacesPlantedSymlink`

  No test was removed: `TestToolDownloadAttachment` was moved, not deleted.
- **Skipped**: none

---

## Ranked Gaps

1. **N1 survived (TOOL-06 / S1, Major-security)**: the symlink refusal (`tools.go:280-285`) is not discriminated, because the test's symlink target is 0755 by umask. Fix: make the symlink target private (`os.Chmod(elsewhere, 0o700)`) so only the `Lstat` symlink check can refuse it. Assert that `elsewhere` stays empty.
2. **N3 survived (T17 Done-when "group/other-accessible", Minor-security)**: only a 0777 directory is tested. Fix: add a 0750 (group-only) case in `tools_unix_test.go`.
3. **N4 survived (S1 owner check, Minor-security)**: `checkPrivate`'s UID branch (`private_unix.go:14`) has no test. Fix: a unit test that calls `checkPrivate` with a `fs.FileInfo` whose `Sys()` is a `*syscall.Stat_t` with another UID, and asserts the "belongs to another user" error.
4. **N11 survived (AGT-10 window, Minor)**: the 1,000-id dedupe window (design) is untested. Fix: a unit test on `idRing` that remembers 1,000 ids and evicts the oldest at 1,001, or a link test that re-sends the first id after 1,000 others.
5. **N9 survived (Minor)**: the pre-upgrade `PrincipalActive` check (`gateway.go:147`; contract: "inactive → 401") has no test. Fix: a gateway test with a `PrincipalChecker` stub that returns false and asserts 401 with no registration.

### Security weaknesses (state at 68e43da)

| Id | Status | Evidence / note |
| -- | ------ | --------------- |
| S1 shared `/tmp` download dir | **Fixed**, with test gaps | Directories are 0700, owner-checked, and refused when they are symlinks or have group/other bits (`tools.go:271-292`, `private_unix.go:13-20`). Files go to an O_EXCL temp file renamed into place (`tools.go:246-264`). Residuals: N1, N3 and N4 above. Another user can pre-create `/tmp/partyline` to deny downloads (refusal with a hint to set `TMPDIR`); this is documented in `docs/claude-code.md:109` |
| S2 unlimited agents per token | **Open** | There is still no cap in `Registry.Upsert` or `gateway.register` |
| S3 trust laundering via own agents | **Open, by design** (AD-006) | - |
| S4 `send_file` reads any local path | **Open** | Mitigated by Claude Code permission prompts |
| S5 pings blocked before `initialized` | **Open** | `link.handle` → `Notify` still blocks the only WS reader (`link.go:294`, `mcp/server.go:107-112`). The MCP `ping` itself is answered (N12 killed) |
| S6 skipped count off by one under a race | **Open** | Count and list are still separate reads (`gateway.go:347,354`). The exact count is asserted in the quiet case (N10 killed) |
| New: revoked token keeps the agent socket | **Fixed** | `gateway.go:191-234`. Tests at `agent_ws_test.go:282-338` |

---

## Fix Plans

| Fix | Task | Priority |
| --- | ---- | -------- |
| F8 | Make the symlinked-dir test's target 0700 so only the symlink check refuses it (kills N1) | Major (security) |
| F9 | Add a group-only (0750) agent dir case (kills N3) | Minor |
| F10 | Unit test for `checkPrivate` with a foreign UID (kills N4) | Minor |
| F11 | Unit test for the 1,000-id `idRing` eviction (kills N11) | Minor |
| F12 | Gateway test for an inactive principal at upgrade → 401 (kills N9) | Minor |

---

## Requirement Traceability Update

| Requirement | Previous | New |
| ----------- | -------- | --- |
| MCP-02, AGT-02, AGT-03, AGT-07, AGT-08 | Fixed, re-verify | ✅ Verified |
| TOOL-06 | Fixed, re-verify | ⚠️ AC verified; hardening tests need F8–F10 |
| AGT-10 | Verified | ✅ Verified (window note, F11) |
| All others | Verified | ✅ Verified |

The Verifier did not edit spec.md.

---

## Summary

**Overall**: ❌ Not Ready. Only small, test-only fixes remain.

**Spec-anchored check**: 22/22 ACs and 3/3 edge cases have evidence; 2 spec-precision notes
**Sensor**: 14/19 killed (iteration-1 survivors 5/5 killed; fresh mutants N1, N3, N4, N9 and N11 survived)
**Gate**: `make check` EXIT 0 (Go 21/21 packages ok, lint 0 issues, web 216/216, build ok). Go `-json` re-run: 490 passed, 0 failed

**Next steps**: F8 (required), then F9–F12. Re-verify in iteration 3 of 3.
