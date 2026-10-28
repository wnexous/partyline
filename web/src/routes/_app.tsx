import { createFileRoute, redirect } from "@tanstack/react-router";
import { AppShell } from "@/app/layouts/app-shell";
import { meQuery } from "@/entities/account";
import { isApiError } from "@/shared/api/client";

export const Route = createFileRoute("/_app")({
  // Every page under _app needs a session: unknown users go to login and come back (UI-02).
  beforeLoad: async ({ context, location }) => {
    try {
      const me = await context.queryClient.ensureQueryData(meQuery());
      return { me };
    } catch (err) {
      if (isApiError(err) && err.status === 401) {
        throw redirect({ to: "/login", search: { redirect: location.href } });
      }
      throw err;
    }
  },
  component: AppLayoutRoute,
});

function AppLayoutRoute() {
  const { queryClient, me } = Route.useRouteContext();
  return <AppShell queryClient={queryClient} initialMe={me} />;
}
