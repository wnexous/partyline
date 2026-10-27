import type { APIRequestContext } from "@playwright/test";
import type {
  Me,
  Message,
  Page as MessagePage,
  PostMessageRequest,
  Room,
  SetupRequest,
  WizardQuestionRequest,
} from "../src/lib/api/types.gen";
import { apiCall, test as base, type Credentials, expect, settled } from "./fixtures";
import { agentJSON, connectAI, loginAs, MACHINE } from "./helpers";
import { type Instance, startInstance } from "./server";

// The questions of a conversation in the real web app (QR-25): an AI asks a quick reply question and
// a wizard through its own room route, a person taps one button and completes the wizard with the
// keyboard, and the conversation shows each answer as one reply with the server's rendered text. A
// reload reads the stored answer back, so the chosen label and the wizard's summary are the state of
// the timeline and not of the tap. The file has a server of its own, like the other files that set an
// instance up: the first-run test of smoke.spec.ts needs an instance nobody has set up.
const test = base.extend<object, { quickReplies: Instance }>({
  quickReplies: [
    // biome-ignore lint/correctness/noEmptyPattern: Playwright fixtures must destructure their first argument.
    async ({}, use) => {
      const instance = await startInstance();
      await use(instance);
      await instance.stop();
    },
    { scope: "worker" },
  ],
  baseURL: async ({ quickReplies }, use) => {
    await use(quickReplies.url);
  },
});
test.describe.configure({ mode: "serial" });

const OWNER: Credentials & { display_name: string } = {
  username: "quick-replies-owner",
  password: "e2e-quick-replies-owner-password",
  display_name: "Replies Owner",
};
const ROOM_NAME = "Delivery questions";
const QUESTION_TEXT = "Deploy hoje?";
const WIZARD_TEXT = "Planejar a entrega";
/** The two questions of the wizard, the first single and the second multiple choice. */
const WIZARD_QUESTIONS: WizardQuestionRequest[] = [
  { title: "Quando?", options: ["Amanhã", "Na sexta"] },
  { title: "O que entra?", kind: "multi", options: ["API", "Web", "Mobile"] },
];

let room: Room;
let question: Message;
let wizard: Message;

test.beforeAll(async ({ browser, quickReplies }) => {
  const context = await browser.newContext();
  try {
    await apiCall<Me>(context.request, quickReplies, "POST", "/setup", {
      token: quickReplies.setupToken,
      ...OWNER,
    } satisfies SetupRequest);
    // An AI in a group of the owner's: it asks through the agent room route, so no connection and no
    // platform takes part in the flow (QR-18).
    const agent = await connectAI(context, quickReplies, "/work/quick-replies", MACHINE);
    room = await apiCall<Room>(context.request, quickReplies, "POST", "/groups", {
      name: ROOM_NAME,
      members: [agent.ai.id],
    });
    question = await agentJSON<Message>(
      context,
      quickReplies,
      agent,
      "POST",
      `/agent/rooms/${room.id}/messages`,
      { text: QUESTION_TEXT, quick_replies: ["Sim", "Não"] } satisfies PostMessageRequest,
    );
    wizard = await agentJSON<Message>(
      context,
      quickReplies,
      agent,
      "POST",
      `/agent/rooms/${room.id}/messages`,
      {
        text: WIZARD_TEXT,
        wizard: { questions: WIZARD_QUESTIONS },
      } satisfies PostMessageRequest,
    );
    expect(question.interactive?.quick_replies?.map((reply) => reply.label)).toEqual([
      "Sim",
      "Não",
    ]);
    expect(wizard.interactive?.wizard?.questions).toHaveLength(2);
  } finally {
    await context.close();
  }
});

/** The structured answers a question holds, as the room serves them (QR-05..QR-07). */
async function answersTo(
  request: APIRequestContext,
  instance: Instance,
  questionId: string,
): Promise<Message[]> {
  const page = await apiCall<MessagePage<Message>>(
    request,
    instance,
    "GET",
    `/rooms/${room.id}/messages`,
  );
  return page.items.filter((message) => message.reply_to === questionId && message.answers);
}

test("a quick reply is one tap and stays chosen after a reload (QR-25)", async ({
  context,
  page,
  quickReplies,
}) => {
  await loginAs(context, quickReplies, OWNER);
  await page.goto(`/rooms/${room.id}`);
  const log = page.getByRole("log");
  const group = log.getByRole("group", { name: "Quick replies" });
  await expect(group.getByRole("button", { name: "Sim" })).toBeVisible();
  await expect(group.getByRole("button", { name: "Não" })).toBeVisible();

  const posted = page.waitForResponse(
    (response) =>
      response.url() === `${quickReplies.url}/api/v1/rooms/${room.id}/messages` &&
      response.request().method() === "POST",
  );
  await group.getByRole("button", { name: "Sim" }).click();
  expect((await posted).status()).toBe(201);

  // The tap holds the labels while it is sent and then marks the chosen one, with the answer's own
  // text as the server rendered it (QR-11, QR-12).
  await expect(group.getByRole("status")).toHaveText("Answer sent: Sim");
  await expect(group.getByRole("button", { name: "Sim" })).toHaveAttribute("aria-pressed", "true");
  await expect(group.getByRole("button", { name: "Não" })).toHaveAttribute("aria-disabled", "true");

  // One reply, whose text is the chosen label, is what the conversation holds (QR-05, QR-06).
  await expect
    .poll(async () => (await answersTo(context.request, quickReplies, question.id)).length)
    .toBe(1);
  const [answer] = await answersTo(context.request, quickReplies, question.id);
  expect(answer?.text).toBe("Sim");
  expect(answer?.answers?.quick_reply).toBe("Sim");
  await expect(log.locator(`[data-testid="message-${answer?.seq}"]`)).toContainText("Sim");

  // The stored answer is the authority (QR-12): a reload reads it back and sends nothing.
  const posts: string[] = [];
  page.on("request", (request) => {
    if (request.method() === "POST") posts.push(request.url());
  });
  await page.reload();
  await expect(group.getByRole("button", { name: "Sim" })).toHaveAttribute("aria-pressed", "true");
  await expect(group.getByRole("status")).toHaveText("Answer sent: Sim");
  await settled(page);
  expect(posts).toEqual([]);
});

test("a wizard is answered with the keyboard and reads back as its summary (QR-25)", async ({
  context,
  page,
  quickReplies,
}) => {
  await loginAs(context, quickReplies, OWNER);
  await page.goto(`/rooms/${room.id}`);
  const log = page.getByRole("log");
  const row = log.locator(`[data-testid="message-${wizard.seq}"]`);
  const first = row.getByRole("radiogroup", { name: "Quando?" });
  await expect(first).toBeVisible();
  await expect(row.getByRole("status")).toHaveText("Step 1 of 2");
  const next = row.getByRole("button", { name: "Next" });
  await expect(next).toBeDisabled();

  // The keyboard alone answers the wizard (QR-17): Space chooses, Tab reaches the next control and
  // Enter moves the step on. Focus starts on the step's question.
  await first.getByRole("radio", { name: "Na sexta" }).focus();
  await page.keyboard.press("Space");
  await expect(next).toBeEnabled();
  await page.keyboard.press("Tab");
  await expect(next).toBeFocused();
  await page.keyboard.press("Enter");

  const second = row.getByRole("group", { name: "O que entra?" });
  await expect(second).toBeVisible();
  await expect(row.getByRole("status")).toHaveText("Step 2 of 2");
  // A step change moves focus to its heading, so the first Tab reaches the first option.
  await page.keyboard.press("Tab");
  await expect(second.getByRole("checkbox", { name: "API" })).toBeFocused();
  await page.keyboard.press("Space");
  await page.keyboard.press("Tab");
  await expect(second.getByRole("checkbox", { name: "Web" })).toBeFocused();
  await page.keyboard.press("Space");
  // Past the last option, the step's own Back comes before Send.
  await page.keyboard.press("Tab");
  await page.keyboard.press("Tab");
  await expect(row.getByRole("button", { name: "Back" })).toBeFocused();
  await page.keyboard.press("Tab");
  const send = row.getByRole("button", { name: "Send" });
  await expect(send).toBeFocused();

  const posted = page.waitForResponse(
    (response) =>
      response.url() === `${quickReplies.url}/api/v1/rooms/${room.id}/messages` &&
      response.request().method() === "POST",
  );
  await page.keyboard.press("Enter");
  expect((await posted).status()).toBe(201);

  // One reply with every answer in the wizard's order, as the server renders the lines (QR-07).
  await expect
    .poll(async () => (await answersTo(context.request, quickReplies, wizard.id)).length)
    .toBe(1);
  const [answer] = await answersTo(context.request, quickReplies, wizard.id);
  expect(answer?.text).toBe("Quando?: Na sexta\nO que entra?: API, Web");
  const reply = log.locator(`[data-testid="message-${answer?.seq}"]`);
  await expect(reply).toContainText("Quando?: Na sexta");
  await expect(reply).toContainText("O que entra?: API, Web");

  // The summary is the stored answer read back (QR-16): the values sent, and no step to fill again.
  const posts: string[] = [];
  page.on("request", (request) => {
    if (request.method() === "POST") posts.push(request.url());
  });
  await page.reload();
  await expect(row.getByRole("heading", { name: "Your answer" })).toBeVisible();
  await expect(row.getByText("Quando?", { exact: true })).toBeVisible();
  await expect(row.getByText("Na sexta", { exact: true })).toBeVisible();
  await expect(row.getByText("O que entra?", { exact: true })).toBeVisible();
  await expect(row.getByText("API, Web", { exact: true })).toBeVisible();
  await expect(row.getByRole("button", { name: "Send" })).toHaveCount(0);
  await settled(page);
  expect(posts).toEqual([]);
});
