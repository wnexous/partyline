import type { Browser, BrowserContext, Locator, Page, TestInfo } from "@playwright/test";
import type { Me, Message, Page as Paged, Room, SetupRequest } from "../src/lib/api/types.gen";
import { makeWav, postVoice } from "./audio";
import { apiCall, test as base, type Credentials, expect, settled } from "./fixtures";
import { expectNoSidewaysScroll, isMobile, loginAs } from "./helpers";
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
    width: box.width,
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
    /** What the stand-in microphone was asked for and told (see `microphoneStandIn`). */
    __mic: { requests: number; stopped: number; types: string[] };
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

/** A tap on a phone, a click elsewhere. */
const press = (testInfo: TestInfo, target: Locator) =>
  isMobile(testInfo) ? target.tap() : target.click();

/** The middle of an element, where a finger lands on it. */
async function centre(locator: Locator) {
  const box = await rect(locator);
  return { x: (box.left + box.right) / 2, y: (box.top + box.bottom) / 2 };
}

/**
 * A finger on a phone (touch events through the browser's own input pipeline, so the page sees the
 * pointer events and the capture a real finger makes), a mouse button elsewhere: the same three
 * moves for a hold, a slide and a release.
 */
async function pointer(page: Page, context: BrowserContext, testInfo: TestInfo) {
  if (!isMobile(testInfo)) {
    return {
      down: async (x: number, y: number) => {
        await page.mouse.move(x, y);
        await page.mouse.down();
      },
      move: (x: number, y: number) => page.mouse.move(x, y, { steps: 6 }),
      up: () => page.mouse.up(),
    };
  }
  const cdp = await context.newCDPSession(page);
  const touch = (type: "touchStart" | "touchMove" | "touchEnd", x?: number, y?: number) =>
    cdp.send("Input.dispatchTouchEvent", {
      type,
      touchPoints: x === undefined || y === undefined ? [] : [{ x, y }],
    });
  return {
    down: (x: number, y: number) => touch("touchStart", x, y),
    move: (x: number, y: number) => touch("touchMove", x, y),
    up: () => touch("touchEnd"),
  };
}

/**
 * A headless browser has no microphone and records nothing. This puts a stand-in in front of
 * `getUserMedia` and `MediaRecorder`: asking for the microphone gives a stream whose tracks count
 * their `stop()`, or fails with the error named in `deny`; a recorder hands over, when it stops,
 * 8 seconds of a tone (a WAV, labelled with the type it was asked for: a stand-in has no encoder).
 * With `absent` the browser has no `MediaRecorder` at all. The page's own code is untouched.
 */
async function microphoneStandIn(page: Page, options: { deny?: string; absent?: boolean } = {}) {
  await page.addInitScript(
    ({ wav, deny, absent }) => {
      if (absent) {
        Object.defineProperty(window, "MediaRecorder", { configurable: true, value: undefined });
        return;
      }
      const mic = { requests: 0, stopped: 0, types: [] as string[] };
      window.__mic = mic;
      const track = {
        stop: () => {
          mic.stopped += 1;
        },
      };
      const stream = { getTracks: () => [track], getAudioTracks: () => [track] };
      Object.defineProperty(navigator.mediaDevices, "getUserMedia", {
        configurable: true,
        value: async () => {
          mic.requests += 1;
          if (deny) throw new DOMException("The user denied the microphone", deny);
          return stream;
        },
      });
      const bytes = Uint8Array.from(atob(wav), (c) => c.charCodeAt(0));
      class StandInRecorder {
        static isTypeSupported = (type: string) => type === "audio/webm;codecs=opus";
        state = "inactive";
        mimeType: string;
        ondataavailable: ((event: { data: Blob }) => void) | null = null;
        onstop: (() => void) | null = null;
        onerror: (() => void) | null = null;
        constructor(_stream: unknown, init: { mimeType?: string } = {}) {
          this.mimeType = init.mimeType ?? "audio/webm;codecs=opus";
          mic.types.push(this.mimeType);
        }
        start() {
          this.state = "recording";
        }
        stop() {
          if (this.state === "inactive") return;
          this.state = "inactive";
          setTimeout(() => {
            this.ondataavailable?.({ data: new Blob([bytes], { type: this.mimeType }) });
            this.onstop?.();
          }, 0);
        }
      }
      Object.defineProperty(window, "MediaRecorder", {
        configurable: true,
        writable: true,
        value: StandInRecorder,
      });
    },
    {
      wav: makeWav(8).toString("base64"),
      deny: options.deny ?? null,
      absent: options.absent ?? false,
    },
  );
}

/** The voice messages of a room, oldest first, as the API has them. */
async function voiceMessages(context: BrowserContext, instance: Instance, roomId: string) {
  const sent = await apiCall<Paged<Message>>(
    context.request,
    instance,
    "GET",
    `/rooms/${roomId}/messages?limit=50`,
  );
  return sent.items
    .filter((m) => m.attachments.some((a) => a.mime.startsWith("audio/")))
    .sort((a, b) => a.seq - b.seq);
}

test("a voice message is recorded, sent and played back in the timeline (VOI-01, VOI-02, VOI-04, VOI-07, VOI-08)", async ({
  browser,
  phone,
}, testInfo) => {
  const { context, page } = await openPage(browser, phone, testInfo);
  try {
    await microphoneStandIn(page);
    const room = await newGroup(context, phone, "e2e-voice", ["a word first", "and another"]);
    await page.goto(`/rooms/${room.id}`);
    const log = page.getByRole("log");
    const box = page.getByRole("textbox", { name: "Message e2e-voice" });
    const record = page.getByRole("button", { name: "Record a voice message" });
    const players = log.getByRole("group", { name: "Voice message" });

    // VOI-01, VOI-02: the microphone is in the composer; a tap records, and the bar that counts
    // the time and offers Cancel and Send takes the text box's place.
    await expect(record).toBeVisible();
    await press(testInfo, record);
    await expect(page.getByText("Recording", { exact: true })).toBeVisible();
    await expect(box).toBeHidden();
    await expect(page.getByRole("button", { name: "Cancel recording" })).toBeVisible();
    await expect(page.getByRole("timer")).toHaveText(/^0:0[1-9]$/);
    expect(await page.evaluate(() => window.__mic.requests)).toBe(1);
    await shoot(page, "voice-recording", testInfo);

    // VOI-04: Send stops the recording and posts it as one audio attachment, in the format the
    // recorder was asked for, with no text; the microphone is let go and the box is back.
    await press(testInfo, page.getByRole("button", { name: "Send voice message" }));
    await expect(players).toHaveCount(1);
    await expect(page.getByText("Recording", { exact: true })).toHaveCount(0);
    await expect(box).toBeVisible();
    expect(await page.evaluate(() => window.__mic.stopped)).toBe(1);
    expect(await page.evaluate(() => window.__mic.types)).toEqual(["audio/webm;codecs=opus"]);
    const [sent] = await voiceMessages(context, phone, room.id);
    expect(sent?.text).toBe("");
    expect(sent?.attachments).toHaveLength(1);
    expect(sent?.attachments[0]).toMatchObject({
      mime: "audio/webm;codecs=opus",
      status: "stored",
    });
    expect(sent?.attachments[0]?.name).toMatch(/^voice-message-\d{4}-\d{2}-\d{2}-\d{6}\.webm$/);

    // VOI-07, VOI-08: the player knows how long the recording is, plays it, and its targets are
    // finger-sized on a phone.
    const mine = players.first();
    const play = mine.getByRole("button", { name: "Play" });
    const slider = mine.getByRole("slider", { name: "Seek" });
    await expect(mine.getByText("0:00 / 0:08")).toBeVisible();
    if (isMobile(testInfo)) {
      expect((await rect(play)).width).toBeGreaterThanOrEqual(44);
      for (const control of [play, slider]) {
        expect((await rect(control)).height).toBeGreaterThanOrEqual(44);
      }
    }
    await expectNoSidewaysScroll(page, "a conversation with a voice message");
    await press(testInfo, play);
    await expect(mine.getByRole("button", { name: "Pause" })).toBeVisible();
    await expect(mine.getByText(/^0:0[1-4] \/ 0:08$/)).toBeVisible();
    await press(testInfo, mine.getByRole("button", { name: "Pause" }));
    await expect(play).toBeVisible();

    // The seek bar is a slider that says where it is, and the arrow keys move the recording.
    await slider.focus();
    const before = Number(await slider.inputValue());
    await page.keyboard.press("ArrowRight");
    await expect.poll(async () => Number(await slider.inputValue())).toBe(before + 1);
    await expect(slider).toHaveAttribute("aria-valuetext", `0:0${before + 1} of 0:08`);
    const heard = await mine.locator("audio").evaluate((el: HTMLAudioElement) => el.currentTime);
    expect(heard).toBeCloseTo(before + 1, 0);

    // VOI-07: starting another recording pauses this one.
    await postVoice(context.request, phone, room.id, { seconds: 40, stamp: "2026-10-24-140509" });
    await expect(players).toHaveCount(2);
    const other = players.nth(1);
    await press(testInfo, play);
    await expect(mine.getByRole("button", { name: "Pause" })).toBeVisible();
    await press(testInfo, other.getByRole("button", { name: "Play" }));
    await expect(other.getByRole("button", { name: "Pause" })).toBeVisible();
    await expect(play).toBeVisible();

    // The picture: one player at rest, one part-way through.
    await press(testInfo, other.getByRole("button", { name: "Pause" }));
    const otherSlider = other.getByRole("slider", { name: "Seek" });
    await otherSlider.focus();
    for (let step = 0; step < 18; step += 1) await page.keyboard.press("ArrowRight");
    await expect(otherSlider).toHaveAttribute("aria-valuetext", /^0:(1[89]|2\d) of 0:40$/);
    await mine.getByRole("button", { name: "Play" }).hover();
    await shoot(page, "voice-player", testInfo);
  } finally {
    await context.close();
  }
});

test("a refused microphone is said in plain words and leaves no recording bar behind (VOI-06)", async ({
  browser,
  phone,
}, testInfo) => {
  const { context, page } = await openPage(browser, phone, testInfo);
  try {
    await microphoneStandIn(page, { deny: "NotAllowedError" });
    const room = await newGroup(context, phone, "e2e-mic-denied", ["nobody is listening"]);
    await page.goto(`/rooms/${room.id}`);
    const box = page.getByRole("textbox", { name: "Message e2e-mic-denied" });
    const record = page.getByRole("button", { name: "Record a voice message" });

    await press(testInfo, record);
    const alert = page.getByRole("alert").filter({ hasText: "Microphone access is blocked" });
    await expect(alert).toBeVisible();
    await expect(alert).toContainText("Allow it for this site in your browser settings");
    await expect(page.getByText("Recording", { exact: true })).toHaveCount(0);
    await expect(box).toBeVisible();
    await expect(record).toBeEnabled();
    expect(await page.evaluate(() => window.__mic.requests)).toBe(1);
    await shoot(page, "voice-denied", testInfo);
    // Typing goes on as before, and the message goes away with the next try at the text.
    await box.fill("then I will type");
    await page.getByRole("button", { name: "Send" }).click();
    await expect(log(page).getByText("then I will type", { exact: true })).toBeVisible();
  } finally {
    await context.close();
  }
});

test("no microphone button is shown where the browser cannot record (VOI-01)", async ({
  browser,
  phone,
}, testInfo) => {
  const { context, page } = await openPage(browser, phone, testInfo);
  try {
    await microphoneStandIn(page, { absent: true });
    const room = await newGroup(context, phone, "e2e-no-recorder", ["text only"]);
    await page.goto(`/rooms/${room.id}`);
    await expect(page.getByRole("textbox", { name: "Message e2e-no-recorder" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Send" })).toBeVisible();
    await expect(page.getByLabel("Attach files")).toBeAttached();
    await expect(page.getByRole("button", { name: "Record a voice message" })).toHaveCount(0);
  } finally {
    await context.close();
  }
});

test("a hold records while held, sends on release and cancels when the finger slides away (VOI-03, VOI-05)", async ({
  browser,
  phone,
}, testInfo) => {
  const { context, page } = await openPage(browser, phone, testInfo);
  try {
    await microphoneStandIn(page);
    const room = await newGroup(context, phone, "e2e-hold", ["hold the microphone"]);
    await page.goto(`/rooms/${room.id}`);
    const record = page.getByRole("button", { name: "Record a voice message" });
    const players = log(page).getByRole("group", { name: "Voice message" });
    await expect(record).toBeVisible();
    const finger = await pointer(page, context, testInfo);
    const { x, y } = await centre(record);

    // Held for more than half a second: it records, says how to cancel, and sends on release.
    await finger.down(x, y);
    await expect(page.getByText("Recording", { exact: true })).toBeVisible();
    await expect(page.getByText("Slide left to cancel")).toBeVisible();
    await finger.up();
    await expect(players).toHaveCount(1);
    await expect(page.getByText("Recording", { exact: true })).toHaveCount(0);
    expect(await page.evaluate(() => window.__mic.stopped)).toBe(1);

    // Held and slid 120 px to the left: it says so before the finger lets go, and nothing is sent.
    const again = await centre(record);
    await finger.down(again.x, again.y);
    await expect(page.getByText("Slide left to cancel")).toBeVisible();
    await finger.move(again.x - 120, again.y);
    await expect(page.getByText("Release to cancel")).toBeVisible();
    await finger.up();
    await expect(page.getByText("Recording", { exact: true })).toHaveCount(0);
    await expect(record).toBeVisible();
    await page.waitForTimeout(300);
    expect(await page.evaluate(() => window.__mic.stopped)).toBe(2);
    expect(await voiceMessages(context, phone, room.id)).toHaveLength(1);
    await expect(players).toHaveCount(1);
  } finally {
    await context.close();
  }
});

test("the browser's own recorder reaches the timeline, with a fake microphone (VOI-02, VOI-04, VOI-08)", async ({
  browser,
  phone,
}, testInfo) => {
  const { context, page } = await openPage(browser, phone, testInfo);
  try {
    const room = await newGroup(context, phone, "e2e-recorder", ["the real recorder"]);
    await page.goto(`/rooms/${room.id}`);
    const canRecord = await page.evaluate(
      () =>
        typeof MediaRecorder === "function" &&
        MediaRecorder.isTypeSupported("audio/webm;codecs=opus"),
    );
    test.skip(!canRecord, "this browser cannot record WebM/Opus");
    const record = page.getByRole("button", { name: "Record a voice message" });
    const player = log(page).getByRole("group", { name: "Voice message" });

    await press(testInfo, record);
    await expect(page.getByText("Recording", { exact: true })).toBeVisible();
    await expect(page.getByRole("timer")).toHaveText(/^0:0[1-9]$/);
    await press(testInfo, page.getByRole("button", { name: "Send voice message" }));
    await expect(player).toHaveCount(1);

    // What the recorder made went through the upload as it was, and the server kept it.
    const [sent] = await voiceMessages(context, phone, room.id);
    expect(sent?.attachments[0]?.mime).toMatch(/^audio\/(webm|ogg)/);
    expect(sent?.attachments[0]?.size).toBeGreaterThan(500);
    expect(sent?.attachments[0]?.status).toBe("stored");
    // The player works out the length of a recording the container does not carry (VOI-08).
    await expect(player.getByText(/ \/ 0:0[1-5]$/)).toBeVisible();
    await press(testInfo, player.getByRole("button", { name: "Play" }));
    await expect
      .poll(() => player.locator("audio").evaluate((el: HTMLAudioElement) => el.currentTime > 0))
      .toBe(true);
  } finally {
    await context.close();
  }
});

/** The conversation's message log. */
const log = (page: Page) => page.getByRole("log");
