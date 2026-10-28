# Tasks: web architecture

Each task is one branch, merged only when `GOMAXPROCS=4 make check SHARDS=2 GOFLAGS=-p=2` and
`make e2e` pass. Moves use `git mv`. No behaviour change beyond what the task lists. Every task
updates `web/scripts/layers-baseline.txt` (it may only shrink, except T1 which creates it) and
ticks its requirement ids here.

## T1 — Foundation + the two visible bugs (WA-01 skeleton, WA-05, WA-07, WA-08, WA-09)
- Create the empty layer folders with a short README each (what goes there, import rules).
- `web/scripts/check-layers.ts` + `bun run check:layers` + wire into `make web-check`; generate
  the initial `layers-baseline.txt`; unit tests for the checker (a violation, a stale baseline
  line, an allowed import, the `@x` exception, index-only rule).
- `web/AGENTS.md` with the rules and the canonical table (design §2–§4); link from root AGENTS.md
  and from `.specs/features/web-app/conventions.md`.
- Draft store (design §5) in `features/send-message/model`, used by the existing composer; tests:
  text + attachment + reply survive switching rooms and back; sending clears only that room.
- `entities/intelligence`: `IntelligenceRow`, `SessionList`/`SessionLabel`, `useIsOnline`,
  `appLabelOf` (moving `intelligence-avatar`, `intelligence-name`, `you-badge` there);
  `entities/room-member/model` adapter; `widgets/member-panel` used for group AND DM (design §6).
  Tests: DM panel shows "principal · :pi-2" for an online AI with a parallel session.
- Status: done. Ticked: WA-01 (skeleton), WA-05 (checker, baseline empty so far), WA-07, WA-08, WA-09.
  Notes: the baseline starts empty (no existing code broke a rule; new slices import legacy, which
  is allowed). A voice recording in progress or stopped is not kept in the draft yet (design §5
  allows a stopped one as a file); everything else of §5 is in. The DM panel reads the sessions of
  an AI from its profile (a participant carries none).

## T2 — shared layer (WA-04, WA-06 part)
- `components/ui/*` → `shared/ui/primitives` (decouple dialog/sheet/sidebar from i18n and hooks;
  delete dead card/table/scroll-area/select). Done in T2a (branch wnexous/web-fsd-t2a):
  closeLabel/touchFirst props, SidebarProvider takes isMobile + labels from the shell.
- T2a atoms/molecules moves (done): status-dot/native-select/brand → shared/ui/atoms; copy-field,
  empty-state, form-fields, page-header, page-section, row-menu-button, action-menu, confirm-dialog,
  inline-rename, emoji-grid, search-button, back-link, status-pages, live-status, attachment-preview
  → shared/ui/molecules. StatusDot takes a translated label prop; LiveIndicator takes status,
  LiveBanner takes disconnected, SearchButton takes onOpen; attachment-preview inlines its pure
  fileKindOf/formatBytes. Left in components/: see the T2a report (app-coupled molecules).
- T2a shared/lib (done): time, utils, clipboard, viewport, forms, avatar (+initials/avatarTone from
  the intelligence entity, re-exported there), one device.ts (isNarrow/useIsMobile,
  isCoarsePointer/useAutoFocus, isTouchFirst/useTouchFirst, enterSends/useEnterSends), generic
  hooks (debounced-value, document-visible, visual-viewport, long-press, disconnected).
- T2a shared/api + shared/i18n (done): client + types.gen (tygo.yaml/Makefile paths updated,
  make generate rerun); lib/api/queries stays for T3; lib/i18n moved as is.
- Remaining (T2b): new `RelativeTime` (adopt at all ~11 sites),
  `ConfirmDialog` with open/onClose (adopt at all 14 sites), `FormDialog`, `NotFound`,
  `ListSkeleton`, `ChoiceGroup`, `PageSection` adoption (3 local Sections).
  `shared/lib` (time incl. the single day-label rule, device predicates, avatar tone/initials,
  utils, clipboard, viewport, forms, validation), `shared/api` (client, queryKeys, error-text,
  socket transport, types.gen), `shared/i18n` (move only).
- Status: T2a done; T2b pending

## T3 — entities (WA-06 part)
- `entities/{intelligence, room, room-member, message, organization, connector}` with `ui/`,
  `model/`, `api/`, `lib/`; split `lib/api/queries.ts` into the entities' `api/` (keys stay in
  `shared/api`); pure helpers out of queries.ts; live handlers per entity model (wired from
  `app/live`).
- Break the message-item ↔ message-menu cycle (message helpers to `entities/message/model`);
  `conversationTitle`, `historyPlatform` to `entities/room/model`; `removalOf`, `isAdminOf`
  adopted at every site; `usePickables`.
- Status: pending

## T4 — features (WA-06 part)
- send-message (composer split: form, attachments, edit bar), mention-autocomplete,
  send-voice-message, delete-message, react-to-message, answer-question, start-conversation
  (single contact row), create-group + add-member (shared picker on IntelligenceRow),
  edit-member-settings, remove-member, conversation-actions, manage-org-member (single org member
  actions), connect-agent, add-connector, edit-profile, change-language, toggle-theme,
  push-notifications, edit-room, create-org, invite-to-org.
- Status: pending

## T5 — widgets + pages (WA-01, WA-06 part)
- widgets: app-sidebar, conversation-list, conversation-header, conversation-timeline,
  member-panel (from T1), profile-panel (also used by contacts.$id), command-palette, org-chart,
  room-files, room-settings.
- pages: every route file becomes a thin wiring of a page slice; split account (sections →
  features), connector page, tokens, identities, org members/chart (one org member row).
- Status: pending

## T6 — strict (WA-05)
- Delete `components/`, `hooks/`, `lib/`; baseline empty and the legacy layer removed from the
  checker; final doc pass. Optional: split i18n dictionaries by domain.
- Status: pending
