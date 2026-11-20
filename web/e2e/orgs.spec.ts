import type {
  AgentInstructors,
  CreatedInvite,
  List,
  Me,
  OrgMember,
  SetupRequest,
} from "../src/shared/api/types.gen";
import { apiCall, test as base, type Credentials, expect } from "./fixtures";
import { connectAI, expectNoSidewaysScroll, loginAs, MACHINE, trackLive } from "./helpers";
import { type Instance, startInstance } from "./server";
import { shoot } from "./shots";

// The organization flow against the built binary (ORG-01, OMEM-01, OMEM-02, SR2-01, SR2-05, SR2-10,
// SR2-12). Alice creates acme, invites Bob as a member (who accepts in a browser of his own), starts an AI in
// acme and, as its host, lets Bob instruct it from the AI's profile. There is no org chart and no position:
// an organization is its members, admins or members. No page scrolls sideways at 1280 or 360 px.
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

/** The accounts as the server made them (their handles), set up before the first test. */
let alice: Me;
let bob: Me;
/** The id of the AI Alice starts in acme. */
let atlasId = "";

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

test("Alice creates acme and becomes its admin (ORG-01)", async ({ context, page, orgs }) => {
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

  const members = await apiCall<List<OrgMember>>(
    context.request,
    orgs,
    "GET",
    "/orgs/acme/members",
  );
  expect(members.items.map((m) => [m.intelligence.handle, m.permission])).toEqual([
    [alice.intelligence.handle, "admin"],
  ]);
});

test("SR2-05: an organization opens on its members, with no org chart and no positions", async ({
  context,
  page,
  orgs,
}) => {
  await loginAs(context, orgs, ALICE);
  await page.goto("/orgs/acme");
  await expect(page.getByRole("heading", { name: "Members", level: 1 })).toBeVisible();
  await expect(page).toHaveURL(/\/orgs\/[^/]+\/members$/);
  const sections = page.getByRole("navigation", { name: "Sections of Acme" });
  await expect(sections.getByRole("link", { name: "Members" })).toBeVisible();
  await expect(sections.getByRole("link", { name: "Org chart" })).toHaveCount(0);
  await expect(sections.getByRole("link", { name: "Positions" })).toHaveCount(0);
});

test("SR2-01: Bob accepts Alice's invitation as a member, and her open members page shows him at once (OMEM-01, OMEM-02)", async ({
  browser,
  context,
  page,
  orgs,
}) => {
  await loginAs(context, orgs, ALICE);

  const live = trackLive(page);
  await page.goto("/orgs/acme/members");
  await expect.poll(() => live.ready).toBe(true);
  const table = page.getByRole("table", { name: "Members of Acme" });
  await expect(table.getByRole("link", { name: "Bob" })).toHaveCount(0);
  // Marks this document: a reload would drop it.
  await page.evaluate(() => Object.assign(window, { e2eSameDocument: true }));

  await page.getByRole("button", { name: "Invite", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Invite to Acme" });
  // An invitation is to be an admin or a member: no guest, no position.
  const permission = dialog.getByLabel("Permission", { exact: true });
  await expect(permission.locator("option")).toHaveText(["Admin", "Member"]);
  await expect(dialog.getByLabel("Position", { exact: true })).toHaveCount(0);
  await dialog.getByLabel("Handle", { exact: true }).fill(`@${bob.intelligence.handle}`);
  await permission.selectOption("member");
  await dialog.getByRole("button", { name: "Send invitation" }).click();
  const pending = page.getByRole("list", { name: "Pending invitations of Acme" });
  await expect(pending.getByRole("listitem", { name: "Bob" })).toBeVisible();

  // Bob answers in his own browser: the invitation says who invited him and as what, and accepting joins him.
  const bobContext = await browser.newContext();
  try {
    await loginAs(bobContext, orgs, BOB);
    const bobPage = await bobContext.newPage();
    await bobPage.goto("/orgs");
    const invitation = bobPage.getByRole("listitem", { name: "Invitation to Acme" });
    await expect(invitation).toBeVisible();
    await expect(invitation.getByText("Alice invited you to Acme as a member.")).toBeVisible();
    await expect(invitation.getByText(/will manage/)).toHaveCount(0);
    await invitation.getByRole("button", { name: "Accept" }).click();
    await expect(bobPage.getByRole("listitem", { name: "Invitation to Acme" })).toHaveCount(0);
    const yours = bobPage.getByRole("list", { name: "Your organizations" });
    await expect(yours.getByRole("listitem", { name: "Acme", exact: true })).toBeVisible();
  } finally {
    await bobContext.close();
  }

  // The members page Alice had open follows without a reload.
  await expect(table.getByRole("link", { name: "Bob" })).toBeVisible();
  expect(await page.evaluate(() => "e2eSameDocument" in window)).toBe(true);
  await expect(pending.getByRole("listitem", { name: "Bob" })).toHaveCount(0);
  const members = await apiCall<List<OrgMember>>(
    context.request,
    orgs,
    "GET",
    "/orgs/acme/members",
  );
  expect(
    members.items.find((m) => m.intelligence.handle === bob.intelligence.handle)?.permission,
  ).toBe("member");
});

test("SR2-10: Alice, the host of an AI, lets Bob instruct it from its profile", async ({
  context,
  page,
  orgs,
}, testInfo) => {
  await loginAs(context, orgs, ALICE);
  // What `partyline claude --org acme` sends when the session starts: Alice hosts the new AI.
  const atlas = await connectAI(context, orgs, "/work/atlas", MACHINE, { org: "acme" });
  atlasId = atlas.ai.id;

  await page.goto(`/contacts/${atlasId}`);
  const section = page.getByRole("region", { name: "Can instruct this AI" });
  await expect(section).toBeVisible();
  const who = section.getByRole("list", { name: "Can instruct this AI" });
  // Before: only its host.
  await expect(who.getByRole("listitem")).toHaveCount(1);
  await expect(who.getByRole("listitem").first()).toContainText("Alice");
  await expect(who.getByRole("listitem").first()).toContainText("Host");

  await section.getByRole("button", { name: "Add instructor" }).click();
  const dialog = page.getByRole("dialog", {
    name: `Who else can instruct ${atlas.ai.display_name}?`,
  });
  await dialog.getByLabel("Handle", { exact: true }).fill(`@${bob.intelligence.handle}`);
  await dialog.getByRole("button", { name: "Add", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(who.getByRole("listitem")).toHaveCount(2);
  await expect(who.getByText("Bob", { exact: true })).toBeVisible();
  await expectNoSidewaysScroll(page, "the full profile with who can instruct the AI");
  await shoot(page, "instructors-profile", testInfo);

  const instructors = await apiCall<AgentInstructors>(
    context.request,
    orgs,
    "GET",
    `/agents/${atlasId}/instructors`,
  );
  expect(instructors.host.handle).toBe(alice.intelligence.handle);
  expect(instructors.instructors.map((i) => i.intelligence.handle)).toEqual([
    bob.intelligence.handle,
  ]);
});

test("SR2-12, SR2-10: Bob, a member, invites and deletes nobody, and reads who instructs the AI without changing it", async ({
  browser,
  orgs,
}) => {
  const bobContext = await browser.newContext();
  try {
    await loginAs(bobContext, orgs, BOB);
    const page = await bobContext.newPage();
    await page.goto("/orgs/acme/members");
    const table = page.getByRole("table", { name: "Members of Acme" });
    await expect(table.getByRole("link", { name: "Alice" })).toBeVisible();
    // Only an admin invites, and a member neither removes a person nor deletes an AI it does not host.
    await expect(page.getByRole("button", { name: "Invite", exact: true })).toHaveCount(0);
    await expect(table.getByRole("button", { name: /^(Delete|Remove from Acme)$/ })).toHaveCount(0);
    await expectNoSidewaysScroll(page, "the members of a member");

    await page.goto(`/contacts/${atlasId}`);
    const section = page.getByRole("region", { name: "Can instruct this AI" });
    await expect(section.getByRole("listitem")).toHaveCount(2);
    await expect(section.getByRole("button", { name: "Add instructor" })).toHaveCount(0);
    await expect(section.getByRole("button", { name: /as an instructor$/ })).toHaveCount(0);
  } finally {
    await bobContext.close();
  }
});

test("no page of an organization scrolls sideways, even with names that cannot break (UI-24)", async ({
  context,
  page,
  orgs,
}) => {
  await loginAs(context, orgs, ALICE);
  // What widens a page is a name with no place to break: an AI whose folder is very long.
  await connectAI(context, orgs, `/work/${"averylongunbrokenfoldername".repeat(3)}`, MACHINE, {
    org: "acme",
  });

  // Each page with the heading it opens on and a thing that only shows once its data has loaded.
  const pages: [path: string, heading: string, loaded: ReturnType<typeof page.getByRole>][] = [
    ["/orgs", "Organizations", page.getByRole("listitem", { name: "Acme", exact: true })],
    ["/orgs/acme/members", "Members", page.getByRole("link", { name: "Bob" })],
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
