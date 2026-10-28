import { createFileRoute, redirect } from "@tanstack/react-router";
import { currentOrgId } from "@/entities/organization";

/*
 * Connections moved under the organization that owns them (SET-06): an old `/connectors` bookmark
 * lands on the current organization's address, or on the organizations list when the viewer
 * belongs to none. The switcher's rule decides which organization, so the address never disagrees
 * with the one the app shows.
 */
export const Route = createFileRoute("/_app/connectors/")({
  beforeLoad: async ({ context }) => {
    const orgId = await currentOrgId(context.queryClient);
    throw redirect(
      orgId
        ? { to: "/orgs/$orgId/connections", params: { orgId }, replace: true }
        : { to: "/orgs", replace: true },
    );
  },
});
