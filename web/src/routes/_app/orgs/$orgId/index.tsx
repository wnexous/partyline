import { createFileRoute, redirect } from "@tanstack/react-router";

export const Route = createFileRoute("/_app/orgs/$orgId/")({
  // The first page of an organization (T41: its settings; the chart comes later).
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/orgs/$orgId/settings", params, replace: true });
  },
});
