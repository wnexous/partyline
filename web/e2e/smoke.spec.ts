import type { BrowserContext, Page, TestInfo } from "@playwright/test";
import type {
  AgentInfo,
  AgentMessage,
  AgentRoom,
  Page as ApiPage,
  CreatedInvite,
  CreatedToken,
  List,
  Me,
  Room,
  Token,
} from "../src/lib/api/types.gen";
import { ADMIN, apiCall, expect, MEMBER, settled, test } from "./fixtures";
import { postPictures } from "./images";
import type { Instance } from "./server";
import { shoot } from "./shots";

// Smoke suite against the built binary (intelligences T52: LAY-01, LAY-05, DM-01, PROF-03,
// TREE-01; web-app UI-01, UI-02, UI-04, UI-14, UI-24; harness-onboarding WIZ-02; messenger-polish
// IMG-01..IMG-06). Set E2E_SHOTS_DIR to also save screenshots of those flows. The tests share
// one fresh instance per project and run in order: the first one creates the admin.
test.describe.configure({ mode: "serial" });

const MOBILE_MAX_WIDTH = 767;

/** The machine every fake AI session reports; the contacts page groups AIs by it (TREE-01). */
const MACHINE = "e2e-box";

/** A 1x1 PNG: the smallest image the avatar upload accepts (PROF-03). */
const PHOTO = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
  "base64",
);

function isMobile(testInfo: TestInfo) {
  return (testInfo.project.use.viewport?.width ?? 1280) <= MOBILE_MAX_WIDTH;
}

/** The home page's heading: beside the sidebar it asks to pick one; on a phone it is the list. */
function homeHeading(testInfo: TestInfo) {
  return isMobile(testInfo) ? "Conversations" : "Pick a conversation";
}

/** Logs a context in through the API; the session cookie lands in the browser context. */
async function loginAs(context: BrowserContext, instance: Instance, who: typeof ADMIN) {
  await apiCall<Me>(context.request, instance, "POST", "/auth/login", who);
}

/** True once the live socket delivered its `hello` frame (it is then subscribed). */
function trackLive(page: Page) {
  const state = { ready: false };
  page.on("websocket", (ws) => {
    ws.on("framereceived", ({ payload }) => {
      if (typeof payload === "string" && payload.includes('"type":"hello"')) state.ready = true;
    });
  });
  return state;
}

/**
 * A fake AI client (AID-01, AID-02): an agent token plus the machine and folder headers the CLI
 * sends. The AI is the one the folder names, so two connects of one folder are one AI.
 */
interface AgentClient {
  secret: string;
  ai: AgentInfo;
  machine: string;
  /** The folder header as the CLI sends it (`url.PathEscape`d, so `/` is `%2F`). */
  folder: string;
}

async function connectAI(
  context: BrowserContext,
  instance: Instance,
  folder: string,
  machine = MACHINE,
): Promise<AgentClient> {
  const token = await apiCall<CreatedToken>(context.request, instance, "POST", "/tokens", {
    kind: "agent",
    name: `e2e ${folder}`,
  });
  const folderHeader = encodeURIComponent(folder);
  const res = await context.request.post(`${instance.url}/api/v1/agent/register`, {
    headers: {
      Authorization: `Bearer ${token.secret}`,
      "X-Partyline-Machine": machine,
      "X-Partyline-Folder": folderHeader,
    },
  });
  expect(res.ok(), `register ${folder} → ${res.status()}`).toBe(true);
  return {
    secret: token.secret,
    ai: (await res.json()) as AgentInfo,
    machine,
    folder: folderHeader,
  };
}

/** One call of the fake AI client, as its folder and machine (never as a client-supplied id). */
function agentCall(
  context: BrowserContext,
  instance: Instance,
  agent: AgentClient,
  method: "GET" | "POST",
  path: string,
  data?: unknown,
) {
  return context.request.fetch(`${instance.url}/api/v1${path}`, {
    method,
    headers: {
      Authorization: `Bearer ${agent.secret}`,
      "X-Partyline-Machine": agent.machine,
      "X-Partyline-Folder": agent.folder,
    },
    data,
  });
}

async function agentJSON<T>(
  context: BrowserContext,
  instance: Instance,
  agent: AgentClient,
  method: "GET" | "POST",
  path: string,
  data?: unknown,
): Promise<T> {
  const res = await agentCall(context, instance, agent, method, path, data);
  expect(res.ok(), `${method} ${path} → ${res.status()}`).toBe(true);
  return (await res.json()) as T;
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
    await apiCall(adminCtx.request, instance, "PUT", `/rooms/${roomId}/sharing`, {
      mode: "shared",
      users: [{ user_id: member.id, level: "view" }],
    });

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
      panel.getByRole("region", { name: "Agents" }).getByText(name, { exact: true }),
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

test("the contacts page groups AIs by machine and nests folders (TREE-01)", async ({
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
  const box = page.getByRole("region", { name: MACHINE });
  const laptop = page.getByRole("region", { name: "e2e-laptop" });
  await expect(box.getByRole("link", { name: atlas.ai.display_name })).toBeVisible();
  await expect(laptop.getByRole("link", { name: scribe.ai.display_name })).toBeVisible();

  // An AI whose folder is inside another AI's folder hangs from its row...
  const atlasItem = box
    .getByRole("listitem")
    .filter({ has: page.getByTestId(`contact-${atlas.ai.id}`) });
  await expect(atlasItem.getByTestId(`contact-${notes.ai.id}`)).toBeVisible();
  await expect(atlasItem.getByTestId(`contact-${atlas.ai.id}`)).toBeVisible();
  // ...and an AI of another machine is not part of that tree.
  await expect(box.getByTestId(`contact-${scribe.ai.id}`)).toHaveCount(0);
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
