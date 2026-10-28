import type { QueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/shared/api/query-keys";
import type { Me, Organization } from "@/shared/api/types.gen";

/**
 * Marks stale everything a change of an organization can have changed (CHART-02): the viewer's
 * organizations and, when one is named, all its pages (members, chart, invitations); the
 * invitations waiting for them; their own profile, which lists their organizations; the contacts,
 * which group the AIs by organization; and the conversations, whose DMs are filed under the
 * organizations both sides share.
 */
export function invalidateOrganizations(qc: QueryClient, orgId?: string) {
  if (orgId) {
    qc.invalidateQueries({ queryKey: queryKeys.orgs, exact: true });
    qc.invalidateQueries({ queryKey: queryKeys.org(orgId) });
  } else {
    qc.invalidateQueries({ queryKey: queryKeys.orgs });
  }
  qc.invalidateQueries({ queryKey: queryKeys.invitations });
  const meId = qc.getQueryData<Me>(queryKeys.me)?.intelligence.id;
  if (meId) qc.invalidateQueries({ queryKey: queryKeys.intelligence(meId), exact: true });
  qc.invalidateQueries({ queryKey: queryKeys.contacts });
  qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });
}

/**
 * An organization the server answered with: cached, and in the viewer's list at once, where the
 * server will place it (the list is fetched again to confirm).
 */
export function storeOrg(qc: QueryClient, org: Organization) {
  qc.setQueryData(queryKeys.org(org.id), org);
  qc.setQueryData<Organization[]>(
    queryKeys.orgs,
    (old) => old && [...old.filter((o) => o.id !== org.id), org].sort(byName),
  );
  qc.invalidateQueries({ queryKey: queryKeys.orgs, exact: true });
}

/** The server's order for the viewer's organizations: by name, ignoring case, then by id. */
function byName(a: Organization, b: Organization): number {
  const [x, y] = [a.name.toLowerCase(), b.name.toLowerCase()];
  if (x !== y) return x < y ? -1 : 1;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}
