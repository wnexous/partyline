import type { BrowserContext, Page, TestInfo } from "@playwright/test";
import type {
  AgentInfo,
  CreatedInvite,
  CreatedToken,
  List,
  Me,
  Room,
  Token,
} from "../src/lib/api/types.gen";
import { ADMIN, apiCall, expect, MEMBER, test } from "./fixtures";
import type { Instance } from "./server";

// Smoke suite against the built binary (web-app T19, T22: UI-01, UI-02, UI-04, UI-14, UI-24;
// harness-onboarding WIZ-02). The tests share one fresh instance per project and run in order:
// the first one creates the admin.
test.describe.configure({ mode: "serial" });

const MOBILE_MAX_WIDTH = 767;

function isMobile(testInfo: TestInfo) {
  return (testInfo.project.use.viewport?.width ?? 1280) <= MOBILE_MAX_WIDTH;
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

test("first run: setup, log out, log back in to a protected route (UI-01, UI-02)", async ({
  page,
  instance,
}, testInfo) => {
  await page.goto("/rooms");
  await expect(page).toHaveURL(`${instance.url}/setup`);
  await expect(page.getByRole("heading", { name: "Set up partyline" })).toBeVisible();

  await page.getByLabel("Setup token", { exact: true }).fill(instance.setupToken);
  await page.getByLabel("Username", { exact: true }).fill(ADMIN.username);
  await page.getByLabel("Password", { exact: true }).fill(ADMIN.password);
  await page.getByRole("button", { name: "Create admin account" }).click();
  await expect(page).toHaveURL(`${instance.url}/`);
  await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();

  if (isMobile(testInfo)) await page.getByRole("button", { name: "Toggle sidebar" }).click();
  await page.getByRole("button", { name: "Account menu" }).click();
  await page.getByRole("menuitem", { name: "Log out" }).click();
  await expect(page).toHaveURL(`${instance.url}/login`);

  // The session is gone: a protected route goes back to login and remembers where it was.
  await page.goto("/rooms");
  await expect(page).toHaveURL(`${instance.url}/login?redirect=%2Frooms`);
  await page.getByLabel("Username", { exact: true }).fill(ADMIN.username);
  await page.getByLabel("Password", { exact: true }).fill(ADMIN.password);
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page).toHaveURL(`${instance.url}/rooms`);
  await expect(page.getByRole("heading", { name: "Rooms", level: 1 })).toBeVisible();
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
    await admin.goto("/rooms");
    await admin.getByRole("button", { name: "New room" }).click();
    const dialog = admin.getByRole("dialog", { name: "New room" });
    await dialog.getByLabel("Name", { exact: true }).fill("e2e-wire");
    await dialog.getByRole("button", { name: "Create room" }).click();
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
  await page.goto("/agents");
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

/** Registers a temporary agent session of project for the logged-in context's user. */
async function registerSession(
  context: BrowserContext,
  instance: Instance,
  project: string,
  harness: string,
) {
  const token = await apiCall<CreatedToken>(context.request, instance, "POST", "/tokens", {
    kind: "agent",
    name: `e2e-${project}-${harness}`,
  });
  const res = await context.request.post(`${instance.url}/api/v1/agent/register`, {
    headers: {
      Authorization: `Bearer ${token.secret}`,
      "X-Partyline-Project": project,
      "X-Partyline-Harness": harness,
      "X-Partyline-Machine": "e2e-box",
    },
  });
  expect(res.ok(), `register → ${res.status()}`).toBe(true);
  return (await res.json()) as AgentInfo;
}

test("agents are grouped by project and the owner renames the project (PROJ-04, PROJ-05)", async ({
  context,
  page,
  instance,
}) => {
  await loginAs(context, instance, ADMIN);
  const session = await registerSession(context, instance, "ruthe", "claude");
  expect(session.display_name).toBe("Ruthe · Claude Code");

  await page.goto("/agents");
  await expect(page.getByRole("heading", { name: "Ruthe", level: 2 })).toBeVisible();
  const group = page.getByRole("list", { name: "Agents of Ruthe" });
  const row = group.getByRole("listitem", { name: "Ruthe · Claude Code" });
  await expect(row.getByText("Temporary")).toBeVisible();
  await expect(row.getByText("e2e-box")).toBeVisible();

  await page.getByRole("button", { name: "Rename project Ruthe" }).click();
  const field = page.getByRole("textbox", { name: "Rename project Ruthe" });
  await field.fill("Videos");
  await field.press("Enter");
  await expect(page.getByRole("heading", { name: "Videos", level: 2 })).toBeVisible();
  await expect(
    page.getByRole("list", { name: "Agents of Videos" }).getByRole("listitem", {
      name: "Videos · Claude Code",
    }),
  ).toBeVisible();
});

test("main pages fit the viewport with no horizontal page scroll (UI-24)", async ({
  context,
  page,
  instance,
}) => {
  await loginAs(context, instance, ADMIN);
  const room = await apiCall<Room>(context.request, instance, "POST", "/rooms", {
    name: "e2e-narrow",
  });
  // A long token with no break opportunity (no spaces or hyphens) is the usual way a
  // timeline row pushes a page sideways; only `break-words` can wrap it.
  await apiCall(context.request, instance, "POST", `/rooms/${room.id}/messages`, {
    text: `See ${"averylongunbrokentoken".repeat(12)} for details`,
  });

  const pages: [string, string][] = [
    ["/", "Overview"],
    ["/rooms", "Rooms"],
    [`/rooms/${room.id}`, "e2e-narrow"],
    ["/agents", "Agents"],
    ["/connectors", "Connectors"],
    ["/settings", "Settings"],
  ];
  for (const [path, heading] of pages) {
    await page.goto(path);
    await expect(page.getByRole("heading", { name: heading, level: 1 })).toBeVisible();
    if (path === `/rooms/${room.id}`) {
      // The room page scrolls inside its timeline, so also check the long line wraps on screen.
      const line = page.getByText(/averylongunbrokentoken/);
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
