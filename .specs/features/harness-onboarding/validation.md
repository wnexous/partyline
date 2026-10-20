# Harness Onboarding Validation

## Validation: harness-onboarding - FAIL ❌

**Date**: 2026-10-20
**Spec**: `.specs/features/harness-onboarding/spec.md`
**Diff range**: `2ce415c^..65e4778` (T1-T22; spec from b9d5995)
**Verifier**: independent sub-agent (author ≠ verifier), detached worktree `partyline-verify-ho` at 65e4778
**Verdict**: FAIL ❌ (one gate failure; every AC has test evidence; 39/39 effective mutants killed)

---

## Task Completion

| Task | Status | Notes |
| ---- | ------ | ----- |
| T1-T16 | ✅ Done | Commits 2ce415c … 4374418 |
| T17 | ✅ Done | `uat.md`: Claude Code, pi, OpenCode pass. omp, Codex and Gemini are partial because the devbox credentials do not work for them (follow-up 3 is still open) |
| T18-T22 | ✅ Done | c1eed23 … 65e4778; the UAT fixes were not re-run against real harnesses (uat.md says so) |
| (missing) | ❌ | No task updated `web/e2e/smoke.spec.ts` UI-14 after T15 replaced the wizard, so `make e2e` fails (Gap 1) |

---

## Gate Check

- **Build gate `make check`** (generate, lint, vet, web lint/typecheck, `go test -race ./...`, web vitest, `make integrations-test`, web build, go build): **EXIT 0**. All 31 Go packages pass, web 298/298 tests (30 files), integrations 52/52 tests.
- **`make e2e`** (`PLAYWRIGHT_CHROMIUM_EXECUTABLE=…chromium_headless_shell-1243…`): **EXIT 2 on both runs, so it is deterministic**. The results were 4 passed, 2 failed and 2 did not run. `web/e2e/smoke.spec.ts:133` UI-14, on desktop-1280 and mobile-360, waits for `getByLabel("Agent name")` and a "Create token" button (`smoke.spec.ts:142-143`). T15 (5cc45a1) removed both from the wizard, so the test times out after 60 s. The suite runs in serial mode (`smoke.spec.ts:8`), so UI-24 (`:168`) is skipped. Run on its own (`-g "UI-01|UI-04|UI-24"`), UI-24 passes 6/6.
- **Test delta (diff range)**: +137 Go `func Test`, 0 removed; web +18 `it(` and −9 `it(`. The removed web tests are those of the old token-creating wizard, which T15 replaced. The deletion is justified, but the e2e equivalent was neither removed nor rewritten. 52 new `bun test` cases.
- Skipped tests: the Windows-only skips in doctor and install are justified (unix permission bits, POSIX installer).

---

## Spec-Anchored Acceptance Criteria

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| ONB-01 install latest, verify, ~/.local/bin, PATH hint | binary 0755 in `~/.local/bin`, latest from API, hint `export PATH="<dir>:$PATH"` | `scripts/install_test.go:145` `require.Equal(0o755, …Perm())`, `require.True(r.requested("/api/latest"))`, `require.Contains(stdout, \`export PATH="…:$PATH"\`)`; `:169` no hint when on PATH; `:182` 4 os/arch assets + unsupported fail | ✅ |
| ONB-02 checksum mismatch → non-zero, no binary | exit≠0, install dir empty | `scripts/install_test.go:225` `require.NotEqual(0, code)`, `require.Empty(entries, "no binary and no temporary file")`; `:241` unlisted asset | ✅ |
| ONB-03 `/install.sh` with instance URL default | served script has `DEFAULT_URL='<instance>'` | `internal/api/install_handler_test.go:51` `require.Contains(body, "DEFAULT_URL='"+env.URL+"'\n")`; `scripts/install_test.go:257` writes `{"url":…,"token":""}` 0600/0700 and never overwrites; `:298` quote escaping | ✅ |
| ONB-04 setup verifies and writes config 0600 | token verified, file 0600, nothing written on failure | `internal/cli/setup_test.go:111` `requireSavedConfig` (0600); `:166` invalid token → exit 1 and no file / file byte-identical; `:188` user token refused; `:201` unreachable | ✅ |
| ONB-05 channel reads file; env/flags override | flag > env > file | `internal/cliconfig/config_test.go:130` exact `Config{…}` per precedence level; `internal/cli/channel_config_test.go:32,43,59` | ✅ |
| ONB-06 doctor checks + fix hints | pass/fail line + fix per check | `internal/cli/doctor_test.go:77` exact all-pass stdout; `:95` one subtest per failing check with its exact `fix:` text (binary, config missing/0640/dir 0750/no token, server, token, missing harness binary, no harness, versions differ/unknown) | ✅ |
| ONB-07 module version from build info | module version, `(devel)`→`dev`, ldflags win | `internal/cli/version_test.go:32,42,53` `require.Equal("v1.2.3\n", out)` etc. | ✅ |
| DEV-01 8-char code, 10 min, URL `<instance>/devices/<code>` | regex `[A-HJ-NP-Z2-9]{8}`, expires_at = t0+10 min | `internal/api/devices_handlers_test.go:51` `require.Regexp`, `require.Equal(env.URL+"/devices/"+code, ApproveURL)`, `require.True(t0.Add(10m).Equal(ExpiresAt))`; CLI prints it: `internal/cli/setup_test.go:229` | ✅ |
| DEV-02 approval → token named after host, delivered once | `<host> (via setup)`, agent kind, owned by approver, second poll no token | `devices_handlers_test.go:81` `require.Equal("laptop-01 (via setup)", …)`, `require.Equal(DevicePoll{Status: approved}, again)`; `:106` owner = approver; `setup_test.go:229` `"devbox-7 (via setup)"` | ✅ |
| DEV-03 expired/denied → exit≠0 with message | `approval expired` / `approval denied` | `setup_test.go:292` `require.Equal(1, code)`, `Contains("approval denied")`; `:312` `"approval expired"` after 5 polls of 2 min; server: `devices_handlers_test.go:119,138` (410 on approve after expiry, poll reads `expired`) | ✅ |
| DEV-04 poll 1/2 s per request; create 10/h/IP | 429 at 1.9 s, 200 at 2 s; 11th create 429 | `devices_handlers_test.go:208` `RequireError(429)` at +1.9 s, `RequireStatus(200)` at +2.0 s; `:162` 11th → 429, other IP ok, +61 min ok; `:177` IPv6 /64; `:187` burst exactly 10/16 | ✅ |
| NAME-01 git root basename, else cwd, slugified | `my-repo` from subdir, `plain-dir` outside git | `internal/channel/name_test.go:25` `require.Equal("my-repo", DeriveName(sub))`, `require.Equal("plain-dir", …)` | ✅ |
| NAME-02 derived name online → `<name>-N`, welcome carries it | `proj`, `proj-2`, `proj-3`; offline reused; cap 50 | `internal/api/agent_names_test.go:38` welcome names; `:56` reuse; `:89` concurrent distinct; `:145` cap 409 "agent limit reached"; `:171` length limit; CLI `internal/cli/channel_names_test.go:31` `{"proj","proj-2"}` online | ✅ |
| NAME-03 explicit → newest wins | old socket closed 4000 `replaced` | `agent_names_test.go:126` `require.Equal(StatusCode(4000), ce.Code)`, `"replaced"`; `channel_names_test.go:56` | ✅ |
| HAR-01 show exact change, confirm, register at user scope with absolute path | exact CLI argv / JSON entry per harness | `internal/cli/setup_harness_test.go:60` plan printed before `[Y/n]`, declined → nothing; `internal/harness/clis_test.go:96,170,292` exact argv (`claude mcp add --scope user …`, `codex mcp add … --tools-only` + Stop hook, `gemini mcp add -s user …` + AfterAgent); `files_test.go:90` exact entries for OpenCode/pi/omp + extension with version header; `:68` absolute binary in extension | ✅ |
| HAR-02 update in place, others byte-for-byte | file equals expected bytes | `files_test.go:90` `require.Equal(c.withEntry, readFile(cfg))`, `:127` in-place update; `clis_test.go:170` hooks.json exact bytes + update in place | ✅ for files setup edits ⚠️ For `~/.claude.json`, `config.toml` and Gemini's `mcpServers`, the harness CLI rewrites the file itself (contract notes say so; the spec does not) |
| HAR-03 `partyline claude …` argv | `claude --dangerously-load-development-channels server:partyline [args]` | `internal/cli/doctor_test.go:205` `require.Equal([]string{"claude","--dangerously-load-development-channels","server:partyline","--resume","abc","-p","hi there"}, argv)` | ✅ |
| HAR-04 uninstall removes exactly what was added | original bytes back, extension + .bak gone, foreign files kept | `files_test.go:90` `require.Equal(c.orig, readFile(cfg))`, `requireMissing(ext/.bak)`; `:146` created config deleted; `:179` foreign extension kept; `clis_test.go:200,231,292`; `setup_harness_test.go:139` | ✅ |
| PUSH-01 listen: JSON line per message, ack after write | exact meta+content map; unwritten line not acked | `internal/cli/listen_test.go:112` `require.Equal(map[string]string{…}, p.line())`; `:155` cursor moves, not repeated; `:183` write failure → exit 1, message redelivered | ✅ |
| PUSH-02 tools-only: no push socket, shares name | agent never online; tools use listen's name | `listen_test.go:201`, `:239`; `internal/api/agent_register_test.go:17` registered offline | ✅ |
| PUSH-03 omp: notification → sendUserMessage (followUp while busy); hold and retry 1 s→30 s, agent_end, one notice | exact sent list, delays `[1000,2000,4000,8000,16000,30000,30000]` | `integrations/omp/partyline.test.ts` "a channel notification while idle…" `toEqual([{content}])`, "busy … followUp", "retries back off…" exact delays, "held messages keep their order…", "without a model nothing is sent; agent_end…" | ✅ (sync throws). ⚠️ Order is not kept when sends return promises that later reject (Gap 3) |
| PUSH-04 pi: spawn listen, `sendMessage(…, {triggerTurn:true, deliverAs:"followUp"})` | exact options | `integrations/pi/partyline.test.ts:52,65` | ✅ |
| PUSH-05 OpenCode: listen → active session prompt (async) | `promptAsync` on active top-level session; queued before first | `integrations/opencode/partyline.test.ts:71,81,91,100,109` | ✅ (spec names `session.prompt`; the code uses `promptAsync` and falls back to `prompt`. The contract notes record this) |
| PUSH-06 framing + trust-aware line; instructions explain both | literal owner/data lines; `trusted` only "true" → owner | `internal/channel/frame_test.go:14` (shared `framing.json`), `:32` literal lines incl. `"yes"`,`"TRUE"`,`""` → data line; adapters: per-case `PUSH-06: framing` tests + `testdata/shared.test.ts` identical blocks; instructions `internal/channel/instructions_test.go:23,53,66` | ✅ (message_id/chat_id attrs added beyond spec list, documented) |
| TURN-01 `wait_for_messages(≤300)` pending or empty on timeout | "no new messages in N seconds"; 301 → 422 | `internal/cli/hook_test.go:111` exact texts, ≥1 s elapsed; `internal/api/agent_pending_test.go:93,117` | ✅ |
| TURN-02 hooks: block/deny with messages as reason; none → allow | `{"decision":"block"\|"deny","reason":<framed>}`, else `{}` | `hook_test.go:43` exact map incl. framed reason; `:68` `"{}\n"`; `:89` failures → `{}` exit 0 | ✅ |
| WIZ-01 install + `setup --browser`, harness selector with start command, manual JSON without token | exact commands, 6 tabs, no token | `web/src/routes/_app/agents.test.tsx:186,202,234` exact strings; `web/src/lib/agent-connect.test.ts`; e2e `web/e2e/smoke.spec.ts:133` **stale, fails** | ✅ unit / ❌ e2e |
| WIZ-02 `/devices/<code>`: machine, code, Approve/Deny | shown, approve/deny call API | `web/src/routes/_app/devices.test.tsx:36,49,68,86,114,128,157` | ✅ |

**Status**: All 28 ACs have spec-anchored unit/integration evidence. ⚠️ 2 spec-precision gaps (HAR-02 for CLI-managed files, PUSH-05 prompt vs promptAsync). ❌ The e2e gate fails.

---

## Edge Cases

- [x] Config not 0600 → channel and doctor refuse and print the chmod fix (`internal/cli/channel_config_test.go:69`, `doctor_test.go:95` subtests). `listen` and the hooks use the same `cliconfig.Resolve` (`internal/cliconfig/config.go:145`), which refuses it as well. No listen-level test covers this (minor).
- [x] git root beats the subdirectory (`internal/channel/name_test.go:38`).
- [x] Invalid JSON harness file is not touched and the parse error names the file (`internal/harness/files_test.go:160`, `clis_test.go:325`). Invalid TOML in `config.toml` is left to `codex mcp add`, which refuses it; there is no test with a real TOML error.
- [x] Device poll after expiry: a pending request reads `expired`, and an approved one still delivers its token (`devices_handlers_test.go:138`).
- [x] Approval auth: anonymous 401, agent token 403 (`devices_handlers_test.go:246`). CSRF comes from the global middleware (`internal/api/middleware.go:234`, tested in `middleware_test.go:140` on another route). No device-route CSRF test exists.

---

## Discrimination Sensor

Scratch: `git worktree add --detach /tmp/claude-1001/pl-ho-mutants 65e4778`. `web/node_modules` was symlinked from the verify tree. Each mutation was applied, the targeted tests ran, and the file was restored. The worktree was then removed. The verify-tree `git status --porcelain` matched the pre-sensor baseline (empty before and after).

| # | File:line | Mutation | Killed? |
| - | --------- | -------- | ------- |
| G01 | `internal/devices/devices.go:41` | RequestTTL 10→11 min | ✅ |
| G02 | `internal/devices/devices.go:331` | token not cleared after delivery | ✅ |
| G03 | `internal/devices/devices.go:188` | `n >= CreateLimit` → `>` | ✅ |
| G04 | `internal/devices/devices.go:315` | poll interval halved | ✅ |
| G05 | `internal/devices/devices.go:263` | expired request approvable | ✅ |
| G06 | `internal/cliconfig/config.go:97` | dir check ignores group bits | ✅ |
| G07 | `internal/cliconfig/config.go:152` | env ignored for URL | ✅ |
| G08 | `internal/cliconfig/config.go:123` | config saved 0644 | ✅ |
| G09 | `internal/agents/gateway.go:297` | online agents count as free names | ✅ |
| G10 | `internal/agents/gateway.go:313` | suffix starts at -3 | ✅ |
| G11 | `internal/harness/harness.go:237` | uninstall keeps `.partyline.bak` | ✅ |
| G12 | `internal/harness/harness.go:166` | parse errors ignored | ✅ |
| G13 | `internal/cli/launcher.go:13` | `server:partyline` dropped from argv | ✅ |
| G14 | `internal/cli/hook.go:23` | Gemini decision `deny`→`block` | ✅ |
| G15 | `internal/cli/hook.go:70` | hook fails closed (exit 1) | ✅ |
| G16 | `internal/channel/listen.go:120` | write error swallowed (ack anyway) | ✅ |
| G17 | `internal/channel/frame.go:37` | `trusted != "false"` → owner line | ✅ |
| G18 | `scripts/install.sh:80` | checksum mismatch only warns | ✅ |
| G19 | `scripts/install.sh:79` | binary unpacked into install dir before verification | ✅ |
| G20 | `scripts/scripts.go:19` | instance URL not substituted | ✅ |
| G21 | `internal/cli/doctor.go:202` | differing versions pass | ✅ |
| G22 | `internal/api/agent_handlers.go:77` | wait=301 accepted | ✅ |
| G23 | `internal/channel/run.go:100` | tools-only ignores listen's name | ✅ |
| G24 | `internal/harness/files.go:244` | omp args lose `--harness omp` | ✅ |
| T01 | `integrations/omp/partyline.ts:128` | always followUp (idle never starts a turn) | ✅ |
| T02 | `integrations/omp/partyline.ts:27` | max backoff 60 s | ✅ |
| T03 | `integrations/omp/partyline.ts:121` | no-model check removed | ✅ |
| T04 | `integrations/omp/partyline.ts:106` | notice on every failure | ✅ |
| T05 | `integrations/pi/partyline.ts:180` | `triggerTurn:false` | ✅ |
| T06 | `integrations/opencode/partyline.ts:218` | parentID check removed in created branch | ⚪ equivalent (the `children.has` guard at :223 still blocks it), replaced by T06b |
| T06b | `integrations/opencode/partyline.ts:223` | `children.has(id)` guard removed | ✅ |
| T07 | all 3 adapters `frame()` | `trusted !== "false"` | ✅ |
| T08 | pi + OpenCode supervision | restart on exit 2 | ✅ |
| T09 | `integrations/omp/partyline.ts:136` | rejected send dropped, not re-queued | ✅ |
| W01 | `web/src/lib/agent-connect.ts:18` | trailing slash kept | ✅ |
| W02 | `web/src/lib/agent-connect.ts:97` | Codex delivery `push` | ✅ |
| W03 | `web/src/routes/_app/devices.$code.tsx:99` | 410 not shown as expired | ✅ |
| W04 | `web/src/routes/_app/devices.$code.tsx:214` | Approve sends deny | ✅ |
| W05 | `web/src/lib/agent-connect.ts:110` | token placeholder in Gemini manual entry | ✅ |

**Sensor depth**: expanded (P0 auth/credential paths), Go + TS + web.
**Sensor outcome**: 39/39 effective mutants killed (1 equivalent mutant excluded); no surviving mutant.

---

## Code Quality

| Principle | Status |
| --- | --- |
| Minimum code / surgical changes | ✅ |
| No scope creep | ✅ (`channel --harness omp` and `POST /agent/register` are justified by UAT / PUSH-02) |
| Matches patterns (DTOs → tygo, testkit e2e, sqlc) | ✅ |
| Spec-anchored outcome check | ✅ with 2 ⚠️ precision gaps |
| Per-layer coverage (routes happy + edge + error, auth matrix) | ✅ devices, pending, register, install |
| Every test maps to an AC / edge case / Done-when | ✅ |
| Documented guidelines: `AGENTS.md`, `contract-notes.md`, `.specs/LESSONS.md` | ⚠️ L-029 (full gate before commit) does not cover `make e2e`, which is where the regression hid |

### Web Interface Guidelines (WIZ-01/02; pinned rules)

- `web/src/routes/_app/devices.$code.tsx:206-218`: one click on Approve grants an agent token, and the page has no "type the code from your terminal" step. The code is part of the link (see Security 2).
- `devices.$code.tsx:184-188`: `role="timer"` with `aria-live="off"` is acceptable. The countdown is decorative, and expiry switches the page state.
- `web/src/components/connect-agent-dialog.tsx`: tabs are labelled (`aria-label`), decorative icons are `aria-hidden`, the `<summary>` has a focus-visible ring and motion-reduce, external links use `noopener`, and code is marked `translate="no"`. No findings.

---

## Ranked Gaps / Fix Plans

1. **Blocker: `make e2e` fails.** In `web/e2e/smoke.spec.ts:133-166`, UI-14 still drives the removed token-creating wizard (Agent name, Create token, `.mcp.json` with `PARTYLINE_TOKEN`). That contradicts WIZ-01 ("no token inline"). Fix: rewrite UI-14 for the new wizard: install command with the instance origin, `partyline setup --browser`, harness tabs and start commands, and no `pl_agent`/`PARTYLINE_TOKEN` in the Advanced entries. Optionally add a devices approve e2e. Done when `make e2e` passes 8/8 on both projects.
2. **Major: the omp retry order breaks for async rejections** (PUSH-03 "retry in order"). `integrations/omp/partyline.ts:133-139` shifts the message out of the queue before its promise settles and keeps sending the next one. A later rejection `unshift`s the message after newer ones were delivered, and two rejections reverse the order. omp 18.6.1 returns void, so this is latent today. Fix: stop the flush loop on a thenable until it settles. Test: two sends that reject must be delivered A then B.
3. **Minor: running setup twice overwrites the `.partyline.bak`.** `internal/harness/harness.go:186-193` backs up the file on every Apply, so after a second `setup` the backup already contains the Partyline entry. Fix: keep the first backup.
4. **Minor: the atomic rename replaces symlinked harness configs** (dotfile managers). `writeFileAtomic` (`internal/harness/harness.go:200`) renames over the link. Fix: resolve the symlink and write the target, or refuse.
5. **Minor: missing evidence.** No listen-level test for a non-0600 config refusal, no device-route CSRF test, and no real-TOML parse-error test.

## Security Weaknesses (not fixed; for the maintainer)

1. **Installer supply chain.** `checksums.txt` comes from the same GitHub release as the binary. It catches corruption, not a compromised release or account; there is no signature or provenance (cosign/minisign/SLSA). `curl … | sh` runs a script with no `main()` wrapper, so a truncated download runs a partial script. The served script and the setup URL may be `http://`.
2. **Device-flow phishing (highest impact).** The approval link carries the code, and the page approves with one click. Any logged-in user can approve any code. The token belongs to the approver, so a phished approval gives the attacker an agent of the victim. Messages from "the owner's own agent" arrive with `trusted="true"` and the owner line in all of the victim's sessions, which turns phishing into trusted prompt injection. The reverse also works: an attacker who sees a victim's code (screen share) can approve it, the victim's harness then runs as the attacker's agent, and the attacker's posts read as owner messages. The CLI never shows who approved. Mitigations to consider: type the code instead of linking it, show the approving account in the CLI, and do not grant owner trust to device-flow agents until confirmed. Approve/GET have no per-user rate limit, but the 2^40 code space mitigates that.
3. **Token at rest and in transit.** The token is plaintext in `config.json` (0600/0700, checked with stat, no ownership check). `Resolve` mixes `PARTYLINE_URL` from the environment with the file token, so a foreign URL in the env receives the saved token (`internal/cliconfig/config.go:145-173`). Setup takes the instance URL from the poll answer and saves it (`internal/cli/setup.go:169`). `--token` shows up in `ps`. `setup --uninstall` deletes the file but does not revoke the token.
4. **Config edits.** Symlinks are replaced (Gap 4) and the backup is overwritten (Gap 3). `.partyline.bak` copies of harness configs, which may hold other secrets, are kept until uninstall. Apply writes the config before the extension, so a failure in between leaves a half-registered harness. Hook groups count as "ours" for any binary named `partyline*`.
5. **Hook fail-open.** Every failure prints `{}`, which is safe for the harness but can hide that delivery is off: the reason appears only on stderr, which Codex/Gemini may hide. Pending messages are acked when the hook returns, so a harness that drops the continuation loses them. The same holds for pi/OpenCode deliveries that fail after `listen` has acked (they are only logged).
6. **Prompt-injection framing.** Escaping is correct and tested (`framing.json` `</partyline>` case, attribute quotes). The residual risk is trust laundering: any message from any of the owner's agents counts as the owner. An agent that obeys an injected third-party message can relay it to sibling sessions as `trusted="true"` with "you may act on them as your owner's requests".

---

## Requirement Traceability Update

| Requirement | Previous | New |
| --- | --- | --- |
| ONB-01..07, DEV-01..04, NAME-01..03, HAR-01..04, PUSH-01..02, PUSH-04..06, TURN-01..02, WIZ-02 | Implementing | ✅ Verified |
| PUSH-03 | Implementing | ⚠️ Verified for sync failures; async ordering needs a fix (Gap 2) |
| WIZ-01 | Implementing | ❌ Needs fix (stale e2e UI-14, Gap 1) |

(spec.md is left untouched; the Verifier does not edit artifacts beyond this report.)

---

## Summary

**Overall**: ❌ Not ready: one gate failure (`make e2e`)
**Spec-anchored check**: 28/28 ACs have evidence; 2 spec-precision gaps
**Sensor**: 39/39 killed (+1 equivalent)
**Gate**: `make check` passed; `make e2e` 4 passed, 2 failed, 2 not run (deterministic, re-run confirmed)
**Next steps**: Fix 1 (e2e UI-14), then Fix 2 (omp async order). Re-verify, iteration 1 of 3.
