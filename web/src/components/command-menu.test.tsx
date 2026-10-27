import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import {
  makeAgent,
  makeAI,
  makeConnector,
  makeConversation,
  makeMe,
  makeProfile,
} from "@/test/fixtures";
import { LoadedImage } from "@/test/image";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

beforeEach(() => useLiveStore.setState({ locale: "en", paletteOpen: false }));
afterEach(() => vi.unstubAllGlobals());

function api(role: "member" | "admin" = "member", extra: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, makeMe({ role })),
    "GET /conversations": json(200, {
      items: [
        makeConversation({ id: "r1", title: "general" }),
        makeConversation({ id: "r2", title: "deploys" }),
      ],
    }),
    "GET /agents": json(200, { items: [makeAgent({ id: "a1", name: "release-bot" })] }),
    "GET /connectors": json(200, {
      items: [makeConnector({ id: "k1", name: "ops-bot", org_id: "o1" })],
    }),
    ...extra,
  });
}

const palette = () => screen.findByRole("dialog", { name: "Go to" });
const options = (dialog: HTMLElement) =>
  within(dialog)
    .queryAllByRole("option")
    .map((o) => o.textContent ?? "");

describe("command palette (UI-22)", () => {
  it("lists an AI with a human-looking name by its @handle, with no AI badge (PROF-09, AD-024)", async () => {
    mockApi({
      "GET /me": json(200, makeMe({ role: "member" })),
      "GET /conversations": json(200, {
        items: [makeConversation({ id: "r1", title: "general" })],
      }),
      "GET /agents": json(200, {
        items: [
          makeAgent({
            id: "a9",
            intelligence: makeAI({ id: "a9", display_name: "Ana Lima", handle: "atlas" }),
          }),
        ],
      }),
      "GET /connectors": json(200, { items: [makeConnector({ id: "k1", name: "ops-bot" })] }),
    });
    const user = userEvent.setup();
    renderRoute("/");
    await screen.findByRole("list", { name: "Conversations" });
    await user.keyboard("{Control>}k{/Control}");
    const dialog = await palette();
    const option = await within(dialog).findByRole("option", { name: /Ana Lima/ });
    // The handle tells it from a person called Ana Lima; nothing in the row says "AI".
    expect(within(option).getByText("@atlas")).toBeInTheDocument();
    expect(within(option).queryByText(/^(AI|IA)$/)).toBeNull();
    expect(within(option).queryByTestId("kind-badge")).toBeNull();
    // The group is named for what it holds, not for the kind of the identities in it.
    expect(
      within(dialog).getByText("Contacts", { selector: "[cmdk-group-heading]" }),
    ).toBeVisible();
    expect(within(dialog).queryByText("Agents")).toBeNull();
  });

  it("opens with Ctrl+K and with Cmd+K, listing conversations, contacts, connectors and pages", async () => {
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
      "Organizations",
      "My account",
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

  it("lists the pages from lib/nav and opens a connection under its organization (SET-03, SET-11)", async () => {
    api();
    const user = userEvent.setup();
    const { router } = renderRoute("/");
    await screen.findByRole("list", { name: "Conversations" });

    const jump = async (query: string) => {
      await user.keyboard("{Control>}k{/Control}");
      const dialog = await palette();
      await within(dialog).findByRole("option", { name: /general/ });
      await user.type(within(dialog).getByRole("combobox"), query);
      await waitFor(() => expect(options(dialog)).toHaveLength(1));
      await user.keyboard("{Enter}");
      await waitFor(() => expect(screen.queryByRole("dialog", { name: "Go to" })).toBeNull());
    };

    await user.keyboard("{Control>}k{/Control}");
    const dialog = await palette();
    await within(dialog).findByRole("option", { name: /general/ });
    const all = options(dialog);
    for (const name of [
      "My account",
      "Identities",
      "API tokens",
      "Devices",
      "Conversations",
      "Contacts",
      "Organizations",
    ]) {
      expect(all).toContain(name);
    }
    // The old top-level destinations are pages of the account or of an organization now.
    expect(all).not.toContain("Connectors");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Go to" })).toBeNull());

    await jump("contacts");
    await waitFor(() => expect(router.state.location.pathname).toBe("/contacts"));
    await jump("ops-bot");
    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs/o1/connections/k1"));
  });

  it("draws each conversation with its face: a group's photo or initials, a DM's counterpart (GAV-07)", async () => {
    vi.stubGlobal("Image", LoadedImage);
    const photo = "/api/v1/rooms/r1/avatar?v=abc";
    api("member", {
      "GET /conversations": json(200, {
        items: [
          makeConversation({ id: "r1", title: "general", avatar_url: photo }),
          makeConversation({ id: "r2", title: "deploys" }),
          makeConversation({
            id: "r3",
            kind: "dm",
            title: "Atlas",
            counterpart: makeAI({ id: "a1", display_name: "Atlas", handle: "atlas" }),
          }),
        ],
      }),
    });
    const user = userEvent.setup();
    renderRoute("/");
    await screen.findByRole("list", { name: "Conversations" });
    await user.keyboard("{Control>}k{/Control}");
    const dialog = await palette();
    const face = async (name: RegExp) =>
      within(await within(dialog).findByRole("option", { name })).getByTestId(
        "intelligence-avatar",
      );

    const group = await face(/general/);
    expect(group.querySelector("img")).toHaveAttribute("src", photo);
    expect(group).toHaveAttribute("aria-hidden", "true"); // the name is beside it
    expect(await face(/deploys/)).toHaveTextContent("D");
    expect((await face(/deploys/)).querySelector("img")).toBeNull();
    expect(await face(/Atlas/)).toHaveTextContent("A");
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

  it("jumps to connectors and admin pages", async () => {
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

    // A connection result goes to the organization that owns it (SET-07, SET-11).
    await jump("ops-bot", "/orgs/o1/connections/k1");
    await jump("audit", "/admin/audit");
  });

  it("shows an AI's profile in the panel, without leaving the page (PRF-01)", async () => {
    api("member", {
      "GET /intelligences/a1": json(
        200,
        makeProfile({ id: "a1", display_name: "release-bot", handle: "release-bot", kind: "ai" }),
      ),
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/");
    await screen.findByRole("list", { name: "Conversations" });

    await user.keyboard("{Control>}k{/Control}");
    const dialog = await palette();
    await within(dialog).findByRole("option", { name: /general/ });
    await user.type(within(dialog).getByRole("combobox"), "release-bot");
    await waitFor(() => expect(options(dialog)).toHaveLength(1));
    await user.keyboard("{Enter}");

    const panel = await screen.findByRole("dialog", { name: "release-bot" });
    expect(within(panel).getByRole("link", { name: "Open full profile" })).toHaveAttribute(
      "href",
      "/contacts/a1",
    );
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Go to" })).toBeNull());
    expect(router.state.location.pathname).toBe("/");
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
