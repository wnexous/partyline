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
