# Room Files and Token Hygiene Specification

## Problem Statement

Rooms collect images, audio and documents from chats and agents, but the only way to find a file is to scroll the timeline. Revoked API tokens also stay listed forever, cluttering the tokens page. Both were reported in first use (2026-10-20).

## Goals

- [ ] Any file shared in a room is findable in under 10 seconds from the room page.
- [ ] The tokens page shows only usable tokens by default; revoked ones can be shown, deleted, and disappear on their own after 30 days.

## Out of Scope

| Feature | Reason |
| --- | --- |
| Full-text search inside files | Needs indexing/OCR; not requested |
| Cross-room file browser | Room-level panel covers the request |
| Bulk download as zip | Not requested |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| Where the panel lives | A "Files" tab next to "Members" in the room side panel (sheet on mobile) | Same place users look for room context | n |
| File kinds for filtering | images, audio, video, documents (everything else) | Matches attachment MIME families | n |
| Page size | 50 files per page, newest first, cursor by attachment id | Consistent with other lists | n |
| Revoked token retention | Purged 30 days after revocation by the daily retention job | Keeps the audit trail (audit log keeps the record) | n |
| Revoked list default | Hidden; "Show revoked" toggle remembered per browser | Less clutter | n |

**Open questions:** none - all resolved or logged above.

---

## User Stories

### P1: Room files panel ⭐ MVP

**User Story**: As a room member, I want to see every file shared in the room so that I find documents and media quickly.

**Why P1**: Requested.

**Acceptance Criteria**:

1. WHEN `GET /api/v1/rooms/{id}/attachments?kind=&before=&limit=` is called by a user with `view` THEN the system SHALL return attachments of that room newest first (default 50, max 100) with id, name, mime, size, status, created_at, message id, author name and kind (`image|audio|video|document`). (FILES-01)
2. IF the caller lacks `view` THEN the system SHALL respond 404. (FILES-02)
3. WHEN `kind` is given THEN the system SHALL return only attachments of that kind; an unknown kind SHALL get 422. (FILES-03)
4. WHEN the user opens the Files tab THEN the app SHALL list the files with thumbnail (images), icon (others), name, size, author and time, filterable by kind, with "open in timeline" jumping to the message. (FILES-04)
5. WHEN a new message with attachments arrives live THEN the Files tab SHALL show the new files without reload. (FILES-05)
6. WHERE an attachment has status `too_large` or `unavailable` the app SHALL show it as unavailable without a download link. (FILES-06)

**Independent Test**: Post messages with an image and a PDF; the Files tab lists both, filter "images" shows one.

---

### P1: Revoked tokens hygiene ⭐ MVP

**User Story**: As a user, I want revoked tokens hidden, deletable and purged automatically so that the tokens page stays clean.

**Why P1**: Requested.

**Acceptance Criteria**:

1. WHEN `GET /api/v1/tokens` is called without `include_revoked=true` THEN the system SHALL omit revoked tokens. (TOKH-01)
2. WHEN `DELETE /api/v1/tokens/{id}/purge` is called on the caller's revoked token THEN the system SHALL delete it and record an audit entry; on an active token it SHALL respond 409. (TOKH-02)
3. WHEN the daily retention job runs THEN the system SHALL delete tokens revoked more than 30 days earlier. (TOKH-03)
4. The tokens page SHALL hide revoked tokens by default, offer a "Show revoked" toggle and a "Delete" action on revoked rows with a confirmation. (TOKH-04)

**Independent Test**: Revoke a token: it disappears from the default list; toggle shows it; delete removes it; a 31-day-old revoked token is gone after the job.

---

## Edge Cases

- WHEN a room has no files THEN the Files tab SHALL show an empty state explaining that shared files appear here (FILES-04).
- IF a token is purged while a client still holds its secret THEN requests with it SHALL get 401 like any unknown token (TOKH-02).

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| FILES-01 | P1: Room files | Tasks | Pending |
| FILES-02 | P1: Room files | Tasks | Pending |
| FILES-03 | P1: Room files | Tasks | Pending |
| FILES-04 | P1: Room files | Tasks | Pending |
| FILES-05 | P1: Room files | Tasks | Pending |
| FILES-06 | P1: Room files | Tasks | Pending |
| TOKH-01 | P1: Token hygiene | Tasks | Pending |
| TOKH-02 | P1: Token hygiene | Tasks | Pending |
| TOKH-03 | P1: Token hygiene | Tasks | Pending |
| TOKH-04 | P1: Token hygiene | Tasks | Pending |

**Coverage:** 10 total, mapped in tasks.md

---

## Success Criteria

- [ ] André finds a document shared in a WhatsApp group from the room's Files tab.
- [ ] The tokens page shows only active tokens by default.
