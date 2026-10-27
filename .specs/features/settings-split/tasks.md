# Account and Organization Settings Tasks

## Execution Protocol (MANDATORY -- do not skip)

Implement these tasks with the `tlc-spec-driven` skill: **activate it by name and follow its Execute flow and Critical Rules.** Do not search for skill files by filesystem path. The skill is the source of truth for the full flow (per-task cycle, sub-agent delegation, adequacy review, Verifier, discrimination sensor).

**If the skill cannot be activated, STOP and tell the user - do not proceed without it.**

---

**Spec**: `.specs/features/settings-split/spec.md`
**Design**: `.specs/features/settings-split/design.md`
**Status**: Draft, awaiting approval

The product owner's brief fixes the map (see the spec's Problem Statement). The design settles what the
brief left to it: `/account` as the account's address with `/settings` redirecting, the identities/tokens/devices
addresses staying put, connections moving to `/orgs/{org}/connections` with resolving redirects, the gear
beside the full org switcher and the switcher's own menu on the phone's top bar (the 320 px budget), the
regrouped account menu, and the rule that nothing on the server changes.

Each task is one commit, at most about 300 changed lines and about 5 hand-written files (renames count once),
under an hour, and is independently testable. Tests are written first and derive from the spec's acceptance
criteria. Generated files (`web/src/routeTree.gen.ts`, `web/src/lib/api/types.gen.ts`,
`internal/store/db/*`) are never hand-edited; `tsr generate` (part of `bun run build`/`typecheck`) writes the
route tree and the result is committed with the task.

## Test Coverage Matrix

> Guidelines found: `AGENTS.md` (tests derive from the spec's ACs; `make check` before every commit; English
> only; synthetic fixtures), `.specs/LESSONS.md` (L-052: a new UI surface that renders an AC-required marker
> gets its own assertion), `.specs/STATE.md` (AD-024 one list of members, AD-016 connections belong to an
> organization), `web/vite.config.ts`, `web/playwright.config.ts`, and the existing tests
> (`web/src/routes/_app/settings.test.tsx`, `web/src/routes/_app/connectors/connectors.test.tsx`,
> `web/src/components/org-switcher.test.tsx`, `web/src/routes/app-shell.test.tsx`,
> `web/src/routes/a11y.test.tsx`, `web/e2e/fit.spec.ts`).

| Code Layer | Required Test Type | Coverage Expectation | Location Pattern | Run Command |
| --- | --- | --- | --- | --- |
| Account page and its redirect | unit (Vitest + Testing Library, real route tree) | `/account` renders the seven sections; `/settings` replaces to `/account` with one history entry; both locales | `web/src/routes/_app/account.test.tsx` | `cd web && bun run test src/routes/_app/account.test.tsx` |
| Connections route and redirects | unit (real route tree) | The list shows only the address's organization; the tab is marked; Add follows the admin rule; `/connectors`, `/connectors/{id}`, foreign-org and unreadable addresses land where the ACs say | `web/src/routes/_app/orgs/org-connections.test.tsx` | `cd web && bun run test src/routes/_app/orgs/org-connections.test.tsx` |
| Organization menu and switcher | unit | The gear and the compact switcher list the same five destinations for the organization shown; Positions is admin-only; picking still switches | `web/src/components/organization-menu.test.tsx`, `web/src/components/org-switcher.test.tsx` | `cd web && bun run test src/components` |
| Account menu | unit | Two groups with the seven destinations and their hrefs; current page marked; admin-only group; 44 px rows; pt-BR labels; theme/language/logout | `web/src/components/account-menu.test.tsx` | `cd web && bun run test src/components/account-menu.test.tsx` |
| Command palette | unit | Page results use the new addresses; a connector result goes to its own organization's connections | `web/src/components/command-menu.test.tsx` | `cd web && bun run test src/components/command-menu.test.tsx` |
| Accessibility | unit (vitest-axe) | The open account menu, the open organization menu and `/account` have no violations in both locales | `web/src/routes/a11y.test.tsx` | `cd web && bun run test src/routes/a11y.test.tsx` |
| i18n | unit (type + parity test) | Both dictionaries keep exactly the same keys; the pt-BR menu reads "Minha conta", "Identidades", "Tokens de API", "Dispositivos" | `web/src/lib/i18n/i18n.test.tsx` | `cd web && bun run test src/lib/i18n` |
| End to end | e2e (Playwright, real binary; desktop 1280 and a touch phone) | Both entries reach their pages; the three old addresses redirect; the organization bar reaches connections; menu rows are ≥ 44 px | `web/e2e/navigation.spec.ts` | `make e2e` |
| Mobile fit | e2e (Playwright, 320×700 and 412×900 with a 125% root font) | `/account`, `/orgs/{org}/connections`, `/orgs/{org}/settings` and both open menus fit: no sideways scroll, no text spill | `web/e2e/fit.spec.ts` | `make e2e` |
| No server change | static | `git diff --stat` names no `internal/` path and no `types.gen.ts`; `make check` green | the commit itself | `make check` |

## Gate Check Commands

| Gate Level | When to Use | Command |
| --- | --- | --- |
| Quick (one file) | While iterating on a route or component | `cd web && bun run test src/<path>.test.tsx` |
| Quick (web) | Before moving on to the next task | `cd web && bun run test && bun run lint && bun run typecheck` |
| Build | **Before every commit** (AGENTS.md, L-029) | `make check` |
| E2E | T5, and after T1 or T2 when a route moved | `make e2e` |
| Push | Before pushing the branch | `make check-race` |

`make generate` is **not** needed: no SQL, migration or DTO changes. `bun run typecheck` (which runs
`tsr generate`) rewrites `web/src/routeTree.gen.ts`; commit it with the task that moves a route.

## Execution Plan

The tasks run in order: every task but the last touches the two i18n dictionaries and, from T1 on, the
generated route tree, so parallel worktrees would only create conflicts to resolve by hand. Each phase merges
before the next starts.

| Group | Phases | Starts after |
| --- | --- | --- |
| G0 | Phase 1 (T1) | — |
| G1 | Phase 2 (T2) | G0 merged |
| G2 | Phase 3 (T3) | G1 merged |
| G3 | Phase 4 (T4) | G2 merged |
| G4 | Phase 5 (T5) | G3 merged |

Dependency graph (every arrow is a `Depends on` of the task it points to):

```
T1 -> T4
T1 -> T5
T2 -> T3
T2 -> T4
T2 -> T5
T3 -> T4
T3 -> T5
T4 -> T5
```

### Phase 1: The account's address

T1 moves the account page to `/account` and leaves a redirect at `/settings`. It is the anchor of the map:
the account's pages keep their behaviour and get a name that belongs to them.

### Phase 2: The organization's connections

T2 moves the connections list and detail under the organization, makes the list read the organization in the
address, and turns `/connectors` and `/connectors/{id}` into resolving redirects. It depends on nothing in
T1; it comes second only because both tasks edit the i18n dictionaries and the route tree.

### Phase 3: The organization's entry

T3 adds the gear beside the full switcher and the organization pages to the compact switcher's menu. It needs
T2's connections route, or the gear would link to a page that is still under `/connectors`.

### Phase 4: The account menu

T4 moves `NAV_GROUPS` to `lib/nav.ts`, extracts the account menu and regroups it. It needs the account
address (T1) so its first destination exists, and the connections route (T2) for the palette's connector
link; it also edits `app-sidebar.tsx`, which T3 touched.

### Phase 5: Proof and record

T5 drives the real binary on a desktop and on a touch phone, extends the phone fit check to the moved pages
and both open menus, updates the addresses in the existing e2e files, and records the decision.

---

## Task Breakdown

### Phase 1: The account's address

#### T1: Give the account page its own address [web]

**What**: `git mv web/src/routes/_app/settings.tsx web/src/routes/_app/account/index.tsx`; the route id becomes
`/_app/account/`; the page's H1 and description come from the new `page.accountTitle` / `page.accountBody`
keys ("Minha conta" / "My account", and a description that names the seven sections); `nav.settings` becomes
`nav.account`. `web/src/routes/_app/settings.tsx` becomes a `beforeLoad` redirect to `/account` with
`replace: true`, exactly like `web/src/routes/_app/agents.tsx`. Every section, its copy, its validation and its
tests move unchanged.

**Where**: `web/src/routes/_app/account/index.tsx`
**Also**: `web/src/routes/_app/settings.tsx`, `web/src/routes/_app/account.test.tsx` (renamed from
`settings.test.tsx`), `web/src/lib/i18n/en.ts`, `web/src/lib/i18n/pt-BR.ts`,
`web/src/routes/app-shell.test.tsx`, `web/src/routes/a11y.test.tsx`,
`web/src/routes/_app/rooms/timeline-scroll.test.tsx`, `web/e2e/smoke.spec.ts`, `web/e2e/push.spec.ts`
**Depends on**: None
**Requirement**: SET-01, SET-02, SET-14
**Tests first** (`web/src/routes/_app/account.test.tsx`, the renamed file; the existing cases keep their
assertions and only their route path changes):
- `shows the account page at /account`: `renderRoute("/account")`; `await screen.findByRole("heading", {
  level: 1, name: "My account" })`; each of "Profile", "Appearance", "Default organization", "Notifications on
  this device", "Password", "Sessions" and "Delete account" is a heading; the description reads "Your profile,
  theme and language, the notifications of this device, your password and your sessions."
- `speaks Portuguese`: locale `pt-BR` gives the H1 "Minha conta".
- `sends /settings to /account without growing the history`: `renderRoute("/settings")`;
  `await waitFor(() => expect(router.state.location.pathname).toBe("/account"))`; the H1 is visible;
  `expect(history.length).toBe(1)`.
- the moved cases: theme in `localStorage`; language in `localStorage` and `PATCH /me`; profile save updates
  the sidebar; password change; sessions list, "this device" and revoke; the push cases; the default
  organization cases; deletion.
- `the account menu item is called My account` (`web/src/routes/app-shell.test.tsx`, rewritten here only as
  far as the label and path): `renderRoute("/account")`, open the avatar; `getByRole("menuitem", { name: "My
  account" })` has `aria-current="page"`; no `menuitem` named "Settings" exists.

**Done when**:
- [x] the seven sections render at `/account` and every moved case passes with its old expectation
- [x] `/settings` lands on `/account`, one history entry, and the address bar reads `/account`
- [x] `web/src/routeTree.gen.ts` was regenerated by `bun run typecheck` and is committed
- [x] no navigation to `/settings` remains in `web/src` except the redirect route and its test; the e2e
  files drive the account page at `/account` (T5 rewrites the fit entry)
- [x] no server or generated API change: no `internal/` file, no `web/src/lib/api/types.gen.ts`
- [x] Gate check passes: `make check`
**Tests**: web unit
**Gate**: build
**Commit**: `feat(web): give the account page its own address`

#### T2: Move connections under the organization [web]

**What**: `git mv` the connections list to `web/src/routes/_app/orgs/$orgId/connections/index.tsx` and the detail
to `web/src/routes/_app/orgs/$orgId/connections/$connectorId.tsx`. The list filters `c.org_id === orgId` from
`Route.useParams()` and takes the viewer's role from `useOrg(orgId)` (not from `useCurrentOrg`, which follows
the browser's pick); the detail page uses the address's organization for its back link and its "Connector of
{org}" sentence and, when `connector.org_id` differs from the address, replaces the URL with the connection's
own organization. `OrgBar` gains the "Connectors" tab (new key `orgs.tabConnectors`, "Conectores" /
"Connectors") between Positions and Settings. The old
`/connectors` and `/connectors/$connectorId` become redirect routes: the list uses the new
`currentOrgId(queryClient)` (in `web/src/hooks/use-current-org.ts`, sharing `resolveCurrentOrg`) and the detail
resolves `connectorQuery(id)` in `beforeLoad`; a failed read sends the viewer to `/orgs`. The add dialog
navigates with `connector.org_id`, the palette with `c.org_id`. The API is untouched: the list still reads
`GET /connectors` and filters client-side.

**Where**: `web/src/routes/_app/orgs/$orgId/connections/index.tsx`
**Also**: `web/src/routes/_app/orgs/$orgId/connections/$connectorId.tsx`,
`web/src/routes/_app/connectors/index.tsx`, `web/src/routes/_app/connectors/$connectorId.tsx`,
`web/src/routes/_app/orgs/$orgId/route.tsx`, `web/src/hooks/use-current-org.ts`,
`web/src/components/add-connector-dialog.tsx`, `web/src/components/command-menu.tsx`,
`web/src/lib/i18n/en.ts`, `web/src/lib/i18n/pt-BR.ts`,
`web/src/routes/_app/orgs/org-connections.test.tsx` (renamed from `connectors.test.tsx`),
`web/e2e/fit.spec.ts`
**Depends on**: None
**Requirement**: SET-05, SET-06, SET-07, SET-08, SET-11, SET-14
**Tests first** (`web/src/routes/_app/orgs/org-connections.test.tsx`, with `GET /me`, `GET /orgs`,
`GET /orgs/o1`, `GET /connectors` returning k1 in o1 and k2 in o2):
- `lists only the connections of the organization in the address`: H1 "Connectors"; the list's accessible
  name is "Connectors of Acme"; k1's name visible, k2's absent; the k1 link is `/orgs/o1/connections/k1`.
- `marks the Connectors tab of the organization`: at `/orgs/o1/connections`,
  `getByRole("navigation", { name: "Sections of Acme" })` has a link "Connectors" with
  `aria-current="page"`, and the same link is present at `/orgs/o1/chart`.
- `offers Add to an admin and not to a member`: `POST /connectors` is reachable from an admin's page; a
  member's page has no "Add" button.
- `shows the organization not found to a non-member`: `GET /orgs/o2` answers 404 → the page reads "This
  organization is not on your line".
- `sends /connectors to the organization the switcher shows`: `me.default_org_id = "o1"` →
  `router.state.location.pathname === "/orgs/o1/connections"`.
- `falls back to the first organization`: no default and no pick, `GET /orgs` = [o2] →
  `/orgs/o2/connections`.
- `sends a viewer with no organization to the list`: `GET /orgs` = `{items: []}` → `/orgs`.
- `sends a bookmarked connection to its own organization`: `/connectors/k1` (k1 `org_id = "o1"`) →
  `/orgs/o1/connections/k1`.
- `sends a connection opened under another organization to its own`: `/orgs/o2/connections/k1` →
  `/orgs/o1/connections/k1`.
- `sends an unreadable connection to the organizations`: `/connectors/k9` with 404 → `/orgs`.
- `opens a connection created into another organization under that organization`: from
  `/orgs/o1/connections`, create with a response `{id: "k9", org_id: "o2"}` → `/orgs/o2/connections/k9`.
- the detail page's existing cases (operator, delivery, chats, enabling, deletion) at the new address.
- `the palette still finds a connection` (`web/src/components/command-menu.test.tsx`): "ops-bot" with
  `org_id: "o1"` goes to `/orgs/o1/connections/k1`.

**Done when**:
- [x] the connections list and detail render only under `/orgs/{org}/connections`; every `/connectors`
  address is a redirect, and the list and its description name the organization
- [x] the list shows only the address's organization, Add follows the admin rule, and the tab is marked
- [x] a connection under a foreign organization moves to its own, and an unreadable one lands on `/orgs`
- [x] the pointer from a profile to `/connectors/{id}` still reaches the connection (through the redirect)
- [x] `web/src/routeTree.gen.ts` regenerated and committed
- [x] Gate check passes: `make check`
**Tests**: web unit
**Gate**: build
**Commit**: `feat(web): move connections under the organization`

### Phase 3: The organization's entry

#### T3: Open the organization's pages from the switcher [web]

**What**: the new `web/src/components/organization-menu.tsx` with one `ORG_PAGES(org)` list (Org chart,
Members, Positions for admins, Connectors, Settings, each with its icon and i18n key, every link carrying
`org.id`) and `OrganizationMenu`, a ghost gear (`Settings2Icon`, `size-11 md:size-9`, aria-label "Páginas de
{org}") whose dropdown starts with the organization's name and the viewer's role (truncated like the
account menu's header, so a 100-character organization name cannot widen the menu), then the pages, a separator
and "All organizations". It renders in `MessengerPanel`'s header beside the full `OrgSwitcher`. The switcher
shows the same `ORG_PAGES` group in its menu **when `compact`** (the phone's top bar), replacing its single
"Org chart" item; the full switcher relies on the gear beside it. New key: `nav.orgMenu` (the gear's name, "Páginas de {org}" / "Pages of {org}"), which is also the name of
the menu it opens; `orgs.tabConnectors` came with T2.

**Where**: `web/src/components/organization-menu.tsx`
**Also**: `web/src/components/org-switcher.tsx`, `web/src/components/app-sidebar.tsx`,
`web/src/lib/i18n/en.ts`, `web/src/lib/i18n/pt-BR.ts`, `web/src/components/organization-menu.test.tsx`,
`web/src/components/org-switcher.test.tsx`, `web/src/routes/a11y.test.tsx`
**Depends on**: T2
**Requirement**: SET-09, SET-10, SET-11, SET-14
**Tests first** (`web/src/components/organization-menu.test.tsx`, with `GET /me`, `GET /orgs` = Acme (admin)
and Globex (member), `GET /conversations`):
- `opens the organization's pages from the gear`: on `/` (desktop), click the button "Pages of Acme"; the menu
  has links "Org chart" → `/orgs/o1/chart`, "Members" → `/orgs/o1/members`, "Positions" → `/orgs/o1/positions`,
  "Connectors" → `/orgs/o1/connections`, "Settings" → `/orgs/o1/settings`, and "All organizations" → `/orgs`.
- `hides positions from a member`: with Acme as `member`, no "Positions" item.
- `names the organization and the role at the top`: the menu shows "Acme" and "Admin".
- `lists the same pages in the compact switcher's menu`: `stubViewport(360)`; on `/contacts`, click the
  compact switcher (named "Organization: Acme"); its menu lists the five hrefs for `o1`; after picking
  Globex, the same menu lists `/orgs/o2/...`.
- `keeps the switcher's job` (`web/src/components/org-switcher.test.tsx`, existing cases): the picker marks
  the current organization, writes the pick to storage, counts unread per organization and still offers "New
  organization"; the "Org chart" item is gone and the pages group is present only in the compact switcher.
- `speaks Portuguese`: "Páginas de Acme", "Organograma", "Membros", "Cargos", "Conectores", "Configurações",
  "Todas as organizações".
- `the open organization menu has no axe violations` (`web/src/routes/a11y.test.tsx`).

**Done when**:
- [x] both entries render the same five destinations from one `ORG_PAGES` list, always for the organization the
  switcher shows
- [x] Positions is admin-only in both, and "All organizations" is present
- [x] every switcher test that existed still passes (picker, counts, roles, new organization)
- [x] the gear exists only beside the full-width switcher; the phone's top bar gains no control
- [x] both dictionaries have the new keys
- [x] Gate check passes: `make check`
**Tests**: web unit
**Gate**: build
**Commit**: `feat(web): open the organization's pages from the switcher`

### Phase 4: The account menu

#### T4: Group the account menu by what it configures [web]

**What**: `web/src/lib/nav.ts` receives `NAV_GROUPS` and `NavItem` from `web/src/components/app-sidebar.tsx`,
regrouped into **Conta** (Minha conta `/account`, Identidades `/identities`, Tokens de API `/tokens`,
Dispositivos `/devices`), **Ir para** (Conversas `/`, Contatos `/contacts`, Organizações `/orgs`) and the
admin-only **Administração** group; `nav.groupWork` and `nav.groupConnections` go away and `nav.groupGoTo`
arrives. The avatar menu moves
to `web/src/components/account-menu.tsx` unchanged in behaviour (trigger, theme and language submenus, Log
out), gains the group labels, `min-h-11 md:min-h-9` rows and `max-w-[calc(100vw-1rem)]`; the palette imports
`NAV_GROUPS` from `lib/nav.ts`. No new sheet and no new control.

**Where**: `web/src/components/account-menu.tsx`
**Also**: `web/src/lib/nav.ts`, `web/src/components/app-sidebar.tsx`,
`web/src/components/command-menu.tsx`, `web/src/lib/i18n/en.ts`, `web/src/lib/i18n/pt-BR.ts`,
`web/src/components/account-menu.test.tsx`, `web/src/routes/app-shell.test.tsx`,
`web/src/routes/a11y.test.tsx`
**Depends on**: T1, T2, T3
**Requirement**: SET-03, SET-04, SET-14
**Tests first** (`web/src/components/account-menu.test.tsx`):
- `lists the account's pages under Conta`: on `/` (desktop), open "Account menu"; the group "Account" has
  "My account" → `/account`, "Identities" → `/identities`, "API tokens" → `/tokens`, "Devices" → `/devices`;
  the group "Go to" has "Conversations" → `/`, "Contacts" → `/contacts`, "Organizations" → `/orgs`;
  no item named "Settings" or "Connectors".
- `marks the page in view`: at `/account`, "My account" has `aria-current="page"`; at `/identities`,
  "Identities" does.
- `shows administration only to an admin`: an admin sees "Users", "Invites", "Audit"; a member sees no
  "Admin" label and none of the three.
- `speaks Portuguese`: "Minha conta", "Identidades", "Tokens de API", "Dispositivos", "Ir para", "Contatos",
  "Organizações".
- `keeps the theme, the language and the logout`: Dark sets `documentElement.dark` and `partyline.theme`;
  "Português (Brasil)" sets `partyline.locale` and sends `PATCH /me`; "Log out" sends `POST /auth/logout` and
  lands on `/login`.
- `gives every row a finger's height`: each link and the logout carry the `min-h-11` class (the e2e measures
  the boxes at 44 px, T5).
- `the open account menu has no axe violations`, in both locales (`web/src/routes/a11y.test.tsx`).
- `the palette lists the pages and a connection` (`web/src/components/command-menu.test.tsx`): "Contacts" →
  `/contacts`; a connector with `org_id: "o1"` → `/orgs/o1/connections/k1`.

**Done when**:
- [x] the account menu lists exactly the seven destinations in two groups, plus the admin group for admins
- [x] no "Settings" or "Connectors" destination remains in it, and `/account` is marked when open
- [x] the palette's page list comes from `lib/nav.ts` and its connector links use `org_id`
- [x] both dictionaries are complete and the parity test passes
- [x] Gate check passes: `make check`
**Tests**: web unit
**Gate**: build
**Commit**: `feat(web): group the account menu by what it configures`

### Phase 5: Proof and record

#### T5: Prove the split on a phone and a desktop [web]

**What**: `web/e2e/navigation.spec.ts` against the real binary: a desktop context drives the two entries and
the three old addresses; a touch context (`hasTouch: true, isMobile: true`, 360×780, as `mobile.spec.ts`
does) drives the phone's account menu on `/` and the top bar's switcher menu, and measures the menu rows.
`web/e2e/fit.spec.ts` replaces `/settings` and `/connectors` with `/account` and
`/orgs/{org}/connections`, adds `/orgs/{org}/settings` and the two open menus, at 320×700 and 412×900 with a
125% root font. `.specs/STATE.md` gains AD-030 (the design's decision record); `README.md`, `docs/agents.md`
and `docs/api.md` say "My account" where they named the page "Settings". No behaviour changes.

**Where**: `web/e2e/navigation.spec.ts`
**Also**: `web/e2e/fit.spec.ts`, `.specs/STATE.md`,
`README.md`, `docs/agents.md`, `docs/api.md`
**Depends on**: T1, T2, T3, T4
**Requirement**: SET-01, SET-02, SET-03, SET-04, SET-05, SET-06, SET-07, SET-08, SET-09, SET-10, SET-11,
SET-12, SET-13, SET-14
**Tests first**:
- desktop, the account: `page.goto("/settings")` → `toHaveURL(/\/account$/)` and the H1 "My account"; from
  `/`, the avatar menu → "My account" → `/account`; "Devices" → `/devices` whose H1 is "Approve a machine".
- desktop, the organization: the gear "Pages of Acme" → "Settings" → `/orgs/{id}/settings` with the H1
  "Organization settings"; "Connectors" → `/orgs/{id}/connections` with the Connectors tab
  `aria-current="page"`.
- desktop, the old addresses: `/connectors` → `/orgs/{id}/connections`; `/connectors/{kid}` (a connection
  created through the API) → `/orgs/{id}/connections/{kid}`; both keep one history entry (Back leaves the
  page, it does not replay the redirect).
- phone (touch context): on `/`, the avatar menu lists "Minha conta"/"My account" and reaches `/account`; on
  `/contacts`, the top bar's switcher menu ("Organization: {org}") lists the organization's pages and reaches
  `/orgs/{id}/settings`; every row of both menus has a bounding box at least 44 px tall and inside the
  viewport width.
- fit, at both sizes and 125%: `/account`, `/orgs/{org}/connections`, `/orgs/{org}/settings`, the open
  account menu on `/` and the open organization menu on `/`; `expectFits` reports no sideways scroll and no
  spilling text.
- `.specs/STATE.md` has AD-030 and no other decision changed; the three docs name "My account".

**Done when**:
- [x] `make e2e` passes for `desktop-1280`, `mobile-360` and the touch contexts
- [x] the old addresses are asserted against the real binary, not only in unit tests
- [x] the fit check covers every moved page and both open menus at 320×700 and 412×900 with a 125% font
- [x] no existing e2e test is weakened or skipped
- [x] `.specs/STATE.md` gains AD-030; `README.md`, `docs/agents.md` and `docs/api.md` name "My account"
- [x] `git diff` for the whole feature names no `internal/` path and no `web/src/lib/api/types.gen.ts`
- [x] Gate check passes: `make check` (and `make check-race` before pushing)
**Tests**: e2e
**Gate**: e2e
**Commit**: `test(e2e): prove the account and organization split`
