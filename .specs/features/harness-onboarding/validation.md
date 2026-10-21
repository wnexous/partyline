# Harness Onboarding Validation

## Validation: harness-onboarding (iteration 2) - FAIL ❌

**Date**: 2026-10-21
**Spec**: `.specs/features/harness-onboarding/spec.md` (amended in fix iteration 1: DEV-01, DEV-05/06, ONB-08..11, HAR-02/03/05, PUSH-03/05, WIZ-01/02)
**Diff range**: `65315a0..59ec52d` (HEAD = `v0.2.1`). Fix commits T23-T34: c2a9b70, 0502632, 6873b2e, 39be9e0, 90e8f50, f3b01b7, 832472c, ea77399, bd9df00, c2a524e, d5c2577, 6c157dd. Not in scope: routing-context merge commits and 59ec52d (other features' specs).
**Verifier**: independent sub-agent (author ≠ verifier), detached worktree `partyline-verify-ho` at 59ec52d
**Verdict**: FAIL ❌. Both gates pass. Every iteration-1 gap is fixed and 48 of 49 mutants are killed. Two minor gaps remain: one surviving mutant (ONB-08 in setup) and a way around the ONB-10 http refusal.

---

## Iteration history

| Iteration | Range | Result | Main gaps |
| --- | --- | --- | --- |
| 1 (2026-10-20) | `2ce415c^..65e4778` | FAIL: `make e2e` 4 passed, 2 failed (stale UI-14); 39/39 mutants killed | stale e2e UI-14; omp async reorder; `.partyline.bak` overwritten; symlinked configs replaced; missing listen/CSRF/TOML tests; HAR-02 and PUSH-05 spec precision; 6 security weaknesses |
| 2 (this report) | `65315a0..59ec52d` | FAIL (minor): gates green, 48/49 mutants killed | K03 survives (setup reuses a saved token for another URL, untested); ONB-10 http refusal bypassable |

Iteration-1 gaps, status now: 1 e2e ✅ fixed (`web/e2e/smoke.spec.ts:134`), 2 omp order ✅ (`integrations/omp/partyline.test.ts:186`), 3 backup ✅ (`internal/harness/files_test.go:234`), 4 symlink ✅ (`files_test.go:249`), 5 missing evidence ✅ (`internal/cli/listen_test.go:204`, `internal/api/devices_handlers_test.go:334`, `internal/harness/clis_test.go:356,375`), precision HAR-02/PUSH-05 ✅ (reworded in ea77399).

---

## Task Completion

| Task | Status | Notes |
| ---- | ------ | ----- |
| T1-T22 | ✅ Done | Verified in iteration 1 |
| T23-T34 | ✅ Done | All `[x]` in tasks.md. Each has a commit, except that T34 is the docs record (6c157dd) |

---

## Gate Check

- **`make check`** (generate, lint, vet, web lint/typecheck, `go test -race ./...`, vitest, `make integrations-test`, web build, go build): **EXIT 0**. 31/31 Go packages ok, web 307/307 (30 files), integrations 53/53.
- **`make e2e`** (`PLAYWRIGHT_CHROMIUM_EXECUTABLE=~/.cache/ms-playwright/chromium_headless_shell-1248/…`): **EXIT 0, 10/10 passed** (UI-01/02, UI-04, UI-14/WIZ-01, WIZ-02/DEV-06, UI-24 on desktop-1280 and mobile-360). Passed on the first run, so no re-run was needed.
- **Test delta (fix commits)**: +47 Go `func Test` and web/bun `it(`/`test(` cases, −9. The 9 removals were all rewritten for the new behavior: the devices page tests became the typed-code tests in `web/src/routes/_app/devices.test.tsx`, the a11y test was renamed "…before and after the code", and `TestClaudeLauncher*` became `internal/cli/launcher_test.go:131,172`.
- Porcelain of the verify tree was empty before and after the gates and the sensor.

---

## Spec-Anchored Acceptance Criteria (ACs new or changed in fix iteration 1; the others are unchanged from iteration 1 and still pass)

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| ONB-08 saved token only to saved URL | error naming both URLs when flag/env URL differs (scheme, host, port) and no token | `internal/cliconfig/config_test.go:177` `require.ErrorAs(err, &mismatch)` for env URL, flag URL, other port, other scheme; `internal/cli/channel_config_test.go:60` channel exits with both URLs | ✅ for Resolve; ⚠️ **setup** reuse only guarded in code (`internal/cli/setup.go:167`), mutant K03 survives |
| ONB-08 setup saves start URL, `--token` warning, `--token-stdin` exclusive | start URL saved; warning text; exit 2 for combos | `internal/cli/setup_test.go:422` `require.Equal(start, got.URL)`; `:396` `Contains("warning: --token is visible…")`, `NotContains(secret)`, 3 combos → `Equal(2, code)` | ✅ |
| ONB-09 uninstall revokes first, reports; failed revoke does not fail | `DELETE /api/v1/agent/token`; "Revoked …" / "Could not revoke …"; exit 0 | `internal/cli/setup_harness_test.go:159` `Contains("Revoked the agent token on "+URL)` + token now 401; `:204` failed revoke reported, file gone, exit 0; server `internal/api/agent_register_test.go:59,81` (self only, user token 403, anon 401); declined DEV-06 token revoked `setup_test.go:330` `NotNil(RevokedAt)` | ✅ |
| ONB-10 main wrapper; refuse http except loopback | cut script does nothing; exit≠0 before download | `scripts/install_test.go:325` every cut line → no files, no requests; `:349` 5 http cases refused, loopback 3 allowed | ✅ as tested; ❌ **conformance probe**: `Http://evil`, `hTTp://…` and `http://localhost:80@evil.com/` pass `require_https` (`scripts/install.sh:47-52`), and curl connects to the non-loopback host |
| ONB-11 Docker version from git | describe → dev-<12> (shallow/no tag or SOURCE_COMMIT) → dev; VERSION wins | `scripts/version_test.go:66,76,89,105,115` exact strings (`"v1.2.3-1-g"+short`, `"dev-"+head[:12]`, `"dev-0123456789ab"`, `"dev"`) | ✅ (`VERSION=dev` counts as unset, `version.sh:13`. The script documents this; the spec does not) |
| DEV-01 `<instance>/devices` + code `ABCD-EFGH`, no URL with code | `approve_url == URL+"/devices"`; printed code regex with hyphen | `internal/api/devices_handlers_test.go:56` `Equal(URL+"/devices", ApproveURL)`; `internal/cli/setup_test.go:246` regex `…\n\n  ([A-HJ-NP-Z2-9]{4})-([A-HJ-NP-Z2-9]{4})\n`, `NotContains("/devices/")` | ✅ |
| DEV-02 token named after host, delivered once, with approver | `"<host> (via setup)"`, `approved_by` | `devices_handlers_test.go:86` `Equal("alice", p.ApprovedBy)`, second poll has no token; `setup_test.go:246` `"devbox-7 (via setup)"` | ✅ |
| DEV-05 code in body; 5 unknown codes/min/user → 429 | old path routes not 200; 6th → 429, other user ok, +61 s ok | `devices_handlers_test.go:253` `NotEqual(200, GET/POST …/{code}…)`; `:268` exact 404×5 then 429×3, bob 200, 59 s 429, 61 s 200; `:292` burst `{404:5, 429:7}` | ✅ |
| DEV-06 `Approved by @<user> on <instance>`, confirm unless `--yes`; decline/EOF → nothing saved, exit≠0 | exact line and prompt | `setup_test.go:246` `Contains("\nApproved by @alice on "+URL+"\nSave this agent token of @alice on this machine? [Y/n]: ")`; `:330` declined / EOF → exit 1, no file, revoked; `--yes` → saved, no prompt | ✅ |
| HAR-02 (reworded) other bytes kept in setup-edited files; CLI-managed files change only the entry | exact bytes / exact argv | `internal/harness/files_test.go:91,128`; `clis_test.go:97,171,293` exact argv | ✅ (precision gap closed) |
| HAR-03 launchers: argv, env with fresh `PARTYLINE_SESSION`, aliases, 127 + text, unregistered warning, `--help`, doctor | exact argv/env/stderr per harness | `internal/cli/launcher_test.go:131` exact argv for 8 commands, exact env (inherited id replaced, fresh per launch); `:172` 127 + exact text ×7; `:196` exact warning ×6 + still execs; `:220` two launches in one folder → `proj`/`proj-2` end to end; `:268` help block; `internal/cli/doctor_test.go:90` `"     start a session: partyline omp\n"` | ✅ |
| HAR-05 keep first backup, write through symlink, extension before config + rollback | `.bak` = original bytes; link kept and target edited; config untouched or extension restored/deleted | `files_test.go:234` `Equal(c.orig, …bak)` after 2nd Apply; `:249` ×3 harnesses link kept, target = withEntry, uninstall restores; `:280` config unwritable → ext restored (earlier) / missing (fresh); `:309` ext unwritable → config and .bak untouched; `:326` emptied symlink kept | ✅ |
| PUSH-03 one at a time, rejection stays at head | attempts `[a]` while in flight; A then B after 2 rejections | `integrations/omp/partyline.test.ts:186` `toEqual([a.text])`, then `[a,a,a,b]`, `sent` A then B, one notice | ✅ |
| PUSH-05 (reworded) `promptAsync`, fallback `prompt` | as worded | `integrations/opencode/partyline.test.ts:71,109` | ✅ (precision gap closed) |
| WIZ-01 launchers in wizard + setup end line | `partyline <id>` ×6, delivery line, no token in Advanced; `Start a session with partyline a, b or c.` | `web/e2e/smoke.spec.ts:134` 6 tabs, exact start command + delivery, no `token\|PARTYLINE_\|pl_agent\|https?:`, token list unchanged; `web/src/lib/agent-connect.test.ts:26`; `internal/cli/setup_harness_test.go:86` exact suffix | ✅ |
| WIZ-02 type code first; old `/devices/<code>` → empty `/devices` | nothing shown before lookup; machine, address, age after | `web/src/routes/_app/devices.test.tsx:48` no region/buttons and no API call; `:59` body `{code}`, Address `203.0.113.24`, Requested `3 min. ago`; `:210` redirect, empty field, no lookup; e2e `smoke.spec.ts:183` | ✅ |

**Status**: 35/35 ACs have spec-anchored evidence. The HAR-02 and PUSH-05 precision gaps are closed. ❌ ONB-10 does not hold for mixed-case schemes or loopback userinfo. ⚠️ ONB-08 (setup path) has no test.

---

## Edge Cases

- [x] Config not 0600: channel, listen and doctor refuse it (`internal/cli/channel_config_test.go:85`, `listen_test.go:204`, `doctor_test.go:96`).
- [x] The git root wins over the working directory (`internal/channel/name_test.go`, unchanged).
- [x] Invalid JSON/TOML is not touched (`internal/harness/files_test.go:161`, `clis_test.go:326,356`; the real Codex CLI with a temp `CODEX_HOME`: `clis_test.go:375`).
- [x] Device routes refuse cross-origin requests (`devices_handlers_test.go:334`).
- [ ] Setup with a saved token for instance A and `--url B` without a token: the code asks for or approves a new token (`setup.go:167`), but no test covers it (K03).

---

## Discrimination Sensor

Scratch: `git worktree add --detach /tmp/claude-1001/pl-ho-mutants2 HEAD` (`web/node_modules` symlinked from the verify tree, unlinked before removal). Each mutant was applied, its targeted tests ran (go test without -race, `bun test omp`, vitest), and the file was restored with `git checkout`. The worktree was then removed. Verify-tree porcelain was the same as the baseline (empty) before and after. 6 mutants did not compile at first (unused variable or import). They were rewritten to compile and re-run, so none counts as killed by a build failure.

| # | File | Mutation | Killed? |
| - | ---- | -------- | ------- |
| D01 | `internal/api/devices_handlers.go:49` | `approve_url` carries the code again | ✅ |
| D02 | `internal/devices/devices.go` `attempt` | `>= CodeAttemptLimit` → `>` | ✅ |
| D03 | `devices.go` `attempt` | known codes never refunded | ✅ |
| D04 | `devices.go` | window 1 → 2 min | ✅ |
| D05 | `devices.go` `decide` | wrong codes on approve/deny not counted | ✅ |
| D06 | `devices.go` `Poll` | `approved_by` empty | ✅ |
| D07 | `internal/cli/setup.go:186` | approver confirmation skipped | ✅ |
| D08 | `setup.go:188` | declined token not revoked | ✅ |
| D09 | `setup.go` `displayCode` | code without hyphen | ✅ |
| D10 | `setup.go:185` | approver not printed | ✅ |
| D11 | `devices.go` `Lookup` | lookup not rate limited | ✅ |
| D12 | `internal/cli/setup_harness.go:81` | "n" accepted as yes | ✅ |
| K01 | `internal/cliconfig/config.go:170` | saved token paired with foreign URL | ✅ |
| K02 | `config.go` `SameURL` | scheme and port ignored | ✅ |
| **K03** | `internal/cli/setup.go:167` | **setup reuses saved token for another `--url`** | ❌ **Survived** |
| K04 | `setup.go:92` | `--token` warning removed | ✅ |
| K05 | `setup.go:88` | `--token-stdin` not exclusive | ✅ |
| K06 | `setup_harness.go:173` | uninstall does not revoke | ✅ |
| K07 | `setup_harness.go:173` | config deleted before revoke | ✅ |
| K08 | `internal/api/agent_handlers.go` | user tokens can self-revoke | ✅ |
| I01 | `scripts/install.sh:141` | `main` never called | ✅ |
| I02 | `install.sh:54` | no `main` wrapper (top-level body) | ✅ |
| I03 | `install.sh:63` | http release base allowed | ✅ |
| I04 | `install.sh:49` | `localhost*` lookalike allowed | ✅ |
| I05 | `install.sh:64` | http instance URL allowed | ✅ |
| H01 | `internal/harness/harness.go` `keepBackup` | backup overwritten each run | ✅ |
| H02 | `harness.go` `writeFileAtomic` | symlink replaced | ✅ |
| H03 | `internal/harness/files.go:136` | no extension rollback | ✅ |
| H04 | `files.go:137` | rollback deletes previous extension | ✅ |
| H05 | `harness.go` `saveRemoval` | emptied symlink deleted | ✅ |
| L01 | `internal/cli/launcher.go:96` | inherited `PARTYLINE_SESSION` kept | ✅ |
| L02 | `launcher.go:81` | missing binary exits 1 | ✅ |
| L03 | `launcher.go:39` | `claude-code` alias dropped | ✅ |
| L04 | `launcher.go:140` | no unregistered warning | ✅ |
| L05 | `launcher.go:102` | user args dropped | ✅ |
| L06 | `launcher.go:30` | Claude loses channel flag | ✅ |
| L07 | `launcher.go:64` | session id constant | ✅ |
| L08 | `internal/cli/doctor.go` | doctor does not name `partyline <id>` | ✅ |
| V01 | `scripts/version.sh:13` | VERSION does not win | ✅ |
| V02 | `version.sh:24` | 8-char sha | ✅ |
| V03 | `version.sh:23` | no rev-parse fallback | ✅ |
| V04 | `version.sh:28` | SOURCE_COMMIT ignored | ✅ |
| T01 | `integrations/omp/partyline.ts` `flush` | sends while one is in flight | ✅ |
| T02 | `partyline.ts` reject branch | rejected message dropped | ✅ |
| T03 | `partyline.ts` | thenable ignored (iteration-1 behavior) | ✅ |
| W01 | `web/src/routes/_app/devices.index.tsx:38` | hyphen not stripped | ✅ |
| W02 | `devices.index.tsx:44` | 429 shown as generic | ✅ |
| W03 | `web/src/lib/agent-connect.ts:98` | Codex start command `codex` | ✅ |
| W04 | `web/src/routes/_app/devices.$code.tsx:9` | old link redirects elsewhere | ✅ |

**Sensor depth**: expanded (P0 auth/credential paths), Go, shell, TS and web.
**Result**: 48/49 killed. ❌ K03 survived.

---

## Code Quality

| Principle | Status |
| --- | --- |
| Minimum code / surgical changes | ✅ |
| No scope creep | ✅ (`DELETE /agent/token` is required by ONB-09) |
| Matches patterns (dto → tygo, testkit, exec seam) | ✅ |
| Spec-anchored outcome check | ✅ except ONB-10 conformance and the untested ONB-08 setup path |
| Per-layer coverage (routes happy + edge + error, auth matrix) | ✅ lookup/approve/deny/revoke, auth + CSRF + rate limit |
| Every test maps to an AC / edge case | ✅ |
| Guidelines: `AGENTS.md`, `contract-notes.md`, `.specs/LESSONS.md` | ✅ |

### Web Interface Guidelines (WIZ-01/02, pinned rules)

- `web/src/routes/_app/devices.index.tsx:115-129`: the input has a label, `autoComplete="off"`, `spellCheck={false}` and `translate="no"`. Errors render in `FormAlert` with `role="alert"`, and the loading text is "Checking…". Minor: no `name` attribute; the placeholder `ABCD-EFGH` has no `…`; the "Continue" label is generic.
- `devices.index.tsx:262-263`: the `role="timer"` countdown has `aria-live="off"`, which is acceptable because expiry switches the view. Decorative icons are `aria-hidden`, and machine/IP are `truncate`/`translate="no"`.
- Approve/Deny are not destructive in the guideline sense (Deny is reversible by re-running setup). No blocking findings.

---

## Ranked Gaps / Fix Plans

1. **Minor: surviving mutant K03, ONB-08 setup path untested.** Setup only reuses the saved token when the start URL matches the saved one (`internal/cli/setup.go:167`), but no test fails when that check is removed. Fix: in `internal/cli/setup_test.go`, save `{URL: A, Token: tA}`, run `setup --url B --yes` (and the interactive variant) against a second testkit server, then assert that the output does not include "Using the saved agent token", that server B never received `tA` (or that setup enters browser approval), and that the file is unchanged on failure. Done when K03 is killed.
2. **Minor: ONB-10 http refusal can be bypassed** (`scripts/install.sh:47-52`). The pattern list is case-sensitive, so `Http://` and `hTTp://` pass. The loopback patterns also accept userinfo: `http://localhost:80@evil.com/`, `http://127.0.0.1:1@evil.com` and `http://[::1]:x@evil.com`. curl accepts both forms and connects to the non-loopback host over plain http (verified with curl against 127.0.0.2). Fix: lowercase the scheme before matching, refuse any `@` in the authority, and add both forms to `TestInstallRefusesPlainHTTP` (`scripts/install_test.go:349`). Exploiting this needs control of `PARTYLINE_*` env vars or a mixed-case `PARTYLINE_PUBLIC_URL`.
3. **Cosmetic: spec precision ONB-11.** The spec says "a given `VERSION` SHALL win", but `VERSION=dev` counts as unset (`scripts/version.sh:13`, tested at `version_test.go:71`). Fix: say so in the spec.

---

## Security Weaknesses (remaining; not fixed by the Verifier)

1. **Installer supply chain (partly fixed).** The `main` wrapper and the http refusal are in place (bypass: Gap 2). `checksums.txt` still comes from the same GitHub release as the binary, with no signature or provenance (cosign/minisign/SLSA), so it detects corruption but not a compromised release or account.
2. **Device-flow phishing (mitigated, not closed).** The code is now typed, never sent in a URL, rate-limited per user (5 wrong per minute, in memory, reset on restart), and the CLI shows `Approved by @user` and asks for confirmation. Residual risks:
   - `--yes` skips the approver confirmation, so `setup --browser --yes` saves a token approved by whoever typed the code.
   - Any logged-in user can still approve any code they are told ("type ABCD-EFGH"); the token then belongs to that user.
   - Device-flow agents get owner trust immediately.
3. **Token handling (mostly fixed).** Fixed: the saved token stays with its URL (Resolve), setup ignores the poll URL, `--token-stdin` exists, uninstall and a declined approval revoke the token. Remaining:
   - The token is plaintext in `config.json`, mode 0600 with no ownership check.
   - `--token` still works and only prints a warning.
   - URL comparison is strict string/host equality (`x` ≠ `x:443`). That is safe, but it can produce false mismatch errors.
4. **Config edits (fixed).** The first backup is kept, writes go through symlinks, the extension is written first with rollback. Remaining:
   - `.partyline.bak` copies of harness configs, which may hold other secrets, stay until uninstall.
   - Hook groups count as "ours" for any binary named `partyline*`.
   - `writeFileAtomic` follows a symlinked config to any target, including one outside `$HOME`.
5. **Hook fail-open (unchanged).** Every failure prints `{}`, and the reason goes only to stderr, which Codex and Gemini may hide. Messages are acked when the hook returns, and pi/OpenCode deliveries that fail after `listen` acked are only logged. The omp queue is in memory only, and a send promise that never settles stalls it.
6. **Framing / trust laundering (unchanged).** Escaping is correct. Any message from any of the owner's agents still reads as `trusted="true"`, so an agent that obeys an injected third-party message can relay it to sibling sessions as owner instructions.
7. **Build (new, low).** `scripts/version.sh` runs `git -c safe.directory='*' describe` on the build context's `.git`, which disables git's ownership check. `describe` runs no hooks or fsmonitor, so the exposure is limited to a malicious `.git/config` in a context the builder already trusts.

---

## Requirement Traceability Update

| Requirement | Previous | New |
| --- | --- | --- |
| ONB-01..07, ONB-09, ONB-11, DEV-01..06, NAME-01..03, HAR-01..05, PUSH-01..06, TURN-01..02, WIZ-01..02 | Implemented | ✅ Verified |
| ONB-08 | Implemented | ⚠️ Verified for Resolve/channel; setup path untested (Gap 1) |
| ONB-10 | Implemented | ❌ Needs fix (Gap 2) |

(spec.md is left untouched; the Verifier does not edit artifacts beyond this report.)

---

## Summary

**Overall**: ❌ Not ready, but the gaps are minor. Both gates are green, and every iteration-1 gap and security fix is verified.
**Spec-anchored check**: 35/35 ACs have evidence. ONB-10 has a conformance gap; ONB-11 has a cosmetic precision note.
**Sensor**: 48/49 killed (K03 survived).
**Gate**: `make check` EXIT 0 (Go 31 packages, web 307, integrations 53); `make e2e` 10/10.
**Next steps**: Fix 1 (K03 test) and Fix 2 (installer scheme case + userinfo), then re-verify. This is iteration 2 of 3.
