import { createRootRouteWithContext, Outlet, useRouter } from "@tanstack/react-router";
import { useEffect } from "react";
import { CrashPage, NotFoundPage } from "@/components/status-pages";
import { Toaster } from "@/components/ui/sonner";
import { setUnauthorizedHandler } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/queries";
import { useLiveStore } from "@/lib/live/store";
import type { RouterContext } from "@/router";

/** Pages reachable without a session; a 401 there is not a reason to redirect. */
const PUBLIC_PATHS = ["/login", "/setup", "/invite/"];

export const isPublicPath = (pathname: string) =>
  PUBLIC_PATHS.some((p) => pathname === p || (p.endsWith("/") && pathname.startsWith(p)));

export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootLayout,
  notFoundComponent: NotFoundPage,
  errorComponent: RootError,
});

function RootLayout() {
  const router = useRouter();
  const { queryClient } = Route.useRouteContext();
  const theme = useLiveStore((s) => s.theme);

  // A 401 during use (expired or revoked session) sends the user to login and back (UI-02).
  useEffect(
    () =>
      setUnauthorizedHandler(() => {
        const loc = router.state.location;
        if (isPublicPath(loc.pathname)) return;
        queryClient.removeQueries({ queryKey: queryKeys.me });
        void router.navigate({ to: "/login", search: { redirect: loc.href } });
      }),
    [router, queryClient],
  );

  return (
    <>
      <Outlet />
      <Toaster position="bottom-right" theme={theme} />
    </>
  );
}

function RootError() {
  const router = useRouter();
  return <CrashPage onRetry={() => void router.invalidate()} />;
}
