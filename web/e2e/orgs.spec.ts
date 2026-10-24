import type { Locator } from "@playwright/test";
import type {
  CreatedInvite,
  CreatePositionRequest,
  Me,
  OrgChart,
  Position,
  SetupRequest,
} from "../src/lib/api/types.gen";
import { apiCall, test as base, type Credentials, expect } from "./fixtures";
import {
  connectAI,
  expectNoSidewaysScroll,
  loginAs,
  MACHINE,
  openSession,
  trackLive,
} from "./helpers";
import { type Instance, startInstance } from "./server";

// The organization flow against the built binary (organizations T54: ORG-01, POS-01, POS-07, OMEM-01,
// OMEM-02, CHART-01 to CHART-04). Alice creates acme, builds Director › Lead developer › Worktrees under her
// root position, invites Bob (who accepts in a browser of his own) and reads the chart, which is a tree:
// roles, levels, one tab stop with arrow keys, and no page that scrolls sideways at 1280 or 360 px.
//
// The file has a server of its own: the first-run test of smoke.spec.ts needs an instance nobody has set up,
// and two files of one project may share a worker, so they would share the worker's `instance` too.
const test = base.extend<object, { orgs: Instance }>({
  orgs: [
    // biome-ignore lint/correctness/noEmptyPattern: Playwright fixtures must destructure their first argument.
    async ({}, use) => {
      const instance = await startInstance();
      await use(instance);
      await instance.stop();
    },
    { scope: "worker" },
  ],
  baseURL: async ({ orgs }, use) => {
    await use(orgs.url);
  },
});
test.describe.configure({ mode: "serial" });

const ALICE: Credentials & { display_name: string } = {
  username: "alice",
  password: "e2e-alice-password",
  display_name: "Alice",
};
const BOB: Credentials & { display_name: string } = {
  username: "bob",
  password: "e2e-bob-password",
  display_name: "Bob",
};

/** The positions Alice adds, each below the one before it, the first below her root "Admin". */
const POSITIONS = ["Director", "Lead developer", "Worktrees"];

/** The accounts as the server made them (their handles), set up before the first test. */
let alice: Me;
let bob: Me;
/** The display name of the AI the chart test starts in Worktrees. */
let atlas = "";

test.beforeAll(async ({ browser, orgs }) => {
  // Alice is the first account (the instance admin); Bob joins by an invite of hers, as a plain user.
  const aliceContext = await browser.newContext();
  const bobContext = await browser.newContext();
  try {
    alice = await apiCall<Me>(aliceContext.request, orgs, "POST", "/setup", {
      token: orgs.setupToken,
      ...ALICE,
    } satisfies SetupRequest);
    const invite = await apiCall<CreatedInvite>(
      aliceContext.request,
      orgs,
      "POST",
      "/admin/invites",
      { role: "member" },
    );
    const token = new URL(invite.url).pathname.split("/").pop();
    bob = await apiCall<Me>(bobContext.request, orgs, "POST", `/invites/${token}/accept`, BOB);
  } finally {
    await aliceContext.close();
    await bobContext.close();
  }
});

/** What a tree reads like: the level and the name of each item, in the order it is drawn. */
function outline(tree: Locator) {
  return tree.getByRole("treeitem").evaluateAll((items) =>
    items.map((item) => {
      const label = document.getElementById(item.getAttribute("aria-labelledby") ?? "");
      return `${item.getAttribute("aria-level")} ${label?.textContent ?? ""}`;
    }),
  );
}

test("Alice creates acme and becomes its admin, holding the root position (ORG-01)", async ({
  context,
  page,
  orgs,
}) => {
  await loginAs(context, orgs, ALICE);

  await page.goto("/orgs");
  await expect(page.getByRole("heading", { name: "Organizations", level: 1 })).toBeVisible();
  // Her personal organization (AD-021) is all she belongs to so far.
  const yours = page.getByRole("list", { name: "Your organizations" });
  await expect(yours.getByRole("listitem")).toHaveCount(1);

  await page.getByRole("button", { name: "New organization" }).click();
  const dialog = page.getByRole("dialog", { name: "New organization" });
  await dialog.getByLabel("Name", { exact: true }).fill("Acme");
  await dialog.getByRole("button", { name: "Create organization" }).click();

  const acme = yours.getByRole("listitem", { name: "Acme", exact: true });
  await expect(acme).toBeVisible();
  await expect(acme.getByText("acme", { exact: true })).toBeVisible();
  await expect(acme.getByText("Admin", { exact: true })).toBeVisible();
  // It is the organization the app follows from now on: the switcher names it.
  await expect(acme.getByText("Current", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Organization: Acme" })).toBeVisible();

  // She holds a root position named "Admin" (ORG-01): the chart has that one position, and her in it.
  const chart = await apiCall<OrgChart>(context.request, orgs, "GET", "/orgs/acme/chart");
  expect(
    chart.positions.map((p) => ({
      name: p.name,
      parent: p.parent_id ?? null,
      holders: p.holders.map((h) => h.intelligence.handle),
    })),
  ).toEqual([{ name: "Admin", parent: null, holders: [alice.intelligence.handle] }]);
});

test("Alice adds the positions Director, Lead developer and Worktrees, each below the one before (POS-01)", async ({
  context,
  page,
  orgs,
}) => {
  await loginAs(context, orgs, ALICE);

  await page.goto("/orgs/acme/chart");
  await page.getByRole("link", { name: "Edit positions" }).click();
  await expect(page.getByRole("heading", { name: "Positions", level: 1 })).toBeVisible();

  const positions = page.getByRole("list", { name: "Positions of Acme" });
  let parent = "Admin";
  for (const name of POSITIONS) {
    await positions.getByRole("button", { name: `Add a position under ${parent}` }).click();
    const field = page.getByRole("textbox", { name: "Name of the new position" });
    await field.fill(name);
    await field.press("Enter");
    await expect(positions.getByRole("listitem", { name, exact: true })).toBeVisible();
    parent = name;
  }

  // Admin › Director › Lead developer › Worktrees: each has the one before it as its parent.
  const chart = await apiCall<OrgChart>(context.request, orgs, "GET", "/orgs/acme/chart");
  expect(chart.positions.map((p) => p.name)).toEqual(["Admin", ...POSITIONS]);
  let above = chart.positions[0];
  expect(above?.parent_id).toBeUndefined();
  for (const position of chart.positions.slice(1)) {
    expect(position.parent_id, `${position.name} sits below ${above?.name}`).toBe(above?.id);
    above = position;
  }
});

test("Bob accepts Alice's invitation in a browser of his own and her open chart shows him at once (OMEM-01, OMEM-02)", async ({
  browser,
  context,
  page,
  orgs,
}) => {
  await loginAs(context, orgs, ALICE);

  // Alice keeps the chart open, its live socket subscribed, and invites from a second tab.
  const live = trackLive(page);
  await page.goto("/orgs/acme/chart");
  const tree = page.getByRole("tree", { name: "Org chart of Acme" });
  await expect(tree).toBeVisible();
  await expect.poll(() => live.ready).toBe(true);
  await expect(tree.getByRole("treeitem", { name: "Bob", exact: true })).toHaveCount(0);
  // Marks this document: a reload would drop it.
  await page.evaluate(() => Object.assign(window, { e2eSameDocument: true }));

  const members = await context.newPage();
  const membersLive = trackLive(members);
  await members.goto("/orgs/acme/members");
  await expect.poll(() => membersLive.ready).toBe(true);
  await members.getByRole("button", { name: "Invite", exact: true }).click();
  const dialog = members.getByRole("dialog", { name: "Invite to Acme" });
  await dialog.getByLabel("Handle", { exact: true }).fill(`@${bob.intelligence.handle}`);
  await dialog.getByLabel("Permission", { exact: true }).selectOption("member");
  await dialog.getByLabel("Position", { exact: true }).selectOption({ label: "Lead developer" });
  await dialog.getByRole("button", { name: "Send invitation" }).click();
  // Until Bob answers, the invitation is pending, and the chart has not changed.
  const pending = members.getByRole("list", { name: "Pending invitations of Acme" });
  await expect(pending.getByRole("listitem", { name: "Bob" })).toBeVisible();
  await expect(tree.getByRole("treeitem", { name: "Bob", exact: true })).toHaveCount(0);

  // Bob answers in his own browser: the invitation says what it grants, and accepting joins him.
  const bobContext = await browser.newContext();
  try {
    await loginAs(bobContext, orgs, BOB);
    const bobPage = await bobContext.newPage();
    await bobPage.goto("/orgs");
    const invitation = bobPage.getByRole("listitem", { name: "Invitation to Acme" });
    await expect(invitation).toBeVisible();
    await expect(invitation.getByText("Alice invited you to Acme as a member.")).toBeVisible();
    await expect(invitation.getByText("Position: Lead developer")).toBeVisible();
    await expect(
      invitation.getByText("Identities above Lead developer will manage you."),
    ).toBeVisible();
    await invitation.getByRole("button", { name: "Accept" }).click();
    await expect(bobPage.getByRole("listitem", { name: "Invitation to Acme" })).toHaveCount(0);
    const yours = bobPage.getByRole("list", { name: "Your organizations" });
    await expect(yours.getByRole("listitem", { name: "Acme", exact: true })).toBeVisible();
  } finally {
    await bobContext.close();
  }

  // The chart Alice had open follows without a reload: Bob holds Lead developer, level 4 of the tree.
  const item = tree.getByRole("treeitem", { name: "Bob", exact: true });
  await expect(item).toBeVisible();
  await expect(item).toHaveAttribute("aria-level", "4");
  expect(await page.evaluate(() => "e2eSameDocument" in window)).toBe(true);
  // The pending list lost his invitation, and he is a member with that permission and position.
  await expect(pending.getByRole("listitem", { name: "Bob" })).toHaveCount(0);
  const chart = await apiCall<OrgChart>(context.request, orgs, "GET", "/orgs/acme/chart");
  const lead = chart.positions.find((p) => p.name === "Lead developer");
  expect(lead?.holders.map((h) => [h.intelligence.handle, h.permission])).toEqual([
    [bob.intelligence.handle, "member"],
  ]);
});

test("an AI started with --position Worktrees holds it, and the chart draws who it is and whether it is online (POS-07, CHART-01)", async ({
  context,
  page,
  orgs,
}) => {
  await loginAs(context, orgs, ALICE);
  const live = trackLive(page);
  // What `partyline claude --org acme --position Worktrees` sends when the session starts.
  const worker = await connectAI(context, orgs, "/work/atlas", MACHINE, {
    org: "acme",
    position: "Worktrees",
  });
  atlas = worker.ai.display_name;

  await page.goto("/orgs");
  await page.getByRole("link", { name: "Org chart of Acme" }).click();
  await expect(page.getByRole("heading", { name: "Org chart", level: 1 })).toBeVisible();

  // The structure: a tree of items with their levels. Each position lists its holders first, then the
  // positions below it, and every identity shows once.
  const tree = page.getByRole("tree", { name: "Org chart of Acme" });
  const items = tree.getByRole("treeitem");
  await expect(items).toHaveCount(7);
  expect(await outline(tree)).toEqual([
    "1 Admin",
    "2 Alice",
    "2 Director",
    "3 Lead developer",
    "4 Bob",
    "4 Worktrees",
    `5 ${atlas}`,
  ]);
  // Positions and members that hold others are open and say so; the ones that hold none do not.
  const openness = await items.evaluateAll((all) =>
    all.map((i) => i.getAttribute("aria-expanded")),
  );
  expect(openness).toEqual(["true", null, "true", "true", null, "true", null]);
  // Position in the set: Alice is the first of the two things the Admin position holds, Director the second.
  await expect(items.nth(1)).toHaveAttribute("aria-posinset", "1");
  await expect(items.nth(1)).toHaveAttribute("aria-setsize", "2");
  await expect(items.nth(2)).toHaveAttribute("aria-posinset", "2");

  // What each holder is: name, @handle, a person or an AI, the admin badge, and "You" on the viewer's own row.
  const alicesRow = items.nth(1).locator('[data-row=""]').first();
  await expect(alicesRow.getByText("Alice", { exact: true })).toBeVisible();
  await expect(alicesRow.getByText(`@${alice.intelligence.handle}`, { exact: true })).toBeVisible();
  await expect(alicesRow.getByText("Person", { exact: true })).toBeVisible();
  await expect(alicesRow.getByText("Admin", { exact: true })).toBeVisible();
  await expect(alicesRow.getByText("You", { exact: true })).toBeVisible();
  const bobsRow = items.nth(4).locator('[data-row=""]').first();
  await expect(bobsRow.getByText(`@${bob.intelligence.handle}`, { exact: true })).toBeVisible();
  await expect(bobsRow.getByText("Person", { exact: true })).toBeVisible();
  // A member has no badge of its own.
  await expect(bobsRow.getByText("Admin", { exact: true })).toHaveCount(0);
  await expect(bobsRow.getByText("Guest", { exact: true })).toHaveCount(0);
  const aiRow = items.nth(6).locator('[data-row=""]').first();
  await expect(aiRow.getByText(atlas, { exact: true })).toBeVisible();
  await expect(aiRow.getByText(`@${worker.ai.name}`, { exact: true })).toBeVisible();
  await expect(aiRow.getByText("AI", { exact: true })).toBeVisible();

  // Presence follows the AI's session as it opens and closes, without a reload. People show none.
  await expect(aiRow).toContainText("Offline");
  await expect(alicesRow).not.toContainText("Online");
  await expect(alicesRow).not.toContainText("Offline");
  await expect.poll(() => live.ready).toBe(true);
  const session = await openSession(orgs, worker);
  try {
    await expect(aiRow).toContainText("Online");
    await expect(aiRow).not.toContainText("Offline");
  } finally {
    session.close();
  }
  await expect(aiRow).toContainText("Offline");
});

test("Alice walks the chart with the keyboard and the page never scrolls sideways (CHART-04)", async ({
  context,
  page,
  orgs,
}) => {
  await loginAs(context, orgs, ALICE);
  await page.goto("/orgs/acme/chart");
  const tree = page.getByRole("tree", { name: "Org chart of Acme" });
  const items = tree.getByRole("treeitem");
  await expect(items).toHaveCount(7);

  // One item is the tab stop, and Tab from the page's own controls reaches it.
  await expect(tree.locator('[role="treeitem"][tabindex="0"]')).toHaveCount(1);
  await page.getByRole("link", { name: "Edit positions" }).focus();
  await page.keyboard.press("Tab");
  await expect(items.nth(0)).toBeFocused();
  // Down and Up walk the items that show; End and Home jump to the ends.
  await page.keyboard.press("ArrowDown");
  await expect(items.nth(1)).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(items.nth(2)).toBeFocused();
  await page.keyboard.press("ArrowUp");
  await expect(items.nth(1)).toBeFocused();
  await page.keyboard.press("End");
  await expect(items.nth(6)).toBeFocused();
  await page.keyboard.press("Home");
  await expect(items.nth(0)).toBeFocused();
  // Left closes an open item and keeps the focus on it; Right opens it again, and on an open one goes inside.
  await page.keyboard.press("ArrowLeft");
  await expect(items).toHaveCount(1);
  await expect(items.nth(0)).toHaveAttribute("aria-expanded", "false");
  await expect(items.nth(0)).toBeFocused();
  await page.keyboard.press("ArrowRight");
  await expect(items).toHaveCount(7);
  await expect(items.nth(0)).toHaveAttribute("aria-expanded", "true");
  await page.keyboard.press("ArrowRight");
  await expect(items.nth(1)).toBeFocused();
  // Left on an item that holds nothing goes to the one that holds it.
  await page.keyboard.press("ArrowLeft");
  await expect(items.nth(0)).toBeFocused();
  // Enter closes and opens an item that holds others (Director holds Lead developer).
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("ArrowDown");
  await expect(items.nth(2)).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(items).toHaveCount(3);
  await expect(items.nth(2)).toHaveAttribute("aria-expanded", "false");
  await page.keyboard.press("Enter");
  await expect(items).toHaveCount(7);

  await expectNoSidewaysScroll(page, "the org chart");
});

test("Bob reads the same chart, and is offered only what his place in it allows (CHART-02, CHART-03)", async ({
  browser,
  orgs,
}) => {
  const bobContext = await browser.newContext();
  try {
    await loginAs(bobContext, orgs, BOB);
    const page = await bobContext.newPage();
    await page.goto("/orgs");
    await page.getByRole("link", { name: "Org chart of Acme" }).click();

    // A member sees the whole chart, not only his branch.
    const tree = page.getByRole("tree", { name: "Org chart of Acme" });
    await expect(tree.getByRole("treeitem")).toHaveCount(7);
    await expect(tree.getByRole("treeitem", { name: "Alice", exact: true })).toBeVisible();
    const bobsRow = tree.getByRole("treeitem", { name: "Bob", exact: true });
    await expect(bobsRow.getByText("You", { exact: true })).toBeVisible();

    // Only admins edit positions: no link to the editor, no such section in the organization's bar.
    await expect(page.getByRole("link", { name: "Edit positions" })).toHaveCount(0);
    const sections = page.getByRole("navigation", { name: "Sections of Acme" });
    await expect(sections.getByRole("link", { name: "Org chart" })).toBeVisible();
    await expect(sections.getByRole("link", { name: "Members" })).toBeVisible();
    await expect(sections.getByRole("link", { name: "Positions" })).toHaveCount(0);

    // Bob holds Lead developer, above Worktrees: he manages the AI there and may move it, but a member
    // cannot delete anyone, and he manages neither Alice nor himself.
    const moves = page.getByRole("button", { name: /^Move / });
    await expect(moves).toHaveCount(1);
    await expect(moves).toHaveAccessibleName(`Move ${atlas}`);
    await expect(page.getByRole("button", { name: /^Delete / })).toHaveCount(0);
    await expect(page.getByRole("button", { name: /^Remove .* from Acme$/ })).toHaveCount(0);

    await expectNoSidewaysScroll(page, "the org chart of a member");
  } finally {
    await bobContext.close();
  }
});

test("no page of an organization scrolls sideways, even with names that cannot break (CHART-04)", async ({
  context,
  page,
  orgs,
}) => {
  await loginAs(context, orgs, ALICE);
  // What widens a page is a name with no place to break: a position of 60 characters, one level deeper than
  // the chart indents, and an AI whose folder is as long.
  const chart = await apiCall<OrgChart>(context.request, orgs, "GET", "/orgs/acme/chart");
  const worktrees = chart.positions.find((p) => p.name === "Worktrees");
  const unbroken = "Principalstaffengineerofworktreeautomationandreleasetrains";
  const created = await apiCall<Position>(context.request, orgs, "POST", "/orgs/acme/positions", {
    name: unbroken,
    parent_id: worktrees?.id,
  } satisfies CreatePositionRequest);
  await connectAI(context, orgs, `/work/${"averylongunbrokenfoldername".repeat(3)}`, MACHINE, {
    org: "acme",
    position: created.name,
  });

  // Each page with the heading it opens on and a thing that only shows once its data has loaded.
  const pages: [path: string, heading: string, loaded: Locator][] = [
    ["/orgs", "Organizations", page.getByRole("listitem", { name: "Acme", exact: true })],
    ["/orgs/acme/chart", "Org chart", page.getByRole("treeitem", { name: unbroken, exact: true })],
    ["/orgs/acme/members", "Members", page.getByRole("link", { name: "Bob" })],
    [
      "/orgs/acme/positions",
      "Positions",
      page.getByRole("listitem", { name: unbroken, exact: true }),
    ],
    [
      "/orgs/acme/settings",
      "Organization settings",
      page.getByRole("heading", { name: "Details" }),
    ],
  ];
  for (const [path, heading, loaded] of pages) {
    await page.goto(path);
    await expect(page.getByRole("heading", { name: heading, level: 1 })).toBeVisible();
    await expect(loaded).toBeVisible();
    await expectNoSidewaysScroll(page, path);
  }
});
