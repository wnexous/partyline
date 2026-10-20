import { vi } from "vitest";

type Handler = (url: URL, init: RequestInit | undefined) => Response | Promise<Response>;

export const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

export const apiError = (
  status: number,
  code: string,
  message = code,
  fields?: Record<string, string>,
) => json(status, { error: { code, message, fields } });

/**
 * Stubs global fetch with a route table keyed by "METHOD /path" (path without /api/v1 and
 * query). Unmatched requests answer 404 so a missing stub fails loudly in assertions.
 */
export function mockApi(routes: Record<string, Handler | Response | (() => Response)>) {
  const calls: { method: string; path: string; url: URL; body: unknown }[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const method = (init?.method ?? "GET").toUpperCase();
    const path = url.pathname.replace(/^\/api\/v1/, "");
    let body: unknown = init?.body;
    if (typeof body === "string") {
      try {
        body = JSON.parse(body);
      } catch {
        // keep raw
      }
    }
    calls.push({ method, path, url, body });
    const route = routes[`${method} ${path}`];
    if (!route) return json(404, { error: { code: "not_found", message: `${method} ${path}` } });
    if (route instanceof Response) return route.clone();
    return (route as Handler)(url, init);
  });
  vi.stubGlobal("fetch", fetchMock);
  return { fetchMock, calls };
}
