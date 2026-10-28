import { createFileRoute } from "@tanstack/react-router";
import { OrgSettingsPage } from "@/pages/orgs";

export const Route = createFileRoute("/_app/orgs/$orgId/settings")({
  component: OrgSettingsPage,
});
