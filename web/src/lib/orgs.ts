import type { Conversation, Me, ProfileMembership } from "@/lib/api/types.gen";

/*
 * The viewer's organizations as the web app uses them (ORG-02, OMEM-03, OMEM-04): which one is
 * current, who may create what in it, and which conversations belong under it. Pure functions;
 * hooks/use-current-org.ts reads the data and keeps the choice.
 */

/** The three permissions of a membership (OMEM-04). */
export const PermissionAdmin = "admin";
export const PermissionMember = "member";
export const PermissionGuest = "guest";

/** Where this browser remembers the organization the viewer picked. */
export const CURRENT_ORG_KEY = "pl.org";

/** An organization the viewer belongs to, with what they are in it. */
export interface MyOrg {
  id: string;
  name: string;
  /** What `--org` takes. */
  slug: string;
  /** admin, member or guest. */
  permission: string;
  /** The position the viewer holds there; absent for none. */
  position?: string;
}

/** The viewer's organizations from the memberships of their own profile, in the server's order. */
export function toMyOrgs(memberships: readonly ProfileMembership[]): MyOrg[] {
  return memberships.map((m) => ({
    id: m.org_id,
    name: m.org_name,
    slug: m.org_slug,
    permission: m.permission,
    ...(m.position_name ? { position: m.position_name } : {}),
  }));
}

/** Admins look after the organization: its connections among other things (ORG-03, OMEM-04). */
export const isAdminOf = (org: MyOrg) => org.permission === PermissionAdmin;

/** Members and admins create groups; a guest takes part in the ones it is added to (OMEM-04). */
export const canCreateGroupsIn = (org: MyOrg) =>
  org.permission === PermissionAdmin || org.permission === PermissionMember;

/**
 * The organization the app is about right now: the one the viewer picked, else their account's
 * default, else the first they belong to. A pick or default they no longer belong to counts for
 * nothing. None for a viewer who belongs to no organization.
 */
export function resolveCurrentOrg(
  orgs: readonly MyOrg[],
  pickedId: string | null | undefined,
  defaultId: string | null | undefined,
): MyOrg | undefined {
  const named = (id: string | null | undefined) =>
    id ? orgs.find((org) => org.id === id) : undefined;
  return named(pickedId) ?? named(defaultId) ?? orgs[0];
}

/** `GET /me` as a server that knows default organizations sends it (the default-organization route). */
export type MeWithDefaultOrg = Me & { default_org_id?: string | null };

/** The account's default organization; nothing when the server or the account names none. */
export function defaultOrgIdOf(me: MeWithDefaultOrg | undefined): string | undefined {
  return me?.default_org_id || undefined;
}

/**
 * Whether a conversation is filed under the organization (ORG-02): a group under its own, a DM
 * under the organizations both sides belong to. One filed under none is held by every
 * organization, so it can never become unreachable.
 */
export function inOrg(conversation: Pick<Conversation, "org_ids">, orgId: string): boolean {
  return conversation.org_ids.length === 0 || conversation.org_ids.includes(orgId);
}
