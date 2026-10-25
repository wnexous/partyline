import { type APIRequestContext, test as base, expect, type Page } from "@playwright/test";
import { type Instance, startInstance } from "./server";

/** What logging in takes. */
export interface Credentials {
  username: string;
  password: string;
}

export const ADMIN: Credentials = { username: "admin", password: "e2e-admin-password" };
export const MEMBER: Credentials = { username: "member", password: "e2e-member-password" };

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
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE",
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

/**
 * Waits for the animations and transitions that end (a dialog's fade and zoom, a colour change
 * with the theme) so sizes are read, and pictures taken, from the settled state. Endless ones,
 * such as the online dot's pulse, are not waited for.
 */
export async function settled(page: Page) {
  await page.evaluate(async () => {
    const frames = () =>
      new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done)));
    const running = () =>
      document
        .getAnimations()
        .filter((animation) => animation.effect?.getComputedTiming().iterations !== Infinity);
    // A transition only exists from the first frame after the style change that starts it, and an
    // element inside another one (a button in a panel) starts its own when the parent's value
    // moves: wave after wave, until a frame finds none.
    for (let wave = 0; wave < 10; wave += 1) {
      await frames();
      const pending = running();
      if (pending.length === 0) return;
      await Promise.all(pending.map((animation) => animation.finished.catch(() => undefined)));
    }
  });
}
