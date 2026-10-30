import type { List, Me, Room, RoomNotification, SetupRequest } from "../src/shared/api/types.gen";
import { apiCall, test as base, type Credentials, expect } from "./fixtures";
import {
  type AgentClient,
  connectAI,
  expectNoSidewaysScroll,
  isMobile,
  loginAs,
  MACHINE,
  openSession,
} from "./helpers";
import { type Instance, startInstance } from "./server";

// The owner mutes a parallel session in one conversation and sees the marker (session notifications
// T4: SN-08). The AI has its principal and a parallel `:claude-2`, which by default is notified of
// mentions only; the member card's session line marks it, the label opens the switch of this
// conversation, and turning it on removes the marker, turning it off brings it back. The server's
// answer is read back through the API, so the marker is the stored setting and not a local state.
//
// The file has a server of its own, like ai-sessions.spec.ts.
const test = base.extend<object, { notifications: Instance }>({
  notifications: [
    // biome-ignore lint/correctness/noEmptyPattern: Playwright fixtures must destructure their first argument.
    async ({}, use) => {
      const instance = await startInstance();
      await use(instance);
      await instance.stop();
    },
    { scope: "worker" },
  ],
  baseURL: async ({ notifications }, use) => {
    await use(notifications.url);
  },
});
test.describe.configure({ mode: "serial" });

const OWNER: Credentials & { display_name: string } = {
  username: "notifications-owner",
  password: "e2e-notifications-owner-password",
  display_name: "Notifications Owner",
};
const AI_FOLDER = "/work/atlas";
const ROOM_NAME = "Atlas notifications";
const PARALLEL_LABEL = "claude-2";
const PARALLEL_LIVE = "e2e-notifications-live-0002";

let agent: AgentClient;
let room: Room;
let sessions: { close: () => void }[] = [];

test.beforeAll(async ({ browser, notifications: instance }) => {
  const context = await browser.newContext();
  try {
    await apiCall<Me>(context.request, instance, "POST", "/setup", {
      token: instance.setupToken,
      ...OWNER,
    } satisfies SetupRequest);
    agent = await connectAI(context, instance, AI_FOLDER, MACHINE);
    room = await apiCall<Room>(context.request, instance, "POST", "/groups", {
      name: ROOM_NAME,
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

/** The session's setting in the room, as the server stores it. */
async function storedNotify(context: Parameters<typeof loginAs>[0], instance: Instance) {
  const list = await apiCall<List<RoomNotification>>(
    context.request,
    instance,
    "GET",
    `/agents/${agent.ai.id}/sessions/${PARALLEL_LABEL}/notifications`,
  );
  return list.items.find((item) => item.room_id === room.id)?.notify;
}

test("the owner mutes a parallel session in a room and the member card marks it (SN-08)", async ({
  context,
  page,
  notifications: instance,
}, testInfo) => {
  const principal = await openSession(instance, agent);
  const parallel = await openSession(instance, agent, {
    live: PARALLEL_LIVE,
    harness: "claude",
  });
  sessions.push(principal, parallel);

  await loginAs(context, instance, OWNER);
  await page.goto(`/rooms/${room.id}`);
  await expect(page.getByRole("heading", { name: ROOM_NAME, level: 1 })).toBeVisible();

  if (isMobile(testInfo)) await page.getByRole("button", { name: "Members (1)" }).click();
  const sessionsLine = page.getByTestId(/^member-sessions-/);
  await expect(sessionsLine.getByTestId("session-label")).toHaveText([
    "principal",
    `:${PARALLEL_LABEL}`,
  ]);
  await expect(page.getByTestId("session-muted")).toHaveCount(1);
  await expect(page.getByTestId("session-muted")).toHaveText(
    "Notifications off in this conversation",
  );

  // The session's label opens this conversation's switch; the default is mentions only.
  await page.getByTestId(`session-notify-${PARALLEL_LABEL}`).click();
  const sheet = page.getByRole("dialog", { name: "Notifications in this conversation" });
  await expect(sheet).toBeVisible();
  const toggle = sheet.getByRole("switch", { name: `Notifications in ${ROOM_NAME}` });
  await expect(toggle).not.toBeChecked();
  await expect(sheet.getByText(/Mentions only/)).toBeVisible();
  await expectNoSidewaysScroll(page, "the notification sheet");

  // On: every message the delivery mode wants reaches it, and the marker goes.
  await toggle.click();
  await expect(toggle).toBeChecked();
  await expect(sheet.getByText(/All messages/)).toBeVisible();
  await expect.poll(() => storedNotify(context, instance)).toBe("on");

  // Off again: it is muted in this conversation, and the member card says so with a bell-off marker.
  await toggle.click();
  await expect(toggle).not.toBeChecked();
  await expect(sheet.getByText(/Mentions only/)).toBeVisible();
  await expect.poll(() => storedNotify(context, instance)).toBe("mentions");
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("session-muted")).toHaveCount(1);
});
