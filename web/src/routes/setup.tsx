import { createFileRoute, redirect } from "@tanstack/react-router";
import { setupStatusQuery } from "@/entities/account";
import { SetupPage } from "@/pages/setup";

export const Route = createFileRoute("/setup")({
  // Setup exists only while the instance has no users (UI-01).
  beforeLoad: async ({ context }) => {
    const status = await context.queryClient.fetchQuery(setupStatusQuery());
    if (!status.needs_setup) throw redirect({ to: "/login", search: { redirect: undefined } });
  },
  component: SetupPage,
});
