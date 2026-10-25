// Partyline's service worker: it shows a push and opens its conversation (PUSH-16, PUSH-17).
// It is a classic script in public/, so it has no imports, no build step and no offline storage:
// the payload lives in memory for the one notification it describes (PUSH-22).

const GENERIC_TITLE = "partyline";
const GENERIC_URL = "/";
const GENERIC_BODY = "New message";
const ICON = "/icons/icon-192.png";

self.addEventListener("install", (event) => {
  event.waitUntil(self.skipWaiting());
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("push", (event) => {
  event.waitUntil(show(event));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  event.waitUntil(open(event.notification));
});

// text keeps a payload field a string: anything else becomes empty and takes the fallback.
function text(value) {
  return typeof value === "string" ? value : "";
}

function generic() {
  return { title: GENERIC_TITLE, author: "", preview: "", url: GENERIC_URL, tag: "" };
}

// parse reads the payload; missing or malformed data falls back to a generic notification that
// still opens the app.
function parse(event) {
  if (!event?.data) return generic();
  let data;
  try {
    data = event.data.json();
  } catch {
    return generic();
  }
  if (!data || typeof data !== "object") return generic();
  return {
    title: text(data.title) || GENERIC_TITLE,
    author: text(data.author),
    preview: text(data.preview),
    url: text(data.url) || GENERIC_URL,
    tag: text(data.tag),
  };
}

function body(data) {
  if (data.preview) return data.author ? `${data.author}: ${data.preview}` : data.preview;
  return data.author || GENERIC_BODY;
}

function path(link, base) {
  try {
    return new URL(link, base).pathname;
  } catch {
    return "";
  }
}

// focusedHere is the focus rule (PUSH-17): only the very conversation the payload names, and only
// in a window the browser reports as focused, is suppressed. The whole URL is never compared, so
// the query string of the route does not matter.
function focusedHere(windows, url) {
  return windows.some(
    (client) => client.focused === true && path(client.url) === path(url, client.url),
  );
}

function optionsFor(data) {
  return {
    body: body(data),
    tag: data.tag,
    data: { url: data.url },
    icon: ICON,
  };
}

// show always ends with a notification. When the conversation is on screen the notification is
// silent and closed at once: Safari revokes a subscription that receives pushes without one, and
// Chrome would show a generic one instead (PUSH-17).
async function show(event) {
  const data = parse(event);
  const options = optionsFor(data);
  const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  if (!focusedHere(windows, data.url)) {
    await self.registration.showNotification(data.title, options);
    return;
  }
  await self.registration.showNotification(data.title, { ...options, silent: true });
  const shown = await self.registration.getNotifications({ tag: data.tag });
  for (const notification of shown) notification.close();
}

function target(notification) {
  const data = notification?.data;
  return data ? text(data.url) || GENERIC_URL : GENERIC_URL;
}

function sameOrigin(link, base) {
  try {
    return new URL(link).origin === new URL(base, link).origin;
  } catch {
    return false;
  }
}

// open focuses a window of the app and navigates it to the conversation, or opens a new one when
// the app is not open.
async function open(notification) {
  const url = target(notification);
  const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  const client = windows.find((candidate) => sameOrigin(candidate.url, url));
  if (!client) {
    await self.clients.openWindow(url);
    return;
  }
  await client.focus();
  if (path(client.url) !== path(url, client.url)) {
    try {
      await client.navigate(url);
    } catch {
      // The window stays focused even when the navigation is refused.
    }
  }
}
