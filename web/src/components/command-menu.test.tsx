import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeAgent, makeConnector, makeConversation, makeMe } from "@/test/fixtures";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

beforeEach(() => useLiveStore.setState({ locale: "en", paletteOpen: false }));
afterEach(() => vi.unstubAllGlobals());

function api(role: "member" | "admin" = "member") {
  return mockApi({
    "GET /me": json(200, makeMe({ role })),
    "GET /conversations": json(200, {
      items: [
        makeConversation({ id: "r1", title: "general" }),
        makeConversation({ id: "r2", title: "deploys" }),
      ],
    }),
    "GET /agents": json(200, { items: [makeAgent({ id: "a1", name: "release-bot" })] }),
    "GET /connectors": json(200, { items: [makeConnector({ id: "k1", name: "ops-bot" })] }),
  });
}

const palette = () => screen.findByRole("dialog", { name: "Go to" });
const options = (dialog: HTMLElement) =>
  within(dialog)
    .queryAllByRole("option")
    .map((o) => o.textContent ?? "");

describe("command palette (UI-22)", () => {
  it("opens with Ctrl+K and with Cmd+K, listing conversations, agents, connectors and pages", async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/");
    await screen.findByRole("list", { name: "Conversations" });
    expect(screen.queryByRole("dialog")).toBeNull();

    await user.keyboard("{Control>}k{/Control}");
    const dialog = await palette();
    await within(dialog).findByRole("option", { name: /general/ });
    const all = options(dialog);
    for (const name of [
      "general",
      "deploys",
      "release-bot",
      "ops-bot",
      "Conversations",
      "Settings",
    ]) {
      expect(all.some((o) => o.includes(name))).toBe(true);
    }
    // Members do not get admin pages.
    expect(all.some((o) => o.includes("Users"))).toBe(false);

    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await user.keyboard("{Meta>}k{/Meta}");
    expect(await palette()).toBeInTheDocument();
  });

  it("filters as the user types", async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/");
    await screen.findByRole("list", { name: "Conversations" });
    await user.keyboard("{Control>}k{/Control}");
    const dialog = await palette();
    await within(dialog).findByRole("option", { name: /general/ });

    await user.type(within(dialog).getByRole("combobox"), "deplo");
    await waitFor(() => expect(options(dialog)).toEqual([expect.stringContaining("deploys")]));

    await user.clear(within(dialog).getByRole("combobox"));
    await user.type(within(dialog).getByRole("combobox"), "zzzz");
    expect(await within(dialog).findByText("Nothing matches.")).toBeInTheDocument();
  });

  it("Enter navigates to the selected room and closes the palette", async () => {
    api();
    const user = userEvent.setup();
    const { router } = renderRoute("/");
    await screen.findByRole("list", { name: "Conversations" });
    await user.keyboard("{Control>}k{/Control}");
    const dialog = await palette();
    await within(dialog).findByRole("option", { name: /general/ });

    await user.type(within(dialog).getByRole("combobox"), "deploys");
    await waitFor(() => expect(options(dialog)).toHaveLength(1));
    await user.keyboard("{Enter}");

    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/r2"));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Go to" })).toBeNull());
  });

  it("jumps to connectors, agents and admin pages", async () => {
    api("admin");
    const user = userEvent.setup();
    const { router } = renderRoute("/");
    await screen.findByRole("list", { name: "Conversations" });

    const jump = async (query: string, path: string) => {
      await user.keyboard("{Control>}k{/Control}");
      const dialog = await palette();
      await within(dialog).findByRole("option", { name: /general/ });
      await user.type(within(dialog).getByRole("combobox"), query);
      await waitFor(() => expect(options(dialog)).toHaveLength(1));
      await user.keyboard("{Enter}");
      await waitFor(() => expect(router.state.location.pathname).toBe(path));
      await waitFor(() => expect(screen.queryByRole("dialog", { name: "Go to" })).toBeNull());
    };

    await jump("ops-bot", "/connectors/k1");
    await jump("release-bot", "/agents");
    await jump("audit", "/admin/audit");
  });

  it("opens from the sidebar search button", async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/");
    await screen.findByRole("list", { name: "Conversations" });
    await user.click(screen.getAllByRole("button", { name: /Search/ })[0] as HTMLElement);
    expect(await palette()).toBeInTheDocument();
  });
});
