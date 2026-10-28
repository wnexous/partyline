import { createFileRoute, redirect } from "@tanstack/react-router";
import { setupStatusQuery } from "@/entities/account";
import { LoginPage, type LoginSearch } from "@/pages/auth";

export const Route = createFileRoute("/login")({
  validateSearch: (search: Record<string, unknown>): LoginSearch => ({
    redirect: typeof search.redirect === "string" ? search.redirect : undefined,
  }),
  // A fresh instance has no accounts yet: setup comes first (UI-01).
  beforeLoad: async ({ context }) => {
    const status = await context.queryClient.fetchQuery(setupStatusQuery());
    if (status.needs_setup) throw redirect({ to: "/setup" });
  },
  component: LoginPage,
});
