import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type Connector, SecretMask } from "@/lib/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeAgent,
  makeChat,
  makeConnector,
  makeConnectorKind,
  makeMe,
  makeRoom,
} from "@/test/fixtures";
import { renderRoute } from "@/test/render";

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
