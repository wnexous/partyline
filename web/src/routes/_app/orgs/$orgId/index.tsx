import { createFileRoute, redirect } from "@tanstack/react-router";

export const Route = createFileRoute("/_app/orgs/$orgId/")({
  // The first page of an organization is its chart.
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/orgs/$orgId/chart", params, replace: true });
  },
});
