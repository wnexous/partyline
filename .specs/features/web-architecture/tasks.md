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
- Split into two branches so they can run in parallel: **T4a** (messaging: send-message,
  mention-autocomplete, send-voice-message, delete-message, react-to-message, answer-question,
  conversation-actions, edit-room, and the rooms routes) and **T4b** (people, organizations and
  account: everything else in the list above, the account sections, and the pages and widgets that
  go with them; see the T4b notes below).
- Status: T4a done and T4b done (merged). T4a notes:
  - The composer is `widgets/composer`, not a feature: it composes send-message, edit-message,
    mention-autocomplete and send-voice-message, and a feature may not import another feature.
    `features/send-message` keeps the draft store, the reply strip, the drop and focus helpers.
  - Pure rules that the entities read moved to the entities: `entities/message/model/permissions`
    (canEditMessage, canDeleteMessage), `entities/message/lib/voice` (isAudio, isVoiceName,
    formatDuration), `entities/message/lib/reaction-list` (withReaction, reactedWith,
    atReactionLimit), `entities/room/model/conversation-rules` (canPost, managesGroup,
    hasConversationActions). The row is `entities/room/ui/conversation-row`.
  - `shared/api/error-text` holds the post and edit rules (`errorText(t, err, "post" | "edit")`)
    and `startConversationError`, which the hook re-exports for the other worker.
  - Tests that drive the live dispatcher (app code) live in `routes/_app/rooms` (layer order).
  - message-button stays in the conversation-timeline widget, as the spec maps it; it is really the
    start-conversation action and can move to a feature in T5.
  - No visible behaviour change.

## T4b — people, organizations, account (WA-06 part)
- features: start-conversation, create-group, add-member, edit-member-settings, manage-org-member,
  connect-agent, add-connector, edit-profile, change-language, toggle-theme, push-notifications,
  create-org, invite-to-org, change-password, revoke-session, delete-account, set-default-org.
- widgets: app-sidebar, profile-panel, command-palette, org-chart, people-group. app: providers,
  layouts. pages: every route with a component (`contacts`, `account`, `orgs`, `connections`,
  `devices`, `tokens`, `identities`, `admin`, `auth`, `setup`, `invite`); every route file is now
  `createFileRoute(...)` with its guard or loader and the page component.
- lib/schemas: base validators in `shared/lib/validation`, form schemas in their feature or page
  `model/schema.ts`; `messageTextSchema` stays in `lib/schemas.ts` for T4a.
- Notes (what was decided, and what stayed):
  - `SecretOnceDialog` is a shared molecule (three slices use it; add-connector cannot import
    connect-agent). `AuthLayout` is the shared `AuthFrame`, filled by each auth page with its
    language and theme controls (pages cannot import app layouts).
  - Theme rules and the preference keys are in `shared/lib` (the prefs store uses them);
    `ThemeRadioGroup` is a shared molecule; the feature keeps `ThemeToggle`.
  - The profile panel's open state is in `entities/intelligence/model/profile-panel` (the
    `profile-link` entity uses it). Position rules are in `entities/organization/lib/positions`; the
    chart tree in `widgets/org-chart/lib`.
  - Pickers: `entities/intelligence/ui/pickable-row` is the one candidate row (IntelligenceRow with
    presence from `useIsOnline`); the two lists keep their own containers (a list of Add buttons, a
    fieldset of checkboxes), so their roles stay as they were.
  - `OrgMemberActions` is the one move/delete/remove set for the members table and the chart; the two
    row shells stay separate (a table row, a chart row).
  - Not done: the contacts profile (`pages/contacts` ProfileView) still has its own sections; the
    profile panel's sections are not yet shared with it (T5).
  - Not done: `messageTextSchema` (T4a); `profileSchema` (unused) was removed.
  - Merge notes: `PhotoEditor` is a shared molecule (edit-profile and edit-room both use it, and a
    molecule cannot read `shared/api`, so it takes the error text as `errorText`, with
    `photoErrorText` in `shared/api/error-text`). `widgets/app-sidebar` cannot import
    `widgets/conversation-list`, so `MessengerPanel`/`AppSidebar` take `renderSearch`/`renderList`
    and the callers (app-shell, pages/home) spread `conversationSlots` from the list widget.
    Composer tests take `AppProviders` from `@/test/render` (a widget cannot import `app`).
- Status: done

## T5 — widgets + pages (WA-01, WA-06 part)
- Left after T4a and T4b: the contacts profile sections into `widgets/profile-panel` (see the notes below).
- widgets: app-sidebar, conversation-list, conversation-header, conversation-timeline,
  member-panel (from T1), profile-panel (also used by contacts.$id), command-palette, org-chart,
  room-files, room-settings.
- pages: every route file becomes a thin wiring of a page slice; split account (sections →
  features), connector page, tokens, identities, org members/chart (one org member row).
- Status: done. Notes:
  - Profile sections (contacts `profile-page` vs `widgets/profile-panel`), decided: the pieces that
    are the same thing are already one component: `ProfileConversations` (both), `MembershipSummary`
    (both), `StatusDot`, `IntelligenceAvatar`, `YouBadge`, `IntelligenceName`, and now
    `SessionLabel` (the page's session rows). The rest stays in each, on purpose: the page's
    header carries its actions and the panel's title is the dialog's accessible name (`SheetTitle`);
    the page's membership rows carry the removal actions; the page's App row reads the harness
    label only while the panel reads `appLabelOf` (server label first), and unifying the two would
    change visible text; the page's sessions list is a detailed table, not the one-line
    `SessionList`.
  - MessageButton is in `features/start-conversation`. `conversation-actions.test.tsx` is in
    `routes/_app` (it drives the app from `/`); `presence-colours.test.tsx` is split beside
    `StatusDot`, `ConnectorStatusBadge` and `LiveIndicator`; `messageTextSchema` (unused) is in
    `features/send-message/model/schema.ts`.
  - Audit (T6): fixed: the conversation list row and the message time are `RelativeTime` (new
    `format="list"`); the second `MemberRow` is `OrgMemberRow`; the profile reads presence through
    `usePresenceEntry`. Listed, not changed (a visible change, or a different rule that needs a
    decision): `shared/ui/atoms/face` base (org-avatar and intelligence-avatar keep two PIXELS maps);
    inline `@handle` spans outside IntelligenceName (conversation-header, chart-row, mention
    suggestions, account menu, profile header and panel, merge dialog and connector page sentences,
    admin users: a username, not an intelligence); hand-built `DialogContent` shells in pages and
    features that are not the form shell (invites, tokens, identities, add-connector, member picker,
    new conversation, new group, connect agent, delete account, command menu, image viewer);
    `NativeSelect` with a hand-made `Label` outside `form-fields` (connector, org members, org
    positions, devices, merge AI, default org, org-select); the page's App row rule (harness only)
    versus `appLabelOf`; `contacts-page` reads the whole presence map for its rows (the rule itself
    is the pure `withContactPresence`); `ConversationFace` and `ConversationAvatar` are two names in
    one file (the face is the picture, the avatar takes a conversation); the composer's and login's
    `formError` state is form state, not the error-text rule; `question-bubble`'s own check is not
    the message permission (different author rule); the edited-time `title` is a text on a span,
    not a time. DropdownMenu outside shared/ui: the account, org, language, theme menus and the
    reaction menu are app menus, not row menus (no row menu bypasses ActionMenu).
  - Strict (T6): `components/`, `hooks/`, `lib/` are deleted; the checker has no legacy layer (a
    file in or an import of them fails `legacy-removed`), the baseline is empty with its stale check,
    and `primitive-dialog` keeps the dialog and sheet primitives inside `shared/ui`.

## T6 — strict (WA-05)
- Delete `components/`, `hooks/`, `lib/`; baseline empty and the legacy layer removed from the
  checker; final doc pass. Optional: split i18n dictionaries by domain.
- Status: done (see the T5 notes for the audit and what stayed).

## T7 — last standardisation (audit leftovers)
- `entities/intelligence/ui/intelligence-handle` `IntelligenceHandle` + `handleText`: the app's one
  `@handle` (the mono, muted style `IntelligenceName showHandle` already had). Adopted by
  `IntelligenceName`, the conversation header, the org chart row, the mention list, the account
  menu, the profile header and panel, the merge and operator option labels, and, inline, the
  connector's operator sentence (`NamedSentence` can carry a handle slot). Admin users keeps its
  `@username`: it is a username, not an intelligence's handle.
- `FormDialog` for the form shells: invites, tokens, add-connector and delete-account. The
  connector form logic (`useConnectorForm`, `ConnectorFields`) is shared with the settings sheet,
  and `FormDialog` gained the destructive submit and the header/form classes a scrolling body
  needs.
- `SelectControl` in `form-fields`, the controlled twin of `SelectField` (label, description,
  error, aria wiring; `hideLabel` for a select the row already names): the connector operator and
  delivery picks, org members' permission, the chart's parent picker, merge AI, the default
  organization and `OrgSelect`.
- `shared/ui/atoms/face` is the one avatar base (size map in pixels, circle/square, tint and
  initials from `shared/lib/avatar`, image); `IntelligenceAvatar` and `OrgAvatar` are the domain
  skins over it, and the duplicated PIXELS maps are gone.
- `ConversationAvatar` is the one exported name for a conversation's face, with a `variant`
  (`conversation` for a row, `face` for the counterpart or group a page names); `ConversationFace`
  is gone and its importers use the variant.
- Status: done. Left on purpose:
  - the identities pairing dialog is a multi-step status flow (its steps own their buttons, it has
    no form and no submit), so it stays a plain Dialog; it already imports
    `shared/ui/molecules/dialog` and uses its `DialogHeader`. The other non-form dialogs (member
    picker, new conversation, new group, connect agent, command menu, image viewer) already do the
    same and are unchanged.
  - the devices page has no select: its hand-made label is on the code input (not a `NativeSelect`).
  - `AIMeta`'s "hosted by @handle" stays translated text inside one sentence (the testable string
    the pickers show), and the command palette's hint stays the generic hint slot (it also holds
    a connector's platform label).
  - the visible result is unchanged except the handle, which is now the same mono span everywhere.
