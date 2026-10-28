import { createFileRoute } from "@tanstack/react-router";
import { OrgChartPage } from "@/pages/orgs";

export const Route = createFileRoute("/_app/orgs/$orgId/chart")({
  component: OrgChartPage,
});
