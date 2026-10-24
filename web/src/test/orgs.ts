import type { ProfileMembership } from "@/lib/api/types.gen";
import { json } from "./fetch";
import { makeMembership, makeProfile } from "./fixtures";

/*
 * The viewer's organizations in the tests of the pages that are about them. The names and ids are
 * the ones sample-contacts.ts uses for its organization groups: Acme (o1), Globex (o2), Initech (o3).
 */

const ORGS = {
  o1: { org_id: "o1", org_name: "Acme", org_slug: "acme" },
  o2: { org_id: "o2", org_name: "Globex", org_slug: "globex" },
  o3: { org_id: "o3", org_name: "Initech", org_slug: "initech" },
} as const;

/** The viewer's membership in Acme (o1), Globex (o2) or Initech (o3) with the permission given. */
export function memberIn(
  org: keyof typeof ORGS,
  permission: "admin" | "member" | "guest",
  over: Partial<ProfileMembership> = {},
): ProfileMembership {
  return makeMembership({ ...ORGS[org], permission, ...over });
}

/**
 * The route that serves the viewer's own profile with these memberships, which is where the app
 * reads the organizations they belong to (useMyOrgs). Spread it into a `mockApi` table.
 */
export function viewerIn(...memberships: ProfileMembership[]) {
  return { "GET /intelligences/u1": json(200, makeProfile({ memberships })) };
}
