import type { BrowserContext, Page, TestInfo } from "@playwright/test";
import type { CreatedInvite, List, Me, Room, Token } from "../src/lib/api/types.gen";
import { ADMIN, apiCall, expect, MEMBER, test } from "./fixtures";
import type { Instance } from "./server";

// Smoke suite against the built binary (web-app T19, T22: UI-01, UI-02, UI-04, UI-14, UI-24). The
// tests share one fresh instance per project and run in order: the first one creates the admin.
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
    await admin.getByRole("textbox", { name: "Message e2e-wire" }).fill(text);
    await admin.getByRole("button", { name: "Send" }).click();
    await expect(admin.getByText(text)).toBeVisible();

    await expect(viewer.getByText(text)).toBeVisible();
    expect(await viewer.evaluate(() => "e2eSameDocument" in window)).toBe(true);
    expect(timelineFetches).toEqual([]);
  } finally {
    await adminCtx.close();
    await memberCtx.close();
  }
});

test("connect an agent shows the .mcp.json snippet with the instance URL and a new agent token (UI-14)", async ({
  context,
  page,
  instance,
}) => {
  await loginAs(context, instance, ADMIN);
  await page.goto("/agents");
  await page.getByRole("button", { name: "Connect an agent" }).click();
  const dialog = page.getByRole("dialog", { name: "Connect an agent" });
  await dialog.getByLabel("Agent name", { exact: true }).fill("e2e-agent");
  await dialog.getByRole("button", { name: "Create token" }).click();

  const config = dialog.getByRole("figure", { name: ".mcp.json" }).locator("pre");
  await expect(config).toBeVisible();
  const snippet = JSON.parse((await config.textContent()) ?? "");
  const server = snippet.mcpServers.partyline;
  // `partyline channel` with the browser's origin, an agent token and the chosen name.
  expect(server.command).toBe("partyline");
  expect(server.args).toEqual(["channel"]);
  expect(server.env.PARTYLINE_URL).toBe(instance.url);
  expect(server.env.PARTYLINE_TOKEN).toMatch(/^pl_agent_[A-Za-z0-9]+$/);
  expect(server.env.PARTYLINE_AGENT).toBe("e2e-agent");

  const launch = dialog.getByRole("figure", { name: "Launch command" }).locator("pre");
  await expect(launch).toHaveText(
    "claude --dangerously-load-development-channels server:partyline",
  );

  // The token in the snippet is the agent token the dialog created.
  const tokens = await apiCall<List<Token>>(context.request, instance, "GET", "/tokens");
  const created = tokens.items.filter((t) => t.kind === "agent" && t.name === "e2e-agent");
  expect(created).toHaveLength(1);
  expect(server.env.PARTYLINE_TOKEN.startsWith(created[0]?.prefix ?? "-")).toBe(true);
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
  // A long unbroken token is the usual way a timeline row pushes a page sideways.
  await apiCall(context.request, instance, "POST", `/rooms/${room.id}/messages`, {
    text: `See https://example.com/${"a-very-long-path-segment".repeat(8)} for details`,
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
      const line = page.getByText(/a-very-long-path-segment/);
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
