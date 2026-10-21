# Harness Onboarding Validation

## Validation: harness-onboarding - PASS ✅ (iteration-3 gaps closed)

> **Gap closure (orchestrator, 2026-10-21)**: commit "test: close the last harness-onboarding gaps" adds the Gemini outside-home settings symlink subtest (S04), asserts the `--yes` warning is absent without `--yes` (Y03), and adds installer rows for a non-numeric loopback port (I06) and `ftp://localhost` (I07); ONB-10 now names the accepted schemes. Each mutant was re-applied in the working copy and reverted: S04 → FAIL, Y03 → FAIL, I06 → FAIL, I07 → FAIL. `make check` EXIT 0 after the change.

### Iteration-3 verdict before gap closure (kept for history): FAIL ❌

**Date**: 2026-10-21
**Spec**: `.specs/features/harness-onboarding/spec.md` (amended in fix iteration 2: ONB-06, ONB-10, ONB-11, DEV-05, DEV-06, HAR-04, HAR-05)
**Diff range**: `b46fcd4..011f5d5` (fix commits T35-T43: a830bbd, a7c5985, ee125e8, ff26f2c, fa3d30d, 6aeaf00, 733c21d, 9de9784, 011f5d5; migration 0090 `device_code_attempts`)
**Verifier**: independent sub-agent (author ≠ verifier), detached worktree `partyline-verify-ho` at 011f5d5
**Verdict**: FAIL ❌. The gaps are in the tests only. Both gates pass. Every iteration-2 gap is fixed: K03 is killed, and the ONB-10 bypasses are refused in dash and bash. The probes confirm the code is correct. Four mutants on new code still survive, so tests are missing. This was the third and last fix→re-verify iteration, so the decision goes to the user (see Ranked Gaps).

---

## Iteration history

| Iteration | Range | Result | Main gaps |
| --- | --- | --- | --- |
| 1 (2026-10-20) | `2ce415c^..65e4778` | FAIL: e2e 4/6; 39/39 killed | stale e2e UI-14; omp async reorder; backup overwritten; symlinks replaced; missing tests; HAR-02/PUSH-05 precision |
| 2 (2026-10-21) | `65315a0..59ec52d` | FAIL: gates green; 48/49 killed | K03 survived (setup reused token for another URL); ONB-10 http refusal bypassable (`Http://`, `http://localhost:80@evil.com`) |
| 3 (this report) | `b46fcd4..011f5d5` | FAIL (tests only): gates green; 34 non-equivalent mutants, 30 killed, 4 survived (+2 benign, 4 equivalent) | S04 Gemini refusal untested; Y03 `--yes` warning not pinned absent without `--yes`; I06 numeric port and I07 non-http scheme in installer untested |

Status of the iteration-2 gaps: 1 K03 ✅ killed (`internal/cli/setup_test.go:449`), 2 ONB-10 bypass ✅ refused (`scripts/install_test.go:351`, plus the probes below), 3 ONB-11 wording ✅ (spec ONB-11 reworded; `scripts/version_test.go:73` `VERSION=` → git version).

---

## Task Completion

| Task | Status | Notes |
| ---- | ------ | ----- |
| T1-T34 | ✅ Done | Verified in iterations 1-2 |
| T35-T43 | ✅ Done | All `[x]`, one commit each (T43 is the docs record). The `--yes` stderr text in the T41 Done-when differs from the spec and the code; the spec and the code match (cosmetic) |

---

## Gate Check

- **`make check`**: **EXIT 0**. 31/31 Go packages ok (`go test -race`), web vitest 307/307 (30 files), integrations `bun test` 53/53, lint/vet/typecheck/build ok.
- **`make e2e`** (`PLAYWRIGHT_CHROMIUM_EXECUTABLE=~/.cache/ms-playwright/chromium_headless_shell-1248/…`): **EXIT 0, 10/10 passed** on the first run. No re-run was needed, and `TestLinkPausesAfterReplaced` did not flake in `make check`.
- **Test delta (fix commits)**: +7 Go tests, 0 removed. One assertion changed on purpose: `clis_test.go:192` "another binary updates in place" became "same binary". Under the narrowed HAR-04, another binary's group is not Partyline's. See the residual weakness in 4c.
- The verify-tree porcelain was empty before and after the gates and the sensor.

---

## Spec-Anchored Acceptance Criteria (ACs changed in fix iteration 2; the other 28 still pass as in iterations 1-2 and their tests are unchanged)

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| ONB-06 doctor reports leftover backup as warning | `WARN` line with delete command; doctor does not fail | `internal/cli/doctor_test.go:221` `Contains("WARN backups: "+bak+" is a copy…\n     fix: delete it once you no longer need it: rm "+bak)`, `Equal(0, r.code)`, suffix `All checks passed.` | ✅ |
| ONB-08 setup never reuses a token saved for another URL | no "Using the saved agent token"; browser approval on B; config `{B, new token}` | `internal/cli/setup_test.go:449` `NotContains("Using the saved agent token")`, `True(approved)`, `Equal(b.URL, got.URL)`, `:484` `NotEqual(a.secret, got.Token)`, `requireAgentToken(b, …)` (--yes and interactive) | ✅ (K03 killed) |
| ONB-10 scheme without case, exact loopback host + numeric port, no scheme-less, no userinfo | exit 1 with message, nothing downloaded; loopback allowed | `scripts/install_test.go:351` 14 refused cases with exact `refusing plain http URL …`/`user info (@) is not allowed`/`refusing URL … use https`, `Equal(before, r.count())`; `:392` 6 accepted incl. `HTTP://LocalHost:8080/` | ✅ behavior (probes); ⚠️ the numeric-port rule (I06) and non-http schemes (I07) have no test |
| ONB-11 empty VERSION and `VERSION=dev` count as unset | git version | `scripts/version_test.go:72-73` `Equal("v1.2.3\n", …"VERSION=dev")`, `…"VERSION="` | ✅ |
| DEV-05 count survives restart | 5 wrong, restart → 429; +61 s → 200 approved | `internal/api/devices_handlers_test.go:314` `RequireError(429)` after `e.env.Restart()` (`:320`) for lookup and approve, then `Equal(DeviceStatusApproved, d.Status)` | ✅ |
| DEV-06 `--yes` stderr warning | exact text on stderr | `internal/cli/setup_test.go:361` `Contains(r.stderr, "partyline setup: warning: --yes accepted the agent token of @bob without asking; if @bob is not you, run partyline setup --uninstall to revoke it\n")` | ✅; ⚠️ nothing pins that the warning is absent without `--yes` (Y03) |
| HAR-04 hook group is ours only with exact command | other partyline* groups unchanged, unlisted; ours appended/removed | `internal/harness/clis_test.go:202` `Empty(entries)`, `Empty(RemovePlan)`, `:230` `Len(stop, 4)`, `Equal(foreign, readFile(hooks))` after Remove | ✅ |
| HAR-05 backup 0600; deleted after success unless `--keep-backups`; failed run keeps; earlier backup kept | mode and presence | `internal/harness/files_test.go:422` `Equal(0o600, …)` with config 0644; `internal/cli/setup_harness_test.go:222` ErrNotExist after success, `:253` 0600 + `Kept the backup` with flag, kept after failure, earlier backup bytes kept | ✅ |
| HAR-05 symlink into home written; outside home refused, nothing changed | exact error; target/config/ext/backup untouched | `files_test.go:364` `EqualError("refusing to write through the symlink "+link+": its target "+target+" is outside your home directory "+env.Home)` ×(opencode, pi, omp)×(config, extension), target bytes and `requireMissing(bak)`; `:384` Codex hooks + `Empty(cli.calls)`; symlinked home writes | ✅ for file harnesses and Codex; ⚠️ **Gemini not tested** (S04 survives; the probe shows the code refuses correctly, with no CLI call) |

**Status**: 35/35 ACs have spec-anchored evidence. The behavior of every amended AC was confirmed. Some branches are untested: Gemini's refusal, the `--yes` warning's absence, the installer's port and scheme rules.

---

## Edge Cases

- [x] Setup with `{A, tokenA}` saved and `--url B`: browser approval on B, A's token never sent (`setup_test.go:449`).
- [x] Pre-existing backup is not deleted by setup (`setup_harness_test.go:222` "an earlier backup stays"); doctor warns about it.
- [x] Symlinked home counts as home (`files_test.go:347` "symlinked home").
- [x] Wrong-code burst still capped at 5 with the DB limiter (`devices_handlers_test.go:292` `{404:5, 429:7}`).
- [ ] A symlinked config directory (e.g. `~/.pi/agent → /outside`) is written outside home, backup included (probe A). The spec only addresses a symlink at the file. Recorded as residual weakness 4b.
- [ ] Binary path changed between setups: the old Codex/Gemini hook group stays, a second one is appended, and uninstall and doctor no longer see the old one (probe B). This follows from the narrowed HAR-04. Recorded as residual weakness 4c.

### ONB-10 conformance probes (`require_https` extracted from `scripts/install.sh:47-75`, run under dash and bash, identical results)

Refused: `Http://evil.com`, `hTTp://evil.com`, `HTTP://evil.com`, `http://localhost:80@evil.com/`, `http://127.0.0.1:1@evil.com`, `http://[::1]:8080@evil.com`, `evil.com`, `//evil.com`, `localhost:8080`, `http://localhost.evil.com`, `http://localhost.`, `http://127.0.0.1.nip.io`, `http://127.1`, `http://0x7f000001`, `http://2130706433`, `http://[::1]evil.com`, `http://[::1].evil.com`, `http://[::ffff:127.0.0.1]`, `http://localhost:80evil`, `http://localhost\@evil.com`, `http://localhost%40evil.com`, space/tab/newline/comma injections, `http:/evil.com`, `http:\\evil.com`, `ftp://x`, `http://user@localhost`.
Allowed (all loopback): `http://[::1]`, `http://[::1]:8080[/x]`, `http://localhost:8080`, `HTTP://LocalHost:8080/`, `http://LOCALHOST`, `http://127.0.0.1`, `http://localhost:`, `http://localhost:080`, `http://localhost/https://evil`, `http://localhost#@evil.com`, `http://localhost?@evil.com`, `https://…`. curl 8 and wget both connect to `localhost` for the `#@`/`?@` forms (checked against port 9). `http://localhost:99999` is allowed by the script but rejected by curl and wget ("Bad port"), which is harmless.

---

## Discrimination Sensor

Scratch: `git worktree add --detach /tmp/claude-1001/pl-ho-mutants3 011f5d5`. A Python runner applied each mutant by exact single-match replacement, built and vetted the code, ran the targeted tests (`go test -count=1`, with `-race` for P04), and restored the file with `git checkout`. The scratch worktree and a second probe worktree were then removed with `git worktree remove --force` and pruned. The verify tree was never stashed or edited, and its porcelain was empty before and after. Two mutants did not compile at first (S01, P03). They were rewritten (S01b, P03b) and run, so none counts as killed by a build failure.

| # | File | Mutation | Killed? |
| - | ---- | -------- | ------- |
| K03 | `internal/cli/setup.go:169` | saved token reused for any `--url` (iteration-2 survivor) | ✅ |
| S01b | `internal/harness/harness.go:221` | outside-home check removed (`rel` never `..`) | ✅ |
| S02 | `harness.go:216` | HOME not resolved through symlinks | ✅ |
| S03 | `internal/harness/clis.go:150` | Codex pre-check removed (CLI runs before refusal) | ✅ |
| **S04** | `clis.go:276` | **Gemini pre-check removed (`gemini mcp add` runs before refusal)** | ❌ Survived |
| S05 | `harness.go:173` | `writeDoc` guard removed | ⚪ equivalent (all callers pre-check; `writeFileAtomic` checks again) |
| S06 | `internal/harness/files.go:110` | file-harness pre-check removed | ⚪ equivalent (the extension write is checked; on config refusal the rollback deletes or restores it, so the end state is the same) |
| S07 | `harness.go:222` | outside-home link silently written | ✅ |
| O01 | `harness.go:138` | ownership by suffix + "partyline" (iteration-2 rule) | ✅ |
| O02 | `harness.go:138` | prefix match (accepts `… --extra`) | ✅ |
| O03 | `clis.go:385` | Entries lists every group | ✅ |
| O04 | `clis.go:429` | Remove deletes any group mentioning partyline | ✅ |
| O05 | `clis.go:406` | Apply updates first group with `hook <name>` | ✅ |
| O06 | `clis.go:370` | a group with an empty `hooks` array counts as ours | 🟡 survived, benign (no program's hook changes) |
| B01 | `harness.go:201` | backup 0644 | ✅ |
| B02 | `internal/cli/setup_harness.go:121` | backups never settled | ✅ |
| B03 | `setup_harness.go:139` | `--keep-backups` ignored | ✅ |
| B04 | `setup_harness.go:121` | backups deleted after a failed run | ✅ |
| B05 | `setup_harness.go:135` | pre-existing backup deleted | ✅ |
| B10 | `setup_harness.go:101` | every backup treated as pre-existing | ✅ |
| B06 | `internal/cli/doctor.go:216` | backup reported as FAIL | ✅ |
| B07 | `doctor.go:59` | backup check not run | ✅ |
| B08 | `doctor.go:75` | fix line only for `!ok` | ⚪ equivalent (warn checks have `ok=false`) |
| B09 | `doctor.go:68` | no WARN mark | ✅ |
| Y01 | `setup.go:188` | no `--yes` warning | ✅ |
| Y02 | `setup.go:191` | warning on stdout | ✅ |
| **Y03** | `setup.go:188` | **warning printed also without `--yes`** | ❌ Survived |
| Y04 | `setup.go:191` | second `@<user>` wrong | ✅ |
| P01 | `internal/devices/devices.go:100` | attempts wiped on service start (restart reset) | ✅ |
| P02 | `devices.go:237` | all attempts expire each call | ✅ |
| P03b | `devices.go:260` | known code not refunded | ✅ |
| P04 | `devices.go:235` | count and insert in separate transactions | ✅ (burst test, `-race`) |
| P05 | `devices.go:240` | window shortened by 2 s | ✅ |
| P06 | `internal/store/db/devices.sql.go` | counts other users' attempts | ✅ |
| I01 | `scripts/install.sh:50` | scheme not lowercased | ✅ |
| I02 | `install.sh:53` | userinfo allowed | ✅ |
| I03 | `install.sh:49` | scheme-less check removed | ⚪ equivalent (the scheme `case` refuses with the same message) |
| I04 | `install.sh:64` | host not lowercased | ✅ |
| I05 | `install.sh:61` | IPv6 bracket parsing removed | ✅ |
| **I06** | `install.sh:67` | **non-numeric port allowed** | ❌ Survived (curl/wget reject such ports, so low risk) |
| **I07** | `install.sh:58` | **schemes other than http/https fall through to the host check (`ftp://localhost` allowed)** | ❌ Survived |
| I08 | `install.sh:51` | authority cut only at `/` (refuses `http://localhost?x`) | 🟡 survived, stricter (no accepted URL with `?`/`#` in the tests) |

**Sensor depth**: expanded (P0 credential/auth/supply-chain), ≥12 mutations on T38-T42 (S×7, O×6, B×9, Y×4, P×6) plus K03 and I×8.
**Sensor outcome (iteration 3)**: 44 mutants run. 4 are equivalent; of the other 40, 34 were killed, 4 survived (S04, Y03, I06, I07) and 2 survived but are benign (O06, I08). FAIL ❌.

---

## Code Quality

| Principle | Status |
| --- | --- |
| Minimum code / surgical changes | ✅ (`Backups()` on the Registrar interface is the minimum needed for doctor and settle) |
| No scope creep | ✅ |
| Matches patterns (sqlc queries + migration, testkit Restart, Env methods) | ✅. Migration `0090` is flagged in the contract notes for renumbering at merge |
| Spec-anchored outcome check | ✅ with the 4 test gaps above |
| Per-layer coverage | ✅ route (restart, burst), CLI, harness unit; ⚠️ Gemini sibling of the symlink guard |
| Every test maps to an AC | ✅ |
| Guidelines: `AGENTS.md`, `contract-notes.md`, `.specs/LESSONS.md` | ✅ |

---

## Ranked Gaps / Fix Plans (test-only; the behavior is correct, confirmed by probes)

1. **Minor, S04 (HAR-05)**: no test refuses a Gemini `settings.json` symlink outside home. The probe confirms the code refuses it with the exact error and makes no CLI call. Fix: add a `gemini` subtest beside "codex hooks" in `TestHarnessRefusesSymlinkOutsideHome` (`internal/harness/files_test.go:376`) with `EqualError(…)`, `Empty(cli.calls)` and the target bytes unchanged.
2. **Minor, Y03 (DEV-06)**: the interactive cases of `TestSetupBrowserApproverConfirmation` (`internal/cli/setup_test.go:331`) do not assert `NotContains(r.stderr, "--yes accepted")`.
3. **Minor, I06 (ONB-10 "optionally with a numeric port")**: add a refused `http://localhost:80evil` to `TestInstallRefusesPlainHTTP` (`scripts/install_test.go:351`).
4. **Minor, I07 (ONB-10)**: add a refused `ftp://localhost/x` (or `file://localhost/…`) case. The spec does not name other schemes, so also state in ONB-10 that only `https` (and `http` to loopback) are accepted.
5. Cosmetic: the `--yes` stderr text in the T41 Done-when (`tasks.md`) is not the shipped text. The spec and the code agree.
6. Optional (benign survivors): accept `http://localhost:8080/?x` in the allowed list (I08); a hook group with empty `hooks` (O06).

**Escalation**: this was the third fix→re-verify iteration, so the protocol bound is reached. The remaining gaps are four missing assertions, about 15 lines of tests, with no behavior change. The user decides whether to merge with these as follow-up or to run one more fix round.

---

## Security Weaknesses: final status (not fixed by the Verifier)

1. **Installer supply chain.** ✅ The http refusal now holds: every bypass in the probe list is refused under dash and bash, and the allowed forms reach only loopback in curl and wget. ✅ The `main` wrapper is in place. ⚠️ Still open: `checksums.txt` comes from the same release as the binary, with no signature or provenance (cosign/minisign/SLSA).
2. **Device-flow phishing (mitigated).**
   - ✅ The code is typed, never in a URL. The wrong-code limit is now in the database and survives a restart. `--yes` names the approver on stdout and warns on stderr.
   - ⚠️ Still open: `--yes` still saves without confirmation.
   - ⚠️ Still open: any logged-in user can approve a code they are told, and the token is then theirs.
   - ⚠️ Still open: device-flow agents get owner trust immediately.
3. **Token handling.** ✅ The saved token stays with its URL (Resolve and setup). ✅ `--token-stdin` exists. ✅ Uninstall and a declined approval revoke the token. ⚠️ Still open: the token is plaintext in `config.json` (0600, no ownership check), and `--token` only warns.
4. **Config edits (mostly fixed).**
   - a. ✅ New backups are 0600 and deleted after a successful setup, and doctor warns about leftovers. ⚠️ A backup from an older version stays 0644 (probe C), though doctor names it.
   - b. ✅ A symlinked config file pointing outside home is refused. ⚠️ A symlinked config directory (`~/.pi/agent → /elsewhere`) is still written outside home, backup included (probe A). Exploiting it needs write access to the home, so it is low.
   - c. ✅ Only the exact hook command counts as Partyline's. ⚠️ Trade-off: after the binary moves (`go install` vs `install.sh`), re-running setup leaves the old group and appends a new one. Codex/Gemini then run both, and uninstall and doctor do not see the old one (probe B). Fix idea: doctor could warn about groups with `* hook codex-stop|gemini-after-agent` that are not ours.
5. **Hook fail-open (unchanged).** Failures print `{}`, and the reason goes only to stderr. Deliveries are acked when the hook returns, the omp queue is in memory only, and a send promise that never settles stalls it.
6. **Trust laundering (unchanged).** Any of the owner's agents reads as `trusted="true"`, so an agent that obeys an injected message can relay it to sibling sessions as owner instructions.
7. **Build (unchanged, low).** `scripts/version.sh` uses `git -c safe.directory='*' describe`.

---

## Requirement Traceability Update

| Requirement | Previous | New |
| --- | --- | --- |
| All 35 (ONB-01..11, DEV-01..06, NAME-01..03, HAR-01..05, PUSH-01..06, TURN-01..02, WIZ-01..02) | Implemented | ✅ Behavior verified |
| HAR-05, DEV-06, ONB-10 | Implemented | ⚠️ Verified; missing assertions S04, Y03, I06, I07 |

(spec.md is left untouched; the Verifier edits no artifact beyond this report.)

---

## Summary

**Overall**: ❌ Not ready by protocol: 4 surviving mutants, all test-strength gaps. The behavior is correct, and both gates are green. Iteration 3 of 3, so this goes to the user.
**Spec-anchored check**: 35/35 ACs have evidence. 4 branches of amended ACs have no assertion.
**Sensor**: 44 mutants run: 34 killed, 4 survived, 2 benign survivors, 4 equivalent.
**Gate**: `make check` EXIT 0 (Go 31 packages, web 307, integrations 53); `make e2e` 10/10.
