import type { Browser, BrowserContext, Locator, Page, TestInfo } from "@playwright/test";
import type {
  AddMemberRequest,
  Conversation,
  CreatedInvite,
  CreateInvitationRequest,
  Invitation,
  List,
  Me,
  Message,
  Organization,
  Page as Paged,
  Room,
  SetupRequest,
} from "../src/lib/api/types.gen";
import { makeWav, postVoice } from "./audio";
import { apiCall, test as base, type Credentials, expect, MEMBER, settled } from "./fixtures";
import {
  agentJSON,
  connectAI,
  expectNoSidewaysScroll,
  isMobile,
  loginAs,
  trackLive,
} from "./helpers";
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

/** A conversation the viewer has not read: an AI that is a member of a new group says something in it. */
async function groupWithUnread(
  context: BrowserContext,
  instance: Instance,
  name: string,
  folder: string,
  said: string,
) {
  const ai = await connectAI(context, instance, folder);
  const room = await newGroup(context, instance, name, ["welcome"]);
  await apiCall(context.request, instance, "POST", `/rooms/${room.id}/members`, {
    kind: "agent",
    member_id: ai.ai.id,
  });
  await agentJSON(context, instance, ai, "POST", `/agent/rooms/${room.id}/messages`, {
    text: said,
  });
  return { room, ai };
}

/** The viewer's conversations, as the API lists them. */
const conversations = (context: BrowserContext, instance: Instance) =>
  apiCall<List<Conversation>>(context.request, instance, "GET", "/conversations").then(
    (list) => list.items,
  );

/**
 * What opens the actions of an element: a finger resting on it on a phone, the right button of a
 * mouse elsewhere. It returns once the sheet or the menu named `name` is up; the finger stays down
 * until `release()`, as it does for a person who has not yet lifted it.
 */
async function openActions(
  page: Page,
  context: BrowserContext,
  testInfo: TestInfo,
  target: Locator,
  name: string | RegExp,
) {
  const { x, y } = await centre(target);
  if (isMobile(testInfo)) {
    const finger = await pointer(page, context, testInfo);
    await finger.down(x, y);
    await expect(surface(page, testInfo, name)).toBeVisible();
    return {
      release: async () => {
        await finger.up();
      },
      x,
      y,
    };
  }
  await page.mouse.click(x, y, { button: "right" });
  await expect(surface(page, testInfo, name)).toBeVisible();
  return { release: async () => {}, x, y };
}

/** The sheet of a phone or the menu of a mouse that was just opened, named after what it acts on. */
const surface = (page: Page, testInfo: TestInfo, name: string | RegExp) =>
  isMobile(testInfo) ? page.getByRole("dialog", { name }) : page.getByRole("menu", { name });

/** One of its actions: a button in the sheet, an item in the menu. */
const action = (page: Page, testInfo: TestInfo, name: string) =>
  isMobile(testInfo)
    ? page.getByRole("dialog").getByRole("button", { name, exact: true })
    : page.getByRole("menu").getByRole("menuitem", { name, exact: true });

test("a conversation of the list opens its actions, and each one does what it says (ACT-01..ACT-04, ACT-06)", async ({
  browser,
  phone,
}, testInfo) => {
  const { context, page } = await openPage(browser, phone, testInfo);
  try {
    const crew = await groupWithUnread(
      context,
      phone,
      "e2e-crew",
      "/work/e2e-actions",
      "release is green",
    );
    const doomed = await newGroup(context, phone, "e2e-doomed", ["nothing to see here"]);
    await page.goto("/");
    const list = page.getByRole("list", { name: "Conversations" });
    const row = (name: string) => list.getByRole("link", { name: new RegExp(name) });
    await expect(row("e2e-crew")).toBeVisible();
    await expect(row("e2e-crew").getByText("1 unread message")).toBeVisible();

    if (isMobile(testInfo)) {
      // ACT-01: a drag that begins on a row is a scroll, not a press; and a tap opens the conversation.
      const finger = await pointer(page, context, testInfo);
      const at = await centre(row("e2e-doomed"));
      await finger.down(at.x, at.y);
      await finger.move(at.x, at.y + 40);
      await page.waitForTimeout(700);
      await finger.up();
      await expect(page.getByRole("dialog")).toHaveCount(0);
    }

    // ACT-01, ACT-02: the actions of a group that has something unread and that the viewer manages.
    const press = await openActions(page, context, testInfo, row("e2e-crew"), "e2e-crew");
    const actions = surface(page, testInfo, "e2e-crew");
    await expect(actions).toBeVisible();
    await expect(action(page, testInfo, "Mark as read")).toBeVisible();
    await expect(action(page, testInfo, "Delete group")).toBeVisible();
    // There is no Leave group: the server's rules give it no effect (see conversation-menu.tsx).
    await expect(page.getByText("Leave group")).toHaveCount(0);
    if (isMobile(testInfo)) {
      // The sheet comes up from the bottom edge, as wide as the screen, with rows a finger can hit.
      const sheet = await rect(actions);
      const screen = page.viewportSize() ?? { width: 0, height: 0 };
      expect(sheet.bottom).toBeCloseTo(screen.height, 0);
      expect(sheet.width).toBeCloseTo(screen.width, 0);
      for (const name of ["Mark as read", "Delete group"]) {
        expect((await rect(action(page, testInfo, name))).height).toBeGreaterThanOrEqual(44);
      }
      await expect(actions).toHaveAccessibleDescription("Group · 1 unread message");
      await shoot(page, "actions-sheet", testInfo);
    } else {
      // A menu at the pointer: its corner is where the button was pressed.
      const menu = await rect(actions);
      expect(Math.abs(menu.left - press.x)).toBeLessThanOrEqual(8);
      expect(Math.abs(menu.top - press.y)).toBeLessThanOrEqual(8);
      await shoot(page, "actions-menu", testInfo);
    }
    await press.release();
    // The press that opened it did not open the conversation.
    await expect(page).toHaveURL(`${phone.url}/`);

    // Escape closes it and focus goes back to the row.
    await page.keyboard.press("Escape");
    await expect(surface(page, testInfo, "e2e-crew")).toHaveCount(0);
    if (!isMobile(testInfo)) {
      await expect(row("e2e-crew")).toBeFocused();
      // With a mouse the "⋯" shows where the time was while the row is hovered, and only then.
      const more = page.getByRole("button", { name: "Actions for e2e-crew" });
      // At rest: the pointer is away and the row has let go of focus (focus within shows it too).
      await page.mouse.move(2, 2);
      await row("e2e-crew").blur();
      await expect(more).toHaveCSS("opacity", "0");
      await row("e2e-crew").hover();
      await expect(more).toHaveCSS("opacity", "1");
      await shoot(page, "actions-hover", testInfo);
      await page.mouse.move(2, 2);
    }

    // Mark as read: the count goes, on the screen and on the server.
    await (await openActions(page, context, testInfo, row("e2e-crew"), "e2e-crew")).release();
    await action(page, testInfo, "Mark as read").click();
    // The row is there, and says nothing is unread; the server agrees.
    await expect(row("e2e-crew")).toBeVisible();
    await expect(row("e2e-crew").getByText(/unread/)).toHaveCount(0);
    await expect
      .poll(
        async () =>
          (await conversations(context, phone)).find((c) => c.id === crew.room.id)?.unread,
      )
      .toBe(0);

    // ACT-04: Delete group asks first, with Cancel focused; cancelling leaves everything.
    await (await openActions(page, context, testInfo, row("e2e-doomed"), "e2e-doomed")).release();
    await action(page, testInfo, "Delete group").click();
    const ask = page.getByRole("alertdialog", { name: "Delete e2e-doomed?" });
    await expect(ask).toBeVisible();
    await expect(ask.getByRole("button", { name: "Cancel" })).toBeFocused();
    await ask.getByRole("button", { name: "Cancel" }).click();
    await expect(ask).toHaveCount(0);
    expect((await conversations(context, phone)).map((c) => c.id)).toContain(doomed.id);
    // Confirmed: it goes, and the user is told.
    await (await openActions(page, context, testInfo, row("e2e-doomed"), "e2e-doomed")).release();
    await action(page, testInfo, "Delete group").click();
    await page
      .getByRole("alertdialog", { name: "Delete e2e-doomed?" })
      .getByRole("button", { name: "Delete group" })
      .click();
    await expect(row("e2e-doomed")).toHaveCount(0);
    await expect(page.getByText("e2e-doomed deleted")).toBeVisible();
    expect((await conversations(context, phone)).map((c) => c.id)).not.toContain(doomed.id);
  } finally {
    await context.close();
  }
});

test("a tap on a conversation still opens it, and the click that ends a long press does not (ACT-01)", async ({
  browser,
  phone,
}, testInfo) => {
  test.skip(!isMobile(testInfo), "a phone's gesture");
  const { context, page } = await openPage(browser, phone, testInfo);
  try {
    await newGroup(context, phone, "e2e-tap", ["tap me"]);
    await page.goto("/");
    const row = page
      .getByRole("list", { name: "Conversations" })
      .getByRole("link", { name: /e2e-tap/ });
    await expect(row).toBeVisible();
    // Held for a good while and let go: the sheet is up and the conversation is not open.
    const held = await openActions(page, context, testInfo, row, "e2e-tap");
    await expect(page.getByRole("dialog", { name: "e2e-tap" })).toBeVisible();
    await page.waitForTimeout(700);
    await held.release();
    await expect(page).toHaveURL(`${phone.url}/`);
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog")).toHaveCount(0);
    // A plain tap opens it.
    await row.tap();
    await expect(page).toHaveURL(/\/rooms\/[^/]+$/);
  } finally {
    await context.close();
  }
});

test("a direct message offers no Delete, and View profile opens the profile on the page that is open (ACT-03)", async ({
  browser,
  phone,
}, testInfo) => {
  const { context, page } = await openPage(browser, phone, testInfo);
  try {
    // A DM is nobody's to delete (DM-02): it offers its unread count and the profile of who it is with.
    const ai = await connectAI(context, phone, "/work/e2e-dm");
    const dm = await apiCall<Room>(context.request, phone, "POST", "/conversations/dm", {
      with: ai.ai.id,
    });
    await agentJSON(context, phone, ai, "POST", `/agent/rooms/${dm.id}/messages`, {
      text: "psst, over here",
    });
    await page.goto("/");
    const row = page
      .getByRole("list", { name: "Conversations" })
      .getByRole("link", { name: /psst, over here/ });
    await expect(row).toBeVisible();
    const press = await openActions(page, context, testInfo, row, ai.ai.display_name);
    const actions = surface(page, testInfo, ai.ai.display_name);
    await expect(actions).toBeVisible();
    await expect(action(page, testInfo, "Mark as read")).toBeVisible();
    await expect(action(page, testInfo, "View profile")).toBeVisible();
    await expect(page.getByText("Delete group")).toHaveCount(0);
    await press.release();
    // View profile opens the panel of the AI, on the page that is open.
    await action(page, testInfo, "View profile").click();
    await expect(page.getByRole("dialog", { name: ai.ai.display_name })).toBeVisible();
    await expect(page).toHaveURL(`${phone.url}/`);
  } finally {
    await context.close();
  }
});

test("a message opens its actions: Copy text copies it, Reply answers it (ACT-01, ACT-02, ACT-05, ACT-07)", async ({
  browser,
  phone,
}, testInfo) => {
  const { context, page } = await openPage(browser, phone, testInfo);
  try {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    const said = "release is green, ship it";
    const { room, ai } = await groupWithUnread(
      context,
      phone,
      "e2e-answer",
      "/work/e2e-answer",
      said,
    );
    const author = ai.ai.display_name;
    await page.goto(`/rooms/${room.id}`);
    const log = page.getByRole("log");
    // The row, by its number: once it is answered, the answer quotes it and so says it too.
    const first = log.locator('[data-testid^="message-"]', { hasText: said });
    await expect(first).toBeVisible();
    const message = log.getByTestId((await first.getAttribute("data-testid")) ?? "");
    const box = page.getByRole("textbox", { name: "Message e2e-answer" });
    // The sheet is named after the author; the mouse's menu says whose message it is.
    const named = new RegExp(author);

    // ACT-05: Reply and Copy text, and a line of what the message says.
    const press = await openActions(page, context, testInfo, message, named);
    await expect(action(page, testInfo, "Reply")).toBeVisible();
    await expect(action(page, testInfo, "Copy text")).toBeVisible();
    if (isMobile(testInfo)) {
      await expect(surface(page, testInfo, named)).toHaveAccessibleDescription(said);
      for (const name of ["Reply", "Copy text"]) {
        expect((await rect(action(page, testInfo, name))).height).toBeGreaterThanOrEqual(44);
      }
      await shoot(page, "actions-message-sheet", testInfo);
    }
    await press.release();

    // Copy text: the whole text is on the clipboard and the user is told.
    await action(page, testInfo, "Copy text").click();
    await expect(page.getByText("Text copied")).toBeVisible();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(said);

    // ACT-02: with a mouse the "⋯" of the row opens the same menu; it shows on hover.
    let reply: () => Promise<void>;
    if (isMobile(testInfo)) {
      reply = async () => {
        await (await openActions(page, context, testInfo, message, named)).release();
        await action(page, testInfo, "Reply").click();
      };
    } else {
      const more = message.getByRole("button", { name: `Actions for the message from ${author}` });
      // Closing the menu gives the focus back to the row's "⋯" (a focused control shows) and the
      // pointer may still rest on the row: start from neither.
      // The menu hands the focus back a moment after it closes, so let go of it until it stays away.
      await page.mouse.move(0, 0);
      await expect
        .poll(async () => {
          await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
          return more.evaluate((el) => getComputedStyle(el).opacity);
        })
        .toBe("0");
      await message.hover();
      await expect(more).toHaveCSS("opacity", "1");
      await shoot(page, "actions-message-hover", testInfo);
      reply = async () => {
        await message.hover();
        await expect(more).toHaveCSS("opacity", "1");
        await more.click();
        await action(page, testInfo, "Reply").click();
      };
    }

    // ACT-07: Reply puts the message above the text box, with the cursor in it.
    await reply();
    const strip = page.getByTestId("reply-strip");
    await expect(strip).toContainText(`Replying to ${author}`);
    await expect(strip).toContainText(said);
    await expect(box).toBeFocused();
    await shoot(page, "reply-strip", testInfo);

    // The answer carries it, and the strip goes.
    await box.fill("shipping it now");
    await page.getByRole("button", { name: "Send" }).click();
    await expect(strip).toHaveCount(0);
    const answer = log.locator('[data-testid^="message-"]', { hasText: "shipping it now" });
    await expect(answer.getByTestId("reply-context")).toContainText(said);
    const sent = await apiCall<Paged<Message>>(
      context.request,
      phone,
      "GET",
      `/rooms/${room.id}/messages?limit=5`,
    );
    const original = sent.items.find((m) => m.text === said);
    expect(sent.items.find((m) => m.text === "shipping it now")?.reply_to).toBe(original?.id);

    // Taking a reply back: Cancel reply, and Escape in the text box.
    await reply();
    await expect(strip).toBeVisible();
    await page.getByRole("button", { name: "Cancel reply" }).click();
    await expect(strip).toHaveCount(0);
    await reply();
    await expect(strip).toBeVisible();
    await box.press("Escape");
    await expect(strip).toHaveCount(0);
  } finally {
    await context.close();
  }
});

test("a message is answered with a swipe to the right; a vertical drag is left to the browser (SWP-01..SWP-04)", async ({
  browser,
  phone,
}, testInfo) => {
  test.skip(!isMobile(testInfo), "a phone's gesture");
  const { context, page } = await openPage(browser, phone, testInfo);
  try {
    // A conversation long enough to scroll, ending in what an AI said.
    const room = await newGroup(
      context,
      phone,
      "e2e-swipe",
      Array.from({ length: 30 }, (_, i) => `filler ${i + 1}`),
    );
    const ai = await connectAI(context, phone, "/work/e2e-swipe");
    await apiCall(context.request, phone, "POST", `/rooms/${room.id}/members`, {
      kind: "agent",
      member_id: ai.ai.id,
    });
    await agentJSON(context, phone, ai, "POST", `/agent/rooms/${room.id}/messages`, {
      text: "the release is green",
    });
    await page.goto(`/rooms/${room.id}`);
    const log = page.getByRole("log");
    const said = log.locator('[data-testid^="message-"]', { hasText: "the release is green" });
    await expect(said).toBeInViewport();
    const strip = page.getByTestId("reply-strip");
    const finger = await pointer(page, context, testInfo);
    const transform = () => said.evaluate((el) => el.style.transform);
    const restingLeft = (await rect(said)).left;

    // SWP-01: the row follows the finger and the reply icon appears; past 60 px it is armed.
    const at = await centre(said);
    await finger.down(at.x, at.y);
    await finger.move(at.x + 50, at.y);
    await expect.poll(transform).toBe("translateX(50px)");
    expect((await rect(said)).left).toBeCloseTo(restingLeft + 50, 0);
    await expect(said).not.toHaveAttribute("data-armed", "true");
    await shoot(page, "swipe-reply", testInfo);
    await finger.move(at.x + 90, at.y);
    await expect(said).toHaveAttribute("data-armed", "true");
    await shoot(page, "swipe-reply-armed", testInfo);
    await finger.up();
    // It answers: the strip is up with the cursor in the text box, and the row is home again.
    await expect(strip).toContainText("the release is green");
    await expect(page.getByRole("textbox", { name: "Message e2e-swipe" })).toBeFocused();
    await expect.poll(transform).toBe("");
    await expect.poll(async () => (await rect(said)).left).toBeCloseTo(restingLeft, 0);
    await page.getByRole("button", { name: "Cancel reply" }).click();
    await expect(strip).toHaveCount(0);

    // SWP-02: a release before 60 px springs back and answers nothing.
    await finger.down(at.x, at.y);
    await finger.move(at.x + 35, at.y);
    await expect.poll(transform).toBe("translateX(35px)");
    await finger.up();
    await expect.poll(transform).toBe("");
    await expect(strip).toHaveCount(0);

    // SWP-02: the row leaves vertical panning to the browser, which is what makes a drag up or down
    // the log's scroll (a headless browser does not pan on touch, so what is checked is the
    // contract: the row's `touch-action`); and a mostly vertical drag moves nothing and answers nothing.
    expect(await said.evaluate((el) => getComputedStyle(el).touchAction)).toBe("pan-y pinch-zoom");
    await finger.down(at.x, at.y);
    for (let step = 1; step <= 12; step += 1) {
      await finger.move(at.x + step / 2, at.y - step * 18);
      await page.waitForTimeout(16);
    }
    expect(await transform()).toBe("");
    await expect(said).not.toHaveAttribute("data-armed", "true");
    await finger.up();
    await expect(strip).toHaveCount(0);

    // SWP-02: a drag that began across is a swipe to its end, whatever the finger does next: the
    // row stays where the finger took it, and letting go short of 60 px answers nothing.
    const again = await centre(said);
    await finger.down(again.x, again.y);
    await finger.move(again.x + 40, again.y);
    await expect.poll(transform).toBe("translateX(40px)");
    for (let step = 1; step <= 10; step += 1) {
      await finger.move(again.x + 40, again.y + step * 16);
      await page.waitForTimeout(16);
    }
    expect(await transform()).toBe("translateX(40px)");
    await finger.up();
    await expect.poll(transform).toBe("");
    await expect(strip).toHaveCount(0);
  } finally {
    await context.close();
  }
});

/**
 * A second person in the group, in a browser of their own: invited to the instance, to the group's
 * organization and to the group (AD-016), so what they do reaches the owner's open page live.
 */
async function addPerson(owner: BrowserContext, browser: Browser, instance: Instance, room: Room) {
  const invite = await apiCall<CreatedInvite>(owner.request, instance, "POST", "/admin/invites", {
    role: "member",
  });
  const token = new URL(invite.url).pathname.split("/").pop();
  const context = await browser.newContext();
  const person = await apiCall<Me>(
    context.request,
    instance,
    "POST",
    `/invites/${token}/accept`,
    MEMBER,
  );
  const invitation = await apiCall<Invitation>(
    owner.request,
    instance,
    "POST",
    `/orgs/${room.org_id}/invitations`,
    { handle: person.intelligence.handle, permission: "member" } satisfies CreateInvitationRequest,
  );
  await apiCall<Organization>(
    context.request,
    instance,
    "POST",
    `/invitations/${invitation.id}/accept`,
  );
  await apiCall(owner.request, instance, "POST", `/rooms/${room.id}/members`, {
    kind: "user",
    member_id: person.id,
  } satisfies AddMemberRequest);
  return { context, person };
}

/** The reactions the server holds for the message with this text. */
async function reactionsOn(context: BrowserContext, instance: Instance, room: Room, text: string) {
  const sent = await apiCall<Paged<Message>>(
    context.request,
    instance,
    "GET",
    `/rooms/${room.id}/messages?limit=20`,
  );
  const message = sent.items.find((m) => m.text === text);
  return { id: message?.id ?? "", reactions: message?.reactions ?? [] };
}

test("a message is reacted to from its sheet or menu; the chips follow every tap and every other person (RCT-01, RCT-02, RCT-06, RCT-08)", async ({
  browser,
  phone,
}, testInfo) => {
  const { context, page } = await openPage(browser, phone, testInfo);
  let bob: Awaited<ReturnType<typeof addPerson>> | undefined;
  try {
    const said = "the build is green";
    const { room, ai } = await groupWithUnread(
      context,
      phone,
      "e2e-react",
      "/work/e2e-react",
      said,
    );
    bob = await addPerson(context, browser, phone, room);
    const owner = await apiCall<Me>(context.request, phone, "GET", "/me");
    const author = new RegExp(ai.ai.display_name);

    await page.goto(`/rooms/${room.id}`);
    const live = trackLive(page);
    await expect.poll(() => live.ready).toBe(true);
    const message = log(page).locator('[data-testid^="message-"]', { hasText: said });
    await expect(message).toBeVisible();
    const chips = message.getByRole("list", { name: "Reactions" });
    await expect(chips).toHaveCount(0);

    // RCT-02: the bar of six leads the sheet, the menu of a mouse leads with the same six.
    const press = await openActions(page, context, testInfo, message, author);
    const bar = surface(page, testInfo, author).getByRole("group", { name: "Quick reactions" });
    await expect(bar).toBeVisible();
    const emoji = ["👍", "❤️", "😂", "😮", "😢", "🙏"];
    for (const e of emoji) {
      await expect(
        bar.getByRole(isMobile(testInfo) ? "button" : "menuitemcheckbox", {
          name: `React with ${e}`,
        }),
      ).toBeVisible();
    }
    if (isMobile(testInfo)) {
      // A finger's size, and the bar inside the sheet at 360 px.
      for (const button of await bar.getByRole("button").all()) {
        const box = await rect(button);
        expect(box.height).toBeGreaterThanOrEqual(44);
        expect(box.width).toBeGreaterThanOrEqual(40);
      }
      const sheet = await rect(page.getByRole("dialog", { name: author }));
      const row = await rect(bar);
      expect(row.left).toBeGreaterThanOrEqual(sheet.left);
      expect(row.right).toBeLessThanOrEqual(sheet.right);
    }
    await shoot(page, "reactions-bar", testInfo);
    await press.release();

    // RCT-01: a tap on an emoji reacts; the chip is under the text, mine, and the server has it.
    await bar
      .getByRole(isMobile(testInfo) ? "button" : "menuitemcheckbox", { name: "React with 👍" })
      .click();
    const mine = message.getByRole("button", { name: "👍 1 reaction, yours" });
    await expect(mine).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(page.getByRole("menu")).toHaveCount(0);
    // The chip shows before the server has answered; the server has it a moment later.
    await expect
      .poll(async () => (await reactionsOn(context, phone, room, said)).reactions)
      .toEqual([{ emoji: "👍", by: [owner.id] }]);
    const stored = await reactionsOn(context, phone, room, said);
    await shoot(page, "reactions-chip", testInfo);

    // RCT-06: another person reacts: the count follows on the open page, in place, nobody reloads.
    await apiCall(
      bob.context.request,
      phone,
      "POST",
      `/rooms/${room.id}/messages/${stored.id}/reactions`,
      { emoji: "👍" },
    );
    await apiCall(
      bob.context.request,
      phone,
      "POST",
      `/rooms/${room.id}/messages/${stored.id}/reactions`,
      { emoji: "🎉" },
    );
    await expect(
      message.getByRole("button", { name: "👍 2 reactions, including yours" }),
    ).toBeVisible();
    await expect(message.getByRole("button", { name: "🎉 1 reaction" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    await shoot(page, "reactions-chips", testInfo);

    // RCT-01: a tap on a chip of someone else's emoji joins it; on my own it takes mine back.
    await message.getByRole("button", { name: "🎉 1 reaction" }).click();
    await expect(
      message.getByRole("button", { name: "🎉 2 reactions, including yours" }),
    ).toBeVisible();
    await message.getByRole("button", { name: "👍 2 reactions, including yours" }).click();
    await expect(message.getByRole("button", { name: "👍 1 reaction" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    await expect
      .poll(async () => (await reactionsOn(context, phone, room, said)).reactions)
      .toEqual([
        { emoji: "👍", by: [bob.person.id] },
        { emoji: "🎉", by: [bob.person.id, owner.id] },
      ]);

    // RCT-02: "More" opens the rest of the emoji: in the sheet, in a picker where the menu was.
    const again = await openActions(page, context, testInfo, message, author);
    await again.release();
    const more = surface(page, testInfo, author).getByRole(
      isMobile(testInfo) ? "button" : "menuitem",
      { name: "More reactions" },
    );
    await more.click();
    const grid = page.getByRole("group", {
      name: isMobile(testInfo) ? "Pick a reaction" : "More reactions",
    });
    await expect(grid).toBeVisible();
    expect(await grid.getByRole("button").count()).toBe(48);
    // Eight to a row, inside the sheet or the picker, once it has settled into place.
    await settled(page);
    const first = await rect(grid.getByRole("button").nth(0));
    const eighth = await rect(grid.getByRole("button").nth(7));
    const ninth = await rect(grid.getByRole("button").nth(8));
    expect(eighth.top).toBeCloseTo(first.top, 0);
    expect(ninth.top).toBeGreaterThan(first.top + 20);
    if (isMobile(testInfo)) {
      const sheet = await rect(page.getByRole("dialog", { name: author }));
      expect(eighth.right).toBeLessThanOrEqual(sheet.right);
      expect(first.left).toBeGreaterThanOrEqual(sheet.left);
      expect(first.height).toBeGreaterThanOrEqual(44);
    }
    await shoot(page, "reactions-more", testInfo);
    await grid.getByRole("button", { name: "React with 🚀" }).click();
    await expect(message.getByRole("button", { name: "🚀 1 reaction, yours" })).toBeVisible();
    await expect(page.getByRole("dialog")).toHaveCount(0);

    // RCT-02: with a mouse the row carries the six too, on hover and on keyboard focus.
    if (!isMobile(testInfo)) {
      const toolbar = message.getByRole("toolbar", { name: "Quick reactions" });
      await expect(toolbar).toHaveCSS("opacity", "0");
      await message.hover();
      await expect(toolbar).toHaveCSS("opacity", "1");
      await shoot(page, "reactions-toolbar", testInfo);
      await toolbar.getByRole("button", { name: "React with 😂" }).click();
      await expect(message.getByRole("button", { name: "😂 1 reaction, yours" })).toBeVisible();
      // The picker from the toolbar keeps the toolbar on screen while it is open.
      await toolbar.getByRole("button", { name: "More reactions" }).click();
      await page.mouse.move(5, 5);
      await expect(toolbar).toHaveCSS("opacity", "1");
      await page.keyboard.press("Escape");
      await expect(page.getByRole("dialog")).toHaveCount(0);
    }
  } finally {
    await bob?.context.close();
    await context.close();
  }
});

test("a message of the viewer's own is edited and deleted from the same sheet or menu as the others are answered (ACT-05, EDT-10, DLT-09)", async ({
  browser,
  phone,
}, testInfo) => {
  const { context, page } = await openPage(browser, phone, testInfo);
  try {
    const room = await newGroup(context, phone, "e2e-own", ["helo wrld"]);
    await page.goto(`/rooms/${room.id}`);
    const message = log(page).locator('[data-testid^="message-"]', { hasText: "helo wrld" });
    await expect(message).toBeVisible();
    const box = page.getByRole("textbox", { name: "Message e2e-own" });
    const named = /Owner/;

    // ACT-05: the actions of a message of one's own, in order: Reply, Copy text, Edit, then Delete, set apart.
    const held = await openActions(page, context, testInfo, message, named);
    await held.release();
    const names = ["Reply", "Copy text", "Edit", "Delete"];
    for (const name of names) await expect(action(page, testInfo, name)).toBeVisible();
    const tops = await Promise.all(
      names.map(async (name) => (await rect(action(page, testInfo, name))).top),
    );
    expect(tops).toEqual([...tops].sort((a, b) => a - b));
    await expect(action(page, testInfo, "Delete")).toHaveClass(/text-destructive|destructive/);
    if (isMobile(testInfo)) {
      for (const name of ["Edit", "Delete"]) {
        expect((await rect(action(page, testInfo, name))).height).toBeGreaterThanOrEqual(44);
      }
    }
    await shoot(page, "actions-own-message", testInfo);

    // EDT-10: Edit loads the text into the composer, with the cursor in it; Cancel leaves and sends nothing.
    await action(page, testInfo, "Edit").click();
    await expect(page.getByText("Editing message")).toBeVisible();
    await expect(box).toHaveValue("helo wrld");
    await expect(box).toBeFocused();
    await shoot(page, "editing-message", testInfo);
    await page.getByRole("button", { name: "Cancel editing" }).click();
    await expect(page.getByText("Editing message")).toHaveCount(0);
    await expect(message).toContainText("helo wrld");

    // Saving puts the new text in the same place, "edited".
    await (await openActions(page, context, testInfo, message, named)).release();
    await action(page, testInfo, "Edit").click();
    await expect(box).toBeFocused();
    await box.fill("hello world");
    await press(testInfo, page.getByRole("button", { name: "Save changes" }));
    await expect(page.getByText("Editing message")).toHaveCount(0);
    const edited = log(page).locator('[data-testid^="message-"]', { hasText: "hello world" });
    await expect(edited.getByText("edited", { exact: true })).toBeVisible();

    // DLT-09: Delete asks first; Cancel leaves the message; the question names what is lost.
    await (await openActions(page, context, testInfo, edited, named)).release();
    await action(page, testInfo, "Delete").click();
    const question = page.getByRole("alertdialog", { name: "Delete this message?" });
    await expect(question).toContainText("deleted for everyone");
    await shoot(page, "delete-message-question", testInfo);
    await question.getByRole("button", { name: "Cancel" }).click();
    await expect(page.getByRole("alertdialog")).toHaveCount(0);
    await expect(edited).toContainText("hello world");

    // Confirming leaves the placeholder, which offers nothing: no sheet, no ⋯.
    await (await openActions(page, context, testInfo, edited, named)).release();
    await action(page, testInfo, "Delete").click();
    await page
      .getByRole("alertdialog", { name: "Delete this message?" })
      .getByRole("button", { name: "Delete" })
      .click();
    await expect(page.getByRole("alertdialog")).toHaveCount(0);
    const gone = log(page).locator('[data-testid^="message-"]', { hasText: "Message deleted" });
    await expect(gone).toBeVisible();
    await expect(gone).not.toContainText("hello world");
    await expect(gone.getByRole("button", { name: /^Actions for the message/ })).toHaveCount(0);
    const at = await centre(gone);
    if (isMobile(testInfo)) {
      const finger = await pointer(page, context, testInfo);
      await finger.down(at.x, at.y);
      await page.waitForTimeout(700);
      await finger.up();
    } else {
      await page.mouse.click(at.x, at.y, { button: "right" });
    }
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(page.getByRole("menu")).toHaveCount(0);
  } finally {
    await context.close();
  }
});
