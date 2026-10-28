# Design: web architecture (FSD + Atomic Design)

## 1. Layout

```
web/src/
  routes/            TanStack Router file routes (generated tree). A route file only does
                     createFileRoute(...)({ loader, component: XPage }) and imports from pages/ and app/.
  app/               providers, router, layouts (templates), live wiring, global styles, push registration
    providers/  layouts/  live/  push/  styles/
  pages/<page>/      one slice per screen: ui/<page>.tsx, model/ (page-only state), index.ts
  widgets/<name>/    organisms: compose features + entities into a block of a screen
  features/<verb-noun>/  one user action each (send-message, add-member, edit-member-settings…)
  entities/<noun>/   business objects: intelligence, room, room-member, message, organization, connector
  shared/
    ui/primitives/   shadcn/radix primitives (button, dialog, sheet…) — no app imports, no i18n
    ui/atoms/        smallest app-agnostic pieces (StatusDot, NativeSelect, RelativeTime, Logo)
    ui/molecules/    combinations of atoms (EmptyState, ConfirmDialog, FormDialog, form fields,
                     ActionMenu, RowMenuButton, PageHeader, PageSection, CopyField, ListSkeleton)
    api/             http client, queryKeys, error text, live socket transport, generated types
    lib/             time, device, avatar tone/initials, clipboard, viewport, forms, validation, cn
    i18n/            I18nProvider, useT, dictionaries
  test/              test helpers (render, fetch, fixtures, fake socket) — may import anything
```

Slice segments (inside a slice, all optional): `ui/` (components), `model/` (hooks, stores,
pure rules), `api/` (queries/mutations of that entity or feature), `lib/` (pure helpers),
`index.ts` (public API: the only thing other slices import). Tests stay co-located
(`*.test.ts(x)` next to the file).

Atomic Design mapping: atoms + molecules = `shared/ui` (app-agnostic) and `entities/*/ui`
(domain molecules: IntelligenceAvatar, IntelligenceRow, SessionList, ConversationAvatar,
MessageAttachment…); organisms = `widgets`; templates = `app/layouts`; pages = `pages`.
A feature's `ui/` holds the controls of its action (a dialog, a form, a menu item).

## 2. Import rules (checked)

Order, top to bottom: `routes` = `app` → `pages` → `widgets` → `features` → `entities` → `shared`.

- A module imports only from layers strictly below it, plus its own slice (relative paths).
- No imports between slices of one layer. Entities may share types through
  `entities/<a>/@x/<b>.ts` (an explicit cross-API of `a` for `b`), imported as
  `@/entities/a/@x/b`.
- Another slice is imported only through its index: `@/features/send-message`, never
  `@/features/send-message/model/draft-store`. `shared` has no slices: deep paths like
  `@/shared/ui/molecules/confirm-dialog` are fine.
- `shared/ui/primitives` imports only other primitives, `shared/lib/utils` (cn) and packages;
  labels it needs (e.g. "Close") come as props with an English default overridden by the caller.
  `atoms` may import primitives and `shared/lib`; `molecules` may import atoms, primitives,
  `shared/lib` and `shared/i18n`.
- Tests and `src/test/**` are exempt from the slice rules (not from the layer order for app code).

Checker: `web/scripts/check-layers.ts`, run by `bun run check:layers` and by `make web-check`
(so by `make check`). It reads every `.ts/.tsx` under `src` (skipping generated files), extracts
static and dynamic import specifiers, resolves `@/` and relative paths to a layer/slice/segment,
and reports each violation as `file:line rule message`. `web/scripts/layers-baseline.txt` holds
the accepted legacy violations, one `file -> target rule` per line; the checker fails on a
violation that is not in the baseline AND on a baseline line that no longer occurs (stale), so
the baseline can only shrink. Legacy folders (`components/`, `hooks/`, `lib/`) are treated as
layer `legacy`: anything may import legacy during the migration (recorded in the baseline when it
crosses a rule), and legacy may import anything. The last task deletes legacy and empties the
baseline.

## 3. Naming

Files kebab-case, components PascalCase, hooks `useX`. A slice name says what it is
(`features/send-message`, not `features/composer`). One exported component per file in `ui/`,
small private helpers allowed. Two components never share a name across the app.

## 4. Canonical components and rules (one per concept)

| Concept | Canonical | Replaces (from `audit.md`) |
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

## 5. Draft store (WA-07)

`features/send-message/model/draft-store.ts`, zustand, memory only:
`drafts: Record<roomId, { text, files: AttachedFile[], replyTo: string | null }>` and
`editDrafts: Record<messageId, string>`; actions `setText`, `addFiles`, `removeFile`, `setReply`,
`clear(roomId)`, `setEditDraft`, `clearEditDraft`. `useDraft(roomId)` returns a stable empty draft
when none exists. The composer reads/writes it instead of `useState`; caret and mention state stay
local. `$roomId.tsx` keeps the `key` on the composer only if caret state needs it; the reply choice
moves into the store. A voice recording in progress is not kept (a live MediaRecorder); a stopped
one is kept as a file. Cleared on successful send/voice send; an edit draft cleared on save/cancel.

## 6. Member panel (WA-08)

`widgets/member-panel` renders the side panel for both kinds of conversation: group → room
members with settings/remove/DM actions; DM → the two participants, read-only. Both map their
data through one adapter (`entities/room-member/model` `toPersonRow(member | participant)`,
normalising `kind` vs `role`) into `IntelligenceRow` with `SessionList` for an online AI. The DM
branch gets the same loading and empty states. The DM action of a member uses `ActionMenu`.

## 7. Migration

Sequential slices, one branch each (`wnexous/web-fsd-sN`), each green on `make check` + `make
e2e` before merge; file moves are `git mv` so history follows; imports updated with the tsc
compiler as the oracle. No behaviour change except the canonical replacements listed for the
slice. After each merge the baseline shrinks. See `tasks.md`.
