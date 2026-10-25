import type { BrowserContext, Locator, Page, TestInfo } from "@playwright/test";
import type {
  AddMemberRequest,
  AgentMessage,
  AgentRoom,
  Page as ApiPage,
  Contacts,
  CreatedInvite,
  CreateInvitationRequest,
  Invitation,
  List,
  Me,
  Message,
  Organization,
  Room,
  Token,
} from "../src/lib/api/types.gen";
import { ADMIN, apiCall, expect, MEMBER, settled, test } from "./fixtures";
import {
  agentJSON,
  connectAI,
  expectNoSidewaysScroll,
  isMobile,
  loginAs,
  MACHINE,
  trackLive,
} from "./helpers";
import { postPictures } from "./images";
import type { Instance } from "./server";
import { shoot } from "./shots";

// Smoke suite against the built binary (intelligences T52: LAY-01, LAY-05, DM-01, PROF-03,
// TREE-01; web-app UI-01, UI-02, UI-04, UI-14, UI-24; harness-onboarding WIZ-02; messenger-polish
// IMG-01..IMG-06). Set E2E_SHOTS_DIR to also save screenshots of those flows. The tests share
// one fresh instance per project and run in order: the first one creates the admin. The organization
// flow has its own file and its own instance (orgs.spec.ts).
test.describe.configure({ mode: "serial" });

/** A 1x1 PNG: the smallest image the avatar upload accepts (PROF-03). */
const PHOTO = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
  "base64",
);

/** The home page's heading: beside the sidebar it asks to pick one; on a phone it is the list. */
function homeHeading(testInfo: TestInfo) {
  return isMobile(testInfo) ? "Conversations" : "Pick a conversation";
}

test("first run: setup, log out, log back in to a protected route (UI-01, UI-02)", async ({
  page,
  instance,
}, testInfo) => {
  await page.goto("/settings");
  await expect(page).toHaveURL(`${instance.url}/setup`);
  await expect(page.getByRole("heading", { name: "Set up partyline" })).toBeVisible();

  await page.getByLabel("Setup token", { exact: true }).fill(instance.setupToken);
  await page.getByLabel("Username", { exact: true }).fill(ADMIN.username);
  await page.getByLabel("Password", { exact: true }).fill(ADMIN.password);
  await page.getByRole("button", { name: "Create admin account" }).click();
  await expect(page).toHaveURL(`${instance.url}/`);
  await expect(page.getByRole("heading", { name: homeHeading(testInfo), level: 1 })).toBeVisible();

  await page.getByRole("button", { name: "Account menu" }).click();
  await page.getByRole("menuitem", { name: "Log out" }).click();
  await expect(page).toHaveURL(`${instance.url}/login`);

  // The session is gone: a protected route goes back to login and remembers where it was.
  await page.goto("/settings");
  await expect(page).toHaveURL(`${instance.url}/login?redirect=%2Fsettings`);
  await page.getByLabel("Username", { exact: true }).fill(ADMIN.username);
  await page.getByLabel("Password", { exact: true }).fill(ADMIN.password);
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page).toHaveURL(`${instance.url}/settings`);
  await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeVisible();
});

test("a message posted in a room reaches another user's open room live (UI-04)", async ({
  browser,
  instance,
}) => {
  const adminCtx = await browser.newContext();
  const memberCtx = await browser.newContext();
  try {
    await loginAs(adminCtx, instance, ADMIN);
    const invite = await apiCall<CreatedInvite>(
      adminCtx.request,
      instance,
      "POST",
      "/admin/invites",
      { role: "member" },
    );
    const inviteToken = new URL(invite.url).pathname.split("/").pop();
    const member = await apiCall<Me>(
      memberCtx.request,
      instance,
      "POST",
      `/invites/${inviteToken}/accept`,
      MEMBER,
    );

    const admin = await adminCtx.newPage();
    await admin.goto("/");
    // The sidebar's button; the empty list offers the same action too.
    await admin.getByRole("button", { name: "New group" }).first().click();
    const dialog = admin.getByRole("dialog", { name: "New group" });
    await dialog.getByLabel("Name", { exact: true }).fill("e2e-wire");
    await dialog.getByRole("button", { name: "Create group" }).click();
    await expect(admin).toHaveURL(new RegExp(`^${instance.url}/rooms/[^/]+$`));
    const roomId = new URL(admin.url()).pathname.split("/").pop();
    // A group is shared by organization membership (AD-016): the member joins the organization that owns
    // it, which is the admin's own, and then the group.
    const room = await apiCall<Room>(adminCtx.request, instance, "GET", `/rooms/${roomId}`);
    const invitation = await apiCall<Invitation>(
      adminCtx.request,
      instance,
      "POST",
      `/orgs/${room.org_id}/invitations`,
      {
        handle: member.intelligence.handle,
        permission: "member",
      } satisfies CreateInvitationRequest,
    );
    await apiCall<Organization>(
      memberCtx.request,
      instance,
      "POST",
      `/invitations/${invitation.id}/accept`,
    );
    await apiCall(adminCtx.request, instance, "POST", `/rooms/${roomId}/members`, {
      kind: "user",
      member_id: member.id,
    } satisfies AddMemberRequest);

    const viewer = await memberCtx.newPage();
    const live = trackLive(viewer);
    await viewer.goto(`/rooms/${roomId}`);
    await expect(viewer.getByRole("heading", { name: "e2e-wire", level: 1 })).toBeVisible();
    await expect.poll(() => live.ready).toBe(true);
    // Marks this document: a reload would drop it. Timeline fetches are counted from here on.
    await viewer.evaluate(() => Object.assign(window, { e2eSameDocument: true }));
    const timelineFetches: string[] = [];
    viewer.on("request", (req) => {
      if (new URL(req.url()).pathname === `/api/v1/rooms/${roomId}/messages`) {
        timelineFetches.push(req.url());
      }
    });

    const text = "Deploy finished on the e2e line";
    const composer = admin.getByRole("textbox", { name: "Message e2e-wire" });
    await composer.fill(text);
    await admin.getByRole("button", { name: "Send" }).click();
    // The live event can render the message before the composer clears, so look in the
    // timeline only, then check the composer was cleared.
    const inTimeline = (page: typeof admin) =>
      page.locator('[data-testid^="message-"]').getByText(text, { exact: true });
    await expect(inTimeline(admin)).toBeVisible();
    await expect(composer).toHaveValue("");

    await expect(inTimeline(viewer)).toBeVisible();
    expect(await viewer.evaluate(() => "e2eSameDocument" in window)).toBe(true);
    expect(timelineFetches).toEqual([]);
  } finally {
    await adminCtx.close();
    await memberCtx.close();
  }
});

test("connect an agent shows the install and setup commands and each harness's start command, with no token (UI-14, WIZ-01)", async ({
  context,
  page,
  instance,
}) => {
  await loginAs(context, instance, ADMIN);
  const before = await apiCall<List<Token>>(context.request, instance, "GET", "/tokens");
  await page.goto("/contacts");
  await page.getByRole("button", { name: "Connect an agent" }).click();
  const dialog = page.getByRole("dialog", { name: "Connect an agent" });
  const code = (label: string) => dialog.getByRole("figure", { name: label }).locator("pre");

  // Install from this instance (the script pre-fills its URL), then approve the machine once.
  await expect(code("Install command")).toHaveText(`curl -fsSL ${instance.url}/install.sh | sh`);
  await expect(code("Setup command")).toHaveText("partyline setup --browser");

  // Every harness starts with its launcher, with one line on how messages reach it.
  const PUSH = "Room messages arrive in the running session as they come in.";
  const TURN_END =
    "Messages arrive when it finishes its current turn, or when it calls wait_for_messages.";
  const harnesses: [tab: string, start: string, delivery: string, manual: string][] = [
    ["Claude Code", "partyline claude", PUSH, ".mcp.json"],
    ["OpenCode", "partyline opencode", PUSH, "~/.config/opencode/opencode.json"],
    ["pi", "partyline pi", PUSH, "~/.pi/agent/mcp.json"],
    ["omp", "partyline omp", PUSH, "~/.omp/agent/mcp.json"],
    ["Codex", "partyline codex", TURN_END, "~/.codex/config.toml"],
    ["Gemini", "partyline gemini", TURN_END, "~/.gemini/settings.json"],
  ];
  const tabs = dialog.getByRole("tablist", { name: "Harness" });
  await expect(tabs.getByRole("tab")).toHaveText(harnesses.map(([tab]) => tab));
  await dialog.getByText("Advanced: register by hand").click();
  for (const [tab, start, delivery, manual] of harnesses) {
    await tabs.getByRole("tab", { name: tab, exact: true }).click();
    const panel = dialog.getByRole("tabpanel");
    await expect(panel.getByRole("figure", { name: "Start command" }).locator("pre")).toHaveText(
      start,
    );
    await expect(panel.getByText(delivery, { exact: true })).toBeVisible();
    // The manual entry runs partyline, which reads the URL and token from its config file.
    const entry = (await code(manual).textContent()) ?? "";
    expect(entry, tab).toContain("partyline");
    expect(entry, tab).not.toMatch(/token|PARTYLINE_|pl_agent|https?:/i);
  }

  // Nothing in the wizard creates a token: setup gets one through the browser approval.
  const after = await apiCall<List<Token>>(context.request, instance, "GET", "/tokens");
  expect(after.items).toEqual(before.items);
});

test("a machine is approved by typing its code; an old link with the code pre-fills nothing (WIZ-02, DEV-06)", async ({
  context,
  page,
  instance,
}) => {
  // What `partyline setup --browser` does: create a request anonymously, then poll it.
  const created = await (
    await context.request.post(`${instance.url}/api/v1/devices`, { data: { machine: "e2e-box" } })
  ).json();
  expect(created.approve_url).toBe(`${instance.url}/devices`);
  await loginAs(context, instance, ADMIN);

  await page.goto(`/devices/${created.code}`);
  await expect(page).toHaveURL(`${instance.url}/devices`);
  const input = page.getByLabel("Code from your terminal");
  await expect(input).toHaveValue("");

  await input.fill(`${created.code.slice(0, 4)}-${created.code.slice(4)}`.toLowerCase());
  await page.getByRole("button", { name: "Continue" }).click();
  const request = page.getByRole("region", { name: "Approval request" });
  await expect(request.getByText("e2e-box")).toBeVisible();
  await page.getByRole("button", { name: "Approve", exact: true }).click();
  await expect(page.getByRole("heading", { name: "e2e-box is approved" })).toBeVisible();

  const poll = await (
    await context.request.post(`${instance.url}/api/v1/devices/poll`, {
      data: { poll_secret: created.poll_secret },
      headers: { Origin: instance.url },
    })
  ).json();
  expect(poll.status).toBe("approved");
  expect(poll.token).toMatch(/^pl_agent_/);
  expect(poll.approved_by).toBe(ADMIN.username);
});

test("the messenger opens a conversation from the list and comes back (LAY-01, LAY-05)", async ({
  context,
  page,
  instance,
}, testInfo) => {
  await loginAs(context, instance, ADMIN);
  const room = await apiCall<Room>(context.request, instance, "POST", "/groups", {
    name: "e2e-messenger",
    members: [],
  });

  await page.goto("/");
  await expect(page.getByRole("heading", { name: homeHeading(testInfo), level: 1 })).toBeVisible();
  const list = page.getByRole("list", { name: "Conversations" });
  const row = list.getByRole("link", { name: /e2e-messenger/ });
  await expect(row).toBeVisible();

  await row.click();
  await expect(page).toHaveURL(`${instance.url}/rooms/${room.id}`);
  await expect(page.getByRole("heading", { name: "e2e-messenger", level: 1 })).toBeVisible();

  if (isMobile(testInfo)) {
    // LAY-05: the conversation owns the screen, and its back button returns to the list.
    await expect(page.getByRole("complementary", { name: "Conversations" })).toHaveCount(0);
    await page.getByRole("link", { name: "Back to conversations" }).click();
  } else {
    // LAY-01: the list stays beside the conversation and marks the open one.
    await expect(page.getByRole("complementary", { name: "Conversations" })).toBeVisible();
    await expect(list.getByRole("link", { name: /e2e-messenger/ })).toHaveAttribute(
      "aria-current",
      "page",
    );
    await page.goBack();
  }

  await expect(page).toHaveURL(`${instance.url}/`);
  await expect(page.getByRole("heading", { name: homeHeading(testInfo), level: 1 })).toBeVisible();
  await expect(
    page.getByRole("list", { name: "Conversations" }).getByRole("link", { name: /e2e-messenger/ }),
  ).toBeVisible();
});

test("a direct message with an AI carries both ways: its post arrives live and the web post reaches it (DM-01)", async ({
  context,
  page,
  instance,
}, testInfo) => {
  await loginAs(context, instance, ADMIN);
  const agent = await connectAI(context, instance, "/work/atlas");
  const name = agent.ai.display_name;

  const live = trackLive(page);
  await page.goto("/contacts");
  await page.getByRole("button", { name: `Message ${name}` }).click();
  await expect(page).toHaveURL(new RegExp(`^${instance.url}/rooms/[^/]+$`));
  const roomId = new URL(page.url()).pathname.split("/").pop() ?? "";
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
  // A DM has exactly two participants and no way to change them (DM-02).
  await expect(page.getByRole("button", { name: "Participants (2)" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "No messages yet" })).toBeVisible();
  await expect.poll(() => live.ready).toBe(true);

  // Marks this document: a reload would drop it. Timeline fetches are counted from here on.
  await page.evaluate(() => Object.assign(window, { e2eSameDocument: true }));
  const timelineFetches: string[] = [];
  page.on("request", (req) => {
    if (new URL(req.url()).pathname === `/api/v1/rooms/${roomId}/messages`) {
      timelineFetches.push(req.url());
    }
  });

  // The AI sees the same DM from its folder and posts in it.
  const rooms = await agentJSON<List<AgentRoom>>(context, instance, agent, "GET", "/agent/rooms");
  expect(rooms.items.find((r) => r.id === roomId)?.kind).toBe("dm");
  const fromAI = "Atlas is listening on the e2e line";
  await agentJSON<AgentMessage>(
    context,
    instance,
    agent,
    "POST",
    `/agent/rooms/${roomId}/messages`,
    { text: fromAI },
  );
  const timeline = page.locator('[data-testid^="message-"]');
  await expect(timeline.getByText(fromAI, { exact: true })).toBeVisible();
  expect(await page.evaluate(() => "e2eSameDocument" in window)).toBe(true);
  expect(timelineFetches).toEqual([]);

  // The other way: the AI reads the web's message, authored by its owner and so trusted.
  const fromWeb = "Here it is on the web side";
  await page.getByRole("textbox", { name: `Message ${name}` }).fill(fromWeb);
  await page.getByRole("button", { name: "Send" }).click();
  await expect(timeline.getByText(fromWeb, { exact: true })).toBeVisible();
  await expect
    .poll(async () => {
      const history = await agentJSON<ApiPage<AgentMessage>>(
        context,
        instance,
        agent,
        "GET",
        `/agent/rooms/${roomId}/messages?limit=20`,
      );
      const seen = history.items.find((m) => m.message.text === fromWeb);
      return seen ? `${seen.trusted}:${seen.message.author_name}` : "missing";
    })
    .toBe(`true:${ADMIN.username}`);

  // DM-01: "Message" again opens the same conversation, never a second one.
  await page.goto("/contacts");
  await page.getByRole("button", { name: `Message ${name}` }).click();
  await expect(page).toHaveURL(`${instance.url}/rooms/${roomId}`);
  await expect(timeline.getByText(fromAI, { exact: true })).toBeVisible();

  // Its one row in the list is titled by the other participant, with the last message (LAY-01).
  if (isMobile(testInfo)) {
    await page.getByRole("link", { name: "Back to conversations" }).click();
  }
  const row = page
    .getByRole("list", { name: "Conversations" })
    .getByRole("link", { name: new RegExp(name) });
  await expect(row).toHaveCount(1);
  await expect(row.getByText(fromWeb, { exact: true })).toBeVisible();
  await expect(row.locator("time")).toHaveCount(1);
});

test("a group is created from the list with an AI as its member (LAY-01, LAY-04)", async ({
  context,
  page,
  instance,
}, testInfo) => {
  await loginAs(context, instance, ADMIN);
  const agent = await connectAI(context, instance, "/work/crew");
  const name = agent.ai.display_name;

  await page.goto("/");
  await page.getByRole("button", { name: "New group" }).click();
  const dialog = page.getByRole("dialog", { name: "New group" });
  await dialog.getByLabel("Name", { exact: true }).fill("e2e-crew");
  await dialog.getByRole("checkbox", { name }).check();
  await dialog.getByRole("button", { name: "Create group" }).click();

  await expect(page).toHaveURL(new RegExp(`^${instance.url}/rooms/[^/]+$`));
  await expect(page.getByRole("heading", { name: "e2e-crew", level: 1 })).toBeVisible();
  // The AI it picked is its one member; the creator reaches it as the owner.
  await expect(page.getByRole("button", { name: "Members (1)" })).toBeVisible();
  if (!isMobile(testInfo)) {
    const panel = page.getByRole("complementary", { name: "Members" });
    await expect(
      panel.getByRole("list", { name: "Members" }).getByText(name, { exact: true }),
    ).toBeVisible();
  }

  // On a phone the list is the home screen again; beside the sidebar it is already there.
  if (isMobile(testInfo)) {
    await page.getByRole("link", { name: "Back to conversations" }).click();
  }
  await expect(
    page.getByRole("list", { name: "Conversations" }).getByRole("link", { name: /e2e-crew/ }),
  ).toBeVisible();
});

test("people and AIs look the same: one member list, no kind badge, the same faces (AD-024)", async ({
  context,
  page,
  instance,
}, testInfo) => {
  await loginAs(context, instance, ADMIN);
  const me = await apiCall<Me>(context.request, instance, "GET", "/me");
  const agent = await connectAI(context, instance, "/work/mirror");
  const name = agent.ai.display_name;
  // The member the second test of this file made is in the admin's organization: a group of the
  // admin, that member and the AI has two users and an AI, in the order they joined.
  const contacts = await apiCall<Contacts>(context.request, instance, "GET", "/contacts");
  const other = contacts.users.find((u) => u.handle === MEMBER.username);
  expect(other, "the member created by the live-message test").toBeDefined();
  const room = await apiCall<Room>(context.request, instance, "POST", "/groups", {
    name: "e2e-mirror",
    members: [me.id, agent.ai.id, other?.id],
  });
  await apiCall(context.request, instance, "POST", "/conversations/dm", { with: agent.ai.id });
  await agentJSON<AgentMessage>(
    context,
    instance,
    agent,
    "POST",
    `/agent/rooms/${room.id}/messages`,
    { text: "Release is green." },
  );
  await apiCall(context.request, instance, "POST", `/rooms/${room.id}/messages`, {
    text: "Thanks, shipping it.",
  });

  // The timeline names both authors and marks neither, for the eye or for a screen reader.
  await page.goto(`/rooms/${room.id}`);
  const log = page.getByRole("log");
  await expect(log.getByText("Release is green.")).toBeVisible();
  await expect(log.getByRole("link", { name, exact: true })).toBeVisible();
  await expect(log.locator('[data-testid="intelligence-avatar"]')).toHaveCount(2);
  for (const mark of ["AI", "IA", "Agent", "Person"]) {
    await expect(log.getByText(mark, { exact: true })).toHaveCount(0);
  }
  await shoot(page, "conversation", testInfo);

  // The member list is one list, the AI among the users, with no Agents and Users sections.
  if (isMobile(testInfo)) await page.getByRole("button", { name: "Members (3)" }).click();
  const members = page.getByRole("list", { name: "Members" });
  await expect(members.getByRole("listitem")).toHaveCount(3);
  await expect(members.getByRole("link", { name, exact: true })).toBeVisible();
  await expect(members.locator('[data-testid="intelligence-avatar"]')).toHaveCount(3);
  for (const mark of ["Agents", "Users", "AI", "IA"]) {
    await expect(page.getByText(mark, { exact: true })).toHaveCount(0);
  }
  await shoot(page, "members", testInfo);

  // The sidebar lists the direct message with the AI and the group by name alone.
  await page.goto("/");
  const list = page.getByRole("list", { name: "Conversations" });
  await expect(list.getByRole("link", { name: new RegExp(name) })).toBeVisible();
  await expect(list.getByRole("link", { name: /e2e-mirror/ })).toBeVisible();
  for (const mark of ["AI", "IA", "Agent", "Person"]) {
    await expect(list.getByText(mark, { exact: true })).toHaveCount(0);
  }
});

/** A group of the admin with these messages, posted as the admin. */
async function groupWith(
  context: BrowserContext,
  instance: Instance,
  name: string,
  texts: string[],
): Promise<Room> {
  const room = await apiCall<Room>(context.request, instance, "POST", "/groups", {
    name,
    members: [],
  });
  for (const text of texts) {
    await apiCall(context.request, instance, "POST", `/rooms/${room.id}/messages`, { text });
  }
  return room;
}

/** Opens a conversation from the sidebar's list; on a phone that list is a page of its own. */
async function openConversation(page: Page, testInfo: TestInfo, name: RegExp | string) {
  const back = page.getByRole("link", { name: "Back to conversations" });
  if (isMobile(testInfo) && (await back.count()) > 0) await back.click();
  await page.getByRole("list", { name: "Conversations" }).getByRole("link", { name }).click();
}

/** How far the timeline is from its newest message, in pixels. */
const distanceFromNewest = (page: Page) =>
  page.getByRole("log").evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight);

/** Where a message sits in the timeline's window, from its top edge. */
const placeInTimeline = (page: Page, text: string) =>
  page
    .getByRole("log")
    .getByText(text, { exact: true })
    .evaluate(
      (el) =>
        el.getBoundingClientRect().top -
        (el.closest('[role="log"]') as HTMLElement).getBoundingClientRect().top,
    );

test("a conversation opens at its newest message whichever one you come from, and older pages keep the reading place (UI-26)", async ({
  context,
  page,
  instance,
}, testInfo) => {
  test.setTimeout(120_000);
  await loginAs(context, instance, ADMIN);
  // 70 messages are two pages (50 and 20); a few tall ones scroll a little; one room has none.
  await groupWith(
    context,
    instance,
    "e2e-scroll",
    Array.from({ length: 70 }, (_, i) => `scroll ${i + 1}`),
  );
  await groupWith(
    context,
    instance,
    "e2e-brief",
    Array.from({ length: 8 }, (_, i) => `brief ${i + 1}\nsecond line\nthird line\nfourth line`),
  );
  await groupWith(context, instance, "e2e-bare", []);
  const log = page.getByRole("log");
  const atNewest = async (text: string | RegExp) => {
    await expect(log.getByText(text, { exact: typeof text === "string" })).toBeInViewport();
    await expect.poll(() => distanceFromNewest(page)).toBeLessThanOrEqual(64);
  };

  await page.goto("/");
  await openConversation(page, testInfo, /e2e-scroll/);
  await atNewest("scroll 70");

  // The short conversation is left wherever its few tall messages scroll to: the router used to
  // carry that into the long one, cached by now, which opened in its middle, or at its top.
  await openConversation(page, testInfo, /e2e-brief/);
  await atNewest(/^brief 8/);
  await openConversation(page, testInfo, /e2e-scroll/);
  await atNewest("scroll 70");

  // Reading older messages: the page that arrives above does not move what is being read.
  const older = page.waitForResponse((res) => res.url().includes("before_seq="));
  await log.evaluate((el) => {
    el.scrollTop = 0;
  });
  await expect(log.getByText("scroll 21", { exact: true })).toBeInViewport();
  const place = await placeInTimeline(page, "scroll 21");
  await older;
  await expect(log.getByText("scroll 20", { exact: true })).toBeAttached();
  await expect
    .poll(async () => Math.abs((await placeInTimeline(page, "scroll 21")) - place))
    .toBeLessThanOrEqual(2);

  // Leaving it up there and coming back, from an empty conversation too, lands on the newest again.
  await openConversation(page, testInfo, /e2e-bare/);
  await expect(page.getByRole("heading", { name: "No messages yet" })).toBeVisible();
  await openConversation(page, testInfo, /e2e-scroll/);
  await atNewest("scroll 70");
  await openConversation(page, testInfo, /e2e-brief/);
  await atNewest(/^brief 8/);
});

test("a conversation whose pictures load late stays at its newest message (UI-26)", async ({
  context,
  page,
  instance,
}, testInfo) => {
  test.setTimeout(120_000);
  await loginAs(context, instance, ADMIN);
  const room = await groupWith(
    context,
    instance,
    "e2e-late",
    Array.from({ length: 12 }, (_, i) => `late ${i + 1}`),
  );
  await postPictures(
    context.request,
    instance,
    room.id,
    [{ name: "late-1.png", hue: 40 }],
    "late pictures 1",
  );
  await postPictures(
    context.request,
    instance,
    room.id,
    [{ name: "late-2.png", hue: 200 }],
    "late pictures 2",
  );
  await groupWith(context, instance, "e2e-other", ["other 1"]);

  // Every picture takes a second to arrive, after the page has been put at the newest message.
  await page.route(/\/api\/v1\/attachments\//, async (route) => {
    await new Promise((done) => setTimeout(done, 1_000));
    await route.continue();
  });
  await page.goto("/");
  await openConversation(page, testInfo, /e2e-other/);
  await expect(page.getByRole("log").getByText("other 1", { exact: true })).toBeInViewport();
  const pictures = page.waitForResponse(
    (res) => res.url().includes("/api/v1/attachments/") && res.ok(),
  );
  await openConversation(page, testInfo, /e2e-late/);
  const newest = page.getByRole("log").getByText("late pictures 2", { exact: true });
  await expect(newest).toBeInViewport();
  await pictures;
  await page.waitForTimeout(1_500);
  await expect(newest).toBeInViewport();
  await expect.poll(() => distanceFromNewest(page)).toBeLessThanOrEqual(64);
});

test("the contacts page groups AIs by organization and names no machine (ORG-02, AD-022)", async ({
  context,
  page,
  instance,
}) => {
  await loginAs(context, instance, ADMIN);
  const atlas = await connectAI(context, instance, "/work/atlas");
  const notes = await connectAI(context, instance, "/work/atlas/notes");
  const scribe = await connectAI(context, instance, "/opt/scribe", "e2e-laptop");

  await page.goto("/contacts");
  await expect(page.getByRole("heading", { name: "Contacts", level: 1 })).toBeVisible();
  // The user started the three AIs, so their home is the user's default organization: one section holds them
  // all, whatever machine each runs on, with the folder it works in.
  const home = page.getByRole("region").filter({ has: page.getByTestId(`contact-${atlas.ai.id}`) });
  await expect(home.getByTestId(`contact-${notes.ai.id}`)).toBeVisible();
  await expect(home.getByTestId(`contact-${scribe.ai.id}`)).toBeVisible();
  await expect(home.getByTestId(`contact-${atlas.ai.id}`).getByText("/work/atlas")).toBeVisible();
  // No machine is a group, a label or a hostname.
  await expect(page.getByRole("region", { name: MACHINE })).toHaveCount(0);
  await expect(page.getByText(MACHINE)).toHaveCount(0);
  await expect(page.getByText("e2e-laptop")).toHaveCount(0);
});

test("a profile photo is uploaded from the profile page and shows across the shell (PROF-03)", async ({
  context,
  page,
  instance,
}) => {
  await loginAs(context, instance, ADMIN);
  const me = await apiCall<Me>(context.request, instance, "GET", "/me");
  await page.goto(`/contacts/${me.intelligence.id}`);
  await expect(
    page.getByRole("heading", { name: me.intelligence.display_name, level: 1 }),
  ).toBeVisible();

  await page.getByLabel("Upload photo").setInputFiles({
    name: "e2e-avatar.png",
    mimeType: "image/png",
    buffer: PHOTO,
  });

  // The photo is sent as soon as it is chosen: no confirmation click. The stored photo replaces
  // the initials on the profile, before and after a reload.
  const photo = new RegExp(`/api/v1/intelligences/${me.intelligence.id}/avatar\\?v=`);
  const face = page.locator('[data-testid="intelligence-avatar"] img').first();
  await expect(face).toHaveAttribute("src", photo);
  await page.reload();
  await expect(page.locator('[data-testid="intelligence-avatar"] img').first()).toHaveAttribute(
    "src",
    photo,
  );

  // PROF-03: the photo shows wherever the user appears, the account menu included.
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Account menu" }).locator("img")).toHaveAttribute(
    "src",
    photo,
  );
});

test("main pages fit the viewport with no horizontal page scroll (UI-24, LAY-05)", async ({
  context,
  page,
  instance,
}, testInfo) => {
  await loginAs(context, instance, ADMIN);
  const me = await apiCall<Me>(context.request, instance, "GET", "/me");
  const room = await apiCall<Room>(context.request, instance, "POST", "/groups", {
    name: "e2e-narrow",
    members: [],
  });
  // A long token with no break opportunity (no spaces or hyphens) is the usual way a
  // timeline row pushes a page sideways; only `break-words` can wrap it.
  await apiCall(context.request, instance, "POST", `/rooms/${room.id}/messages`, {
    text: `See ${"averylongunbrokentoken".repeat(12)} for details`,
  });

  // The sidebar row of that room previews the same unbroken line: it must be cut, not widen.
  const pages: [string, string][] = [
    ["/", homeHeading(testInfo)],
    [`/rooms/${room.id}`, "e2e-narrow"],
    ["/contacts", "Contacts"],
    [`/contacts/${me.intelligence.id}`, me.intelligence.display_name],
    ["/connectors", "Connectors"],
    ["/settings", "Settings"],
  ];
  for (const [path, heading] of pages) {
    await page.goto(path);
    await expect(page.getByRole("heading", { name: heading, level: 1 })).toBeVisible();
    if (path === `/rooms/${room.id}`) {
      // The room page scrolls inside its timeline, so also check the long line wraps on screen.
      // The sidebar row of the room previews the same text: look in the timeline.
      const line = page.getByRole("log").getByText(/averylongunbrokentoken/);
      await expect(line).toBeVisible();
      const right = await line.evaluate((el) => {
        const range = document.createRange();
        range.selectNodeContents(el);
        return range.getBoundingClientRect().right;
      });
      expect(right, "long message runs off screen").toBeLessThanOrEqual(
        await page.evaluate(() => window.innerWidth),
      );
    }
    const width = await page.evaluate(() => ({
      scroll: document.documentElement.scrollWidth,
      viewport: window.innerWidth,
    }));
    expect(width.scroll, `${path} scrolls sideways`).toBeLessThanOrEqual(width.viewport);
  }
});

test("an image opens in the viewer inside the app, the others are an arrow away and focus comes back (IMG-01..IMG-06)", async ({
  context,
  page,
  instance,
}, testInfo) => {
  await loginAs(context, instance, ADMIN);
  const room = await apiCall<Room>(context.request, instance, "POST", "/groups", {
    name: "e2e-gallery",
    members: [],
  });
  await postPictures(
    context.request,
    instance,
    room.id,
    [
      { name: "harbour.png", hue: 200 },
      { name: "market.png", hue: 30 },
    ],
    "Two from the trip",
  );
  await postPictures(context.request, instance, room.id, [{ name: "sunset.png", hue: 340 }]);
  const tabs: string[] = [];
  context.on("page", (opened) => tabs.push(opened.url()));

  await page.goto(`/rooms/${room.id}`);
  const timeline = page.getByRole("log");
  const thumbnail = (name: string) => timeline.getByRole("button", { name: `Open ${name}` });
  await thumbnail("market.png").click();

  // IMG-01, IMG-02: a dialog over the conversation, the picture fitted to the screen, no new tab.
  const viewer = page.getByRole("dialog", { name: "market.png" });
  await expect(viewer).toBeVisible();
  await expect(viewer.getByText("2 of 3")).toBeVisible();
  expect(tabs).toEqual([]);
  await expect(page).toHaveURL(`${instance.url}/rooms/${room.id}`);
  const picture = viewer.getByRole("img", { name: "market.png" });
  await expect
    .poll(() => picture.evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth))
    .toBe(1500);
  const screen = page.viewportSize() ?? { width: 0, height: 0 };
  const box = await picture.boundingBox();
  expect(box?.width).toBeLessThanOrEqual(screen.width);
  expect(box?.height).toBeLessThanOrEqual(screen.height);
  // Scaled down, not cropped: the whole 3:2 picture is on screen.
  expect(box?.width).toBeLessThan(1500);
  expect((box?.width ?? 0) / (box?.height ?? 1)).toBeCloseTo(1.5, 1);
  await expect(viewer.getByRole("link", { name: "Download" })).toHaveAttribute(
    "download",
    "market.png",
  );
  await expect(viewer.getByRole("link", { name: "Open original" })).toHaveAttribute(
    "target",
    "_blank",
  );

  // IMG-06: everything fits a phone, and every control is a finger wide.
  const width = await page.evaluate(() => ({
    scroll: document.documentElement.scrollWidth,
    viewport: window.innerWidth,
  }));
  expect(width.scroll, "the viewer scrolls sideways").toBeLessThanOrEqual(width.viewport);
  const controls = [
    ...(await viewer.getByRole("button").all()),
    ...(await viewer.getByRole("link").all()),
  ];
  // Close, Previous, Next, Download and Open original.
  expect(controls).toHaveLength(5);
  await settled(page);
  const finger = isMobile(testInfo) ? 44 : 32;
  for (const control of controls) {
    const label = (await control.getAttribute("aria-label")) ?? (await control.textContent());
    const size = await control.boundingBox();
    expect(size?.width, `${label} is too narrow`).toBeGreaterThanOrEqual(finger);
    expect(size?.height, `${label} is too short`).toBeGreaterThanOrEqual(finger);
  }
  await shoot(page, "lightbox", testInfo);

  // IMG-04: the arrows walk the other images and stop at both ends; so does a swipe.
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("dialog", { name: "sunset.png" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Next image" })).toBeDisabled();
  await page.keyboard.press("ArrowLeft");
  await expect(page.getByRole("dialog", { name: "market.png" })).toBeVisible();
  const stage = page.getByTestId("image-viewer-backdrop");
  const swipe = async (from: number, to: number) => {
    await stage.dispatchEvent("pointerdown", { pointerType: "touch", clientX: from, clientY: 300 });
    await stage.dispatchEvent("pointerup", { pointerType: "touch", clientX: to, clientY: 306 });
  };
  await swipe(60, 240);
  await expect(page.getByRole("dialog", { name: "harbour.png" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Previous image" })).toBeDisabled();

  // IMG-03: Escape closes it and focus is on the thumbnail of the image it showed last, which is
  // not the one that opened it.
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(thumbnail("harbour.png")).toBeFocused();

  // The close button and the backdrop close it too.
  await thumbnail("sunset.png").click();
  await page
    .getByRole("dialog", { name: "sunset.png" })
    .getByRole("button", { name: "Close" })
    .click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(thumbnail("sunset.png")).toBeFocused();
  await thumbnail("sunset.png").click();
  await expect(page.getByRole("dialog", { name: "sunset.png" })).toBeVisible();
  await page.getByTestId("image-viewer-backdrop").click({ position: { x: 4, y: 4 } });
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(tabs).toEqual([]);
});

test("Enter sends with a mouse and adds a line on a phone, where only Send sends (ENT-01..ENT-04)", async ({
  browser,
  context,
  page,
  instance,
}, testInfo) => {
  await loginAs(context, instance, ADMIN);
  const room = await apiCall<Room>(context.request, instance, "POST", "/groups", {
    name: "e2e-enter",
    members: [],
  });
  const message = (on: Page) => on.locator('[data-testid^="message-"]');

  // A mouse: Enter sends, Shift+Enter adds a line, and the Return key of a screen keyboard says send.
  await page.goto(`/rooms/${room.id}`);
  const box = page.getByRole("textbox", { name: "Message e2e-enter" });
  await expect(box).toHaveAttribute("enterkeyhint", "send");
  if (!isMobile(testInfo)) {
    await expect(page.getByText("Enter to send · Shift+Enter for a new line")).toBeVisible();
  }
  await box.fill("first line");
  await box.press("Shift+Enter");
  await box.pressSequentially("second line");
  await expect(box).toHaveValue("first line\nsecond line");
  await box.press("Enter");
  await expect(message(page)).toContainText("second line");
  await expect(box).toHaveValue("");

  // A phone: Enter writes, Send sends, and the hint says so at 360 px without breaking the row.
  const phone = await browser.newContext({
    viewport: { width: 360, height: 780 },
    hasTouch: true,
    isMobile: true,
  });
  try {
    await loginAs(phone, instance, ADMIN);
    const touch = await phone.newPage();
    await touch.goto(`/rooms/${room.id}`);
    const field = touch.getByRole("textbox", { name: "Message e2e-enter" });
    await expect(field).toHaveAttribute("enterkeyhint", "enter");
    const hint = touch.getByText("Enter for a new line · tap Send to send");
    await expect(hint).toBeVisible();
    const send = touch.getByRole("button", { name: "Send" });
    const [hintBox, sendBox] = [await hint.boundingBox(), await send.boundingBox()];
    expect((hintBox?.x ?? 0) + (hintBox?.width ?? 0)).toBeLessThanOrEqual(sendBox?.x ?? 0);

    await field.tap();
    await touch.keyboard.type("one");
    await touch.keyboard.press("Enter");
    await touch.keyboard.type("two");
    await expect(field).toHaveValue("one\ntwo");
    // Enter wrote a line: nothing was sent besides the mouse's message above.
    await expect(message(touch)).toHaveCount(1);
    await send.tap();
    await expect(message(touch).last()).toContainText("two");
    await expect(field).toHaveValue("");
  } finally {
    await phone.close();
  }
});

test("a click on a name opens the profile panel: a sidebar on a wide screen, a drawer on a phone (PRF-01..PRF-05)", async ({
  context,
  page,
  instance,
}, testInfo) => {
  await loginAs(context, instance, ADMIN);
  const me = await apiCall<Me>(context.request, instance, "GET", "/me");
  const agent = await connectAI(context, instance, "/work/panel");
  const name = agent.ai.display_name;
  await apiCall(context.request, instance, "PATCH", `/intelligences/${agent.ai.id}`, {
    bio: "Keeps the release train on time. Answers in the room, or in a DM.",
  });
  const room = await apiCall<Room>(context.request, instance, "POST", "/groups", {
    name: "e2e-panel",
    members: [],
  });
  await apiCall(context.request, instance, "POST", `/rooms/${room.id}/members`, {
    kind: "agent",
    member_id: agent.ai.id,
  });
  await agentJSON<AgentMessage>(
    context,
    instance,
    agent,
    "POST",
    `/agent/rooms/${room.id}/messages`,
    { text: "Release is green." },
  );
  await apiCall(context.request, instance, "POST", `/rooms/${room.id}/messages`, {
    text: "Thanks, shipping it.",
  });

  // PRF-01: a click on the author's name opens the panel, and the page stays where it is.
  await page.goto(`/rooms/${room.id}`);
  const author = page.getByRole("log").getByRole("link", { name, exact: true });
  await author.click();
  const panel = page.getByRole("dialog", { name });
  await expect(panel).toBeVisible();
  await expect(page).toHaveURL(`${instance.url}/rooms/${room.id}`);

  // PRF-02: the right edge of a wide screen, the bottom of a phone's.
  await settled(page);
  const screen = page.viewportSize() ?? { width: 0, height: 0 };
  const box = await panel.boundingBox();
  if (isMobile(testInfo)) {
    await expect(panel).toHaveAttribute("data-layout", "drawer");
    expect(box?.x).toBe(0);
    expect(box?.width).toBe(screen.width);
    expect((box?.y ?? 0) + (box?.height ?? 0)).toBeCloseTo(screen.height, 0);
    expect(box?.y ?? 0).toBeGreaterThan(0);
  } else {
    await expect(panel).toHaveAttribute("data-layout", "sidebar");
    expect((box?.x ?? 0) + (box?.width ?? 0)).toBeCloseTo(screen.width, 0);
    expect(box?.height).toBeCloseTo(screen.height, 0);
    expect(box?.width ?? 0).toBeLessThanOrEqual(384);
  }

  // PRF-03: who it is, and what it can do.
  await expect(panel.getByRole("heading", { name })).toBeVisible();
  await expect(panel.getByText(`@${agent.ai.name}`, { exact: true })).toBeVisible();
  await expect(panel.getByText("Keeps the release train on time.")).toBeVisible();
  // An AI is not marked as one (AD-024): no kind row, no badge; what is true of anyone stays.
  await expect(panel.getByText("Kind", { exact: true })).toHaveCount(0);
  await expect(panel.getByText("AI", { exact: true })).toHaveCount(0);
  await expect(panel.getByText("Offline", { exact: true })).toBeVisible();
  // The organization the viewer shares with the AI is its home (OMEM-03). Where the AI runs is not
  // shown (AD-022), and nothing is shared one by one (AD-016).
  const orgs = panel.getByRole("region", { name: "Organizations" });
  await expect(orgs).toBeVisible();
  await expect(orgs.getByText("Home organization")).toBeVisible();
  await expect(panel.getByText(MACHINE)).toHaveCount(0);
  await expect(panel.getByRole("button", { name: /share/i })).toHaveCount(0);
  const message = panel.getByRole("button", { name: "Message", exact: true });
  const full = panel.getByRole("link", { name: "Open full profile" });
  await expect(message).toBeVisible();
  await expect(full).toHaveAttribute("href", `/contacts/${agent.ai.id}`);
  // The viewer manages this AI, and still edits it from its page, not from here.
  await expect(panel.getByRole("link", { name: "Edit profile" })).toHaveCount(0);
  if (isMobile(testInfo)) {
    for (const control of [message, full, panel.getByRole("button", { name: "Close" })]) {
      const size = await control.boundingBox();
      expect(size?.height ?? 0).toBeGreaterThanOrEqual(44);
      expect(size?.width ?? 0).toBeGreaterThanOrEqual(44);
    }
  }
  await shoot(page, "profile-panel", testInfo);

  // PRF-05: Escape closes it and focus is back on the name.
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(author).toBeFocused();

  // The viewer's own profile offers Edit profile, not Message.
  const mine = page
    .getByRole("log")
    .getByRole("link", { name: me.intelligence.display_name, exact: true });
  await mine.click();
  const own = page.getByRole("dialog", { name: me.intelligence.display_name });
  await expect(own.getByRole("link", { name: "Edit profile" })).toBeVisible();
  await expect(own.getByRole("button", { name: "Message", exact: true })).toHaveCount(0);
  await own.getByRole("button", { name: "Close" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(mine).toBeFocused();

  // A member of the list opens it too (wide screens show the list beside the conversation).
  if (!isMobile(testInfo)) {
    await page
      .getByRole("complementary", { name: "Members" })
      .getByRole("link", { name, exact: true })
      .click();
    await expect(page.getByRole("dialog", { name })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog")).toHaveCount(0);
  }

  // The contacts page shows it instead of leaving the list.
  await page.goto("/contacts");
  await page
    .getByTestId(`contact-${agent.ai.id}`)
    .getByRole("link", { name: new RegExp(name) })
    .click();
  await expect(page.getByRole("dialog", { name })).toBeVisible();
  await expect(page).toHaveURL(`${instance.url}/contacts`);

  // PRF-04: the full page is one action away, and the panel makes way for it.
  await page.getByRole("dialog", { name }).getByRole("link", { name: "Open full profile" }).click();
  await expect(page).toHaveURL(`${instance.url}/contacts/${agent.ai.id}`);
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("a message is edited from its menu and deleted after a question; a second window and the AI's own edits follow live (EDT-01, EDT-10, DLT-01, DLT-09, DLT-10)", async ({
  browser,
  context,
  page,
  instance,
}, testInfo) => {
  await loginAs(context, instance, ADMIN);
  const agent = await connectAI(context, instance, "/work/editor");
  const room = await apiCall<Room>(context.request, instance, "POST", "/groups", {
    name: "e2e-edit",
    members: [],
  });
  await apiCall(context.request, instance, "POST", `/rooms/${room.id}/members`, {
    kind: "agent",
    member_id: agent.ai.id,
  });
  const rows = (on: Page) => on.locator('[data-testid^="message-"]');
  const menuOf = (row: Locator) => row.getByRole("button", { name: "Message actions" });
  const box = page.getByRole("textbox", { name: "Message e2e-edit" });

  // A second window of the same account follows everything live, without a reload.
  const other = await browser.newContext();
  try {
    await loginAs(other, instance, ADMIN);
    const watcher = await other.newPage();
    const live = trackLive(watcher);
    await watcher.goto(`/rooms/${room.id}`);
    await expect.poll(() => live.ready).toBe(true);
    await page.goto(`/rooms/${room.id}`);
    await expect(box).toBeVisible();
    await Promise.all(
      [page, watcher].map((on) =>
        on.evaluate(() => Object.assign(window, { e2eSameDocument: true })),
      ),
    );

    // The first message is the viewer's, the second the AI's, so a change to the first shows it keeps its place.
    await box.fill("helo wrld");
    await box.press("Enter");
    await expect(rows(page)).toHaveCount(1);
    await agentJSON<Message>(context, instance, agent, "POST", `/agent/rooms/${room.id}/messages`, {
      text: "noted",
    });
    for (const on of [page, watcher]) {
      await expect(rows(on)).toHaveCount(2);
      await expect(rows(on).nth(0)).toContainText("helo wrld");
      await expect(rows(on).nth(1)).toContainText("noted");
    }

    // EDT-10: Edit loads the text into the composer and focus is in the box; Escape leaves and sends nothing.
    await box.fill("a draft");
    await menuOf(rows(page).nth(0)).click();
    await page.getByRole("menuitem", { name: "Edit" }).click();
    await expect(page.getByText("Editing message")).toBeVisible();
    await expect(page.getByRole("button", { name: "Attach files" })).toHaveCount(0);
    await expect(box).toHaveValue("helo wrld");
    await expect(box).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(page.getByText("Editing message")).toHaveCount(0);
    await expect(box).toHaveValue("a draft");
    await expect(rows(page).nth(0)).not.toContainText("edited");

    // EDT-01: Enter saves; the message keeps its place, says "edited" and the other window gets it live.
    await menuOf(rows(page).nth(0)).click();
    await page.getByRole("menuitem", { name: "Edit" }).click();
    await expect(box).toBeFocused();
    await box.fill("hello world");
    await box.press("Enter");
    await expect(page.getByText("Editing message")).toHaveCount(0);
    await expect(box).toHaveValue("a draft");
    for (const on of [page, watcher]) {
      await expect(rows(on)).toHaveCount(2);
      await expect(rows(on).nth(0)).toContainText("hello world");
      await expect(rows(on).nth(0).getByText("edited", { exact: true })).toBeVisible();
      await expect(rows(on).nth(1)).toContainText("noted");
      await expect(rows(on).nth(1)).not.toContainText("edited");
    }
    await expectNoSidewaysScroll(page, "a conversation with an edited message");

    // The AI's message gets no Edit from the viewer, only Delete as the creator of the group (DLT-02).
    await menuOf(rows(page).nth(1)).click();
    await expect(page.getByRole("menuitem", { name: "Delete" })).toBeVisible();
    await expect(page.getByRole("menuitem", { name: "Edit" })).toHaveCount(0);
    await page.keyboard.press("Escape");
    await expect(page.getByRole("menu")).toHaveCount(0);

    // DLT-09: Delete asks first; Cancel leaves the message, and the page still answers to clicks afterwards.
    await menuOf(rows(page).nth(0)).click();
    await page.getByRole("menuitem", { name: "Delete" }).click();
    const question = page.getByRole("alertdialog", { name: "Delete this message?" });
    await expect(question).toBeVisible();
    await expect(question).toContainText("deleted for everyone");
    await question.getByRole("button", { name: "Cancel" }).click();
    await expect(page.getByRole("alertdialog")).toHaveCount(0);
    await expect(rows(page).nth(0)).toContainText("hello world");
    await box.click();
    await expect(box).toBeFocused();

    // Confirming leaves the placeholder where the message was, here and in the other window, at once.
    await menuOf(rows(page).nth(0)).click();
    await page.getByRole("menuitem", { name: "Delete" }).click();
    await page
      .getByRole("alertdialog", { name: "Delete this message?" })
      .getByRole("button", { name: "Delete" })
      .click();
    await expect(page.getByRole("alertdialog")).toHaveCount(0);
    for (const on of [page, watcher]) {
      await expect(rows(on)).toHaveCount(2);
      await expect(rows(on).nth(0)).toContainText("Message deleted");
      await expect(rows(on).nth(0)).not.toContainText("hello world");
      await expect(menuOf(rows(on).nth(0))).toHaveCount(0);
      await expect(rows(on).nth(1)).toContainText("noted");
    }
    await box.click();
    await expect(box).toBeFocused();
    if (!isMobile(testInfo)) {
      // The list beside the conversation previews the last message, which is the AI's.
      await expect(
        page.getByRole("list", { name: "Conversations" }).getByRole("link", { name: /e2e-edit/ }),
      ).toContainText("noted");
    }

    // EDT-08, DLT-08: the AI changes and deletes its own message through its own routes; both windows follow.
    const mine = await agentJSON<Message>(
      context,
      instance,
      agent,
      "POST",
      `/agent/rooms/${room.id}/messages`,
      { text: "sending the bulid" },
    );
    for (const on of [page, watcher]) await expect(rows(on)).toHaveCount(3);
    await agentJSON<Message>(context, instance, agent, "PATCH", `/agent/messages/${mine.id}`, {
      text: "sending the build",
    });
    for (const on of [page, watcher]) {
      await expect(rows(on).nth(2)).toContainText("sending the build");
      await expect(rows(on).nth(2).getByText("edited", { exact: true })).toBeVisible();
    }
    await agentJSON<Message>(context, instance, agent, "DELETE", `/agent/messages/${mine.id}`);
    for (const on of [page, watcher]) {
      await expect(rows(on).nth(2)).toContainText("Message deleted");
      await expect(rows(on).nth(2)).not.toContainText("sending the build");
    }
    if (!isMobile(testInfo)) {
      await expect(
        page.getByRole("list", { name: "Conversations" }).getByRole("link", { name: /e2e-edit/ }),
      ).toContainText("Message deleted");
    }

    // DLT-02: the creator of the group removes the AI's other message from the menu.
    await menuOf(rows(page).nth(1)).click();
    await page.getByRole("menuitem", { name: "Delete" }).click();
    await page
      .getByRole("alertdialog", { name: "Delete this message?" })
      .getByRole("button", { name: "Delete" })
      .click();
    for (const on of [page, watcher]) {
      await expect(rows(on).nth(1)).toContainText("Message deleted");
      await expect(rows(on).nth(1)).not.toContainText("noted");
    }
    await expectNoSidewaysScroll(page, "a conversation with deleted messages");
    await shoot(page, "message-deleted", testInfo);

    // Nothing reloaded: the live frames and the answers of the requests did all of it.
    for (const on of [page, watcher]) {
      expect(await on.evaluate(() => "e2eSameDocument" in window)).toBe(true);
    }
  } finally {
    await other.close();
  }
});
