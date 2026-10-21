# Room Files and Token Hygiene Validation

## Validation: files-and-tokens - PASS ✅ (W12 closed)

> **Gap closure (orchestrator, 2026-10-21)**: commit "test(web): offer delete only on revoked token rows" asserts the expired, never-revoked row ("old export") has no Delete button while the revoked row has one (`web/src/routes/_app/tokens.test.tsx`). Re-applied W12 (`state === "revoked"` → `state !== "active"` in `tokens.tsx`) in the working copy and reverted: 1 failed. `make check` EXIT 0 after the change.

### Verdict before gap closure (kept for history): FAIL ❌

**Date**: 2026-10-21
**Spec**: `.specs/features/files-and-tokens/spec.md` (10 ACs FILES-01..06, TOKH-01..04 + 2 edge cases)
**Diff range**: `59ec52d..1718767` (a7e9ae0 T1, b4fb044 T2, b16e48d T3, 1718767 T4; base v0.2.1)
**Verifier**: independent sub-agent (author ≠ verifier), detached worktree `partyline-verify-files` at 1718767
**Verdict**: FAIL ❌. The gates are green and all 10 ACs and both edge cases have spec-anchored evidence. Of 34 effective mutants, 33 were killed. One survived (W12: the "Delete" action also shows on expired tokens that were never revoked). The production code behaves correctly here; the fix is one missing test assertion.

---

## Task Completion

| Task | Status | Notes |
| ---- | ------ | ----- |
| T1 | ✅ Done | a7e9ae0 `GET /rooms/{id}/attachments` |
| T2 | ✅ Done | b4fb044 include_revoked, purge, retention task |
| T3 | ✅ Done | b16e48d Files tab, live append, open in timeline |
| T4 | ✅ Done | 1718767 tokens page toggle and delete. W12 shows the "Delete only on revoked rows" rule is not pinned by a test |

---

## Gate Check

- **`make check`** (generate drift, golangci-lint `0 issues`, vet, web lint/typecheck, `go test -race ./...`, web vitest, integrations, web build, go build): **EXIT 0**. All 31 Go packages pass. Web: 325/325 tests in 32 files. Integrations: 53 pass, 0 fail.
- **`make e2e`** (`PLAYWRIGHT_CHROMIUM_EXECUTABLE=…chromium_headless_shell-1248…`): **EXIT 0**, 10/10 passed (desktop-1280 + mobile-360). The feature adds no e2e test of its own; the smoke suite is unchanged.
- **Test delta (diff range)**: +7 Go `func Test` and +17 web `it(`. No test was removed and no skips were added.
- **Edits to existing tests: assertions kept or made stronger.**
  - `internal/api/admin_handlers_test.go:239-247` TestTokenEndpoints now asks with `?include_revoked=true` and adds `require.True(found)`. Before, the loop passed silently if the revoked token was missing; now a missing token fails the test.
  - `internal/api/agent_register_test.go:71-75` TestAgentRevokesOwnToken keeps its exact map `{"laptop": true, "desktop": false}`. That map still requires the revoked token to be listed, which `include_revoked=true` makes possible.
  - `internal/api/devices_handlers_test.go:122,135,262` `require.Empty` with `include_revoked=true` still proves "no token was created, revoked or not". With the new default, the old call would have passed vacuously for a revoked token.
  - `internal/cli/setup_test.go:366-368` TestSetupBrowserApproverConfirmation keeps `Len 1` + `RevokedAt NotNil`, so the declined token must still exist and be revoked.
  - `web/src/routes/_app/tokens.test.tsx:54` (UI-19 list) turns the toggle on first. All of its old assertions are unchanged.

---

## Spec-Anchored Acceptance Criteria

| Criterion | Spec-defined outcome | `file:line` + assertion | Result |
| --- | --- | --- | --- |
| FILES-01 list newest first, default 50, max 100, fields + kind | 200; newest first; id, name, mime, size, status, created_at, message id, author name, kind | `internal/api/files_handlers_test.go:60` exact name order; `:64-68` full `dto.RoomFile` equality (incl. message_id/seq, author_kind/name); `:85-89` kind per MIME (incl. `IMAGE/JPEG`); `:130-132` `Len 50` + `HasMore`; `:134-136` `limit=500` → `Len 100`; `:148` cursor walk == full list (no gaps/repeats); `:154` cursor inside kind filter | ✅ |
| FILES-02 no view → 404 | 404 | `internal/api/files_handlers_test.go:175` matrix `statuses(200,200,200,404,404)` (member without grant and admin without grant 404); `:180` unknown room 404; `:181` anon 401 | ✅ |
| FILES-03 kind filter; unknown → 422 | only that kind; 422 | `internal/api/files_handlers_test.go:91-94` each kind exact list; `:96-97` `RequireError(422, invalid)` + `Fields` has `kind`; `:178` 422 for grantees, 404 (not 422) for strangers, so no existence leak | ✅ |
| FILES-04 Files tab: thumbnail/icon, name, size, author, time, filter, open in timeline | listed with those fields, filterable, jump to message | `web/src/components/room-files.test.tsx:56-77` (img src, link href, `download`, "500 KB", author, `<time dateTime>`); `:88,93,102` kind query param per filter; `:115` `onOpen({id:"m-r1-4",seq:4})`; `web/src/routes/_app/rooms/room-files.test.tsx:66-71` header opens Files tab; `:101-112` loads the older page (`before_seq=51`) and scrolls the row into the viewport | ✅ |
| FILES-05 live append without reload | new files shown, no refetch | `web/src/components/room-files.test.tsx:222-232` order `[f21,f20,f3,f1]` + `filesCalls` unchanged; `:252` dedupe/other room ignored; `:287-288` filtered list adds only matching kind; `:267` catch-up refetch after reconnect | ✅ |
| FILES-06 too_large/unavailable shown as unavailable, no link | no download link | `web/src/components/room-files.test.tsx:179-185` text "too large to store"/"unavailable", no `link`, no `a`, no `img`; backend lists them with status `internal/api/files_handlers_test.go:74-79` | ✅ |
| TOKH-01 default omits revoked | revoked absent unless include_revoked=true | `internal/api/token_hygiene_test.go:37-38` default and `=false` → `[keep]`; `:39-43` `=true` → both, with `RevokedAt` set only on the revoked one | ✅ |
| TOKH-02 purge own revoked → delete + audit; active → 409 | 204, gone, audit `token.purge`; 409 | `internal/api/token_hygiene_test.go:45` `RequireError(409, conflict)`; `:47` other user 404; `:51-53` 204, gone, second purge 404; `:59-71` exactly one `token.purge` entry, target, actor = bob; `internal/users/token_purge_test.go:71,90` | ✅ |
| TOKH-03 retention deletes tokens revoked > 30 days | 31 d deleted, 29 d kept | `internal/users/token_purge_test.go:44-54` fake clock: 31 d → n=1, 29 d kept, active + expired-unrevoked kept; `internal/app/token_retention_test.go:38-44` real job in `App.Start` deletes the 31 d token, keeps the 29 d one | ✅ |
| TOKH-04 hidden by default, "Show revoked" toggle, Delete on revoked rows with confirmation | hidden; toggle (remembered); confirm dialog → purge | `web/src/routes/_app/tokens.test.tsx:181` fetch uses include_revoked=true; `:184` revoked hidden; `:189-193` toggle off → on, localStorage; `:199-202` remembered + off again; `:209` no toggle when none revoked; `:231-238` alertdialog "Delete old laptop?" → only `DELETE /tokens/t2/purge`, row gone, dialog closed; `:253-256` failure keeps dialog and row | ✅ (W12 survived: nothing pins "Delete only on revoked") |

**Edge cases**

- [x] Empty room → empty state: `web/src/components/room-files.test.tsx:135-138` (exact title + body). API returns `items: []`, not null: `internal/api/files_handlers_test.go:99-102`.
- [x] Purged secret → 401 like an unknown token: `internal/api/token_hygiene_test.go:56`; `internal/users/token_purge_test.go:80` `ErrUnauthenticated`. Note: a token can only be purged after it is revoked, so this outcome already held before the feature. No mutant can separate the two cases.

**Status**: ✅ All ACs covered. No spec-precision gaps. Minor note on FILES-04: only images the browser can show inline (png/jpeg/gif/webp) get a thumbnail. SVG and other image types show the image icon, matching the timeline's inline rule. The spec just says "thumbnail (images)".

---

## Discrimination Sensor

Scratch: `git worktree add --detach /tmp/claude-1001/pl-files-mutants 1718767`. One mutant at a time, with `git checkout -- .` between them. Targeted tests: `go test ./internal/api -run 'TestRoomFiles|TestTokenHygiene|TestTokenEndpoints'`, `./internal/users -run TestPurge` and `./internal/app -run TestRetentionPurges`, plus vitest on `room-files.test.tsx` (component and route) and `tokens.test.tsx`. The scratch was removed. Real-tree `git status --porcelain` was empty before and after (unchanged).

| # | File | Mutation | Killed? |
| - | ---- | -------- | ------- |
| B1 | `internal/rooms/files.go:45` | kind filter ignored (`Kind: ""`) | ✅ TestRoomFilesList, Paging |
| B2 | `internal/store/db/files.sql.go:22` | cursor `a.id <` → `<=` | ✅ Paging |
| B3 | `internal/rooms/files.go:45` | fetch `limit` not `limit+1` (has_more) | ✅ Paging |
| B4 | `internal/api/files_handlers.go:15` | default page 50 → 20 | ✅ Paging |
| B5 | `internal/rooms/files.go:28` | view authorization bypassed | ✅ AuthMatrix (stranger/admin) |
| B6 | `internal/rooms/files.go:31` | unknown kind accepted | ✅ List, AuthMatrix |
| B7 | `internal/store/db/files.sql.go:22` | room filter dropped (cross-room leak) | ✅ List |
| B8 | `internal/api/admin_handlers.go:214` | include_revoked default on | ✅ TokenHygieneEndpoints |
| B9 | `internal/users/tokens.go:141` | active purge → ErrNotFound (404 not 409) | ✅ |
| B10 | `internal/store/db/token_purge.sql.go:14` | purge also deletes active tokens | ✅ |
| B11 | `internal/users/tokens.go:143` | purge audit entry not recorded | ✅ |
| B12 | `internal/store/db/token_purge.sql.go:14` | purge ignores owner (`user_id = ? OR 1`) | ✅ (carol 404) |
| B13 | `internal/users/tokens.go:134` | other user's token → 409 (ownership oracle) | ✅ |
| B14 | `internal/users/tokens.go:152` | effective retention 28 d (29 d purged) | ✅ |
| B15 | `internal/users/tokens.go:152` | effective retention 32 d (31 d kept) | ✅ |
| B16 | `internal/app/app.go:244-245` | retention task not wired | ✅ TestRetentionPurgesOldRevokedTokens |
| B17 | `internal/store/db/token_purge.sql.go:17` | retention also deletes unrevoked tokens | ✅ |
| B18+B19 | `internal/rooms/files.go:40` + `internal/api/files_handlers.go:15` | both 100-caps removed | ✅ (`Len 100` vs 130). Each cap removed alone survives because the other one still enforces the limit. Equivalent, not counted |
| W1 | `web/src/lib/api/queries.ts:464` | `kind` param never sent | ✅ |
| W2 | `web/src/lib/api/queries.ts:471` | cursor = first id instead of last | ✅ |
| W3 | `web/src/components/room-files.tsx:187` | too_large treated as stored (link) | ✅ |
| W4 | `web/src/lib/live/handlers.ts:151` | live `prependFiles` removed | ✅ |
| W5 | `web/src/lib/live/handlers.ts:121` | live append ignores filter | ✅ |
| W6 | `web/src/lib/live/handlers.ts:290` | no files refetch on catch-up | ✅ |
| W7 | `web/src/components/room-files.tsx:132` | open-in-timeline wrong seq | ✅ |
| W8 | `web/src/components/message-timeline.tsx:158` | focus never loads older pages | ✅ route test |
| W9 | `web/src/routes/_app/tokens.tsx:86` | toggle default on | ✅ |
| W10 | `web/src/routes/_app/tokens.tsx:90` | revoked never filtered | ✅ |
| W11 | `web/src/routes/_app/tokens.tsx:474` | dialog not closed after success | ✅ |
| **W12** | `web/src/routes/_app/tokens.tsx:252` | Delete offered on `state !== "active"` (expired, unrevoked rows too) | ❌ **Survived** |
| W13 | `web/src/lib/api/queries.ts:233` | purge calls the revoke endpoint | ✅ |
| W14 | `web/src/routes/_app/tokens.tsx:93` | toggle not persisted | ✅ |
| W15 | `web/src/routes/_app/tokens.tsx:469` | confirm closes before outcome (no `preventDefault`) | ✅ failure test |

**Sensor depth**: expanded. The feature touches auth: room access and token ownership.
**Sensor outcome (before closure)**: 33/34 effective mutants killed. 1 survived (W12). B18 and B19 alone are equivalent mutants (the limit is capped in two places); their combination is killed. Result: FAIL ❌.

---

## Code Quality

| Principle | Status |
| --------- | ------ |
| Minimum code | ✅. Minor: the page limit is capped twice (`httpx.ParsePage` and `rooms.Files`), and `files.sql` repeats the MIME→kind `CASE` for the select and the filter |
| Surgical changes | ✅ `MembersPanel` became a two-tab `SidePanel`; timeline `focus` prop is additive |
| No scope creep | ✅. "Open in timeline" loads older pages and shows a toast when the message is gone, which FILES-04 requires |
| Matches patterns | ✅ authorize → `access.ErrNotFound` (404), `validation.Check` (422), `WithTx` + `audit.Record`, `dto.Page` cursor, sqlc + tygo regenerated (check-generate clean) |
| Spec-anchored outcome check | ✅ every AC asserts the spec's status codes and values |
| Per-layer coverage (services 1:1, endpoints happy+edge+error+auth matrix, web per visible AC) | ✅ |
| Every test maps to a requirement | ✅. The load-error/retry tests map to the panel failure state (L-028) |
| Guidelines followed: `AGENTS.md`, `.specs/LESSONS.md` (L-017 409 observed, L-018 isolated negatives, L-016 29/31 boundaries, L-028 failure state) | ✅ |

**UI (web-design-guidelines)**: icon-only buttons have `aria-label`: the header Files toggle (`$roomId.tsx`) and "Open {name} in the timeline" (`room-files.tsx:267`). Thumbnails use `alt=""` + width/height + `loading="lazy"` (`room-files.tsx:207-215`). Text truncates with `min-w-0`. The tape-settle animation honors `prefers-reduced-motion` (`index.css:235`). Strings use `…` and curly `’` in en and pt-BR. The Switch label uses `htmlFor`. Deletion asks for confirmation in an alertdialog. axe is clean in en and pt-BR (`web/src/routes/a11y.test.tsx`, files tab). Minor, not blocking:
- The files filter and the selected panel tab are not reflected in the URL.
- "Load older files" is not virtualized, so lists past ~100 rows render everything.
- `$roomId.tsx` has two controls named "Files" (the header toggle and the tab). That is fine, but it adds noise for screen readers.

---

## Security Review

- **Cross-room attachment listing**: closed. The query filters on `v.room_id` after `authorize(view)` (`internal/rooms/files.go:28`, `files.sql`), proven by B7 and B5. A `before` cursor from another room only bounds the id range inside the caller's own room, so it leaks nothing. Strangers get 404 even with an invalid `kind` (`files_handlers_test.go:178`), so the kind check does not reveal whether a room exists.
- **Purging another user's token**: closed. The SQL is scoped to `user_id` (B12), and the fallback answers 404 rather than 409 for foreign tokens (B13), so the endpoint does not reveal which token ids exist. Agent tokens cannot reach `/api/v1/tokens/*` (`internal/api/middleware.go:221` agentRestriction). The purge is audited with the actor.
- **Observations (no fix required)**:
  - Ordering and the cursor use the attachment's UUIDv7 (insertion time), not the message time. Files from backfilled or imported chat history sort by import time.
  - `before` is not validated as a UUID; any string is accepted and only narrows the range.

---

## Fix Plans

### Fix 1: pin "Delete only on revoked rows" (W12)

- **Root cause**: `tokens.test.tsx` checks that an expired token is still listed (`:186`) and that active rows have no Delete (`:228`). Nothing asserts that an expired but unrevoked row has no Delete. The backend would answer 409 for such a delete.
- **Fix task**: in `web/src/routes/_app/tokens.test.tsx` "hides revoked tokens by default…" (or the delete test), add `expect(within(row("old export")).queryByRole("button", { name: /^Delete/ })).toBeNull()`. Re-apply W12 (`state === "revoked"` → `state !== "active"` at `tokens.tsx:252`) and confirm it fails. No production change.
- **Priority**: Minor (test-only).

---

## Requirement Traceability Update

| Requirement | Previous Status | New Status |
| ----------- | --------------- | ---------- |
| FILES-01..06 | Implementing | ✅ Verified |
| TOKH-01..03 | Implementing | ✅ Verified |
| TOKH-04 | Implementing | ⚠️ Verified behavior; one surviving mutant (Fix 1) |

---

## Summary

**Overall**: ❌ Not Ready. One test-only fix is needed.

**Spec-anchored check**: 10/10 ACs and 2/2 edge cases match the spec's outcomes. No spec-precision gaps.
**Sensor**: 33/34 effective mutants killed. W12 survived.
**Gate**: `make check` EXIT 0 (Go 31/31 packages, web 325/325, integrations 53/53); `make e2e` 10/10.

**What works**:
- The files endpoint: ordering, cursor, both caps, kinds, 404/422, room isolation.
- Token hygiene: the default list, purge (409/404/audit/401), and the 30-day retention wired into the daily job.
- The Files tab: filter, live append with dedupe and filtering, catch-up refetch, unavailable rows, open in timeline across pages.
- The tokens page: toggle remembered, purge through a confirmation, failure handling.

**Issues found**: W12 (Fix 1).

**Next steps**: an implementer adds the Fix 1 assertion and re-applies W12 to show it fails; then a re-verify.
