import { api } from "@/shared/api/client";
import type { PushKey, PushSubscriptionRequest } from "@/shared/api/types.gen";
import { activeRegistration } from "./subscription";

/*
 * The page's half of the push setup (PUSH-19, PUSH-28): register the worker that shows the
 * notifications, and hand the server a subscription the browser already holds. Neither asks for
 * permission, so loading any page stays quiet (PUSH-21).
 */

/** The worker is served from the root so its scope can be the whole app (PUSH-19). */
export const SERVICE_WORKER_URL = "/sw.js";

/**
 * registerServiceWorker registers the worker once, at the root scope. A browser without service
 * workers, or a registration that fails, is not fatal: the app keeps working and Settings
 * explains the unsupported state (PUSH-21).
 */
export async function registerServiceWorker(): Promise<void> {
  const container = navigator.serviceWorker;
  if (!container) return;
  try {
    await container.register(SERVICE_WORKER_URL, { scope: "/" });
  } catch {
    // Nothing to do: notifications stay off on this device and the rest of the app runs.
  }
}

let running: Promise<void> | null = null;

/**
 * ensurePushSubscription re-registers the subscription the browser already holds, without asking
 * for permission. The server ties a subscription to the registering session, so a new session (or
 * a VAPID key the server had to replace) has to be told about the device again (PUSH-19, PUSH-28).
 *
 * It is safe to call on every signed-in mount: concurrent calls share one run and it resolves even
 * when the browser has nothing to offer or its worker never gets ready.
 */
export function ensurePushSubscription(): Promise<void> {
  running ??= registerExisting()
    .catch(() => {})
    .finally(() => {
      running = null;
    });
  return running;
}

async function registerExisting(): Promise<void> {
  if (typeof Notification === "undefined" || Notification.permission !== "granted") return;
  const manager = await pushManager();
  if (!manager) return;
  const existing = await manager.getSubscription();
  if (!existing) return;

  const { public_key: publicKey } = await api<PushKey>("GET", "/push/key");
  const subscription = sameKey(existing, publicKey)
    ? existing
    : await subscribeWith(manager, existing, publicKey);
  const request = requestOf(subscription);
  if (!request) return;
  await api("POST", "/push/subscriptions", request);
}

/** pushManager returns the active registration's PushManager, or null when there is none. */
async function pushManager(): Promise<PushManager | null> {
  const container = navigator.serviceWorker;
  if (!container) return null;
  // Rejects when the worker never gets ready; ensurePushSubscription stays quiet about that.
  const registration = await activeRegistration();
  return registration.pushManager ?? null;
}

/**
 * sameKey reports whether the subscription was made with the instance's current key. A browser
 * that does not report its key is trusted, so a page load never churns a working subscription.
 */
function sameKey(subscription: PushSubscription, publicKey: string): boolean {
  const current = subscription.options?.applicationServerKey;
  if (!current) return true;
  return encodeKey(current) === canonicalKey(publicKey);
}

/**
 * subscribeWith replaces a subscription made with another key: the browser is already granted
 * permission, so subscribing again shows no prompt (PUSH-28).
 */
async function subscribeWith(
  manager: PushManager,
  existing: PushSubscription,
  publicKey: string,
): Promise<PushSubscription> {
  await existing.unsubscribe();
  return manager.subscribe({
    userVisibleOnly: true,
    applicationServerKey: decodeKey(publicKey),
  });
}

/** requestOf is the body of POST /push/subscriptions, or null when the subscription has no keys. */
function requestOf(subscription: PushSubscription): PushSubscriptionRequest | null {
  const json = subscription.toJSON();
  const endpoint = json.endpoint ?? subscription.endpoint;
  const p256dh = json.keys?.p256dh;
  const auth = json.keys?.auth;
  if (!endpoint || !p256dh || !auth) return null;
  return { endpoint, p256dh, auth };
}

/** decodeKey turns the server's base64url key (no padding) into the bytes the browser subscribes with. */
function decodeKey(value: string): Uint8Array<ArrayBuffer> {
  const base64 = canonicalKey(value).replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(base64.padEnd(Math.ceil(base64.length / 4) * 4, "="));
  const bytes = new Uint8Array(new ArrayBuffer(binary.length));
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

/** encodeKey is the inverse of decodeKey: base64url without padding, to compare two keys. */
function encodeKey(value: ArrayBuffer | ArrayBufferView): string {
  const bytes = ArrayBuffer.isView(value)
    ? new Uint8Array(value.buffer, value.byteOffset, value.byteLength)
    : new Uint8Array(value);
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/** canonicalKey is the server's key in the one spelling the comparison and decoding use. */
function canonicalKey(value: string): string {
  return value.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
