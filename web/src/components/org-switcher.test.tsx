import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import { CURRENT_ORG_KEY } from "@/lib/orgs";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeConversation, makeMe, makeOrg } from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { useLiveStore } from "@/test/live-store";
import { stubViewport } from "@/test/media";
import { renderRoute } from "@/test/render";
import { sampleContacts } from "@/test/sample-contacts";

expect.extend(matchers);

vi.mock("@/app/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * The organization switcher (T39: ORG-01, ORG-02, CHART-02): the viewer's organizations with what
 * they are in each and what waits unread in each, the pick that the sidebar follows, and the way
 * to start a new organization. Read through the real shell, where the switcher lives.
 */

const AXE_OPTIONS = { rules: { "color-contrast": { enabled: false } } };

const acme = makeOrg({ id: "o1", name: "Acme", slug: "acme", permission: "admin" });
const globex = makeOrg({ id: "o2", name: "Globex", slug: "globex", permission: "member" });
const initech = makeOrg({ id: "o3", name: "Initech", slug: "initech", permission: "guest" });

// Acme holds 2 + 3 unread, Globex 5 + 3: the DM with Bob is filed under both.
const conversations = [
  makeConversation({ id: "g1", title: "ops", org_ids: ["o1"], unread: 2 }),
  makeConversation({ id: "g2", title: "sales", org_ids: ["o2"], unread: 5 }),
  makeConversation({
    id: "d1",
    kind: "dm",
    title: "Bob Lima",
    org_ids: ["o1", "o2"],
    unread: 3,
  }),
  makeConversation({ id: "g3", title: "hr", org_ids: ["o3"], unread: 0 }),
];

function api(over: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, { ...makeMe(), default_org_id: "o1" }),
    "GET /orgs": json(200, { items: [acme, globex, initech] }),
    "GET /conversations": json(200, { items: conversations }),
    ...over,
  });
}

const trigger = (name: RegExp | string = /^Organization:/) => screen.findByRole("button", { name });
const items = () => screen.getAllByRole("menuitemradio");
const labels = () => items().map((i) => i.getAttribute("aria-label"));

stubLayout();
beforeEach(() => {
  useLiveStore.setState({ locale: "en", currentOrgId: null });
  window.localStorage.clear();
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("the organizations it lists", () => {
  it("shows the current organization on the button and every organization of the viewer in the menu, each with what they are in it", async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await trigger("Organization: Acme, 5 unread messages in other organizations"));
    expect(await screen.findByRole("menu", { name: "Organizations" })).toBeInTheDocument();
    expect(labels()).toEqual([
      "Acme, Admin, 5 unread messages",
      "Globex, Member, 8 unread messages",
      "Initech, Guest",
    ]);
  });

  it("counts a DM shared by two organizations in both of them", async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await trigger());
    const [acmeItem, globexItem] = items();
    // Acme: ops 2 + the DM 3. Globex: sales 5 + the same DM 3.
    expect(within(acmeItem as HTMLElement).getByText("5")).toBeInTheDocument();
    expect(within(globexItem as HTMLElement).getByText("8")).toBeInTheDocument();
  });

  it("marks the current organization as the checked one", async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await trigger());
    expect(screen.getByRole("menuitemradio", { name: /^Acme/ })).toBeChecked();
    expect(screen.getByRole("menuitemradio", { name: /^Globex/ })).not.toBeChecked();
    expect(screen.getByRole("menuitemradio", { name: /^Initech/ })).not.toBeChecked();
  });

  it("shows no count for an organization where nothing waits, and no badge on the button when nothing waits elsewhere", async () => {
    api({
      "GET /conversations": json(200, {
        items: [makeConversation({ id: "g1", org_ids: ["o1"], unread: 4 })],
      }),
    });
    const user = userEvent.setup();
    renderRoute("/");
    const button = await trigger("Organization: Acme");
    expect(button).toBeInTheDocument();
    await user.click(button);
    expect(labels()).toEqual([
      "Acme, Admin, 4 unread messages",
      "Globex, Member",
      "Initech, Guest",
    ]);
  });

  it("keeps a long count readable: 99+ on screen, the full number for screen readers", async () => {
    api({
      "GET /conversations": json(200, {
        items: [makeConversation({ id: "g1", org_ids: ["o2"], unread: 120 })],
      }),
    });
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await trigger());
    const globexItem = screen.getByRole("menuitemradio", {
      name: "Globex, Member, 120 unread messages",
    });
    expect(within(globexItem).getByText("99+")).toBeInTheDocument();
  });

  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    api({ "GET /me": json(200, { ...makeMe({ locale: "pt-BR" }), default_org_id: "o1" }) });
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await trigger(/^Organização:/));
    expect(await screen.findByRole("menu", { name: "Organizações" })).toBeInTheDocument();
    expect(labels()).toEqual([
      "Acme, Administrador, 5 mensagens não lidas",
      "Globex, Membro, 8 mensagens não lidas",
      "Initech, Convidado",
    ]);
    expect(screen.getByRole("menuitem", { name: "Nova organização" })).toBeInTheDocument();
  });
});

describe("switching (ORG-02)", () => {
  it("makes the picked organization the current one: the button and the sidebar follow", async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/");
    await screen.findByRole("link", { name: /ops/ });
    expect(screen.queryByRole("link", { name: /sales/ })).toBeNull();

    await user.click(await trigger());
    await user.click(screen.getByRole("menuitemradio", { name: /^Globex/ }));

    expect(await trigger(/^Organization: Globex/)).toBeInTheDocument();
    expect(await screen.findByRole("link", { name: /sales/ })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /ops/ })).toBeNull();
    expect(useLiveStore.getState().currentOrgId).toBe("o2");
  });

  it("keeps the pick in this browser, so a reload lands on the same organization", async () => {
    api();
    const user = userEvent.setup();
    const first = renderRoute("/");
    await user.click(await trigger());
    await user.click(screen.getByRole("menuitemradio", { name: /^Initech/ }));
    await trigger(/^Organization: Initech/);
    expect(window.localStorage.getItem(CURRENT_ORG_KEY)).toBe("o3");
    first.unmount();

    // A reload starts the store again from what the browser kept.
    useLiveStore.setState({ currentOrgId: window.localStorage.getItem(CURRENT_ORG_KEY) });
    renderRoute("/");
    expect(await trigger(/^Organization: Initech/)).toBeInTheDocument();
  });

  it("falls back to the account's default when the browser refuses to read storage", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("denied", "SecurityError");
    });
    api({ "GET /me": json(200, { ...makeMe(), default_org_id: "o2" }) });
    // What the store reads at start-up when the browser refuses.
    useLiveStore.setState({ currentOrgId: null });
    renderRoute("/");
    expect(await trigger(/^Organization: Globex/)).toBeInTheDocument();
  });

  it("still switches for this tab when the browser refuses to store the pick", async () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("denied", "SecurityError");
    });
    api();
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await trigger());
    await user.click(screen.getByRole("menuitemradio", { name: /^Globex/ }));
    expect(await trigger(/^Organization: Globex/)).toBeInTheDocument();
  });

  it("starts on the first organization when the account has no default and nothing was picked", async () => {
    api({ "GET /me": json(200, makeMe()) });
    renderRoute("/");
    expect(await trigger(/^Organization: Acme/)).toBeInTheDocument();
  });

  it("ignores a pick of an organization the viewer has left", async () => {
    window.localStorage.setItem(CURRENT_ORG_KEY, "gone");
    useLiveStore.setState({ currentOrgId: "gone" });
    api({ "GET /me": json(200, { ...makeMe(), default_org_id: "o3" }) });
    renderRoute("/");
    expect(await trigger(/^Organization: Initech/)).toBeInTheDocument();
  });
});

describe("beside the organization's own entry (SET-09, SET-10)", () => {
  it("keeps its own job: the picker, the counts, all organizations and the new one, without the pages the gear beside it lists", async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await trigger());
    const menu = await screen.findByRole("menu", { name: "Organizations" });
    expect(within(menu).getAllByRole("menuitemradio")).toHaveLength(3);
    expect(within(menu).getByRole("menuitem", { name: "All organizations" })).toHaveAttribute(
      "href",
      "/orgs",
    );
    expect(within(menu).getByRole("menuitem", { name: "New organization" })).toBeInTheDocument();
    // The pages are the gear's beside the full switcher; they are not listed twice in one menu.
    expect(within(menu).queryByRole("menuitem", { name: "Org chart" })).toBeNull();
    expect(within(menu).queryByRole("menuitem", { name: "Settings" })).toBeNull();
  });

  it("carries the organization's pages in a phone's top bar, where no gear fits", async () => {
    stubViewport(360);
    api({ "GET /contacts": json(200, sampleContacts) });
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await trigger(/^Organization: Acme/));
    const menu = await screen.findByRole("menu", { name: "Organizations" });
    for (const [name, href] of [
      ["Org chart", "/orgs/o1/chart"],
      ["Members", "/orgs/o1/members"],
      ["Positions", "/orgs/o1/positions"],
      ["Connectors", "/orgs/o1/connections"],
      ["Settings", "/orgs/o1/settings"],
    ] as const) {
      expect(within(menu).getByRole("menuitem", { name })).toHaveAttribute("href", href);
    }
    expect(within(menu).getByRole("menuitem", { name: "All organizations" })).toHaveAttribute(
      "href",
      "/orgs",
    );
    // Nothing of the switcher's own job was lost by adding them.
    expect(within(menu).getAllByRole("menuitemradio")).toHaveLength(3);
    expect(within(menu).getByRole("menuitem", { name: "New organization" })).toBeInTheDocument();
    // The pages follow the pick, and the pick still changes the organization.
    await user.click(within(menu).getByRole("menuitemradio", { name: /^Globex/ }));
    await user.click(await trigger(/^Organization: Globex/));
    expect(await screen.findByRole("menuitem", { name: "Settings" })).toHaveAttribute(
      "href",
      "/orgs/o2/settings",
    );
  });

  it("has no gear on a phone's top bar: the switcher is the organization's entry there", async () => {
    stubViewport(360);
    api({ "GET /contacts": json(200, sampleContacts) });
    renderRoute("/contacts");
    expect(await trigger(/^Organization: Acme/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Pages of / })).toBeNull();
  });
});

describe("a new organization (ORG-01)", () => {
  it('"+ New organization" opens the dialog that asks for its name', async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await trigger());
    await user.click(screen.getByRole("menuitem", { name: "New organization" }));
    const dialog = await screen.findByRole("dialog", { name: "New organization" });
    expect(within(dialog).getByRole("textbox", { name: "Name" })).toHaveFocus();
    expect(within(dialog).getByRole("button", { name: "Create organization" })).toBeInTheDocument();
  });

  it("creates it, makes it the current one and lists it", async () => {
    const created = makeOrg({ id: "o9", name: "Hooli", slug: "hooli", permission: "admin" });
    let listed = [acme, globex, initech];
    const { calls } = api({
      "GET /orgs": () => json(200, { items: listed }),
      "POST /orgs": () => {
        listed = [acme, globex, created, initech];
        return json(201, created);
      },
    });
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await trigger());
    await user.click(screen.getByRole("menuitem", { name: "New organization" }));
    const dialog = await screen.findByRole("dialog", { name: "New organization" });
    await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "  Hooli ");
    await user.click(within(dialog).getByRole("button", { name: "Create organization" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(calls.find((c) => c.method === "POST" && c.path === "/orgs")?.body).toEqual({
      name: "Hooli",
    });
    expect(await trigger(/^Organization: Hooli/)).toBeInTheDocument();
    expect(useLiveStore.getState().currentOrgId).toBe("o9");
    await user.click(screen.getByRole("button", { name: /^Organization: Hooli/ }));
    expect(labels()).toContain("Hooli, Admin");
  });

  it("shows it at once, before the list of organizations is fetched again, so the button never lands on another one first", async () => {
    const created = makeOrg({ id: "o9", name: "Hooli", slug: "hooli", permission: "admin" });
    let held = false;
    let release: () => void = () => {};
    api({
      "GET /orgs": () =>
        held
          ? new Promise<Response>((resolve) => {
              release = () => resolve(json(200, { items: [acme, globex, created, initech] }));
            })
          : json(200, { items: [acme, globex, initech] }),
      "POST /orgs": () => {
        held = true;
        return json(201, created);
      },
    });
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await trigger());
    await user.click(screen.getByRole("menuitem", { name: "New organization" }));
    const dialog = await screen.findByRole("dialog", { name: "New organization" });
    await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Hooli");
    await user.click(within(dialog).getByRole("button", { name: "Create organization" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    // The list is still on its way: the answer of the POST already put the organization in it.
    expect(await trigger(/^Organization: Hooli/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Organization: Acme/ })).toBeNull();
    release();
  });

  it("refuses a name over 100 characters before asking the server, and a blank one", async () => {
    const { calls } = api();
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await trigger());
    await user.click(screen.getByRole("menuitem", { name: "New organization" }));
    const dialog = await screen.findByRole("dialog", { name: "New organization" });
    const name = within(dialog).getByRole("textbox", { name: "Name" });
    const create = within(dialog).getByRole("button", { name: "Create organization" });

    await user.click(create);
    expect(await within(dialog).findByText("Required")).toBeInTheDocument();

    await user.clear(name);
    await user.type(name, "x".repeat(101));
    await user.click(create);
    expect(await within(dialog).findByText("At most 100 characters")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });

  it("accepts a name of exactly 100 characters, counted in characters and not bytes", async () => {
    const name = "é".repeat(100);
    const { calls } = api({
      "POST /orgs": json(201, makeOrg({ id: "o9", name, slug: "e", permission: "admin" })),
    });
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await trigger());
    await user.click(screen.getByRole("menuitem", { name: "New organization" }));
    const dialog = await screen.findByRole("dialog", { name: "New organization" });
    await user.click(within(dialog).getByRole("textbox", { name: "Name" }));
    await user.paste(name);
    await user.click(within(dialog).getByRole("button", { name: "Create organization" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ name });
  });

  it("shows the server's reason under the name when it refuses (422)", async () => {
    api({
      "POST /orgs": apiError(422, "invalid", "invalid", { name: "must not be blank" }),
    });
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await trigger());
    await user.click(screen.getByRole("menuitem", { name: "New organization" }));
    const dialog = await screen.findByRole("dialog", { name: "New organization" });
    await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Hooli");
    await user.click(within(dialog).getByRole("button", { name: "Create organization" }));
    expect(await within(dialog).findByText("must not be blank")).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("says so, and stays open, when the server cannot be reached", async () => {
    api({ "POST /orgs": () => Promise.reject(new TypeError("offline")) as never });
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await trigger());
    await user.click(screen.getByRole("menuitem", { name: "New organization" }));
    const dialog = await screen.findByRole("dialog", { name: "New organization" });
    await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Hooli");
    await user.click(within(dialog).getByRole("button", { name: "Create organization" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Can’t reach the server. Check your connection and try again.",
    );
  });

  it("starts empty every time it opens", async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await trigger());
    await user.click(screen.getByRole("menuitem", { name: "New organization" }));
    let dialog = await screen.findByRole("dialog", { name: "New organization" });
    await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Hooli");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    await user.click(await trigger());
    await user.click(screen.getByRole("menuitem", { name: "New organization" }));
    dialog = await screen.findByRole("dialog", { name: "New organization" });
    expect(within(dialog).getByRole("textbox", { name: "Name" })).toHaveValue("");
  });
});

describe("when the organizations are not known", () => {
  it("offers only to create one to a viewer who belongs to none", async () => {
    api({ "GET /orgs": json(200, { items: [] }) });
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await screen.findByRole("button", { name: "New organization" }));
    expect(await screen.findByRole("dialog", { name: "New organization" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Organization:/ })).toBeNull();
  });

  it("shows nothing, and leaves the rest of the sidebar alone, when the list cannot be read", async () => {
    api({ "GET /orgs": apiError(500, "internal", "boom") });
    renderRoute("/");
    expect(await screen.findByRole("link", { name: /ops/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Organization:/ })).toBeNull();
    expect(screen.queryByRole("button", { name: "New organization" })).toBeNull();
  });

  it("holds its place with a skeleton while the organizations load", async () => {
    let release: () => void = () => {};
    api({
      "GET /orgs": () =>
        new Promise<Response>((resolve) => {
          release = () => resolve(json(200, { items: [acme, globex] }));
        }),
    });
    renderRoute("/");
    await screen.findByRole("button", { name: "New conversation" });
    expect(screen.queryByRole("button", { name: /^Organization:/ })).toBeNull();
    release();
    expect(await trigger(/^Organization: Acme/)).toBeInTheDocument();
  });
});

describe("on a phone", () => {
  it("is in the top bar of a full-screen page, as a picture with the name for screen readers, and switches the same way", async () => {
    stubViewport(360);
    api({
      "GET /contacts": json(200, {
        me: makeMe().intelligence,
        org_ais: [],
        hosted: [],
        users: [],
        people: [],
      }),
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await trigger(/^Organization: Acme/));
    expect(labels()).toEqual([
      "Acme, Admin, 5 unread messages",
      "Globex, Member, 8 unread messages",
      "Initech, Guest",
    ]);
    await user.click(screen.getByRole("menuitemradio", { name: /^Globex/ }));
    expect(await trigger(/^Organization: Globex/)).toBeInTheDocument();
  });

  it("is in the phone's home screen, above the search", async () => {
    stubViewport(360);
    api();
    renderRoute("/");
    expect(await trigger(/^Organization: Acme/)).toBeInTheDocument();
    expect(await screen.findByRole("link", { name: /ops/ })).toBeInTheDocument();
  });
});

describe("accessibility", () => {
  it("has no axe violations: the button in the sidebar, the open menu and the dialog", async () => {
    api();
    const user = userEvent.setup();
    const { container } = renderRoute("/");
    await user.click(await trigger());
    const menu = await screen.findByRole("menu", { name: "Organizations" });
    expect(await axe(menu, AXE_OPTIONS)).toHaveNoViolations();
    await user.click(screen.getByRole("menuitem", { name: "New organization" }));
    const dialog = await screen.findByRole("dialog", { name: "New organization" });
    expect(await axe(dialog, AXE_OPTIONS)).toHaveNoViolations();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("has no axe violations in Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    api({ "GET /me": json(200, { ...makeMe({ locale: "pt-BR" }), default_org_id: "o1" }) });
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await trigger(/^Organização:/));
    const menu = await screen.findByRole("menu", { name: "Organizações" });
    expect(await axe(menu, AXE_OPTIONS)).toHaveNoViolations();
    await user.click(screen.getByRole("menuitem", { name: "Nova organização" }));
    const dialog = await screen.findByRole("dialog", { name: "Nova organização" });
    expect(await axe(dialog, AXE_OPTIONS)).toHaveNoViolations();
  });
});
