# Web architecture: Feature-Sliced Design + Atomic Design

Status: Done. Specify, Design (`design.md`) and Tasks (`tasks.md`, T1 to T6) are all done; the
starting point is in `audit.md` (2026-10-28), the end state in `tasks.md` (T6 audit).

## Problem

The web app (`web/src`, ~24k lines of TSX) grew as 82 flat files in `components/` plus `hooks/`,
`lib/` and fat route files. The same concept is rendered by different components that drift apart:
the group "Members" list and the DM "Participants" list are two components, so the DM one does not
show an AI's parallel sessions; "is this AI online" is computed in 12 places with 3 rules; an AI's
app label has 3 rules that disagree; 14 confirmations are hand-built although `ConfirmDialog`
exists. The owner asked (2026-10-28) to standardise on reusable components in the right layers,
using Feature-Sliced Design with Atomic Design, and to make it a culture that stays.

Two user-visible bugs come with it: the composer loses its text and attachments when the person
switches to another conversation and back, and the DM side panel does not list an AI's sessions.

## Requirements

1. The web source SHALL be organised in the FSD layers `app`, `pages`, `widgets`, `features`,
   `entities`, `shared` under `web/src/`, with `routes/` kept for the TanStack Router file routes,
   which only wire a URL to a page. (WA-01)
2. A module SHALL import only from layers strictly below its own (`app`/`routes` → `pages` →
   `widgets` → `features` → `entities` → `shared`); slices of the same layer SHALL NOT import each
   other, except an entity importing another entity's `@x` cross-API. (WA-02)
3. Code outside a slice SHALL import it only through the slice's public `index.ts`. (WA-03)
4. Inside `shared/ui`, `primitives` SHALL import nothing from `atoms`/`molecules` nor app code
   (no i18n, no hooks), `atoms` SHALL NOT import `molecules`. Organisms live in `widgets`,
   templates (layouts) in `app/layouts`, pages in `pages`. (WA-04)
5. `make check` SHALL run a layer checker (`bun run check:layers`) that fails on any new
   violation of WA-02..WA-04; during the migration a committed baseline lists the remaining legacy
   violations and the checker SHALL fail when the baseline holds an entry that no longer occurs
   (so it only shrinks). At the end of the migration the baseline SHALL be empty and the legacy
   folders `components/`, `hooks/`, `lib/` SHALL be gone. (WA-05)
6. Each concept in the canonical list of `design.md` §4 SHALL have exactly one component or rule,
   and every place that renders or computes it SHALL use it. (WA-06)
7. WHEN the person types text, attaches files or picks a message to reply to in a conversation and
   goes to another conversation or page and back THEN the composer SHALL show the same text,
   attachments and reply; a successful send SHALL clear only that conversation's draft; drafts are
   kept in memory for the browser tab (not across reloads). (WA-07)
8. The DM side panel and the group members panel SHALL render their people with the same row
   component; an online AI with a labelled live session SHALL show its sessions ("principal ·
   :pi-2") in both. (WA-08)
9. `web/AGENTS.md` (linked from the root `AGENTS.md`) SHALL state the layer rules, the atomic
   placement, the "search before you create; extend the canonical component" rule and the
   canonical list, so agents and people follow them. (WA-09)
10. Nothing else SHALL change behaviour: every existing unit test and e2e test SHALL keep passing,
    moved next to its code; screens look the same except where a duplicate is replaced by its
    canonical component. (WA-10)

## Out of scope

Visual redesign; new features; splitting the i18n dictionaries by domain (optional last task);
server changes.
