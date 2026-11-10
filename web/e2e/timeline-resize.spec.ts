import type { Browser, Locator, Page } from "@playwright/test";
import type {
  Me,
  Message,
  PostMessageRequest,
  Room,
  SetupRequest,
} from "../src/shared/api/types.gen";
import { postVoice } from "./audio";
import { apiCall, test as base, type Credentials, expect, settled } from "./fixtures";
import { type AgentClient, agentJSON, connectAI, isMobile, loginAs } from "./helpers";
import { postPictures } from "./images";
import { type Instance, startInstance } from "./server";

// The timeline keeps its place when its box changes height (KBD-02): a phone at 360x740 whose
// on-screen keyboard opens (the window shrinks to 360x420, as Chrome does with
// `interactive-widget=resizes-content`, and the composer takes focus), in a DM and in a group, at
// the browser's font and at 150% (a large system font, applied on the open page). A reader at the
// newest message stays at it and gets no "Jump to latest" strip; one who scrolled up keeps what
// they were reading.
//
// Its own instance, like mobile.spec.ts: the first-run test of smoke.spec.ts needs one nobody has
// set up.
const test = base.extend<object, { timeline: Instance }>({
  timeline: [
    // biome-ignore lint/correctness/noEmptyPattern: Playwright fixtures must destructure their first argument.
    async ({}, use) => {
      const instance = await startInstance();
      await use(instance);
      await instance.stop();
    },
    { scope: "worker" },
  ],
  baseURL: async ({ timeline }, use) => {
    await use(timeline.url);
  },
});
test.describe.configure({ mode: "serial" });

const OWNER: Credentials & { display_name: string } = {
  username: "timeline-owner",
  password: "e2e-timeline-owner-password",
  display_name: "Alice",
};
const AI_NAME = "Atlas";
const GROUP_NAME = "e2e-timeline-resize";
const PHONE = { width: 360, height: 740 };
/** The window with the on-screen keyboard open. */
const KEYBOARD = 420;
/** How near the newest message counts as at it (the timeline's own threshold). */
const AT_END_PX = 64;

let dm: Room;
let group: Room;

/** A long message, so rows wrap over several lines and the list is taller than the window. */
const long = (n: number) =>
  `Message ${n} of the release train: ${"the rollout keeps going on every phone size, ".repeat(5)}and the notes follow.`;

test.beforeAll(async ({ browser, timeline }) => {
  const context = await browser.newContext();
  try {
    await apiCall<Me>(context.request, timeline, "POST", "/setup", {
      token: timeline.setupToken,
      ...OWNER,
    } satisfies SetupRequest);
    const agent = await connectAI(context, timeline, "/work/timeline");
    await apiCall(context.request, timeline, "PATCH", `/intelligences/${agent.ai.id}`, {
      display_name: AI_NAME,
    });
    const say = (room: Room, body: PostMessageRequest) =>
      agentJSON<Message>(
        context,
        timeline,
        agent as AgentClient,
        "POST",
        `/agent/rooms/${room.id}/messages`,
        body,
      );

    // Few rows, each taller than the virtualizer's estimate of one, so they fill more than the
    // window: what the owner's conversations look like, and what left the timeline above its newest
    // message when the keyboard opened (KBD-02). The DM: the AI's long messages, a file, and a
    // voice message, the newest.
    dm = await apiCall<Room>(context.request, timeline, "POST", "/conversations/dm", {
      with: agent.ai.id,
    });
    await say(dm, {
      text: `See ${"averylongunbrokenwordthatmustwrapinsidemessagesandnotpushthepage".repeat(2)} for the release notes. The release train is green on every phone, and the rollback notes are at https://example.com/releases/${"averylongpath".repeat(4)}`,
    });
    for (let i = 1; i <= 2; i++) await say(dm, { text: long(i) });
    const file = new FormData();
    file.append("message", JSON.stringify({ text: "The full log" }));
    file.append("file", new Blob(["log line\n".repeat(40)], { type: "text/plain" }), "log.txt");
    const posted = await context.request.post(`${timeline.url}/api/v1/rooms/${dm.id}/messages`, {
      multipart: file,
      headers: { Origin: timeline.url },
    });
    expect(posted.ok(), `post file → ${posted.status()}`).toBe(true);
    await postVoice(context.request, timeline, dm.id, { seconds: 2, stamp: "2026-11-10-100000" });

    // The group: a person and the AI, a picture, a voice message and a question with buttons.
    group = await apiCall<Room>(context.request, timeline, "POST", "/groups", {
      name: GROUP_NAME,
      members: [agent.ai.id],
    });
    await apiCall(context.request, timeline, "POST", `/rooms/${group.id}/messages`, {
      text: long(1),
    });
    await say(group, { text: long(2) });
    await postPictures(
      context.request,
      timeline,
      group.id,
      [{ name: "atlas.png", hue: 200 }],
      "A picture",
    );
    await postVoice(context.request, timeline, group.id, {
      seconds: 2,
      stamp: "2026-11-10-100001",
    });
    await say(group, { text: "Deploy today?", quick_replies: ["Yes", "Not yet"] });
    await apiCall(context.request, timeline, "POST", `/rooms/${group.id}/messages`, {
      text: long(3),
    });
    await say(group, { text: "group newest" });
  } finally {
    await context.close();
  }
});

async function openPhone(browser: Browser, instance: Instance) {
  const context = await browser.newContext({ viewport: PHONE, hasTouch: true, isMobile: true });
  await loginAs(context, instance, OWNER);
  const page = await context.newPage();
  return { context, page };
}

/** A group of the owner's alone, for the live tests that add messages to it. */
async function ownGroup(browser: Browser, instance: Instance, name: string) {
  const context = await browser.newContext();
  try {
    await loginAs(context, instance, OWNER);
    const room = await apiCall<Room>(context.request, instance, "POST", "/groups", {
      name,
      members: [],
    });
    for (const n of [1, 2]) {
      await apiCall(context.request, instance, "POST", `/rooms/${room.id}/messages`, {
        text: long(n),
      });
    }
    await postPictures(context.request, instance, room.id, [{ name: "alice.png", hue: 40 }]);
    await apiCall(context.request, instance, "POST", `/rooms/${room.id}/messages`, {
      text: long(3),
    });
    return room;
  } finally {
    await context.close();
  }
}

/** How far the log is from its newest message, in pixels. */
const distanceFromNewest = (page: Page) =>
  page.getByRole("log").evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight);

/** Where the element's top sits in the log's box. */
async function topInLog(page: Page, row: Locator) {
  const [log, box] = await Promise.all([page.getByRole("log").boundingBox(), row.boundingBox()]);
  if (!log || !box) throw new Error("the row or the log has no box");
  return box.y - log.y;
}

/** The conversation open at the phone's full height, the system font applied on the open page. */
async function open(
  page: Page,
  room: Room,
  title: string,
  newest: (log: Locator) => Locator,
  font: "100%" | "150%",
) {
  await page.setViewportSize(PHONE);
  await page.goto(`/rooms/${room.id}`);
  await expect(page.getByRole("heading", { level: 1, name: title })).toBeVisible();
  await expect(newest(page.getByRole("log"))).toBeInViewport();
  await settled(page);
  if (font !== "100%") {
    await page.evaluate((size) => {
      document.documentElement.style.fontSize = size;
    }, font);
  }
  await settled(page);
}

/** The reader scrolls up a screen and a half; the row then at the top of the log is what they read. */
async function scrollUp(page: Page) {
  const log = page.getByRole("log");
  await log.evaluate((el) => {
    el.scrollTop -= el.clientHeight * 1.5;
  });
  await settled(page);
  const index = await log.evaluate((el) => {
    const top = el.getBoundingClientRect().top;
    const rows = [...el.querySelectorAll<HTMLElement>("[data-index]")];
    const first = rows
      .filter((row) => row.getBoundingClientRect().top >= top)
      .sort((a, b) => a.getBoundingClientRect().top - b.getBoundingClientRect().top)[0];
    return first?.dataset.index ?? null;
  });
  expect(index, "no row at the top of the log").not.toBeNull();
  return log.locator(`[data-index="${index}"]`);
}

/** The keyboard opens: the window shrinks and the composer has focus. */
async function openKeyboard(page: Page, title: string) {
  await page.setViewportSize({ width: PHONE.width, height: KEYBOARD });
  await page.getByRole("textbox", { name: `Message ${title}` }).focus();
  await settled(page);
}

const CASES = [
  {
    kind: "DM",
    room: () => dm,
    title: AI_NAME,
    newest: (log: Locator) => log.getByRole("group", { name: "Voice message" }),
  },
  {
    kind: "group",
    room: () => group,
    title: GROUP_NAME,
    newest: (log: Locator) => log.getByText("group newest", { exact: true }),
  },
] as const;

for (const { kind, room, title, newest } of CASES) {
  for (const font of ["100%", "150%"] as const) {
    test(`a ${kind} at its newest message stays there when the keyboard opens, at ${font} (KBD-02)`, async ({
      browser,
      timeline,
    }, testInfo) => {
      test.skip(!isMobile(testInfo), "a phone's keyboard");
      const { context, page } = await openPhone(browser, timeline);
      try {
        await open(page, room(), title, newest, font);
        const log = page.getByRole("log");
        const jump = page.getByRole("button", { name: "Jump to latest" });
        await expect(newest(log)).toBeInViewport();
        expect(await distanceFromNewest(page)).toBeLessThanOrEqual(AT_END_PX);
        await expect(jump).toHaveCount(0);

        await openKeyboard(page, title);
        const at = `${kind} at ${font}, ${PHONE.width}x${KEYBOARD}`;
        expect(
          await distanceFromNewest(page),
          `${at}: the log left the newest message`,
        ).toBeLessThanOrEqual(AT_END_PX);
        await expect(jump, `${at}: Jump to latest shows`).toHaveCount(0);
        await expect(newest(log)).toBeInViewport();

        // And the keyboard closes again: still at the newest message.
        await page.setViewportSize(PHONE);
        await settled(page);
        expect(await distanceFromNewest(page), `${at}: closed again`).toBeLessThanOrEqual(
          AT_END_PX,
        );
        await expect(jump).toHaveCount(0);
        await expect(newest(log)).toBeInViewport();
      } finally {
        await context.close();
      }
    });

    test(`a ${kind} read higher up keeps its place when the keyboard opens, at ${font} (KBD-02)`, async ({
      browser,
      timeline,
    }, testInfo) => {
      test.skip(!isMobile(testInfo), "a phone's keyboard");
      const { context, page } = await openPhone(browser, timeline);
      try {
        await open(page, room(), title, newest, font);
        const log = page.getByRole("log");
        const jump = page.getByRole("button", { name: "Jump to latest" });
        await expect(newest(log)).toBeInViewport();
        const older = await scrollUp(page);
        await expect(jump).toBeVisible();
        await settled(page);
        const before = await topInLog(page, older);
        expect(await distanceFromNewest(page)).toBeGreaterThan(AT_END_PX);

        await openKeyboard(page, title);
        const at = `${kind} at ${font}, ${PHONE.width}x${KEYBOARD}`;
        expect(
          Math.abs((await topInLog(page, older)) - before),
          `${at}: the place moved`,
        ).toBeLessThanOrEqual(2);
        expect(await distanceFromNewest(page), `${at}: it jumped to the newest`).toBeGreaterThan(
          AT_END_PX,
        );
        await expect(jump).toBeVisible();
      } finally {
        await context.close();
      }
    });
  }
}

test("a new message keeps a reader at the newest message there with the keyboard open, and one higher up where they were (KBD-02)", async ({
  browser,
  timeline,
}, testInfo) => {
  test.skip(!isMobile(testInfo), "a phone's keyboard");
  const room = await ownGroup(browser, timeline, "e2e-timeline-live");
  const { context, page } = await openPhone(browser, timeline);
  try {
    const say = (text: string) =>
      apiCall(context.request, timeline, "POST", `/rooms/${room.id}/messages`, { text });
    const log = page.getByRole("log");
    const jump = page.getByRole("button", { name: "Jump to latest" });
    await open(
      page,
      room,
      "e2e-timeline-live",
      (l) => l.getByText(long(3), { exact: true }),
      "100%",
    );
    await openKeyboard(page, "e2e-timeline-live");

    // At the newest message: the new one comes into view under it, with no strip.
    await say(long(4));
    const arrived = log.getByText(long(4), { exact: true });
    await expect(arrived).toBeInViewport();
    await settled(page);
    expect(await distanceFromNewest(page)).toBeLessThanOrEqual(AT_END_PX);
    await expect(jump).toHaveCount(0);

    // Higher up: the reader keeps their place and the strip stays.
    const older = await scrollUp(page);
    await expect(jump).toBeVisible();
    await settled(page);
    const before = await topInLog(page, older);
    // It arrives below, out of view: the log grows by its row.
    const height = await log.evaluate((el) => el.scrollHeight);
    await say(long(5));
    await expect.poll(() => log.evaluate((el) => el.scrollHeight)).toBeGreaterThan(height);
    await settled(page);
    expect(Math.abs((await topInLog(page, older)) - before), "the place moved").toBeLessThanOrEqual(
      2,
    );
    await expect(jump).toBeVisible();
  } finally {
    await context.close();
  }
});
