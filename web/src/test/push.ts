import { vi } from "vitest";

/*
 * A fake browser push stack for the Settings tests (PUSH-18, PUSH-21, PUSH-22): a Notification
 * permission, a service worker registration whose PushManager hands out subscriptions, and the
 * endpoint and keys a real browser would create. Nothing leaves the process and no push service
 * is ever contacted.
 */

/** The instance key `GET /push/key` answers: a base64url, unpadded 65-byte point (PUSH-02). */
export const PUSH_SERVER_KEY =
  "BAUSHyw5RlNgbXqHlKGuu8jV4u_8CRYjMD1KV2RxfouYpbK_zNnm8wANGic0QU5baHWCj5yptsPQ3er3BBEeKzg";

/** What the browser's subscription carries and the page POSTs (PUSH-03, PUSH-22). */
export const PUSH_ENDPOINT = "https://push.example.test/e2e";
export const PUSH_P256DH =
  "BCVxAwoRGB8mLTQ7QklQV15lbHN6gYiPlp2kq7K5wMfO1dzj6vH4_wYNFBsiKTA3PkVMU1phaG92fYSLkpmgp64";
export const PUSH_AUTH = "BTBZAQwXIi04Q05ZZG96hQ";

export interface FakePushSubscription {
  endpoint: string;
  toJSON: () => { endpoint: string; keys: { p256dh: string; auth: string } };
  unsubscribe: ReturnType<typeof vi.fn>;
}

export interface FakePushOptions {
  /** `Notification.permission` before any prompt; "default" by default. */
  permission?: NotificationPermission;
  /** What the permission prompt answers; "granted" by default. */
  answer?: NotificationPermission;
  /** Whether the browser has `PushManager` at all; true by default. */
  supported?: boolean;
  /** Whether the browser already holds a subscription, as after a reload. */
  subscribed?: boolean;
}

export interface FakePush {
  /** `Notification.requestPermission`, recorded: the app may call it only on a click. */
  requestPermission: ReturnType<typeof vi.fn>;
  /** `pushManager.subscribe`, recorded with the options the page passed. */
  subscribe: ReturnType<typeof vi.fn>;
  /** The subscription the browser holds now; created by `subscribe` or the initial one. */
  current: () => FakePushSubscription | null;
}

/**
 * Replaces `navigator.serviceWorker`, `PushManager` and `Notification` for one test: `vi.unstubAllGlobals`
 * in `afterEach` puts the browser back.
 */
export function stubPush(options: FakePushOptions = {}): FakePush {
  const make = (): FakePushSubscription => ({
    endpoint: PUSH_ENDPOINT,
    toJSON: () => ({
      endpoint: PUSH_ENDPOINT,
      keys: { p256dh: PUSH_P256DH, auth: PUSH_AUTH },
    }),
    unsubscribe: vi.fn(async () => true),
  });
  let held: FakePushSubscription | null = options.subscribed ? make() : null;

  const requestPermission = vi.fn<() => Promise<NotificationPermission>>(
    async () => options.answer ?? "granted",
  );
  const subscribe = vi.fn<(options: PushSubscriptionOptionsInit) => Promise<FakePushSubscription>>(
    async () => {
      held = make();
      return held;
    },
  );

  const registration = {
    pushManager: {
      getSubscription: vi.fn(async () => held),
      subscribe,
    },
  };
  const serviceWorker = {
    ready: Promise.resolve(registration),
    register: vi.fn(async () => registration),
  };
  // The app reads the three APIs behind lib/push/subscription.ts, so stubbing the globals is enough.
  vi.stubGlobal("navigator", { ...navigator, serviceWorker });
  if (options.supported !== false) vi.stubGlobal("PushManager", class PushManager {});
  vi.stubGlobal(
    "Notification",
    Object.assign(class Notification {}, {
      permission: options.permission ?? "default",
      requestPermission,
    }),
  );

  return { requestPermission, subscribe, current: () => held };
}
