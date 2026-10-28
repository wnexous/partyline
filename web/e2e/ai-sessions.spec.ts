import type { Me, Message, Room, SetupRequest } from "../src/shared/api/types.gen";
import { apiCall, test as base, type Credentials, expect } from "./fixtures";
import { type AgentClient, agentJSON, connectAI, loginAs, MACHINE, openSession } from "./helpers";
import { type Instance, startInstance } from "./server";

// Two live sessions of one AI, seen and used by a person (ai-sessions T14: SESS-30, SESS-32, SESS-33):
// the profile lists the principal and the parallel session, the timeline shows `via :claude-2` only on
// what the parallel socket wrote, and the mention list offers both addresses and inserts the labelled
// one. The sockets are real agent WebSockets of the binary under test, so the labels and the author
// come from the server's presence, never from the message text.
//
// The file has a server of its own: another file of the same project may share a worker's `instance`,
// and the first-run test of smoke.spec.ts needs one nobody has set up.
const test = base.extend<object, { sessions: Instance }>({
  sessions: [
    // biome-ignore lint/correctness/noEmptyPattern: Playwright fixtures must destructure their first argument.
    async ({}, use) => {
      const instance = await startInstance();
      await use(instance);
      await instance.stop();
    },
    { scope: "worker" },
  ],
  baseURL: async ({ sessions }, use) => {
    await use(sessions.url);
  },
});
test.describe.configure({ mode: "serial" });

const OWNER: Credentials & { display_name: string } = {
  username: "sessions-owner",
  password: "e2e-sessions-owner-password",
  display_name: "Sessions Owner",
};
/** The AI's folder: its handle and display name come from the folder's base name. */
const AI_FOLDER = "/work/atlas";
const AI_NAME = "Atlas";
const ROOM_NAME = "Atlas parallel work";
/** The process id the parallel socket sends: the same AI, another session (SESS-10, SESS-11). */
const SECOND_LIVE = "e2e-sessions-live-0002";
/** The second socket reports the AI's harness, so the free parallel label is "claude-2". */
const SECOND_LABEL = "claude-2";
const PRINCIPAL_TEXT = "The principal session posted this.";
const SECOND_TEXT = "The parallel session posted this.";
/** A group message that names the AI and the parallel session: both addresses are in one text. */
const ADDRESSED_TEXT = "@atlas and @atlas:claude-2 both read this group.";
const TYPED_TEXT = "@atlas:claude-2 ship it";

let agent: AgentClient;
let room: Room;
let principalMessage: Message;
let secondMessage: Message;
let addressedMessage: Message;
let sessions: { close: () => void }[] = [];

test.beforeAll(async ({ browser, sessions: instance }) => {
  const context = await browser.newContext();
  try {
    await apiCall<Me>(context.request, instance, "POST", "/setup", {
      token: instance.setupToken,
      ...OWNER,
    } satisfies SetupRequest);
    agent = await connectAI(context, instance, AI_FOLDER, MACHINE);
    expect(agent.ai.name, "the AI's handle comes from its folder").toBe("atlas");
    room = await apiCall<Room>(context.request, instance, "POST", "/groups", {
      name: ROOM_NAME,
      members: [agent.ai.id],
    });

    // Two real sessions of one folder: the first holds the principal label, so the second one, which
    // reports the AI's harness, is allocated the parallel `claude-2`.
    const principal = await openSession(instance, agent);
    sessions.push(principal);
    const second = await openSession(instance, agent, {
      live: SECOND_LIVE,
      harness: "claude",
    });
    sessions.push(second);
    expect(second.live, "the socket sent the process id it was given").toBe(SECOND_LIVE);

    // SESS-30: the server records the session each message came from. The principal posts without a
    // process id (the folder names it), the second through its live id.
    principalMessage = await agentJSON<Message>(
      context,
      instance,
      agent,
      "POST",
      `/agent/rooms/${room.id}/messages`,
      { text: PRINCIPAL_TEXT },
    );
    expect(principalMessage.author_label ?? "", "the principal has no label").toBe("");

    // A person's message naming the AI and the parallel session (SESS-21): both addresses are stored.
    // It sits between the AI's own messages so the parallel one is not grouped under the principal's.
    addressedMessage = await apiCall<Message>(
      context.request,
      instance,
      "POST",
      `/rooms/${room.id}/messages`,
      { text: ADDRESSED_TEXT },
    );
    expect(addressedMessage.author_label ?? "", "a person carries no session label").toBe("");

    secondMessage = await agentJSON<Message>(
      context,
      instance,
      agent,
      "POST",
      `/agent/rooms/${room.id}/messages`,
      { text: SECOND_TEXT },
      SECOND_LIVE,
    );
    expect(secondMessage.author_label, "the parallel session's label").toBe(SECOND_LABEL);
  } finally {
    await context.close();
  }
});

test.afterAll(() => {
  for (const session of sessions) session.close();
  sessions = [];
});

test("the AI's profile lists the principal and the parallel session (SESS-32)", async ({
  context,
  page,
  sessions: instance,
}) => {
  await loginAs(context, instance, OWNER);
  await page.goto(`/contacts/${agent.ai.id}`);
  await expect(page.getByRole("heading", { name: AI_NAME, level: 1 })).toBeVisible();

  const section = page.getByRole("region", { name: "Live sessions" });
  await expect(section).toBeVisible();
  const labels = section.getByTestId("session-label");
  await expect(labels).toHaveText(["principal", `:${SECOND_LABEL}`]);
  // The parallel session reports its harness, so the row says which app it runs.
  await expect(section.getByText("Claude Code", { exact: true })).toBeVisible();
});

test("the timeline shows via only on the parallel session's message (SESS-30)", async ({
  context,
  page,
  sessions: instance,
}) => {
  await loginAs(context, instance, OWNER);
  await page.goto(`/rooms/${room.id}`);
  const log = page.getByRole("log");

  const principalRow = log.locator(`[data-testid="message-${principalMessage.seq}"]`);
  await expect(principalRow.getByText(PRINCIPAL_TEXT, { exact: true })).toBeVisible();
  await expect(principalRow.getByText(/via :/)).toHaveCount(0);

  const secondRow = log.locator(`[data-testid="message-${secondMessage.seq}"]`);
  await expect(secondRow.getByText(SECOND_TEXT, { exact: true })).toBeVisible();
  await expect(secondRow.getByText(`via :${SECOND_LABEL}`, { exact: true })).toBeVisible();

  // The person's message with both addresses is stored as written, with no label of its own.
  await expect(log.getByText(ADDRESSED_TEXT, { exact: true })).toBeVisible();
});

test("the mention list offers both sessions and inserts the labelled address (SESS-33)", async ({
  context,
  page,
  sessions: instance,
}) => {
  await loginAs(context, instance, OWNER);
  await page.goto(`/rooms/${room.id}`);
  const box = page.getByRole("textbox", { name: `Message ${ROOM_NAME}` });
  await expect(box).toBeVisible();

  await box.fill("@atl");
  const list = page.getByRole("listbox", { name: "Mention someone" });
  const options = list.getByRole("option");
  await expect(options).toHaveCount(2);
  // The AI itself is the plain `@atlas`; the labelled session adds `:claude-2` after it.
  await expect(options.nth(0).getByText("@atlas", { exact: true })).toBeVisible();
  await expect(options.nth(0).getByText(`:${SECOND_LABEL}`, { exact: true })).toHaveCount(0);
  await expect(options.nth(1).getByText("@atlas", { exact: true })).toBeVisible();
  await expect(options.nth(1).getByText(`:${SECOND_LABEL}`, { exact: true })).toBeVisible();

  // Picking the session inserts the whole labelled address and keeps the caret after it.
  await options.nth(1).click();
  await expect(box).toHaveValue(`@atlas:${SECOND_LABEL} `);

  await box.pressSequentially("ship it");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await expect(page.getByRole("log").getByText(TYPED_TEXT, { exact: true })).toBeVisible();
});
