import { createFileRoute, isRedirect, redirect } from "@tanstack/react-router";
import { connectorQuery } from "@/lib/api/queries";

/*
 * A connection names its organization (SET-07): `/connectors/{id}` reads the connection and sends
 * the viewer to it under the organization that owns it. One that cannot be read — deleted, or of
 * an organization the viewer is not in — lands on the organizations list instead of an error page.
 * `ensureQueryData` leaves the connection in the cache, so the moved page renders it at once.
 */
export const Route = createFileRoute("/_app/connectors/$connectorId")({
  beforeLoad: async ({ context, params }) => {
    try {
      const connector = await context.queryClient.ensureQueryData(
        connectorQuery(params.connectorId),
      );
      throw redirect({
        to: "/orgs/$orgId/connections/$connectorId",
        params: { orgId: connector.org_id, connectorId: connector.id },
        replace: true,
      });
    } catch (err) {
      if (isRedirect(err)) throw err;
      throw redirect({ to: "/orgs", replace: true });
    }
  },
});
