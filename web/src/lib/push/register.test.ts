import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { json, mockApi } from "@/test/fetch";
// The manifest is a static file in public/, served as-is; the test parses the committed bytes.
import manifestSource from "../../../public/manifest.webmanifest?raw";
import { ensurePushSubscription, registerServiceWorker } from "./register";

/*
 * The page's side of the push setup (PUSH-19, PUSH-20, PUSH-28): it registers the worker that shows
 * notifications, re-registers a browser subscription the server may have lost, and never asks for
 * permission on load.
 */

const ORIGIN = "https://partyline.example";

// The instance's key and a browser subscription, base64url without padding (internal/api/dto/push.go).
const SERVER_KEY =
  "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4";
const OTHER_KEY =
  "BLH8l6GCsUgHVw6w6f8m93U1y2ihF3FL77xn8KvJcFt8pLx1nH12Vp6GTgGrsL6KQ7pXk3sEw7CTjJ4d8sWZ2oQ";
const P256DH =
  "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4";
const AUTH = "BTBZMqHH6r4Tts7J_aSIgg";

function bytes(value: string): Uint8Array {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(padded.padEnd(Math.ceil(padded.length / 4) * 4, "="));
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) out[i] = binary.charCodeAt(i);
  return out;
}

function bufferOf(value: string): ArrayBuffer {
  return bytes(value).buffer as ArrayBuffer;
}

function plain(value: ArrayBuffer | ArrayBufferView): Uint8Array {
  if (ArrayBuffer.isView(value)) {
    return new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
  }
  return new Uint8Array(value);
}

interface FakeSubscription {
  endpoint: string;
  options: { applicationServerKey: ArrayBuffer | null; userVisibleOnly: boolean };
  toJSON: () => { endpoint: string; keys: { p256dh: string; auth: string } };
  unsubscribe: ReturnType<typeof vi.fn>;
}

function subscription(options: {
  endpoint?: string;
  key?: string | null;
  p256dh?: string;
  auth?: string;
}): FakeSubscription {
  const endpoint = options.endpoint ?? `${ORIGIN}/push/device-1`;
  const key = options.key === undefined ? SERVER_KEY : options.key;
  return {
    endpoint,
    options: {
      applicationServerKey: key === null ? null : bufferOf(key),
      userVisibleOnly: true,
    },
    toJSON: () => ({
      endpoint,
      keys: { p256dh: options.p256dh ?? P256DH, auth: options.auth ?? AUTH },
    }),
    unsubscribe: vi.fn(async () => true),
  };
}

interface FakePushManager {
  getSubscription: ReturnType<typeof vi.fn>;
  subscribe: ReturnType<typeof vi.fn>;
}

function pushManager(
  existing: FakeSubscription | null,
  created: FakeSubscription | null = null,
): FakePushManager {
  return {
    getSubscription: vi.fn(async () => existing),
    subscribe: vi.fn(async () => created ?? existing),
  };
}

function serviceWorker(manager: FakePushManager | null) {
  return {
    register: vi.fn(async () => ({ scope: "/" })),
    ready: Promise.resolve(manager === null ? { scope: "/" } : { pushManager: manager }),
  };
}

function notification(permission: NotificationPermission) {
  return { permission, requestPermission: vi.fn(async () => permission) };
}

// useBrowser installs the browser APIs the page reads; it returns the permission stub so a test
// can assert that nothing asked for it.
function useBrowser(options: { serviceWorker?: unknown; permission?: NotificationPermission }) {
  const prompt = notification(options.permission ?? "granted");
  vi.stubGlobal("navigator", { serviceWorker: options.serviceWorker });
  vi.stubGlobal("Notification", prompt);
  return prompt;
}

function mockPushApi(): ReturnType<typeof mockApi> {
  return mockApi({
    "GET /push/key": json(200, { public_key: SERVER_KEY }),
    "POST /push/subscriptions": json(200, {
      id: "sub-1",
      endpoint: `${ORIGIN}/push/device-1`,
      created_at: "2026-01-01T00:00:00Z",
    }),
  });
}

function posts(calls: ReturnType<typeof mockApi>["calls"]) {
  return calls.filter((call) => call.method === "POST" && call.path === "/push/subscriptions");
}

beforeEach(() => {
  mockPushApi();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("registering the worker (PUSH-19)", () => {
  it("registers /sw.js at the root scope exactly once", async () => {
    const container = serviceWorker(pushManager(null));
    useBrowser({ serviceWorker: container });

    await registerServiceWorker();

    expect(container.register).toHaveBeenCalledTimes(1);
    expect(container.register).toHaveBeenCalledWith("/sw.js", { scope: "/" });
  });

  it("swallows a failed registration instead of failing the page", async () => {
    const container = {
      register: vi.fn(async () => {
        throw new Error("registration failed");
      }),
      ready: Promise.resolve({}),
    };
    useBrowser({ serviceWorker: container });

    await expect(registerServiceWorker()).resolves.toBeUndefined();
    expect(container.register).toHaveBeenCalledTimes(1);
  });

  it("does nothing in a browser without a service worker", async () => {
    useBrowser({ serviceWorker: undefined });

    await expect(registerServiceWorker()).resolves.toBeUndefined();
  });
});

describe("re-registering a subscription the browser already holds (PUSH-19, PUSH-28)", () => {
  it("posts the existing subscription once permission is already granted", async () => {
    const existing = subscription({});
    const manager = pushManager(existing);
    const prompt = useBrowser({ serviceWorker: serviceWorker(manager) });
    const api = mockPushApi();

    await ensurePushSubscription();

    expect(manager.getSubscription).toHaveBeenCalledTimes(1);
    expect(manager.subscribe).not.toHaveBeenCalled();
    expect(existing.unsubscribe).not.toHaveBeenCalled();
    expect(posts(api.calls)).toHaveLength(1);
    expect(posts(api.calls)[0]?.body).toEqual({
      endpoint: `${ORIGIN}/push/device-1`,
      p256dh: P256DH,
      auth: AUTH,
    });
    expect(prompt.requestPermission).not.toHaveBeenCalled();
  });

  it.each(["default", "denied"] as const)(
    "never asks and never posts when permission is %s",
    async (permission) => {
      const existing = subscription({});
      const manager = pushManager(existing);
      const prompt = useBrowser({ serviceWorker: serviceWorker(manager), permission });
      const api = mockPushApi();

      await ensurePushSubscription();

      expect(prompt.requestPermission).not.toHaveBeenCalled();
      expect(manager.getSubscription).not.toHaveBeenCalled();
      expect(posts(api.calls)).toHaveLength(0);
    },
  );

  it("gives up quietly after 10 s when the worker never gets ready", async () => {
    vi.useFakeTimers();
    const manager = pushManager(subscription({}));
    useBrowser({
      serviceWorker: {
        register: vi.fn(async () => ({ scope: "/" })),
        // A registration that failed leaves `ready` pending forever.
        ready: new Promise(() => {}),
      },
    });
    const api = mockPushApi();

    let outcome = "pending";
    const done = ensurePushSubscription().then(() => {
      outcome = "resolved";
    });

    await vi.advanceTimersByTimeAsync(9_999);
    expect(outcome).toBe("pending");

    await vi.advanceTimersByTimeAsync(1);
    await done;

    expect(outcome).toBe("resolved");
    expect(manager.getSubscription).not.toHaveBeenCalled();
    expect(posts(api.calls)).toHaveLength(0);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("posts as soon as the worker is ready instead of waiting for the timeout", async () => {
    vi.useFakeTimers();
    const existing = subscription({});
    const manager = pushManager(existing);
    useBrowser({ serviceWorker: serviceWorker(manager) });
    const api = mockPushApi();

    await ensurePushSubscription();

    expect(posts(api.calls)).toHaveLength(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("posts nothing when the browser holds no subscription", async () => {
    const manager = pushManager(null);
    useBrowser({ serviceWorker: serviceWorker(manager) });
    const api = mockPushApi();

    await ensurePushSubscription();

    expect(manager.getSubscription).toHaveBeenCalledTimes(1);
    expect(manager.subscribe).not.toHaveBeenCalled();
    expect(posts(api.calls)).toHaveLength(0);
  });

  it("replaces a subscription made with another key without a prompt", async () => {
    const stale = subscription({ endpoint: `${ORIGIN}/push/old`, key: OTHER_KEY });
    const fresh = subscription({ endpoint: `${ORIGIN}/push/new`, key: SERVER_KEY });
    const manager = pushManager(stale, fresh);
    const prompt = useBrowser({ serviceWorker: serviceWorker(manager) });
    const api = mockPushApi();

    await ensurePushSubscription();

    expect(stale.unsubscribe).toHaveBeenCalledTimes(1);
    expect(manager.subscribe).toHaveBeenCalledTimes(1);
    const options = manager.subscribe.mock.calls[0]?.[0] as PushSubscriptionOptionsInit;
    expect(options.userVisibleOnly).toBe(true);
    expect(Array.from(plain(options.applicationServerKey as ArrayBuffer))).toEqual(
      Array.from(bytes(SERVER_KEY)),
    );
    expect(prompt.requestPermission).not.toHaveBeenCalled();
    expect(posts(api.calls)).toHaveLength(1);
    expect(posts(api.calls)[0]?.body).toEqual({
      endpoint: `${ORIGIN}/push/new`,
      p256dh: P256DH,
      auth: AUTH,
    });
  });
});

describe("the web app manifest (PUSH-20)", () => {
  it("names the app, starts at the root and offers 192 and 512 icons", () => {
    const manifest = JSON.parse(manifestSource) as {
      name: string;
      short_name: string;
      start_url: string;
      scope: string;
      display: string;
      background_color: string;
      theme_color: string;
      icons: { src: string; sizes: string; type: string }[];
    };

    expect(manifest.name).toBe("partyline");
    expect(manifest.short_name).toBe("partyline");
    expect(manifest.start_url).toBe("/");
    expect(manifest.scope).toBe("/");
    expect(manifest.display).toBe("standalone");
    expect(manifest.background_color).toBe("#fafafa");
    expect(manifest.theme_color).toBe("#fafafa");
    expect(manifest.icons.map((icon) => icon.sizes).sort()).toEqual(["192x192", "512x512"]);
    for (const icon of manifest.icons) {
      expect(icon.src).toMatch(/^\/icons\/icon-(192|512)\.png$/);
      expect(icon.type).toBe("image/png");
    }
  });
});
