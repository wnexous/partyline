# Web App Validation

**Date**: 2026-10-19
**Spec**: `.specs/features/web-app/spec.md`
**Diff range**: feature `web/` tree (plus `Makefile` `e2e` target and the CI `e2e` job) at commit `69e5ce0` (HEAD of the verify worktree, `test(web): add playwright smoke suite`)
**Verifier**: independent sub-agent (author ≠ verifier)

**Verdict**: FAIL ❌. Gates are green and 22 of 25 ACs plus all 3 edge cases have spec-anchored evidence. Two requirements have no discriminating test: UI-23 (the system colour scheme) and UI-24 (no horizontal scroll at 360 px). The UI-07 room-page wiring is also untested. The sensor kept 3 real surviving mutants (M17, M18, M19).

---

## Task Completion

| Task | Status | Notes |
| ---- | ------ | ----- |
| T1-T19 | ✅ Done | All marked `[x]` in tasks.md; matching commits in `git log` (`819a140` … `69e5ce0`) |

---

## Spec-Anchored Acceptance Criteria

### P1: Access flows

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| UI-01 no users → setup page | Setup page with setup token, username, password | `web/src/routes/auth.test.tsx:20-24` - heading "Set up partyline", `router.state.location.pathname).toBe("/setup")`, `getByLabelText("Setup token"/"Username"/"Password")`; e2e `web/e2e/smoke.spec.ts:37-38` `toHaveURL(.../setup)` on the real binary | ✅ PASS |
| UI-02 protected route → `/login`, back after login | `/login?redirect=<route>` then the original route | `web/src/routes/app-shell.test.tsx:25-26` - `pathname).toBe("/login")`, `search).toEqual({ redirect: "/rooms" })`; `web/src/routes/auth.test.tsx:82-83` - `pathname).toBe("/agents")`, `search).toEqual({ tab: "mine" })`; open-redirect guard `auth.test.tsx:97-98`; e2e `web/e2e/smoke.spec.ts:54,58` | ✅ PASS |
| UI-03 invite accept form, API rules (3-32 `a-z0-9_-`, password ≥ 10) | Rejects invalid input before POST; accepts valid input | `web/src/lib/schemas.test.ts:14-35` - rejects `ab`, `UPPER`, 33 chars, `dots.no`; accepts 32 chars; `"123456789"` → `validation.passwordMin`, 10 chars ok; `web/src/routes/auth.test.tsx:138-152` - error texts shown, `calls.some(POST)).toBe(false)`, then body `{username:"bia", password:"1234567890"}`; expired invite `auth.test.tsx:117-121` | ✅ PASS |

### P1: Live rooms

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| UI-04 `room.message` appended without refetch | Message in timeline; no extra fetch | `web/src/lib/live/handlers.test.ts:47-48` - `items.at(-1)).toEqual(m)`, seqs `[1,2,3]`; dedupe `:63`; `web/src/components/message-timeline.test.tsx:209-211` - visible, `calls.length).toBe(fetches)`; e2e `web/e2e/smoke.spec.ts:118-120` - second user sees it, same document, `timelineFetches).toEqual([])` | ✅ PASS |
| UI-05 author kind icon, name, time, trust badge, reply, attachments | Each element rendered | `message-timeline.test.tsx:67-75` (Untrusted/Trusted/Verified, none for agent/user), `:90-99` (kind label, name, `time[datetime]`), `:112-117` (reply context), `:135-146` (inline png `loading=lazy`, file link `download`, svg never inline) | ✅ PASS (trust derived from `author_user_id`, documented DTO gap) |
| UI-06 "reconnecting" indicator while disconnected; refetch after last seq | Banner while not open > 2 s; `after_seq=lastSeq` fetch merged | `web/src/routes/app-shell.test.tsx:113-131` - no status at 1 999 ms, `toHaveTextContent("Connection lost. Reconnecting…")` at 2 000 ms, gone on open; `web/src/lib/live/socket.test.ts:156-164` - seqs `[59,60,61,62]`, `urls).toContain(".../messages?after_seq=60&limit=50")` | ✅ PASS |
| UI-07 composer with `post`, read-only notice otherwise | `view` → notice; `post`/`manage` → composer | `web/src/components/composer.test.tsx:102-113` - `queryByRole("textbox")).toBeNull()` + notice text for `view`; textbox for `manage`. Enter/Shift+Enter/max length `:54,64-65,84-88,97-99` | ⚠️ PASS at component level; room page passing `my_level` is untested (M18 survived) |
| UI-08 loop-guard banner with "resume agents" for managers | Banner; button only for managers; POST resume | `web/src/routes/_app/rooms/room.test.tsx:188-193` (banner, POST `/rooms/r1/resume`, banner gone), `:199-200` (no button for `post`), `:207` (hidden while running) | ✅ PASS |
| UI-09 agent Markdown without raw HTML | `<script>` rendered as text; no unsafe links | `message-timeline.test.tsx:172-184` - `strong.tagName).toBe("STRONG")`, `toHaveTextContent("<script>…")`, `querySelector("script")).toBeNull()`, `javascript:` href stripped, remote image not loaded; chat text `:155-157` | ✅ PASS |

### P1: Rooms management and sharing

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| UI-10 member picker: usable agents/chats only, online state, text filter | Non-usable or existing members hidden; filter narrows the list | `room.test.tsx:139-153` - a2 "Offline", a3 "Online", a1/c1 (members) and a9 (`view`) absent, filter "SUPP" keeps only c4; add body `{kind:"chat", member_id:"c3"}` `:174-175` | ✅ PASS |
| UI-11 visibility private/shared/public + who can access | Exact `dto.Sharing` payloads; summary | `web/src/components/sharing-dialog.test.tsx:70-73` (summary), `:87` `{mode:"private",users:[]}`, `:112-118` shared users+levels, `:159` `{mode:"public",users:[],public_level:"post"}`, `:176,186` `use` level | ✅ PASS |
| UI-12 toggle chat member `outbound` | PATCH `{outbound:false}`; switch updates | `room.test.tsx:97-102` - `toMatchObject({ path: "/rooms/r1/members/mb-ops", body: { outbound: false } })`, `not.toBeChecked()` | ✅ PASS |
| UI-13 "Route chat to agent" in one action | One call creates the room; navigate there | `web/src/routes/_app/connectors/connectors.test.tsx:226-230` - `pathname).toBe("/rooms/r7")`, body `{ agent_id: "a1" }` | ✅ PASS (private room and membership are server-side, outside this feature) |

### P1: Agents, connectors, identities and tokens

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| UI-14 connect wizard: agent token, `.mcp.json` + launch command with instance URL | POST token kind agent; snippet has URL, token, name | `web/src/routes/_app/agents.test.tsx:160-178` - body `{kind:"agent",name:"build-bot"}`, `env).toEqual({PARTYLINE_URL: window.location.origin, PARTYLINE_TOKEN: SECRET, PARTYLINE_AGENT:"build-bot"})`, launch command string; e2e `web/e2e/smoke.spec.ts:144-159` (real origin, real token prefix) | ⚠️ Spec-precision gap: the launch command has no URL; only the `.mcp.json` does. The spec wording is ambiguous |
| UI-15 agents with live online/offline and last seen | State flips on presence; last-seen time shown | `agents.test.tsx:62-65` (Online/Offline/`/^Last seen /`/"Never connected"), `:83-84` live flip; `handlers.test.ts:95-104` | ⚠️ PASS; the last-seen *value* is not asserted (only the prefix) |
| UI-16 connector form from schema, secret → password | `type="password"` for secret fields | `web/src/components/schema-form.test.tsx:29-33` - `toHaveAttribute("type", "password")`, url/text/switch/tag; `connectors.test.tsx:117,139`; payload `:125-133` | ✅ PASS |
| UI-17 connector live status and last error | Status and error shown, live updates | `connectors.test.tsx:58-60` (Connected/Error/"session logged out"), `:78-79` live, `:186` detail; `handlers.test.ts:134-141` | ✅ PASS |
| UI-18 pairing code, command, countdown; update on `identity.linked` | Code, `!link CODE`, timer, success + refetch | `web/src/routes/_app/identities.test.tsx:88-95` (`K7P2QX`, `!link K7P2QX`, timer `10:00` → `8:59`), `:116` expired, `:145-147` success + list refetch, `:164` stale event ignored | ✅ PASS |
| UI-19 token secret once, copy button, not-shown-again warning | Secret visible once; warning; then prefix only | `web/src/routes/_app/tokens.test.tsx:89-98` - secret text, `/won’t be shown again/`, row shows `pl_user_Q8zt`, `body.textContent).not.toContain(SECRET)` | ⚠️ PASS; the copy button is only exercised through the same `SecretOnceDialog` on invites (`admin.test.tsx:173-176`), not on tokens |

### P1: Admin

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| UI-20 Admin: users (enable/disable, role), invites (create with role, copy link, revoke) | PATCH role/disabled; POST role; link copied; DELETE | `web/src/routes/_app/admin/admin.test.tsx:106` `{role:"admin"}`, `:132,137` `{disabled:true}`/`{disabled:false}`, `:172` `{role:"admin"}`, `:175-176` clipboard `toBe(URL)` + `/invite/<token>`, revoke DELETE `:199-204`; admin nav `app-shell.test.tsx:51-54` | ✅ PASS |
| UI-21 non-admin on admin route → 403 | 403 page, no admin API calls | `admin.test.tsx:66-67` (three routes) - "Admins only", `calls.some(c => c.path.startsWith("/admin"))).toBe(false)`; `app-shell.test.tsx:60-61` | ✅ PASS |

### P2: Polish

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| UI-22 Ctrl/Cmd+K palette to rooms, agents, connectors | Opens with both shortcuts, filters, Enter navigates | `web/src/components/command-menu.test.tsx:43-55` (Ctrl and Meta), `:68` filter, `:88` `/rooms/r2`, connectors/agents/admin jumps `:109-111` | ✅ PASS |
| UI-23 follows the system scheme; light/dark override remembered | `system` resolves from `prefers-color-scheme`; override in localStorage | Override: `web/src/routes/_app/settings.test.tsx:52-55`, `app-shell.test.tsx:91-92`. **System-following: no test**; M17 (`system` → always light) survived the full suite | ❌ GAP |
| UI-24 usable from 360 px, no horizontal page scroll | No horizontal overflow at 360 px | The e2e `mobile-360` project runs the smoke flows (`web/playwright.config.ts`), but nothing asserts `scrollWidth <= clientWidth` or otherwise checks for horizontal scroll | ❌ GAP (no evidence for the stated outcome) |
| UI-25 en and pt-BR for every screen | Key parity; strings switch | `web/src/lib/i18n/i18n.test.tsx:19,24,30-34` - `dictionaryDiff(...)).toEqual({missing:[],extra:[]})`, placeholders equal, missing/empty detected; `settings.test.tsx` pt-BR heading; `a11y.test.tsx` both locales | ✅ PASS (parity proves keys; a grep found no hardcoded English JSX literals outside `components/ui`) |

**Status**: ❌ Gaps present (UI-23, UI-24), plus ⚠️ precision gaps (UI-07 page wiring, UI-14, UI-15, UI-19).

---

## Edge Cases

- [x] 401 during use → login with the current route kept: `web/src/routes/app-shell.test.tsx:72-73` - `pathname).toBe("/login")`, `search).toEqual({ redirect: "/agents?tab=mine" })`.
- [x] Live event for a room not in the cache is ignored: `web/src/lib/live/handlers.test.ts:71` - `getQueryData(roomMessages("elsewhere"))).toBeUndefined()`.
- [x] Zero messages → empty state explaining how to add members: `web/src/components/message-timeline.test.tsx:247-248` - heading "No messages yet", `/Add agents and chats from Members/`.

---

## Discrimination Sensor

Scratch: `git worktree add --detach /tmp/claude-1001/pl-web-mutants 69e5ce0`, with `web/node_modules` symlinked. Each mutant ran the targeted test files, and every survivor was re-run against the full suite (201 tests). The scratch was removed with `git worktree remove --force`, followed by `git worktree prune`. The verify worktree's `git status --porcelain` matches the pre-sensor baseline (empty).

| # | File | Description | Killed? |
| - | ---- | ----------- | ------- |
| M1 | `web/src/lib/live/handlers.ts:36` | `room.message` dedupe removed (`fresh = incoming`) | ✅ Killed (`handlers.test.ts` "does not duplicate") |
| M2 | `web/src/lib/live/handlers.ts:36` | Dedupe by seq only (id check dropped) | ➖ Survived; equivalent under contract (seq is unique per room) |
| M2b | `web/src/lib/live/handlers.ts:36` | Dedupe by id only (seq check dropped) | ➖ Survived; equivalent under contract (one id per message) |
| M3 | `web/src/lib/live/handlers.ts:203` | `lagged` no longer invalidates | ✅ Killed |
| M4 | `web/src/lib/live/handlers.ts:236` | Reconnect catch-up uses `after_seq: 0` instead of last seq | ✅ Killed (`socket.test.ts` reconnect) |
| M5 | `web/src/routes/_app.tsx:25` | Guard redirect drops `redirect` search param | ✅ Killed |
| M6 | `web/src/routes/__root.tsx:34` | 401 handler keeps pathname only (query lost) | ✅ Killed |
| M7 | `web/src/components/message-item.tsx:79` | Unverified chat sender shown as "verified", not "untrusted" | ✅ Killed |
| M8 | `web/src/components/markdown.tsx:36` | Identity `urlTransform` (keeps `javascript:`) | ✅ Killed |
| M9 | `web/src/components/composer.tsx:81` | Max length `>` → `>=` | ✅ Killed |
| M10 | `web/src/components/composer.tsx:108` | Shift+Enter sends | ✅ Killed |
| M11 | `web/src/components/composer.tsx:92` | Sends untrimmed text | ✅ Killed |
| M12 | `web/src/components/sharing-dialog.tsx:96` | Public payload drops `public_level` | ✅ Killed |
| M13 | `web/src/components/sharing-dialog.tsx:94` | Shared payload ignores chosen level | ✅ Killed |
| M14 | `web/src/routes/_app/admin/route.tsx:14` | Admin 403 guard removed | ✅ Killed (4 tests) |
| M15 | `web/src/lib/i18n/pt-BR.ts:10` | pt-BR key removed | ✅ Killed (parity) |
| M16 | `web/src/components/schema-form.tsx:259` | Secret field rendered as `text` | ✅ Killed |
| M17 | `web/src/lib/theme.ts:36` | `system` preference ignores `prefers-color-scheme` (always light) | ❌ Survived → fix task 1 |
| M18 | `web/src/routes/_app/rooms/$roomId.tsx:120` | Room page passes `level="post"` (ignores `my_level`) | ❌ Survived → fix task 2 |
| M19 | `web/src/lib/live/socket.ts:61` | Backoff not reset after a successful open | ❌ Survived → fix task 4 (design-level, not a spec AC) |
| M20 | `web/src/hooks/use-disconnected.ts:5` | Reconnecting banner grace 2 s → 0 | ✅ Killed |

**Sensor depth**: standard-plus (21 behaviour-level mutations, targeted files plus full-suite re-run of survivors)
**Result**: 16/21 killed, 2 equivalent, 3 real survivors - FAIL ❌

---

## Gate Check

- **Lint**: `cd web && bun run lint` - Biome, 116 files, no findings.
- **Typecheck**: `cd web && bun run typecheck` - exit 0.
- **Unit**: `cd web && bun run test` - 25 files, **201 passed, 0 failed, 0 skipped**. The first run on a loaded host (load average about 14) failed 2 tests at `web/src/routes/_app/rooms/rooms.test.tsx:34` (`findAllByRole` default 1 s timeout). Three re-runs were all green (201/201), so this is load-sensitive flakiness, not a regression.
- **E2E**: `PLAYWRIGHT_CHROMIUM_EXECUTABLE=~/.cache/ms-playwright/chromium_headless_shell-1243/.../chrome-headless-shell make e2e` - built binary, **6 passed** (3 tests × `desktop-1280`, `mobile-360`), 7.2 s.
- **Test count before feature**: 0 (no `web/` before this feature). **After**: 201 unit + 6 e2e.
- **Skipped tests**: none.
- CI: the `e2e` job in `.github/workflows/ci.yml:30-53` runs `make web-install`, `playwright install --with-deps chromium`, `make e2e`, and uploads traces on failure.

---

## Code Quality

| Principle | Status |
| --- | --- |
| Minimum code / surgical changes / no scope creep | ✅ (dashboard activity feed is a spec'd T16 item; SPEC_DEVIATION recorded in `queries.ts` for the missing `/activity` endpoint) |
| Matches patterns (conventions.md) | ✅ |
| Spec-anchored outcome check | ⚠️ UI-23, UI-24 lack evidence; UI-07/14/15/19 precision gaps |
| Per-layer coverage (lib 1:1; pages happy+edge+error) | ⚠️ pages strong; theme lib (`resolveTheme`) has no unit test |
| Every test maps to an AC / edge / done-when | ✅ (settings/audit/sessions tests map to T14/T15 done-when) |
| Documented guidelines followed: `AGENTS.md`, `.specs/features/web-app/conventions.md`, `web-design-guidelines` | ✅ with notes below |

### UI checklist (web-design-guidelines, pinned rules)

```
web/src/components/message-item.tsx:393 - inline attachment <img> has no width/height (CLS while the image loads)
web/src/components/live-status.tsx:39 - role=status/aria-live region mounts only when disconnected; some screen readers skip live regions inserted with content
web/src/routes/login.tsx:78, setup.tsx:63, invite.$token.tsx:120, create-room-dialog.tsx:78, connect-agent-dialog.tsx:133, route-chat-dialog.tsx:82 - autoFocus also on mobile (opens the keyboard); acceptable for single-input forms, check on phones
web/src/components/composer.tsx:171, schema-form.tsx:353 - outline-none on inputs; ok, the wrapper has a focus-within ring
web/src/routes/_app.tsx:61 - outline-none on <main tabIndex=-1>; ok, it is a skip-link target, not interactive
```

No `transition-all`, no zoom blocking (`index.html:5`), no `onPaste` blocking, no clickable `div`/`span`, dates and numbers go through `Intl.*` (`lib/time.ts`), `color-scheme` and `theme-color` metas are present, and `prefers-reduced-motion` is handled (`index.css:235`). axe has no violations on the shell, room and login pages in both locales (`web/src/routes/a11y.test.tsx:84,102,109`; colour contrast is excluded because happy-dom has no layout).

### Security notes (in passing)

- Markdown: no rehype-raw, default `urlTransform` (M8 proves the test guards it), links use `noopener noreferrer nofollow`, and remote images become links. SVG attachments are never inlined.
- `safeRedirect` (`web/src/lib/forms.ts:38-46`) rejects `//host` and `/\` and non-absolute targets; covered at `auth.test.tsx:90-99`.
- Composer length uses UTF-16 `text.length`, while the server counts runes (`internal/rooms/messages.go:131`). The client is stricter for astral characters such as emoji, so it can refuse text the server would accept. This is not a safety issue.
- `detectLocale(["en-US","pt-BR"])` returns `pt-BR` (`web/src/lib/i18n/i18n.test.tsx:49`): a browser that prefers English but lists Portuguese gets Portuguese. The spec says "Browser language"; confirm this is intended.

---

## Fix Plans

### Fix 1: UI-23 system scheme has no discriminating test (M17)
- **Root cause**: `resolveTheme("system")` / `systemPrefersDark()` and the `matchMedia` listener in `components/app-providers.tsx:13-14` are never exercised.
- **Fix task**: unit test in `web/src/lib/theme.test.ts` (or the settings/app-shell suite) that stubs `window.matchMedia` with `matches: true`, sets the preference to `system`, and asserts the `dark` class on `<html>`. Also cover the light case and a live `change` event.
- **Priority**: Major

### Fix 2: UI-07 room page does not prove it gates the composer by `my_level` (M18)
- **Fix task**: in `web/src/routes/_app/rooms/room.test.tsx`, `roomApi({ my_level: "view" })` → assert `queryByRole("textbox", { name: /Message/ })` is null and the read-only notice is shown.
- **Priority**: Major

### Fix 3: UI-24 no evidence of "no horizontal page scroll" at 360 px
- **Fix task**: in `web/e2e/smoke.spec.ts` (the `mobile-360` project), on the overview, rooms, room and agents pages assert `document.documentElement.scrollWidth <= window.innerWidth`.
- **Priority**: Major

### Fix 4: backoff reset after a successful open is untested (M19)
- **Fix task**: in `web/src/lib/live/socket.test.ts`, after a successful reopen, drop the socket again and assert the next retry fires at 1 000 ms, not 4 000 ms.
- **Priority**: Minor

### Fix 5 (precision, optional): UI-15 / UI-19 / UI-14
- UI-15: assert the formatted last-seen value (fake `Date`), not only the `Last seen` prefix.
- UI-19: click "Copy" in the tokens secret dialog and assert `navigator.clipboard.readText()` equals the secret.
- UI-14: decide in the spec whether "launch command containing the instance URL" means the `.mcp.json` only (as now) and reword the AC.
- **Priority**: Minor

---

## Requirement Traceability Update (proposed; spec.md not edited by the Verifier)

| Requirement | Previous | New |
| --- | --- | --- |
| UI-01..UI-06, UI-08..UI-13, UI-16..UI-18, UI-20..UI-22, UI-25 | Implementing | ✅ Verified |
| UI-07, UI-14, UI-15, UI-19 | Implementing | ✅ Verified (precision notes) |
| UI-23 | Implementing | ❌ Needs Fix (Fix 1) |
| UI-24 | Implementing | ❌ Needs Fix (Fix 3) |

---

## Summary

**Overall**: ❌ Not Ready. The work is close: four small test-only fix tasks remain, and no product defect was found.

**Spec-anchored check**: 22/25 ACs with spec-anchored evidence. 2 ACs have no evidence (UI-23, UI-24). There are 4 precision gaps (UI-07 page wiring, UI-14, UI-15, UI-19). All 3 edge cases are covered.
**Sensor**: 16/21 killed, 2 equivalent, 3 survived (M17, M18, M19)
**Gate**: lint ✅, typecheck ✅, unit 201/201 ✅ (1 load-flaky run), e2e 6/6 ✅

**What works**: the live pipeline (append/dedupe, lagged, reconnect catch-up), the auth guard and 401 return path, trust badges, safe Markdown, the composer, sharing payloads, the admin guard, i18n parity, the schema form and the real-binary smoke suite at both widths.

**Next steps**: run Fixes 1-4 as test-only tasks, then re-verify.
