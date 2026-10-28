import type { Browser, BrowserContextOptions, Locator, Page } from "@playwright/test";
import type { Connector, Me, Organization, SetupRequest } from "../src/shared/api/types.gen";
import { apiCall, test as base, type Credentials, expect, settled } from "./fixtures";
import { loginAs } from "./helpers";
import { type Instance, startInstance } from "./server";

// The split between the account and the organization, against the built binary (settings-split
// SET-01..SET-11): the account has one address and one entry, the organization's pages have one
// entry, and the three addresses that moved still work. The desktop half drives the two menus at a
// desktop width; the phone half opens a touch context of its own, as mobile.spec.ts does, and
// measures the rows a finger hits.
//
// The file has a server of its own: the first-run test of smoke.spec.ts needs an instance nobody
// has set up, and two files of one project may share a worker, so they would share the worker's
// `instance` too.
const test = base.extend<object, { nav: Instance }>({
  nav: [
    // biome-ignore lint/correctness/noEmptyPattern: Playwright fixtures must destructure their first argument.
    async ({}, use) => {
      const instance = await startInstance();
      await use(instance);
      await instance.stop();
    },
    { scope: "worker" },
  ],
  baseURL: async ({ nav }, use) => {
    await use(nav.url);
  },
});
test.describe.configure({ mode: "serial" });

const OWNER: Credentials & { display_name: string } = {
  username: "navigator",
  password: "e2e-navigator-password",
  display_name: "Navigator",
};
const CONNECTOR_NAME = "Navigation webhook";
const CONNECTOR_SECRET = "synthetic-e2e-navigation-secret-not-real";

/** A desktop window: the sidebar, its two menus and every page. */
const DESKTOP: BrowserContextOptions = { viewport: { width: 1280, height: 800 } };
/** A phone: the home screen's menus and the top bar of a full-screen page, driven by a finger. */
const PHONE: BrowserContextOptions = {
  viewport: { width: 360, height: 780 },
  hasTouch: true,
  isMobile: true,
};

/** The organization the account is in and the connection it owns, made before the first test. */
let acme: Organization;
let connector: Connector;

test.beforeAll(async ({ browser, nav }) => {
  const context = await browser.newContext();
  try {
    await apiCall<Me>(context.request, nav, "POST", "/setup", {
      token: nav.setupToken,
      ...OWNER,
    } satisfies SetupRequest);
    acme = await apiCall<Organization>(context.request, nav, "POST", "/orgs", { name: "Acme" });
    // The switcher follows the account's default when this browser has no pick of its own, so the
    // pages are Acme's from the first load (SET-06).
    await apiCall<Me>(context.request, nav, "PUT", "/me/default-org", { org_id: acme.id });
    connector = await apiCall<Connector>(context.request, nav, "POST", "/connectors", {
      org_id: acme.id,
      kind: "webhook",
      name: CONNECTOR_NAME,
      config: { secret: CONNECTOR_SECRET },
    });
  } finally {
    await context.close();
  }
});

/** A context logged in as the owner: the desktop window unless the caller brings a phone's options. */
async function openPage(browser: Browser, nav: Instance, options: BrowserContextOptions = DESKTOP) {
  const context = await browser.newContext(options);
  await loginAs(context, nav, OWNER);
  return { context, page: await context.newPage() };
}

/** Every row of a menu a finger must hit (SET-04): 44 px tall and inside the window's width. */
async function expectFingerRows(page: Page, rows: Locator, where: string) {
  const found = await rows.count();
  expect(found, `${where} has no rows`).toBeGreaterThan(0);
  const width = await page.evaluate(() => window.innerWidth);
  for (const row of await rows.all()) {
    const label = ((await row.textContent()) ?? "").replace(/\s+/g, " ").trim();
    const box = await row.boundingBox();
    expect(box, `${where}: "${label}" has no box, so it is not on screen`).not.toBeNull();
    if (!box) continue;
    expect(box.height, `${where}: "${label}" is shorter than 44 px`).toBeGreaterThanOrEqual(44);
    expect(box.x, `${where}: "${label}" starts outside the window`).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width, `${where}: "${label}" ends outside the window`).toBeLessThanOrEqual(
      width,
    );
  }
}

/**
 * A moved address lands on the new one and the redirect replaced the entry it arrived on, so Back
 * leaves the page instead of replaying the redirect (SET-02, SET-06). It takes a page with no
 * history of its own, so the entry before the redirect is the last one.
 */
async function expectReplacesEntry(
  page: Page,
  root: string,
  from: string,
  to: string,
  landed?: () => Promise<void>,
) {
  // Start from the conversation list, so Back has somewhere to go.
  await page.goto(root);
  const before = await page.evaluate(() => history.length);
  await page.goto(from);
  await expect(page).toHaveURL(to);
  if (landed) await landed();
  expect(await page.evaluate(() => history.length), `${from} grew the history`).toBe(before + 1);
  await page.goBack();
  await expect(page).toHaveURL(root);
}

test("the account keeps one address and the avatar leads to it (SET-01, SET-02, SET-03)", async ({
  browser,
  nav,
}) => {
  const { context, page } = await openPage(browser, nav);
  try {
    // A bookmark of the old address lands on the account.
    await page.goto("/settings");
    await expect(page).toHaveURL(`${nav.url}/account`);
    await expect(page.getByRole("heading", { name: "My account", level: 1 })).toBeVisible();

    // The one entry to the account, and where it points.
    await page.goto("/");
    await page.getByRole("button", { name: "Account menu" }).click();
    const menu = page.getByRole("menu");
    await expect(menu.getByRole("menuitem", { name: "My account" })).toHaveAttribute(
      "href",
      "/account",
    );
    await menu.getByRole("menuitem", { name: "My account" }).click();
    await expect(page).toHaveURL(`${nav.url}/account`);
    await expect(page.getByRole("heading", { name: "My account", level: 1 })).toBeVisible();

    // A page of the account, from the same menu.
    await page.getByRole("button", { name: "Account menu" }).click();
    await page.getByRole("menuitem", { name: "Devices" }).click();
    await expect(page).toHaveURL(`${nav.url}/devices`);
    await expect(page.getByRole("heading", { name: "Approve a machine", level: 1 })).toBeVisible();
  } finally {
    await context.close();
  }
});

test("the gear opens the organization's settings and connections (SET-05, SET-09, SET-11)", async ({
  browser,
  nav,
}) => {
  const { context, page } = await openPage(browser, nav);
  try {
    await page.goto("/");
    const gear = page.getByRole("button", { name: "Pages of Acme" });
    await expect(gear).toBeVisible();
    await gear.click();
    await page.getByRole("menuitem", { name: "Settings", exact: true }).click();
    await expect(page).toHaveURL(`${nav.url}/orgs/${acme.id}/settings`);
    await expect(
      page.getByRole("heading", { name: "Organization settings", level: 1 }),
    ).toBeVisible();

    await page.goto("/");
    await page.getByRole("button", { name: "Pages of Acme" }).click();
    await page.getByRole("menuitem", { name: "Connectors", exact: true }).click();
    await expect(page).toHaveURL(`${nav.url}/orgs/${acme.id}/connections`);
    await expect(page.getByRole("heading", { name: "Connectors", level: 1 })).toBeVisible();
    // The organization's section bar says where the page is (SET-05).
    const sections = page.getByRole("navigation", { name: "Sections of Acme" });
    await expect(sections.getByRole("link", { name: "Connectors" })).toHaveAttribute(
      "aria-current",
      "page",
    );
  } finally {
    await context.close();
  }
});

test("the old organization addresses land on the organization's pages (SET-06, SET-07)", async ({
  browser,
  nav,
}) => {
  const { context, page } = await openPage(browser, nav);
  try {
    const root = `${nav.url}/`;

    await expectReplacesEntry(
      page,
      root,
      "/connectors",
      `${nav.url}/orgs/${acme.id}/connections`,
      async () => {
        await expect(page.getByRole("heading", { name: "Connectors", level: 1 })).toBeVisible();
      },
    );

    // A second page, so the entry before this redirect is the last one of its own history.
    const bookmark = await context.newPage();
    try {
      await expectReplacesEntry(
        bookmark,
        root,
        `/connectors/${connector.id}`,
        `${nav.url}/orgs/${acme.id}/connections/${connector.id}`,
        // The connection names no organization that does not own it (SET-07).
        async () => {
          await expect(
            bookmark.getByRole("heading", { name: CONNECTOR_NAME, level: 1 }),
          ).toBeVisible();
        },
      );
    } finally {
      await bookmark.close();
    }
  } finally {
    await context.close();
  }
});

test("a phone's menus reach the account and the organization's pages in finger-sized rows (SET-01, SET-04, SET-09, SET-12)", async ({
  browser,
  nav,
}) => {
  const { context, page } = await openPage(browser, nav, PHONE);
  try {
    // The account, from the phone's home screen.
    await page.goto("/");
    await page.getByRole("button", { name: "Account menu" }).click();
    const account = page.getByRole("menu");
    await expect(account.getByRole("menuitem", { name: "My account" })).toBeVisible();
    await settled(page);
    await expectFingerRows(page, account.getByRole("menuitem"), "the account menu");
    await account.getByRole("menuitem", { name: "My account" }).click();
    await expect(page).toHaveURL(`${nav.url}/account`);
    await expect(page.getByRole("heading", { name: "My account", level: 1 })).toBeVisible();

    // The organization's entry on the home screen, beside the full switcher.
    await page.goto("/");
    await page.getByRole("button", { name: "Pages of Acme" }).click();
    const gear = page.getByRole("menu", { name: "Pages of Acme" });
    await expect(gear.getByRole("menuitem", { name: "All organizations" })).toBeVisible();
    await settled(page);
    await expectFingerRows(page, gear.getByRole("menuitem"), "the organization menu");
    await page.keyboard.press("Escape");
    await expect(gear).toHaveCount(0);

    // The top bar of a full-screen page carries the same pages in the compact switcher's menu.
    await page.goto("/contacts");
    await page.getByRole("button", { name: "Organization: Acme" }).click();
    const switcher = page.getByRole("menu", { name: "Organizations" });
    await expect(switcher.getByRole("menuitem", { name: "Settings", exact: true })).toBeVisible();
    await settled(page);
    for (const [name, section] of [
      ["Org chart", "chart"],
      ["Members", "members"],
      ["Positions", "positions"],
      ["Connectors", "connections"],
      ["Settings", "settings"],
    ] as const) {
      await expect(switcher.getByRole("menuitem", { name, exact: true })).toHaveAttribute(
        "href",
        `/orgs/${acme.id}/${section}`,
      );
    }
    // The rows the feature adds and the picker it keeps are a finger's height; the switcher's own
    // "All organizations" and "New organization" rows keep the size they had before the split.
    await expectFingerRows(
      page,
      switcher.getByRole("menuitem", {
        name: /^(Org chart|Members|Positions|Connectors|Settings)$/,
      }),
      "the organization's pages",
    );
    await expectFingerRows(page, switcher.getByRole("menuitemradio"), "the organization picker");
    await switcher.getByRole("menuitem", { name: "Settings", exact: true }).click();
    await expect(page).toHaveURL(`${nav.url}/orgs/${acme.id}/settings`);
    await expect(
      page.getByRole("heading", { name: "Organization settings", level: 1 }),
    ).toBeVisible();
  } finally {
    await context.close();
  }
});
