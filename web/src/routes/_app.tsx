import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { SearchIcon } from "lucide-react";
import { useEffect } from "react";
import { AppSidebar } from "@/components/app-sidebar";
import { Wordmark } from "@/components/brand";
import { CommandMenu } from "@/components/command-menu";
import { LiveBanner } from "@/components/live-status";
import { Button } from "@/components/ui/button";
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
      <a
        href="#main"
        className="sr-only z-50 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground focus:not-sr-only focus:fixed focus:top-3 focus:left-3 focus:px-3 focus:py-2 focus-visible:ring-[3px] focus-visible:ring-ring/50"
      >
        {t("nav.skip")}
      </a>
      <AppSidebar me={me} />
      {/* Full-height pages (a room) mark themselves with data-fill and scroll inside. */}
      <SidebarInset
        id="main"
        tabIndex={-1}
        className="min-w-0 outline-none has-[[data-fill]]:h-svh has-[[data-fill]]:overflow-hidden"
      >
        <div className="sticky top-0 z-20 flex h-14 items-center gap-2 border-b bg-background/95 px-3 backdrop-blur md:hidden">
          <SidebarTrigger aria-label={t("nav.toggleSidebar")} />
          <Wordmark />
          <Button
            variant="ghost"
            size="icon"
            className="ml-auto"
            aria-label={t("palette.open")}
            onClick={() => useLiveStore.getState().setPaletteOpen(true)}
          >
            <SearchIcon aria-hidden="true" />
          </Button>
        </div>
        <LiveBanner />
        <Outlet />
      </SidebarInset>
      <CommandMenu me={me} />
    </SidebarProvider>
  );
}
