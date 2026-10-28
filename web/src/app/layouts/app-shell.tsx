import type { QueryClient } from "@tanstack/react-query";
import { Outlet, useRouterState } from "@tanstack/react-router";
import { SearchIcon } from "lucide-react";
import { useEffect } from "react";
import { startLive } from "@/app/live";
import { useMe } from "@/entities/account";
import { ensurePushSubscription } from "@/features/push-notifications";
import type { Me } from "@/shared/api/types.gen";
import { isLocale, useT } from "@/shared/i18n";
import { useIsMobile } from "@/shared/lib/device";
import { usePrefsStore } from "@/shared/lib/prefs-store";
import { LOCALE_KEY, readStorage } from "@/shared/lib/storage";
import { useDisconnected } from "@/shared/lib/use-disconnected";
import { useVisualViewport } from "@/shared/lib/use-visual-viewport";
import { Wordmark } from "@/shared/ui/atoms/brand";
import { BackToConversations } from "@/shared/ui/molecules/back-link";
import { LiveBanner } from "@/shared/ui/molecules/live-status";
import { Button } from "@/shared/ui/primitives/button";
import { SidebarInset, SidebarProvider } from "@/shared/ui/primitives/sidebar";
import { AppSidebar, OrgSwitcher } from "@/widgets/app-sidebar";
import { CommandMenu } from "@/widgets/command-palette";
import { conversationSlots } from "@/widgets/conversation-list";
import { ProfilePanel } from "@/widgets/profile-panel";

/*
 * The messenger shell (LAY-01, LAY-05): the conversation list in a column beside the page on wide
 * screens. Below 768 px there is room for one of them: `/` is the list, every other page opens
 * full screen with a way back. A conversation brings its own header, back button included.
 */
export function AppShell({ queryClient, initialMe }: { queryClient: QueryClient; initialMe: Me }) {
  const t = useT();
  const me = useMe().data ?? initialMe;
  const isMobile = useIsMobile();

  useEffect(() => startLive(queryClient), [queryClient]);
  // A browser that already holds a push subscription hands it to this session without a prompt
  // (PUSH-19, PUSH-28).
  useEffect(() => {
    void ensurePushSubscription();
  }, []);
  // The conversation is as tall as what the user can see, keyboard included (KBD-01).
  useVisualViewport();

  // A browser without its own language choice starts in the account's language.
  useEffect(() => {
    const prefs = usePrefsStore.getState();
    if (readStorage(LOCALE_KEY) === null && isLocale(me.locale) && me.locale !== prefs.locale) {
      prefs.setLocale(me.locale);
    }
  }, [me.locale]);

  return (
    <SidebarProvider
      isMobile={isMobile}
      labels={{
        navLabel: t("nav.label"),
        sheetDescription: t("nav.sheetDescription"),
        toggleSidebar: t("nav.toggleSidebar"),
      }}
    >
      <div className="flex min-h-svh w-full">
        <a
          href="#main"
          className="sr-only z-50 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground focus:not-sr-only focus:fixed focus:top-3 focus:left-3 focus:px-3 focus:py-2 focus-visible:ring-[3px] focus-visible:ring-ring/70"
        >
          {t("nav.skip")}
        </a>
        {isMobile ? null : <AppSidebar me={me} {...conversationSlots} />}
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
          <LiveBanner disconnected={useDisconnected()} />
          <Outlet />
        </SidebarInset>
        <CommandMenu me={me} />
        <ProfilePanel />
      </div>
    </SidebarProvider>
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
          onClick={() => usePrefsStore.getState().setPaletteOpen(true)}
        >
          <SearchIcon aria-hidden="true" />
        </Button>
      </div>
    </div>
  );
}
