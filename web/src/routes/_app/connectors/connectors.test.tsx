import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type Connector, SecretMask } from "@/lib/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeAgent,
  makeAI,
  makeChat,
  makeConnector,
  makeConnectorKind,
  makeIntelligence,
  makeMe,
  makeRoom,
} from "@/test/fixtures";
import { renderRoute } from "@/test/render";
import { sampleContacts } from "@/test/sample-contacts";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

const me = makeMe();
const telegram = makeConnectorKind();
const webhook = makeConnectorKind({
  name: "webhook",
  label: "Webhook",
  can_list_chats: false,
  fields: [
    {
      name: "secret",
      label: "Signing secret",
      type: "secret",
      required: true,
      secret: true,
      generated: true,
      help: "Leave empty to generate one; it is shown once",
    },
  ],
});
const kinds = json(200, { items: [telegram, webhook] });

beforeEach(() => useLiveStore.setState({ locale: "en", connectors: {} }));
afterEach(() => vi.unstubAllGlobals());

const row = (name: string) => screen.getByRole("listitem", { name });

describe("connectors list (UI-17)", () => {
  it("shows each connector's status and last error", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /connectors": json(200, {
        items: [
          makeConnector({ id: "k1", name: "ops-bot", status: "connected" }),
          makeConnector({
            id: "k2",
            name: "support",
            kind: "whatsapp",
            status: "error",
            last_error: "session logged out",
          }),
        ],
      }),
    });
    renderRoute("/connectors");
    await screen.findByRole("listitem", { name: "ops-bot" });
    expect(within(row("ops-bot")).getByText("Connected")).toBeInTheDocument();
    expect(within(row("ops-bot")).getByText("Telegram")).toBeInTheDocument();
    expect(within(row("support")).getByText("Error")).toBeInTheDocument();
    expect(within(row("support")).getByText("session logged out")).toBeInTheDocument();
    expect(within(row("ops-bot")).getByRole("link")).toHaveAttribute("href", "/connectors/k1");
  });

  it("updates the status live from connector.status events", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /connectors": json(200, { items: [makeConnector({ id: "k1", name: "ops-bot" })] }),
    });
    const { queryClient } = renderRoute("/connectors");
    await screen.findByRole("listitem", { name: "ops-bot" });
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "connector.status",
        data: { connector_id: "k1", status: "error", last_error: "token rejected" },
      }),
    );
    expect(within(row("ops-bot")).getByText("Error")).toBeInTheDocument();
    expect(within(row("ops-bot")).getByText("token rejected")).toBeInTheDocument();
  });

  it("shows an empty state that offers to add a connector", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /connectors": json(200, { items: [] }),
    });
    renderRoute("/connectors");
    expect(await screen.findByRole("heading", { name: "No connectors yet" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add connector" })).toBeInTheDocument();
  });
});

describe("add connector (UI-16)", () => {
  async function openAdd(routes: Parameters<typeof mockApi>[0] = {}) {
    const api = mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /connectors": json(200, { items: [] }),
      ...routes,
    });
    const user = userEvent.setup();
    const utils = renderRoute("/connectors");
    await user.click(await screen.findByRole("button", { name: "Add connector" }));
    const dialog = await screen.findByRole("dialog", { name: "Add connector" });
    return { ...api, ...utils, user, dialog };
  }

  it("renders the chosen kind's form and posts kind, name and config", async () => {
    const created = makeConnector({ id: "k9", name: "ops-bot" });
    const { user, dialog, calls, router } = await openAdd({
      "POST /connectors": json(201, created),
      "GET /connectors/k9": json(200, created),
      "GET /connectors/k9/chats": json(200, { items: [] }),
    });
    await user.click(within(dialog).getByRole("radio", { name: /Telegram/ }));
    expect(within(dialog).getByLabelText("Bot token")).toHaveAttribute("type", "password");
    await user.type(within(dialog).getByLabelText("Name"), "ops-bot");
    await user.type(within(dialog).getByLabelText("Bot token"), "123:abc");
    await user.click(within(dialog).getByRole("button", { name: "Add connector" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/connectors/k9"));
    expect(calls.find((c) => c.method === "POST" && c.path === "/connectors")?.body).toEqual({
      kind: "telegram",
      name: "ops-bot",
      config: {
        bot_token: "123:abc",
        api_url: "",
        greeting: "hello",
        groups_only: false,
        allowed_chats: [],
      },
    });
  });

  it("switching kind swaps the fields", async () => {
    const { user, dialog } = await openAdd();
    await user.click(within(dialog).getByRole("radio", { name: /Webhook/ }));
    expect(within(dialog).getByLabelText("Signing secret")).toHaveAttribute("type", "password");
    expect(within(dialog).queryByLabelText("Bot token")).toBeNull();
  });

  it("shows generated secrets once with the inbound URL, then keeps them masked", async () => {
    // The webhook kind as the server describes it: a required secret it generates when empty.
    const hook = webhook;
    const stored = makeConnector({
      id: "k9",
      kind: "webhook",
      name: "ci-hook",
      config: { secret: SecretMask },
      inbound_url: "https://pl.example.test/api/v1/hooks/k9",
    });
    const { user, dialog, router, queryClient, calls } = await openAdd({
      "GET /connector-kinds": json(200, { items: [hook] }),
      "POST /connectors": json(201, {
        ...stored,
        generated_secrets: { secret: "s3cr3t-generated" },
      }),
      "GET /connectors/k9": json(200, stored),
      "GET /connectors/k9/chats": json(200, { items: [] }),
    });
    await user.type(within(dialog).getByLabelText("Name"), "ci-hook");
    expect(within(dialog).getByLabelText(/Signing secret/)).toHaveValue("");
    expect(
      within(dialog).getByText("Leave empty to generate one; it is shown once"),
    ).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Add connector" }));

    const secret = await screen.findByRole("dialog", { name: "Copy the generated secret" });
    // While the secret is on screen, the cached connector already exists without it.
    const cached = queryClient.getQueryData<Connector>(["connectors", "k9"]);
    expect(cached?.id).toBe("k9");
    expect(cached).not.toHaveProperty("generated_secrets");
    expect(calls.find((c) => c.method === "POST" && c.path === "/connectors")?.body).toEqual({
      kind: "webhook",
      name: "ci-hook",
      config: { secret: "" },
    });
    const block = within(secret).getByRole("figure", { name: "Signing secret" });
    expect(block.querySelector("pre")?.textContent).toBe("s3cr3t-generated");
    expect(within(secret).getByRole("note")).toHaveTextContent("won’t be shown again");
    expect(
      within(secret).getByRole("figure", { name: "Inbound URL" }).querySelector("pre")?.textContent,
    ).toBe("https://pl.example.test/api/v1/hooks/k9");
    await user.click(within(block).getByRole("button", { name: "Copy Signing secret" }));
    expect(await navigator.clipboard.readText()).toBe("s3cr3t-generated");
    // The connector page waits until the secret was seen.
    expect(router.state.location.pathname).toBe("/connectors");

    await user.click(within(secret).getByRole("button", { name: "I copied it" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/connectors/k9"));
    await screen.findByRole("heading", { name: "ci-hook" });
    expect(screen.queryByText("s3cr3t-generated")).toBeNull();
    expect(queryClient.getQueryData<Connector>(["connectors", "k9"])?.generated_secrets).toBe(
      undefined,
    );
  });

  it("shows 422 reasons under the fields", async () => {
    const { user, dialog } = await openAdd({
      "POST /connectors": apiError(422, "invalid", "validation failed", {
        "config.bot_token": "rejected by Telegram",
      }),
    });
    await user.click(within(dialog).getByRole("radio", { name: /Telegram/ }));
    await user.type(within(dialog).getByLabelText("Name"), "ops-bot");
    await user.type(within(dialog).getByLabelText("Bot token"), "bad");
    await user.click(within(dialog).getByRole("button", { name: "Add connector" }));
    expect(await within(dialog).findByText("rejected by Telegram")).toBeInTheDocument();
  });
});

describe("connector page (UI-13, UI-17)", () => {
  const connector = makeConnector({
    id: "k1",
    name: "ops-bot",
    status: "error",
    last_error: "token rejected",
    config: { bot_token: SecretMask, greeting: "hi", groups_only: false, allowed_chats: [] },
  });
  const chats = [
    makeChat({ id: "c1", title: "Ops on call" }),
    makeChat({ id: "c2", title: "Release notes", kind: "channel", external_id: "-1002" }),
  ];
  const base = {
    "GET /me": json(200, me),
    "GET /connector-kinds": kinds,
    "GET /connectors/k1": json(200, connector),
    "GET /connectors/k1/chats": json(200, { items: chats }),
    "GET /agents": json(200, {
      items: [
        makeAgent({ id: "a1", name: "release-bot", online: true }),
        makeAgent({ id: "a2", name: "reviewer" }),
      ],
    }),
  };

  it("shows status, last error and the chats", async () => {
    mockApi(base);
    renderRoute("/connectors/k1");
    expect(await screen.findByRole("heading", { name: "ops-bot" })).toBeInTheDocument();
    expect(screen.getByText("token rejected")).toBeInTheDocument();
    const list = await screen.findByRole("list", { name: "Chats" });
    expect(
      within(list)
        .getAllByRole("listitem")
        .map((li) => li.getAttribute("aria-label")),
    ).toEqual(["Ops on call", "Release notes"]);
  });

  it("names untitled chats after their platform (CTX-07)", async () => {
    mockApi({
      ...base,
      "GET /connectors/k1/chats": json(200, {
        items: [makeChat({ id: "c3", title: "", connector_kind: "whatsapp" })],
      }),
    });
    renderRoute("/connectors/k1");
    const list = await screen.findByRole("list", { name: "Chats" });
    const row = within(list).getByRole("listitem", { name: "WhatsApp chat" });
    expect(within(row).getByText("WhatsApp chat")).toBeInTheDocument();
    expect(within(row).getByRole("button", { name: /WhatsApp chat/ })).toBeInTheDocument();
  });

  it("refreshes the chat list on demand", async () => {
    const { calls } = mockApi({
      ...base,
      "POST /connectors/k1/chats/refresh": new Response(null, { status: 204 }),
    });
    const user = userEvent.setup();
    renderRoute("/connectors/k1");
    await user.click(await screen.findByRole("button", { name: "Refresh chats" }));
    await waitFor(() =>
      expect(
        calls.some((c) => c.method === "POST" && c.path === "/connectors/k1/chats/refresh"),
      ).toBe(true),
    );
  });

  it("routes a chat to an agent in one action and opens the new room", async () => {
    const room = makeRoom({ id: "r7", name: "Ops on call → release-bot" });
    const { calls } = mockApi({
      ...base,
      "POST /chats/c1/route": json(201, room),
      "GET /rooms/r7": json(200, room),
      "GET /rooms/r7/members": json(200, { items: [] }),
      "GET /rooms/r7/messages": json(200, { items: [], has_more: false }),
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/connectors/k1");
    await screen.findByRole("listitem", { name: "Ops on call" });
    await user.click(
      within(row("Ops on call")).getByRole("button", { name: "Route Ops on call to an agent" }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: /release-bot/ }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/r7"));
    expect(calls.find((c) => c.method === "POST" && c.path === "/chats/c1/route")?.body).toEqual({
      agent_id: "a1",
    });
  });

  it("names the AIs of the route dialog by their profile and filters by name or handle", async () => {
    mockApi({
      ...base,
      "GET /agents": json(200, {
        items: [
          makeAgent({
            id: "a1",
            intelligence: makeAI({ id: "a1", display_name: "Atlas Prime", handle: "atlas" }),
          }),
          makeAgent({
            id: "a2",
            intelligence: makeAI({ id: "a2", display_name: "Reviewer", handle: "rev" }),
          }),
        ],
      }),
    });
    const user = userEvent.setup();
    renderRoute("/connectors/k1");
    await screen.findByRole("listitem", { name: "Ops on call" });
    await user.click(
      within(row("Ops on call")).getByRole("button", { name: "Route Ops on call to an agent" }),
    );
    const dialog = await screen.findByRole("dialog");
    const agents = await within(dialog).findByRole("list", { name: "Agents" });
    const buttons = within(agents).getAllByRole("button");
    expect(buttons).toHaveLength(2);
    expect(buttons[0]).toHaveTextContent("Atlas Prime");
    expect(buttons[0]).not.toHaveTextContent("atlas");
    expect(buttons[1]).toHaveTextContent("Reviewer");

    const filter = within(dialog).getByRole("searchbox", { name: "Filter agents" });
    await user.type(filter, "REV");
    expect(within(agents).getAllByRole("button")).toHaveLength(1);
    expect(within(agents).getByRole("button", { name: /Reviewer/ })).toBeInTheDocument();
    await user.clear(filter);
    await user.type(filter, "Prime");
    expect(within(agents).getByRole("button", { name: /Atlas Prime/ })).toBeInTheDocument();
    expect(within(agents).queryByRole("button", { name: /Reviewer/ })).toBeNull();
  });

  it("edits the connector and keeps untouched secrets masked", async () => {
    const { calls } = mockApi({
      ...base,
      "PATCH /connectors/k1": json(200, { ...connector, name: "ops-bot-2" }),
    });
    const user = userEvent.setup();
    renderRoute("/connectors/k1");
    await user.click(await screen.findByRole("button", { name: "Connector settings" }));
    const sheet = await screen.findByRole("dialog");
    const name = within(sheet).getByLabelText("Name");
    await user.clear(name);
    await user.type(name, "ops-bot-2");
    await user.click(within(sheet).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PATCH")).toBe(true));
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({
      name: "ops-bot-2",
      config: {
        bot_token: SecretMask,
        api_url: "",
        greeting: "hi",
        groups_only: false,
        allowed_chats: [],
      },
    });
  });

  it("enables and disables the connector", async () => {
    const { calls } = mockApi({
      ...base,
      "PATCH /connectors/k1": json(200, { ...connector, enabled: false }),
    });
    const user = userEvent.setup();
    renderRoute("/connectors/k1");
    await user.click(await screen.findByRole("switch", { name: "Enabled" }));
    await waitFor(() =>
      expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ enabled: false }),
    );
  });

  it("deletes the connector after confirmation and returns to the list", async () => {
    const { calls } = mockApi({
      ...base,
      "DELETE /connectors/k1": new Response(null, { status: 204 }),
      "GET /connectors": json(200, { items: [] }),
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/connectors/k1");
    await user.click(await screen.findByRole("button", { name: "Connector settings" }));
    const sheet = await screen.findByRole("dialog");
    await user.click(within(sheet).getByRole("button", { name: "Delete connector" }));
    const confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Delete connector" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/connectors"));
    expect(calls.some((c) => c.method === "DELETE" && c.path === "/connectors/k1")).toBe(true);
  });

  it("hides management for a connector shared for use", async () => {
    mockApi({ ...base, "GET /connectors/k1": json(200, { ...connector, my_level: "use" }) });
    renderRoute("/connectors/k1");
    await screen.findByRole("heading", { name: "ops-bot" });
    expect(screen.queryByRole("button", { name: "Connector settings" })).toBeNull();
    expect(screen.queryByRole("button", { name: /Share/ })).toBeNull();
    expect(screen.queryByRole("switch", { name: "Enabled" })).toBeNull();
  });

  it("shows the inbound URL the server reports for a webhook connector", async () => {
    mockApi({
      ...base,
      "GET /connectors/k1": json(200, {
        ...connector,
        kind: "webhook",
        config: {},
        inbound_url: "https://pl.example.test/api/v1/hooks/k1",
      }),
    });
    renderRoute("/connectors/k1");
    const url = await screen.findByRole("figure", { name: "Inbound URL" });
    expect(url.querySelector("pre")?.textContent).toBe("https://pl.example.test/api/v1/hooks/k1");
  });

  it("shows no inbound URL for a connector without one", async () => {
    mockApi(base);
    renderRoute("/connectors/k1");
    await screen.findByRole("heading", { name: "ops-bot" });
    expect(screen.queryByRole("figure", { name: "Inbound URL" })).toBeNull();
  });

  it("shows a not-found state for a connector the user cannot see", async () => {
    mockApi({ ...base, "GET /connectors/k1": apiError(404, "not_found") });
    renderRoute("/connectors/k1");
    expect(
      await screen.findByRole("heading", { name: "This connector is not on your line" }),
    ).toBeInTheDocument();
  });
});

describe("catch-all routing (ROUTE-01, ROUTE-04)", () => {
  const connector = makeConnector({ id: "k1", name: "support-line" });
  const rooms = [
    makeRoom({ id: "r1", name: "deploys", my_level: "manage" }),
    makeRoom({ id: "r2", name: "watch-only", my_level: "view" }),
    makeRoom({ id: "r3", name: "front desk", my_level: "manage" }),
  ];
  const api = (over: Partial<Connector> = {}, patch?: Response, viewer = me) =>
    mockApi({
      "GET /me": json(200, viewer),
      "GET /connector-kinds": kinds,
      "GET /connectors/k1": json(200, { ...connector, ...over }),
      "GET /connectors/k1/chats": json(200, { items: [] }),
      "GET /rooms": json(200, { items: rooms }),
      "PATCH /connectors/k1":
        patch ??
        ((_url, init) => json(200, { ...connector, ...over, ...JSON.parse(String(init?.body)) })),
    });
  const card = () => screen.findByRole("region", { name: "Catch-all" });
  const patches = (calls: ReturnType<typeof mockApi>["calls"]) =>
    calls.filter((c) => c.method === "PATCH");

  it("shows the current scope and room with the exposure warning", async () => {
    api({ auto_route_scope: "dms", auto_route_room_id: "r1" });
    renderRoute("/connectors/k1");
    const region = await card();
    expect(within(region).getByRole("radio", { name: "Direct chats" })).toBeChecked();
    expect(await within(region).findByRole("combobox", { name: "Room" })).toHaveValue("r1");
    expect(within(region).getByRole("note")).toHaveTextContent(
      "Anyone who writes to this account reaches the agents in deploys.",
    );
  });

  it("shows no warning and no room picker while off", async () => {
    api();
    renderRoute("/connectors/k1");
    const region = await card();
    expect(within(region).getByRole("radio", { name: "Off" })).toBeChecked();
    expect(within(region).queryByRole("note")).toBeNull();
    expect(within(region).queryByRole("combobox", { name: "Room" })).toBeNull();
  });

  it("offers only rooms the user manages and saves scope and room together", async () => {
    const { calls } = api();
    const user = userEvent.setup();
    renderRoute("/connectors/k1");
    const region = await card();
    await user.click(within(region).getByRole("radio", { name: "Groups" }));
    expect(within(region).getByRole("note")).toBeInTheDocument();
    const picker = await within(region).findByRole("combobox", { name: "Room" });
    expect(
      within(picker)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["Pick a room", "deploys", "front desk"]);
    await user.selectOptions(picker, "r3");
    await user.click(within(region).getByRole("button", { name: "Save catch-all" }));
    await waitFor(() => expect(patches(calls)).toHaveLength(1));
    expect(patches(calls)[0]?.body).toEqual({
      auto_route_scope: "groups",
      auto_route_room_id: "r3",
    });
    expect(within(region).getByRole("note")).toHaveTextContent("front desk");
  });

  it("turning it off clears the room", async () => {
    const { calls } = api({ auto_route_scope: "all", auto_route_room_id: "r1" });
    const user = userEvent.setup();
    renderRoute("/connectors/k1");
    const region = await card();
    await user.click(within(region).getByRole("radio", { name: "Off" }));
    await user.click(within(region).getByRole("button", { name: "Save catch-all" }));
    await waitFor(() => expect(patches(calls)).toHaveLength(1));
    expect(patches(calls)[0]?.body).toEqual({ auto_route_scope: "off", auto_route_room_id: "" });
  });

  it("asks for a room before sending a scope other than off", async () => {
    const { calls } = api();
    const user = userEvent.setup();
    renderRoute("/connectors/k1");
    const region = await card();
    await user.click(within(region).getByRole("radio", { name: "All chats" }));
    await user.click(within(region).getByRole("button", { name: "Save catch-all" }));
    expect(await within(region).findByText("Pick the room new chats join")).toBeInTheDocument();
    expect(within(region).getByRole("combobox", { name: "Room" })).toHaveAttribute(
      "aria-invalid",
      "true",
    );
    expect(patches(calls)).toHaveLength(0);
  });

  it("explains a 403 when the user lost manage on the room", async () => {
    api({ auto_route_scope: "dms", auto_route_room_id: "r1" }, apiError(403, "forbidden"));
    const user = userEvent.setup();
    renderRoute("/connectors/k1");
    const region = await card();
    await user.selectOptions(await within(region).findByRole("combobox", { name: "Room" }), "r3");
    await user.click(within(region).getByRole("button", { name: "Save catch-all" }));
    expect(await within(region).findByRole("alert")).toHaveTextContent(
      "You need manage access on that room to route chats to it.",
    );
  });

  it("shows a 422 room reason under the picker", async () => {
    api(
      { auto_route_scope: "dms", auto_route_room_id: "r1" },
      apiError(422, "validation", "invalid", {
        auto_route_room_id: "required when the scope is not off",
      }),
    );
    const user = userEvent.setup();
    renderRoute("/connectors/k1");
    const region = await card();
    const picker = await within(region).findByRole("combobox", { name: "Room" });
    await user.selectOptions(picker, "r3");
    await user.click(within(region).getByRole("button", { name: "Save catch-all" }));
    // A11Y-01: the reason is announced and describes the picker.
    expect(await within(region).findByRole("alert")).toHaveTextContent(
      "required when the scope is not off",
    );
    expect(picker).toHaveAccessibleDescription("required when the scope is not off");
  });

  it("announces a 422 scope reason and ties it to the scope choice (A11Y-01)", async () => {
    api(
      { auto_route_scope: "dms", auto_route_room_id: "r1" },
      apiError(422, "validation", "invalid", {
        auto_route_scope: "must be off, dms, groups or all",
      }),
    );
    const user = userEvent.setup();
    renderRoute("/connectors/k1");
    const region = await card();
    await user.click(within(region).getByRole("radio", { name: "All chats" }));
    await user.click(within(region).getByRole("button", { name: "Save catch-all" }));
    expect(await within(region).findByRole("alert")).toHaveTextContent(
      "must be off, dms, groups or all",
    );
    expect(
      within(region).getByRole("radiogroup", { name: "New chats to route" }),
    ).toHaveAccessibleDescription("must be off, dms, groups or all");
  });

  it("says why Save is disabled before anything changes (A11Y-04)", async () => {
    api({ auto_route_scope: "dms", auto_route_room_id: "r1" });
    const user = userEvent.setup();
    renderRoute("/connectors/k1");
    const region = await card();
    const save = within(region).getByRole("button", { name: "Save catch-all" });
    expect(save).toBeDisabled();
    expect(save).toHaveAccessibleDescription("No changes to save.");
    expect(within(region).getByText("No changes to save.")).toBeInTheDocument();
    await user.click(within(region).getByRole("radio", { name: "All chats" }));
    expect(save).toBeEnabled();
    expect(within(region).queryByText("No changes to save.")).toBeNull();
  });

  it("names a catch-all room the user cannot pick any more", async () => {
    api({ auto_route_scope: "dms", auto_route_room_id: "r-gone" });
    renderRoute("/connectors/k1");
    const region = await card();
    const picker = await within(region).findByRole("combobox", { name: "Room" });
    expect(picker).toHaveValue("r-gone");
    expect(within(picker).getByRole("option", { selected: true })).toHaveTextContent(
      "Unavailable room",
    );
  });

  it("says when the rooms could not be loaded", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /connectors/k1": json(200, { ...connector, auto_route_scope: "dms" }),
      "GET /connectors/k1/chats": json(200, { items: [] }),
      "GET /rooms": apiError(500, "internal"),
    });
    renderRoute("/connectors/k1");
    const region = await card();
    expect(await within(region).findByText("Couldn’t load your rooms.")).toBeInTheDocument();
  });

  it("offers to switch a legacy catch-all room to per-chat conversations", async () => {
    const { calls } = api({ auto_route_scope: "dms", auto_route_room_id: "r1" });
    const user = userEvent.setup();
    renderRoute("/connectors/k1");
    const region = await card();
    expect(await within(region).findByRole("combobox", { name: "Room" })).toHaveValue("r1");
    expect(within(region).getByRole("note")).toHaveTextContent(
      "Anyone who writes to this account reaches the agents in deploys.",
    );
    await user.click(
      within(region).getByRole("button", { name: "Switch to per-chat conversations" }),
    );
    await waitFor(() => expect(patches(calls)).toHaveLength(1));
    // The scope is kept: an empty room is how per-chat conversations are chosen.
    expect(patches(calls)[0]?.body).toEqual({ auto_route_room_id: "" });
    await waitFor(() =>
      expect(
        within(region).queryByRole("button", { name: "Switch to per-chat conversations" }),
      ).toBeNull(),
    );
    expect(within(region).getByRole("note")).toHaveTextContent(
      "Each new chat becomes its own conversation with the operator.",
    );
    expect(
      within(region).getByText(
        "Leave it empty: each new chat opens its own conversation with the operator.",
      ),
    ).toBeInTheDocument();
  });

  it("has no switch action without a legacy catch-all room", async () => {
    api({ auto_route_scope: "dms" });
    renderRoute("/connectors/k1");
    const region = await card();
    expect(
      within(region).queryByRole("button", { name: "Switch to per-chat conversations" }),
    ).toBeNull();
  });

  it("is not shown for a connector shared for use", async () => {
    api({ my_level: "use" });
    renderRoute("/connectors/k1");
    await screen.findByRole("heading", { name: "support-line" });
    expect(screen.queryByRole("region", { name: "Catch-all" })).toBeNull();
  });

  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    api(
      { auto_route_scope: "dms", auto_route_room_id: "r1" },
      undefined,
      makeMe({ locale: "pt-BR" }),
    );
    renderRoute("/connectors/k1");
    const region = await screen.findByRole("region", { name: "Encaminhar tudo" });
    expect(within(region).getByRole("radio", { name: "Chats diretos" })).toBeChecked();
  });
});

describe("connector operator (ACC-01)", () => {
  const base = makeConnector({
    id: "k1",
    name: "support-line",
    operator: makeIntelligence(),
  });
  const atlas = makeAI();
  const api = (over: Partial<Connector> = {}, patch?: Response) =>
    mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /connectors/k1": json(200, { ...base, ...over }),
      "GET /connectors/k1/chats": json(200, { items: [] }),
      "GET /contacts": json(200, sampleContacts),
      "PATCH /connectors/k1":
        patch ??
        ((_url, init) => {
          const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
          const operator = body.operator_id === me.id ? me.intelligence : atlas;
          return json(200, { ...base, ...over, ...body, operator });
        }),
    });
  const patches = (calls: ReturnType<typeof mockApi>["calls"]) =>
    calls.filter((c) => c.method === "PATCH");
  const openSettings = async (user: ReturnType<typeof userEvent.setup>) => {
    await user.click(await screen.findByRole("button", { name: "Connector settings" }));
    return screen.findByRole("dialog", { name: "Connector settings" });
  };

  it("lets the owner pick one of their AIs as the operator and saves it", async () => {
    const { calls } = api();
    const user = userEvent.setup();
    renderRoute("/connectors/k1");
    const sheet = await openSettings(user);
    const picker = await within(sheet).findByRole("combobox", { name: "Operator" });
    expect(
      within(picker)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual([
      "Ana Souza (you)",
      "Atlas (@atlas)",
      "Notes (@notes)",
      "Lab (@lab)",
      "Old helper (@old-helper)",
    ]);
    expect(picker).toHaveValue(me.id);
    await user.selectOptions(picker, "a1");
    await user.click(within(sheet).getByRole("button", { name: "Save operator" }));
    await waitFor(() => expect(patches(calls)).toHaveLength(1));
    expect(patches(calls)[0]?.body).toEqual({ operator_id: "a1" });
    expect(await screen.findByText("Operator saved")).toBeInTheDocument();
  });

  it("shows the operator read-only to someone the connector is shared with", async () => {
    api({ my_level: "use", operator: atlas });
    renderRoute("/connectors/k1");
    expect(await screen.findByText("Operated by Atlas")).toBeInTheDocument();
    // No settings sheet, so no way to change it.
    expect(screen.queryByRole("button", { name: "Connector settings" })).toBeNull();
    expect(screen.queryByRole("combobox", { name: "Operator" })).toBeNull();
  });

  it("shows the server's reason under the operator pick and keeps the old one", async () => {
    api(
      {},
      apiError(422, "invalid", "invalid", {
        operator_id: "must be you or an AI you own",
      }),
    );
    const user = userEvent.setup();
    renderRoute("/connectors/k1");
    const sheet = await openSettings(user);
    const picker = await within(sheet).findByRole("combobox", { name: "Operator" });
    await user.selectOptions(picker, "a1");
    await user.click(within(sheet).getByRole("button", { name: "Save operator" }));
    expect(await within(sheet).findByRole("alert")).toHaveTextContent(
      "must be you or an AI you own",
    );
    expect(picker).toHaveAttribute("aria-invalid", "true");
    expect(picker).toHaveValue("a1");
  });

  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    mockApi({
      "GET /me": json(200, makeMe({ locale: "pt-BR" })),
      "GET /connector-kinds": kinds,
      "GET /connectors/k1": json(200, base),
      "GET /connectors/k1/chats": json(200, { items: [] }),
      "GET /contacts": json(200, sampleContacts),
    });
    const user = userEvent.setup();
    renderRoute("/connectors/k1");
    await user.click(await screen.findByRole("button", { name: "Configurações do conector" }));
    const sheet = await screen.findByRole("dialog", { name: "Configurações do conector" });
    expect(await within(sheet).findByRole("combobox", { name: "Operador" })).toBeInTheDocument();
    expect(within(sheet).getByRole("button", { name: "Salvar operador" })).toBeDisabled();
  });
});
