import { createFileRoute, redirect } from "@tanstack/react-router";

// The rooms list is the sidebar's conversation list now (LAY-01): old links land on the home page.
export const Route = createFileRoute("/_app/rooms/")({
  beforeLoad: () => {
    throw redirect({ to: "/", replace: true });
  },
});
