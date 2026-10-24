import type { Browser, BrowserContext, Locator, Page, TestInfo } from "@playwright/test";
import type { Me, Room, SetupRequest } from "../src/lib/api/types.gen";
import { apiCall, test as base, type Credentials, expect, settled } from "./fixtures";
import { isMobile, loginAs } from "./helpers";
import { type Instance, startInstance } from "./server";
import { shoot } from "./shots";

// The phone, in the browser (messenger-polish KBD-01..KBD-06, VOI-01..VOI-08, ACT-01..ACT-07,
// SWP-01..SWP-04, RCT-01..RCT-08). At 360 px the page is driven the way a phone drives it: a touch
// context, a finger, a long press. At 1280 px the same flows use a mouse. A headless browser has no
// on-screen keyboard and no microphone, so those are stood in for (see `keyboardStandIn` and the
// recorder in the voice test) and each test says what it does and does not prove.
//
// The file has a server of its own, like orgs.spec.ts: the first-run test of smoke.spec.ts needs an
// instance nobody has set up, and two files of one project may share a worker.
const test = base.extend<object, { phone: Instance }>({
  phone: [
    // biome-ignore lint/correctness/noEmptyPattern: Playwright fixtures must destructure their first argument.
    async ({}, use) => {
      const instance = await startInstance();
      await use(instance);
      await instance.stop();
    },
    { scope: "worker" },
  ],
  baseURL: async ({ phone }, use) => {
    await use(phone.url);
  },
});
test.describe.configure({ mode: "serial" });

const OWNER: Credentials & { display_name: string } = {
  username: "owner",
  password: "e2e-owner-password",
  display_name: "Owner",
};

test.beforeAll(async ({ browser, phone }) => {
  const context = await browser.newContext();
  try {
    await apiCall<Me>(context.request, phone, "POST", "/setup", {
      token: phone.setupToken,
      ...OWNER,
    } satisfies SetupRequest);
  } finally {
    await context.close();
  }
});

/** A phone: 360 px wide, a finger for a pointer. Wider projects get an ordinary window. */
async function openPage(browser: Browser, phone: Instance, testInfo: TestInfo) {
  const context = isMobile(testInfo)
    ? await browser.newContext({
        viewport: { width: 360, height: 780 },
        hasTouch: true,
        isMobile: true,
      })
    : await browser.newContext({ viewport: { width: 1280, height: 800 } });
  await loginAs(context, phone, OWNER);
  return { context, page: await context.newPage() };
}

async function newGroup(
  context: BrowserContext,
  instance: Instance,
  name: string,
  texts: string[],
) {
  const room = await apiCall<Room>(context.request, instance, "POST", "/groups", {
    name,
    members: [],
  });
  for (const text of texts) {
    await apiCall(context.request, instance, "POST", `/rooms/${room.id}/messages`, { text });
  }
  return room;
}

/** Where an element sits in the window (the visual viewport the test sees). */
async function rect(locator: Locator) {
  const box = await locator.boundingBox();
  if (!box) throw new Error("the element has no box: it is not on screen");
  return {
    top: box.y,
    bottom: box.y + box.height,
    height: box.height,
    left: box.x,
    right: box.x + box.width,
  };
}

/** How far the log is from its newest message, in pixels. */
const distanceFromNewest = (page: Page) =>
  page.getByRole("log").evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight);

declare global {
  interface Window {
    /** The stand-in for the on-screen keyboard: shrinks the visual viewport, not the layout one. */
    __viewport: (over: { height?: number | null; offsetTop?: number; scale?: number }) => void;
    __blurs: number;
  }
}

/**
 * A headless browser has no on-screen keyboard. This puts a stand-in in front of
 * `window.visualViewport`: it reports the height, offset and scale the test sets and tells the
 * page, the way a phone does when the keyboard opens, while the layout viewport stays as tall as
 * the window. That is Chrome without `interactive-widget` and iOS Safari, and it is what the page
 * must cope with; the real layout viewport is resized by the test itself where Chrome would.
 */
async function keyboardStandIn(page: Page) {
  await page.addInitScript(() => {
    const real = window.visualViewport;
    if (!real) return;
    const set: { height: number | null; offsetTop: number | null; scale: number | null } = {
      height: null,
      offsetTop: null,
      scale: null,
    };
    const stand = new Proxy(real, {
      get(target, prop) {
        if (prop === "height" && set.height !== null) return set.height;
        if (prop === "offsetTop" && set.offsetTop !== null) return set.offsetTop;
        if (prop === "scale" && set.scale !== null) return set.scale;
        const value = Reflect.get(target, prop, target);
        return typeof value === "function" ? value.bind(target) : value;
      },
    });
    Object.defineProperty(window, "visualViewport", { configurable: true, value: stand });
    window.__viewport = (over) => {
      if ("height" in over) set.height = over.height ?? null;
      set.offsetTop = over.offsetTop ?? null;
      set.scale = over.scale ?? null;
      real.dispatchEvent(new Event("resize"));
    };
  });
}

test("the composer stays above the keyboard and the timeline at its newest message (KBD-01..KBD-04, KBD-06)", async ({
  browser,
  phone,
}, testInfo) => {
  const { context, page } = await openPage(browser, phone, testInfo);
  try {
    await keyboardStandIn(page);
    const room = await newGroup(
      context,
      phone,
      "e2e-keyboard",
      Array.from({ length: 40 }, (_, i) => `keyboard ${i + 1}`),
    );
    await page.goto(`/rooms/${room.id}`);
    const log = page.getByRole("log");
    const box = page.getByRole("textbox", { name: "Message e2e-keyboard" });
    const composer = box.locator("xpath=ancestor::form");
    const send = page.getByRole("button", { name: "Send" });
    const header = page.getByRole("heading", { name: "e2e-keyboard", level: 1 });
    const screen = page.viewportSize() ?? { width: 0, height: 0 };
    const KEYBOARD = 420;
    const visible = screen.height - KEYBOARD;

    await expect(log.getByText("keyboard 40", { exact: true })).toBeInViewport();
    await expect.poll(() => distanceFromNewest(page)).toBeLessThanOrEqual(64);
    await settled(page);
    // Without a keyboard the composer sits on the bottom edge of the window.
    expect((await rect(composer)).bottom).toBeCloseTo(screen.height, 0);

    // KBD-01: the keyboard opens. The page is as tall as what is left, from the top; the header
    // stays where it was and the composer's bottom edge is the keyboard's top edge.
    await page.evaluate((height) => window.__viewport({ height }), visible);
    await expect.poll(async () => (await rect(composer)).bottom).toBeCloseTo(visible, 0);
    await settled(page);
    expect((await rect(header)).top).toBeGreaterThanOrEqual(0);
    expect((await rect(header)).bottom).toBeLessThan(visible / 2);
    expect((await rect(send)).bottom).toBeLessThanOrEqual(visible);
    // KBD-02: and the newest message is still the last thing above it.
    const newest = log.getByText("keyboard 40", { exact: true });
    await expect.poll(async () => (await rect(newest)).bottom).toBeLessThanOrEqual(visible);
    await expect.poll(() => distanceFromNewest(page)).toBeLessThanOrEqual(64);
    expect((await rect(newest)).top).toBeGreaterThanOrEqual((await rect(log)).top);
    // The picture says what the empty part is: a stand-in for the keyboard, drawn for the shot only.
    await page.evaluate((height) => {
      const pad = document.createElement("div");
      pad.id = "keyboard-pad";
      pad.textContent = "On-screen keyboard (simulated)";
      pad.setAttribute("aria-hidden", "true");
      pad.style.cssText = `position:fixed;left:0;right:0;bottom:0;height:${height}px;display:grid;place-items:center;background:#c9ccd1;color:#40444b;font:500 14px sans-serif;`;
      document.body.append(pad);
    }, KEYBOARD);
    await shoot(page, "composer-keyboard", testInfo);
    await page.evaluate(() => document.getElementById("keyboard-pad")?.remove());

    // iOS Safari pans the page up to keep a focused field in view instead of shrinking it: the
    // page then starts where the visible area does, so the header and the composer move with it.
    await page.evaluate((height) => window.__viewport({ height, offsetTop: 40 }), visible);
    await expect.poll(async () => (await rect(composer)).bottom).toBeCloseTo(visible + 40, 0);
    expect((await rect(header)).top).toBeGreaterThanOrEqual(40);
    await page.evaluate((height) => window.__viewport({ height }), visible);
    await expect.poll(async () => (await rect(composer)).bottom).toBeCloseTo(visible, 0);

    // KBD-03: a message sent from the Send button keeps focus in the text box, press included.
    await box.focus();
    await page.evaluate(() => {
      window.__blurs = 0;
      document
        .querySelector('textarea[aria-label^="Message e2e-keyboard"]')
        ?.addEventListener("blur", () => {
          window.__blurs += 1;
        });
    });
    await box.fill("one more, from the phone");
    await send.click();
    await expect(log.getByText("one more, from the phone", { exact: true })).toBeVisible();
    await expect(box).toBeFocused();
    expect(await page.evaluate(() => window.__blurs), "focus left the text box").toBe(0);
    await expect(box).toHaveValue("");
    // The new message is the last line above the composer; the composer did not move.
    const sent = log.getByText("one more, from the phone", { exact: true });
    await expect.poll(async () => (await rect(sent)).bottom).toBeLessThanOrEqual(visible);
    await expect.poll(() => distanceFromNewest(page)).toBeLessThanOrEqual(64);
    expect((await rect(composer)).bottom).toBeCloseTo(visible, 0);

    // KBD-04: a long message grows the text box to a cap and scrolls inside it; attach and Send
    // stay on screen, above the keyboard.
    await box.fill(
      Array.from({ length: 30 }, (_, i) => `line ${i + 1} of a long message`).join("\n"),
    );
    const capped = await rect(box);
    expect(capped.height).toBeLessThanOrEqual(visible * 0.4 + 1);
    expect(capped.height).toBeLessThanOrEqual(160 + 1);
    expect(await box.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true);
    for (const control of [send, page.getByLabel("Attach files").locator("..")]) {
      expect((await rect(control)).bottom).toBeLessThanOrEqual(visible);
      expect((await rect(control)).top).toBeGreaterThan((await rect(header)).bottom);
    }
    expect((await rect(composer)).bottom).toBeCloseTo(visible, 0);
    await box.fill("");

    // KBD-06: scrolled away from the newest message, Jump to latest has a strip of its own; it
    // touches neither the messages above it nor the composer below.
    await log.evaluate((el) => {
      el.scrollTop = 0;
    });
    const jump = page.getByRole("button", { name: "Jump to latest" });
    await expect(jump).toBeVisible();
    await settled(page);
    const [pill, messages, form] = [await rect(jump), await rect(log), await rect(composer)];
    expect(pill.top).toBeGreaterThanOrEqual(messages.bottom - 0.5);
    expect(pill.bottom).toBeLessThanOrEqual(form.top + 0.5);
    await jump.click();
    await expect(jump).toHaveCount(0);
    await expect.poll(() => distanceFromNewest(page)).toBeLessThanOrEqual(64);

    // The keyboard closes: the composer is back on the bottom edge, the newest message above it.
    await page.evaluate(() => window.__viewport({ height: null }));
    await expect.poll(async () => (await rect(composer)).bottom).toBeCloseTo(screen.height, 0);
    await expect.poll(() => distanceFromNewest(page)).toBeLessThanOrEqual(64);

    // A pinch-zoomed page is no keyboard: the layout stays as it was.
    await page.evaluate(() => window.__viewport({ height: 390, scale: 2 }));
    await page.waitForTimeout(100);
    expect((await rect(composer)).bottom).toBeCloseTo(screen.height, 0);
    await page.evaluate(() => window.__viewport({ height: null }));
  } finally {
    await context.close();
  }
});

test("a browser that resizes the page for the keyboard is followed too: Chrome with interactive-widget (KBD-01, KBD-02, KBD-05)", async ({
  browser,
  phone,
}, testInfo) => {
  const { context, page } = await openPage(browser, phone, testInfo);
  try {
    const room = await newGroup(
      context,
      phone,
      "e2e-resize",
      Array.from({ length: 40 }, (_, i) => `resize ${i + 1}`),
    );
    await page.goto(`/rooms/${room.id}`);
    const log = page.getByRole("log");
    const box = page.getByRole("textbox", { name: "Message e2e-resize" });
    const composer = box.locator("xpath=ancestor::form");
    const screen = page.viewportSize() ?? { width: 0, height: 0 };
    await expect(log.getByText("resize 40", { exact: true })).toBeInViewport();

    // The layout viewport itself shrinks, as it does in Chrome with `interactive-widget=resizes-content`.
    const height = screen.height - 420;
    await page.setViewportSize({ width: screen.width, height });
    await expect.poll(async () => (await rect(composer)).bottom).toBeCloseTo(height, 0);
    await expect(log.getByText("resize 40", { exact: true })).toBeInViewport();
    await expect.poll(() => distanceFromNewest(page)).toBeLessThanOrEqual(64);

    await page.setViewportSize(screen);
    await expect.poll(async () => (await rect(composer)).bottom).toBeCloseTo(screen.height, 0);
    await expect.poll(() => distanceFromNewest(page)).toBeLessThanOrEqual(64);

    // Someone reading higher up is left where they are when the box changes height.
    await log.evaluate((el) => {
      el.scrollTop = 0;
    });
    await expect(log.getByText("resize 1", { exact: true })).toBeInViewport();
    await page.setViewportSize({ width: screen.width, height });
    await page.waitForTimeout(200);
    await expect(log.getByText("resize 1", { exact: true })).toBeInViewport();
    expect(await distanceFromNewest(page)).toBeGreaterThan(200);
  } finally {
    await context.close();
  }
});
