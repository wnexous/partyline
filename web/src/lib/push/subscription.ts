import type { PushSubscriptionRequest } from "@/lib/api/types.gen";

/*
 * The browser's side of Web Push (PUSH-18, PUSH-21, PUSH-22): whether this browser can push, the
 * permission it gave, the subscription it holds and the endpoint and keys the API stores. Every
 * browser API is read here, so the Settings tests can replace them with a fake (test/push.ts).
 */

/** Whether this browser can hold a push subscription at all (PUSH-21). */
export function pushSupported(): boolean {
  if (typeof window === "undefined" || typeof navigator === "undefined") return false;
  if (!("Notification" in window) || !("PushManager" in window)) return false;
  if (!("serviceWorker" in navigator)) return false;
  // Push needs a secure origin. A browser that does not report it is given the benefit of the doubt.
  return window.isSecureContext !== false;
}

/** The permission this browser gave partyline; a browser without notifications is "denied". */
export function permissionState(): NotificationPermission {
  return "Notification" in window ? Notification.permission : "denied";
}

/** Asks for the permission. Settings calls it only from the Enable button (PUSH-18). */
export function requestPermission(): Promise<NotificationPermission> {
  return Notification.requestPermission();
}

/** The registration of the app's worker, waiting for it when it is not active yet (PUSH-19). */
async function registration(): Promise<ServiceWorkerRegistration> {
  return navigator.serviceWorker.ready;
}

/** The subscription this browser holds; null when it holds none. */
export async function currentSubscription(): Promise<PushSubscription | null> {
  const active = await registration();
  return active.pushManager.getSubscription();
}

/** Creates a subscription for the instance key; the user must have granted permission first. */
export async function subscribe(
  applicationServerKey: Uint8Array<ArrayBuffer>,
): Promise<PushSubscription> {
  const active = await registration();
  return active.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey });
}

/** Drops this browser's subscription; one already gone is fine (PUSH-05). */
export async function unsubscribe(): Promise<void> {
  const held = await currentSubscription();
  await held?.unsubscribe();
}

/** What the API stores for one subscription: its endpoint and the two keys (PUSH-03, PUSH-22). */
export function subscriptionJSON(held: PushSubscription): PushSubscriptionRequest {
  const { keys } = held.toJSON();
  return {
    endpoint: held.endpoint,
    p256dh: keys?.p256dh ?? "",
    auth: keys?.auth ?? "",
  };
}

/** Decodes the unpadded base64url key `GET /push/key` answers into the bytes subscribe takes (PUSH-02). */
export function applicationServerKey(base64url: string): Uint8Array<ArrayBuffer> {
  const base64 = base64url.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(base64.padEnd(Math.ceil(base64.length / 4) * 4, "="));
  return Uint8Array.from(binary, (char) => char.charCodeAt(0));
}
