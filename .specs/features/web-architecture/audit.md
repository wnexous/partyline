# partyline web app audit (read-only)

Scope: `/home/dev/orca/projects/partyline/web/src` (components/, components/ui/, components/intelligence/, hooks/, lib/, routes/).
Method: file reads, grep over imports and usages, and an import-graph script. No source file was modified; no tests or builds were run. Test files (`*.test.*`, 108 of them) were excluded from counts and placement, except where noted.
Line numbers are from the current tree.

---

## 0. Headline findings

1. **Member lists**: the room "Membros" list (`MemberList`) and the DM "Participantes" list (`ParticipantList`) are two separate row components with different markup, different data sources and different actions. See 1.A.
2. **Presence**: "is this AI online" is resolved in 12 places with 3 different fallback rules. See 1.C.
3. **Dialogs**: `ConfirmDialog` exists (`components/confirm-dialog.tsx`) but is used once. 14 hand-built `AlertDialog` confirmations are open-coded in 12 files. See 1.G.
4. **App label of an AI** (harness vs. server app label) is computed three ways, and the three disagree. See 1.D.
5. **Time display**: the "title = absolute, text = relative" pair is hand-written at about 11 sites, and day-separator logic exists in two places. See 1.F.
6. **Layer violations**: one import cycle (`message-item.tsx` <-> `message-menu.tsx`). `components/ui/*` imports app code in 5 places. Several "presentational" components call mutations directly. Business rules for org-member removal are duplicated in 3 files. See section 2.
7. **Composer draft bug**: text and attachments live in `useState` inside `ComposerForm`, and `routes/_app/rooms/$roomId.tsx:224` remounts it with `key={composer-${roomId}}`. No draft store exists. See section 4.
8. **Dead code**: `components/ui/card.tsx`, `table.tsx`, `scroll-area.tsx`, `select.tsx` have zero importers.

---

## 1. Duplicates and near-duplicates

### 1.A Room members vs DM participants (confirmed)

| | Room "Membros" | DM "Participantes" |
|---|---|---|
| Component | `components/member-list.tsx` `MemberList` (L34), row `MemberRow` (L83) | `components/participant-list.tsx` `ParticipantList` (L12) |
| Rendered in | `routes/_app/rooms/$roomId.tsx` L368 via `MembersPanel` (L349) | `routes/_app/rooms/$roomId.tsx` L336 via `SidePanel` when `participants` is set (L334) |
| Discriminator | `m.kind` (`agent` / `user`) | `p.role` (`ai` / `person` / ...) |
| Avatar + name | `IntelligenceAvatar` + `IntelligenceName` (showHandle, profileLink, stretch), L142-158 | identical props, L22-31 |
| Status line | `StatusDot withLabel` + owner (`rooms.ownedBy`) + display_name (`memberSettings.as`) + `memberSettingsSummary(m)` keys, only for `kind==="agent"`, L103-117 | `AiStatusLine` (StatusDot + app label from `profile.apps[0]`), only for `role==="ai"`, L32 |
| Sessions line | `member-sessions-${id}`, L118-134 (`:label` or principal, shown only when online and some label is non-empty) | none |
| Actions | "Message privately" menu `MemberDmActions` L193-216 (raw `DropdownMenu`, not `RowMenuButton`/`ActionMenu`), settings `MemberSettings` L171, remove X L172-182 | none (read-only, per its docstring) |
| Online source | `presence[m.member_id]?.online ?? m.online` (L74) | `AiStatusLine` -> `useIntelligence(id)` + `presence[id]` (ai-status.tsx L229-233): a second fetch per AI row |
| Loading state | `MembersPanel` shows a Skeleton while `members.isPending` (L362-366) | none: the DM branch of `SidePanel` never shows the loading state (L334-340) |
| Empty state | `<p className="px-4 py-6 ...">` inline, L63-65 | none |
| Hooks used | `useAddMember`, `useRemoveMember`, `useMe`, `useStartConversation`, `useLiveStore` | `useMe` only |

**Canonical**: `MemberList` is the superset. The shared row should be one `IntelligenceRow` (avatar + `IntelligenceName` + `meta` slot + `sessions` slot + `actions` slot) in `entities/intelligence/ui`. `ParticipantList` becomes `<IntelligenceRow>` with no actions and no sessions. The DM branch should also use the same loading and empty states.
Note the discriminator mismatch: `kind` on a room member and `role` on an `IntelligenceRef` must be normalised in one adapter.

**Sessions line, second copy**: `routes/_app/contacts.$id.tsx` L207-222 renders the same "`:label` / `profile.sessionPrincipal`" vocabulary as a `<ul divide-y>` list. `components/message-item.tsx` L386-388 renders the same vocabulary as `message.viaSession` in the message header. Three renderings of one concept. Canonical: `entities/intelligence/ui/session-label.tsx` (a `SessionLabel` atom plus a `SessionList` molecule). `mention-suggestions.tsx` L92 also reads `member.sessions`.

### 1.B Intelligence presentation: name + handle + avatar + meta, 12 variants

Each of these shows an avatar and a name with an @handle, and each does it a little differently:

- `components/conversation-header.tsx` L66-111: DM face is re-implemented inline (duplicates `ConversationAvatar`'s choice, see 1.E), title is a `ProfileLink` around a plain string, handle is `<span className="font-mono text-xs">@{handle}</span>` (L110), then `AiStatusLine` (L113).
- `components/participant-list.tsx` L22-32 (see 1.A).
- `components/member-list.tsx` L142-158 (see 1.A).
- `components/contact-row.tsx` L35-41 and L358-370: wraps `IntelligenceName` (without `profileLink`) in a `ProfileLink` by hand, instead of using the `profileLink` prop.
- `components/new-conversation-dialog.tsx` L146-170: a second, private `ContactRow` (same name as `components/contact-row.tsx`, different shape, built on `CommandItem`).
- `components/new-group-dialog.tsx` `Candidates` L355-416: checkbox + avatar + name + `PickableMeta`.
- `components/member-picker.tsx` `PickableCandidate` L160-190 and `Candidate` L195-236: `Candidate` is a generic row with an Add button.
- `components/invite-dialog.tsx` L175-176: avatar + `IntelligenceName` (showHandle), no meta.
- `components/mention-suggestions.tsx` L183-190: avatar + bare `@handle`.
- `components/account-menu.tsx` L54-61: avatar + `@{handle}`.
- `components/profile-panel.tsx` L226-235: `xl` avatar + name + `@{handle}`.
- `routes/_app/contacts.$id.tsx` L153-160: `xl` avatar + `@{handle}` in `font-mono text-sm`.
- `components/command-menu.tsx` L121-147: `PaletteItem` with `ConversationAvatar` (sm) and `StatusDot`.

Also four ways to make a name a link: `IntelligenceName profileLink` (`intelligence-name.tsx` L140-143), `ProfileLink` wrapping a string (`conversation-header.tsx` L93-98), `ProfileLink` wrapping `IntelligenceName` (`contact-row.tsx` L365-370), and an `aria-label` `ProfileLink` around only the avatar (`message-item.tsx` L349-355, `conversation-header.tsx` L68-72).

**Canonical**: `IntelligenceAvatar`, `IntelligenceName` (with `profileLink`, `stretch`, `showHandle`) and a new `IntelligenceHandle` atom. Rows should be composed from them, not hand-built. The `@handle` rendering should be the `IntelligenceName` `showHandle` span (`intelligence-name.tsx` L172-174), and nothing else.

### 1.C Online / presence (12 sites, 3 fallback rules)

All sites read `useLiveStore((s) => s.presence[...])` and fall back to the profile or contact value:

| Site | Expression |
|---|---|
| `member-list.tsx` L44, L74 | `presence[m.member_id]?.online ?? m.online ?? false` |
| `ai-status.tsx` L22, L33 | `live?.online ?? ai.online` |
| `profile-panel.tsx` L211, L213 | `presence?.online ?? ai?.online ?? false` |
| `routes/_app/contacts.$id.tsx` L136, L146, L371-372 | `presence?.online ?? profile.ai?.online ?? false` |
| `routes/_app/contacts.index.tsx` L53, L67 | `withContactPresence(ai, presence[...])` |
| `new-group-dialog.tsx` L80, L331 | `withContactPresence(p.ai, presence[p.ref.id]).online` |
| `new-conversation-dialog.tsx` L61, L104 | same as above |
| `member-picker.tsx` L69, L143 | same as above |
| `command-menu.tsx` L86, L147 | `presence[agent.id]?.online ?? agent.online` |
| `connect-agent-dialog.tsx` L243-247 | local helper `online(id, fallback)` |
| `routes/_app/orgs/$orgId/chart.tsx` L239 | `live?.online` (no fallback) |

`lib/presence.ts` `withContactPresence` is the only shared rule, and only the contact-list flavour uses it. **Canonical**: `entities/intelligence/model/presence.ts` with one hook `useIsOnline(id, fallback)` and `withContactPresence` kept as a pure helper.

Status dot and label variants:
- `components/status-dot.tsx` `StatusDot` (8 importers): canonical.
- `components/connector-status.tsx` L18-19 `DOT` map: a separate online/connecting/error/stopped dot, same pulse token `animate-line-pulse bg-online`.
- `components/live-status.tsx` L23-24 `LiveIndicator`: same pulse token, third implementation.
- `components/ai-status.tsx` `AiStatusLine` = `StatusDot` + app label. `components/contact-meta.tsx` `AIMeta` L299 = `StatusDot` + last-seen + app + folder + owner.
- `routes/_app/contacts.$id.tsx` L389-393: `StatusDot` + last-seen text, a copy of `AIMeta`'s last-seen block (`contact-meta.tsx` L300-308).

### 1.D AI app / harness label (3 rules, they disagree)

- `components/ai-status.tsx` L25: `profile.data?.apps[0]?.label || harnessLabel(ai.app)`.
- `components/profile-panel.tsx` L216: `profile.apps[0]?.label || harnessLabel(ai.app) || ai.app`.
- `components/contact-meta.tsx` L296 (`AIMeta`, used by contacts list and picker rows): `harnessLabel(ai.app) ?? ai.app`. It ignores the server's `apps[0].label`.

**Canonical**: a `appLabelOf(profile|ai)` in `entities/intelligence/lib`, the server label first, then `harnessLabel`, then the raw id. Currently `AIMeta` shows a different name than the profile page for the same AI.

### 1.E Conversation / group avatar

- `components/conversation-avatar.tsx` L16-33 chooses DM face (`IntelligenceAvatar`) or group face (`GroupAvatar`). It is canonical.
- `components/conversation-header.tsx` L66-81 re-implements the same choice inline (`face ? IntelligenceAvatar : GroupAvatar`).
- `components/room-settings-sheet.tsx` L208 and `components/conversation-header.tsx` L76 build `GroupAvatar` with a hand-made `group={{ id, name, avatarUrl }}`.
- `components/group-avatar.tsx` is `IntelligenceAvatar` with `rounded-lg`. It could be a `shape` prop on `IntelligenceAvatar`.

### 1.F Avatar shell and org face

- `components/org-avatar.tsx` duplicates `IntelligenceAvatar`'s `PIXELS` map (L453 vs `intelligence-avatar.tsx` L43), its `Avatar` + `AvatarFallback` structure, and the initials/tint import from `components/intelligence/intelligence-avatar` (L1). The file comment at L441-442 says "the same greys the intelligences use", but `avatarTone` yields coloured tints. The comment is stale.

### 1.G Dialogs and confirmations

- **Canonical, used once**: `components/confirm-dialog.tsx` `ConfirmDialog` (`title`, `body`, `confirmLabel`, `pending`, `onConfirm`, `onCloseFocus`). Its only user is `components/conversation-menu.tsx` L218.
- **Hand-built AlertDialogs (14 blocks in 12 files)** with the same skeleton (`AlertDialogContent` / `Header` / `Title` / `Description` / `Footer` / `Cancel` / `Action`):
  - `components/message-actions.tsx` L61-75 `DeleteMessageDialog`
  - `components/room-settings-sheet.tsx` L231-255 (inline trigger)
  - `components/delete-ai-dialog.tsx` L36-50
  - `components/end-session-dialog.tsx` L32-46
  - `components/remove-from-org-dialog.tsx` L52-72
  - `routes/_app/admin/users.tsx` L163-178
  - `routes/_app/identities.tsx` L149-165
  - `routes/_app/admin/invites.tsx` L277-290
  - `routes/_app/tokens.tsx` L418-432 (revoke) and L455-470 (delete)
  - `routes/_app/orgs/$orgId/settings.tsx` L234-255 (leave org) and L321-340 (delete org)
  - `routes/_app/orgs/$orgId/connections/$connectorId.tsx` L666-685 (inline trigger)
  - `routes/_app/orgs/$orgId/positions.tsx` L419-440
  
  Most use the same open-state shape: `open={x !== null} onOpenChange={(open) => (open ? null : onClose())}`.
- **Form dialogs with no shared shell**: `NewOrgDialog`, `NewGroupDialog`, `NewConversationDialog`, `InviteDialog`, `AddConnectorDialog`, `ConnectAgentDialog`, `MergeAIDialog`, `MoveMemberDialog`, `SecretOnceDialog` each build `Dialog` header, body and footer themselves. Sheet-based panels (`RoomSettingsSheet`, `ProfilePanel`) are separate again.
- **Canonical**: `shared/ui/molecules/confirm-dialog` with `open` / `onClose` props (the open-state wrapper is the common part), and a `FormDialog` molecule for the form dialogs.

### 1.H Empty, loading and error states

- **Empty**: `components/empty-state.tsx` `EmptyState` is used 19 times, canonical. Exception: `components/member-list.tsx` L63-65 inlines a `<p>`.
- **Not-found wrappers around EmptyState**: `RoomNotFound` (`routes/_app/rooms/$roomId.tsx` L449), `OrgNotFound` (`components/org-not-found.tsx` L11), `ProfileNotFound` (`routes/_app/contacts.$id.tsx` L96), `NotFoundPage` (`components/status-pages.tsx` L26). Four variants of one thing.
- **Loading**: `Skeleton` is used in 29 files. Hand-made skeleton blocks: `MembersPanel` L362-366, `RoomSkeleton` L435-447, `ListSkeleton` in `components/conversation-list.tsx` L348, `ProfileSkeleton` in `contacts.$id.tsx` L113. No shared `PanelSkeleton` / `ListSkeleton`.
- **Errors**: 30 `toast.error(...)` call sites. Three separate status-to-copy mappers: `postErrorText` (`composer.tsx` L89), `editErrorText` (L111), `startConversationError` (`hooks/use-start-conversation.ts` L13), plus `formError` in `routes/_app/account/index.tsx` L97. **Canonical**: `shared/api/error-text.ts` with a per-operation map, not one function per feature.

### 1.I Menus and row actions

- **Canonical row menu**: `components/action-menu.tsx` `ActionMenu` (sheet on touch, popup otherwise, documented at the top of the file), used by `message-menu.tsx` and `conversation-menu.tsx`. Trigger: `components/row-menu-button.tsx` `RowMenuButton` (used by `message-item.tsx` and `conversation-list.tsx`).
- **Bypass**: `components/member-list.tsx` `MemberDmActions` L193-216 builds its own `DropdownMenu` with a `⋯` trigger. On a phone this skips the long-press and bottom-sheet behaviour that `ActionMenu` provides.
- **Other menu shells**: `account-menu.tsx`, `organization-menu.tsx`, `org-switcher.tsx`, `language-switch.tsx`, `theme-toggle.tsx` each use `DropdownMenu` directly. Some of this is legitimate (they are not row actions).

### 1.J Form fields

- **Canonical**: `components/form-fields.tsx` `TextField` (L59), `TextAreaField` (L134), `SelectField` (L194), `FormAlert` (L181), `FieldError` (L22). 17 importers.
- **Bypass**: `components/org-select.tsx` and `components/move-member-dialog.tsx` use `Label` + `NativeSelect` by hand. `routes/_app/devices.index.tsx` uses `Label` + `Input` by hand.
- **Two select primitives**: `components/native-select.tsx` (8 importers, used) and `components/ui/select.tsx` (Radix, zero importers, dead).
- **Choice groups, five implementations**: `ThemeRadioGroup` (`theme-toggle.tsx` L22), `LanguageRadioGroup` (`language-switch.tsx` L35), `Choice` (`routes/_app/account/index.tsx` L197), `ConnectorChoice` (`routes/_app/identities.tsx` L291), plus `components/ui/radio-group.tsx` (6 importers) and `components/ui/toggle-group.tsx` (2).

### 1.K Section and block shells

- `components/page-section.tsx` `PageSection` (2 importers) vs three local `Section` helpers: `routes/_app/contacts.index.tsx` L294, `routes/_app/contacts.$id.tsx` L347 (plus `Row` L359), `routes/_app/orgs/$orgId/connections/$connectorId.tsx` L260. The three local ones differ in heading size, `id` handling and actions slot.

### 1.L Org member rows and membership rules

- Two components named `MemberRow`: `routes/_app/orgs/$orgId/members.tsx` L247 (table row) and `routes/_app/orgs/$orgId/chart.tsx` L218 (chart row). Both render move / remove / delete actions for an org member, and both open `MoveMemberDialog`, `RemoveFromOrgDialog`, `DeleteAIDialog`.
- `MembershipRow` (`routes/_app/contacts.$id.tsx` L301) is a third per-membership row. `components/membership-summary.tsx` is the shared summary and is used by `profile-panel.tsx` L276 and `contacts.$id.tsx`.
- Two permission-label sources: `components/membership-summary.tsx` L6-10 `PERMISSION_LABELS` (keys) vs `lib/orgs.ts` L30-41 `permissionLabel` (used by `members.tsx`, `route.tsx`, `invite-dialog.tsx`).

### 1.M Name collisions and same-named components

- `ContactRow`: `components/contact-row.tsx` L339 vs `components/new-conversation-dialog.tsx` L146.
- `MemberRow`: `components/member-list.tsx` L83, `routes/_app/orgs/$orgId/members.tsx` L247, `routes/_app/orgs/$orgId/chart.tsx` L218.
- `Section`: three local helpers, see 1.K.

### 1.N Time formatting

- **Relative with absolute tooltip** (`<span title={formatAbsolute(x)}>{t(key, { time: formatRelative(x) })}</span>` or `<time>`): `routes/_app/tokens.tsx` L227-237, `routes/_app/admin/invites.tsx` L172-184, `routes/_app/admin/audit.tsx` L80-82, `routes/_app/account/index.tsx` L705-706, `routes/_app/contacts.$id.tsx` L226-230 and L391-392, `components/contact-meta.tsx` L302-304, `components/room-files.tsx` L241-244, `routes/_app/devices.index.tsx` L255-256, `routes/_app/orgs/index.tsx` L263-264, `routes/_app/orgs/$orgId/members.tsx` L424-425, `routes/_app/orgs/$orgId/connections/$connectorId.tsx` L345-347. About 11 sites.
- **Ad hoc**: `routes/invite.$token.tsx` L85 `Intl.DateTimeFormat(dateStyle long, timeStyle short)` (not `formatAbsolute`). `routes/_app/admin/users.tsx` L122 and `routes/_app/identities.tsx` L122 use `formatAbsolute` only.
- **Day separators, two implementations**: `components/message-timeline.tsx` L63-76 (`dayKey`, `dayLabel`: today / yesterday / weekday-long date) vs `lib/time.ts` L36-59 (`startOfDay`, `formatListTime`: clock / yesterday / weekday-short / date). Same concept, different strings and rules.
- **Canonical**: `shared/ui/molecules/relative-time.tsx` (`<RelativeTime iso>` renders the `<time>` with title), and `shared/lib/time.ts` owns the day-label rule once.

### 1.O Message "via" and platform rendering

- `components/message-item.tsx` header, L360-412: (a) sr-only grouped label with `viaSession` L369-374; (b) `viaSession` for agents L385-389; (c) for chats with a `chat` object: `message.via` + platform chip (`data-testid="chat-platform"`, L397-401) + `TrustBadge`; (d) for chats without a `chat` object: `message.via` + ` · ${platform}` text (L409-413) + `TrustBadge`. (c) and (d) produce different platform text.
- Two platform name functions in the same file: `platformLabel` (L75, raw map value) and `platformName` (L78, capitalised fallback). `PLATFORMS` (L67-72) duplicates the server's names.
- `components/message-item.tsx` `chatTitle` L87 and `routes/_app/orgs/$orgId/connections/$connectorId.tsx` (chat rows) both render chat names. Not yet unified.

### 1.P Pickers and candidate building

- Candidate building `pickableList(contacts, orgId, orgUsers)` is called in three places with slightly different wiring: `member-picker.tsx` L77-81 (org-scoped, plus `useOrgUsers`), `new-conversation-dialog.tsx` L70-72 (unscoped), `new-group-dialog.tsx` (org-scoped, `useOrgUsers` at L103). Each then re-implements `matchesTerms` filtering and a `useMemo`. **Canonical**: one `usePickables({ orgId })` hook in `entities/intelligence/model`.

### 1.Q Hooks: mobile and touch detection (three rules)

- `hooks/use-mobile.ts` `useIsMobile`: width < 768 px. Users: `routes/_app.tsx` L48, L105; `routes/_app/rooms/$roomId.tsx` L68; `routes/_app/index.tsx` L20; `components/profile-panel.tsx` L52; `components/ui/sidebar.tsx` L68.
- `hooks/use-auto-focus.ts` `isCoarsePointer()`: `(pointer: coarse)` only. Users: `components/ui/dialog.tsx` L64, `components/member-settings.tsx` L96.
- `hooks/use-enter-sends.ts` `isTouchFirst()` / `useTouchFirst`: `(pointer: coarse)` OR `(hover: none)`. Users: `components/composer.tsx` (`useEnterSends`), `components/message-timeline.tsx` L129, `components/conversation-list.tsx` L84.
- **Canonical**: `shared/lib/device.ts` with the three named predicates (`isCoarsePointer`, `isTouchFirst`, `isNarrow`) and their hooks in one file.

### 1.R Hooks and query usage

- `useStartConversation` (`hooks/use-start-conversation.ts`) wraps `useOpenDM` and is the canonical "open a DM" entry. Used by `member-list.tsx` L195 and `profile-panel.tsx`.
- `useMarkRoomRead` (`hooks/use-mark-room-read.ts`) wraps `useMarkRead` and `useNewestSeq`. `components/conversation-menu.tsx` L89 calls `useMarkRead` directly for "mark as read". Two paths to the same mutation.
- `useAcceptInvitation` / `useDeclineInvitation` (`lib/api/queries.ts` L1402-1403) are one-line aliases of `useAnswerInvitation`.
- `lib/api/queries.ts` L1804 lines, roughly 130 exports. It mixes queries and mutations for every domain with pure helpers: `fileKindOf` L1453, `messageFiles` L1459, `newestFirst` L1506, `trimActivity` L1515, `previewOf` L820, `newestActivityFirst` L795, `readReactions` L687, `applyReactions` L700, `replaceMessage` L607, `normalizePage` L531, `olderPageCursor` L536. Pure functions do not belong in the API module.

---

## 2. Layer violations and misplaced code

### 2.a Presentational-looking components that call queries or mutations

| File | Hooks / effects | Why it is a problem |
|---|---|---|
| `components/member-list.tsx` L16, L44-46, L97, L195 (via `MemberDmActions`) | `useAddMember`, `useRemoveMember`, `useMe`, `useLiveStore`, `useStartConversation` | The list also owns the remove-with-undo toast flow (L49-61). Should be a widget container over a presentational row. |
| `components/participant-list.tsx` L4, L13 | `useMe` | Only uses it for `viewerId`. Should take `viewerId` as a prop. |
| `components/ai-status.tsx` L229 | `useIntelligence(id)` | A leaf fetches its own profile. Each AI row in a list causes a request. |
| `components/language-switch.tsx` | `useUpdateMe`, `useLiveStore` | A shared control performs an account mutation. Feature `change-language`. |
| `components/message-actions.tsx` | `useDeleteMessage` | A dialog performs a mutation. Feature `delete-message`. |
| `components/conversation-menu.tsx` L8 | `useDeleteRoom`, `useLeaveRoom`, `useMarkRead`, `useRoomMembers`, `useProfilePanel` | A menu fetches the member list to decide permissions (L37-42). Feature `conversation-actions`. |
| `components/command-menu.tsx` | `useAgents`, `useConnectorState`, `useConnectors`, `useConversations`, `useProfilePanel`, `useNavigate` | Five queries inside one palette. Widget. |
| `components/new-group-dialog.tsx` L77-103 | `useContacts`, `useCurrentOrg`, `useOrgUsers`, `useCreateGroup`, `useLiveStore` | Business rules (creatable orgs via `canCreateGroupsIn`, candidate building) inside the dialog. |
| `components/member-picker.tsx` L63-81 | `useContacts`, `useRoom`, `useAddMember`, `useOrgUsers`, `useLiveStore` | Org scoping and the add mutation inside a component. Feature `add-member` + entity model. |
| `components/connect-agent-dialog.tsx` | `useAgents`, `useMe`, `useLiveStore` | Presence and agent lists in a dialog. |
| `components/org-switcher.tsx` | `useConversations`, `useCurrentOrg` | Derives unread per org in the component (`orgs.ts` `unreadByOrg` is the rule). |
| `components/room-settings-sheet.tsx` | `useUpdateRoom`, `useUploadRoomAvatar`, `useRemoveRoomAvatar`, `useDeleteRoom` | Feature `edit-room`, fine as a feature, but lives in `components/`. |

### 2.b Route files that contain UI logic (not just wiring)

| Route | Lines | Inline components | Notes |
|---|---|---|---|
| `routes/_app/rooms/$roomId.tsx` | 464 | `RoomPage` (L62), `SidePanel` (L278), `MembersPanel` (L349), `HistoryNotice` (L378), `PausedBanner` (L404), `RoomSkeleton` (L435), `RoomNotFound` (L449) | Reply/edit choice state L86-97, mobile sheet orchestration L131-146 and L237-257, permission gating L107-126. |
| `routes/_app/account/index.tsx` | 814 | 13: `SettingsPage`, `ProfileSection`, `AppearanceSection`, `Choice`, `NotificationsSection` (L245-499, 255 lines, push subscription logic), `DefaultOrgSection`, `PasswordSection`, `SessionsSection`, `DeleteAccountSection`, `DeleteAccountBody` | Biggest route. Each section is a feature. |
| `routes/_app/contacts.$id.tsx` | 477 | 12: `ProfilePage`, `ProfileView` (L128-300), `MembershipRow`, `Section`, `Row`, `AIDetails`, `PersonDetails`, `AppRow` | Business rules at L139 (`manages`), L146 (`canEnd`), L313 (`home` = delete_effect). |
| `routes/_app/orgs/$orgId/connections/$connectorId.tsx` | 700 | 9 incl. `ChatsSection`, `Section`, delete dialog | Largest non-account route. |
| `routes/_app/tokens.tsx` | 486 | 8, incl. two AlertDialogs | |
| `routes/_app/identities.tsx` | 455 | 8, incl. `ConnectorChoice` and an AlertDialog | |
| `routes/_app/orgs/$orgId/positions.tsx` | 459 | 5 | |
| `routes/_app/orgs/$orgId/members.tsx` | 444 | 6, incl. `MemberRow` (L247), `PendingInvitations`, `PendingItem` | Row actions duplicated with `chart.tsx`. |
| `routes/_app/orgs/$orgId/settings.tsx` | 358 | 7 | |
| `routes/_app/devices.index.tsx` | 353 | 9 | |
| `routes/_app/contacts.index.tsx` | 332 | 4, incl. `Section` (L294) | |
| `routes/_app/orgs/$orgId/chart.tsx` | 324 | 4, incl. `ChartRow` (L163), `MemberRow` (L218) | |
| `routes/_app/admin/invites.tsx` | 303 | 4 | |
| `routes/_app/orgs/index.tsx` | 305 | 5 | |

### 2.c `components/ui/*` importing app code

- `components/ui/dialog.tsx` L7: `hooks/use-auto-focus` (`isCoarsePointer`). L8: `lib/i18n` (close label).
- `components/ui/sheet.tsx`: `lib/i18n` (close label).
- `components/ui/sidebar.tsx` L7: `hooks/use-mobile`. L8: `lib/i18n`.

Fix: pass the close label as a prop (or a default from the shared i18n), move `isCoarsePointer` to `shared/lib/device`. Everything else in `components/ui` imports only `components/ui` and `lib/utils`.

### 2.d Circular and upward imports

- **Cycle**: `components/message-item.tsx` <-> `components/message-menu.tsx`. `message-menu.tsx` imports `authorDisplayName` from `message-item.tsx`; `message-item.tsx` imports the type `MessageMenuTarget` from `message-menu.tsx`. This is the only strongly connected component in the non-test module graph.
- **Upward imports from pages/routes into component internals**: `routes/_app/rooms/$roomId.tsx` imports `platformName` from `message-item`, `conversationTitle` and `historyPlatform` from `conversation-header`, `canPost` from `composer`. Pure rules live in components.
- `components/composer.tsx` L32 imports `authorDisplayName` from `message-item.tsx`.
- `components/org-avatar.tsx` L1 imports `avatarTone` and `initials` from `components/intelligence/intelligence-avatar.tsx`: an org depends on an intelligence.
- `components/group-avatar.tsx` depends on `lib/conversations.ts` (`groupFace`), which fabricates an `IntelligenceRef` for a group.
- No `lib/` module imports from `components/`, `hooks/` or `routes/`. No `hooks/` module imports from `components/`. Checked by the script.

### 2.e Business rules computed in several places

| Rule | Where it is computed |
|---|---|
| Can post (send/edit/react) | `components/composer.tsx` L87 `canPost`. Used by `routes/_app/rooms/$roomId.tsx` L113, L121, L217. |
| Can manage a room / group | `routes/_app/rooms/$roomId.tsx` L107 (`!isDM && my_level===LevelManage`), `components/conversation-menu.tsx` L37-42 (`managesGroup`, `memberOfGroup`), `routes/_app/orgs/$orgId/connections/$connectorId.tsx` L159, `routes/_app/contacts.$id.tsx` L139 (`profile.ai?.my_level===LevelManage`). |
| Org admin | `lib/orgs.ts` L76 `isAdminOf` (used by `organization-menu.tsx`, `add-connector-dialog.tsx` L154, `connections/index.tsx` L67). Inline `org.permission === PermissionAdmin` in `positions.tsx` L78, `orgs/$orgId/route.tsx` L118, `settings.tsx` L59, `members.tsx` L82. |
| Can create groups in an org | `lib/orgs.ts` L79-80 `canCreateGroupsIn`, used by `new-group-dialog.tsx` L96. |
| Removal semantics (delete the identity vs remove it from the org) | `routes/_app/orgs/$orgId/chart.tsx` L243-244 (`can_delete && delete_effect === DeleteEffectDelete`), `routes/_app/orgs/$orgId/members.tsx` L353 and L365 (same), `routes/_app/contacts.$id.tsx` L313 (`home = delete_effect === "delete"`), `components/membership-summary.tsx` (home tag). Canonical: `entities/organization/model` helper `removalOf(member): 'delete' | 'remove' | null`. |
| Can message someone | `components/member-list.tsx` L102 (`agent` or `user`, not self, PDM-04), `components/profile-panel.tsx` L310 (`!own && role !== person`). Two different predicates for the same action. |
| History-only conversation (CID-21) | `components/conversation-header.tsx` L27-31 `historyPlatform`, `routes/_app/rooms/$roomId.tsx` L72-75 `refusedAsHistory`, L108-109 `historyOnly`. |
| Message trust (chat senders) | `components/message-item.tsx` L103-124 (`chatTrust`). Single site: fine. |
| Visible members (no chats) | `components/member-list.tsx` L31 `visibleMembers`. |
| Permission wording | `components/membership-summary.tsx` L6-10 and `lib/orgs.ts` L30-41 (see 1.M). |
| AI app label | three rules, see 1.D. |
| Avatar tint hash | `components/intelligence/intelligence-avatar.tsx` L35-41 `avatarTone`, reused by `org-avatar.tsx`. Fine as a shared rule, but it lives in an entity. |

---

## 3. Proposed FSD + atomic placement

Layer names follow FSD. Within `shared/ui`, `atoms` and `molecules` follow Atomic Design; `primitives/` holds the shadcn set, untouched. `widgets/` holds the organisms.

### 3.1 Target layers (summary)

- `app/`: providers, router, live wiring, layout shell.
- `pages/`: one per route, thin.
- `widgets/`: organisms that compose features and entities (sidebar, conversation list and header, timeline, member panel, profile panel, command palette, org chart).
- `features/`: user actions (send-message, edit-message, delete-message, react-to-message, add-member, edit-member-settings, create-group, start-conversation, invite-to-org, connect-agent, edit-profile, change-language, toggle-theme, answer-question, send-voice-message, mention-autocomplete, conversation-actions, manage-org-member, push-notifications).
- `entities/`: business objects with their model, lib and presentational UI (message, room, room-member, intelligence, organization, connector).
- `shared/`: `ui` (primitives, atoms, molecules), `api` (client, error text, live socket), `lib` (time, device, clipboard, gestures, forms, validation), `i18n`.

### 3.2 Table (every non-test file, grouped by target)

Shorthand: paths are relative to `web/src/`. `[split]` marks files to split. `[canon]` marks the canonical survivor of a duplicate group.

**app/**

| Target | Files |
|---|---|
| `app/providers` | `components/app-providers.tsx`, `main.tsx` |
| `app/router` | `router.tsx`, `routeTree.gen.ts` (generated), `routes/__root.tsx`, `routes/_app.tsx` (becomes `app/layouts/app-shell.tsx`) |
| `app/live` | `lib/live/index.ts`, `lib/live/socket.ts`, `lib/live/handlers.ts` [split: per-entity handlers move to their entity `model/`] |
| `app/push` | `lib/push/register.ts`, `lib/push/subscription.ts` (or `features/push-notifications`) |
| `app/styles` | `index.css` |

**pages/** (route files; the page owns the URL, the widgets own the UI)

| Target | Files |
|---|---|
| `pages/room` | `routes/_app/rooms/$roomId.tsx` [split: `SidePanel` and `MembersPanel` to `widgets/member-panel`, `HistoryNotice` and `PausedBanner` to `widgets/conversation-timeline`, `RoomSkeleton` and `RoomNotFound` stay], `routes/_app/rooms/index.tsx` |
| `pages/home` | `routes/_app/index.tsx`, `routes/_app/agents.tsx`, `routes/_app/settings.tsx` |
| `pages/contacts` | `routes/_app/contacts.index.tsx` [split: `Section` to shared], `routes/_app/contacts.$id.tsx` [split: `ProfileView` sections to `widgets/intelligence-profile`] |
| `pages/account` | `routes/_app/account/index.tsx` [split by section: `ProfileSection` to `features/edit-profile`, `AppearanceSection` to `features/toggle-theme`, `NotificationsSection` to `features/push-notifications`, `PasswordSection` to `features/change-password`, `SessionsSection` to `features/revoke-session`, `DeleteAccountSection` to `features/delete-account`, `DefaultOrgSection` to `features/set-default-org`] |
| `pages/admin` | `routes/_app/admin/route.tsx`, `routes/_app/admin/users.tsx`, `routes/_app/admin/invites.tsx`, `routes/_app/admin/audit.tsx` |
| `pages/orgs` | `routes/_app/orgs/index.tsx`, `routes/_app/orgs/$orgId/index.tsx`, `routes/_app/orgs/$orgId/route.tsx`, `routes/_app/orgs/$orgId/settings.tsx`, `routes/_app/orgs/$orgId/members.tsx` [split: `MemberRow` to `features/manage-org-member`], `routes/_app/orgs/$orgId/positions.tsx`, `routes/_app/orgs/$orgId/chart.tsx` [split: `ChartRow`, `MemberRow` to `widgets/org-chart`] |
| `pages/connections` | `routes/_app/connectors/index.tsx`, `routes/_app/connectors/$connectorId.tsx` (redirect only), `routes/_app/orgs/$orgId/connections/index.tsx`, `routes/_app/orgs/$orgId/connections/$connectorId.tsx` [split: `ChatsSection` and delete dialog] |
| `pages/devices` | `routes/_app/devices.index.tsx` [split], `routes/_app/devices.$code.tsx` |
| `pages/auth` | `routes/login.tsx`, `routes/setup.tsx`, `routes/invite.$token.tsx`, `components/auth-layout.tsx` |
| `pages/tokens-identities` | `routes/_app/tokens.tsx`, `routes/_app/identities.tsx` |

**widgets/**

| Target | Files |
|---|---|
| `widgets/app-sidebar` | `components/app-sidebar.tsx`, `components/org-switcher.tsx`, `components/account-menu.tsx`, `components/organization-menu.tsx`, `lib/nav.ts` |
| `widgets/conversation-list` | `components/conversation-list.tsx` [split: row to `entities/room/ui/conversation-row`, `ListSkeleton` to shared] |
| `widgets/command-palette` | `components/command-menu.tsx` |
| `widgets/conversation-header` | `components/conversation-header.tsx` [split: `conversationTitle`, `historyPlatform` to `entities/room/model`; face to `ConversationAvatar`] |
| `widgets/conversation-timeline` | `components/message-timeline.tsx` [split: day logic to shared], `components/message-item.tsx` (the `MessageItem` row only, see 3.3), `components/message-menu.tsx`, `components/message-button.tsx`, `components/reaction-bar.tsx`, `components/reaction-chips.tsx`, `components/reaction-picker.tsx`, `hooks/use-stick-to-end.ts` |
| `widgets/member-panel` | `components/member-list.tsx` [split: row to `entities/intelligence/ui/intelligence-row`, remove flow to `features/remove-member`], `components/participant-list.tsx` [fold into the same row component], `components/people-group.tsx` |
| `widgets/profile-panel` | `components/profile-panel.tsx`, `components/profile-conversations.tsx`, `hooks/use-profile-panel.ts` |
| `widgets/room-files` | `components/room-files.tsx` |
| `widgets/room-settings` | `components/room-settings-sheet.tsx` (its form parts to `features/edit-room`) |
| `widgets/org-chart` | `components/chart-tree.tsx`, `lib/org-chart.ts` |

**features/**

| Target | Files |
|---|---|
| `features/send-message` | `components/composer.tsx` [split: see 3.3 and section 4] |
| `features/mention-autocomplete` | `components/mention-suggestions.tsx` [split: pure `mentionAt`, `mentionCandidates`, `matchMentions` to `model`, rendering to `ui`] |
| `features/send-voice-message` | `components/voice-controls.tsx`, `hooks/use-voice-recorder.ts`, `lib/voice.ts`, `lib/webm-duration.ts` (663 lines: a codec parser, separate module) |
| `features/delete-message` | `components/message-actions.tsx` (its `DeleteMessageDialog`) |
| `features/react-to-message` | `hooks/use-react.ts`, `lib/reactions.ts` |
| `features/answer-question` | `hooks/use-answer-question.ts`, `lib/questions.ts` (wizard state of `question-bubble.tsx`) |
| `features/start-conversation` | `hooks/use-start-conversation.ts`, `components/new-conversation-dialog.tsx` |
| `features/create-group` | `components/new-group-dialog.tsx` [split: candidate list to shared picker] |
| `features/create-org` | `components/new-org-dialog.tsx` |
| `features/invite-to-org` | `components/invite-dialog.tsx` |
| `features/add-member` | `components/member-picker.tsx` |
| `features/edit-member-settings` | `components/member-settings.tsx` [split: `memberSettingsSummary` and `changes` to `entities/room-member/model`] |
| `features/remove-member` | (flow from `member-list.tsx`) |
| `features/edit-room` | `components/room-settings-sheet.tsx` form parts |
| `features/conversation-actions` | `components/conversation-menu.tsx` |
| `features/manage-org-member` | `components/move-member-dialog.tsx`, `components/remove-from-org-dialog.tsx`, `components/delete-ai-dialog.tsx`, `components/end-session-dialog.tsx`, `components/merge-ai-dialog.tsx` |
| `features/connect-agent` | `components/connect-agent-dialog.tsx`, `components/secret-once-dialog.tsx`, `lib/agent-connect.ts` |
| `features/add-connector` | `components/add-connector-dialog.tsx`, `components/schema-form.tsx` [split: `connectorFormSchema` to `entities/connector/model`] |
| `features/edit-profile` | `components/profile-editor.tsx`, `components/photo-editor.tsx` |
| `features/change-language` | (the action in `components/language-switch.tsx`; the radio control is a molecule above) |
| `features/toggle-theme` | `lib/theme.ts` (the `ThemeRadioGroup` control is a molecule above) |
| `features/push-notifications` | `lib/push/*` (if not kept in `app/push`) |

**entities/**

| Target | Files |
|---|---|
| `entities/message` | `components/message-item.tsx` (AUTHOR_KIND, `chatTrust`, `authorDisplayName`, platform helpers and `TrustBadge`; see 3.3), `components/markdown.tsx`, `components/audio-player.tsx`, `components/image-viewer.tsx`, `components/question-bubble.tsx` (render part), `lib/message-excerpt.ts` |
| `entities/room` | `lib/conversations.ts`, `components/conversation-avatar.tsx` [canon for DM/group face], `components/group-avatar.tsx` (becomes a `shape` option of `IntelligenceAvatar`), `components/conversation-row` (new, from `conversation-list.tsx`), `hooks/use-mark-room-read.ts` (model), `hooks/use-open-conversation.ts` (model) |
| `entities/room-member` | `components/member-settings.tsx` model part (`memberSettingsSummary`, `changes`), `lib/room-member.ts` (new: the `kind` vs `role` adapter, see 1.A) |
| `entities/intelligence` | `components/intelligence/intelligence-avatar.tsx` [canon], `components/intelligence/intelligence-name.tsx` [canon], `components/intelligence/you-badge.tsx`, `components/intelligence/intelligence-row.tsx` (new, from `member-list.tsx` `MemberRow` and `participant-list.tsx`), `components/ai-status.tsx`, `components/contact-meta.tsx` (`AIMeta`, `PickableMeta`), `components/contact-row.tsx` [canon for contact rows], `components/profile-link.tsx`, `components/membership-summary.tsx`, `lib/presence.ts`, `lib/agents.ts` (`harnessLabel`), `lib/contacts.ts` (`pickables`, `pickableList`, `filterOrgs`, `onlineCount`), `lib/live/store.ts` (presence part) |
| `entities/organization` | `components/org-avatar.tsx` [split: shares a `Face` base with `IntelligenceAvatar`], `components/org-select.tsx`, `lib/orgs.ts` [split: permissions model, `removalOf`, `isAdminOf`], `hooks/use-current-org.ts`, `hooks/use-org-users.ts` |
| `entities/connector` | `components/connector-status.tsx`, `lib/schemas.ts` (connector part) |

**shared/**

| Target | Files |
|---|---|
| `shared/ui/primitives` (shadcn, unchanged) | `components/ui/alert-dialog.tsx`, `alert.tsx`, `avatar.tsx`, `badge.tsx`, `button.tsx`, `checkbox.tsx`, `command.tsx`, `dialog.tsx` (decouple from i18n and `use-auto-focus`), `dropdown-menu.tsx`, `form.tsx`, `input.tsx`, `label.tsx`, `popover.tsx`, `radio-group.tsx`, `separator.tsx`, `sheet.tsx` (decouple from i18n), `sidebar.tsx` (decouple from `use-mobile` and i18n), `skeleton.tsx`, `sonner.tsx`, `switch.tsx`, `tabs.tsx`, `textarea.tsx`, `toggle-group.tsx`, `toggle.tsx`, `tooltip.tsx` |
| `shared/ui/primitives` (dead, delete candidates) | `components/ui/card.tsx`, `components/ui/table.tsx`, `components/ui/scroll-area.tsx`, `components/ui/select.tsx` (zero importers) |
| `shared/ui/atoms` | `components/native-select.tsx` [canon select], `components/status-dot.tsx` [canon presence dot], `components/brand.tsx` (`LogoMark`, `Wordmark`) |
| `shared/ui/molecules` | `components/copy-field.tsx` (`CopyButton`, `CodeBlock`), `components/empty-state.tsx` [canon], `components/form-fields.tsx` (`TextField`, `TextAreaField`, `SelectField`, `FormAlert`, `FieldError`) [canon], `components/page-header.tsx` [canon], `components/page-section.tsx` [canon section shell], `components/row-menu-button.tsx` [canon trigger], `components/action-menu.tsx` [canon row menu], `components/confirm-dialog.tsx` [canon confirm] , `components/inline-rename.tsx`, `components/emoji-grid.tsx`, `components/search-button.tsx`, `components/conversation-search.tsx`, `components/theme-toggle.tsx` (`ThemeRadioGroup`), `components/language-switch.tsx` (`LanguageRadioGroup` as a radio molecule, action in `features/change-language`), `components/attachment-preview.tsx`, `components/status-pages.tsx`, `components/org-not-found.tsx`, `components/back-link.tsx`, `components/live-status.tsx` (`LiveIndicator`, `LiveBanner`), `components/org-select.tsx`, plus new `shared/ui/molecules/relative-time.tsx` from section 1.N |
| `shared/api` | `lib/api/client.ts`, `lib/api/types.gen.ts` (generated, untouched), `lib/api/queries.ts` [split by domain into `entities/*/api`], `lib/live/store.ts` (only the generic socket-status part), `lib/schemas.ts` (base validators: `textLength`, `usernameSchema`, `passwordSchema`, `normalizeHandle`, `validationVars`), error-text helpers from section 1.H (new) |
| `shared/lib` | `lib/time.ts`, `lib/utils.ts` (`cn`), `lib/clipboard.ts`, `lib/viewport.ts`, `lib/forms.ts`, `hooks/use-mobile.ts`, `hooks/use-auto-focus.ts`, `hooks/use-enter-sends.ts`, `hooks/use-visual-viewport.ts`, `hooks/use-debounced-value.ts`, `hooks/use-document-visible.ts`, `hooks/use-disconnected.ts`, `hooks/use-long-press.ts`, `hooks/use-swipe-reply.ts`, `lib/avatar.ts` (`prepareAvatar`) |
| `shared/i18n` | `lib/i18n/index.tsx`, `lib/i18n/en.ts` [split by domain], `lib/i18n/pt-BR.ts` [split by domain, same files] |

### 3.3 Files that should be split (sizes from `wc -l`)

| File | Lines | Split into |
|---|---|---|
| `lib/api/queries.ts` | 1804 | one module per entity (`room`, `message`, `intelligence`, `organization`, `connector`, `admin`, `account`, `device`, `activity`), pure helpers to the entity `lib/` |
| `lib/i18n/en.ts`, `lib/i18n/pt-BR.ts` | 1237, 1243 | by domain (`room`, `composer`, `org`, `profile`, `admin`, ...) |
| `routes/_app/account/index.tsx` | 814 | seven feature folders (see 3.2) |
| `routes/_app/orgs/$orgId/connections/$connectorId.tsx` | 700 | `ChatsSection`, `ConnectorForm`, delete dialog |
| `components/composer.tsx` | 843 | `ComposerForm` (text, attachments, submit: `features/send-message`), voice branch (`features/send-voice-message`), mention branch (`features/mention-autocomplete`), edit bar (`features/edit-message`), drag and drop (`shared/lib`) |
| `components/message-item.tsx` | 666 | `MessageItem` (`widgets/conversation-timeline`), `AUTHOR_KIND`, `chatTrust`, `authorDisplayName`, `platformLabel`, `platformName`, `chatTitle` (`entities/message/model`), `TrustBadge`, `AttachmentView`, `ReplyContext`, `formatBytes` (`entities/message/ui`) |
| `components/message-timeline.tsx` | 465 | timeline (`widgets`), `useTimelineMessages` and `dayLabel` (model) |
| `components/question-bubble.tsx` | 433 | wizard state to `features/answer-question/model`, render to `entities/message/ui` |
| `components/member-settings.tsx` | 439 | `SettingsForm` and `KeywordsField` (`features/edit-member-settings/ui`), `memberSettingsSummary` and `changes` (`entities/room-member/model`) |
| `components/new-group-dialog.tsx` | 416 | `GroupForm` (feature), `Candidates` (shared picker molecule) |
| `components/schema-form.tsx` | 366 | `connectorFormSchema` (`entities/connector/model`), `ConnectorForm` (feature) |
| `components/profile-panel.tsx` | 358 | keep the widget; move `MembershipSummary` usage and the `canMessage` rule to the model |
| `components/conversation-list.tsx` | 365 | list (widget), row (`entities/room/ui`), `useFreshConversations` (hook) |
| `components/room-files.tsx` | 289 | list (widget), file row (`entities/message/ui`) |
| `components/image-viewer.tsx` | 330 | viewer (`entities/message/ui`), `collectImages` (model) |
| `components/message-menu.tsx` | 329 | menu (`features/delete-message` plus `features/react-to-message`), focus helpers to `shared/lib` |
| `lib/webm-duration.ts` | 663 | own module under `features/send-voice-message/lib` |
| `lib/live/handlers.ts` | 417 | per-entity handler files under `app/live` |
| `lib/schemas.ts` | 215 | base validators to `shared/lib/validation`, per-form schemas to their feature |
| `components/ui/sidebar.tsx` | 728 | shadcn-generated, leave as is |

### 3.4 Cross-cutting moves that unlock the table

1. Move the message helpers (`authorDisplayName`, `chatTrust`, `platformName`) out of `components/message-item.tsx` into `entities/message/model`. This breaks the cycle with `message-menu.tsx` and removes the `composer.tsx` and `rooms/$roomId.tsx` imports from `message-item`.
2. Move `conversationTitle`, `historyPlatform` and `canPost` into `entities/room/model` and `features/send-message/model`, so pages stop importing from components.
3. Move `initials` and `avatarTone` into `shared/lib/avatar` so `org-avatar.tsx` no longer imports from `components/intelligence`.
4. Decouple `components/ui/dialog.tsx`, `sheet.tsx`, `sidebar.tsx` from i18n and hooks by passing labels as props.

---

## 4. Composer draft bug: why text and attachments are lost

### 4.1 Mechanism

- `routes/_app/rooms/$roomId.tsx` L223-233 mounts `<Composer key={`composer-${roomId}`} ... />`. The `key` changes with the room, so React unmounts and remounts the whole subtree.
- `components/composer.tsx` `Composer` (L137-167) is a wrapper that renders `ComposerForm` (L169-185). All the draft state is local to `ComposerForm`:
  - `const [text, setText] = useState("")` (L189)
  - `const [attached, setAttached] = useState<AttachedFile[]>([])` (L190), `nextFileId` ref (L191)
  - `draft` / `shownEdit` for the edit flow (L236-237)
  - voice state through `useVoiceRecorder()` (L214). A recording is lost on remount.
  - `setText("")` and `setAttached([])` on successful send (L406-411).
- A remount with a new `key` starts again from `useState("")`, so the draft of the previous room is gone.
- `routes/_app.tsx` L89 `<Outlet />` is not keyed, and `RoomPage` itself is not remounted. So `RoomPage` keeps `replyChoice` (L91), `editChoice` (L95) and `focus` (L86). `replyChoice` and `editChoice` are cleared on room change by `liveMessage` (L92-93, L96-97). `focus` is not reset, which can scroll the next room to a stale message.
- The same remount happens on any navigation away from the room page (for example, to contacts) and when the phone shell swaps list and room. Any draft is lost on any unmount.

### 4.2 What exists today

- No draft store. A grep for `localStorage`, `sessionStorage` and `draft` finds only `lib/theme.ts`, the language switch, `question-bubble.tsx` L158 (answer wizard, local), `member-settings.tsx` L140 (keyword input, local), `schema-form.tsx` L304 (chip input, local). No draft is persisted or kept across mounts.
- `composer.test.tsx` has no case for a draft that survives a room switch.

### 4.3 Proposed home and shape

- **Where**: `features/send-message/model/draft-store.ts`. The composer is a feature; `entities/message` should only hold `Message` types and read models. zustand is already a dependency (`lib/live/store.ts` L1, `hooks/use-profile-panel.ts`).
- **Shape**:

```ts
// features/send-message/model/draft-store.ts (proposed)
type Draft = { text: string; files: AttachedFile[]; replyTo: string | null; recording?: VoiceRecording };
interface DraftState {
  drafts: Record<string /* roomId */, Draft>;
  setText(roomId: string, text: string): void;
  addFiles(roomId: string, files: File[]): void;
  removeFile(roomId: string, fileId: number): void;
  setReply(roomId: string, messageId: string | null): void;
  clear(roomId: string): void;          // on successful send / save
}
export const useDraftStore = create<DraftState>()((set) => ({ /* ... */ }));
export const useDraft = (roomId: string) => useDraftStore((s) => s.drafts[roomId] ?? EMPTY_DRAFT);
```

- **Edit draft**: keep it separate, keyed by message id (`editDrafts: Record<messageId, string>`), so that editing does not overwrite the new-message draft. Today the edit flow stashes the new-message text in `draft` (L236-243) and restores it afterwards; the store makes that unnecessary.
- **Persistence**: memory only. `File` objects cannot go to `localStorage`, and the requirement is "survives switching rooms", not reload. If reload survival is wanted later, store text only, or use IndexedDB.
- **Voice**: a recording in progress cannot survive a remount (it is a live `MediaRecorder`). Once stopped (`phase === "recorded"`), its `File` can be kept as `draft.files` or a `recording` field. Alternatively, block room switching during an active recording.
- **Clear**: on successful `post.mutate` (`composer.tsx` L406-411) and on successful voice send (L370-376), and on successful edit save (`saveEdit`, L417-430) for the edit draft.

### 4.4 What changes where

1. `components/composer.tsx`: replace `useState` for `text`, `attached` and `replyTo` with `useDraft(roomId)` and the store actions. `onChange` (L454-457) calls `setText(roomId, ...)`. `addFiles` (L497-509), `pickFiles` (L512-516), `onPaste` (L521-527), `onDrop` (L552-560) and the attachment remove (L692-694) call the store.
2. `routes/_app/rooms/$roomId.tsx` L224: drop `key={composer-${roomId}}`, or keep it only for caret and mention state (those can stay local). Move `replyChoice` (L91) into `features/send-message/model` keyed by room, so the reply strip also survives a switch.
3. `components/composer.tsx` `ComposerForm`: the mention and caret state (L198-201) can stay local, because they follow the caret, not the draft.
4. Tests to add: `composer.test.tsx` case "text and an attachment survive switching to another room and back", and "a sent message clears only its room's draft".

---

## 5. Quick stats

### 5.1 Largest non-test files (lines)

| Lines | File |
|---|---|
| 1804 | `lib/api/queries.ts` (`lib/api/types.gen.ts` 3021 and `routeTree.gen.ts` 717 are generated and excluded) |
| 1243 | `lib/i18n/pt-BR.ts` |
| 1237 | `lib/i18n/en.ts` |
| 814 | `routes/_app/account/index.tsx` |
| 843 | `components/composer.tsx` |
| 728 | `components/ui/sidebar.tsx` (shadcn) |
| 700 | `routes/_app/orgs/$orgId/connections/$connectorId.tsx` |
| 666 | `components/message-item.tsx` |
| 663 | `lib/webm-duration.ts` |
| 486 | `routes/_app/tokens.tsx` |
| 477 | `routes/_app/contacts.$id.tsx` |
| 465 | `components/message-timeline.tsx` |
| 464 | `routes/_app/rooms/$roomId.tsx` |
| 459 | `routes/_app/orgs/$orgId/positions.tsx` |
| 455 | `routes/_app/identities.tsx` |
| 444 | `routes/_app/orgs/$orgId/members.tsx` |
| 439 | `components/member-settings.tsx` |
| 433 | `components/question-bubble.tsx` |
| 417 | `lib/live/handlers.ts` |
| 416 | `components/new-group-dialog.tsx` |
| 366 | `components/schema-form.tsx` |
| 365 | `components/conversation-list.tsx` |
| 358 | `components/profile-panel.tsx`, `routes/_app/orgs/$orgId/settings.tsx` |
| 353 | `routes/_app/devices.index.tsx` |
| 332 | `routes/_app/contacts.index.tsx` |

### 5.2 Components used in one place

Non-`ui` components with exactly one importer (file-level, counted by grep on `@/…` import paths): `account-menu`, `add-connector-dialog`, `attachment-preview`, `audio-player`, `chart-tree`, `command-menu`, `composer`, `confirm-dialog` (only `conversation-menu`), `connect-agent-dialog`, `conversation-header`, `conversation-list`, `conversation-menu`, `conversation-search`, `end-session-dialog`, `image-viewer`, `inline-rename`, `invite-dialog`, `member-list`, `member-picker`, `member-settings`, `mention-suggestions`, `message-actions`, `message-timeline`, `participant-list`, `people-group`, `profile-panel`, `question-bubble`, `reaction-chips`, `room-files`, `room-settings-sheet`, `search-button`, `voice-controls`. Plus hooks `use-answer-question`, `use-debounced-value`, `use-disconnected`, `use-mark-room-read`, `use-open-conversation`, `use-react`, `use-stick-to-end`, `use-swipe-reply`, `use-visual-viewport`.

Single-use is not a problem by itself. It only matters for the ones in a duplicate group (1.A, 1.G, 1.K): consolidating those will change their importer counts.

### 5.3 Zero importers (dead)

- `components/ui/card.tsx`, `components/ui/table.tsx`, `components/ui/scroll-area.tsx`, `components/ui/select.tsx`.

### 5.4 Unused exports

- Name-based check over all non-test modules (`grep -w` on each exported symbol, excluding its own file): no exported symbol is referenced nowhere else. The check is name-based, so it can miss a shadowed name, but it found no clear dead export.
- `components/markdown.tsx` is only imported dynamically (`message-item.tsx` L39, `lazy(() => import(...))`). It is used.
- `lib/live/index.ts` is imported only from `routes/_app.tsx` (as `@/lib/live`).

### 5.5 Stale or misleading comments

- `components/org-avatar.tsx` L441-442: "the same greys the intelligences use" is out of date (see 1.F).
- `components/message-item.tsx` L105-107 notes a SPEC_DEVIATION about trust derivation. It is still accurate, and a good candidate for the `entities/message` model doc.

---

## Scripts used

- import-graph script: builds the import graph of `web/src` (non-test, non-generated), reports lib/components/hooks/ui upward imports and strongly connected components. Run with `python3 -I`. Output (summarised): one cycle (`message-item.tsx` <-> `message-menu.tsx`), five `components/ui` -> app imports, no `lib/`, `hooks/` upward imports.
