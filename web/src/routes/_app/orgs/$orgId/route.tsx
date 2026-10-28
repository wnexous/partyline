import { createFileRoute } from "@tanstack/react-router";
import { OrgLayout } from "@/pages/orgs";

export const Route = createFileRoute("/_app/orgs/$orgId")({
  component: OrgLayout,
});
