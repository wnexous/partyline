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
