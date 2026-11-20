import { createFileRoute, redirect } from "@tanstack/react-router";

export const Route = createFileRoute("/_app/orgs/$orgId/")({
  // The first page of an organization is its members.
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/orgs/$orgId/members", params, replace: true });
  },
});
