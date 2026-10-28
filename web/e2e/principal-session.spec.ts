import type { Me, Room, SetupRequest } from "../src/shared/api/types.gen";
import { apiCall, test as base, type Credentials, expect } from "./fixtures";
import { type AgentClient, connectAI, loginAs, MACHINE, openSession } from "./helpers";
import { type Instance, startInstance } from "./server";

// The AI's principal session is fixed by the harness session id (ai-sessions P1: fixed principal,
// SESS-50..SESS-61). Two real agent sockets send their harness session ids: the first one holds the
// principal role, the second is `:claude-2`. When the principal leaves, nobody takes its place: the
// profile says "Principal offline since" while the AI stays online through the parallel session, and the
// owner makes the parallel session the principal with the button of the Sessions list.
//
// The file has a server of its own, like ai-sessions.spec.ts.
const test = base.extend<object, { principal: Instance }>({
  principal: [
    // biome-ignore lint/correctness/noEmptyPattern: Playwright fixtures must destructure their first argument.
    async ({}, use) => {
      const instance = await startInstance();
      await use(instance);
      await instance.stop();
    },
    { scope: "worker" },
  ],
  baseURL: async ({ principal }, use) => {
    await use(principal.url);
  },
});
test.describe.configure({ mode: "serial" });

const OWNER: Credentials & { display_name: string } = {
  username: "principal-owner",
  password: "e2e-principal-owner-password",
  display_name: "Principal Owner",
};
const AI_FOLDER = "/work/atlas";
const AI_NAME = "Atlas";
/** The harness session ids the two sessions send (Claude Code's CLAUDE_CODE_SESSION_ID). */
const FIRST_SESSION = "e2e-harness-session-0001";
const SECOND_SESSION = "e2e-harness-session-0002";
const SECOND_LABEL = "claude-2";

let agent: AgentClient;
let sessions: { close: () => void }[] = [];

test.beforeAll(async ({ browser, principal: instance }) => {
  const context = await browser.newContext();
  try {
    await apiCall<Me>(context.request, instance, "POST", "/setup", {
      token: instance.setupToken,
      ...OWNER,
    } satisfies SetupRequest);
    agent = await connectAI(context, instance, AI_FOLDER, MACHINE);
    await apiCall<Room>(context.request, instance, "POST", "/groups", {
      name: "Atlas principal",
      members: [agent.ai.id],
    });
  } finally {
    await context.close();
  }
});

test.afterAll(() => {
  for (const session of sessions) session.close();
  sessions = [];
});

test("a parallel session does not take the principal role when the principal leaves, and the owner can hand it over (SESS-53, SESS-59, SESS-61)", async ({
  context,
  page,
  principal: instance,
}) => {
  const first = await openSession(instance, agent, {
    live: "e2e-principal-live-0001",
    harness: "claude",
    harnessSession: FIRST_SESSION,
  });
  const second = await openSession(instance, agent, {
    live: "e2e-principal-live-0002",
    harness: "claude",
    harnessSession: SECOND_SESSION,
  });
  sessions.push(first, second);

  await loginAs(context, instance, OWNER);
  await page.goto(`/contacts/${agent.ai.id}`);
  await expect(page.getByRole("heading", { name: AI_NAME, level: 1 })).toBeVisible();
  const section = page.getByRole("region", { name: "Live sessions" });
  await expect(section.getByTestId("session-label")).toHaveText(["principal", `:${SECOND_LABEL}`]);
  await expect(page.getByTestId("profile-principal-offline")).toHaveCount(0);

  // The principal's process dies: the parallel session stays parallel, the AI stays online.
  first.close();
  await expect(section.getByTestId("session-label")).toHaveText([`:${SECOND_LABEL}`]);
  await expect(page.getByTestId("profile-principal-offline")).toContainText(
    "Principal offline since",
  );
  await expect(page.getByRole("region", { name: "Details" }).getByText("Online")).toBeVisible();

  // The owner hands the role to the parallel session. The server asks it to reconnect (the fake
  // session is a bare socket and just ends), and it comes back with its harness session id.
  await page
    .getByRole("button", { name: `Make :${SECOND_LABEL} the principal session of ${AI_NAME}` })
    .click();
  await expect(
    page.getByText(`:${SECOND_LABEL} is taking over as ${AI_NAME}’s principal session`),
  ).toBeVisible();
  second.close();
  const back = await openSession(instance, agent, {
    live: "e2e-principal-live-0003",
    harness: "claude",
    harnessSession: SECOND_SESSION,
  });
  sessions.push(back);
  await expect(section.getByTestId("session-label")).toHaveText(["principal"]);
  await expect(page.getByTestId("profile-principal-offline")).toHaveCount(0);
});
