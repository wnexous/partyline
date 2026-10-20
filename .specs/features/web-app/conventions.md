# Web App Conventions (from web-app Phase 1)

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

## Design language (`src/index.css`, `PRODUCT.md`, `.impeccable/`)
- Paper background, blue-black ink, buff sidebar, telegram-tape yellow for live/selected (`bg-tape`, `text-tape-foreground`, `tape-strong`); semantic `success`, `warning`, `info`.
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
- DTO gaps for the backend: `Room` has no member count (rooms list omits it); `Message` has no viewer-relative trust flag (derived as above).
- `make check` needs `sqlc` and `tygo` on PATH; without them run `make check SQLC="go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1" TYGO="go run github.com/gzuidhof/tygo@v0.2.21"`.
