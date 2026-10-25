import { createFileRoute, Outlet, redirect, useRouterState } from "@tanstack/react-router";
import { SearchIcon } from "lucide-react";
import { useEffect } from "react";
import { AppSidebar } from "@/components/app-sidebar";
import { BackToConversations } from "@/components/back-link";
import { Wordmark } from "@/components/brand";
import { CommandMenu } from "@/components/command-menu";
import { LiveBanner } from "@/components/live-status";
import { OrgSwitcher } from "@/components/org-switcher";
import { ProfilePanel } from "@/components/profile-panel";
import { Button } from "@/components/ui/button";
import { SidebarInset } from "@/components/ui/sidebar";
import { useIsMobile } from "@/hooks/use-mobile";
import { useVisualViewport } from "@/hooks/use-visual-viewport";
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

/*
 * The messenger shell (LAY-01, LAY-05): the conversation list in a column beside the page on wide
 * screens. Below 768 px there is room for one of them: `/` is the list, every other page opens
 * full screen with a way back. A conversation brings its own header, back button included.
 */
function AppLayout() {
  const t = useT();
  const { queryClient, me: initialMe } = Route.useRouteContext();
  const me = useMe().data ?? initialMe;
  const isMobile = useIsMobile();

  useEffect(() => startLive(queryClient), [queryClient]);
  // The conversation is as tall as what the user can see, keyboard included (KBD-01).
  useVisualViewport();

  // A browser without its own language choice starts in the account's language.
  useEffect(() => {
    const live = useLiveStore.getState();
    if (readStorage(LOCALE_KEY) === null && isLocale(me.locale) && me.locale !== live.locale) {
      live.setLocale(me.locale);
    }
  }, [me.locale]);

  return (
    <div className="flex min-h-svh w-full">
      <a
        href="#main"
        className="sr-only z-50 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground focus:not-sr-only focus:fixed focus:top-3 focus:left-3 focus:px-3 focus:py-2 focus-visible:ring-[3px] focus-visible:ring-ring/70"
      >
        {t("nav.skip")}
      </a>
      {isMobile ? null : <AppSidebar me={me} />}
      {/*
        Full-height pages (a conversation, the phone's list) mark themselves with data-fill and scroll
        inside. They are as tall as the visual viewport (`--app-height`, below the page's top by
        `--app-top`), which the on-screen keyboard shrinks in every browser; `100dvh` stands in until
        it is known (KBD-01, KBD-05).
      */}
      <SidebarInset
        id="main"
        tabIndex={-1}
        className="min-w-0 outline-none has-[[data-fill]]:top-[var(--app-top,0px)] has-[[data-fill]]:h-[var(--app-height,100dvh)] has-[[data-fill]]:overflow-hidden"
      >
        <MobileBar />
        <LiveBanner />
        <Outlet />
      </SidebarInset>
      <CommandMenu me={me} />
      <ProfilePanel />
    </div>
  );
}

/**
 * The top bar of a phone's full-screen pages: a way back to the list, the brand, the organization
 * switcher and the search.
 * The list itself (`/`) and a conversation (`/rooms/…`, whose header carries the back button)
 * have none. It follows the route itself, so the layout does not render again on navigation.
 */
function MobileBar() {
  const t = useT();
  const isMobile = useIsMobile();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  if (!isMobile || pathname === "/" || pathname.startsWith("/rooms/")) return null;
  return (
    <div className="sticky top-0 z-20 flex h-14 shrink-0 items-center gap-1 border-b bg-background/95 px-1 backdrop-blur">
      <BackToConversations />
      <Wordmark />
      <div className="ml-auto flex items-center">
        <OrgSwitcher compact />
        <Button
          variant="ghost"
          size="icon"
          className="size-11"
          aria-label={t("palette.open")}
          onClick={() => useLiveStore.getState().setPaletteOpen(true)}
        >
          <SearchIcon aria-hidden="true" />
        </Button>
      </div>
    </div>
  );
}
