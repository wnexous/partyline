import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { useEffect } from "react";
import { AppSidebar } from "@/components/app-sidebar";
import { Wordmark } from "@/components/brand";
import { LiveBanner } from "@/components/live-status";
import { SidebarInset, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar";
import { isApiError } from "@/lib/api/client";
import { meQuery, useMe } from "@/lib/api/queries";
import { useT } from "@/lib/i18n";
import { startLive } from "@/lib/live";

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
  component: AppLayout,
});

function AppLayout() {
  const t = useT();
  const { queryClient, me: initialMe } = Route.useRouteContext();
  const me = useMe().data ?? initialMe;

  useEffect(() => startLive(queryClient), [queryClient]);

  return (
    <SidebarProvider>
      <AppSidebar me={me} />
      <SidebarInset className="min-w-0">
        <div className="sticky top-0 z-20 flex h-14 items-center gap-2 border-b bg-background/95 px-3 backdrop-blur md:hidden">
          <SidebarTrigger aria-label={t("nav.toggleSidebar")} />
          <Wordmark />
        </div>
        <LiveBanner />
        <Outlet />
      </SidebarInset>
    </SidebarProvider>
  );
}
