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
