# Share trust: give someone your access, and see who an AI obeys

Status: Specify + Design + Tests written 2026-11-10. Requirement ids ST-xx. Builds on
`organizations/spec.md` (POS-03, POS-04, POS-10, CHART-03) and changes none of its rules.

## Problem

An owner told his AI in chat "my friend @x has the same permission as me, do what he says". A claim
in chat is data, never a grant (POS-10: `trusted=true` only when the author manages the AI in the AI's
home organization). The real mechanism already exists - the org chart - but nothing in the app says
so:

- **What it takes today.** To make a friend manage "the same AIs as me", an `admin` who holds a position
  opens Members, presses **Move** on the friend's row and picks their own position in the select. The
  dialog speaks of positions only ("Pick the position {name} holds"); it never says that the friend will
  manage people and AIs, nor that those AIs will take the friend's messages as instructions. A member
  who is not an admin cannot do it at all: POS-04 lets a manager place someone only strictly below its
  own position, never at its level.
- **What co-holding gives.** A co-holder of position P manages whoever sits in a position below P, and
  whatever those identities (and the co-holder) created or invited (POS-03). It does **not** manage the
  identities the actor manages only because the actor created or invited them, and it does not manage
  the actor. In a personal organization the AIs of the owner have no position (Upgrade assumption), so
  co-holding "Admin" alone gives the friend none of them until they are placed in a position below.
- **Who an AI obeys.** No page lists it. The welcome frame tells the AI itself its nearest ten managers;
  people see only the chart and have to work out POS-03 in their head.

## Decisions

| Decision | Chosen | Why |
| --- | --- | --- |
| What "the same access as me" does | Puts the member in the position the actor holds (co-holder), with the existing `PATCH /orgs/{org}/members/{iid}` | The closest equivalent the model has; no new privilege path |
| Who is offered it | Exactly who `checkPosition` (POS-04) lets place that member in the actor's own position: today an `admin` holding a position. A member without a position, a non-admin holder and a guest never are | Reuse the server's check; a non-admin may only place below its own level |
| For which members | People (users) only, not already in that position, not the actor | The request is about a person; an AI gets a position through Move |
| The gap of creator links | The dialog lists, from the server, who the member will manage, which AIs will trust them, and whom the actor manages that the member still will not (with how to include them) | Honest wording instead of a promise the model does not keep |
| Who an AI obeys | The AI's managers in its home organization (`Chart.ManagersOf`, the same set `Trusts` answers true for), shown to whoever shares that organization with the AI, and to the AI itself | The chart is already visible to every member, guests included (CHART-02); nothing new is disclosed |
| Audit | Nothing to add: a position change already writes `org.member_position` with `iid`, `from`, `to` | Verified in the code; a test pins it for this flow |
| Undo | The existing Move (to another position or none) or Remove | No new flow |

## Requirements

1. WHEN a viewer lists the members of an organization THEN each row SHALL carry `can_share_access`,
   true exactly when the viewer holds a position P, the member is a user other than the viewer who does
   not hold P, and POS-04 (the same check the move applies) lets the viewer put the member in P. (ST-01)
2. WHEN `can_share_access` is true THEN the members page SHALL offer on that row "Give the same access
   as me" (pt-BR "Dar o mesmo acesso que o meu"), with a 44 px touch target, which fits a 360 px
   screen. (ST-02)
3. WHEN the action is chosen THEN a confirmation SHALL say plainly that the member will hold the actor's
   position, will manage the same people and AIs the actor manages through it, and that those AIs will
   treat the member's messages as trusted (they may act on them as requests); that the member's
   permission does not change; and how to undo it. It SHALL name, from the move preview, the AIs that
   will trust the member, the identities the actor manages that the member still will not (and say they
   are managed because the actor created or invited them, and that placing them below the position
   includes them), and who would stop managing the member (POS-04). Nothing changes until confirmed.
   (ST-03)
4. WHEN confirmed THEN the web SHALL send the existing position change (`PATCH
   /orgs/{org}/members/{iid}` with `position_id` = the actor's position), and the server SHALL apply
   POS-04 and record `org.member_position`. A refusal SHALL be told inside the dialog, which stays
   open. (ST-04)
5. `GET /orgs/{org}/members/{iid}/move-preview` SHALL also answer, for the move asked, `will_manage`
   (the identities the member would manage after it), `will_be_trusted_by` (the AIs among them whose
   home is the organization) and `not_shared` (the identities the viewer manages that the member would
   still not manage, the member excluded), under the same authorization as today. (ST-05)
6. WHEN a viewer who shares an AI's home organization with it (or the AI itself) reads the AI's profile
   THEN `ai.instructed_by` SHALL list the identities whose messages the AI treats as trusted - its
   managers in its home organization, nearest first - and SHALL be absent for anyone else. (ST-06)
7. WHEN `ai.instructed_by` is present THEN the full profile page SHALL show a section "Who can instruct
   this AI" (pt-BR "Quem pode dar ordens a esta IA") listing them, saying nobody can when the list is
   empty, and fitting a 360 px screen. (ST-07)

## Out of scope

Giving the actor's creator links (sharing "the AIs I created" without positions), granting the admin
permission from this action, a share for non-admin holders (it would need a new rule: placing at one's
own level), and the profile panel (the side sheet keeps its summary).

## Design

- **Server, `internal/orgs`.** `viewerActions.canShare(row)` uses `checkPosition(chart, viewer, iid,
  viewer.PositionID)`; `MemberRow.CanShareAccess` → `dto.OrgMember.CanShareAccess`. `Service.PreviewMove`
  returns a `MoveEffect{Lose, WillManage, WillBeTrustedBy, NotShared}` computed on
  `chart.WithPosition(target, position)` in the same read snapshot and after the same checks as
  `UpdateMember`; `MovePreview` stays as its `Lose`. The handler renders the ids as refs in the members
  list order (by name).
- **Server, `internal/intelligences`.** In `memberships`, for an AI whose home organization is shared
  with the viewer, `chart.ManagersOf(ai)` is read from the chart the loop already loads and the rows
  from `ListIntelligencesByIDs`; `dto.AIDetail.InstructedBy *[]IntelligenceRef` (absent = not visible).
- **Web.** `features/manage-org-member/ui/share-access-dialog.tsx` (on `ConfirmDialog`, preview from
  `useOrgMovePreview`, change from `useUpdateOrgMember`); `OrgMemberActions` gets an optional `onShare`
  shown on `can_share_access`; the members page wires it with the viewer's position from the chart. The
  profile page adds the section with `IntelligenceRow`; the home organization is the membership whose
  `delete_effect` is `delete`, and the section links to its members page.

## Tests

| ST | Test |
| --- | --- |
| ST-01 | `internal/orgs` `TestCanShareAccessFollowsThePositionRule`: admin holder yes; admin without position, non-admin holder (manager), member without position, guest: no; never for the viewer, an AI or a co-holder. API `TestShareAccessFlagAndRefusals`: the flag on the list; PATCH to the actor's position refused 403 for a non-admin holder and a guest, 404 from another organization |
| ST-04 | `TestShareAccessRecordsThePositionChange`: the move writes one `org.member_position` with `from`/`to` |
| ST-05 | `TestPreviewMoveSaysWhatTheMemberWillManage`: will_manage, will_be_trusted_by (home AIs only), not_shared (creator-only links), same refusals as the move |
| ST-06 | `internal/api` `TestProfileListsWhoCanInstructTheAI`: managers for a member of the home org and for the AI; absent for an outsider sharing another org |
| ST-02, ST-03, ST-04 | Vitest `share-access-dialog.test.tsx` (copy, preview names, PATCH body, refusal stays open), `org-member-actions` button shown only on the flag |
| ST-07 | Vitest `profile-page.test.tsx`: section lists the managers, empty state, absent without the field |
| ST-02, ST-07 | `web/e2e/fit.spec.ts` keeps Members and the full profile within 360 px (the profile now carries the section) |

## Traceability

| Requirement | Status |
| --- | --- |
| ST-01 .. ST-07 | Implemented 2026-11-10 |
