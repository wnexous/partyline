import { createFileRoute } from "@tanstack/react-router";
import { OrgPositionsPage } from "@/pages/orgs";

export const Route = createFileRoute("/_app/orgs/$orgId/positions")({
  component: OrgPositionsPage,
});
