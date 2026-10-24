import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeConversation, makeMe, makeRoom } from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { stubViewport } from "@/test/media";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * The messenger shell (LAY-01, LAY-05, LAY-06): a conversation sidebar beside the page on wide
 * screens; below 768 px the list is the first screen and everything else opens full screen with
 * a way back. The header menu keeps every former navigation destination.
 */

function api(role: "member" | "admin" = "member") {
  return mockApi({
    "GET /me": json(200, makeMe({ role })),
    "GET /conversations": json(200, {
      items: [
        makeConversation({ id: "r1", title: "deploys" }),
        makeConversation({ id: "r2", title: "support-bridge" }),
      ],
    }),
    "GET /rooms/r1": json(200, makeRoom({ id: "r1", name: "deploys" })),
    "GET /rooms/r1/members": json(200, { items: [] }),
    "GET /rooms/r1/messages": json(200, { items: [], has_more: false }),
    "GET /connectors": json(200, { items: [] }),
  });
}

const conversationList = () => screen.findByRole("list", { name: "Conversations" });
const BACK = "Back to conversations";

stubLayout();
beforeEach(() => useLiveStore.setState({ locale: "en", openRoomId: null }));
afterEach(() => vi.unstubAllGlobals());

describe("wide screens (LAY-01)", () => {
  it("keeps the conversation list beside the open conversation, with no way back needed", async () => {
    api();
    renderRoute("/rooms/r1");
    expect(await screen.findByRole("heading", { name: "deploys", level: 1 })).toBeInTheDocument();
    expect(screen.getByRole("complementary", { name: "Conversations" })).toContainElement(
      await conversationList(),
    );
    expect(screen.queryByRole("link", { name: BACK })).toBeNull();
  });

  it("asks to pick a conversation on the home page", async () => {
    api();
    renderRoute("/");
    expect(
      await screen.findByRole("heading", { name: "Pick a conversation", level: 1 }),
    ).toBeInTheDocument();
    expect(await conversationList()).toBeInTheDocument();
  });

  it("keeps the list beside the other pages too", async () => {
    api();
    renderRoute("/connectors");
    expect(
      await screen.findByRole("heading", { name: "Connectors", level: 1 }),
    ).toBeInTheDocument();
    expect(await conversationList()).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: BACK })).toBeNull();
  });

  it("sends the old rooms list address to the home page", async () => {
    api();
    const { router } = renderRoute("/rooms");
    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
    expect(await screen.findByRole("heading", { name: "Pick a conversation" })).toBeInTheDocument();
  });
});

describe("below 768 px (LAY-05)", () => {
  beforeEach(() => stubViewport(360));

  it("makes the conversation list the first screen", async () => {
    api();
    renderRoute("/");
    expect(await conversationList()).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Conversations", level: 1 })).toBeInTheDocument();
    expect(screen.queryByText("Pick a conversation")).toBeNull();
    expect(screen.queryByRole("complementary", { name: "Conversations" })).toBeNull();
  });

  it("opens a conversation full screen with a back button that returns to the list", async () => {
    api();
    const user = userEvent.setup();
    const { router } = renderRoute("/");
    const list = await conversationList();

    await user.click(within(list).getByRole("link", { name: /deploys/ }));
    expect(await screen.findByRole("heading", { name: "deploys", level: 1 })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/rooms/r1");
    expect(screen.queryByRole("list", { name: "Conversations" })).toBeNull();

    await user.click(screen.getByRole("link", { name: BACK }));
    expect(await conversationList()).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/");
  });

  it("opens the other pages full screen with the same way back", async () => {
    api();
    const user = userEvent.setup();
    const { router } = renderRoute("/connectors");
    expect(
      await screen.findByRole("heading", { name: "Connectors", level: 1 }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Conversations" })).toBeNull();

    await user.click(screen.getByRole("link", { name: BACK }));
    expect(await conversationList()).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/");
  });
});

describe("header menu (LAY-06)", () => {
  const openMenu = async () => {
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Account menu" }));
    return screen.findByRole("menu");
  };
  const destinations = (menu: HTMLElement) =>
    within(menu)
      .getAllByRole("menuitem")
      .filter((item) => item.hasAttribute("href"))
      .map((item) => [item.textContent, item.getAttribute("href")]);

  it("reaches organizations, contacts, connectors, linked accounts, devices, tokens and settings, and no admin page for a member", async () => {
    api("member");
    renderRoute("/");
    const menu = await openMenu();
    expect(destinations(menu)).toEqual([
      ["Conversations", "/"],
      ["Organizations", "/orgs"],
      ["Contacts", "/contacts"],
      ["Connectors", "/connectors"],
      ["Identities", "/identities"],
      ["Devices", "/devices"],
      ["API tokens", "/tokens"],
      ["Settings", "/settings"],
    ]);
  });

  it("adds the admin pages for an admin", async () => {
    api("admin");
    renderRoute("/");
    const menu = await openMenu();
    expect(destinations(menu).slice(-3)).toEqual([
      ["Users", "/admin/users"],
      ["Invites", "/admin/invites"],
      ["Audit log", "/admin/audit"],
    ]);
  });

  it("opens the page of the chosen entry", async () => {
    api();
    const user = userEvent.setup();
    const { router } = renderRoute("/");
    const menu = await openMenu();
    await user.click(within(menu).getByRole("menuitem", { name: "Connectors" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/connectors"));
  });

  it("shows the person's name and handle at the top of the menu", async () => {
    api();
    renderRoute("/");
    const menu = await openMenu();
    expect(within(menu).getByText("Ana Souza")).toBeInTheDocument();
    expect(within(menu).getByText("@ana")).toBeInTheDocument();
  });
});
