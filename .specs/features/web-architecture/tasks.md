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
- T2b (done): `shared/ui/atoms/relative-time` (adopted at the 12 sites of audit 1.N; the timeline
  day label is `shared/lib/time` `formatDayLabel`), `shared/ui/molecules/confirm-dialog` (open/onClose,
  adopted at all 14 sites), `form-dialog` (new org, merge AI, move member, invite),
  `not-found` (four variants; `org-not-found.tsx` deleted), `list-skeleton` and `panel-skeleton`,
  `choice-group` (account appearance, language, push group; identities connector pick),
  `PageSection` layouts `stacked` and `list` (the three local Sections), SelectField in move member.
- T2b not adopted (behaviour would change or the molecule does not fit): ThemeRadioGroup and
  LanguageRadioGroup (dropdown menu radio items, role menuitemradio, not radio cards); org-select
  (controlled by its callers, one outside react-hook-form) and the devices code input (own
  attributes and local error); RoomSkeleton (a header, not a panel).
- Status: T2a done; T2b done (see the list above for what stayed)

## T3 — entities (WA-06 part)
- `entities/{intelligence, room, room-member, message, organization, connector}` with `ui/`,
  `model/`, `api/`, `lib/`; split `lib/api/queries.ts` into the entities' `api/` (keys stay in
  `shared/api`); pure helpers out of queries.ts; live handlers per entity model (wired from
  `app/live`).
- Break the message-item ↔ message-menu cycle (message helpers to `entities/message/model`);
  `conversationTitle`, `historyPlatform` to `entities/room/model`; `removalOf`, `isAdminOf`
  adopted at every site; `usePickables`.
- Status: done. Notes (what moved, what stayed):
  - api: `lib/api/queries.ts` is gone. Keys in `shared/api/query-keys`; cache shapes (MessagePages,
    FilePages, PeoplePages, FileFilter) in `shared/api/cache-types`; `items` in `shared/api/list`;
    avatar upload in `shared/api/avatar-upload`. Entities own their api: room, message,
    intelligence (profile, contacts, agents), organization (orgs, chart, invitations), connector,
    account (session, push, tokens, admin, identities, devices, activity), room-member. Cross-entity
    needs go through `@x` files (room→message, message→room, intelligence→room/organization,
    organization→account/intelligence, account→organization).
  - `useAcceptInvitation`/`useDeclineInvitation` removed: `useAnswerInvitation(verb)`.
  - live: `lib/live` is gone. `app/live` holds the socket, `startLive`, the dispatcher
    (`dispatch.ts`) and catch-up. Each entity's `model/live.ts` patches its own caches. The store
    is split by owner: `shared/api/socket-store` (status, outage), `shared/lib/prefs-store` (theme,
    locale, palette), `entities/intelligence/model/presence-store`, `entities/connector/model/state-store`,
    `entities/account/model/linked-identity-store`, `entities/room/model/open-room-store`,
    `entities/organization/model/current-org-store`. `test/live-store.ts` is a facade for tests.
  - message: helpers in `entities/message/model` (author, platform, excerpt in lib), TrustBadge,
    AttachmentView, ReplyContext, AudioPlayer, ImageViewer, Markdown (`LazyMarkdown`) in
    `entities/message/ui`. `formatBytes` in `shared/lib/bytes`. `platformLabel` is gone: one
    `platformName`. `components/question-bubble.tsx` is NOT split (it has answering logic: T4).
  - room: conversations, avatar (`ConversationFace`/`ConversationAvatar`; `GroupAvatar` deleted,
    `shape="square"` on IntelligenceAvatar), `conversationTitle`, `historyPlatform`,
    `isHistoryOnly`, `useMarkRoomRead` (manual) and `useAutoMarkRoomRead` (effect).
    `conversation-header` (a widget) stays in components/ and uses the entity pieces.
  - intelligence: ai-status, contact-meta, contact-row, profile-link, membership-summary, presence,
    agents, contacts (lib), `usePickables`, `canMessage`. Presence sites use `useIsOnline`; the
    contact rows of the three pickers (member-picker, new-group, new-conversation) and the contacts
    list keep `withContactPresence` (the same rule, pure) until T4 splits their rows.
    `usePickables` gates on the org's users loading: member-picker now waits for them like
    new-group does (instance users are no longer listed first while the org's load).
  - organization: lib/orgs (one `permissionLabel`, used by membership-summary too), current-org and
    org-users hooks, org-avatar, org-select, `removalOf` (chart, members, contacts.$id), `isAdminOf`
    (positions, org route, settings, members).
  - connector: connector-status (its dot is a `StatusDot` variant; the dot classes live once in
    `STATUS_DOT_CLASS`), the connector form schema (`connectorFormSchema`, `connectorFormDefaults`,
    `ConfigValue`) out of schema-form. `lib/schemas.ts` had no connector part; nothing moved from it.
  - room-member: `memberSettingsSummary`, `memberChanges`, `visibleRows` (member panel).
  - Skipped or partial: `question-bubble.tsx` (answering logic, T4); the `ConversationHeader`
    widget (T5). Legacy `components/`, `hooks/`, `lib/` still hold features and widgets (T4, T5).
  - Visible behaviour changes: none intended. Two deliberate ones: unknown platform kinds show
    capitalised (one `platformName`), and the member picker waits for the org's users (above).

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
