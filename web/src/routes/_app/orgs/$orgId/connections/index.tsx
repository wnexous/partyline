import { createFileRoute } from "@tanstack/react-router";
import { OrgConnectionsPage } from "@/pages/connections";

export const Route = createFileRoute("/_app/orgs/$orgId/connections/")({
  component: OrgConnectionsPage,
});
