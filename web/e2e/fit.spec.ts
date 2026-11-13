import { createHmac } from "node:crypto";
import type { Locator, Page } from "@playwright/test";
import type {
  Connector,
  Identity,
  List,
  Me,
  Message,
  Organization,
  PairingCode,
  PostMessageRequest,
  Room,
  Schedule,
  ScheduleRequest,
  SetupRequest,
  WizardQuestionRequest,
} from "../src/shared/api/types.gen";
import { postVoice } from "./audio";
import { apiCall, test as base, type Credentials, expect, settled } from "./fixtures";
import {
  agentJSON,
  connectAI,
  expectFits,
  headerAction,
  loginAs,
  MACHINE,
  openSession,
} from "./helpers";
import { postPictures } from "./images";
import { type Instance, startInstance } from "./server";
import { shootWhole } from "./shots";

// The phone fit check (UI-24): the pages the product owner uses on an Android phone, at the two
// phone widths that must fit with the system font enlarged, and in both projects the suite already
// runs. A page fits when it does not scroll sideways and no visible text spills out of its own box;
// a long name, a long word, an image, a voice message, the radio cards of My account and a second
// organization named at the 60-character limit are seeded so the check has something to catch. Its
// own instance, like orgs.spec.ts, keeps the first-run test of smoke.spec.ts on an instance nobody
// has set up.
const test = base.extend<object, { fit: Instance }>({
  fit: [
    // biome-ignore lint/correctness/noEmptyPattern: Playwright fixtures must destructure their first argument.
    async ({}, use) => {
      const instance = await startInstance();
      await use(instance);
      await instance.stop();
    },
    { scope: "worker" },
  ],
  baseURL: async ({ fit }, use) => {
    await use(fit.url);
  },
});
test.describe.configure({ mode: "serial" });

const OWNER: Credentials & { display_name: string } = {
  username: "fit-owner",
  password: "e2e-fit-owner-password",
  display_name: "Mobile Fit Owner Of The Release Train Withoutbreakopportunities",
};
const AI_NAME = "Atlas ReleaseTrainOperatorWithoutAnyBreakOpportunity";
const ROOM_NAME = "Mobile fit release train averylongunbrokenroomnamethatkeepsgoing";
const CONNECTOR_NAME = "Mobile fit webhook averylongunbrokenconnectorname";
/**
 * The organization of the compact switcher's menu (SET-09, SET-12): exactly 60 characters, one of
 * them a 45-character word that cannot wrap, so the menu's group label has something to cut.
 */
const ORG_NAME = "Mobile fit org organizationnamethatcannotbreakonaphonescreen";
/**
 * The AI's second live session (SESS-34): it runs in a linked worktree whose folder base is exactly
 * the 40-character label limit, so the profile row and the `via` chip carry one unbroken word that
 * must wrap inside them instead of widening the page.
 */
const SECOND_LABEL = "worktreeparallelsessionlonglabelnobreaks";
const SECOND_FOLDER = `/work/${SECOND_LABEL}`;
const SECOND_LIVE = "e2e-fit-live-session-0002";
const SECOND_TEXT = "The parallel session answers from its worktree.";
const CONNECTOR_SECRET = "synthetic-e2e-webhook-secret-not-real";
const LONG_TEXT = `See ${"averylongunbrokenwordthatmustwrapinsidemessagesandnotpushthepage".repeat(2)} for the release notes`;
/** The long draft of the composer check: many lines, so the box grows to its cap. */
const LONG_DRAFT =
  "This draft keeps going so the composer grows over several lines on a phone. ".repeat(8);
/** A routine of the AI with a long name and prompt, run once so its history has a row (HB-11). */
const ROUTINE: ScheduleRequest = {
  name: "Morning summary of the release train for the whole team",
  prompt: "Summarize what happened overnight in the release room and list anything to roll back.",
  cron: "30 8 * * 1-5",
};
const BIO = `Keeps the release train moving through every phone size. ${"averylongunbrokenbiowordthatmustwrapinsidetheprofilepanelwithoutwideninganything".repeat(2)}`;

/**
 * The longest label a quick reply or an option may carry (QR-03, 48 characters), ending in one run
 * with no space, so the bubble has to break the word rather than wrap between words (QR-24).
 */
const label48 = (head: string) => `${head} releasebreakerreleasebreakerreleasebreaker`.slice(0, 48);
/** The four buttons of the seeded question, each at that limit. */
const FIT_REPLIES = [
  label48("Deploy today"),
  label48("Wait for review"),
  label48("Ship the API only"),
  label48("Cancel the release"),
];
/** The wizard's first question, the one the fit check answers to reach Next (QR-24). */
const FIRST_QUESTION = "Which deployment window should this delivery use next Monday?";
/** The eight questions of the seeded wizard, every title and option long enough to wrap. */
const FIT_QUESTIONS: WizardQuestionRequest[] = [
  {
    title: FIRST_QUESTION,
    options: [label48("Monday evening"), label48("Friday morning")],
  },
  {
    title: "Who follows the publication of the version until the window closes?",
    kind: "multi",
    options: [label48("Release captain"), label48("Support lead"), label48("On-call engineer")],
  },
  {
    title: "Which parts ship in this delivery before the code freeze?",
    kind: "multi",
    options: [label48("Public API"), label48("Web interface"), label48("Database")],
  },
  {
    title: "How do we tell the people who use the product every day?",
    kind: "multi",
    options: [label48("In-app notice"), label48("Status page"), label48("Direct message")],
  },
  {
    title: "What is the rollback plan when the publication fails halfway?",
    options: [label48("Revert the release"), label48("Restore snapshot")],
  },
  {
    title: "How long does the support team need to validate the delivery?",
    options: [label48("Fifteen minutes"), label48("Half an hour")],
  },
  {
    title: "Where do we record the result of the publication once it ends?",
    options: [label48("Release notes"), label48("Incident log")],
  },
  {
    title: "When do we review what this delivery taught the release team?",
    options: [label48("Next standup"), label48("Next retro")],
  },
];

let owner: Me;
let orgId = "";
let agentId = "";
let room: Room;
let dm: Room;
let questionAt: Message;
let wizardAt: Message;
let secretAt: Message;
let sessions: { close: () => void }[] = [];

/**
 * The root font size of a check: the browser's own, or enlarged the way the phone's system setting
 * enlarges it (rem scaling; the app has no text size setting of its own).
 */
type Font = "100%" | "125%" | "150%";

const enlarge = async (page: Page, font: Font) => {
  if (font === "100%") return;
  await page.evaluate((size) => {
    document.documentElement.style.fontSize = size;
  }, font);
};

/** Walks the page from top to offscreen bottom, so rows skipped offscreen get laid out too. */
async function scrollThrough(page: Page) {
  const height = await page.evaluate(() => document.documentElement.scrollHeight);
  const step = await page.evaluate(() => Math.max(200, Math.floor(window.innerHeight * 0.75)));
  for (let top = 0; top < height; top += step) {
    await page.evaluate((y) => window.scrollTo(0, y), top);
    await page.evaluate(
      () =>
        new Promise((done) => {
          requestAnimationFrame(() => done(null));
        }),
    );
  }
  await page.evaluate(() => window.scrollTo(0, 0));
}

/** The picture's name for a check: "fit-" and the page's part of `where`, e.g. "fit-my-account". */
function shotName(where: string) {
  const page = where.slice(where.lastIndexOf(": ") + 2);
  return `fit-${page
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "")}`;
}

async function checkPage(
  page: Page,
  where: string,
  path: string,
  ready: () => Locator,
  font: Font,
) {
  await page.goto(path);
  // The check is of the phone as the person has it: the page loads, then the system font applies.
  await enlarge(page, font);
  await expect(ready()).toBeVisible();
  await settled(page);
  await scrollThrough(page);
  await settled(page);
  await expectFits(page, where);
  await shootWhole(page, shotName(where));
}

/**
 * The two seeded questions at the end of the conversation (QR-24): the four buttons at the label
 * limit and the wizard's first step. Choosing an option must leave Next reachable on the phone.
 */
async function checkQuestions(page: Page) {
  const log = page.getByRole("log");
  const asked = log.locator(`[data-testid="message-${questionAt.seq}"]`);
  const quick = asked.getByRole("group", { name: "Quick replies" });
  await quick.scrollIntoViewIfNeeded();
  for (const label of FIT_REPLIES) {
    await expect(quick.getByRole("button", { name: label })).toBeVisible();
  }
  const planning = log.locator(`[data-testid="message-${wizardAt.seq}"]`);
  const step = planning.getByRole("radiogroup", { name: FIRST_QUESTION });
  await step.scrollIntoViewIfNeeded();
  await expect(step).toBeVisible();
  const next = planning.getByRole("button", { name: "Next" });
  await expect(next).toBeVisible();
  await expect(next).toBeDisabled();
  await step.getByRole("radio").first().check();
  await expect(next).toBeEnabled();
}

/** The longest secret name (SR-01, 64 characters): one word the card must break, never spill. */
const FIT_SECRET_NAME = `EXAMPLE_${"LONG_".repeat(11)}API_KEY`.slice(0, 64);

/**
 * The seeded secret request (SR-13): the owner manages the AI, so the card offers the field, the show
 * toggle and Save, and on a touch screen each of them is a 44 px target.
 */
async function checkSecretCard(page: Page) {
  const card = page
    .getByRole("log")
    .locator(`[data-testid="message-${secretAt.seq}"]`)
    .getByRole("region", { name: `Secret requested: ${FIT_SECRET_NAME}` });
  await card.scrollIntoViewIfNeeded();
  await expect(card).toBeVisible();
  const field = card.getByLabel(`Value of ${FIT_SECRET_NAME}`);
  await expect(field).toBeVisible();
  const targets = [
    field,
    card.getByRole("button", { name: "Show the value" }),
    card.getByRole("button", { name: "Save" }),
  ];
  const coarse = await page.evaluate(() => window.matchMedia("(pointer: coarse)").matches);
  for (const target of targets) {
    await expect(target).toBeVisible();
    const box = await target.boundingBox();
    expect(box, "the control has a box").not.toBeNull();
    if (coarse && box) {
      expect(box.height, "a touch target of the secret card is 44 px tall").toBeGreaterThanOrEqual(
        44,
      );
    }
  }
}

/** The conversation of the seeded group, with its long author, long word, image, voice and questions. */
async function checkConversation(page: Page, where: string, font: Font) {
  await page.goto(`/rooms/${room.id}`);
  await enlarge(page, font);
  const log = page.getByRole("log");
  await expect(log.getByText(LONG_TEXT, { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: ROOM_NAME, level: 1 })).toBeVisible();
  await expect(log.getByRole("button", { name: "Open mobilefit-landscape.png" })).toBeVisible();
  await expect(log.getByRole("group", { name: "Voice message" })).toBeVisible();
  // The message a parallel session posted carries its label beside the AI's name (SESS-30).
  await expect(log.getByText(SECOND_TEXT, { exact: true })).toBeVisible();
  await expect(log.getByText(`via :${SECOND_LABEL}`, { exact: true })).toBeVisible();
  // The empty composer is one line: a long room name in its placeholder is cut, not wrapped, so it
  // does not take the timeline's room on a small phone.
  const composer = page.getByRole("textbox", { name: `Message ${ROOM_NAME}` });
  await expect(composer).toHaveValue("");
  const lines = await composer.evaluate((el) => {
    const style = getComputedStyle(el);
    return (
      (el.getBoundingClientRect().height - Number.parseFloat(style.minHeight)) /
      Number.parseFloat(style.lineHeight)
    );
  });
  expect(lines, `${where}: the empty composer is taller than one line`).toBeLessThan(0.5);
  await checkQuestions(page);
  await checkSecretCard(page);
  await settled(page);
  await scrollThrough(page);
  await settled(page);
  await expectFits(page, where);
  await shootWhole(page, shotName(where));
}

/**
 * The full profile page of the seeded AI: its live sessions list the principal and the second
 * session, whose 40-character label must wrap inside its row (SESS-32, SESS-34).
 */
async function checkFullProfile(page: Page, where: string, font: Font) {
  await page.goto(`/contacts/${agentId}`);
  await enlarge(page, font);
  await expect(page.getByRole("heading", { name: AI_NAME, level: 1 })).toBeVisible();
  const live = page.getByRole("region", { name: "Live sessions" });
  await expect(live).toBeVisible();
  await expect(live.getByTestId("session-label")).toHaveText(["principal", `:${SECOND_LABEL}`]);
  await settled(page);
  await scrollThrough(page);
  await settled(page);
  await expectFits(page, where);
  await shootWhole(page, shotName(where));
}

/** The profile panel of the seeded AI: a sidebar on a wide screen, a drawer on a phone. */
async function checkProfilePanel(page: Page, where: string, font: Font) {
  await page.goto(`/rooms/${room.id}`);
  await enlarge(page, font);
  const log = page.getByRole("log");
  await expect(page.getByRole("heading", { name: ROOM_NAME, level: 1 })).toBeVisible();
  // Keyboard activation: a long truncated name's center can sit under the row's hover controls. The AI
  // authored more than one row, since it asks the questions too: any of its names opens the panel.
  await log.getByRole("link", { name: AI_NAME, exact: true }).first().focus();
  await page.keyboard.press("Enter");
  const panel = page.getByRole("dialog", { name: AI_NAME });
  await expect(panel).toBeVisible();
  await expect(panel.getByText(/Keeps the release train moving/)).toBeVisible();
  if ((page.viewportSize()?.width ?? 0) < 400) {
    // A small phone puts the photo above the name, so the name has the panel's width to wrap in.
    const title = await panel.getByRole("heading", { name: AI_NAME }).boundingBox();
    const box = await panel.boundingBox();
    expect(
      (title?.width ?? 0) / (box?.width ?? 1),
      `${where}: the AI's name is squeezed beside its photo`,
    ).toBeGreaterThan(0.7);
  }
  // The conversations the viewer can see it in (PEOP-03): the group the AI answers in.
  const conversations = panel.getByRole("region", { name: "Conversations" });
  await expect(conversations.getByRole("link", { name: ROOM_NAME })).toHaveAttribute(
    "href",
    `/rooms/${room.id}`,
  );
  await panel.getByRole("link", { name: "Open full profile" }).scrollIntoViewIfNeeded();
  await settled(page);
  await scrollThrough(page);
  await settled(page);
  await expectFits(page, where);
  await shootWhole(page, shotName(where));
}

/** The account's menu and the organization's, open on the list screen: they must fit too (SET-12). */
async function checkOpenMenu(page: Page, where: string, trigger: () => Locator, font: Font) {
  await page.goto("/");
  await enlarge(page, font);
  const button = trigger();
  await expect(button).toBeVisible();
  await settled(page);
  await button.click();
  const menu = page.getByRole("menu");
  await expect(menu.getByRole("menuitem").first()).toBeVisible();
  await settled(page);
  await expectFits(page, where);
  await shootWhole(page, shotName(where));
  await page.keyboard.press("Escape");
  await expect(menu).toHaveCount(0);
}

/**
 * The compact switcher's menu of a phone's top bar (SET-09, SET-12): it carries the organization's
 * pages, and a 60-character organization name must be cut inside it, not spill out of its box.
 */
async function checkCompactOrgMenu(page: Page, where: string, font: Font) {
  await page.goto("/account");
  await enlarge(page, font);
  const trigger = () => page.getByRole("button", { name: /^Organization: / });
  await expect(trigger()).toBeVisible();
  await settled(page);
  await trigger().click();
  const menu = page.getByRole("menu", { name: "Organizations" });
  // The pick still switches the menu's organization: the long-named one becomes the current one.
  await menu.getByRole("menuitemradio", { name: new RegExp(`^${ORG_NAME}`) }).click();
  await expect(trigger()).toHaveAccessibleName(new RegExp(`^Organization: ${ORG_NAME}`));
  await settled(page);
  await trigger().click();
  await expect(menu.getByRole("menuitem", { name: "Settings" })).toBeVisible();
  await settled(page);
  await expectFits(page, where);
  await shootWhole(page, shotName(where));
  await page.keyboard.press("Escape");
  await expect(menu).toHaveCount(0);
}

/**
 * The control is on screen: scrolled to if it must be, then wholly inside the window, so a finger
 * reaches it (a button pushed off the side or under the bottom edge fails here).
 */
async function expectInView(page: Page, control: Locator, where: string, what: string) {
  await control.scrollIntoViewIfNeeded();
  await expect(control, `${where}: ${what} is not visible`).toBeVisible();
  const box = await control.boundingBox();
  const size = page.viewportSize() ?? { width: 0, height: 0 };
  expect(box, `${where}: ${what} has no box`).not.toBeNull();
  if (!box) return;
  expect(box.x, `${where}: ${what} starts left of the window`).toBeGreaterThanOrEqual(-1);
  expect(box.y, `${where}: ${what} starts above the window`).toBeGreaterThanOrEqual(-1);
  expect(box.x + box.width, `${where}: ${what} ends right of the window`).toBeLessThanOrEqual(
    size.width + 1,
  );
  expect(box.y + box.height, `${where}: ${what} ends under the window`).toBeLessThanOrEqual(
    size.height + 1,
  );
}

/** The tallest the conversation header may be on a phone: one row (UI-24). */
const HEADER_MAX = 80;
/** The least of the window's height the messages keep beside the header and the composer. */
const LOG_MIN_SHARE = 0.35;

/**
 * The conversation header of a phone (UI-24), at the window's full height and with the on-screen
 * keyboard taking its lower part (the window shrinks to `keyboard` px tall, the composer focused):
 * one row of the way back, the face, the name and "More options", no taller than `HEADER_MAX`; the
 * messages keep `LOG_MIN_SHARE` of the window; and every action of the header is in the menu, each
 * on screen and big enough for a finger.
 */
async function checkCompactHeader(
  page: Page,
  where: string,
  conversation: Room,
  title: string,
  actions: (string | RegExp)[],
  keyboard: number,
  font: Font,
) {
  const size = page.viewportSize() ?? { width: 360, height: 740 };
  await page.goto(`/rooms/${conversation.id}`);
  await enlarge(page, font);
  const header = page.getByTestId("conversation-header");
  await expect(header.getByRole("heading", { level: 1, name: title })).toBeVisible();
  const parts = [
    header.getByRole("link", { name: "Back to conversations" }),
    header.getByTestId("intelligence-avatar"),
    header.getByRole("heading", { level: 1 }),
    header.getByRole("button", { name: "More options" }),
  ];
  const composer = page.getByRole("textbox", { name: `Message ${title}` });
  for (const height of [size.height, keyboard]) {
    await page.setViewportSize({ width: size.width, height });
    await settled(page);
    // The keyboard is up because the composer has focus.
    if (height !== size.height) await composer.focus();
    await settled(page);
    const at = `${where}: the header of ${title} in a ${size.width}x${height} window`;
    const bar = await header.boundingBox();
    expect(bar, `${at}: no header`).not.toBeNull();
    if (!bar) return;
    expect(bar.height, `${at}: the header is taller than one row`).toBeLessThanOrEqual(HEADER_MAX);
    // One row: every part of it shares a horizontal band, none sits under another.
    const boxes = await Promise.all(parts.map((part) => part.boundingBox()));
    for (const [index, box] of boxes.entries()) {
      expect(box, `${at}: part ${index} of the header is missing`).not.toBeNull();
    }
    const top = Math.max(...boxes.map((box) => box?.y ?? 0));
    const bottom = Math.min(...boxes.map((box) => (box?.y ?? 0) + (box?.height ?? 0)));
    expect(bottom, `${at}: the header's parts are on more than one row`).toBeGreaterThan(top);
    const more = boxes[3];
    expect(more?.width ?? 0, `${at}: More options is narrower than 44 px`).toBeGreaterThanOrEqual(
      44,
    );
    expect(more?.height ?? 0, `${at}: More options is shorter than 44 px`).toBeGreaterThanOrEqual(
      44,
    );
    // The messages have what lies between the header and the composer (with the Jump to latest
    // strip under them when the reader is not at the newest message).
    const form = await composer.locator("xpath=ancestor::form").boundingBox();
    const messages = (form?.y ?? 0) - (bar.y + bar.height);
    const log = await page.getByRole("log").boundingBox();
    expect(log?.y ?? 0, `${at}: the messages do not start under the header`).toBeGreaterThanOrEqual(
      bar.y + bar.height - 1,
    );
    expect(
      messages / height,
      `${at}: the messages keep less than ${LOG_MIN_SHARE * 100}% of the window`,
    ).toBeGreaterThanOrEqual(LOG_MIN_SHARE);
    await expectFits(page, at);
    await shootWhole(page, shotName(`${where}: the header of ${conversation.kind} at ${height}`));

    // Every action of the header, by its name, in the menu.
    await header.getByRole("button", { name: "More options" }).click();
    const menu = page.getByRole("dialog", { name: title });
    await expect(menu).toBeVisible();
    await settled(page);
    for (const name of actions) {
      const item = menu.getByRole("button", { name, exact: true });
      await expectInView(page, item, at, String(name));
      const box = await item.boundingBox();
      expect(box?.height ?? 0, `${at}: ${name} is shorter than 44 px`).toBeGreaterThanOrEqual(44);
    }
    await expectFits(page, `${at}: its menu`);
    await page.keyboard.press("Escape");
    await expect(menu).toHaveCount(0);
  }
  await page.setViewportSize(size);
}

/**
 * What the owner meets every day on the phone, with the largest system font (a 360x740 phone at
 * 150%): the one-row header of a conversation, the DM with a file and a voice message, the
 * composer with a long draft, both also with the on-screen keyboard open (the window shrinks, as
 * `interactive-widget=resizes-content` makes Chrome do), the members of a group, and the routines
 * of the AI with a history and the new routine dialog. Each fits the window and keeps its controls on screen.
 */
async function checkDailyScreens(page: Page, where: string, font: Font) {
  const size = page.viewportSize() ?? { width: 360, height: 740 };

  // The header of the DM and of the group stays one row, with the keyboard open too (UI-24).
  const keyboard = 420;
  await checkCompactHeader(page, where, dm, AI_NAME, ["Participants (2)", "Files"], keyboard, font);
  await checkCompactHeader(
    page,
    where,
    room,
    ROOM_NAME,
    [/^Members \(\d+\)$/, "Files", "Room settings"],
    keyboard,
    font,
  );

  // The DM: the header keeps the way back and the name; the voice player keeps its buttons.
  await page.goto(`/rooms/${dm.id}`);
  await enlarge(page, font);
  const log = page.getByRole("log");
  await expect(log.getByRole("group", { name: "Voice message" })).toBeVisible();
  const header = page.getByTestId("conversation-header");
  await expectInView(
    page,
    header.getByRole("link", { name: "Back to conversations" }),
    where,
    "the back button",
  );
  await expectInView(
    page,
    header.getByRole("heading", { level: 1, name: AI_NAME }),
    where,
    "the DM's title",
  );
  const title = await header.getByRole("heading", { level: 1 }).boundingBox();
  expect(
    title?.width ?? 0,
    `${where}: the DM's title is squeezed to a letter or two`,
  ).toBeGreaterThan(size.width / 3);
  const voice = log.getByRole("group", { name: "Voice message" });
  await expectInView(page, voice.getByRole("button", { name: "Play" }), where, "Play");
  await expectInView(page, voice.getByRole("slider", { name: "Seek" }), where, "the seek bar");
  await expectInView(page, voice.getByRole("link", { name: "Download audio" }), where, "Download");
  await settled(page);
  await scrollThrough(page);
  await settled(page);
  await expectFits(page, `${where}: the DM`);
  await shootWhole(page, shotName(`${where}: the DM`));

  // The members of the group, in the sheet a phone opens them in: tabs, Add members and the row's
  // actions stay inside it, and the member's name is not cut to a letter.
  await page.goto(`/rooms/${room.id}`);
  await enlarge(page, font);
  await (await headerAction(page, /^Members/)).click();
  const panel = page.getByRole("complementary", { name: /Members/ });
  await expect(panel).toBeVisible();
  await settled(page);
  await expectInView(
    page,
    panel.getByRole("button", { name: "Add members" }),
    where,
    "Add members",
  );
  const name = await panel.getByRole("link", { name: AI_NAME }).first().boundingBox();
  expect(name?.width ?? 0, `${where}: the member's name is squeezed`).toBeGreaterThan(
    size.width / 3,
  );
  await expectFits(page, `${where}: the members panel`);
  await shootWhole(page, shotName(`${where}: the members panel`));
  await page.keyboard.press("Escape");

  // The routines of the AI, with the history of one open, and the new routine dialog: its title
  // stays clear of the close button and Create routine is reachable at the end of its scroll.
  await page.goto(`/contacts/${agentId}`);
  await enlarge(page, font);
  const routines = page.getByRole("region", { name: "Routines" });
  const row = routines.getByRole("listitem").filter({ hasText: ROUTINE.name ?? "" });
  await expectInView(
    page,
    row.getByRole("button", { name: `Run ${ROUTINE.name} now` }),
    where,
    "Run now",
  );
  await row.getByRole("button", { name: `History of ${ROUTINE.name}` }).click();
  await expect(
    row.getByRole("list", { name: `History of ${ROUTINE.name}` }).getByRole("listitem"),
  ).toHaveCount(1);
  await settled(page);
  await scrollThrough(page);
  await settled(page);
  await expectFits(page, `${where}: the routines with a history open`);
  await shootWhole(page, shotName(`${where}: the routines`));
  await routines.getByRole("button", { name: "New routine" }).click();
  const dialog = page.getByRole("dialog", { name: /New routine for/ });
  await expect(dialog).toBeVisible();
  await settled(page);
  // The lines of the title, not its box (a block as wide as the dialog): none may run under the
  // close button's icon. The button's own box is grown to 44 px for a finger and may reach them.
  const icon = await dialog.getByRole("button", { name: "Close" }).locator("svg").boundingBox();
  expect(icon, `${where}: the dialog has no close button`).not.toBeNull();
  const underIcon = await dialog
    .getByRole("heading")
    .first()
    .evaluate((title, box) => {
      if (!box) return false;
      const range = document.createRange();
      range.selectNodeContents(title);
      return [...range.getClientRects()].some(
        (line) =>
          line.left < box.x + box.width &&
          box.x < line.right &&
          line.top < box.y + box.height &&
          box.y < line.bottom,
      );
    }, icon);
  expect(underIcon, `${where}: the dialog's title runs under its close button`).toBe(false);
  await expectFits(page, `${where}: the new routine dialog`);
  // The dialog scrolls up and down, so a field or a word wider than it would not widen the page:
  // it would scroll sideways inside it. It must not.
  const sideways = await dialog.evaluate((el) => {
    const edge = el.getBoundingClientRect().right;
    const out = [...el.querySelectorAll("*")]
      .filter((child) => child.getBoundingClientRect().right > edge + 1)
      .slice(0, 4)
      .map(
        (child) => `<${child.tagName.toLowerCase()}> "${(child.textContent ?? "").slice(0, 40)}"`,
      );
    return { by: el.scrollWidth - el.clientWidth, out };
  });
  expect(
    sideways.by,
    `${where}: the new routine dialog scrolls sideways; sticking out: ${sideways.out.join(" | ")}`,
  ).toBeLessThanOrEqual(1);
  await expectInView(
    page,
    dialog.getByRole("button", { name: "Create routine" }),
    where,
    "Create routine",
  );
  await expectInView(page, dialog.getByRole("button", { name: "Cancel" }), where, "Cancel");
  await shootWhole(page, shotName(`${where}: the new routine dialog`));
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);

  // Last, as it resizes the window: the composer of the DM with a long draft. The box grows to its
  // cap and every button stays on screen, with the window at its full height and with the
  // keyboard taking its lower part.
  await page.goto(`/rooms/${dm.id}`);
  await enlarge(page, font);
  const composer = page.getByRole("textbox", { name: `Message ${AI_NAME}` });
  await composer.fill(LONG_DRAFT);
  for (const height of [size.height, Math.round(size.height * 0.57)]) {
    await page.setViewportSize({ width: size.width, height });
    await settled(page);
    const at = `${where}: the composer with a long draft in a ${size.width}x${height} window`;
    await expectInView(page, composer, at, "the text box");
    await expectInView(page, page.getByLabel("Attach files"), at, "Attach files");
    await expectInView(
      page,
      page.getByRole("button", { name: "Record a voice message" }),
      at,
      "the microphone",
    );
    await expectInView(page, page.getByRole("button", { name: "Send" }), at, "Send");
    await expectFits(page, at);
    await shootWhole(page, shotName(`${where}: the composer at ${height}`));
  }
  await page.setViewportSize(size);
  await composer.fill("");
}

async function checkEveryPage(page: Page, where: string, font: Font) {
  await checkPage(
    page,
    `${where}: the conversation list`,
    "/",
    () => page.getByRole("list", { name: "Conversations" }),
    font,
  );
  await checkConversation(page, `${where}: the group conversation`, font);
  await checkPage(
    page,
    `${where}: My account`,
    "/account",
    () => page.getByRole("radio", { name: "System" }),
    font,
  );
  await checkPage(
    page,
    `${where}: Members`,
    `/orgs/${orgId}/members`,
    () => page.getByRole("table", { name: /^Members of / }),
    font,
  );
  await checkPage(
    page,
    `${where}: the org chart`,
    `/orgs/${orgId}/chart`,
    () => page.getByRole("tree", { name: /^Org chart of / }),
    font,
  );
  if (font !== "100%") {
    // The phone has a finger, not a mouse: each arrow that opens or closes a branch is 44 px.
    const arrows = await page.getByRole("button", { name: /^(Collapse|Expand) / }).all();
    expect(arrows.length, `${where}: the org chart has no arrow to check`).toBeGreaterThan(0);
    for (const arrow of arrows) {
      const box = await arrow.boundingBox();
      expect(
        box?.height ?? 0,
        `${where}: an org chart arrow is shorter than 44 px`,
      ).toBeGreaterThanOrEqual(44);
      expect(
        box?.width ?? 0,
        `${where}: an org chart arrow is narrower than 44 px`,
      ).toBeGreaterThanOrEqual(44);
    }
  }
  await checkPage(
    page,
    `${where}: Identities`,
    "/identities",
    () => page.getByRole("list", { name: "Your verified chat accounts" }),
    font,
  );
  await checkPage(
    page,
    `${where}: Connectors`,
    `/orgs/${orgId}/connections`,
    () => page.getByRole("list", { name: /^Connectors of / }),
    font,
  );
  await checkPage(
    page,
    `${where}: the organization's settings`,
    `/orgs/${orgId}/settings`,
    () => page.getByRole("heading", { name: "Details" }),
    font,
  );
  await checkPage(
    page,
    `${where}: Contacts`,
    "/contacts",
    () => page.getByRole("heading", { level: 1, name: "Contacts" }),
    font,
  );
  await checkPage(
    page,
    `${where}: API tokens`,
    "/tokens",
    () => page.getByRole("heading", { level: 1, name: "API tokens" }),
    font,
  );
  await checkPage(
    page,
    `${where}: Organizations`,
    "/orgs",
    () => page.getByRole("heading", { level: 1, name: "Organizations" }),
    font,
  );
  await checkPage(
    page,
    `${where}: Users`,
    "/admin/users",
    () => page.getByRole("heading", { level: 1, name: "Users" }),
    font,
  );
  await checkPage(
    page,
    `${where}: Invites`,
    "/admin/invites",
    () => page.getByRole("heading", { level: 1, name: "Invites" }),
    font,
  );
  await checkPage(
    page,
    `${where}: the audit log`,
    "/admin/audit",
    () => page.getByRole("heading", { level: 1, name: "Audit log" }),
    font,
  );
  await checkProfilePanel(page, `${where}: the profile panel`, font);
  await checkFullProfile(page, `${where}: the full profile`, font);
  await checkOpenMenu(
    page,
    `${where}: the account menu`,
    () => page.getByRole("button", { name: "Account menu" }),
    font,
  );
  await checkOpenMenu(
    page,
    `${where}: the organization menu`,
    () => page.getByRole("button", { name: /^Pages of / }),
    font,
  );
}

test.beforeAll(async ({ browser, fit }) => {
  const context = await browser.newContext();
  try {
    owner = await apiCall<Me>(context.request, fit, "POST", "/setup", {
      token: fit.setupToken,
      ...OWNER,
    } satisfies SetupRequest);
    const orgs = await apiCall<List<Organization>>(context.request, fit, "GET", "/orgs");
    orgId = orgs.items[0]?.id ?? owner.default_org_id ?? "";
    expect(orgId, "the owner's personal organization").not.toBe("");
    // A second organization whose name sits at the 60-character limit: the compact switcher's menu
    // has to cut it instead of growing past the phone's width (SET-09).
    const longName = await apiCall<Organization>(context.request, fit, "POST", "/orgs", {
      name: ORG_NAME,
    });
    expect(longName.name).toBe(ORG_NAME);

    // An AI with a long name and a long bio: the panel and the chart must carry both.
    const agent = await connectAI(context, fit, "/work/mobilefit", MACHINE);
    agentId = agent.ai.id;
    await apiCall(context.request, fit, "PATCH", `/intelligences/${agent.ai.id}`, {
      display_name: AI_NAME,
      bio: BIO,
    });
    room = await apiCall<Room>(context.request, fit, "POST", "/groups", {
      name: ROOM_NAME,
      members: [agent.ai.id],
    });
    await apiCall(context.request, fit, "POST", `/rooms/${room.id}/messages`, {
      text: LONG_TEXT,
    });
    await agentJSON<Message>(context, fit, agent, "POST", `/agent/rooms/${room.id}/messages`, {
      text: "The release train is green on every phone.",
    });
    await postVoice(context.request, fit, room.id, { seconds: 2, stamp: "2026-10-24-140509" });
    await postPictures(
      context.request,
      fit,
      room.id,
      [{ name: "mobilefit-landscape.png", hue: 150 }],
      "A picture from the release train",
    );
    // The questions the fit check measures (QR-24): four buttons at the label limit and an
    // eight-question wizard, both asked by the AI through its own room route.
    questionAt = await agentJSON<Message>(
      context,
      fit,
      agent,
      "POST",
      `/agent/rooms/${room.id}/messages`,
      { text: "Deploy today?", quick_replies: FIT_REPLIES } satisfies PostMessageRequest,
    );
    wizardAt = await agentJSON<Message>(
      context,
      fit,
      agent,
      "POST",
      `/agent/rooms/${room.id}/messages`,
      {
        text: "Plan the release",
        wizard: { questions: FIT_QUESTIONS },
      } satisfies PostMessageRequest,
    );
    // The secret request the fit check measures (SR-13): the longest name and a reason that wraps.
    secretAt = await agentJSON<Message>(
      context,
      fit,
      agent,
      "POST",
      `/agent/rooms/${room.id}/messages`,
      {
        text: "To send the weekly release report by email I need the key of the mail service; paste it here and it goes to a private file on this machine.",
        secret: { name: FIT_SECRET_NAME },
      } satisfies PostMessageRequest,
    );
    // The AI's two live sessions, the principal first; the second is the worktree whose 40-character
    // label the profile and the conversation must carry. A person's message between the AI's own
    // stops the parallel message from being grouped under it, so its `via` chip shows.
    sessions.push(await openSession(fit, agent));
    sessions.push(
      await openSession(fit, agent, {
        live: SECOND_LIVE,
        harness: "claude",
        folder: SECOND_FOLDER,
        mainFolder: "/work/mobilefit",
      }),
    );
    await apiCall(context.request, fit, "POST", `/rooms/${room.id}/messages`, {
      text: "One last look from the release manager before the parallel session answers.",
    });
    await agentJSON<Message>(
      context,
      fit,
      agent,
      "POST",
      `/agent/rooms/${room.id}/messages`,
      { text: SECOND_TEXT },
      SECOND_LIVE,
    );

    // The DM of the daily check (360 px at 150%): a long message of the AI, a file and a voice
    // message, so the conversation has every kind of row the owner meets each day.
    dm = await apiCall<Room>(context.request, fit, "POST", "/conversations/dm", {
      with: agent.ai.id,
    });
    await agentJSON<Message>(context, fit, agent, "POST", `/agent/rooms/${dm.id}/messages`, {
      text: `${LONG_TEXT}. The release train is green on every phone, and the rollback notes are at https://example.com/releases/${"averylongpath".repeat(4)}`,
    });
    const file = new FormData();
    file.append("message", JSON.stringify({ text: "The full log" }));
    file.append(
      "file",
      new Blob(["log line\n".repeat(40)], { type: "text/plain" }),
      "release-train-overnight-full-log-with-a-long-name.txt",
    );
    const posted = await context.request.post(`${fit.url}/api/v1/rooms/${dm.id}/messages`, {
      multipart: file,
      headers: { Origin: fit.url },
    });
    expect(posted.ok(), `post file → ${posted.status()}`).toBe(true);
    await postVoice(context.request, fit, dm.id, { seconds: 3, stamp: "2026-10-24-140510" });
    const routine = await apiCall<Schedule>(
      context.request,
      fit,
      "POST",
      `/agents/${agent.ai.id}/schedules`,
      ROUTINE,
    );
    await apiCall(
      context.request,
      fit,
      "POST",
      `/agents/${agent.ai.id}/schedules/${routine.id}/run`,
      {},
    );

    // A connection with a long name, and one verified identity linked through it, so both lists
    // have a row to check; the hook is signed with the synthetic secret the create answered.
    const connector = await apiCall<Connector>(context.request, fit, "POST", "/connectors", {
      kind: "webhook",
      name: CONNECTOR_NAME,
      config: { secret: CONNECTOR_SECRET },
    });
    await apiCall<Connector>(context.request, fit, "PATCH", `/connectors/${connector.id}`, {
      operator_id: agent.ai.id,
    });
    await expect
      .poll(
        async () =>
          (await apiCall<Connector>(context.request, fit, "GET", `/connectors/${connector.id}`))
            .status,
      )
      .toBe("connected");
    const pairing = await apiCall<PairingCode>(
      context.request,
      fit,
      "POST",
      "/identities/pairing",
      {
        connector_id: connector.id,
      },
    );
    const body = JSON.stringify({
      chat_id: "120363000000000001@fit",
      sender_id: "5511999990000",
      sender_name: "Mobile fit verified chat identity",
      text: `!link ${pairing.code}`,
    });
    const stamp = Math.floor(Date.now() / 1000).toString();
    const signature = `sha256=${createHmac("sha256", CONNECTOR_SECRET)
      .update(`${stamp}.${body}`)
      .digest("hex")}`;
    const hooked = await context.request.post(`${fit.url}/api/v1/hooks/${connector.id}`, {
      headers: {
        "Content-Type": "application/json",
        Origin: fit.url,
        "X-Partyline-Timestamp": stamp,
        "X-Partyline-Signature": signature,
      },
      data: body,
    });
    expect(hooked.status(), "the webhook accepted the pairing link").toBe(202);
    const identities = await apiCall<List<Identity>>(context.request, fit, "GET", "/identities");
    expect(identities.items).toHaveLength(1);
  } finally {
    await context.close();
  }
});

test.afterAll(() => {
  for (const session of sessions) session.close();
  sessions = [];
});

test("every page fits the window of this project", async ({ context, page, fit }, testInfo) => {
  await loginAs(context, fit, OWNER);
  await checkEveryPage(page, `the ${testInfo.project.name} window`, "100%");
});

// The owner's phone is about 360 px wide and runs with a large system font: 150% is that phone.
for (const [width, height, font] of [
  [320, 700, "125%"],
  [412, 900, "125%"],
  [360, 740, "150%"],
] as const) {
  test(`every page fits a ${width}x${height} phone with a ${font} root font`, async ({
    browser,
    fit,
  }) => {
    // The 150% phone also walks the daily screens: twice the pages of the others.
    if (font === "150%") test.slow();
    const context = await browser.newContext({
      viewport: { width, height },
      hasTouch: true,
      isMobile: true,
    });
    try {
      await loginAs(context, fit, OWNER);
      const page = await context.newPage();
      const where = `a ${width}x${height} phone at ${font}`;
      await checkEveryPage(page, where, font);
      await checkCompactOrgMenu(page, `${where}: the compact switcher's menu`, font);
      if (font === "150%") await checkDailyScreens(page, where, font);
    } finally {
      await context.close();
    }
  });
}
