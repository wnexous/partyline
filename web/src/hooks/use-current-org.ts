import type { QueryClient } from "@tanstack/react-query";
import { useMemo } from "react";
import { meQuery, orgsQuery, useMe, useMyOrgs } from "@/lib/api/queries";
import { useLiveStore } from "@/lib/live/store";
import { defaultOrgIdOf, type MyOrg, resolveCurrentOrg, toMyOrgs } from "@/lib/orgs";

const NO_ORGS: MyOrg[] = [];

export interface CurrentOrg {
  /** The organization the sidebar, the connections and new groups are about; none until the viewer's are known, or when they belong to none. */
  org: MyOrg | undefined;
  /** Every organization the viewer belongs to, in the server's order. */
  orgs: MyOrg[];
  /** True while the viewer's organizations are being fetched for the first time. */
  loading: boolean;
  /** True when the viewer's organizations could not be read: none, as opposed to not known. */
  failed: boolean;
  /** Picks an organization and remembers the pick in this browser. */
  select: (orgId: string) => void;
}

/**
 * The organization the app is about right now (ORG-02): the one the viewer picked, else their
 * account's default, else the first they belong to. The organization switcher and every page that
 * is scoped to an organization read and change it here.
 */
export function useCurrentOrg(): CurrentOrg {
  const mine = useMyOrgs();
  const me = useMe().data;
  const pickedId = useLiveStore((s) => s.currentOrgId);
  const select = useLiveStore((s) => s.setCurrentOrg);
  const orgs = mine.data ?? NO_ORGS;
  const defaultId = defaultOrgIdOf(me);
  const org = useMemo(
    () => resolveCurrentOrg(orgs, pickedId, defaultId),
    [orgs, pickedId, defaultId],
  );
  return { org, orgs, loading: mine.isLoading, failed: mine.isError && !mine.data, select };
}

/**
 * The same rule outside React (SET-06): the organization the switcher shows, for a route that
 * redirects before it renders. It waits for the viewer and their organizations (`ensureQueryData`)
 * instead of guessing, so an old `/connectors` bookmark never bounces while they load.
 */
export async function currentOrgId(queryClient: QueryClient): Promise<string | undefined> {
  const [me, orgs] = await Promise.all([
    queryClient.ensureQueryData(meQuery()),
    queryClient.ensureQueryData(orgsQuery()),
  ]);
  const pickedId = useLiveStore.getState().currentOrgId;
  return resolveCurrentOrg(toMyOrgs(orgs), pickedId, defaultOrgIdOf(me))?.id;
}
