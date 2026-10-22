import { QueryClient } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import { AppProviders } from "@/components/app-providers";
import { createAppRouter } from "@/router";

export function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } },
  });
}

/** Renders the real route tree at `path` with an isolated query client and memory history. */
export function renderRoute(path: string, queryClient = makeQueryClient()) {
  const history = createMemoryHistory({ initialEntries: [path] });
  const router = createAppRouter(queryClient, history);
  const utils = render(
    <AppProviders queryClient={queryClient}>
      <RouterProvider router={router} />
    </AppProviders>,
  );
  return { ...utils, router, queryClient, history };
}

/** Renders a component with the app providers (query cache, i18n, tooltips), without a router. */
export function renderWithProviders(ui: ReactNode, queryClient = makeQueryClient()) {
  const utils = render(<AppProviders queryClient={queryClient}>{ui}</AppProviders>);
  return { ...utils, queryClient };
}

/**
 * Renders a component inside a router that knows only the profile route it links to (PDM-03),
 * for components that navigate but are not a page of the app.
 */
export function renderWithRouter(ui: ReactNode, queryClient = makeQueryClient()) {
  const root = createRootRoute({ component: () => <>{ui}</> });
  const profile = createRoute({
    getParentRoute: () => root,
    path: "/contacts/$id",
    component: () => null,
  });
  const router = createRouter({
    routeTree: root.addChildren([profile]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  const utils = render(
    <AppProviders queryClient={queryClient}>
      <RouterProvider router={router} />
    </AppProviders>,
  );
  return { ...utils, router, queryClient };
}
