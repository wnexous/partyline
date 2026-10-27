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
  SetupRequest,
  WizardQuestionRequest,
} from "../src/lib/api/types.gen";
import { postVoice } from "./audio";
import { apiCall, test as base, type Credentials, expect, settled } from "./fixtures";
import { agentJSON, connectAI, expectFits, loginAs, MACHINE } from "./helpers";
import { postPictures } from "./images";
import { type Instance, startInstance } from "./server";

// The phone fit check (UI-24): the pages the product owner uses on an Android phone, at the two
// phone widths that must fit with the system font enlarged, and in both projects the suite already
// runs. A page fits when it does not scroll sideways and no visible text spills out of its own box;
// a long name, a long word, an image, a voice message and the radio cards of My account are seeded
// so the check has something to catch. Its own instance, like orgs.spec.ts, keeps the first-run test
// of smoke.spec.ts on an instance nobody has set up.
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
const CONNECTOR_SECRET = "synthetic-e2e-webhook-secret-not-real";
const LONG_TEXT = `See ${"averylongunbrokenwordthatmustwrapinsidemessagesandnotpushthepage".repeat(2)} for the release notes`;
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
let room: Room;
let questionAt: Message;
let wizardAt: Message;

/** A root font size enlarged the way the phone's system setting enlarges it. */
const enlarge = (page: Page) =>
  page.evaluate(() => {
    document.documentElement.style.fontSize = "125%";
  });

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

async function checkPage(
  page: Page,
  where: string,
  path: string,
  ready: () => Locator,
  enlarged: boolean,
) {
  await page.goto(path);
  // The check is of the phone as the person has it: the page loads, then the system font applies.
  if (enlarged) await enlarge(page);
  await expect(ready()).toBeVisible();
  await settled(page);
  await scrollThrough(page);
  await settled(page);
  await expectFits(page, where);
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

/** The conversation of the seeded group, with its long author, long word, image, voice and questions. */
async function checkConversation(page: Page, where: string, enlarged: boolean) {
  await page.goto(`/rooms/${room.id}`);
  if (enlarged) await enlarge(page);
  const log = page.getByRole("log");
  await expect(log.getByText(LONG_TEXT, { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: ROOM_NAME, level: 1 })).toBeVisible();
  await expect(log.getByRole("button", { name: "Open mobilefit-landscape.png" })).toBeVisible();
  await expect(log.getByRole("group", { name: "Voice message" })).toBeVisible();
  await checkQuestions(page);
  await settled(page);
  await scrollThrough(page);
  await settled(page);
  await expectFits(page, where);
}

/** The profile panel of the seeded AI: a sidebar on a wide screen, a drawer on a phone. */
async function checkProfilePanel(page: Page, where: string, enlarged: boolean) {
  await page.goto(`/rooms/${room.id}`);
  if (enlarged) await enlarge(page);
  const log = page.getByRole("log");
  await expect(page.getByRole("heading", { name: ROOM_NAME, level: 1 })).toBeVisible();
  // Keyboard activation: a long truncated name's center can sit under the row's hover controls. The AI
  // authored more than one row, since it asks the questions too: any of its names opens the panel.
  await log.getByRole("link", { name: AI_NAME, exact: true }).first().focus();
  await page.keyboard.press("Enter");
  const panel = page.getByRole("dialog", { name: AI_NAME });
  await expect(panel).toBeVisible();
  await expect(panel.getByText(/Keeps the release train moving/)).toBeVisible();
  await panel.getByRole("link", { name: "Open full profile" }).scrollIntoViewIfNeeded();
  await settled(page);
  await scrollThrough(page);
  await settled(page);
  await expectFits(page, where);
}

/** The account's menu and the organization's, open on the list screen: they must fit too (SET-12). */
async function checkOpenMenu(page: Page, where: string, trigger: () => Locator, enlarged: boolean) {
  await page.goto("/");
  if (enlarged) await enlarge(page);
  const button = trigger();
  await expect(button).toBeVisible();
  await settled(page);
  await button.click();
  const menu = page.getByRole("menu");
  await expect(menu.getByRole("menuitem").first()).toBeVisible();
  await settled(page);
  await expectFits(page, where);
  await page.keyboard.press("Escape");
  await expect(menu).toHaveCount(0);
}

async function checkEveryPage(page: Page, where: string, enlarged: boolean) {
  await checkPage(
    page,
    `${where}: the conversation list`,
    "/",
    () => page.getByRole("list", { name: "Conversations" }),
    enlarged,
  );
  await checkConversation(page, `${where}: the group conversation`, enlarged);
  await checkPage(
    page,
    `${where}: My account`,
    "/account",
    () => page.getByRole("radio", { name: "System" }),
    enlarged,
  );
  await checkPage(
    page,
    `${where}: Members`,
    `/orgs/${orgId}/members`,
    () => page.getByRole("table", { name: /^Members of / }),
    enlarged,
  );
  await checkPage(
    page,
    `${where}: the org chart`,
    `/orgs/${orgId}/chart`,
    () => page.getByRole("tree", { name: /^Org chart of / }),
    enlarged,
  );
  await checkPage(
    page,
    `${where}: Identities`,
    "/identities",
    () => page.getByRole("list", { name: "Your verified chat accounts" }),
    enlarged,
  );
  await checkPage(
    page,
    `${where}: Connectors`,
    `/orgs/${orgId}/connections`,
    () => page.getByRole("list", { name: /^Connectors of / }),
    enlarged,
  );
  await checkPage(
    page,
    `${where}: the organization's settings`,
    `/orgs/${orgId}/settings`,
    () => page.getByRole("heading", { name: "Details" }),
    enlarged,
  );
  await checkProfilePanel(page, `${where}: the profile panel`, enlarged);
  await checkOpenMenu(
    page,
    `${where}: the account menu`,
    () => page.getByRole("button", { name: "Account menu" }),
    enlarged,
  );
  await checkOpenMenu(
    page,
    `${where}: the organization menu`,
    () => page.getByRole("button", { name: /^Pages of / }),
    enlarged,
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

    // An AI with a long name and a long bio: the panel and the chart must carry both.
    const agent = await connectAI(context, fit, "/work/mobilefit", MACHINE);
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

test("every page fits the window of this project", async ({ context, page, fit }, testInfo) => {
  await loginAs(context, fit, OWNER);
  await checkEveryPage(page, `the ${testInfo.project.name} window`, false);
});

for (const [width, height] of [
  [320, 700],
  [412, 900],
] as const) {
  test(`every page fits a ${width}x${height} phone with a 125% root font`, async ({
    browser,
    fit,
  }) => {
    const context = await browser.newContext({
      viewport: { width, height },
      hasTouch: true,
      isMobile: true,
    });
    try {
      await loginAs(context, fit, OWNER);
      const page = await context.newPage();
      await checkEveryPage(page, `a ${width}x${height} phone at 125%`, true);
    } finally {
      await context.close();
    }
  });
}
