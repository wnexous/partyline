import { createFileRoute, redirect } from "@tanstack/react-router";

// The account page has its own address (SET-02): old links and bookmarks land on it, and the
// replace keeps one history entry so Back never replays the redirect.
export const Route = createFileRoute("/_app/settings")({
  beforeLoad: () => {
    throw redirect({ to: "/account", replace: true });
  },
});
