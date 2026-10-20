import { type APIRequestContext, test as base, expect } from "@playwright/test";
import { type Instance, startInstance } from "./server";

export const ADMIN = { username: "admin", password: "e2e-admin-password" };
export const MEMBER = { username: "member", password: "e2e-member-password" };

/** One fresh server per worker; a worker runs a single project, so each viewport gets its own. */
export const test = base.extend<object, { instance: Instance }>({
  instance: [
    // biome-ignore lint/correctness/noEmptyPattern: Playwright fixtures must destructure their first argument.
    async ({}, use) => {
      const instance = await startInstance();
      await use(instance);
      await instance.stop();
    },
    { scope: "worker" },
  ],
  baseURL: async ({ instance }, use) => {
    await use(instance.url);
  },
});

export { expect };

/**
 * JSON call to the API with the context's cookies. Cookie-authenticated unsafe requests must
 * carry a same-origin `Origin` header (CSRF rule), which a browser adds and the API client does not.
 */
export async function apiCall<T>(
  request: APIRequestContext,
  instance: Instance,
  method: "GET" | "POST" | "PUT",
  path: string,
  data?: unknown,
): Promise<T> {
  const res = await request.fetch(`${instance.url}/api/v1${path}`, {
    method,
    data,
    headers: { Origin: instance.url },
  });
  expect(res.ok(), `${method} ${path} → ${res.status()}`).toBe(true);
  return (await res.json()) as T;
}
