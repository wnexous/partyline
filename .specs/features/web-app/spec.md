# Web App Specification

## Problem Statement

Users need one place to see and steer everything: who is online, which chats feed which rooms, what agents are saying right now, and who has access. The app must feel live (WebSocket), be fast, and make the safe choice the easy one.

## Goals

- [ ] Every core-platform, agent-channel and connectors capability that a human performs is reachable from the UI.
- [ ] Live events update open screens without reload; a dropped socket shows a visible "reconnecting" state and recovers.
- [ ] Lighthouse accessibility score ≥ 90 on login, dashboard and room pages.

## Out of Scope

| Feature | Reason |
| --- | --- |
| Native mobile apps | Responsive web covers phones |
| Internationalization beyond English and Brazilian Portuguese | Two locales cover the first users |
| Rich text editor | Messages are plain text with Markdown rendering of agent output |
| Theming beyond light/dark | shadcn tokens already allow customization in code |

---

## Assumptions & Open Questions

| Assumption / decision | Chosen default | Rationale | Confirmed? |
| --- | --- | --- | --- |
| Stack | Vite + React + TypeScript, shadcn/ui (Radix), Tailwind v4, TanStack Router (file routes) + Query, zustand for live state, zod + react-hook-form for forms | User asked for shadcn, zod, TanStack, zustand; agent chose | y |
| Package manager | Bun | Already installed; fast installs and Docker stage | n |
| Language default | Browser language (pt-BR or en), switchable | Brazilian users first, public repo | n |
| Markdown rendering | `react-markdown` with no raw HTML | Agents answer in Markdown; raw HTML would be XSS | n |
| Timeline size | Last 50 messages, older on scroll (50 per page) | Smooth with long rooms | n |
| Connector forms | Rendered from the API field schema (AD-007) | No per-connector UI code | y |

**Open questions:** none - all resolved or logged above.

---

## User Stories

### P1: Access flows ⭐ MVP

**User Story**: As a person, I want setup, login and invite pages so that I can get in.

**Why P1**: Gate to everything.

**Acceptance Criteria**:

1. WHEN the instance has no users THEN the app SHALL show the setup page asking for the setup token, username and password. (UI-01)
2. IF the user is not logged in THEN the app SHALL redirect protected routes to `/login` and return to the original route after login. (UI-02)
3. WHEN an invite link is opened THEN the app SHALL show the accept form, validated with the same rules as the API (username 3-32 of `a-z 0-9 _ -`, password ≥ 10). (UI-03)

**Independent Test**: Fresh instance in Playwright: setup → logout → login → protected route.

---

### P1: Live rooms ⭐ MVP

**User Story**: As a user, I want to watch and talk in rooms live so that I follow agents and chats as they work.

**Why P1**: Main screen.

**Acceptance Criteria**:

1. WHEN a `room.message` event arrives for the open room THEN the app SHALL append it to the timeline without refetching. (UI-04)
2. The app SHALL show for each message the author's avatar and name (without any mark of whether the author is an AI or a person, AD-023), time, a trusted/untrusted badge for chat senders, reply context and attachments. (UI-05)
3. WHILE the live socket is disconnected the app SHALL show a "reconnecting" indicator and, after reconnecting, SHALL refetch the open room's messages after the last seen sequence. (UI-06)
4. WHERE the user has `post` the app SHALL show the composer; otherwise it SHALL show a read-only notice. (UI-07)
5. WHEN a room is paused by the loop guard THEN the app SHALL show a banner with a "resume agents" button for managers. (UI-08)
6. The app SHALL render agent message text as Markdown without raw HTML. (UI-09)
7. WHEN a conversation opens THEN its timeline SHALL show the newest message, whichever conversation or page it came from and whatever the one before it was scrolled to, and the older pages loaded as the reader scrolls up SHALL keep what is being read in place. (UI-26)

**Independent Test**: Two tabs, post in one, appears in the other in under 1 second.

---

### P1: Rooms management and sharing ⭐ MVP

**User Story**: As a room manager, I want to add members and set visibility so that I control routing and access.

**Why P1**: Routing is configured here.

**Acceptance Criteria**:

1. WHEN adding members THEN the app SHALL list only agents and chats the user may use, with agents' online state, filterable by text. (UI-10)
2. WHEN changing visibility THEN the app SHALL offer private, shared (pick users and level) and public (view or post) and show who can access afterwards. (UI-11)
3. WHEN a chat member is shown THEN the app SHALL allow toggling its `outbound` flag. (UI-12)
4. The app SHALL provide "Route chat to agent" from a chat row, creating a private room with both as members in one action. (UI-13)

**Independent Test**: Create room, add agent and chat, toggle outbound, share with second user who sees it.

---

### P1: Agents, connectors, identities and tokens ⭐ MVP

**User Story**: As a user, I want to manage my agents, connectors, verified identities and tokens so that sessions and platforms connect.

**Why P1**: Onboarding path.

**Acceptance Criteria**:

1. WHEN the user opens "Connect an agent" THEN the app SHALL create an agent token and show a ready-to-copy `.mcp.json` snippet containing the instance URL and the token, and the launch command. (UI-14)
2. The app SHALL show each agent with live online/offline state and last-seen time. (UI-15)
3. WHEN adding a connector THEN the app SHALL render the form from the kind's field schema with secret fields as password inputs. (UI-16)
4. The app SHALL show each connector's live status and last error. (UI-17)
5. WHEN the user starts identity linking THEN the app SHALL show the pairing code, the command to send and a countdown, and SHALL update when the `identity.linked` event arrives. (UI-18)
6. WHEN a token is created THEN the app SHALL show the secret once with a copy button and a warning that it will not be shown again. (UI-19)

**Independent Test**: Connect wizard produces a snippet that a real `partyline channel` accepts.

---

### P1: Admin ⭐ MVP

**User Story**: As an admin, I want to manage users and invites so that colleagues can join.

**Why P1**: Multi-user requirement.

**Acceptance Criteria**:

1. WHERE the user is admin the app SHALL show the Admin section with users (enable/disable, role) and invites (create with role, copy link, revoke). (UI-20)
2. IF a non-admin opens an admin route THEN the app SHALL show a 403 page. (UI-21)

**Independent Test**: Admin invites a user, disables them, user is logged out on next request.

---

### P2: Polish

**User Story**: As a frequent user, I want shortcuts and theming so that the app is pleasant every day.

**Why P2**: Quality, not function.

**Acceptance Criteria**:

1. WHEN the user presses Ctrl/Cmd+K THEN the app SHALL open a command palette to jump to rooms, agents and connectors. (UI-22)
2. The app SHALL follow the system color scheme and allow overriding it to light or dark, remembered per browser. (UI-23)
3. The app SHALL render usable layouts from 360 px wide upward with no horizontal page scroll. (UI-24)
4. The app SHALL provide English and Brazilian Portuguese strings for every screen. (UI-25)

**Independent Test**: Playwright at 360 px and 1280 px, both themes, both locales.

---

## Edge Cases

- IF the API returns 401 during use THEN the app SHALL redirect to login and keep the current route for return (UI-02).
- IF a live event references a room not in cache THEN the app SHALL ignore it unless that room is open (UI-04).
- WHEN a room has zero messages THEN the app SHALL show an empty state explaining how to add members (UI-05).

---

## Requirement Traceability

| Requirement ID | Story | Phase | Status |
| --- | --- | --- | --- |
| UI-01 | P1: Access flows | Tasks | ✅ Verified |
| UI-02 | P1: Access flows | Tasks | ✅ Verified; T35 (live 1008 → login) to re-verify |
| UI-03 | P1: Access flows | Tasks | ✅ Verified |
| UI-04 | P1: Live rooms | Tasks | ✅ Verified; T34 (activity feed) to re-verify |
| UI-05 | P1: Live rooms | Tasks | ✅ Verified |
| UI-06 | P1: Live rooms | Tasks | ✅ Verified; T35 (close codes) to re-verify |
| UI-07 | P1: Live rooms | Tasks | ✅ Verified; T37 (listen-only recipients) to re-verify |
| UI-08 | P1: Live rooms | Tasks | ✅ Verified |
| UI-09 | P1: Live rooms | Tasks | ✅ Verified |
| UI-10 | P1: Rooms management | Tasks | ✅ Verified; T33 (member count) to re-verify |
| UI-11 | P1: Rooms management | Tasks | ✅ Verified |
| UI-12 | P1: Rooms management | Tasks | ✅ Verified; T37 (listen-only recipients) to re-verify |
| UI-13 | P1: Rooms management | Tasks | ✅ Verified |
| UI-14 | P1: Agents etc. | Tasks | ✅ Verified |
| UI-15 | P1: Agents etc. | Tasks | ✅ Verified |
| UI-16 | P1: Agents etc. | Tasks | ✅ Verified; T36 (generated secrets, inbound URL), T39 (generated fields may stay empty) to re-verify |
| UI-17 | P1: Agents etc. | Tasks | ✅ Verified |
| UI-18 | P1: Agents etc. | Tasks | ✅ Verified |
| UI-19 | P1: Agents etc. | Tasks | ✅ Verified; T36, T39 (generated secrets) to re-verify |
| UI-20 | P1: Admin | Tasks | ✅ Verified |
| UI-21 | P1: Admin | Tasks | ✅ Verified |
| UI-22 | P2: Polish | Tasks | ✅ Verified |
| UI-23 | P2: Polish | Tasks | ✅ Verified (T20) |
| UI-24 | P2: Polish | Tasks | ✅ Verified (T22, T31) |
| UI-25 | P2: Polish | Tasks | ✅ Verified |
| UI-26 | P1: Live rooms | Tasks | Implemented with unit and Playwright tests; not yet verified independently |

**Coverage:** 26 total, 25 mapped in tasks.md (Phase 6 follow-ups T33-T37 touch UI-02, 04, 06, 07, 10, 12, 16, 19); UI-26 came with a bug fix and is covered by `timeline-scroll.test.tsx` and the timeline scenarios of `smoke.spec.ts`

---

## Success Criteria

- [ ] Playwright smoke suite passes against the Docker image.
- [ ] New user connects an agent and routes a chat without reading docs.
