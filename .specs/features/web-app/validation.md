# Web App Validation

**Date**: 2026-10-20
**Spec**: `.specs/features/web-app/spec.md`
**Diff range**: feature `web/` tree plus the `Makefile` `e2e` target and the CI `e2e` job, at `a6de457` (`test(web): use an unbreakable token in the overflow e2e fixture`). The fix surface since iteration 1 is `4127774..a6de457` (T20-T32 plus the iteration-2 fixture fix).
**Verifier**: independent sub-agent (author ≠ verifier), iteration 3 of 3

**Verdict**: PASS ✅. All gates are green: lint, typecheck, 216/216 unit tests and 8/8 e2e tests. All 25 ACs and all 3 edge cases have spec-anchored `file:line` evidence. The iteration-2 survivor E1 is now killed. The sensor ran 7 fresh mutants: 5 were killed, and the other 2 (F4, F6) are equivalent. They are two redundant write paths for the same value, and the combined mutant is killed.

---

## Iteration history

| Iteration | Verdict | Gaps | Fix |
| --- | --- | --- | --- |
| 1 (`69e5ce0`) | FAIL ❌ | UI-23 (M17) and UI-24 had no evidence. M18 survived (the room page did not gate UI-07) and so did M19 (backoff reset). There were also precision notes for UI-14, UI-15 and UI-19, plus UI notes | Tasks T20-T32 (`28617e6` … `e124928`), recorded in `830d152` |
| 2 (`830d152`) | FAIL ❌ | All 25 ACs and 3 edge cases had evidence. Sensor: 17 injected, 15 killed, 1 equivalent (N7), 1 survived. The survivor, E1, removed `break-words` from the message text and was not caught because the UI-24 fixture used a hyphenated token that wraps at each hyphen | `a6de457`: the fixture now uses an unbreakable token |
| 3 (`a6de457`) | PASS ✅ | none | - |

---

## Task Completion

| Task | Status | Notes |
| ---- | ------ | ----- |
| T1-T19 | ✅ Done | All `[x]` in tasks.md |
| T20-T32 (Phase 5, iteration-1 fixes) | ✅ Done | No unchecked `[ ]` box remains in tasks.md |
| iteration-2 fix (E1) | ✅ Done | Commit `a6de457`. It has no task entry in tasks.md, which is a process note only |

---

## Spec-Anchored Acceptance Criteria

### P1: Access flows

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| UI-01 no users → setup page | Setup page with setup token, username, password | `web/src/routes/auth.test.tsx:20-22`: heading "Set up partyline", `pathname).toBe("/setup")`, `getByLabelText("Setup token")`. e2e on the real binary: `web/e2e/smoke.spec.ts:37`, `toHaveURL(\`${instance.url}/setup\`)` | ✅ |
| UI-02 protected route → login → back | `/login?redirect=<route>`, then the original route | `web/src/routes/app-shell.test.tsx:25-26`: `toBe("/login")`, `search).toEqual({ redirect: "/rooms" })`. `web/src/routes/auth.test.tsx:82-83`: `toBe("/agents")`, `{ tab: "mine" }`. e2e: `web/e2e/smoke.spec.ts:54,58` | ✅ |
| UI-03 invite form, API rules | Username 3-32 `a-z0-9_-`, password ≥ 10, rejected before the POST | `web/src/lib/schemas.test.ts:14` rejects `ab`, `UPPER`, 33 chars and `dots.no`. `:29` gives `"validation.passwordMin"`. `web/src/routes/auth.test.tsx:142`: `calls.some(c => c.method === "POST")).toBe(false)`. `:151`: the body carries `password: "1234567890"` | ✅ |

### P1: Live rooms

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| UI-04 `room.message` appended, no refetch | In the timeline, no fetch | `web/src/lib/live/handlers.test.ts:47-48`: `items.at(-1)).toEqual(m)`, seqs `[1, 2, 3]`. `web/src/components/message-timeline.test.tsx:231`: `calls.length).toBe(fetches)`. e2e: `web/e2e/smoke.spec.ts:62` (second user, live) | ✅ |
| UI-05 kind icon, name, time, trust badge, reply, attachments | Each element rendered | `message-timeline.test.tsx:67,70,74` (Untrusted, and no badge for agents or users), `:95` `time` with `datetime="2026-10-19T12:34:00Z"`, `:137` `loading="lazy"`, `:140,143` `download` (SVG never inline) | ✅ |
| UI-06 reconnecting indicator; refetch after last seq | Banner while disconnected; `after_seq=<last>` | `web/src/routes/app-shell.test.tsx:109,119`: no banner at 1 999 ms, "Connection lost. Reconnecting…" after that. `web/src/lib/live/socket.test.ts:187,190`: seqs `[59, 60, 61, 62]`, `urls).toContain(".../messages?after_seq=60&limit=50")` | ✅ |
| UI-07 composer with `post`, read-only notice otherwise | `view` → notice, no textbox; `post` → composer | Page level (iteration-1 fix): `web/src/routes/_app/rooms/room.test.tsx:182-185`, `my_level: "view"` → `/You can read this room but not post/` and `queryByRole("textbox", …)).toBeNull()`. `:189-192` `post` → textbox shown, no notice. Component: `web/src/components/composer.test.tsx:132-142` | ✅ |
| UI-08 loop-guard banner, resume for managers | Banner; button for managers only; POST resume | `room.test.tsx:205-207`: click "Resume agents" → `POST /rooms/r1/resume`. `:216`: no button for non-managers | ✅ |
| UI-09 Markdown without raw HTML | `<script>` as text; no `javascript:` | `message-timeline.test.tsx:192` `tagName).toBe("STRONG")`, `:193` text contains `<script>…`, `:194` `querySelector("script")).toBeNull()`, `:202` href `not.toMatch(/javascript:/i)` | ✅ |

### P1: Rooms management and sharing

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| UI-10 picker: usable only, online state, filter | Non-usable and existing members hidden; text filter | `room.test.tsx:141` "Offline", `:148` `candidate-agent-a9` (`view`) `toBeNull()`, `:150` filter "SUPP", `:175` add body `{ kind: "chat", member_id: "c3" }` | ✅ |
| UI-11 private / shared / public + who can access | Exact `Sharing` payloads; summary region | `web/src/components/sharing-dialog.test.tsx:69,83,107` region "Who can access". `:87` `{ mode: "private", users: [] }`. `:159` `{ mode: "public", users: [], public_level: "post" }` | ✅ |
| UI-12 toggle `outbound` | PATCH `{outbound:false}`; switch updates | `room.test.tsx:94` `toBeChecked()`, `:99` `body: { outbound: false }`, `:102` `not.toBeChecked()` | ✅ |
| UI-13 Route chat to agent, one action | One call; navigate to the new room | `web/src/routes/_app/connectors/connectors.test.tsx:227` `pathname).toBe("/rooms/r7")`, `:229` `agent_id: "a1"` | ✅ |

### P1: Agents, connectors, identities and tokens

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| UI-14 agent token, `.mcp.json` with instance URL and token, launch command | POST kind agent; snippet env; command | `web/src/routes/_app/agents.test.tsx:196` `kind: "agent"`, `:203` `PARTYLINE_URL: window.location.origin`, `:205` `PARTYLINE_AGENT`, `:208` launch command. e2e on the real binary: `web/e2e/smoke.spec.ts:146` `PARTYLINE_URL).toBe(instance.url)`, `:147` token `^pl_agent_…`, `:159` prefix matches the created token. The AC was reworded in `0d52b5f` | ✅ |
| UI-15 live online/offline + last seen | State flips; last-seen value | `agents.test.tsx:62-65` Online/Offline/"Never connected", `:87` `"Last seen 3 hr. ago"`, `:95` `"Last seen 5 min. ago"` after a presence event, `:108,116` live flips | ✅ |
| UI-16 schema form, secret → password | `type="password"` | `web/src/components/schema-form.test.tsx:29` and `connectors.test.tsx:117,139` `toHaveAttribute("type", "password")` | ✅ |
| UI-17 connector live status + last error | Status and error text | `connectors.test.tsx:57` "Connected", `:59` "Error", `:60` "session logged out" | ✅ |
| UI-18 code, command, countdown; `identity.linked` | `K7P2QX`, `!link K7P2QX`, timer, success | `web/src/routes/_app/identities.test.tsx:88,91` (`pre` text `toBe("!link K7P2QX")`), `:93,95` timer `10:00` → `8:59`, `:116` expired, `:145` "Ana (phone) is linked", `:164` stale link ignored | ✅ |
| UI-19 secret once, copy button, warning | Secret shown; warning; clipboard = secret; then prefix only | `web/src/routes/_app/tokens.test.tsx:93` `/won’t be shown again/`, `:94-95` click "Copy ci" → `clipboard.readText()).toBe(SECRET)`, `:99` the row shows the prefix, `:100` `body.textContent).not.toContain(SECRET)` | ✅ |

### P1: Admin

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| UI-20 users enable/disable and role; invites create, copy, revoke | Exact PATCH/POST/DELETE; link copied | `web/src/routes/_app/admin/admin.test.tsx:106` `{ role: "admin" }`, `:132` `{ disabled: true }`, `:174` the clipboard holds the `/invite/<token>` URL, `:200` `DELETE /admin/invites/inv1` | ✅ |
| UI-21 non-admin → 403 | 403 page, no admin calls | `admin.test.tsx:66-67` "Admins only" + `calls.some(c => c.path.startsWith("/admin"))).toBe(false)`. `app-shell.test.tsx:60` | ✅ |

### P2: Polish

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| UI-22 Ctrl/Cmd+K palette | Both shortcuts; jump to rooms, agents and connectors | `web/src/components/command-menu.test.tsx:42,54` Control and Meta, `:88` `toBe("/rooms/r2")`, `:109-110` jumps to `/connectors/k1` and `/agents` | ✅ |
| UI-23 follows the system scheme; override remembered | `system` follows `prefers-color-scheme`, live; override wins and persists | `web/src/components/app-providers.test.tsx:43` dark when the system is dark, `:51` light, `:58-62` follows a live `change`, `:69,72` an override beats the system. `web/src/routes/_app/settings.test.tsx:52` `localStorage.getItem(THEME_KEY)).toBe("dark")` | ✅ |
| UI-24 usable from 360 px, no horizontal scroll | `scrollWidth ≤ innerWidth` on main pages; a long line wraps | `web/e2e/smoke.spec.ts:205` `width.scroll … toBeLessThanOrEqual(width.viewport)` on 6 pages, and `:197` the right edge of a long unbreakable line is ≤ `innerWidth`. Runs in both projects (`web/playwright.config.ts`, 360 and 1280) | ✅ |
| UI-25 en + pt-BR for every screen | Key parity | `web/src/lib/i18n/i18n.test.tsx:19` `dictionaryDiff(en, …)).toEqual({ missing: [], extra: [] })`, `:30-31` missing and empty keys detected, `:47-49` locale order | ✅ |

**Status**: ✅ All 25 ACs covered with spec-anchored assertions. No open spec-precision gaps.

---

## Edge Cases

- [x] 401 during use → login keeps the route: `web/src/routes/app-shell.test.tsx:72-73`, `toBe("/login")` and `{ redirect: "/agents?tab=mine" }`.
- [x] Live event for a room not in the cache is ignored: `web/src/lib/live/handlers.test.ts:71`, `getQueryData(roomMessages("elsewhere"))).toBeUndefined()`.
- [x] Zero messages → empty state: `web/src/components/message-timeline.test.tsx:267-268`, "No messages yet" and `/Add agents and chats from Members/`.

---

## Discrimination Sensor

Scratch: `git worktree add --detach /tmp/claude-1001/pl-web-mutants3 a6de457`, with `web/node_modules` symlinked. For the e2e mutants, each run rebuilt the SPA and the binary in the scratch (`bun run build`, copy into `internal/webui/dist`, `go build`), then ran Playwright with `PARTYLINE_BIN` set to the scratch binary. Unit mutants ran the targeted vitest files, and each survivor was re-run against the full suite. Each mutant was reverted with `git checkout` before the next. Cleanup: `git worktree remove --force` and `git worktree prune`. The real tree's `git status --porcelain` matches the pre-sensor baseline (empty).

| # | File:line | Description | Killed? |
| - | --------- | ----------- | ------- |
| E1 (re-apply) | `web/src/components/message-item.tsx:261` | `break-words` dropped from the message text | ✅ Killed. `smoke.spec.ts:197` "long message runs off screen" fails in both `desktop-1280` and `mobile-360` |
| F5 | `web/src/routes/_app.tsx:61` | `min-w-0` → `min-w-[420px]` on `<main>` (the page overflows at 360 px) | ✅ Killed. `smoke.spec.ts:205` "/ scrolls sideways" fails in `mobile-360` |
| F1 | `web/src/lib/theme.ts:36` | `resolveTheme` ignores an explicit light/dark override (always follows the system) | ✅ Killed by `app-providers.test.tsx` "keeps an explicit override" and `app-shell.test.tsx` theme switch |
| F2 | `web/src/lib/schemas.ts:105` | `textLength` counts UTF-16 units (`text.length`) instead of code points | ✅ Killed by 2 tests in `composer.test.tsx` (Unicode counting, one over the max) |
| F3 | `web/src/routes/_app/identities.tsx:216` | Pairing dialog accepts an identity linked before it opened (baseline check removed) | ✅ Killed by `identities.test.tsx:152-164` |
| F4 | `web/src/lib/live/handlers.ts:139` | `agent.presence` cache patch keeps the old `last_seen_at` | ➖ Equivalent: the full suite passes (216/216), because the UI reads `withPresence` (`web/src/lib/presence.ts:14`), which takes the live store's value first |
| F6 | `web/src/lib/presence.ts:14` | `withPresence` ignores the live store's `last_seen_at` | ➖ Equivalent: the targeted suite passes, because the handler's cache patch (`handlers.ts:139`) carries the same value. **F4 + F6 together are killed** by `agents.test.tsx` "shows the last seen time … follows presence events" (`:95`), so the behaviour is guarded and only the redundancy goes untested |

**Sensor depth**: lightweight-plus (1 re-applied survivor and 6 fresh behaviour-level mutations, 2 of them at e2e level against the real binary)
**Result**: 5/7 killed, 2 equivalent (redundant dual write), 0 real survivors - PASS ✅

---

## Gate Check

- **Lint**: `cd web && bun run lint` (Biome): 120 files, no findings.
- **Typecheck**: `cd web && bun run typecheck`: exit 0. A harmless Node warning about `replaceRouteChunk` from `tsr generate` was printed. Porcelain is unchanged afterwards.
- **Unit**: `cd web && bun run test`: 27 files, **216 passed, 0 failed, 0 skipped**, green on the first run (load average about 14-16).
- **E2E**: `PLAYWRIGHT_CHROMIUM_EXECUTABLE=~/.cache/ms-playwright/chromium_headless_shell-1243/chrome-headless-shell-linux64/chrome-headless-shell make e2e` (the variable `web/playwright.config.ts:9` reads): binary built at `a6de457`, **8 passed** (4 tests × `desktop-1280`, `mobile-360`), 34.8 s.
- **Test count**: before the feature, 0. After iteration 1, 201 unit + 6 e2e. Now 216 unit + 8 e2e (+15 unit, +2 e2e from the fix tasks). No test was deleted or weakened.
- **Skipped tests**: none.

---

## Code Quality

| Principle | Status |
| --- | --- |
| Minimum code / surgical changes / no scope creep | ✅ Fix commits touch only the files their tasks name (`git diff --stat 4127774..a6de457 -- web`: 24 files, test-heavy). Product changes (T27-T31) are small and each is covered by a test |
| Matches patterns (`.specs/features/web-app/conventions.md`) | ✅ |
| Spec-anchored outcome check | ✅ 25/25 |
| Per-layer coverage (lib 1:1; pages happy + edge + error; e2e for layout) | ✅ The theme lib is now covered (`app-providers.test.tsx`). UI-24 is covered at e2e, where the layout is real |
| Every test maps to an AC, edge case or done-when | ✅ The autofocus tests (`web/src/routes/autofocus.test.tsx`) map to T31, which serves UI-24 |
| Documented guidelines followed: `AGENTS.md`, `conventions.md`, `web-design-guidelines` | ✅ The iteration-1 UI notes were addressed: the image box (`message-item.tsx:69,402`), the always-mounted live region (`live-status.tsx`), coarse-pointer autofocus, code-point length, and locale order |

Observation (not a gap): agent `last_seen_at` is written twice, to the query cache (`handlers.ts:139`) and to the live presence store, and is read through `withPresence`. The sensor shows each write alone is redundant. Removing one write path is a simplification candidate, not a defect.

---

## Requirement Traceability Update (proposed; spec.md not edited by the Verifier)

| Requirement | Previous | New |
| --- | --- | --- |
| UI-01 … UI-22, UI-25 | ✅ Verified | ✅ Verified (re-confirmed) |
| UI-23 | Fixed - re-verify (T20) | ✅ Verified |
| UI-24 | Fixed - re-verify (T22, T31) | ✅ Verified |

---

## Summary

**Overall**: ✅ Ready

**Spec-anchored check**: 25/25 ACs and 3/3 edge cases have spec-anchored evidence. No precision gaps remain.
**Sensor**: 5/7 killed, 2 equivalent (redundant dual write, combined mutant killed), 0 survivors. The iteration-2 survivor E1 is killed.
**Gate**: lint ✅, typecheck ✅, unit 216/216 ✅, e2e 8/8 ✅

**What works**: the live pipeline, the auth guard and 401 return, room-page access gating, the system scheme with override, the no-horizontal-scroll layout at 360 px with real long-token wrapping, Unicode-correct length limits, safe Markdown, sharing payloads, the admin guard, i18n parity and the real-binary smoke suite.

**Next steps**: update the spec traceability for UI-23 and UI-24 to ✅ Verified. Optionally add the iteration-2 fix to tasks.md for the record.
