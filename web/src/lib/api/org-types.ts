import type { IntelligenceRef } from "./types.gen";

/*
 * The organization API as Phase 8 reads it (T12-T17 of .specs/features/organizations).
 *
 * INTERIM. Phase 3 serves these routes and generates their DTOs into types.gen.ts; this branch is
 * built before that merge, so the types are written here with the names and shapes tygo gives them:
 *   - T12/T13, exactly as committed on wnexous/pl-org-p3: Permission, Organization, Position,
 *     OrgMember (its base fields) and the create/update requests;
 *   - T14-T16, from the design (members flags, invitations, the chart) and marked ASSUMED.
 * After the merge of wnexous/pl-org-g0 with Phase 3, delete this file, import the same names from
 * "./types.gen" and adapt `lib/org-chart.ts`, the one place that reads the chart's shape.
 */

/** What a membership allows in an organization itself, apart from the position held (OMEM-04). */
export type Permission = "admin" | "member" | "guest";

/** An organization as one of its members sees it, with the viewer's own permission and position. */
export interface Organization {
  id: string;
  name: string;
  /** What `--org` takes. */
  slug: string;
  /** /api/v1/orgs/{id}/avatar?v=<sha>; absent without an avatar. */
  avatar_url?: string;
  permission: Permission;
  /** The viewer's position; absent for none. */
  position_id?: string;
  created_at: string;
  updated_at: string;
}

/** POST /orgs: the creator becomes its admin, holding the root position (ORG-01). */
export interface CreateOrganizationRequest {
  name: string;
}

/** PATCH /orgs/{org}: absent fields are unchanged. */
export interface UpdateOrganizationRequest {
  name?: string;
  slug?: string;
}

/** One node of an organization's chart (POS-01); no parent for a root. */
export interface Position {
  id: string;
  name: string;
  parent_id?: string;
}

export interface CreatePositionRequest {
  name: string;
  parent_id?: string;
}

/** PATCH /orgs/{org}/positions/{id}: an empty parent makes the position a root. */
export interface UpdatePositionRequest {
  name?: string;
  parent_id?: string;
}

/** One membership of an organization (OMEM-01, OMEM-03). */
export interface OrgMember {
  intelligence: IntelligenceRef;
  permission: Permission;
  /** Absent when the member holds no position. */
  position_id?: string;
  position_name?: string;
  /** The identity that invited a guest; absent for other permissions. */
  invited_by?: IntelligenceRef;
  joined_at: string;
}

/**
 * PATCH /orgs/{org}/members/{iid} (T14, ASSUMED): absent fields are unchanged. An empty
 * `position_id` takes the member out of its position, which only an admin may do.
 */
export interface UpdateOrgMemberRequest {
  permission?: Permission;
  position_id?: string;
}

/** A pending invitation (T15, ASSUMED): who is invited, with what, by whom and until when. */
export interface Invitation {
  id: string;
  org_id: string;
  org_name: string;
  org_slug: string;
  invitee: IntelligenceRef;
  permission: Permission;
  position_id?: string;
  position_name?: string;
  invited_by: IntelligenceRef;
  created_at: string;
  /** Fourteen days after it was made (OMEM-07). */
  expires_at: string;
}

/** POST /orgs/{org}/invitations (T15, ASSUMED): the invitee by @handle; others than admins send `guest`. */
export interface CreateInvitationRequest {
  handle: string;
  permission: Permission;
  position_id?: string;
}

/** What the viewer may do about one member of the chart, decided by the server (CHART-03, T16, ASSUMED). */
export interface ChartMember {
  intelligence: IntelligenceRef;
  permission: Permission;
  online: boolean;
  /**
   * For a member with no position, the member it hangs under: a guest's inviter, else its creator
   * when that is a member. Absent when it hangs under nobody (CHART-01 "Unassigned").
   */
  under?: string;
  /** Delete (POS-05) is allowed to the viewer. */
  can_delete: boolean;
  /** What deleting does here (POS-06): "delete" for an AI of this organization, "remove" otherwise. */
  delete_effect: string;
  /** The viewer may move it between positions (POS-04). */
  can_move: boolean;
}

/** A position with the members that hold it. */
export interface ChartPosition extends Position {
  holders: ChartMember[];
}

/** GET /orgs/{org}/chart (T16, ASSUMED). */
export interface OrgChart {
  viewer: { admin: boolean };
  /** Every position, in any order: `parent_id` links them. */
  positions: ChartPosition[];
  /** Members with no position that hang under another member (`under`). */
  members?: ChartMember[];
  /** Members with no position that hang under nobody. */
  unassigned?: ChartMember[];
}

/** The counts `organization_not_empty` carries in `details` (ORG-06), as strings. */
export interface NotEmptyDetails {
  agents?: string;
  connectors?: string;
  groups?: string;
}

/** Codes of Phase 3's errors that types.gen.ts does not know yet (`ApiError.code` is typed by it). */
export const ErrorCodeOrganizationNotEmpty = "organization_not_empty";
export const ErrorCodePositionCycle = "position_cycle";
export const ErrorCodeAlreadyMember = "already_member";
