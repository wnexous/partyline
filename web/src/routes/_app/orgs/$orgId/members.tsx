import { createFileRoute } from "@tanstack/react-router";
import { OrgMembersPage } from "@/pages/orgs";

export const Route = createFileRoute("/_app/orgs/$orgId/members")({
  component: OrgMembersPage,
});
