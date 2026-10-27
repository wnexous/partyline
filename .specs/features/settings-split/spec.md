# Account and Organization Settings Specification

## Problem Statement

The product owner asked for "a better split between what is my organization's configuration and what is my profile's configuration" (2026-10-27). The pages exist, but nothing says which belongs to whom:

- `web/src/routes/_app/settings.tsx` holds seven sections on one page: Profile, Appearance, Notifications on this device, Default organization, Password, Sessions and Delete account.
- An organization's pages live under `web/src/routes/_app/orgs/$orgId/` (chart, members, positions, settings) — except its connectors, which live at `/connectors` and `/connectors/$connectorId` and are filtered by whichever organization this browser last picked.
- Identities, API tokens and the machine-approval page hang off the account menu with no stated owner.
- There is **one** entry to the account page (the avatar menu) and **none** to the current organization's configuration: the org switcher's menu only offers the org chart, all organizations and "New organization" (`web/src/components/org-switcher.tsx:128-160`). The organization's settings are reachable only after opening the organization's list and then one of its pages.
- On a phone there is no page drawer at all: below 768 px the conversation list is the page and every other page opens full screen with a top bar (back, brand, org switcher, search, `web/src/routes/_app.tsx:103-126`); the avatar menu is a dropdown anchored to the avatar on `/`, and the sheets in the app belong to a row's actions, the room settings and the profile panel (`web/src/components/action-menu.tsx`, `room-settings-sheet.tsx`, `profile-panel.tsx`). So a phone can reach the account page only from the list screen, and the organization's configuration not at all without its address.

This feature fixes the map and the two entries, and moves nothing on the server.

The decisions below are fixed by the product owner's brief:

- **My account** is about the person and the same in every organization: profile, appearance and language, the notifications of this device, identities, password, sessions and devices, API tokens, deleting the account, and the default organization.
- **Organization** is about the current organization: name and picture, members, chart and positions, its connections, its AIs, its settings.
- One obvious entry to My account (the avatar menu) and one to the current organization's settings, next to the org switcher.
- Existing URLs keep working; anything that moves gets a redirect.
- No server change: this is navigation and page structure.
- Mobile first: every screen works at 320 and 412 px wide with a 125% root font, with touch targets of at least 44 px.
- Every page is reachable from a control on screen; no page exists only as a URL.
- Portuguese (pt-BR) and English strings for everything.
- Tests first, derived from the acceptance criteria below.

## Goals

- [ ] One page says what "my account" is (`/account`), it keeps every section it had, and every old link to `/settings` lands on it.
- [ ] The account menu names its owner ("Conta") and lists the account's pages (account, identities, tokens, devices) and a "Ir para" group for conversations, contacts and organizations, with the page in view marked.
- [ ] The current organization has one entry at the org switcher — a gear beside it, or its own menu on a phone's top bar — and it lists the organization's pages (chart, members, positions for admins, connections, settings).
- [ ] A connection belongs to an organization on screen as it does in the API: `/orgs/{org}/connections` and `/orgs/{org}/connections/{id}`, with `/connectors` and `/connectors/{id}` redirecting there.
- [ ] A connection opened under an organization it does not belong to moves to its own organization's address.
- [ ] Every moved page fits 320 px and 412 px with a 125% root font, every menu row is at least 44 px, and both languages are complete.
- [ ] No HTTP route, DTO, table or permission rule changes; the change is `web/` and the specs.
- [ ] The fit check (`web/e2e/fit.spec.ts`) covers the moved pages and the open menus, and a new e2e test proves the two entries and the redirects on a desktop and on a phone.

## Out of Scope

| Feature | Reason |
| --- | --- |
| Splitting the account page into several pages (profile, security, notifications as their own addresses) | The split the owner asked for is between the account and the organization; the account page is one scroll on a phone and its seven sections stay together (design, Alternatives) |
| Moving `/identities`, `/tokens` or `/devices` under `/account` | They already say whose they are, are deep-linked from the CLI and from dialogs; each extra move costs a redirect and a test for no product gain. They are listed under "Conta" |
| A per-organization "AIs" page | The organization's AIs are its AI members: the members page and the chart already list them; a second list would split one truth (AD-024 forbids an AIs-versus-people split) |
| Renaming "connectors" to "connections" in the UI, the API or the code | The brief's word is "connections"; the product's word is "connectors" everywhere; renaming is a copy change with no navigation value |
| Server-side redirects (HTTP 301) or new HTTP routes | The Go server serves the SPA for any path (`internal/webui/webui.go:76-99`); the existing `/agents` → `/contacts` redirect is client-side and this feature follows it |
| A new phone drawer for pages | The phone has no page drawer today; adding one duplicates the list screen and re-implements theme/language controls the menu already owns (design, Navigation on a phone) |
| Any change to permissions, membership, roles or the org chart rules | Navigation only; the API and its authorization are untouched |
| A gear inside the phone's top bar | The bar already holds four 55 px controls at a 125% root font and fits 320 px with a few pixels to spare; a fifth 55 px control and its gap need at least 60 px more (design, The 320 px budget) |
| A search or command-palette redesign | Only the connector link in the palette follows its new address |
| Changing `/orgs`, `/contacts` or `/admin` addresses | They are neither the account's nor one organization's configuration (design, The map) |
| Reordering or rewriting the account page's sections | Their behaviour, validation and copy are out of scope; only the page's address and title change |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| The account's address | `/account`, with `/settings` redirecting to it (`replace`), the page's H1 "Minha conta" / "My account" | "Settings" namelessly competed with the organization's settings (`/orgs/{org}/settings`); the account is the other half of the pair | y |
| Identities and API tokens | Keep `/identities` and `/tokens`; list them in the account menu's "Conta" group | Both pages are already account-only; one more redirect each for no product gain | y (coordinator, delegated by the owner, 2026-10-27) |
| The `/devices` address | Keep `/devices`; list it in the account menu's "Conta" group | `partyline setup --browser` prints it and `TestSetupBrowserApproval` pins `<instance>/devices` (DEV-01), so moving it is not a web-only change | y (coordinator, delegated by the owner, 2026-10-27) |
| The default organization | Stays on the account page, next to the appearance choices | It is a per-user preference (`users.default_org_id`), like the language, and it decides which organization the app opens in | y |
| Connections | Move to `/orgs/{org}/connections` and `/orgs/{org}/connections/{id}`; `/connectors` and `/connectors/{id}` redirect | A connection belongs to an organization in the API (`/api/v1/connectors` with `org_id`, AD-016) and the list already filters by the current organization; a URL says what the data says | y |
| The organization's AIs | Its AI members, reached from Members and the org chart; no separate page | One list of members is the product rule (AD-024); the chart says where each AI sits | y (coordinator, delegated by the owner, 2026-10-27) |
| Contacts | Stays at `/contacts`, in the account menu's "Ir para" group | It is the directory of everyone the viewer can reach in every organization plus the AIs they host: neither a setting nor one organization's page | y (coordinator, delegated by the owner, 2026-10-27) |
| Where the organization's entry stands | A gear beside the org switcher when the viewport is 768 px or wider (desktop sidebar and the phone's list screen), and the switcher's own menu on the phone's top bar | The top bar has no room for a fifth control at 320 px with a 125% font (design, The 320 px budget); the switcher is already the organization's control there | y |
| Where the account's entry stands | The avatar menu, on every width, on the list screen (desktop sidebar and phone home screen) | The owner named it; keeping it in one place keeps one entry per area | y |
| The container of each menu | Both menus stay dropdown menus on every device, with rows of at least 44 px and group headings; no new sheet | The app's sheets belong to a row's actions; the account menu's theme/language submenus already exist and work on touch | y |
| How old addresses redirect | Client-side `beforeLoad` redirects with `replace: true`, as `/agents` → `/contacts` does | One pattern, testable with `renderRoute`, and no server route to keep in two places | y |
| Which organization a redirect uses | The same resolution as the switcher: the browser's pick, else the account's default, else the first membership; no organization at all means `/orgs` | One rule for the whole app (`resolveCurrentOrg`, `web/src/lib/orgs.ts:87`); a viewer with no organization is offered the organization list | y |
| The account page's structure | One page, same seven sections, same order | The brief lists them all as "mine"; splitting them is a second feature (Out of Scope) | y (coordinator, delegated by the owner, 2026-10-27) |
| The organization's section order | Chart, Members, Positions (admins), Connectors, Settings | Chart is the landing page of an organization; settings stay last, as today | y |
| Labels | The account group is "Conta" / "Account" with the page "Minha conta" / "My account"; the gear reads "Páginas de {org}" / "Pages of {org}", which also names the menu it opens | The switcher's button already reads "Organization: {org}"; the gear needs a different name or a screen reader cannot tell the two apart | y |
| What "connection" means in the UI | The existing word "Conector" / "Connector" (page title and labels are unchanged) | The brief's "connections" is the same object; renaming is out of scope | y |
| The tests | Vitest through the real route tree for pages, menus, redirects and a11y; Playwright for the two entries, the redirects and the phone fit | Same shape as the existing suite (`web/src/routes/_app/settings.test.tsx`, `web/e2e/fit.spec.ts`) | y |

**Open questions:** none blocking; the five rows marked `n` are adopted defaults with the product owner's confirmation pending (see "Open questions for the product owner" at the end).

---

## User Stories

### P1: My account has one home ⭐ MVP

**User Story**: As a person, I want my own settings in one place with a name that says they are mine, so that I stop confusing them with the organization's settings.

**Acceptance Criteria**:

1. WHEN a signed-in user opens `/account` THEN the system SHALL show one page whose heading is "My account" in English and "Minha conta" in Portuguese, with the sections Profile, Appearance, Default organization, Notifications on this device, Password, Sessions and Delete account, each keeping its current behaviour. (SET-01)
2. WHEN a user opens `/settings`, from a bookmark, an old link or the address bar THEN the system SHALL replace it with `/account` and show the account page, leaving one history entry instead of two. (SET-02)
3. WHEN the account menu opens THEN the system SHALL group its destinations under "Conta" (My account, Identities, API tokens, Devices) and "Ir para" (Conversations, Contacts, Organizations), SHALL mark the destination in view, and SHALL list "Administração" only for an instance admin; no "Settings" or "Connectors" destination SHALL remain in it. (SET-03)
4. The account and organization menus SHALL have rows at least 44 px tall and SHALL stay inside the viewport at 320 and 412 px wide with a 125% root font. (SET-04)

**Independent Test**: Opening `/settings` from a bookmark lands on `/account` with the heading "My account" (and "Minha conta" in pt-BR); the avatar menu lists "My account", "Identities", "API tokens" and "Devices" under "Account" and marks "My account" as the current page when the account page is open.

---

### P1: The organization has one home ⭐ MVP

**User Story**: As a member, I want one control next to the organization I am in that takes me to its configuration and its connections, so that I do not have to remember addresses.

**Acceptance Criteria**:

1. WHEN a member opens `/orgs/{org}/connections` THEN the system SHALL list exactly the connections whose `org_id` is that organization, SHALL offer "Add" only to an admin of it, and SHALL mark a "Connectors" tab in the organization's section bar; IF the viewer is not a member of that organization THEN the system SHALL show the existing organization-not-found page. (SET-05)
2. WHEN a signed-in user opens `/connectors` THEN the system SHALL redirect it to `/orgs/{current}/connections`, where `{current}` is the organization the switcher shows, resolved as the browser's pick, else the account's default, else the first membership. (SET-06)
3. WHEN a signed-in user opens `/connectors/{id}` THEN the system SHALL read that connection and redirect to `/orgs/{org}/connections/{id}` under the connection's own organization; WHEN the address names another organization than the connection's THEN the system SHALL redirect to the connection's own organization's address; IF the connection cannot be read THEN the system SHALL land on `/orgs`. (SET-07)
4. WHEN a connection is created from an organization's connections page THEN the system SHALL open the new connection under the organization that owns it, whatever organization the creation dialog was set to. (SET-08)
5. WHEN the viewer uses the organization entry — the gear beside the org switcher, or the switcher's own menu where the gear does not fit — THEN the system SHALL list the current organization's pages (Org chart, Members, Positions for admins, Connectors, Settings) and "All organizations", every link pointing at the organization the switcher shows. (SET-09)
6. WHEN the organization switcher's menu opens THEN the system SHALL keep its picker, each organization's role and unread count, "All organizations" and "New organization", and a pick SHALL still change the current organization for the whole app. (SET-10)
7. The system SHALL make every account page reachable from the account menu and every organization page reachable from the organization entry or the organization's section bar, so that no page exists only as a typed URL. (SET-11)

**Independent Test**: From the list screen the gear opens "Pages of Acme" with a link to Settings and to Connectors; on a phone's top bar the switcher's menu has the same links; `/connectors` lands on `/orgs/acme/connections`, a bookmarked `/connectors/k1` lands on `/orgs/acme/connections/k1`, and `/orgs/globex/connections/k1` moves to `/orgs/acme/connections/k1`.

---

### P1: The phone comes first ⭐ MVP

**User Story**: As a person on a phone, I want the new pages and menus to fit my screen with the system font enlarged, so that nothing pushes sideways or hides behind the edge.

**Acceptance Criteria**:

1. WHEN the viewport is 320×700 or 412×900 with a 125% root font THEN `/account`, `/orgs/{org}/connections`, `/orgs/{org}/settings` and both open menus SHALL fit without sideways page scroll and without text spilling out of its box. (SET-12)

**Independent Test**: `web/e2e/fit.spec.ts` walks the three pages and the two open menus at both sizes and reports no horizontal scroll and no overflowing text.

---

### P2: Nothing on the server moves; both languages stay complete

**User Story**: As a maintainer, I want the split to be a web change with complete translations, so that the API, the CLI and the stored data are untouched and no screen falls back to English.

**Acceptance Criteria**:

1. The system SHALL change no HTTP route, DTO, table or permission rule: no file under `internal/`, no `web/src/lib/api/types.gen.ts` and no behaviour described in `docs/api.md` SHALL change. (SET-13)
2. WHEN a screen, menu or dialog of this feature renders in Portuguese or in English THEN the system SHALL use a string from that language's dictionary, and the dictionaries SHALL keep exactly the same keys. (SET-14)

**Independent Test**: `git diff --stat` shows no `internal/` file and no generated TypeScript type; `web/src/lib/i18n/i18n.test.tsx` passes; the pt-BR menu test reads "Minha conta", "Identidades", "Tokens de API" and "Dispositivos".

---

## Edge Cases

- WHEN the viewer's organizations are still loading and they open `/connectors` THEN the redirect SHALL wait for the organization list (`ensureQueryData`) instead of guessing, and SHALL never bounce back to `/connectors`. (SET-06)
- IF the viewer belongs to no organization THEN `/connectors` and `/connectors/{id}` SHALL land on `/orgs`, whose page offers "New organization". (SET-06)
- WHEN the address of an organization is its slug (`/orgs/acme/connections`) THEN the page SHALL work like every other organization page: the layout replaces the slug with the id and keeps the section. (SET-05)
- WHEN a connection opened under organization B belongs to organization A THEN the page SHALL move to `/orgs/A/connections/{id}`, and a viewer who is not a member of A SHALL see the organization-not-found page after the move. (SET-07)
- IF a connection was deleted THEN `/connectors/{id}` SHALL land on `/orgs` rather than on an error screen. (SET-07)
- WHEN an organization is left while one of its pages is open THEN the existing organization-not-found page SHALL show, as it does today. (SET-05)
- WHEN a page is opened on the phone's top bar THEN the org switcher's menu SHALL scroll inside itself with the enlarged font, and its rows SHALL stay at least 44 px. (SET-04, SET-09)
- WHEN the account page redirects from `/settings` while signed out THEN the login redirect SHALL keep the original address and bring the user to `/account` after login. (SET-02)
- WHEN the browser's Back button is pressed after a redirect THEN the redirect SHALL NOT replay (a `replace` redirect leaves one entry). (SET-02, SET-06)

---

## Implicit-requirement sweep

| Dimension | Covered by |
| --- | --- |
| Input validation & bounds | No new input; `{org}` accepts an id or a slug through the existing organization layout (SET-05), `{id}` is a connection id (SET-07) |
| Failure / partial-failure states | A connection that cannot be read → `/orgs` (SET-07); no organization → `/orgs` (SET-06); a non-member → not-found (SET-05); the organizations still loading → the redirect waits (SET-06) |
| Idempotency / duplicates | Redirects use `replace`, so the history does not grow and Back does not replay them (SET-02, SET-06); visiting a moved page twice is stable |
| Auth boundaries | Unchanged on the server (SET-13); the existing client guards (`_app` session guard, the organization layout's 404, the admin route) are reused (SET-05) |
| Concurrency / ordering | None: no data is written; navigation is one router transition |
| Data lifecycle | None: nothing is stored or deleted; the browser's current-organization pick keeps its meaning (SET-10) |
| Observability | None new: no logs, metrics or events for navigation; the server logs no page visits |
| External-dependency failure | None: no outbound request is added |
| State-transition integrity | The current organization is one value resolved by one rule (SET-06, SET-10); opening a page never changes the pick; a menu closes and returns focus to its trigger |
| Accessibility and touch | Menus have group labels, the current page is marked, rows are at least 44 px, focus returns to the trigger, Escape closes (SET-03, SET-04, SET-09) |
| i18n | Every new string exists in both dictionaries; the parity test is the gate (SET-14) |
| Mobile fit | The 320/412 px, 125% fit check covers the moved pages and both menus (SET-12) |

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| SET-01 | P1: My account has one home | Execute | Pending |
| SET-02 | P1: My account has one home | Execute | Pending |
| SET-03 | P1: My account has one home | Execute | Pending |
| SET-04 | P1: My account has one home | Execute | Pending |
| SET-05 | P1: The organization has one home | Execute | Pending |
| SET-06 | P1: The organization has one home | Execute | Pending |
| SET-07 | P1: The organization has one home | Execute | Pending |
| SET-08 | P1: The organization has one home | Execute | Pending |
| SET-09 | P1: The organization has one home | Execute | Pending |
| SET-10 | P1: The organization has one home | Execute | Pending |
| SET-11 | P1: The organization has one home | Execute | Pending |
| SET-12 | P1: The phone comes first | Execute | Pending |
| SET-13 | P2: Nothing on the server moves | Execute | Pending |
| SET-14 | P2: Both languages stay complete | Execute | Pending |

**Coverage:** 14 total, 14 mapped to tasks (`tasks.md` T1-T5), status Pending (the plan is not executed yet).

---

## Success Criteria

- [ ] A bookmark of `/settings` lands on `/account`, and the heading says "Minha conta" for a Portuguese reader and "My account" for an English one.
- [ ] The avatar menu lists "Minha conta" under "Conta", with identities, tokens and devices beside it, and the current page is marked.
- [ ] The gear beside the org switcher opens the current organization's pages, and "Settings" is one click away from anywhere the gear is shown.
- [ ] On a phone's top bar, the org switcher's menu lists the same organization pages without a new control in the bar.
- [ ] `/connectors` lands on the current organization's connections; `/connectors/{id}` lands on the connection under its own organization; a connection opened under another organization moves to its own.
- [ ] The organization's section bar shows a Connectors tab, and the connections list shows only that organization's connections.
- [ ] At 320 px and 412 px with a 125% root font, `/account`, the organization's connections and settings, and both menus fit with no sideways scroll, and every menu row is at least 44 px.
- [ ] No file under `internal/` changed, no generated TypeScript type changed, and both dictionaries have the same keys.
- [ ] No page of the account or the organization is reachable only by typing its URL.

---

## Open questions for the product owner

1. **Identities and API tokens keep their own addresses.** They are listed under "Minha conta" but stay at `/identities` and `/tokens`. Confirm, or ask for the account to be one address space (`/account/identities`, `/account/tokens`) with redirects.
2. **Contacts stays a directory.** It lists the AIs of every organization plus the ones the person hosts, so it belongs neither to one organization nor to the account settings; it sits in the account menu's "Ir para" group at `/contacts`. Confirm, or say it should move under the organization.
3. **The organization's AIs have no page of their own.** They are its AI members, seen on Members and placed on the chart. Confirm, or ask for an "AIs" tab in the organization.
4. **The account page stays one page.** Its seven sections keep their order instead of becoming `/account/profile`, `/account/security` and `/account/notifications`. Confirm, or ask for the tabs (a follow-up change of its own).
5. **`/devices` keeps its address.** The CLI prints it and `TestSetupBrowserApproval` pins that URL (DEV-01), so moving it is not a web-only change. Confirm it stays, or schedule the server change.
