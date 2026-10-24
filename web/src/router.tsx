import type { QueryClient } from "@tanstack/react-query";
import { createRouter, type RouterHistory } from "@tanstack/react-router";
import { routeTree } from "./routeTree.gen";

export interface RouterContext {
  queryClient: QueryClient;
}

/**
 * Whether the router puts scroll positions back on arriving at a page (and scrolls a new one to
 * the top). A conversation keeps its own: its timeline is virtualized and pinned to the newest
 * message, and the router finds what scrolled again by its DOM path, which is the same in every
 * conversation. It therefore wrote the position of the conversation you came from (or went back
 * to) into the timeline you opened, which cancelled the timeline's scroll to its end and left it
 * in the middle, or at the top. The side panels' lists are the same case.
 */
export function restoresScroll(pathname: string): boolean {
  return !pathname.startsWith("/rooms/");
}

export function createAppRouter(queryClient: QueryClient, history?: RouterHistory) {
  return createRouter({
    routeTree,
    history,
    context: { queryClient },
    defaultPreload: "intent",
    // Query owns caching; the router always asks loaders, which read from the query cache.
    defaultPreloadStaleTime: 0,
    scrollRestoration: ({ location }) => restoresScroll(location.pathname),
  });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
