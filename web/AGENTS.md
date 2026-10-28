# Web app: how code is organised

Binding for every change under `web/`. Spec and design: `.specs/features/web-architecture/`
(`spec.md`, `design.md`, `tasks.md`, `audit.md`). Conventions of the screens: `.specs/features/web-app/conventions.md`.

Feature-Sliced Design for the layers, Atomic Design for the UI inside them. The migration from the
flat `components/`, `hooks/`, `lib/` folders (called **legacy**) is task by task: new code goes in
the layers below; code you move leaves the legacy folders.

## Layers

```
web/src/
  routes/     TanStack Router file routes: only wire a URL to a page (createFileRoute + loader + component)
  app/        providers, layouts (templates), live wiring, push registration, global styles
  pages/      one slice per screen
  widgets/    organisms: blocks of a screen that compose features and entities
  features/   one user action per slice (send-message, add-member…)
  entities/   business objects (intelligence, room, room-member, message, organization, connector)
  shared/     ui/{primitives,atoms,molecules}, api, lib, i18n: no business knowledge
  test/       test helpers; may import anything
```

A slice has optional segments `ui/` (components), `model/` (hooks, stores, pure rules, adapters),
`api/` (queries and mutations) and `lib/` (pure helpers), and a public `index.ts`.

## Import rules (checked)

1. Order, top to bottom: `routes` = `app` -> `pages` -> `widgets` -> `features` -> `entities` -> `shared`.
   A module imports only from layers strictly below its own, plus its own slice (relative paths).
2. Slices of one layer never import each other. An entity may use another entity's explicit
   cross-API `entities/<a>/@x/<b>.ts`, imported as `@/entities/a/@x/b` (by `b` only).
3. Another slice is imported only through its `index.ts`: `@/features/send-message`, never
   `@/features/send-message/model/draft-store`. `shared` has no slices: deep paths such as
   `@/shared/ui/molecules/confirm-dialog` are fine.
4. `shared/ui/primitives` imports only other primitives, `shared/lib/utils` and packages (no i18n,
   no hooks; a label it needs, like "Close", is a prop). `atoms` import primitives and
   `shared/lib`, never molecules. `molecules` import atoms, primitives, `shared/lib`, `shared/i18n`.
5. Tests (`*.test.ts(x)`) and `src/test/**` are exempt from rules 2 and 3 (they still follow the
   layer order). Tests stay next to the file they test.
6. Legacy: anything may import legacy during the migration and legacy may import anything, but
   legacy reaches a slice through its `index.ts` like everyone else.

Run the checker (it also runs in `make web-check`, so in `make check`):

```
cd web && bun run check:layers              # fails on a new violation or on a stale baseline line
cd web && bun run check:layers --prune      # after fixing violations: drop their baseline lines
```

Output is `file:line rule message`. `web/scripts/layers-baseline.txt` lists accepted legacy
violations (`file -> target rule`); it only shrinks, so never add a line to hide a new violation.
Fix the import instead (move the code down a layer, or go through the index).

## Atomic placement

- Atoms and molecules that know nothing of the app: `shared/ui/atoms`, `shared/ui/molecules`
  (primitives for shadcn/radix).
- Domain molecules (they know an intelligence, a room, a message): `entities/<noun>/ui`.
- Organisms: `widgets/<name>`. Templates (layouts): `app/layouts`. Pages: `pages/<page>`.
- The controls of one user action (dialog, form, menu item) live in that feature's `ui/`.

## Search before you create

- Before writing a component or a rule, search `web/src` for the concept (grep the noun in
  `entities/`, `shared/ui/` and the table below). If it exists, **use it**; if it almost fits,
  **extend the canonical component** (a prop, a slot) instead of making a variant beside it.
- One component per concept. Two components never share a name across the app. One exported
  component per file in `ui/` (small private helpers are fine).
- Names: files kebab-case, components PascalCase, hooks `useX`; a slice is named for what it is
  (`features/send-message`, not `features/composer`).
- A business rule (is it online, what is its app label, may this member be removed) is computed in
  one function of its entity's `model/` or `lib/` and imported everywhere.
- Mobile first: the owner uses a ~412 px phone with a large font. Rows wrap instead of
  overflowing, and touch targets stay (`pointer-coarse:size-11`).

## Canonical components and rules (one per concept)

| Concept | Canonical | Replaces (see `audit.md`) |
|---|---|---|
| Person/AI row (avatar, name, @handle, meta, sessions, actions slots) | `entities/intelligence/ui/intelligence-row` | MemberRow (member-list), ParticipantList rows, contact-row ContactRow + new-conversation ContactRow, picker Candidate/PickableCandidate, invite rows (§1.A, 1.B, 1.M) |
| Avatar / name / handle / profile link | `IntelligenceAvatar` (gains `shape: "circle" \| "square"` for groups), `IntelligenceName` (`profileLink`, `showHandle`) | GroupAvatar, hand-made `@{handle}` spans, ProfileLink wrappers (§1.B, 1.E) |
| Conversation face | `entities/room/ui/conversation-avatar` | inline copy in conversation-header (§1.E) |
| Org avatar | `entities/organization/ui/org-avatar` on a shared `shared/ui/atoms/face` base with the tint/initials from `shared/lib/avatar` | duplicated PIXELS map (§1.F) |
| Sessions of an AI | `entities/intelligence/ui/session-list` + `session-label` | member-list line, contacts.$id list, message "via :label" (§1.A) |
| Online state | `entities/intelligence/model` `useIsOnline(id, fallback)` (+ pure `withContactPresence`) | 12 sites, 3 rules (§1.C) |
| App label of an AI | `entities/intelligence/lib` `appLabelOf(profileOrAi)`: server label, then harness label, then raw id | 3 rules (§1.D) |
| Presence/status dots | `shared/ui/atoms/status-dot` (variants online/connecting/error/stopped) | connector-status DOT map, LiveIndicator dot (§1.C) |
| Confirmation | `shared/ui/molecules/confirm-dialog` (`open`, `onClose`, `title`, `body`, `confirmLabel`, `destructive`, `pending`, `onConfirm`) | 14 hand-built AlertDialogs (§1.G) |
| Form dialog shell | `shared/ui/molecules/form-dialog` | hand-built Dialog shells (§1.G) |
| Empty / not found / loading | `EmptyState`, `NotFound` (one, with title/body/action props), `ListSkeleton`, `PanelSkeleton` | 4 not-found variants, inline skeletons (§1.H) |
| Error text | `shared/api/error-text` (per-operation status→key maps) | postErrorText, editErrorText, startConversationError, formError (§1.H) |
| Row menu | `ActionMenu` + `RowMenuButton` | member-list raw DropdownMenu (§1.I) |
| Form fields / choice groups | `form-fields` (`TextField`, `TextAreaField`, `SelectField`), `ChoiceGroup` molecule | hand-made Label+NativeSelect/Input, 5 choice groups (§1.J) |
| Section shell | `PageSection` | 3 local `Section` helpers (§1.K) |
| Org member row + removal rule | `features/manage-org-member/ui/org-member-actions` + `entities/organization/model` `removalOf(member)`, `isAdminOf` | 2 MemberRow, inline admin checks (§1.L, 2.e) |
| Time | `shared/ui/atoms/relative-time` (`<time title=absolute>relative</time>`), day labels only in `shared/lib/time` | ~11 hand-written pairs, 2 day-label rules (§1.N) |
| Message helpers | `entities/message/model` (`authorDisplayName`, `chatTrust`, `platformName`, `chatTitle`) | helpers inside message-item (cycle with message-menu) (§1.O, 2.d) |
| Device predicates | `shared/lib/device` (`isNarrow`/`useIsMobile`, `isCoarsePointer`, `isTouchFirst`) | 3 files (§1.Q) |
| Pickable candidates | `entities/intelligence/model` `usePickables({ orgId })` | 3 wirings (§1.P) |
| Composer draft | `features/send-message/model/draft-store` (zustand, per room: text, files, replyTo; edit drafts per message id) | local useState + keyed remount (§4) |


Some of these land with later migration tasks (`.specs/features/web-architecture/tasks.md`); until
then the code under "Replaces" still exists and the canonical one is what new code must use.
