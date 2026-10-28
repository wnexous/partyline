import { describe, expect, it, vi } from "vitest";
// The worker is a classic script served from the root, so the test runs the real file as text
// instead of importing it as a module.
import source from "../../../../public/sw.js?raw";

/*
 * What the service worker does with a push: show it, suppress the conversation that is already on
 * screen, and open the conversation when the user taps it (PUSH-16, PUSH-17, PUSH-22).
 */

const ORIGIN = "https://partyline.example";
const ROOM_R1 = `${ORIGIN}/rooms/r1`;

const PAYLOAD = {
  title: "Design",
  author: "Ana",
  preview: "green",
  url: ROOM_R1,
  tag: "room:r1",
};

interface PushData {
  json: () => unknown;
}

interface FakeClient {
  url: string;
  focused: boolean;
  focus: ReturnType<typeof vi.fn>;
  navigate: ReturnType<typeof vi.fn>;
}

type CloseMock = ReturnType<typeof vi.fn>;

interface Shown {
  title: string;
  options: Record<string, unknown>;
  close: CloseMock;
}

interface FakeNotification {
  close: () => void;
  data?: { url?: string };
}

interface Harness {
  shown: Shown[];
  windows: FakeClient[];
  showNotification: ReturnType<typeof vi.fn>;
  openWindow: ReturnType<typeof vi.fn>;
  push: (data: PushData | null) => Promise<void>;
  click: (notification: FakeNotification) => Promise<void>;
}

function client(path: string, focused: boolean): FakeClient {
  return {
    url: `${ORIGIN}${path}`,
    focused,
    focus: vi.fn(async () => {}),
    navigate: vi.fn(async () => {}),
  };
}

function data(value: unknown): PushData {
  return { json: () => value };
}

// harness runs the real worker source against a fake `self` that records the listeners it
// registers and the notifications it shows. The worker reads the browser language from `navigator`,
// so the fake one makes the fallback text deterministic.
function harness(language = "en-US"): Harness {
  const listeners = new Map<string, (event: Record<string, unknown>) => void>();
  const shown: Shown[] = [];
  const windows: FakeClient[] = [];

  const showNotification = vi.fn<
    (title: string, options: Record<string, unknown>) => Promise<{ close: () => void }>
  >(async (title, options) => {
    const close = vi.fn();
    shown.push({ title, options, close });
    return { close };
  });

  const registration = {
    showNotification,
    getNotifications: vi.fn<(filter?: { tag?: string }) => Promise<{ close: CloseMock }[]>>(
      async (filter) => {
        const tag = filter?.tag;
        return shown
          .filter((notification) => tag === undefined || notification.options.tag === tag)
          .map((notification) => ({ close: notification.close }));
      },
    ),
  };

  const openWindow = vi.fn<(url: string) => Promise<unknown>>(async () => null);

  const self = {
    addEventListener: (type: string, listener: (event: Record<string, unknown>) => void) => {
      listeners.set(type, listener);
    },
    skipWaiting: vi.fn(async () => {}),
    registration,
    clients: {
      claim: vi.fn(async () => {}),
      matchAll: vi.fn(async () => windows),
      openWindow,
    },
  };

  new Function("self", "navigator", source)(self, { language });

  async function dispatch(type: string, event: Record<string, unknown>) {
    const listener = listeners.get(type);
    if (!listener) throw new Error(`the worker registers no ${type} listener`);
    let waited: Promise<unknown> | undefined;
    listener({
      ...event,
      waitUntil: (promise: Promise<unknown>) => {
        waited = promise;
      },
    });
    await waited;
  }

  return {
    shown,
    windows,
    showNotification,
    openWindow,
    push: (payload) => dispatch("push", { data: payload }),
    click: (notification) => dispatch("notificationclick", { notification }),
  };
}

describe("showing a push (PUSH-16)", () => {
  it("shows the conversation title, the author and the preview", async () => {
    const worker = harness();
    await worker.push(data(PAYLOAD));

    expect(worker.showNotification).toHaveBeenCalledTimes(1);
    expect(worker.showNotification).toHaveBeenCalledWith("Design", {
      body: "Ana: green",
      tag: "room:r1",
      data: { url: ROOM_R1 },
      icon: "/icons/icon-192.png",
    });
  });

  it("shows the author alone when the message carries no text", async () => {
    const worker = harness();
    await worker.push(data({ ...PAYLOAD, preview: "" }));

    expect(worker.shown).toHaveLength(1);
    expect(worker.shown[0]?.options.body).toBe("Ana");
  });
});

describe("a conversation that is already on screen (PUSH-17)", () => {
  it("shows the push silently and closes it at once, never showing nothing", async () => {
    const worker = harness();
    worker.windows.push(client("/rooms/r1", true));
    await worker.push(data(PAYLOAD));

    expect(worker.showNotification).toHaveBeenCalledTimes(1);
    expect(worker.shown[0]?.options).toMatchObject({ silent: true, tag: "room:r1" });
    expect(worker.shown[0]?.close).toHaveBeenCalledTimes(1);
  });

  it("shows a push for another conversation", async () => {
    const worker = harness();
    worker.windows.push(client("/rooms/r2", true));
    await worker.push(data(PAYLOAD));

    expect(worker.showNotification).toHaveBeenCalledTimes(1);
    expect(worker.shown[0]?.options.silent).toBeUndefined();
  });

  it("shows a push when the window is not focused", async () => {
    const worker = harness();
    worker.windows.push(client("/rooms/r1", false));
    await worker.push(data(PAYLOAD));

    expect(worker.showNotification).toHaveBeenCalledTimes(1);
    expect(worker.shown[0]?.options.silent).toBeUndefined();
  });
});

describe("opening the conversation (PUSH-16)", () => {
  it("closes the notification, focuses the window and navigates it", async () => {
    const worker = harness();
    const tab = client("/", true);
    worker.windows.push(tab);
    const notification = { close: vi.fn(), data: { url: ROOM_R1 } };

    await worker.click(notification);

    expect(notification.close).toHaveBeenCalledTimes(1);
    expect(tab.focus).toHaveBeenCalledTimes(1);
    expect(tab.navigate).toHaveBeenCalledWith(ROOM_R1);
  });

  it("opens a window when the app is not open", async () => {
    const worker = harness();
    const notification = { close: vi.fn(), data: { url: ROOM_R1 } };

    await worker.click(notification);

    expect(notification.close).toHaveBeenCalledTimes(1);
    expect(worker.openWindow).toHaveBeenCalledWith(ROOM_R1);
  });
});

describe("the fallback body (PUSH-16)", () => {
  it("reads the browser language: Portuguese for pt-BR", async () => {
    const worker = harness("pt-BR");
    await worker.push(data({ ...PAYLOAD, author: "", preview: "" }));

    expect(worker.shown).toHaveLength(1);
    expect(worker.shown[0]?.options.body).toBe("Nova mensagem");
  });

  it("keeps English for every other language", async () => {
    const worker = harness("en-US");
    await worker.push(data({ ...PAYLOAD, author: "", preview: "" }));

    expect(worker.shown).toHaveLength(1);
    expect(worker.shown[0]?.options.body).toBe("New message");
  });
});

describe("a push without a usable payload", () => {
  it("still shows a generic notification that opens the app", async () => {
    const payloads: (PushData | null)[] = [
      null,
      data(null),
      { json: () => JSON.parse("not json") },
    ];

    for (const payload of payloads) {
      const worker = harness();
      await expect(worker.push(payload)).resolves.toBeUndefined();
      expect(worker.shown).toHaveLength(1);
      expect(worker.shown[0]?.title).toBe("partyline");
      expect(worker.shown[0]?.options.data).toEqual({ url: "/" });
    }
  });
});

describe("the worker stays a plain, stateless script (PUSH-22)", () => {
  it("keeps message text out of storage and has no cache, import or export", () => {
    expect(source).not.toMatch(/localStorage|indexedDB|caches/);
    expect(source).not.toMatch(/^\s*(import|export)\s/m);
    expect(source).not.toMatch(/innerHTML|insertAdjacentHTML|document\./);
  });
});
