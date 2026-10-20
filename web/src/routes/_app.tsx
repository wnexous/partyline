import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { useEffect } from "react";
import { AppSidebar } from "@/components/app-sidebar";
import { Wordmark } from "@/components/brand";
import { LiveBanner } from "@/components/live-status";
import { SidebarInset, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar";
import { isApiError } from "@/lib/api/client";
import { meQuery, useMe } from "@/lib/api/queries";
import { isLocale, useT } from "@/lib/i18n";
import { startLive } from "@/lib/live";
import { useLiveStore } from "@/lib/live/store";
import { LOCALE_KEY, readStorage } from "@/lib/theme";

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

  // A browser without its own language choice starts in the account's language.
  useEffect(() => {
    const live = useLiveStore.getState();
    if (readStorage(LOCALE_KEY) === null && isLocale(me.locale) && me.locale !== live.locale) {
      live.setLocale(me.locale);
    }
  }, [me.locale]);

  return (
    <SidebarProvider>
      <AppSidebar me={me} />
      {/* Full-height pages (a room) mark themselves with data-fill and scroll inside. */}
      <SidebarInset className="min-w-0 has-[[data-fill]]:h-svh has-[[data-fill]]:overflow-hidden">
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
