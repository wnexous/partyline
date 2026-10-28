import type {
  Conversation,
  IntelligenceRef,
  Me,
  Organization,
  OrgMember,
} from "@/shared/api/types.gen";
import type { MessageKey, Translate } from "@/shared/i18n";

/*
 * The viewer's organizations as the web app uses them (ORG-02, OMEM-03, OMEM-04): which one is
 * current, who may create what in it, and which conversations belong under it. Pure functions;
 * hooks/use-current-org.ts reads the data and keeps the choice.
 */

/** The longest an organization's name may be (ORG-01) and a position's (POS-01), in characters. */
export const MaxOrgNameLength = 100;
export const MaxPositionNameLength = 60;
/** The longest an organization's address (slug) may be, and its shape: groups of a-z0-9 joined by single hyphens. */
export const MaxOrgSlugLength = 40;
const ORG_SLUG = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

/** The address as the server reads it: no spaces around it, lower case. */
export const normalizeSlug = (text: string) => text.trim().toLowerCase();

/** Whether a normalized address is one the server accepts (1 to 40 characters). */
export const isOrgSlug = (slug: string) => slug.length <= MaxOrgSlugLength && ORG_SLUG.test(slug);

/** The three permissions of a membership (OMEM-04). */
export const PermissionAdmin = "admin";
export const PermissionMember = "member";
export const PermissionGuest = "guest";

const PERMISSION_LABEL: Record<string, MessageKey> = {
  [PermissionAdmin]: "profile.permissionAdmin",
  [PermissionMember]: "profile.permissionMember",
  [PermissionGuest]: "profile.permissionGuest",
};

/** What a permission is called (Admin, Member, Guest); one the app does not know is shown as it came. */
export function permissionLabel(t: Translate, permission: string): string {
  const key = PERMISSION_LABEL[permission];
  return key ? t(key) : permission;
}

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
  /** The picture the organization's admins gave it; initials show without one. */
  avatarUrl?: string;
  /** The position the viewer holds there; absent for none. */
  positionId?: string;
}

/** The viewer's organizations from `GET /orgs`, in the server's order (by name). */
export function toMyOrgs(orgs: readonly Organization[]): MyOrg[] {
  return orgs.map((o) => ({
    id: o.id,
    name: o.name,
    slug: o.slug,
    permission: o.permission,
    ...(o.avatar_url ? { avatarUrl: o.avatar_url } : {}),
    ...(o.position_id ? { positionId: o.position_id } : {}),
  }));
}

/** Admins look after the organization: its connections among other things (ORG-03, OMEM-04). */
export const isAdminOf = (org: { permission: string }) => org.permission === PermissionAdmin;

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

/**
 * `GET /me`. The generated `Me` carries `default_org_id` (absent when the account belongs to no
 * organization); a `null` from an older server is read as none too.
 */
export type MeWithDefaultOrg = Omit<Me, "default_org_id"> & { default_org_id?: string | null };

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

/**
 * The unread messages each organization holds (the switcher's counts): the sum of the unread of
 * the conversations it holds, so a DM both sides share with two organizations counts in both.
 * Every organization of `orgs` has an entry, 0 when nothing waits in it.
 */
export function unreadByOrg(
  conversations: readonly Pick<Conversation, "unread" | "org_ids">[],
  orgs: readonly Pick<MyOrg, "id">[],
): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const org of orgs) {
    counts[org.id] = conversations
      .filter((c) => inOrg(c, org.id))
      .reduce((sum, c) => sum + c.unread, 0);
  }
  return counts;
}

/**
 * The unread messages the current organization's list does not show: those of the conversations
 * it does not hold, each counted once however many other organizations hold it. The switcher's
 * button carries it, so a message waiting elsewhere is not missed.
 */
export function unreadOutsideOrg(
  conversations: readonly Pick<Conversation, "unread" | "org_ids">[],
  orgId: string,
): number {
  return conversations.filter((c) => !inOrg(c, orgId)).reduce((sum, c) => sum + c.unread, 0);
}

/**
 * The users among an organization's members, by name and without the viewer: who a group of the
 * organization can take in besides its AIs (ORG-03). A member list holds users and AIs only.
 */
export function userMembers(members: readonly OrgMember[], viewerId?: string): IntelligenceRef[] {
  return members
    .map((m) => m.intelligence)
    .filter((who) => who.role === "user" && who.id !== viewerId)
    .sort((a, b) => {
      const [x, y] = [a.display_name.toLowerCase(), b.display_name.toLowerCase()];
      if (x !== y) return x < y ? -1 : 1;
      return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
    });
}

/**
 * What an organization still owns, in words, from the `details` of a 409 `organization_not_empty`
 * (ORG-06): "2 AIs, 1 connection, and 3 groups", leaving out what it has none of. Nothing when the
 * server named no counts.
 */
export function ownedThings(
  t: Translate,
  locale: string,
  details: Record<string, string> | undefined,
): string | null {
  const count = (key: string) => Math.max(0, Number.parseInt(details?.[key] ?? "", 10) || 0);
  const parts: string[] = [];
  const add = (n: number, one: MessageKey, many: MessageKey) => {
    if (n > 0) parts.push(t(n === 1 ? one : many, { count: n }));
  };
  add(count("agents"), "orgs.ownsAgentsOne", "orgs.ownsAgentsMany");
  add(count("connectors"), "orgs.ownsConnectorsOne", "orgs.ownsConnectorsMany");
  add(count("groups"), "orgs.ownsGroupsOne", "orgs.ownsGroupsMany");
  if (parts.length === 0) return null;
  return new Intl.ListFormat(locale, { style: "long", type: "conjunction" }).format(parts);
}
