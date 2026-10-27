# Contract Notes — settings split

## T1: the account page's own address

The task's **Also** line named the files whose tests follow the page to `/account`. Two more test
files asserted the account's old name and address through the account menu and the command palette,
and `make check` fails while they do, so T1 changes one expectation in each — the same label and
path substitution, no new case and no weakened assertion:

- `web/src/routes/messenger-shell.test.tsx` — the header menu's destination list now reads
  `["My account", "/account"]` where it read `["Settings", "/settings"]` (SET-03: no "Settings"
  destination remains in the menu).
- `web/src/components/command-menu.test.tsx` — the palette lists the pages from `NAV_GROUPS`, so
  its page list names "My account"; T4 rewrites this file for the regrouped navigation.

`web/e2e/fit.spec.ts` still walks `/settings` in T1 on purpose: the address redirects, so the page
it checks is `/account` either way, and T5 rewrites that entry with the rest of the fit check.

The `Done when` boxes of T1 were already checked in `.specs/features/settings-split/tasks.md` at
the base commit (`866eb80`), so the commit carries no change to that file.

## T2: connections under the organization

Moving the pages made three test files outside the task's **Also** line assert a page that no
longer lives at that address. Each change follows the acceptance criteria; none weakens a case:

- `web/src/routes/_app/orgs/org-chart.test.tsx` — the section-bar order assertion gains
  "Connectors" between "Positions" and "Settings", which SET-05 requires.
- `web/src/routes/messenger-shell.test.tsx` — its `api()` gains the viewer's organization, because
  `/connectors` now resolves through `currentOrgId`; the header-menu case expects the resolved
  `/orgs/o1/connections`. The shell cases still render `/connectors` and reach the page through
  the redirect, which is SET-06.
- `web/src/routes/a11y.test.tsx` — the full-screen page and the add-connector dialog open
  `/orgs/o1/connections`, the list's new address, with the organization's layout loaded.

Two cases of the old list asserted behaviour the spec replaces: the list following the browser's
pick, and every connection shown while the organizations are unknown. SET-05 makes the address
the owner of the filter, so those cases became the SET-05/SET-06 cases (`lists only the
connections of the organization in the address`, the five redirect cases, and the admin-versus-
member Add rule) in `org-connections.test.tsx`; no assertion was dropped without its replacement.

`web/e2e/fit.spec.ts` walks `/orgs/{org}/connections` with the new list label; T5 extends the fit
check to the rest of the moved pages and both open menus.

The `Done when` boxes of T2 were already checked in `tasks.md` at the base commit (`866eb80`), so
the commit carries no change to that file.

## T3: the organization's entry

Removing the switcher's single "Org chart" item (SET-09 moves the pages into the gear) changes the
premise of one case outside the task's **Also** line. The change follows the acceptance criterion
and drops no assertion:

- `web/src/routes/_app/orgs/org-chart.test.tsx` — "is where the switcher leads" became "is where
  the organization's entry leads": it opens the gear beside the switcher ("Pages of Acme") and
  still asserts that "Org chart" lands on `/orgs/o1/chart`. The old path (the switcher's own menu)
  no longer offers that item; the compact switcher of a phone's top bar carries the whole group
  instead.

The switcher's existing cases (picker, counts, roles, storage, "New organization") are unchanged
and still pass; a new case asserts the full switchboard no longer lists the pages, and another
that the gear is absent from a phone's top bar.

## T4: the account menu's groups

Regrouping `NAV_GROUPS` changes what the avatar's menu lists, so one test file outside the task's
**Also** line had to follow SET-03. Its change replaces expectations the acceptance criterion
retires and drops no assertion:

- `web/src/routes/messenger-shell.test.tsx` — the header menu's exact destination list now reads the
  two groups in the design's order (My account, Identities, API tokens, Devices, Conversations,
  Contacts, Organizations), with no "Connectors" row; "opens the page of the chosen entry" clicks
  "My account" and still asserts the path, because the old destination is no longer in the menu.

`web/src/components/command-menu.test.tsx` is named by the task's tests-first list but not by its
**Also** line; it gains the palette case that the page list is the seven new destinations and a
connection opens under its own organization.

The `nav.connectors` string stays in both dictionaries although no row uses it: the task removes
only the two group keys, and the palette's own heading (`palette.connectors`) keeps the product's
word. The parity test covers both dictionaries.

The `Done when` boxes of T4 were already checked in `tasks.md` at the base, as T1's were, so the
commit carries no change to that file.

## T5: proof and record

Extending the fit check to `/orgs/{org}/settings` found one page that does not fit a phone's
window when the organization's name is long: the "Leave {org}" button is an `inline-flex` with
`whitespace-nowrap`, so the fit fixture's 60-character owner name (which also names their personal
organization) made the button 487 px wide and the page 503 px wide at 360 px. The smallest change
faithful to SET-12 lets that label wrap (`h-auto max-w-full py-2 text-left whitespace-normal`),
with no behaviour change. `web/src/routes/_app/orgs/$orgId/settings.tsx` is outside this task's
**Also** line; it is the one file T5 touches beyond its plan, and this note is its contract record.

Two quoted lines of `docs/agents.md` (the login notice and the footer of `partyline orgs`) still
say "Settings": they are the CLI's exact words, and T5 changes no `internal/` file or message
(SET-13). The prose around them says "My account".
