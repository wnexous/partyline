import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { render } from "@testing-library/react";
import { createAppRouter } from "@/router";

/** Renders the real route tree at `path` with an isolated query client and memory history. */
export function renderRoute(path: string) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } },
  });
  const history = createMemoryHistory({ initialEntries: [path] });
  const router = createAppRouter(queryClient, history);
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { ...utils, router, queryClient, history };
}
