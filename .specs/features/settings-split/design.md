# Account and Organization Settings Design

**Feature**: `settings-split` · **Spec**: [spec.md](spec.md) · **Tasks**: [tasks.md](tasks.md)
**Status**: Draft for approval · **Base**: `main` at `a1f6544` (after the phone-fit fix `1f60dc2`)

## Answers to the brief's design questions

| Question | Answer |
| --- | --- |
| Which pages are "Minha conta"? | `/account` (profile, appearance, default organization, notifications on this device, password, sessions, delete account), `/identities`, `/tokens`, `/devices`. Listed in the account menu under "Conta". → [The map](#the-map) |
| Which pages are "Organização"? | `/orgs` (the list), `/orgs/{org}/chart`, `/members`, `/positions`, `/connections`, `/settings`. Listed in the organization entry. → [The map](#the-map) |
| Where do Agents/Contacts go? | `Contacts` (`/contacts`) is the directory of every organization's AIs plus the AIs the person hosts: it is neither a setting nor one organization's page, so it stays a conversation-group destination. The **organization's** AIs are its AI members, on `/orgs/{org}/members` and in the chart; no new page (AD-024: one list, no kind sections). → [The map](#the-map) |
| Where do Connectors go? | Under the organization that owns them: `/orgs/{org}/connections` and `/orgs/{org}/connections/{id}`; `/connectors` and `/connectors/{id}` redirect there. → [Connections](#connections) |
| What is ambiguous? | The default organization (a per-user preference → account), the machine-approval page `/devices` (account, but its URL is printed by the CLI → stays), Contacts (a directory → conversation group), Admin (instance administration → its own group). → [The map](#the-map) |
| The account's entry | The avatar menu, on every width, on the list screen (desktop sidebar, phone home). → [Navigation](#navigation) |
| The organization's entry | A gear beside the org switcher where the switcher is a full row (desktop sidebar ≥ 768 px, the phone's list screen); the switcher's own menu on the phone's top bar, where a fifth control does not fit. → [Navigation](#navigation) |
| The phone's drawer | There is none today: below 768 px the list is the page, other pages get a top bar, and the only sheets are a row's actions, the room settings and the profile panel (`ui/sidebar.tsx`'s sheet branch is dead code: no layout uses `SidebarProvider`). This feature adds no drawer and no new control in the top bar. → [Navigation on a phone](#navigation-on-a-phone) |
| URLs | `/settings` → `/account`; `/connectors` → the current organization's connections; `/connectors/{id}` → the connection's own organization. Everything else keeps its address. All redirects are client-side and replace. → [URLs and redirects](#urls-and-redirects) |
| Does the server change? | No. No route, DTO, migration, query or permission rule changes; the SPA fallback already serves any path (`internal/webui/webui.go:76-99`), which is how `/agents` → `/contacts` works today. → [No server change](#no-server-change) |
| How is it tested? | Vitest through the real route tree for the pages, redirects, menus and a11y; Playwright for both entries on a desktop and a touch phone, the old addresses, and the 320/412 px fit with a 125% root font. → [Tests](#tests) |

## Overview

Today:

```
avatar → Settings (7 sections)
         Identities
         API tokens
         Devices
         Conversations
         Contacts
         Organizations
         Admin

org switcher → Org chart only
              → All organizations
              → New organization

/connectors, /connectors/{id}   (filtered by the browser's current organization)
nothing names the current organization's settings
```

After:

```
avatar → Conta
         ├─ Minha conta      /account
         ├─ Identities       /identities
         ├─ API tokens       /tokens
         └─ Devices          /devices
       → Ir para: Conversas / · Contatos /contacts · Organizações /orgs
       → Administração: users · invites · audit   (admins)

org switcher → picker, roles, unread counts, “All organizations”, “New organization”
             → gear: Organização
                     ├─ Org chart   /orgs/{org}/chart
                     ├─ Members     /orgs/{org}/members
                     ├─ Positions   /orgs/{org}/positions   (admins)
                     ├─ Connectors  /orgs/{org}/connections
                     └─ Settings    /orgs/{org}/settings
```

The change is a re-parenting of pages and two menus. Nothing is fetched, written or authorized differently.

## The map

| Page | What it is | Today | After | Owner | Why |
| --- | --- | --- | --- | --- | --- |
| Account page | Profile, appearance + language, default organization, notifications on this device, password, sessions, delete account | `/settings` | `/account` (H1 "Minha conta") | Minha conta | All seven sections are the person's: the language, the theme, the default organization and the device notification state follow the account, not the organization |
| Verified identities | Chat accounts proven to be the person's | `/identities` | `/identities` (unchanged) | Minha conta | The identity is the person's; the organization's connection is only the channel that verifies it (AD-008) |
| API tokens | Agent and script credentials | `/tokens` | `/tokens` (unchanged) | Minha conta | A token acts as the person |
| Approve a machine | The page `partyline setup --browser` prints | `/devices` | `/devices` (unchanged) | Minha conta | A login links a computer to the person; the URL is printed by the CLI and pinned by a CLI test (DEV-01), so it is not a web-only move |
| Organizations list | Every membership, invitations, "new organization" | `/orgs` | `/orgs` (unchanged) | Organização | It is the picker of organizations, not a setting of one |
| Org chart | Positions and who holds them | `/orgs/{org}/chart` | unchanged | Organização | — |
| Members | The organization's users and AIs | `/orgs/{org}/members` | unchanged | Organização | This is where the organization's AIs are |
| Positions | The chart's editor (admins) | `/orgs/{org}/positions` | unchanged | Organização | — |
| Connections | The platform accounts of one organization | `/connectors`, `/connectors/{id}` (filtered by the browser's pick) | `/orgs/{org}/connections`, `/orgs/{org}/connections/{id}` | Organização | They belong to the organization in the API (`Connector.org_id`, AD-016); a URL and a page that cannot disagree with the data |
| Organization settings | Name, address, picture, leave, delete | `/orgs/{org}/settings` | unchanged | Organização | — |
| Contacts | The AIs of every organization, the ones the person hosts, users and connector people | `/contacts` | unchanged | Conversas | A directory, not a setting; it spans organizations, so neither owner fits |
| Admin | Instance accounts and invites | `/admin/*` | unchanged | Admin | Instance administration is neither the account's nor an organization's configuration |
| Conversations | Rooms and DMs | `/`, `/rooms/{id}` | unchanged | Conversas | — |

Two ambiguous rows deserve the answer in words:

- **"Agents: my AIs vs the organization's AIs."** There is no "my AIs" page and no agents page at all (`/agents` has redirected to `/contacts` since T47). The AIs the person hosts appear on Contacts under "Hosted"; the AIs of an organization are its AI members, on Members and on the chart. The brief's "its AIs" is therefore answered by the organization's two existing pages, and no page is created: a second list would contradict AD-024's one-list rule and would need a new API filter.
- **"Default organization"** is on the account page because it is a column of `users` (`default_org_id`) that decides which organization the app opens in; the organization's own settings are its name, address and picture, at `/orgs/{org}/settings`.

## Navigation

### Desktop (≥ 768 px)

The sidebar header gains one control and keeps everything else:

```
┌ sidebar ───────────────────────────────┐
│ partyline                    ⌕   (me)  │   (me)  = avatar → account menu
│ ┌──────────────────────────┐ ┌──────┐  │
│ │ ◉ Acme               ⌄   │ │ ⚙    │  │   ⚙ = gear → organization menu
│ └──────────────────────────┘ └──────┘  │
│ ⌕ search conversations                 │
│ [+ New conversation] [+ New group]     │
│ conversation list …                    │
│ ● Live                                 │
└────────────────────────────────────────┘
```

- **Account menu (avatar).** Groups, in this order: **Conta** (Minha conta, Identidades, Tokens de API, Dispositivos), **Ir para** (Conversas, Contatos, Organizações), **Administração** (users, invites, audit; admins only). Then the theme and language submenus, then Log out. The destination in view carries `aria-current="page"` through the link's `data-[status=active]` state, as today.
- **Organization menu (gear, aria-label "Páginas de {org}").** A labelled menu that starts with the organization's name and the viewer's role, then **Organograma**, **Membros**, **Cargos** (admins only), **Conectores**, **Configurações**, a separator and **Todas as organizações**. Every link uses `org.id`, so it always names the organization the switcher shows.
- The switcher keeps its job: the picker with each organization's role and unread count, "Todas as organizações" and "Nova organização". Its single "Organograma" item moves into the gear's menu on a wide screen; the compact switcher of the phone's top bar gains the whole group instead, because the bar has no room for a gear (see below).

### Navigation on a phone (< 768 px)

Read from the code: the shell renders no sidebar below 768 px (`web/src/routes/_app.tsx:75`); `/` is the conversation list screen with the full switcher and the avatar; every other page gets the top bar — back, wordmark, compact org switcher, search (`web/src/routes/_app.tsx:103-126`). The account menu exists only on `/`. The app's bottom sheets belong to a row's actions (`web/src/components/action-menu.tsx`), the room settings and the profile panel; `web/src/components/ui/sidebar.tsx:183-205` has a sheet branch, but no shell uses `SidebarProvider`, so it is dead code. **A page drawer does not exist**, and this feature does not invent one: the list screen is already the home, and a drawer would re-implement theme and language, which the account menu's submenus already own.

What the phone gets instead:

- On `/`: the avatar (account menu) and the gear beside the full switcher (organization menu), exactly as on the desktop sidebar. There is room: the row is a full-width column.
- On every other page: the compact org switcher in the top bar **is** the organization's entry. Its menu keeps the picker and its counts and, below them, the same organization pages (Organograma, Membros, Cargos for admins, Conectores, Configurações, Todas as organizações). The account page stays one back-tap away (`/` → avatar), which is the number of taps it takes today.
- Both menus stay dropdown menus on touch, with rows of `min-h-11` (44 px) and `md:min-h-9` for pointers, following `OrgItem`'s existing rule. The account menu keeps its theme/language submenus, which already work on touch: converting it to a sheet would require re-implementing those radio groups outside a `DropdownMenu` and would add a second navigation pattern for no gain.

### The 320 px budget

At a 125% root font `size-11` is 55 px (2.75 rem) and the top bar already holds four fixed controls (back, brand, org switcher, search) with the wordmark between them; `web/e2e/fit.spec.ts` proves it fits 320×700 today. A fifth 55 px control plus its gap needs at least 60 px the bar does not have, and nothing else in the bar may go (the back arrow is the way home, the search is the palette on a phone). Hence the rule: **the gear exists only where the switcher is a full row**; on the top bar the switcher itself carries the organization's pages. The fit test opens that menu at 320 px and 412 px with the enlarged font, so the rule is measured, not assumed.

### Labels and accessibility

- Distinct accessible names: the switcher keeps `orgs.switcher` ("Organização: {org}" / "Organization: {org}"); the gear is `nav.orgMenu` ("Páginas de {org}" / "Pages of {org}"), and that name is also the name of the menu it opens; the avatar keeps `shell.userMenu` ("Menu da conta" / "Account menu").
- Group headings inside the account menu are `DropdownMenuLabel`s, not headings, so the menu's own name stays the only one announced; the organization menu's first label is the organization's name.
- The menu closes with Escape and returns focus to its trigger (Radix default); it is a menu, not a dialog, so no focus trap is introduced, and its links are real `<Link>`s, so a destination can be opened in a new tab and the router marks the current page.

## URLs and redirects

| From | To | When | How |
| --- | --- | --- | --- |
| `/settings` | `/account` | Always | `beforeLoad` redirect, `replace` |
| `/connectors` | `/orgs/{current}/connections` | Signed in with at least one organization | `beforeLoad`: `currentOrgId(queryClient)`, `replace` |
| `/connectors` | `/orgs` | Signed in, no organization | Same redirect, no id |
| `/connectors/{id}` | `/orgs/{org}/connections/{id}` | The connection can be read | `beforeLoad`: `ensureQueryData(connectorQuery(id))`, `replace` |
| `/connectors/{id}` | `/orgs` | The connection cannot be read (404/403/network) | Same redirect, on error |
| `/orgs/{other}/connections/{id}` | `/orgs/{org}/connections/{id}` | The connection belongs to another organization | The page compares `connector.org_id` with the address's organization and replaces the URL |

```tsx
// web/src/routes/_app/settings.tsx — the account page moved (SET-02)
export const Route = createFileRoute("/_app/settings")({
  beforeLoad: () => {
    throw redirect({ to: "/account", replace: true });
  },
});
// Same shape as web/src/routes/_app/agents.tsx, the app's existing redirect.
```

```tsx
// web/src/routes/_app/connectors/index.tsx — the list is the organization's now (SET-06)
export const Route = createFileRoute("/_app/connectors/")({
  beforeLoad: async ({ context }) => {
    const orgId = await currentOrgId(context.queryClient);
    throw redirect(
      orgId
        ? { to: "/orgs/$orgId/connections", params: { orgId }, replace: true }
        : { to: "/orgs", replace: true },
    );
  },
});
```

```tsx
// web/src/routes/_app/connectors/$connectorId.tsx — the connection names its organization (SET-07)
export const Route = createFileRoute("/_app/connectors/$connectorId")({
  beforeLoad: async ({ context, params }) => {
    try {
      const c = await context.queryClient.ensureQueryData(connectorQuery(params.connectorId));
      throw redirect({
        to: "/orgs/$orgId/connections/$connectorId",
        params: { orgId: c.org_id, connectorId: c.id },
        replace: true,
      });
    } catch (err) {
      if (isRedirect(err)) throw err;
      throw redirect({ to: "/orgs", replace: true });
    }
  },
});
```

```ts
// web/src/hooks/use-current-org.ts — the switcher's rule, outside React (SET-06)
export async function currentOrgId(queryClient: QueryClient): Promise<string | undefined> {
  const [me, orgs] = await Promise.all([
    queryClient.ensureQueryData(meQuery()),
    queryClient.ensureQueryData(orgsQuery()),
  ]);
  return resolveCurrentOrg(toMyOrgs(orgs), useLiveStore.getState().currentOrgId, defaultOrgIdOf(me))
    ?.id;
}
```

`{org}` accepts an id or the slug on every organization page already (`web/src/routes/_app/orgs/$orgId/route.tsx:34-42` replaces the slug with the id); the redirects always write the id. No server route is added: the Go handler serves `index.html` for any unknown path, which is why `/agents` already works.

`replace: true` on every redirect keeps one history entry, so Back does not replay it and the account page cannot loop with `/settings`.

### Canonical addresses for a connection

The detail page reads the organization from the address, not from the browser's pick:

```tsx
// web/src/routes/_app/orgs/$orgId/connections/$connectorId.tsx
const { orgId, connectorId } = Route.useParams();
const org = useOrg(orgId).data;               // the layout has loaded it
const connector = useConnector(connectorId).data;
useEffect(() => {
  if (org && connector && connector.org_id !== org.id) {
    void navigate({
      to: "/orgs/$orgId/connections/$connectorId",
      params: { orgId: connector.org_id, connectorId: connector.id },
      replace: true,
    });
  }
}, [org, connector, navigate]);
```

The page's "Connector of {org}" sentence and its back link use `org` and `/orgs/{org}/connections`, so a page can no longer name an organization that does not own the connection. The connections list filters on `c.org_id === orgId` (the API is unchanged; the viewer receives only the connections they may see, `GET /connectors`).

## Page structure

### `/account`

One page, the same seven sections in the same order, a new heading and a description that names what is there:

| Section | Component moved unchanged | Depends on |
| --- | --- | --- |
| Profile | `ProfileSection` (`ProfileEditor`, `PROF-03`) | — |
| Appearance (theme, language) | `AppearanceSection` (`UI-23`, `UI-25`) | — |
| Default organization | `DefaultOrgSection` (`ORG-04`) | — |
| Notifications on this device | `NotificationsSection` (`PUSH-18`) | the push routes |
| Password | `PasswordSection` | — |
| Sessions | `SessionsSection` (`UI-19`) | — |
| Delete account | `DeleteAccountSection` (`POS-06`) | — |

The H1 is `page.accountTitle` ("Minha conta" / "My account") and the description is `page.accountBody` ("Seu perfil, o tema e o idioma, as notificações deste dispositivo, sua senha e suas sessões." / "Your profile, theme and language, the notifications of this device, your password and your sessions."). Everything inside keeps its current copy, validation and tests: the move is `git mv web/src/routes/_app/settings.tsx web/src/routes/_app/account/index.tsx`, the route id, and the two strings.

### The organization's section bar

`OrgBar` gains one tab between Positions and Settings:

```tsx
<Link to="/orgs/$orgId/connections" params={{ orgId: org.id }} className={TAB}>
  {t("orgs.tabConnectors")}
</Link>
```

Order: Org chart → Members → Positions (admins) → **Connectors** → Settings. The label reuses the product's word ("Conectores" / "Connectors"); "connections" stays the API's word.

### Connections

- List (`/orgs/{org}/connections`): the existing page, with `useCurrentOrg` replaced by `Route.useParams().orgId` and `useOrg(orgId)`; the tenant filter becomes `c.org_id === orgId`; Add is offered only when the viewer is an admin of `org` (the dialog's own `OrgSelect` still lists their organizations, defaulting to the one in the address). Two strings stop speaking for the person and speak for the organization: the list's accessible name becomes `connectors.listLabelOrg` ("Connectors of {org}" / "Conectores de {org}") and the description becomes "The WhatsApp, Telegram, Discord and webhook accounts of this organization." ("As contas de WhatsApp, Telegram, Discord e webhook desta organização.").
- Detail (`/orgs/{org}/connections/{id}`): the existing page under the organization layout, so the organization's bar and identity sit above it; it uses `org` from the address for its sentences and back link, and moves itself to the connection's own organization when they differ.
- Creation: the dialog navigates with `connector.org_id`, so a connection created into another organization opens under that organization (SET-08).

## Components

### `web/src/lib/nav.ts` (new)

`NAV_GROUPS` moves out of `web/src/components/app-sidebar.tsx:77-107` so the account menu, the palette and the tests can import it without a circular dependency. The `to` union loses `/settings` and `/connectors` and gains `/account`; the groups become:

| Group label | Items |
| --- | --- |
| `nav.groupAccount` ("Conta" / "Account") | `nav.account` `/account` · `nav.identities` `/identities` · `nav.tokens` `/tokens` · `nav.devices` `/devices` |
| `nav.groupGoTo` ("Ir para" / "Go to") | `nav.conversations` `/` (exact) · `nav.contacts` `/contacts` · `nav.organizations` `/orgs` |
| `nav.admin` ("Administração" / "Admin"), admin only | users · invites · audit |

`nav.groupWork` and `nav.groupConnections` disappear with the old grouping: the new "Go to" group takes their destinations, and the old "Connections" group — which is what mixed contacts, connectors, identities and devices in the first place — is gone.

### `web/src/components/account-menu.tsx` (new, extracted from `app-sidebar.tsx:200-278`)

`AccountMenu({ me })` keeps the avatar trigger and the dropdown with the theme/language submenus and Log out; it renders `NAV_GROUPS` with the group labels. Its rows get `min-h-11 md:min-h-9`, and the content gets `max-w-[calc(100vw-1rem)]` so a 125% font at 320 px cannot push it off screen. The link's active state stays `data-[status=active]` with `aria-current` (TanStack's `Link` already sets it).

### `web/src/components/organization-menu.tsx` (new)

- `ORG_PAGES(org)`: the five destinations (icon, `MessageKey`, `to`, `params`, `adminOnly` for positions) — one list, used by the gear and by the compact switcher, so the two can never disagree.
- `OrganizationMenu({ org })`: a ghost icon button (`size-11 md:size-9`, `aria-label={t("nav.orgMenu", { org: org.name })}`, `Settings2Icon`) and a `DropdownMenu` whose first label is the organization's name and the viewer's role (both `truncate`, so a 100-character name cannot widen the menu), then the pages, a separator and "All organizations".
- Rendered in `MessengerPanel`'s header next to the full `OrgSwitcher` (`web/src/components/app-sidebar.tsx:154`), which is the desktop sidebar and the phone's list screen.

### `web/src/components/org-switcher.tsx`

The dropdown keeps everything it has and, when `compact` (the phone's top bar), adds the same `ORG_PAGES` group below the picker and above "All organizations"; the full-width switcher does not, because the gear beside it is that entry. The switcher's own item "Org chart" is replaced by the group, so nothing is lost and nothing is listed twice in one menu.

### `web/src/components/command-menu.tsx`

Imports `NAV_GROUPS` from `lib/nav.ts`; its connector results navigate to `/orgs/$orgId/connections/$connectorId` with `c.org_id`, so the palette keeps finding every connection whatever the current organization is. No other palette change.

### Existing links that keep working

`web/src/routes/_app/contacts.$id.tsx:416,462` and `web/src/routes/_app/profile.test.tsx` link to `/connectors/{id}`; they are left as they are and follow the resolving redirect. Nothing else in `web/src` links to `/settings` or `/connectors` except the routes themselves.

## No server change

| Area | Change |
| --- | --- |
| `internal/` (API, store, CLI, webui) | none |
| `docs/api.md` | one UI name: "a device revoked in Settings" becomes "in My account"; no route, status code or behaviour |
| `web/src/lib/api/types.gen.ts` | none; no `make generate` needed |
| Migrations, queries, DTOs | none |

The only Go-adjacent fact used is that the SPA fallback serves `index.html` for any path (`internal/webui/webui.go:76-99`), which makes client-side redirects sufficient.

## Tests

Written first, from the spec's acceptance criteria. Values are the expected outcomes.

| Layer | File | Cases and expected values |
| --- | --- | --- |
| Account page | `web/src/routes/_app/account.test.tsx` (renamed from `settings.test.tsx`) | H1 "My account" at `/account`; the seven section headings; pt-BR H1 "Minha conta"; every existing settings case (theme, language, profile, password, sessions, push, default org, delete) unchanged at the new route |
| Account redirect | same file | `renderRoute("/settings")` → `router.state.location.pathname === "/account"`; `history.length === 1` (replace); the H1 is visible |
| Connections page | `web/src/routes/_app/orgs/org-connections.test.tsx` (renamed from `connectors.test.tsx`) | `/orgs/o1/connections` lists k1 (o1) and not k2 (o2); the "Connectors" tab is `aria-current="page"`; Add is hidden for a member and shown for an admin; a non-member reads "This organization is not on your line" |
| Connection redirects | same file | `/connectors` → `/orgs/o1/connections` (default o1); with no default → the first organization; with no organization → `/orgs`; `/connectors/k1` → `/orgs/o1/connections/k1`; `/orgs/o2/connections/k1` → `/orgs/o1/connections/k1`; `/connectors/k9` (404) → `/orgs` |
| Connection creation | same file | creating from `/orgs/o1/connections` with a response whose `org_id` is `o2` opens `/orgs/o2/connections/k9` |
| Account menu | `web/src/components/account-menu.test.tsx` | groups "Account" and "Conversations" with the seven destinations and their hrefs; "My account" marked at `/account`; no "Settings"/"Connectors"; admin group only for admins; pt-BR labels; theme/language/logout still work; rows carry `min-h-11` |
| Organization menu | `web/src/components/organization-menu.test.tsx` | the gear opens "Pages of Acme" with `/orgs/o1/{chart,members,positions,connections,settings}` and `/orgs`; Positions absent for a member; the compact switcher's menu lists the same links for the organization it shows; a pick still changes the organization |
| Palette | `web/src/components/command-menu.test.tsx` | "Contacts" goes to `/contacts`; a connector result goes to `/orgs/o1/connections/k1` |
| A11y | `web/src/routes/a11y.test.tsx` | the open account menu, the open organization menu and `/account` have no axe violations in both locales |
| i18n | `web/src/lib/i18n/i18n.test.tsx` | both dictionaries have identical keys; the pt-BR menu test reads "Minha conta", "Identidades", "Tokens de API", "Dispositivos" |
| End to end | `web/e2e/navigation.spec.ts` (new) | desktop: `/settings` → `/account`; avatar → My account; gear → Settings → `/orgs/{id}/settings`; `/connectors` → `/orgs/{id}/connections`; a bookmarked `/connectors/{kid}` → `/orgs/{id}/connections/{kid}`; phone (touch context): the avatar menu on `/` reaches `/account`, the top bar's switcher menu reaches the organization's Settings; every menu row's box is ≥ 44 px |
| Fit | `web/e2e/fit.spec.ts` | at 320×700 and 412×900 with a 125% root font: `/account`, `/orgs/{org}/connections`, `/orgs/{org}/settings`, the open account menu and the open organization menu; no sideways scroll, no text spill |
| Static check | `git diff --stat`, `make check` | no `internal/` path and no `types.gen.ts` in the diff; `make check` green |

## Code Reuse Analysis

| Existing component | Location | How the feature uses it |
| --- | --- | --- |
| Redirect pattern | `web/src/routes/_app/agents.tsx` | `/settings` and the two `/connectors` routes copy its `beforeLoad` + `redirect(..., replace)` shape |
| Current-organization rule | `web/src/hooks/use-current-org.ts`, `web/src/lib/orgs.ts:87-95` | `currentOrgId(queryClient)` is the same `resolveCurrentOrg` the switcher uses; no second rule |
| Connection query | `web/src/lib/api/queries.ts` (`connectorQuery`) | The `/connectors/{id}` redirect reads the connection through the same cache the page uses |
| Organization bar and tabs | `web/src/routes/_app/orgs/$orgId/route.tsx` (`TAB`, `OrgBar`) | The Connectors tab is one more `Link` with the same class; the moved pages inherit the bar |
| Organization avatar and role labels | `web/src/components/org-avatar.tsx`, `web/src/lib/orgs.ts` (`permissionLabel`) | The organization menu's header and the switcher's rows |
| Menu rows | `web/src/components/org-switcher.tsx` (`OrgItem`, `min-h-11 md:min-h-9`) | The account menu's rows take the same touch size |
| Account menu markup | `web/src/components/app-sidebar.tsx:200-278` | Moved, not rewritten: same trigger, submenus and logout |
| Palette | `web/src/components/command-menu.tsx` | Keeps listing `NAV_GROUPS`; only the connector link uses `org_id` |
| Test helpers | `web/src/test/render.tsx`, `web/src/test/fetch.ts`, `web/src/test/media.ts` | `renderRoute`, `mockApi`, `stubViewport`, `stubPointer` drive every new unit test |
| E2E helpers | `web/e2e/fixtures.ts`, `web/e2e/helpers.ts` | `startInstance`, `loginAs`, `apiCall`, `expectFits`, `settled`; the phone test creates a `hasTouch` context as `mobile.spec.ts` does |

## Error Handling Strategy

| Scenario | Handling | What the user sees |
| --- | --- | --- |
| A connection cannot be read for `/connectors/{id}` | `ensureQueryData` throws; the route redirects to `/orgs` | The organization list, with an invitation to pick one |
| The viewer belongs to no organization at `/connectors` | Redirect to `/orgs` | The organization list with "New organization" |
| The address names an organization the viewer is not in | The organization layout shows its existing not-found page | "This organization is not on your line" |
| The address names another organization than the connection's | Replace the URL with the connection's own organization | The same connection, under its own organization |
| The organization is left while its page is open | The layout's query answers 404 | The organization-not-found page |
| A redirect during sign-out | The `_app` guard runs first; the login redirect keeps the original address | Login, then `/account` |
| A menu's destination does not exist (old bookmark) | The client routes' not-found page | "Nothing on this line" with a way back |
| A moved page is opened with the organizations still loading | `ensureQueryData` waits; no redirect to `/orgs` and no bounce | The page, a moment later |

## Risks & Concerns

| Concern | Location | Impact | Mitigation |
| --- | --- | --- | --- |
| A moved page keeps reading the browser's current organization | `web/src/routes/_app/connectors/*` | The URL and the page could disagree | Both pages read `orgId` from the route and filter/compare against it; a test opens a connection under the wrong organization |
| `/connectors/{id}` becomes a two-request address | `connectors/$connectorId.tsx` (old) | An old bookmark costs one `GET` before the redirect | `ensureQueryData` puts the connection in the cache, so the moved page renders it at once; the address is canonical after the first visit |
| A redirect loop between `/settings` and `/account` | route definitions | The page never loads | One direction only, and `replace`; a test asserts the final path and the single history entry |
| The account menu gets long on a phone | `account-menu.tsx` | It might push off screen at 125% | Rows stay 44 px; `max-w` + Radix's available-height scrolling; the name and role truncate; the fit test opens it at 320 and 412 px |
| The gear disappears on the phone's top bar | `web/src/routes/_app.tsx` | The owner may look for it there | The switcher's menu carries the same pages and the design explains the 320 px budget; the phone e2e reaches the organization's Settings from the top bar |
| A connection created into another organization navigates away from the page | `add-connector-dialog.tsx` | The admin may be surprised | The new connection opens under the organization that owns it, which is the rule the list follows (SET-08) |
| Two menus list the same organization pages on a wide screen | `org-switcher.tsx` vs the gear | Duplication | Only the compact switcher lists them; the full one relies on the gear beside it |
| A moved route's deep link has no test | every redirect | A 404 ships | T1 and T2 add the redirect cases before the pages move; T5 repeats them against the real binary |
| The `nav` keys are renamed | `web/src/lib/i18n/*` | A stale key breaks a menu item | The dictionary type makes a missing key a compile error; `i18n.test.tsx` keeps the key sets equal |

## Alternatives considered

| Alternative | Why not |
| --- | --- |
| Keep `/settings` and only rename the label | The address is the product's vocabulary; `/account` says whose page it is and pairs with `/orgs/{org}/settings`. A redirect is two lines and one test |
| Move `/identities`, `/tokens` and `/devices` under `/account` | Three more redirects, one per page, plus a CLI change for `/devices`, for a space nobody types; the "Conta" group already gives them one home in the UI |
| Split the account page into `/account/profile`, `/account/security`, `/account/notifications` | It moves ~800 lines and their 765-line test file for a second, different split; the owner asked for the account-vs-organization split first |
| A separate "AIs" page per organization | AD-024's one list; the members page and the chart already answer "which AIs are here and where" |
| Keep `/connectors` and only add a link from the organization | The page's own filter already depends on the browser's pick, so the URL and the data disagree; the address is the cheapest place to fix that |
| Put the gear in the phone's top bar | A fifth 55 px control does not fit 320 px with a 125% font (The 320 px budget); the switcher's menu is already there |
| A new page drawer on the phone | The drawer duplicates the list screen and re-implements theme/language and logout, which the account menu's submenus own; the brief's owner described a drawer that the code does not have |
| Server-side 301 redirects | The app is a single-page app served for every path; a server rule would be a second redirect mechanism for no benefit, and would need Go changes the brief rules out |
| Put the organization pages in the command palette only | The palette is a search, not an entry; it would leave the organization without a visible control |

## Operations and rollback

- The change is a set of web routes and components; there is no data migration, no configuration and no new secret. Rolling back is reverting the commits: the old URLs are the source of the redirects, so a revert removes them and restores `/settings` and `/connectors` in one step.
- No cache or bookmark migration is needed beyond the redirects: a bookmark of `/settings` or `/connectors/{id}` resolves on every load, and the canonical address is written into the bar by the replace.
- The `web` build is part of `make check` and `make build`; the Docker image needs no change.

## Decision record (proposed)

When the plan is approved, record one AD in `.specs/STATE.md` (AD-030, the next free number): *the account and the organization are two places in the web app — `/account` owns the person's settings and `/settings` redirects to it, a connection lives at its organization's address (`/orgs/{org}/connections`), and every move is a client-side replace redirect so no HTTP route changes.* T5 adds it with the end-to-end proof.

## Open questions

See [spec.md](spec.md#open-questions-for-the-product-owner): the five rows marked `n` in the assumptions table (identities and tokens keeping their addresses, the `/devices` address, contacts placement, the organization's AIs page, and the single account page) are the only ones awaiting the owner.
