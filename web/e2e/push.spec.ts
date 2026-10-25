import type { Page } from "@playwright/test";
import type { Me, SetupRequest } from "../src/lib/api/types.gen";
import { apiCall, test as base, type Credentials, expect, settled } from "./fixtures";
import { expectNoSidewaysScroll, loginAs } from "./helpers";
import { type Instance, startInstance } from "./server";

// The push flow against the built binary (web-push T12: PUSH-16 to PUSH-21). The manifest, the
// service worker, the icons and the Settings section are the real ones served by the server; only
// the browser's push service side is stood in for (see stubPush), so enabling a device stores a
// subscription and no request ever leaves for a real push service.
//
// The file has a server of its own, like orgs.spec.ts: the first-run test of smoke.spec.ts needs an
// instance nobody has set up, and two files of one project may share a worker.
const test = base.extend<object, { push: Instance }>({
  push: [
    // biome-ignore lint/correctness/noEmptyPattern: Playwright fixtures must destructure their first argument.
    async ({}, use) => {
      const instance = await startInstance();
      await use(instance);
      await instance.stop();
    },
    { scope: "worker" },
  ],
  baseURL: async ({ push }, use) => {
    await use(push.url);
  },
});
test.describe.configure({ mode: "serial" });

const OWNER: Credentials & { display_name: string } = {
  username: "owner",
  password: "e2e-push-owner-password",
  display_name: "Owner",
};

/** The RFC 8291 section 5 UA keys: a valid P-256 point and a 16-byte auth secret. */
const E2E_P256DH =
  "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4";
const E2E_AUTH = "BTBZMqHH6r4Tts7J_aSIgg";
/** The endpoint the stubbed browser hands out; example.test resolves nowhere. */
const E2E_ENDPOINT = "https://push.example.test/e2e";

test.beforeAll(async ({ browser, push }) => {
  const context = await browser.newContext();
  try {
    await apiCall<Me>(context.request, push, "POST", "/setup", {
      token: push.setupToken,
      ...OWNER,
    } satisfies SetupRequest);
  } finally {
    await context.close();
  }
});

/**
 * stubPush stands in for the browser's push service (PUSH-18): permission is granted and a
 * PushManager whose subscribe() hands back the endpoint and keys above, so the page's POST carries a
 * real subscription body and nothing is sent. The worker itself registers for real below.
 */
async function stubPush(page: Page) {
  await page.addInitScript(
    (data: { endpoint: string; p256dh: string; auth: string }) => {
      interface Held {
        endpoint: string;
        toJSON: () => { endpoint: string; keys: { p256dh: string; auth: string } };
        unsubscribe: () => Promise<boolean>;
      }
      let held: Held | null = null;
      const manager = {
        getSubscription: async (): Promise<Held | null> => held,
        subscribe: async (): Promise<Held> => {
          held = {
            endpoint: data.endpoint,
            toJSON: () => ({
              endpoint: data.endpoint,
              keys: { p256dh: data.p256dh, auth: data.auth },
            }),
            unsubscribe: async () => {
              held = null;
              return true;
            },
          };
          return held;
        },
      };
      // The worker registers at the root (PUSH-19): only the registration's PushManager is replaced.
      Object.defineProperty(ServiceWorkerRegistration.prototype, "pushManager", {
        configurable: true,
        get: () => manager,
      });
      if (!("PushManager" in window)) {
        Object.defineProperty(window, "PushManager", { configurable: true, value: class {} });
      }
      // Permission as the user gave it, so pressing Enable never opens a prompt (PUSH-21).
      Object.defineProperty(Notification, "permission", {
        configurable: true,
        get: () => "granted",
      });
      Object.defineProperty(Notification, "requestPermission", {
        configurable: true,
        value: async () => "granted",
      });
    },
    { endpoint: E2E_ENDPOINT, p256dh: E2E_P256DH, auth: E2E_AUTH },
  );
}

test("Settings enables this device, saves the group setting and disables it again (PUSH-18, PUSH-21)", async ({
  context,
  page,
  push,
}) => {
  await loginAs(context, push, OWNER);
  await stubPush(page);
  await page.goto("/settings");
  await expect(page.getByRole("heading", { name: "Notifications on this device" })).toBeVisible();
  await expect(page.getByRole("radio", { name: "Mentions only" })).toBeChecked();
  await expect(page.getByRole("button", { name: "Enable notifications" })).toBeVisible();
  await expect(page.getByText("On for this device")).toHaveCount(0);

  // Enabling asks nothing new (permission is granted) and stores the browser's subscription.
  const stored = page.waitForResponse(
    (res) =>
      res.url() === `${push.url}/api/v1/push/subscriptions` && res.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Enable notifications" }).click();
  const storedResponse = await stored;
  expect(storedResponse.status()).toBe(200);
  expect(storedResponse.request().postDataJSON()).toEqual({
    endpoint: E2E_ENDPOINT,
    p256dh: E2E_P256DH,
    auth: E2E_AUTH,
  });
  await expect(page.getByText("On for this device")).toBeVisible();

  // The account's group setting is saved through PATCH /me and survives a reload.
  const patched = page.waitForResponse(
    (res) => res.url() === `${push.url}/api/v1/me` && res.request().method() === "PATCH",
  );
  await page.getByRole("radio", { name: "Off" }).click();
  expect((await patched).status()).toBe(200);
  await expect(page.getByRole("radio", { name: "Off" })).toBeChecked();

  // Disabling drops the browser's subscription and the row in one go.
  const removed = page.waitForResponse(
    (res) =>
      res.url() === `${push.url}/api/v1/push/subscriptions` && res.request().method() === "DELETE",
  );
  await page.getByRole("button", { name: "Disable notifications" }).click();
  expect((await removed).status()).toBe(204);
  await expect(page.getByText("Not enabled on this device")).toBeVisible();

  await page.reload();
  await expect(page.getByRole("radio", { name: "Off" })).toBeChecked();
  await expect(page.getByRole("radio", { name: "Mentions only" })).not.toBeChecked();
});

test("the manifest, the worker and the icons are served from the root (PUSH-19, PUSH-20)", async ({
  context,
  page,
  push,
}) => {
  const [manifest, worker, icon192, icon512] = await Promise.all([
    context.request.get(`${push.url}/manifest.webmanifest`),
    context.request.get(`${push.url}/sw.js`),
    context.request.get(`${push.url}/icons/icon-192.png`),
    context.request.get(`${push.url}/icons/icon-512.png`),
  ]);

  expect(manifest.status()).toBe(200);
  expect(manifest.headers()["content-type"]).toBe("application/manifest+json");
  const app = (await manifest.json()) as { name: string; icons: { sizes: string }[] };
  expect(app.name).toBe("partyline");
  expect(app.icons.map((icon) => icon.sizes)).toEqual(["192x192", "512x512"]);

  expect(worker.status()).toBe(200);
  expect(worker.headers()["content-type"]).toContain("text/javascript");
  expect(await worker.text()).toContain('addEventListener("push"');

  for (const icon of [icon192, icon512]) {
    expect(icon.status()).toBe(200);
    expect(icon.headers()["content-type"]).toBe("image/png");
  }

  // The app registers the real worker at the root, so a notification can open any route (PUSH-19).
  await loginAs(context, push, OWNER);
  await page.goto("/settings");
  await expect
    .poll(async () =>
      page.evaluate(async () => (await navigator.serviceWorker.getRegistration())?.scope ?? ""),
    )
    .toBe(`${push.url}/`);
});

test("the notifications settings fit a 360 px phone (PUSH-21, UI-24)", async ({
  context,
  page,
  push,
}) => {
  await loginAs(context, push, OWNER);
  await stubPush(page);
  await page.goto("/settings");
  await expect(page.getByRole("heading", { name: "Notifications on this device" })).toBeVisible();
  await settled(page);
  await expectNoSidewaysScroll(page, "the notifications settings");
});
