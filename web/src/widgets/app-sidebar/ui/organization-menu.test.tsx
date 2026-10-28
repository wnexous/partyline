import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { json, mockApi } from "@/test/fetch";
import { makeConversation, makeMe, makeOrg } from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { useLiveStore } from "@/test/live-store";
import { stubViewport } from "@/test/media";
import { renderRoute } from "@/test/render";
import { sampleContacts } from "@/test/sample-contacts";

vi.mock("@/app/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * The organization's entry (SET-09, SET-11): the gear beside the full switcher and the compact
 * switcher's own menu on a phone's top bar list the same pages of the organization the switcher
 * shows, and "All organizations". Positions is the admins' page in both.
 */

const acme = makeOrg({ id: "o1", name: "Acme", slug: "acme", permission: "admin" });
const globex = makeOrg({ id: "o2", name: "Globex", slug: "globex", permission: "member" });

function api(over: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, { ...makeMe(), default_org_id: "o1" }),
    "GET /orgs": json(200, { items: [acme, globex] }),
    "GET /conversations": json(200, {
      items: [makeConversation({ id: "g1", title: "ops", org_ids: ["o1"] })],
    }),
    "GET /contacts": json(200, sampleContacts),
    ...over,
  });
}

/** The five pages of Acme, in the design's order, with the address each one opens. */
const PAGES = [
  ["Org chart", "/orgs/o1/chart"],
  ["Members", "/orgs/o1/members"],
  ["Positions", "/orgs/o1/positions"],
  ["Connectors", "/orgs/o1/connections"],
  ["Settings", "/orgs/o1/settings"],
] as const;

const button = (name: string | RegExp) => screen.findByRole("button", { name });

stubLayout();
beforeEach(() => {
  useLiveStore.setState({ locale: "en", currentOrgId: null });
  window.localStorage.clear();
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("the gear beside the switcher (SET-09)", () => {
  it("opens the organization's pages from the gear", async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await button("Pages of Acme"));
    const menu = await screen.findByRole("menu", { name: "Pages of Acme" });
    for (const [name, href] of PAGES) {
      expect(within(menu).getByRole("menuitem", { name })).toHaveAttribute("href", href);
    }
    expect(within(menu).getByRole("menuitem", { name: "All organizations" })).toHaveAttribute(
      "href",
      "/orgs",
    );
  });

  it("names the organization and the role at the top", async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await button("Pages of Acme"));
    const menu = await screen.findByRole("menu", { name: "Pages of Acme" });
    expect(within(menu).getByText("Acme")).toBeInTheDocument();
    expect(within(menu).getByText("Admin")).toBeInTheDocument();
  });

  it("hides positions from a member", async () => {
    api({
      "GET /orgs": json(200, { items: [{ ...acme, permission: "member" }, globex] }),
    });
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await button("Pages of Acme"));
    const menu = await screen.findByRole("menu", { name: "Pages of Acme" });
    expect(within(menu).getByRole("menuitem", { name: "Members" })).toBeInTheDocument();
    expect(within(menu).queryByRole("menuitem", { name: "Positions" })).toBeNull();
  });

  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    api({ "GET /me": json(200, { ...makeMe({ locale: "pt-BR" }), default_org_id: "o1" }) });
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await button("Páginas de Acme"));
    const menu = await screen.findByRole("menu", { name: "Páginas de Acme" });
    for (const name of ["Organograma", "Membros", "Cargos", "Conectores", "Configurações"]) {
      expect(within(menu).getByRole("menuitem", { name })).toBeInTheDocument();
    }
    expect(within(menu).getByRole("menuitem", { name: "Todas as organizações" })).toHaveAttribute(
      "href",
      "/orgs",
    );
  });
});

describe("the phone's top bar (SET-09)", () => {
  it("lists the same pages in the compact switcher's menu", async () => {
    stubViewport(360);
    api();
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await button(/^Organization: Acme/));
    const menu = await screen.findByRole("menu", { name: "Organizations" });
    for (const [name, href] of PAGES) {
      expect(within(menu).getByRole("menuitem", { name })).toHaveAttribute("href", href);
    }
    expect(within(menu).getByRole("menuitem", { name: "All organizations" })).toHaveAttribute(
      "href",
      "/orgs",
    );

    // The pick still changes the organization, and the pages follow it.
    await user.click(within(menu).getByRole("menuitemradio", { name: /^Globex/ }));
    await user.click(await button(/^Organization: Globex/));
    const next = await screen.findByRole("menu", { name: "Organizations" });
    expect(within(next).getByRole("menuitem", { name: "Org chart" })).toHaveAttribute(
      "href",
      "/orgs/o2/chart",
    );
    expect(within(next).getByRole("menuitem", { name: "Settings" })).toHaveAttribute(
      "href",
      "/orgs/o2/settings",
    );
    expect(within(next).queryByRole("menuitem", { name: "Positions" })).toBeNull();
  });
});
