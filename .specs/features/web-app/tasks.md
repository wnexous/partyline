# Web App Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Design**: `.specs/features/web-app/design.md`
**Status**: Approved
**Depends on feature**: core-platform T7 (DTO contract + generated types). Runs against a mocked API in unit tests; e2e (T19) needs all backend features.

---

## Test Coverage Matrix

> Guidelines found: `AGENTS.md` (repo) - strong defaults applied.

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| lib (api client, live handlers, schemas, i18n) | unit | All branches; 1:1 ACs | `web/src/lib/**/*.test.ts` | `cd web && bun run test` |
| Components and pages | unit (Vitest + Testing Library, fetch mocked) | Each AC visible behavior: render, interaction, error state | `web/src/**/*.test.tsx` | `cd web && bun run test` |
| Full app against real server | e2e (Playwright, built binary) | Smoke: setup, login, room live update, connect-agent snippet | `web/e2e/*.spec.ts` | `make e2e` |
| shadcn generated primitives, config | none | build gate only | `web/src/components/ui/*` | `make check` |

## Gate Check Commands

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick | Unit tasks | `cd web && bun run test` |
| Full | Tasks touching API integration | `cd web && bun run typecheck && bun run test` |
| Build | Last task of a phase | `make check` (includes `bun run lint`, `typecheck`, `test`, `build`) |

---

## Execution Plan

### Phase 1: Foundation

```
T1 → T2 → T3 → T4 → T5
```

### Phase 2: Rooms

```
T6 → T7 → T8 → T9 → T10
```

### Phase 3: Management

```
T11 → T12 → T13 → T14 → T15
```

### Phase 4: Polish and end-to-end

```
T16 → T17 → T18 → T19
```

---

## Task Breakdown

### Phase 1: Foundation

#### T1: Scaffold the web project

**What**: Vite + React + TS in `web/`, Tailwind v4 (`@tailwindcss/vite`), shadcn init (Radix, neutral base, CSS variables) plus the component list in design, TanStack Router plugin (autoCodeSplitting), Biome, Vitest + happy-dom, scripts `dev/build/test/lint/typecheck`; Vite dev proxy `/api` → `:8080`; Makefile builds `web/dist` before Go build.
**Where**: `web/vite.config.ts`
**Depends on**: None
**Reuses**: shadcn Vite docs
**Requirement**: UI-23 (theme tokens)
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [x] `bun run build` outputs `web/dist`; Go embed serves it (embedding itself is core-platform T25)
- [x] One smoke test renders the root route
**Tests**: unit
**Gate**: build
**Commit**: `build(web): scaffold vite react app with shadcn and tanstack router`

#### T2: API client, query hooks, schemas and i18n

**What**: `client.ts` (ApiError with status/code/fields; 401 → redirect hook), `queries.ts` (key factory + hooks for every resource), `schemas.ts` (zod from generated constants), i18n `en`/`pt-BR` with typed keys and `useT`.
**Where**: `web/src/lib/api/client.ts`
**Depends on**: T1
**Reuses**: `types.gen.ts`
**Requirement**: UI-02, UI-03, UI-25
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Error envelope parsed into ApiError fields; 401 triggers redirect callback with current path
- [x] Username/password zod rules reject `ab`, `UPPER`, 9-char password
- [x] Missing pt-BR key fails a parity test
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): add typed api client, query hooks, schemas and i18n`

#### T3: Live socket, handlers and store

**What**: `socket.ts` (backoff 1→30 s, status), `handlers.ts` pure cache patches per event type, `store.ts` zustand (status, presence, connector status, theme, locale, palette).
**Where**: `web/src/lib/live/handlers.ts`
**Depends on**: T2
**Reuses**: query keys
**Requirement**: UI-04, UI-06, UI-15, UI-17
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] `room.message` appends to the room's infinite query data without duplicates
- [x] `lagged` invalidates; reconnect requests `after_seq=lastSeq`
- [x] presence/connector events update the store
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): add live socket with cache-patching handlers`

#### T4: App shell and guard

**What**: `__root.tsx`, `_app.tsx` (beforeLoad `/me`, redirect with `redirect` param), sidebar (rooms, agents, connectors, identities, tokens, settings, admin when admin), reconnecting banner, theme toggle, language switch.
**Where**: `web/src/routes/_app.tsx`
**Depends on**: T3
**Reuses**: shadcn sidebar
**Requirement**: UI-02, UI-06, UI-21, UI-23
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [x] Unauthenticated → `/login?redirect=/rooms`; admin link hidden for members
- [x] Banner visible when socket status is not open for > 2 s
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): add authenticated app shell with sidebar and live banner`

#### T5: Setup, login and invite pages

**What**: `/setup`, `/login`, `/invite/$token` with react-hook-form + zod, field errors from 422.
**Where**: `web/src/routes/login.tsx`
**Depends on**: T4
**Reuses**: schemas
**Requirement**: UI-01, UI-02, UI-03
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [x] Setup shown when `needs_setup`; login returns to `redirect`; invalid invite shows expired state
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): add setup, login and invite pages`

### Phase 2: Rooms

#### T6: Rooms list and creation

**What**: `/rooms` list with member counts, paused badge, create dialog.
**Where**: `web/src/routes/_app/rooms/index.tsx`
**Depends on**: T5
**Reuses**: queries
**Requirement**: UI-10 (entry), UI-05 (empty state)
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [x] Create dialog posts and navigates to the new room; empty state when none
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): add rooms list and creation`

#### T7: Timeline and message item

**What**: `message-timeline.tsx` (infinite, virtualized, stick to bottom), `message-item.tsx`, `markdown.tsx` (no raw HTML).
**Where**: `web/src/components/message-timeline.tsx`
**Depends on**: T6
**Reuses**: handlers (T3)
**Requirement**: UI-04, UI-05, UI-09
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [x] Chat message shows `untrusted` badge for `trusted=false`; `<script>` in text renders as text
- [x] New live message appended and visible
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): add live message timeline`

#### T8: Composer

**What**: `composer.tsx` (Enter/Shift+Enter, max length, files, recipients), read-only notice without post.
**Where**: `web/src/components/composer.tsx`
**Depends on**: T7
**Reuses**: usePostMessage
**Requirement**: UI-07
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Enter sends trimmed text; Shift+Enter adds newline; over `MaxTextLength` disabled with counter
- [x] View-only user sees notice instead of composer
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): add message composer`

#### T9: Room page, members and pause banner

**What**: `/rooms/$roomId` combining timeline, composer, members panel (online dots, outbound switch, remove), member picker (usable agents/chats, filter), settings sheet (name, bridge chats, retention), loop-guard banner with resume.
**Where**: `web/src/routes/_app/rooms/$roomId.tsx`
**Depends on**: T8
**Reuses**: components above
**Requirement**: UI-08, UI-10, UI-12
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [x] Picker lists only usable agents/chats; outbound toggle calls PATCH; resume button only for managers
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): add room page with members and settings`

#### T10: Sharing dialog

**What**: `sharing-dialog.tsx` reused for rooms, agents and connectors (private/shared/public, user picker, levels, access summary).
**Where**: `web/src/components/sharing-dialog.tsx`
**Depends on**: T9
**Reuses**: `/users` directory
**Requirement**: UI-11
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Each mode submits the exact `dto.Sharing` payload; summary lists who can access
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): add reusable sharing dialog`

### Phase 3: Management

#### T11: Agents page and connect wizard

**What**: `/agents` table with live presence and last seen, sharing, delete; "Connect an agent" wizard creating an agent token and showing `.mcp.json` + launch command with the instance URL.
**Where**: `web/src/routes/_app/agents.tsx`
**Depends on**: T10
**Reuses**: copy-field, secret-once-dialog
**Requirement**: UI-14, UI-15
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [x] Snippet contains `partyline channel`, the URL from `window.location.origin` and the new token
- [x] Presence event flips the row's status dot
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): add agents page with connect wizard`

#### T12: Connectors pages and schema form

**What**: `/connectors` (list, status, add via `schema-form.tsx`), `/connectors/$connectorId` (status, error, chats, refresh, sharing, "Route to agent").
**Where**: `web/src/components/schema-form.tsx`
**Depends on**: T11
**Reuses**: connector kinds endpoint
**Requirement**: UI-13, UI-16, UI-17
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [x] Field types map to inputs (secret → password); required errors shown from zod and 422
- [x] "Route to agent" calls `/chats/{id}/route` and navigates to the room
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): add connector pages with schema-driven forms`

#### T13: Identities page

**What**: `/identities` list, delete, pairing dialog with code, command and countdown; closes on `identity.linked`.
**Where**: `web/src/routes/_app/identities.tsx`
**Depends on**: T12
**Reuses**: live store
**Requirement**: UI-18
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Countdown reaches 0 → expired state; live event → success and list refetch
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): add identities page with pairing flow`

#### T14: Tokens and settings pages

**What**: `/tokens` (create kind/name/expiry, secret once, revoke), `/settings` (profile, password, sessions revoke, theme, language).
**Where**: `web/src/routes/_app/tokens.tsx`
**Depends on**: T13
**Reuses**: secret-once-dialog
**Requirement**: UI-19, UI-23, UI-25
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Secret shown once with warning; list shows prefix only; theme/language persist in localStorage
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): add tokens and settings pages`

#### T15: Admin pages

**What**: `/admin/users` (role, enable/disable), `/admin/invites` (create, copy link, revoke), `/admin/audit` (paged), 403 page for non-admins.
**Where**: `web/src/routes/_app/admin/users.tsx`
**Depends on**: T14
**Reuses**: queries
**Requirement**: UI-20, UI-21
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Member opening `/admin/users` sees 403 page; invite link copied contains `/invite/<token>`
**Tests**: unit
**Gate**: build
**Commit**: `feat(web): add admin users, invites and audit pages`

### Phase 4: Polish and end-to-end

#### T16: Dashboard

**What**: `/` with online agents, connector health, recent activity (last messages across visible rooms), quick actions (connect agent, add connector, new room).
**Where**: `web/src/routes/_app/index.tsx`
**Depends on**: T15
**Reuses**: queries, live store
**Requirement**: UI-15, UI-17
**Tools**: MCP: NONE; Skill: `impeccable`
**Done when**:
- [x] Cards reflect live presence/status changes; empty states link to the right flows
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): add live dashboard`

#### T17: Command palette

**What**: `command-menu.tsx` on Ctrl/Cmd+K: rooms, agents, connectors, pages.
**Where**: `web/src/components/command-menu.tsx`
**Depends on**: T16
**Reuses**: shadcn command
**Requirement**: UI-22
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Ctrl+K opens; typing filters; Enter navigates
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): add command palette`

#### T18: Responsive, accessibility and translation pass

**What**: verify 360 px layouts (sidebar as sheet), focus states, aria labels, complete pt-BR strings.
**Where**: `web/src/lib/i18n/pt-BR.ts`
**Depends on**: T17
**Reuses**: `web-design-guidelines` skill checklist
**Requirement**: UI-24, UI-25
**Tools**: MCP: NONE; Skill: `web-design-guidelines`
**Done when**:
- [x] i18n parity test passes; axe check (vitest-axe) on shell, room and login has no violations
**Tests**: unit
**Gate**: quick
**Commit**: `feat(web): complete responsive, accessibility and pt-br pass`

#### T19: Playwright smoke suite

**What**: `web/e2e/smoke.spec.ts` against the built binary on a temp data dir: setup (token read from log), login, create room, post, second context receives live, connect-agent snippet visible; `make e2e`; CI job.
**Where**: `web/e2e/smoke.spec.ts`
**Depends on**: T18
**Reuses**: Playwright headless shell
**Requirement**: UI-01, UI-04, UI-14
**Tools**: MCP: NONE; Skill: NONE
**Done when**:
- [x] Suite passes locally and in CI at 1280 px and 360 px
**Tests**: e2e
**Gate**: build
**Commit**: `test(web): add playwright smoke suite`

---

## Phase 5: Validation fixes (iteration 1)

From `validation.md` (iteration 1): gaps M17, M18, UI-24, M19, the precision notes and the Verifier's
UI notes. Each task is one commit; tests come from the spec ACs.

```
T20 → T21 → T22 → T23 → T24 → T25 → T26 → T27 → T28 → T29 → T30 → T31 → T32
```

#### T20: System colour scheme test (UI-23, M17)
**Where**: `web/src/components/app-providers.test.tsx`
**Depends on**: T19
**Requirement**: UI-23
**Done when**:
- [x] With preference `system`, `<html>` has `dark` iff `prefers-color-scheme: dark` matches, and a live media-query change re-applies it
**Tests**: unit
**Gate**: quick

#### T21: Room page gates the composer by `my_level` (UI-07, M18)
**Where**: `web/src/routes/_app/rooms/room.test.tsx`
**Depends on**: T20
**Requirement**: UI-07
**Done when**:
- [x] `my_level: "view"` → read-only notice and no message textbox; `post` → message textbox
**Tests**: unit
**Gate**: quick

#### T22: No horizontal scroll at 360 px (UI-24)
**Where**: `web/e2e/smoke.spec.ts`
**Depends on**: T21
**Requirement**: UI-24
**Done when**:
- [x] `scrollWidth <= innerWidth` on dashboard, rooms, room, agents, connectors and settings in `mobile-360`
**Tests**: e2e
**Gate**: full

#### T23: Backoff resets after a successful open (M19)
**Where**: `web/src/lib/live/socket.test.ts`
**Depends on**: T22
**Requirement**: UI-06
**Done when**:
- [x] After failed retries and a successful open, the next drop retries after 1 s
**Tests**: unit
**Gate**: quick

#### T24: Agents show the last-seen time (UI-15)
**Where**: `web/src/routes/_app/agents.test.tsx`
**Depends on**: T23
**Requirement**: UI-15
**Done when**:
- [x] The rendered last-seen value matches the agent's `last_seen_at` under a fixed clock
**Tests**: unit
**Gate**: quick

#### T25: Token secret copy button (UI-19)
**Where**: `web/src/routes/_app/tokens.test.tsx`
**Depends on**: T24
**Requirement**: UI-19
**Done when**:
- [ ] Clicking Copy in the token secret dialog puts the secret on the clipboard
**Tests**: unit
**Gate**: quick

#### T26: Reword UI-14 (snippet holds URL and token)
**Where**: `.specs/features/web-app/spec.md`
**Depends on**: T25
**Requirement**: UI-14
**Done when**:
- [ ] AC reads "…ready-to-copy `.mcp.json` snippet containing the instance URL and the token, and the launch command"; `agents.test.tsx` and the e2e assert exactly that
**Tests**: none (existing tests match)
**Gate**: quick

#### T27: Composer counts Unicode code points like the server
**Where**: `web/src/components/composer.tsx`
**Depends on**: T26
**Requirement**: UI-07
**Done when**:
- [ ] A message of max-length emoji is accepted; one more code point is rejected
**Tests**: unit
**Gate**: quick

#### T28: Locale detection follows browser preference order
**Where**: `web/src/lib/i18n/`
**Depends on**: T27
**Requirement**: UI-25
**Done when**:
- [ ] `["en-US","pt-BR"]` → `en`; `["fr","pt-BR"]` → `pt-BR`; `["fr"]` → `en`
**Tests**: unit
**Gate**: quick

#### T29: Inline attachment images reserve space
**Where**: `web/src/components/message-item.tsx`
**Depends on**: T28
**Requirement**: UI-05
**Done when**:
- [ ] Inline images carry `width`/`height` when the metadata has them, otherwise sit in a fixed aspect box
**Tests**: unit
**Gate**: quick

#### T30: Reconnecting live region is always mounted
**Where**: `web/src/components/live-status.tsx`
**Depends on**: T29
**Requirement**: UI-06
**Done when**:
- [ ] The `status` region exists while connected (empty) and gets the reconnecting text after 2 s
**Tests**: unit
**Gate**: quick

#### T31: No autofocus on coarse pointers
**Where**: `web/src/hooks/`, forms and dialogs using `autoFocus`
**Depends on**: T30
**Requirement**: UI-24
**Done when**:
- [ ] With `(pointer: coarse)` matching, the first field is not focused on mount; on fine pointers it is
**Tests**: unit
**Gate**: quick

#### T32: Load-robust waits in unit tests
**Where**: `web/src/**/*.test.tsx`, vitest config
**Depends on**: T31
**Requirement**: none (test infrastructure)
**Done when**:
- [ ] Async `find*`/`waitFor` tolerate a loaded host (longer default timeout), no assertion changed
**Tests**: unit
**Gate**: build
