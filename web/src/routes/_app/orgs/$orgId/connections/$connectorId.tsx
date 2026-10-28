import { createFileRoute } from "@tanstack/react-router";
import { ConnectorPage } from "@/pages/connections";

export const Route = createFileRoute("/_app/orgs/$orgId/connections/$connectorId")({
  component: ConnectorPage,
});
