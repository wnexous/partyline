import { createFileRoute, redirect } from "@tanstack/react-router";

// The agents page became the contacts page (T47): old links and bookmarks land there.
export const Route = createFileRoute("/_app/agents")({
  beforeLoad: () => {
    throw redirect({ to: "/contacts", replace: true });
  },
});
