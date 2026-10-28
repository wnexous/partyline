# Web App Conventions (from web-app Phase 1)

Where code lives (layers, import rules, canonical components): `web/AGENTS.md`.

Binding for every later web task.

## Structure
- Authenticated pages: `src/routes/_app/...`; public pages at `src/routes` top level. Tests co-located `*.test.ts(x)` (router ignores them).
- Placeholders to replace: overview, rooms, agents, connectors, identities, tokens, settings, admin/users. `_app/admin/route.tsx` already renders the 403 page for members. New nav entries must be added to the allowed set in `NAV_GROUPS` (`components/app-sidebar.tsx`).

## Tests
- `src/test/render.tsx` (`renderRoute(path)` mounts the real router), `src/test/fetch.ts` (`mockApi({"GET /me": json(...)})`, `apiError`), `src/test/fixtures.ts` (`makeMessage`, `makeMe`; synthetic only), `src/test/fake-socket.ts`.
- Page tests mock live: `vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }))`.

## Data
- `lib/api/queries.ts`: reads `useX`, mutations `useVerbX`, loader options `xQuery()`, keys from `queryKeys`. Lists cached as plain arrays.
- Room timeline: infinite query, first page newest, items within a page oldest first (client sorts each page; cursor = lowest seq as `before_seq`).
- `usePostMessage` does not write to the cache; the live `room.message` event appends.
- `lib/live/{socket,handlers,store}.ts`; zustand store: status, outage start, presence, connector status, last linked identity, theme, locale, palette open.

## i18n
- `en.ts` defines flat `area.thing` keys; `pt-BR.ts` must match exactly (parity test). Use `useT()` / `useI18n()`. Form errors are i18n keys rendered via `errorText`.

## Forms
- react-hook-form + zod; `TextField`, `FieldError`, `FormAlert` from `components/form-fields.tsx`; `applyFieldErrors` maps 422 reasons; `safeRedirect` keeps redirects internal.

## Design language (`src/index.css`, `DESIGN.md`, `PRODUCT.md`, `.impeccable/`)
- Neutral base with a little colour (owner's briefs, 2026-10-23): surfaces and ink are greys (white paper, black ink, grey sidebar). Colour is a small fixed vocabulary: one dusty-indigo accent (`primary`, `link`, `ring`, and the tinted `tape`/`tape-strong` for live and selected, `bg-ai` for the AI badge), a quiet green for presence (`bg-online`), the muted `destructive` red for errors and destructive actions, and soft avatar tints (`bg-tint-*`, warm for people, cool for AIs, picked by `avatarTone`). There are no `success`, `warning` or `info` tokens: state keeps its shape (filled vs hollow dots, outlines, dashes, an inverted banner) and its word, so colour is never the only signal (`DESIGN.md`).
- Colour never bypasses the tokens: no hex, `rgb()`/`oklch()` literals or Tailwind palette classes in components, and a fill and its ink flip together with the theme, so write `bg-tape text-tape-foreground` without `dark:` colours. Focus halos are `ring-ring/70`. `src/lib/palette.test.ts` pins the neutral surfaces, the hue budget, the WCAG AA pairs, the favicon and the `theme-color` hex (`THEME_COLORS`, `index.html`).
- `label-caps` for small section labels; `.tabular` for numbers. `animate-tape-settle` highlights new live messages; `animate-line-pulse` for connection dots.
- Archivo for UI; JetBrains Mono only for data (seq, times, tokens, commands). Shared: `PageHeader`, `EmptyState`, status pages (403, not found, error).
- No inline scripts (CSP `default-src 'self'`): theme pre-paint via `/theme-init.js`.

## Contract notes
- Wrong setup token → 401 (`ErrBadSetupToken`). 422 field reasons map onto inputs.
- `Me.locale` sync lands in T14 (until then localStorage only).

# Additions from web-app Phase 2 (rooms)

## Structure and shared components
- Full-height pages put `data-fill` on their root; `_app.tsx` then bounds the inset (`h-svh`, no page scroll) so the page scrolls inside. The room page uses it.
- Reuse: `StatusDot` (agent presence, `withLabel`), `VisibilityLabel` / `VISIBILITY` (room-visibility.tsx), `platformLabel(kind)`, `TrustBadge` / `chatTrust`, `formatBytes` (message-item.tsx), `formatRelative` / `formatAbsolute` / `formatClock` (`lib/time.ts`).
- `SharingDialog kind={"rooms"|"agents"|"connectors"} id name owner trigger`: rooms grant view/post/manage (public view|post); agents and connectors grant `use` only. T11/T12 wire it with `kind="agents"` / `"connectors"`; gate the trigger on `my_level === "manage"`.
- Dialog/sheet bodies mount only while open (`{open ? <Body/> : null}`) so queries (users, agents, chats, sharing) run on demand and forms start fresh.
- Destructive member actions use a sonner toast with Undo instead of a confirm dialog.

## Tests
- `renderWithProviders(ui)` (src/test/render.tsx) mounts a component with query cache, i18n and tooltips, no router.
- `stubLayout()` (src/test/layout.ts) gives happy-dom boxes a viewport and row height; call it at the top of any suite that renders the virtualized timeline (room pages included).
- Live behavior in component tests: call the real `handleLiveEvent(queryClient, useLiveStore.getState(), event)` inside `act`.
- Fixtures: `makeRoom`, `makeMember`, `makeAgent`, `makeChat` (synthetic).
- Lint runs on the whole `web/` (`bun run lint`); check it before committing, not only `biome check src`.

## Data and contract notes
- `useUpdateSharing` invalidates only `[kind]` and `[kind, id]` exactly (never timelines or members).
- Message trust in the web app: dto.Message has no `trusted` flag. Chat messages show `untrusted` when `author_user_id` is empty, `trusted` when it is the viewer, `verified` when it is another user (only that user's agents trust it).
- Composer recipients are chat ids (`RoomMember.member_id` of chat members), never `RoomMember.id`; anything else is a 422 on `recipients`. None picked = omit `recipients` (server sends to chats with outbound on).
- Attachments render inline only for png/jpeg/gif/webp (what the server serves inline); SVG and everything else is a download link.
- Agent Markdown: react-markdown without rehype-raw (raw HTML shows as text), default URL transform, links open in a new tab with `noopener noreferrer nofollow`, images become links.
- DTO gaps for the backend: `Message` has no viewer-relative trust flag (derived as above). (`Room.member_count`
  landed; the rooms list shows it since Phase 6.)
- `make check` needs `sqlc` and `tygo` on PATH; without them run `make check SQLC="go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1" TYGO="go run github.com/gzuidhof/tygo@v0.2.21"`.

# Additions from web-app Phase 3 (management)

## Structure and shared components
- A dialog opened from a page that can switch between its empty state and its list (connect wizard,
  add connector, pairing, new token, new invite) is controlled by the page (`open` state lifted, plain
  `Button` triggers). A trigger inside `EmptyState` unmounts when the first item appears and would
  close the dialog mid-flow.
- Reuse: `CopyButton` / `CodeBlock` (`copy-field.tsx`; a block is a `figure` captioned by its label),
  `SecretOnceDialog` (`secret-once-dialog.tsx`; token secrets and invite links; outside clicks do not
  close it), `ConnectorForm` + `connectorFormSchema` + `connectorFormDefaults` (`schema-form.tsx`),
  `ConnectorStatusBadge` + `useConnectorState` (`connector-status.tsx`), `RouteChatDialog`,
  `ConnectAgentDialog`, `AddConnectorDialog`, `useChooseLocale(syncAccount)` (`language-switch.tsx`).
- Destructive confirmations use `ui/alert-dialog.tsx` (added; role `alertdialog`): delete connector,
  remove identity, revoke token/invite, disable account. The agents page delete still uses `Dialog`.
- `lib/agent-connect.ts` builds the `.mcp.json` entry and holds the launch, install, releases and docs
  constants; the wizard reads `window.location.origin`.
- Detail pages (`/connectors/$connectorId`) use a back link + title header and a left-aligned
  `max-w-3xl` body; 404 renders an `EmptyState` "not on your line".

## i18n namespaces
- `agents.`, `connect.` (wizard), `connectors.` (list/add), `connector.` (detail), `route.`, `schema.`,
  `identities.`, `pairing.`, `tokens.`, `account.` (account settings page; `settings.` stays the room
  settings sheet), `admin.`, `invites.`, `audit.`.

## Live and preferences
- `agent.presence` for an agent id missing from the cached list invalidates `["agents"]` (agents
  register on first connection), so the wizard's "waiting → online" line works.
- Language: `useChooseLocale(true)` writes localStorage and `PATCH /me {locale}` (sidebar menu and
  settings); `_app.tsx` adopts `Me.locale` only when the browser has no stored choice. Theme stays
  per browser.

## Tests
- Timers: `vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] })` keeps user-event and
  TanStack Query timeouts real; advance inside `act`.
- Clipboard: `userEvent.setup()` stubs `navigator.clipboard`; assert with `navigator.clipboard.readText()`.
- Code blocks: `within(x).getByRole("figure", { name }).querySelector("pre")?.textContent`.
- Fixtures: `makeConnector`, `makeConnectorKind` (one field of every type).
- Radix `Select` is avoided in forms that tests drive; use `RadioGroup` cards or `ToggleGroup`.

## Contract notes and DTO gaps
- `PostMessageRequest.recipients` are chat ids (`RoomMember.member_id`), see Phase 2 note (fixed).
- Agent tokens are `POST /tokens {kind: "agent"}`; the token is not bound to a name, the wizard uses the
  agent name as the token name and as `PARTYLINE_AGENT`.
- No generated constant for the agent name rule: `AgentNamePattern` in `schemas.ts` mirrors
  `^[a-z0-9][a-z0-9._-]{0,47}$` from the agent-channel design.
- `ConnectorField.label`/`help` arrive from the server in English only (forms are not localized there).
- Connector 422 field keys: the form maps `name`, `config.<field>` and bare `<field>`; the backend should
  pick one (suggest `config.<field>`).
- Resolved in Phase 6: `Connector.inbound_url` comes from the server (the page no longer builds it).
- Pairing countdown compares `PairingCode.expires_at` with the browser clock (no server time offset).
- Admins cannot demote or disable themselves in the UI; the server now also answers 409 `conflict`
  (self change, last admin).

# Additions from web-app Phase 4a (dashboard, palette, a11y pass)

## Structure and shared components
- Dashboard (`routes/_app/index.tsx`): switchboard (agents, connectors) beside the wire (recent
  activity). Panels are `section aria-labelledby` with a `label-caps` h2; empty states inside a
  titled section pass `headingLevel="h3"` to `EmptyState`.
- `lib/presence.ts`: `withPresence`, `byLiveness`, `AgentView` (agents page and dashboard).
- `message-item.tsx` also exports `AUTHOR_KIND` (icon, label, tile per author kind) and
  `contentKindLabel(kind)` for text-less messages.
- Command palette: `components/command-menu.tsx` (mounted once in `_app.tsx`, Ctrl/Cmd+K, open flag
  `paletteOpen` in the live store, body mounts only while open). `components/search-button.tsx`
  opens it from the sidebar; the mobile top bar has an icon button. Pages come from `NAV_GROUPS`,
  so a new nav entry appears in the palette too.
- Destructive confirmations: always `ui/alert-dialog.tsx` (role `alertdialog`, action styled
  `bg-destructive text-white hover:bg-destructive/90`, `e.preventDefault()` then close on success).
  Member removal keeps the undo toast.
- The shell has a skip link to `<main id="main">` (`SidebarInset`).

## Data
- Dashboard feed: `useActivity()` / `queryKeys.activity` caches `ActivityItem[]` (Phase 6) newest first, shaped by
  `trimActivity` (≤ `ACTIVITY_PER_ROOM` per room, ≤ `ACTIVITY_SIZE` total). The live `room.message`
  handler prepends into it only when cached; `room.deleted` drops that room; `catchUp` invalidates it.
- Resolved in Phase 6: the feed is one `GET /activity?limit=` request (was N room samples).

## Accessibility and copy
- `src/routes/a11y.test.tsx` runs vitest-axe (colour-contrast off: happy-dom has no layout) on the
  shell, room and login pages in en and pt-BR. Add a page there when it gets a new layout.
  `src/test/axe.d.ts` types `toHaveNoViolations` for vitest 5; extend matchers per file.
- shadcn primitives must not carry English literals: use `useT()` inside them (dialog/sheet close,
  sidebar sheet title, toggle labels).
- No `transition-all`: list the properties. Overlays and scrolling lists use `overscroll-contain`.
- English copy uses typographic apostrophes (’); tests asserting such copy must use them too.
- `translate="no"` on the wordmark and code blocks; `theme-color` metas follow the theme
  (`THEME_COLORS` in `lib/theme.ts`).

## Tests
- Query-cache-driven UI updates after a live event render on the next tick: wrap the assertion in
  `waitFor` (zustand-driven updates are synchronous inside `act`).
- Visual checks run against `/tmp/claude-1001/pl-visual/mock.ts` + `shot.mjs` (outside the repo).

# Additions from web-app Phase 5 (validation fixes, iteration 1)

## Behaviour
- Message length counts Unicode code points like the server (`textLength()` in `lib/schemas.ts`,
  Go's rune count). Never use `text.length` against `MaxTextLength`.
- `detectLocale` walks `navigator.languages` in order and takes the first supported one (`pt*` →
  pt-BR, `en*` → en); English when none matches.
- Focus on mount only with a fine pointer: `useAutoFocus()` (`hooks/use-auto-focus.ts`) gates
  `autoFocus` (TextField applies it, so pass `autoFocus` freely); `DialogContent` focuses the dialog
  itself instead of its first field when `(pointer: coarse)` matches. New raw `<Input autoFocus>`
  must use the hook.
- Live regions stay mounted and change content (`LiveBanner` renders an empty `role="status"` while
  connected). Do not mount a live region together with its text.
- Inline attachment images sit in a fixed 320 px, 4:3 box (`object-contain`, `width`/`height`
  attributes). DTO gap: `Attachment` has no pixel width/height; with them the box could follow the
  image's own ratio.

## Tests
- `src/test/media.ts`: `stubMatchMedia(fn)` and `stubPointer("coarse" | "fine")`; theme tests use a
  controllable `prefers-color-scheme` stub with change listeners (`app-providers.test.tsx`).
- `find*` / `waitFor` default to 5 s (`configure({ asyncUtilTimeout })` in `src/test/setup.ts`) and
  tests to 15 s (`vite.config.ts`): the host is often loaded. Do not add per-call 1 s timeouts.
- E2E UI-24: the smoke suite asserts `document.documentElement.scrollWidth <= innerWidth` on the
  main pages in both projects; the room page scrolls inside its timeline (the inset clips), so it
  also checks a long unbroken message line ends inside the viewport. Add new top-level pages there.
- Under heavy load the e2e login step can exceed the 10 s `expect` timeout (password hashing on the
  server); a re-run passes. Not a product defect.

# Additions from web-app Phase 6 (backend follow-ups)

## Data
- Rooms list rows show `Room.member_count` ("No members" / "1 member" / "N members").
- Dashboard feed: `activityQuery()` → `GET /activity?limit=ACTIVITY_FETCH` (`DefaultActivityLimit`),
  then `trimActivity` (≤ `ACTIVITY_PER_ROOM` per room, ≤ `ACTIVITY_SIZE`). Rows name the room from
  `ActivityItem.room`. Live `room.message` prepends `{room, message}` naming the room from the feed,
  the room cache or the rooms list; a room none of them knows invalidates the feed instead.
  `room.changed` renames, `room.deleted` drops.
- `useCreateConnector` caches the connector without `generated_secrets`; only the create response
  (handled by `AddConnectorDialog`) ever holds them.

## Live socket and session end
- Close 1008 (`CLOSE_SESSION_REVOKED`, "session revoked"): `createLiveSocket` stops (no reconnect) and
  calls `onSessionEnded`; `startLive` clears the query cache and calls `notifyUnauthorized()`
  (`lib/api/client.ts`), the same handler a 401 runs → `/login?redirect=<current>`.
- Close 1013 ("session check failed") and every other code: normal reconnect with backoff.
- `FakeSocket.drop(code = 1006, reason)` passes a close event; `close()` sends 1000.
- `src/routes/live-session.test.tsx` drives the real socket in the shell by stubbing the global
  `WebSocket` with `FakeSocket` (no `vi.mock("@/lib/live")` there).

## Connectors
- `AddConnectorDialog`: a create response with `generated_secrets` closes the add dialog and opens
  `SecretOnceDialog` (first secret as the main block, any others plus `inbound_url` and the signing
  recipe as extra `CodeBlock`s); the connector page opens when it closes. Without generated secrets it
  navigates at once, as before.
- The connector page shows the inbound section only when `Connector.inbound_url` is set.

## Composer
- Chat members with `outbound=false` are listen-only (DLV-05): disabled in the recipients picker,
  labelled `composer.listenOnly` with a `title` hint, and filtered out of `recipients`. A 422
  `recipients: listen-only` shows `composer.recipientsListenOnly`, sets `aria-invalid` on the picker
  trigger and points its `aria-describedby` at the error.

## i18n
- The pairing dialog (connectors fix T28) and every Phase 6 string have pt-BR entries; the parity
  test (`lib/i18n/i18n.test.tsx`) passes. Keys equal in both locales are cognates or names
  (`Chats`, `Online`, `Admin`, `GitHub Releases`).

## DTO gaps for the backend
- Resolved (connectors T34, web-app T39): `ConnectorField.generated` marks fields the server fills
  when left empty (the webhook `secret`, still `required: true`). The schema form requires a value
  only when `required && !generated`; a generated field keeps its plain label and shows its help, the
  create request sends it as `""` and the secret-once dialog shows the generated value. Editing keeps
  secrets as `SecretMask`; an edit that sends a generated field empty keeps the stored value.
