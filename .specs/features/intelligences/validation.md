# Intelligences Validation

**Date**: 2026-10-22
**Spec**: `.specs/features/intelligences/spec.md` (84 requirements)
**Diff range**: `be30f10^..d52458b` (96 commits, 390 files, +51 673 / −10 367)
**Verifier**: independent sub-agent (author ≠ verifier; the code was written by Sonnet 5.5, DeepSeek and Claude Opus workers)

## Validation: intelligences - FAIL ❌

**Result**: FAIL - 74/84 ACs matched their spec outcome; 4 are partial (one is a confirmed delivery-loss defect in merge, MRG-01) and 6 have spec-precision gaps. 9 of 32 sensor mutants survived. `make check-race` is flaky: run 1 failed on one load-sensitive takeover test (it fails 1 in 8 runs alone under `-race`), run 2 passed. `make check` and `make e2e` (20/20) pass. T54 (real Claude Code UAT) is still pending.

The core of the release is sound. The v0.3 → 0015/0016 migration preserves every row of every table and rolls back as one transaction. I found no cross-owner leak in DMs, profiles, avatars, folder identity or adoption. The failures below are bounded and each has a one-line fix.

---

## Task Completion

| Task | Status | Notes |
| --- | --- | --- |
| T1–T53 | ✅ Done | Every `Done when` is checked in `tasks.md`. |
| T54 | ⏳ Pending | The real Claude Code UAT is running in another worktree (`wnexous/pl-t54-sonnet`). It will add `uat.md`. Not verified here. |
| T55 | ✅ Done | Merge from the profile page. |

---

## Gate Check

| Gate | Command | Result |
| --- | --- | --- |
| Build | `make check` | ✅ exit 0 in 2m03s: 0 lint issues, vet clean, no generated-code drift. Go: 23 sharded packages + 14 others all `ok` (e.g. api 329, channel 111, cli 133, connectors 123, store 37 tests). Web: 48 files / 770 tests. Integrations: 70 tests. Build OK. |
| Race | `make check-race` | ⚠️ flaky. Run 1: exit 2 (9m36s). One failure: `TestTakenOverChannelNotifiesOnceAndDeliversNothingMore` (`internal/channel/session_lost_test.go:158`, expected the "after" message, got the earlier "before" message). Run alone: `go test -race -count=8 -run …` fails 1 of 8; without `-race`, 10 of 10 pass. **Re-run** of the full `make check-race` on a quieter machine: ✅ exit 0 (7m18s). The gate is flaky, not deterministically red. |
| E2E | `make e2e` (Chromium headless shell 1248) | ✅ 20 passed (10 tests × desktop-1280 + mobile-360) |

- **Test count**: Go top-level tests went from 802 to 1 154 (+352). Web `it`/`test` calls went from 318 to 679, e2e tests from 7 to 10 (×2 projects). No tests were deleted. The suite grew, and the tests replaced along with v0.3 code (projects, session keys) were replaced with intelligences equivalents.
- **Skipped tests**: none.
- **Root cause of the race failure** (read from the code, reproduced in isolation): `Gateway.run` only drops a live push whose seq is at or below `sent[room]` (`internal/agents/gateway.go:442`). `sent` is filled only by messages the backlog actually sent (`gateway.go:528`). If the old session already acked a message, the new session's backlog sends nothing for that room, so `sent[room]` stays 0. A late dispatcher push of the old message then reaches the taker a second time. The product result is a duplicate delivery after a takeover (at-least-once, Minor); the test then fails under load. Fix: seed `sent[room]` with the agent's cursor in `backlog`.

---

## Spec-Anchored Acceptance Criteria

Evidence is `file:line` plus the assertion. Every line cited was checked with `grep -n`/`sed -n`. ✅ = the asserted value is the spec outcome; ⚠️ = spec-precision gap; ❌ = partial or failed.

### AID - folder identity

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| AID-01 | (O,M,F) resolved; created with default name/handle | `internal/agents/folder_test.go:105-106` `Equal("Acme", i.DisplayName)`, `Equal("acme", i.Handle)` | ✅ |
| AID-02 | same id; `app` = new harness | `internal/agents/folder_test.go:112-114` `Equal(first.ID, again.ID)`; `internal/api/agent_folder_test.go:85,88` | ✅ |
| AID-03 | `name`, `name-2`, same display name | `internal/agents/folder_test.go:250,253` `Equal([]string{"notes","notes-2","notes-3"}, got)` | ✅ |
| AID-04 | tools-only call acts as (O,M,F) | `internal/api/agent_folder_test.go:57` author `[agent, ai.ID, …]` | ✅ |
| AID-05 | 422 `folder_required` | `internal/api/agent_folder_test.go:405` `RequireError(422, ErrorCodeFolderRequired)` on every route, `:410` WebSocket | ✅ |
| AID-06 | rename visible in timelines, members, mentions, signatures; 1–64 / 1–40 | `internal/api/message_authors_test.go:83,92`; `internal/api/mentions_test.go:42-45`; `internal/connectors/agent_rename_test.go:35`; `internal/intelligences/service_test.go:294-301` | ✅ |
| AID-07 | canonical folder + hostname on every call | `internal/channel/identity_test.go:51` `Equal(c.want, got)` (symlink, trailing slash, dot segments) | ✅ |

### SES - one live session

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| SES-01 | exact prompt text | `internal/cli/launcher_test.go:390` `Equal("Atlas is open in another session (Claude Code, since 09:30). Take over? [y/N] ", r.stderr)` | ✅ |
| SES-02 | takeover; old closed `taken_over` | `internal/cli/launcher_test.go:392` `PARTYLINE_TAKEOVER=1`; `internal/api/agent_live_test.go:135` `requireClosedWith(t, first, 4002, "taken_over")` | ✅ |
| SES-03 | no/enter → exit 1, harness not started | `internal/cli/launcher_test.go:416-417` `Equal(1, r.code)`, `False(r.execed)` | ✅ |
| SES-04 | 409 `identity_busy`; every tool answers the busy text | `internal/api/agent_live_test.go:88` `RequireError(409, ErrorCodeIdentityBusy)`; `internal/channel/session_lost_test.go:89` `Equal(wantBusy, text)` | ✅ (mutant M10b survived: the empty live-id case is untested) |
| SES-05 | one exact notice, then no deliveries | `internal/channel/session_lost_test.go:151` `Equal(wantTakenOver, n.Content)`, `:159` `expectNoNotification` | ✅ (flaky under `-race`, see Gate) |
| SES-06 | offline ≤5 s; next session needs no prompt | `internal/api/agent_live_test.go:224` 5 s deadline, `:241` | ⚠️ Only proven for a clean close. A half-open TCP connection takes ping 25 s + write 10 s (`internal/agents/gateway.go:36-37`). |
| SES-07 | no TTY → no prompt, no takeover unless flag/env | `internal/cli/launcher_test.go:441-444`, `:490` | ✅ |
| SES-08 | backlog, 50 cap + notice | `internal/api/agent_live_test.go:266` `Equal("5 older messages skipped; use the history tool", n.Text)` | ✅ |

### TREE - machine › folder tree

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| TREE-01 | machine groups; nested child | `internal/api/contacts_test.go:125` `shape(devbox.Nodes,0)`; `web/src/routes/_app/contacts.test.tsx:171`; e2e `web/e2e/smoke.spec.ts:476` | ✅ |
| TREE-02 | `c/d › e` collapsed; `~` for home | `internal/intelligences/tree_test.go:31` `{Path:"c/d",Name:"e"}`, `:47` `Name:"~"`; `contacts.test.tsx:181,183` | ⚠️ The server computes `TreeNode.Name`, but the web (`web/src/components/machine-group.tsx:159-166`) and CLI (`internal/cli/agents.go:103-106`) render `Path › DisplayName`. `~` appears only in the folder column. |
| TREE-03 | row fields; online machines first, then name | `internal/intelligences/tree_test.go:128`; `internal/api/contacts_test.go:244`; golden `internal/cli/agents_test.go:74` | ✅ (M13 killed) |
| TREE-04 | label replaces hostname | `internal/api/contacts_test.go:257-296` (64 ok, 65 → 422, '' resets, other owner 404) | ✅ |
| TREE-05 | filter keeps ancestors | `web/src/lib/contacts.test.ts:154,176-179` | ✅ |
| TREE-06 | Unlinked with merge/delete | `internal/api/contacts_test.go:141,235`; `contacts.test.tsx:580-592,718-737` | ✅ |
| TREE-07 | `partyline agents` tree | `internal/cli/agents_test.go:74-82` exact golden, `:90` empty text | ✅ |

### CONT / DM - contacts and private conversations

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| CONT-01 | own tree, shared AIs, users, people per connector | `internal/api/contacts_test.go:144,149,155`; `contacts.test.tsx:136` | ✅ |
| DM-01 | exactly one DM per pair | `internal/api/direct_messages_test.go:83`, `:152` `Equal(1, created)` (12 concurrent) | ✅ |
| DM-02 | 422 `dm_members_fixed` | `internal/api/direct_messages_test.go:296-298` `RequireError(422, ErrorCodeDMMembersFixed)` | ⚠️ A stranger gets 404 (`:304-306`), not 422 as "anyone" says. 404 is the right privacy choice; amend the spec. Disabled-counterpart edge case: mutant M18 survived. |
| DM-03 | `conversation="dm"`; reply in DM | `internal/api/dm_agent_test.go:62,69` | ✅ |
| DM-04 | `to:` creates DM if the owner sees the target, else `contact_not_found` | `internal/api/agent_conversations_test.go:70`, `:121` `RequireError(404, ErrorCodeContactNotFound)` | ✅ (M05 killed) |
| DM-05 | person only via the account the sender operates, else `no_route_to_person` | `internal/connectors/platform_dm_test.go:322`, `:331` `RequireError(422, ErrorCodeNoRouteToPerson)` | ✅ |
| DM-06 | together, newest first, unread per user | `internal/api/conversations_list_test.go:96,175,193` | ✅ (M14 killed) |
| DM-07 | mark read to newest | `web/src/routes/_app/rooms/conversation.test.tsx:322` `toEqual([{ seq: 5 }])`; `conversations_list_test.go:249` | ✅ |
| DM-08 | two participants post; AI owner and `use` grantees view; grants 422 | `internal/api/room_membership_access_test.go:85` `statuses(403,403,201,404,404)`, `:100` `statuses(422,422,422,404,404)`; `internal/access/access_test.go` matrix | ⚠️ A `*` (public) use grant does not give view (`internal/access/access.go:172`). That is safer than the spec text; amend it. DM-08 also conflicts with PEOP-04 (Gap 3). |

### GRP / AGRP - groups

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| GRP-01 | rooms are groups, routing unchanged | `internal/store/intelligences_migration_test.go:209`; `internal/api/upgrade_test.go:68` | ✅ |
| GRP-02 | people per bridged chat with platform | `internal/api/people_test.go:328-329` | ✅ |
| GRP-03 | authored by the person | `internal/connectors/people_test.go:111,117` | ✅ |
| GRP-04 | delivery modes, keywords, signatures, broadcast | `internal/api/member_settings_test.go:77`; `internal/api/no_broadcast_test.go:81`; `dm_agent_test.go:108` | ✅ |
| AGRP-01 | owned by the AI's owner, owner's add-member rules | `internal/api/agent_conversations_test.go:230,265` | ✅ (M29 killed) |
| AGRP-02 | nothing created; refused list | `internal/api/agent_conversations_test.go:264,269` | ✅ |

### PEOP - people

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| PEOP-01 | person per (connector, external id) | `internal/connectors/people_test.go:96,111,140,188-212` | ⚠️ Created only for routed messages (CON-07). This is written in contract-notes T26 but not in the spec. |
| PEOP-02 | linked account authored by the user | `internal/connectors/people_test.go:267`; `internal/api/people_test.go:304` | ✅ |
| PEOP-03 | name, apps, visible conversations | `internal/api/people_test.go:212,218,221` | ✅ |
| PEOP-04 | only connector owner/use see people; nobody else | `internal/api/people_test.go:153-158` (404 for carol/admin) | ⚠️ Built wider than the spec. Anyone who can view a conversation where the person was seen can read the profile (`internal/intelligences/service.go:396-401`). The design's security review allows this, the spec does not. |
| PEOP-05 | platform rename unless user-renamed | `internal/connectors/people_test.go:340,348,381` | ✅ (M24 killed) |
| PEOP-06 | trusted unchanged | `internal/connectors/people_test.go:303,305`; `internal/delivery/rules.go:96` | ✅ |

### ACC - accounts and operators

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| ACC-01 | owner or owner's AI; audit | `internal/api/connector_operator_test.go:99` `Len(audits, 1)`, `:136` 403, `:141-159` 422, `:170` fallback | ✅ |
| ACC-02 | platform DM → DM person↔operator | `internal/connectors/platform_dm_test.go:95,105,112`; catch-all `:197-198` | ✅ (M21 killed) |
| ACC-03 | operator AI unsigned | `internal/connectors/operator_signature_test.go:67` `Empty(…, "its own account: no author line")` | ✅ (M20 killed) |
| ACC-04 | AI signs per setting when a user operates | `internal/connectors/operator_signature_test.go:83,88,106` | ✅ |
| ACC-05 | own-account message authored by the operator | `internal/connectors/selfmsg_test.go:51-54,69`; echo `:109,164,174` | ✅ (M22 killed). The new trust source is not recorded in AD-006 (Gap 6). |
| ACC-06 | new group chat → group with operator | `internal/connectors/auto_groups_test.go:42,51,95,122` | ✅ |
| ACC-07 | apps on operator's page | `internal/api/connector_operator_test.go:218` `{claude, "Fake: @atlas_bot"}` | ✅ |

### PROF - profiles

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| PROF-01 | one intelligence per user/AI/person | `internal/intelligences/service_test.go:148,211`; `internal/connectors/people_test.go:113` | ✅ |
| PROF-02 | unique case-insensitive, 1–40 `a-z0-9-`, letter first | `internal/handles/handles_test.go:53,67`; `internal/store/intelligences_schema_test.go:59` | ✅ |
| PROF-03 | own edit shows everywhere | `internal/api/intelligences_handlers_test.go:52-91`; `message_authors_test.go:83`; `web/src/routes/_app/settings.test.tsx:125`; e2e `smoke.spec.ts:503` | ✅ |
| PROF-04 | `update_profile` on itself only | `internal/api/avatar_handlers_test.go:266,299`; `internal/intelligences/service_test.go:399,405` | ✅ |
| PROF-05 | owner edits AI; grantee 403 | `internal/api/intelligences_handlers_test.go:276` `statuses(200,403,404,404,404)` | ✅ (M03 killed) |
| PROF-06 | 422 `avatar_invalid`, old kept | `internal/api/avatar_handlers_test.go:159-190` | ✅ |
| PROF-07 | 256×256 PNG; served to who can see | `internal/api/avatar_handlers_test.go:115` `Equal(image.Rect(0,0,256,256), img.Bounds())`, `:141` 404 | ❌ Partial. The height bound is not tested (M23 survived: a 10×5000 image passes `DecodeConfig`). No positive test serves a person's avatar. |
| PROF-08 | initials on colour from id | `web/src/components/intelligence/intelligence.test.tsx:87-89` | ✅ |
| PROF-09 | badge + @handle next to every name | `web/src/components/intelligence/intelligence.test.tsx:160` `toHaveTextContent(/^AI$/)`, `:197` | ❌ Partial. Bare names without a badge in `web/src/components/conversation-header.tsx:15-19,58-60` (an owner viewing an AI's DM), `web/src/components/new-group-dialog.tsx:180` and `web/src/components/room-files.tsx:260`. |
| PROF-10 | 409 `handle_taken` | `internal/api/intelligences_handlers_test.go:242-247` | ✅ |
| PROF-11 | user rename beats platform | `internal/api/people_test.go:258,262` | ✅ |

### LAY - messenger layout

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| LAY-01 | avatar, name, 80-char preview, sender prefix, time, unread, newest first | `internal/api/conversations_list_test.go:358,375`; `web/src/components/conversation-list.test.tsx:143` `"Bob Lima: rolled back"` | ✅ |
| LAY-02 | live message to top; unread +1 unless open | `web/src/lib/live/handlers.test.ts:410,436,439,453` | ✅ (W2 killed; W1 on the read clamp survived) |
| LAY-03 | search names + members; new conversation/group | `conversation-list.test.tsx:314-357`; `web/src/components/new-conversation-dialog.test.tsx:81-151` | ✅ (member search covers the first 20 members only, `internal/conversations/list.go:59`) |
| LAY-04 | user owner + members, add-member rules | `internal/api/groups_test.go:28,83`; e2e `smoke.spec.ts:440` | ✅ |
| LAY-05 | <768 px sidebar first, full-screen conversation with back | `web/src/routes/messenger-shell.test.tsx:89,103`; e2e `smoke.spec.ts:541` (mobile-360) | ✅ |
| LAY-06 | header menu reaches the pages | `web/src/routes/messenger-shell.test.tsx:133-163` | ✅ |

### CLI - login and setup

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| CLI-01 | device flow; exact "Logged in as …" | `internal/cli/login_test.go:42` `Contains(…"Logged in as @alice on "+env.URL+" (machine devbox-7)\n")` | ✅ |
| CLI-02 | login opens menu when nothing registered | `internal/cli/setup_menu_test.go:163-165,181-182` | ✅ |
| CLI-03 | checklist keys, state, pre-selection | `internal/cli/menu_test.go:43-56,72-73,129-130`; width 30 `:183-189` | ✅ |
| CLI-04 | register/remove selected; one line per harness; nothing else | `internal/cli/setup_menu_test.go:49,57,61,64` | ❌ Partial. Lines are printed only for changed harnesses (`internal/cli/setup_harness.go:233-256`). Mutants M15 (registered-but-not-installed starts unchecked) and M16 (apply failure exits 0) survived. |
| CLI-05 | no TTY → flags; exit 2 + hint | `internal/cli/setup_menu_test.go:97-98,108,118,132` | ✅ |
| CLI-06 | "Run partyline login first", exit 1 | `internal/cli/setup_menu_test.go:143-144` | ✅ |
| CLI-07 | `setup --browser` | `internal/cli/setup_test.go:361-362` | ✅ |

### MIG - migration

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| MIG-01 | groups, no DMs, every message/member/grant/bridge/cursor kept | `internal/store/intelligences_migration_test.go:166` `Equal(before[table], e.rowsOf(table, cols))` for every table, discovered from `sqlite_master`; `:187` `pragma_foreign_key_check` empty; `:209-210` | ✅ All 23 pre-0015 tables are seeded and compared. Project grants and members of agentless projects are dropped by design (`0015_intelligences.sql:127-128`). |
| MIG-02 | adopt the owner's unlinked identity once | `internal/agents/folder_test.go:351,355,396,420,522-525` | ✅ (M26 killed: another owner's identity is never adopted) |
| MIG-03 | project member → most recently connected agent; projects removed | `internal/store/projects_conversion_test.go:77-92` | ✅ (M07 killed) |
| MIG-04 | chat senders → persons, alt ids folded | `internal/store/intelligences_backfill_test.go:140,169`; `intelligences_migration_test.go:236,252` | ✅ (M08 killed) |
| MIG-05 | one transaction; unchanged on failure | `internal/store/intelligences_migration_test.go:287` (hook fails), `:453` (0016 fails → 0015 rolled back); `internal/store/migrate_test.go:139` | ✅ (M09 killed) |

### MRG / NOTI

| AC | Spec-defined outcome | Evidence | Result |
| --- | --- | --- | --- |
| MRG-01 | groups (B wins), DMs merged, cursors max, grants, messages, delete A, audit | `internal/api/merge_test.go:211,218,249,275,316,345` | ❌ **Defect, reproduced.** When A's DM is appended to B's DM with the same counterpart, A's renumbered cursor is MAXed into B's (`internal/conversations/merge.go:288-301`). B's undelivered messages are then skipped for good. Repro: B has 2 unreceived messages, A has 1 received; after the merge B's cursor is 4 (expected 0). The counterpart's read cursor jumps the same way (`merge.go:303-312`). M19 survived. |
| MRG-02 | 409 `identity_online` | `internal/api/merge_test.go:423` `RequireError(409, ErrorCodeIdentityOnline)` | ✅ |
| NOTI-01 | choose one own linked account or none | `internal/connectors/notify_test.go:147,254` | ✅ |
| NOTI-02 | "<AI name>: <text>", links, outbox marked | `internal/connectors/notify_test.go:304` `Equal("Atlas: the build is green", sent.Text)`, `:358,417` | ✅ (M17 survived: every fixture has the user as `dm_a`) |
| NOTI-03 | retries; DM readable | `internal/connectors/notify_test.go:470,474` | ✅ |

**Status**: ❌ 74 ✅ / 6 ⚠️ (SES-06, TREE-02, DM-02, DM-08, PEOP-01, PEOP-04) / 4 ❌ (PROF-07, PROF-09, CLI-04, MRG-01).

---

## Edge Cases

- [x] Simultaneous takeover → exactly one live: `internal/api/agent_live_test.go:196-210`, `internal/agents/presence_test.go:163`
- [x] Folder >1024 bytes / NUL → 422 `folder_invalid`: `internal/agents/folder_test.go:303-304,321,330`; `internal/api/agent_folder_test.go:273,275,291,296` (M12 killed)
- [x] Same folder on two machines → two identities: `internal/agents/folder_test.go:230`. Caveat: hostnames are truncated to 64 runes, and an unreadable hostname becomes `"unknown"` (`internal/channel/identity.go:14`), so distinct machines can merge.
- [x] AI deleted → DMs deleted, memberships removed: `internal/api/direct_messages_test.go:386,402`. The DMs' attachment blobs are not swept (`internal/agents/registry.go:331-341`, Minor).
- [~] Disabled counterpart → readable, post 403: `internal/api/direct_messages_test.go:337,339,347`. Only proven when the user is `dm_a` (M18).
- [x] Operator AI deleted → DM with owner: `internal/connectors/platform_dm_test.go:245-249`
- [x] Unknown menu key ignored: `internal/cli/menu_test.go:53-56`
- [~] Width < 40: `internal/cli/menu_test.go:183-189` (width 30 only). Widths under 20 are clamped to 20 and wrap (`internal/cli/menu.go:159-161`).

---

## Discrimination Sensor

Every mutant ran in the detached scratch worktree `/home/dev/orca/projects/partyline-verify-intel-mut` (`git worktree add --detach … d52458b`), one at a time, followed by `go test -count=1` on the covering packages, or `bun x vitest run` for W*. The scratch was removed with `git worktree remove --force`. `git status --porcelain` of the verify worktree was empty before and after.

| # | File:line | Mutation | Killed? |
| --- | --- | --- | --- |
| M01 | `internal/access/access.go:149` | AI owner/grantee gets `post` instead of `view` on the AI's DM | ✅ `TestDMRoomLevelMatrix` |
| M02 | `internal/access/access.go:169` | any user "owns" the AI participant (cross-owner DM read) | ✅ `TestDMRoomLevelMatrix`, `TestDMOfAPublicAIIsNotVisibleToEveryone` |
| M03 | `internal/intelligences/service.go:389` | `use` grantee may edit the AI | ✅ `TestCanSeeAndCanEdit`, `TestGetProfile` |
| M04 | `internal/intelligences/service.go:401` | every user sees every person (PEOP-04 leak) | ✅ `TestPersonProfileRoutesAuthMatrix`, `TestOpenDMWithAPersonHasNoRoute` |
| M05 | `internal/conversations/dm.go:100` | DM target visibility check removed (DM-04) | ✅ `TestAIDMTargetsAreThoseItsOwnerSees` |
| M06 | `internal/conversations/dm.go:78` | AI new-conversation rate limit removed | ✅ `TestOnlyNewDMsOfAnAICountAgainstTheLimit` |
| M07 | `internal/store/migrations/0015_intelligences.sql:111` | MIG-03 picks the least recently connected agent | ✅ `TestUpgradeV03KeepsEveryRow`, `TestUpgradeV03Identities` |
| M08 | `internal/store/intelligences_backfill.go:333` | linked chat senders not attributed to the user (MIG-04/PEOP-02) | ✅ `TestBackfillMessageAuthors` |
| M09 | `internal/store/store.go:190` | hook error ignored (MIG-05) | ✅ `TestMigrateFailingHookRollsBackEverything`, `TestUpgradeV03RollsBackOnHookFailure` |
| M10b | `internal/agents/presence.go:93,124` | a connection with no live id counts as the holder's process (silent replace) | ❌ **Survived** |
| M11 | `internal/agents/presence.go:94` | busy check removed (SES-04) | ✅ `TestPresenceRefusesASecondProcess` |
| M12 | `internal/agents/folder.go:91` | folder length bound off by one | ✅ `TestResolveFolderValidation` |
| M13 | `internal/intelligences/tree.go:202` | offline machines sort first (TREE-03) | ✅ `TestBuildMachinesOrder` |
| M14 | `internal/store/db/conversations.sql.go:20` | unread ignores the upgrade baseline (DM-06) | ✅ `TestUnreadCountsPerUser` |
| M15 | `internal/cli/setup_harness.go:144` | registered-but-not-installed harness starts unchecked (Enter removes it) | ❌ **Survived** |
| M16 | `internal/cli/setup_harness.go:162` | menu apply failure exits 0 | ❌ **Survived** |
| M17 | `internal/rooms/notify.go:23-26` | notify target = `dm_a` always | ❌ **Survived** |
| M18 | `internal/rooms/messages.go:111-114` | counterpart check uses the owner's id for AI posts | ❌ **Survived** |
| M19 | `internal/conversations/merge.go:293-296` | every appended agent cursor credited to the survivor | ❌ **Survived** |
| M20 | `internal/connectors/sender.go:93` | every author unsigned (ACC-03/04) | ✅ `TestAIThroughSomeoneElsesAccountSignsPerItsSetting` |
| M21 | `internal/connectors/autoroute.go:41` | catch-all connectors use ACC-06 routing (Spec issue 2) | ✅ `TestCatchAllRoutesNewDMs`, `TestCatchAllScopes` |
| M22 | `internal/connectors/selfmsg.go:73` | Partyline's own echo not dropped (ACC-05) | ✅ `TestEchoOfAPartylineSendIsDropped`, `TestEchoBeforeTheSendReturnsIsDropped` |
| M23 | `internal/avatars/avatars.go:52` | height bound removed (decompression bound) | ❌ **Survived** |
| M24 | `internal/intelligences/service.go:535` | any person edit locks the name (PROF-11) | ✅ `TestInboundUserRenameSurvivesPlatformRename` |
| M25 | `internal/agents/gateway.go:308` | takeover header inverted | ✅ `TestASecondSessionIsRefusedWhileTheFirstIsLive` |
| M26 | `internal/store/db/agents.sql.go:276` | adoption ignores the owner (cross-owner MIG-02) | ✅ `TestResolveFolderIgnoresAStaleAdopt` |
| M27 | `internal/store/db/agents.sql.go:169` | folder resolution ignores the owner (cross-owner AID) | ✅ `TestResolveFolderIdentityIsOwnerMachineAndFolder` |
| M28 | `internal/access/access.go:131` | group user members lose `post` (AD-011) | ✅ `TestGroupUserMemberPosts` |
| M29 | `internal/conversations/groups.go:133` | owner's add-member rules ignored (AGRP-01) | ✅ `TestAIGroupWithARefusedMemberCreatesNothing` |
| M30 | `internal/connectors/autoroute.go:210` | senderless platform DM is routed (contract-notes T30 "not routed") | ❌ **Survived** |
| W1 | `web/src/lib/api/queries.ts:540` | `room.read` clamp removed (`unread = after`) | ❌ **Survived** |
| W2 | `web/src/lib/live/handlers.ts:174` | own live messages count as unread | ✅ `handlers.test.ts` |

The Attach-only variant of M10b (`presence.go:93` alone) also survived. It is unreachable through the gateway, because `Presence.Busy` runs first (`internal/agents/gateway.go:189`).

**Sensor depth**: P0-full (critical feature): 32 behaviour-level mutants across migration, authorization, live session, folder identity, operators/signatures, conversations, CLI and web.
**Result**: 23/32 killed - FAIL ❌ (9 survivors → fix tasks).

---

## Interactive UAT Results

T54 (real Claude Code session in a folder; resume; takeover prompt; web DM; `send_message to:`; `update_profile`; `create_group`; `partyline agents`; login/setup) is being run by another worker and will be recorded in `uat.md`. Not performed here.

---

## Code Quality

| Principle | Status |
| --- | --- |
| Minimum code | ✅ No speculative abstractions found in the reviewed paths. |
| Surgical changes | ✅ v0.3 project/session-key code removed in T16/T50, with tests replaced. |
| No scope creep | ⚠️ PEOP-04 (conversation-shared people) and DM-02 (404 for strangers) differ from the spec text without a spec amendment. |
| Matches patterns | ✅ sqlc + raw-SQL hooks (AD-012), DTO/tygo contract (AD-003), one authorization package. |
| Spec-anchored outcome check | ⚠️ 4 partial, 6 spec-precision gaps (above). |
| Per-layer coverage (domain 1:1; routes happy+edge+error) | ⚠️ The participant-order branches (`dm_a`/`dm_b`) are untested (M17, M18, M19). Routes have auth matrices (`internal/api/authmatrix_test.go` and per-route matrices). |
| Every test maps to a requirement | ✅ Test names and comments cite AC ids throughout. |
| Documented guidelines followed: `AGENTS.md`, `contract-notes.md`, `.specs/LESSONS.md` (L-017/018/019/029) | ⚠️ L-019 recurred: contract-notes T30 "a sender without any id … not routed" has no test (M30). |

---

## Gaps and Fix Plans (ranked)

1. **Major - MRG-01 merge drops undelivered messages.** `internal/conversations/merge.go:288-313`. The fix task: when appending a DM, advance a party's cursor in the target only if that party's existing target cursor already reached the target's pre-merge last seq; otherwise keep its cursor. Apply the same rule to read cursors. Add a test where B has an unreceived message in its DM with X and still receives it after the merge, and one where the counterpart is an AI with a cursor (kills M19).
2. **Major (gate) - `make check-race` intermittently fails on `TestTakenOverChannelNotifiesOnceAndDeliversNothingMore`.** Root cause: `internal/agents/gateway.go:442` with `:528`. Fix: seed `sent[room]` with the agent's cursor in `backlog`, so live pushes at or below the acked cursor are dropped. That also removes the duplicate delivery after takeover.
3. **Major (decision) - PEOP-04 vs DM-08.** `internal/access/access.go:138-176` gives an AI's `use` grantees `view` on the AI's platform DMs with external people. Through `internal/intelligences/people.go:73` they then see those people's profiles and avatars without any connector access. The spec's "nobody else" (PEOP-04) and "use grantees view" (DM-08) contradict each other here. Fix: André decides. Either require `CanUseConnector` for DMs that have a chat member, or amend PEOP-04.
4. **Minor - surviving mutants M17, M18 (participant order).** Add NOTI-02 and disabled-counterpart tests where the AI is `dm_a` (create the AI before the user). Files: `internal/connectors/notify_test.go`, `internal/api/direct_messages_test.go`.
5. **Minor - M10b.** Add a presence/gateway test: two live connections without `X-Partyline-Live` → the second gets 409 `identity_busy` (`internal/agents/presence_test.go`, `internal/api/agent_live_test.go`).
6. **Minor (decision record) - ACC-05 trust.** Own-account platform messages are stored with `author_user_id` = connector owner (`internal/connectors/selfmsg.go:22,50`), so the owner's AIs trust them. AD-006 does not list this source, and catch-all connectors now post these messages where CON-12 used to drop them. Fix: record an AD-006 amendment in `.specs/STATE.md`.
7. **Minor - PROF-07 / M23.** Add a 10×5000 avatar case (`internal/avatars/avatars_test.go`) and a positive person-avatar serving test (`internal/api/people_test.go`).
8. **Minor - PROF-09 badges.** Render `IntelligenceName` in `web/src/components/conversation-header.tsx:15-19,58-60` (both participants when there is no counterpart), `web/src/components/new-group-dialog.tsx:180` and `web/src/components/room-files.tsx:260`.
9. **Minor - CLI-04 / M15 / M16.** Test a registered-but-not-installed harness and a failing apply in the menu path (`internal/cli/setup_menu_test.go`). Amend the spec to "one line per changed harness", or print unchanged lines. Also, `a` selects harnesses that are not installed (`internal/cli/menu.go:107-117`), which then fail to apply. Limit `a` to installed harnesses.
10. **Minor - M30 / L-019.** Add a test that a senderless platform DM is not routed (`internal/connectors/platform_dm_test.go`).
11. **Minor - W1.** Add a `markConversationRead` test with `after > unread` (`web/src/lib/api/queries.test.ts` or `handlers.test.ts`).
12. **Minor - notify outbox visibility.** `purpose=notify` outbox items, with their chat id and errors, are rendered to every DM viewer (`internal/api/convert/convert.go:113`, `internal/live/hub.go:370-379`). Show them to the DM's user only.
13. **Minor - spec text.** Amend SES-06 (bound for unclean disconnects), TREE-02 (what renders `~`), DM-02 (404 for callers who cannot view), DM-08 (`*` grants excluded) and PEOP-01 (routed messages only).
14. **Minor - other findings.** Unread for a user added to a group later counts all history since the upgrade baseline (`internal/store/queries/conversations.sql:57-64`). Operator validation races deletion (`internal/connectors/service.go:374,401`, 500 instead of 422). `settle` blocks the adapter's inbound loop up to 10 s (`internal/connectors/selfmsg.go:56-78`). People can squat AI handles (`internal/connectors/people.go`). For pi/OpenCode, the tools half of a session refused `identity_busy` still acts as the AI (`internal/channel/run.go:105`; allowed by the "Tools-only processes" assumption, but worth a note in `docs/agents.md`).

---

## Requirement Traceability Update

| Requirement | Previous Status | New Status |
| --- | --- | --- |
| 74 ACs marked ✅ above | Implementing | ✅ Verified |
| SES-06, TREE-02, DM-02, DM-08, PEOP-01, PEOP-04 | Implementing | ⚠️ Verified with spec-precision gap (spec amendment) |
| MRG-01 | Implementing | ❌ Needs Fix (Gap 1) |
| PROF-07, PROF-09, CLI-04 | Implementing | ❌ Needs Fix (Gaps 7, 8, 9) |
| AID-01/02, SES-01..03/05, DM-03/04, PROF-04, AGRP-01, TREE-07, CLI-01/04 | - | also pending T54 UAT |

---

## Summary

**Overall**: ❌ Not Ready. The core is ready; the fix list is small.

**Spec-anchored check**: 74/84 matched; 6 spec-precision gaps; 4 partial (1 confirmed defect).
**Sensor**: 23/32 mutations killed.
**Gate**: `make check` ✅, `make e2e` ✅ 20/20, `make check-race` ⚠️ flaky: run 1 failed on one test, run 2 passed; the test fails 1 in 8 runs alone under `-race`.

**What works**:
- The migration preserves every row of all 23 v0.3 tables and is atomic across 0015 + 0016.
- DM privacy holds (participants post, owner and grantees view, admins none, grants refused).
- Folder identity and adoption are owner-scoped.
- Busy/takeover protocol, operators and signatures, catch-all compatibility, people per connector, profiles and avatars, the messenger web shell and the CLI login/setup/agents all behave as specified.

**Production-release blockers**: none in the migration or the authorization core. Fix before release: Gap 1 (merge loses deliveries), Gap 2 (race-gate flake / duplicate after takeover), Gap 3 (privacy decision), and complete T54.

**Next steps**: route Gaps 1–11 to fix tasks (one phase), get André's decision on Gap 3 and Gap 6, then re-verify (iteration 1 of 3).

---

## Lessons distilled

Lessons were recorded with `scripts/lessons.py`; `.specs/LESSONS.md` is re-rendered from them.

- **Added (candidates)**:
  - L-043: merge cursor (MRG-01)
  - L-044: test both orderings of a pair (M17–M19)
  - L-045: CLI failure paths (M15, M16)
  - L-046: clamps (W1)
  - L-047: visibility conflicts between ACs (PEOP-04/DM-08)
  - L-048: amend the spec when the design changes an outcome
  - L-049: timing on unclean disconnects (SES-06)
  - L-050: drop pushes at or below the acked cursor (check-race failure)
- **Promoted**: L-042 (every condition of a match predicate gets a test where only it excludes) recurred in a second feature (M10b, M23) and is now confirmed.
- **Penalized**: L-019 ("every behaviour written into contract notes needs a test") was loaded for this feature. It recurred: contract-notes T30's "a sender without any id … not routed" is untested (M30). This is its second penalty, so the script quarantined it.
