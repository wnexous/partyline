import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type Connector, SecretMask } from "@/lib/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeAI,
  makeChat,
  makeConnector,
  makeConnectorKind,
  makeMe,
  makeMembership,
  makeOrg,
  makeOrgChart,
  makeProfile,
} from "@/test/fixtures";
import { viewerIn } from "@/test/orgs";
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

/*
 * A connection lives at its organization's address (SET-05): the page's layout reads the
 * organization first, and its section bar marks the Connectors tab.
 */
const acmeAdmin = makeOrg({ id: "o1", name: "Acme", slug: "acme", permission: "admin" });
const acmeMember = makeOrg({ id: "o1", name: "Acme", slug: "acme", permission: "member" });
const globexOrg = makeOrg({ id: "o2", name: "Globex", slug: "globex", permission: "member" });
const adminAcme = json(200, acmeAdmin);
const memberAcme = json(200, acmeMember);

beforeEach(() => {
  useLiveStore.setState({ locale: "en", connectors: {}, currentOrgId: null });
  window.localStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

const row = (name: string) => screen.getByRole("listitem", { name });

/** The element holding exactly this text, even when part of it (a name) is a link of its own. */
const sentence = (text: string) => (_content: string, element: Element | null) =>
  element?.textContent === text &&
  Array.from(element.children).every((c) => c.textContent !== text);

describe("connectors list (UI-17)", () => {
  it("shows each connector's status and last error", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /orgs/o1": adminAcme,
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
    renderRoute("/orgs/o1/connections");
    await screen.findByRole("listitem", { name: "ops-bot" });
    expect(within(row("ops-bot")).getByText("Connected")).toBeInTheDocument();
    expect(within(row("ops-bot")).getByText("Telegram")).toBeInTheDocument();
    expect(within(row("support")).getByText("Error")).toBeInTheDocument();
    expect(within(row("support")).getByText("session logged out")).toBeInTheDocument();
    expect(within(row("ops-bot")).getByRole("link")).toHaveAttribute(
      "href",
      "/orgs/o1/connections/k1",
    );
  });

  it("updates the status live from connector.status events", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /orgs/o1": adminAcme,
      "GET /connectors": json(200, { items: [makeConnector({ id: "k1", name: "ops-bot" })] }),
    });
    const { queryClient } = renderRoute("/orgs/o1/connections");
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
      "GET /orgs/o1": adminAcme,
      "GET /connectors": json(200, { items: [] }),
    });
    renderRoute("/orgs/o1/connections");
    expect(await screen.findByRole("heading", { name: "No connectors yet" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add connector" })).toBeInTheDocument();
  });
});

describe("add connector (UI-16)", () => {
  async function openAdd(routes: Parameters<typeof mockApi>[0] = {}) {
    const api = mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /orgs/o1": adminAcme,
      "GET /connectors": json(200, { items: [] }),
      ...routes,
    });
    const user = userEvent.setup();
    const utils = renderRoute("/orgs/o1/connections");
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

    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs/o1/connections/k9"));
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
    expect(router.state.location.pathname).toBe("/orgs/o1/connections");

    await user.click(within(secret).getByRole("button", { name: "I copied it" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs/o1/connections/k9"));
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

describe("connector page (UI-17)", () => {
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
    "GET /orgs/o1": adminAcme,
    "GET /connectors/k1": json(200, connector),
    "GET /connectors/k1/chats": json(200, { items: chats }),
    "GET /contacts": json(200, sampleContacts),
  };

  it("shows status, last error and the chats", async () => {
    mockApi(base);
    renderRoute("/orgs/o1/connections/k1");
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
    renderRoute("/orgs/o1/connections/k1");
    const list = await screen.findByRole("list", { name: "Chats" });
    const row = within(list).getByRole("listitem", { name: "WhatsApp chat" });
    expect(within(row).getByText("WhatsApp chat")).toBeInTheDocument();
  });

  it("refreshes the chat list on demand", async () => {
    const { calls } = mockApi({
      ...base,
      "POST /connectors/k1/chats/refresh": new Response(null, { status: 204 }),
    });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/connections/k1");
    await user.click(await screen.findByRole("button", { name: "Refresh chats" }));
    await waitFor(() =>
      expect(
        calls.some((c) => c.method === "POST" && c.path === "/connectors/k1/chats/refresh"),
      ).toBe(true),
    );
  });

  it("edits the connector and keeps untouched secrets masked", async () => {
    const { calls } = mockApi({
      ...base,
      "PATCH /connectors/k1": json(200, { ...connector, name: "ops-bot-2" }),
    });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/connections/k1");
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
    renderRoute("/orgs/o1/connections/k1");
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
    const { router } = renderRoute("/orgs/o1/connections/k1");
    await user.click(await screen.findByRole("button", { name: "Connector settings" }));
    const sheet = await screen.findByRole("dialog");
    await user.click(within(sheet).getByRole("button", { name: "Delete connector" }));
    const confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Delete connector" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs/o1/connections"));
    expect(calls.some((c) => c.method === "DELETE" && c.path === "/connectors/k1")).toBe(true);
  });

  it("hides management from a member who does not administer the organization", async () => {
    mockApi({ ...base, "GET /connectors/k1": json(200, { ...connector, my_level: "use" }) });
    renderRoute("/orgs/o1/connections/k1");
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
    renderRoute("/orgs/o1/connections/k1");
    const url = await screen.findByRole("figure", { name: "Inbound URL" });
    expect(url.querySelector("pre")?.textContent).toBe("https://pl.example.test/api/v1/hooks/k1");
  });

  it("shows no inbound URL for a connector without one", async () => {
    mockApi(base);
    renderRoute("/orgs/o1/connections/k1");
    await screen.findByRole("heading", { name: "ops-bot" });
    expect(screen.queryByRole("figure", { name: "Inbound URL" })).toBeNull();
  });

  it("shows a not-found state for a connector the user cannot see", async () => {
    mockApi({ ...base, "GET /connectors/k1": apiError(404, "not_found") });
    renderRoute("/orgs/o1/connections/k1");
    expect(
      await screen.findByRole("heading", { name: "This connector is not on your line" }),
    ).toBeInTheDocument();
  });
});

describe("connection page as an AI's app (CID-22, CID-23, CID-27)", () => {
  const atlas = makeAI();
  const base = makeConnector({
    id: "k1",
    name: "support-line",
    operator: atlas,
    deliver: "dms",
    groups_mentions_only: false,
    queued: 4,
  });
  const api = (
    over: Partial<Connector> = {},
    patch?: Response,
    extra: Parameters<typeof mockApi>[0] = {},
  ) =>
    mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /orgs/o1": adminAcme,
      "GET /connectors/k1": json(200, { ...base, ...over }),
      "GET /connectors/k1/chats": json(200, { items: [makeChat({ id: "c1" })] }),
      "GET /contacts": json(200, sampleContacts),
      "PATCH /connectors/k1":
        patch ??
        ((_url, init) => json(200, { ...base, ...over, ...JSON.parse(String(init?.body)) })),
      ...extra,
    });
  const atlasProfile = json(200, makeProfile({ ...atlas, bio: "Answers the support line." }));
  const patches = (calls: ReturnType<typeof mockApi>["calls"]) =>
    calls.filter((c) => c.method === "PATCH");

  it("shows the operator, the delivery filters, the status and the queued count", async () => {
    api();
    renderRoute("/orgs/o1/connections/k1");
    await screen.findByRole("heading", { name: "support-line" });
    // CID-23: the header names the operator and the queue, the status is live.
    expect(screen.getByText(sentence("Operated by Atlas"))).toBeInTheDocument();
    expect(screen.getByText("4 queued")).toBeInTheDocument();
    expect(screen.getByText("Connected")).toBeInTheDocument();
    const operator = await screen.findByRole("combobox", { name: "Operator" });
    expect(operator).toHaveValue(atlas.id);
    // Only the AIs whose home is the connection's organization (ORG-07): no user, none of another organization.
    expect(
      within(operator)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["Choose an AI", "Atlas (@atlas)", "Notes (@notes)", "Sage (@sage)"]);
    expect(screen.getByRole("combobox", { name: "Deliver" })).toHaveValue("dms");
    expect(screen.getByRole("switch", { name: "Groups: only when mentioned" })).not.toBeChecked();
    expect(screen.getByRole("button", { name: "Save delivery" })).toBeDisabled();
  });

  it("shows a WhatsApp number that is not connected and clears it once it is back (WA-06)", async () => {
    const detail = "the WhatsApp number is not connected; reconnect it in evolution-go";
    api({ kind: "whatsapp" });
    const { queryClient } = renderRoute("/orgs/o1/connections/k1");
    await screen.findByRole("heading", { name: "support-line" });
    expect(screen.getByText("Connected")).toBeInTheDocument();
    expect(screen.queryByText("Last error")).toBeNull();

    // The connector reports the lost number while its API and its socket still work.
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "connector.status",
        data: { connector_id: "k1", status: "error", last_error: detail },
      }),
    );
    expect(screen.getByText("Error")).toBeInTheDocument();
    expect(screen.queryByText("Connected")).toBeNull();
    expect(screen.getByText("Last error")).toBeInTheDocument();
    expect(screen.getByText(detail)).toBeInTheDocument();

    // As soon as the number is open again the status says so and the detail is gone.
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "connector.status",
        data: { connector_id: "k1", status: "connected" },
      }),
    );
    expect(screen.getByText("Connected")).toBeInTheDocument();
    expect(screen.queryByText("Error")).toBeNull();
    expect(screen.queryByText("Last error")).toBeNull();
    expect(screen.queryByText(detail)).toBeNull();
  });

  it("asks for an operator when there is none and saves the pick (CID-02)", async () => {
    const { calls } = api({ operator: undefined, queued: 0 });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/connections/k1");
    expect(await screen.findByText("No operator yet")).toBeInTheDocument();
    expect(screen.getByRole("note")).toHaveTextContent(
      "Choose the AI that operates this connection. Until you do, nothing arrives through it.",
    );
    const picker = await screen.findByRole("combobox", { name: "Operator" });
    expect(picker).toHaveValue("");
    const save = screen.getByRole("button", { name: "Save delivery" });
    expect(save).toBeDisabled();
    await user.selectOptions(picker, atlas.id);
    await user.click(save);
    await waitFor(() => expect(patches(calls)).toHaveLength(1));
    expect(patches(calls)[0]?.body).toEqual({ operator_id: atlas.id });
    expect(await screen.findByText("Delivery settings saved")).toBeInTheDocument();
  });

  it("saves deliver and groups_mentions_only", async () => {
    const { calls } = api();
    const user = userEvent.setup();
    renderRoute("/orgs/o1/connections/k1");
    await user.selectOptions(await screen.findByRole("combobox", { name: "Deliver" }), "groups");
    await user.click(screen.getByRole("switch", { name: "Groups: only when mentioned" }));
    await user.click(screen.getByRole("button", { name: "Save delivery" }));
    await waitFor(() => expect(patches(calls)).toHaveLength(1));
    expect(patches(calls)[0]?.body).toEqual({ deliver: "groups", groups_mentions_only: true });
  });

  it("shows the server's reason under the deliver field", async () => {
    api({}, apiError(422, "invalid", "invalid", { deliver: "must be dms, groups or all" }));
    const user = userEvent.setup();
    renderRoute("/orgs/o1/connections/k1");
    const deliver = await screen.findByRole("combobox", { name: "Deliver" });
    await user.selectOptions(deliver, "groups");
    await user.click(screen.getByRole("button", { name: "Save delivery" }));
    expect(await screen.findByText("must be dms, groups or all")).toBeInTheDocument();
    expect(deliver).toHaveAttribute("aria-invalid", "true");
    expect(deliver).toHaveAccessibleDescription("must be dms, groups or all");
    expect(deliver).toHaveValue("groups");
  });

  it("shows the server's reason under the operator pick", async () => {
    api(
      { operator: undefined },
      apiError(422, "invalid", "invalid", {
        operator_id: "must be an AI of the connection's organization",
      }),
    );
    const user = userEvent.setup();
    renderRoute("/orgs/o1/connections/k1");
    const picker = await screen.findByRole("combobox", { name: "Operator" });
    await user.selectOptions(picker, atlas.id);
    await user.click(screen.getByRole("button", { name: "Save delivery" }));
    expect(
      await screen.findByText("must be an AI of the connection's organization"),
    ).toBeInTheDocument();
    expect(picker).toHaveValue(atlas.id);
  });

  it("offers no route action and no catch-all controls (CID-22)", async () => {
    const { calls } = api();
    renderRoute("/orgs/o1/connections/k1");
    await screen.findByRole("heading", { name: "support-line" });
    expect(screen.queryByRole("button", { name: /Route/ })).toBeNull();
    expect(screen.queryByRole("region", { name: "Catch-all" })).toBeNull();
    expect(screen.queryByRole("combobox", { name: "New chats to route" })).toBeNull();
    expect(screen.queryByRole("combobox", { name: "Room" })).toBeNull();
    expect(screen.queryByRole("radio", { name: "Direct chats" })).toBeNull();
    // The page shows no message text and no way to post: the connection has no timeline.
    expect(screen.queryByRole("log")).toBeNull();
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(calls.some((c) => c.path === "/chats/c1/route")).toBe(false);
  });

  it("opens the operator's profile panel from the header, without leaving the page (PRF-01)", async () => {
    api({}, undefined, { "GET /intelligences/a1": atlasProfile });
    const user = userEvent.setup();
    const { router } = renderRoute("/orgs/o1/connections/k1");
    const header = (await screen.findByRole("heading", { name: "support-line" })).closest(
      "header",
    ) as HTMLElement;

    await user.click(within(header).getByRole("link", { name: "Atlas" }));
    const panel = await screen.findByRole("dialog", { name: "Atlas" });
    expect(within(panel).getByText("Answers the support line.")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/orgs/o1/connections/k1");
  });

  it("opens it from the read-only summary of a member of the organization too", async () => {
    api({ my_level: "use" }, undefined, { "GET /intelligences/a1": atlasProfile });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/connections/k1");
    const summary = (await screen.findByText("Operator", { selector: "dt" }))
      .parentElement as HTMLElement;

    await user.click(within(summary).getByRole("link", { name: "Atlas" }));
    expect(await screen.findByRole("dialog", { name: "Atlas" })).toBeInTheDocument();
  });

  it("reads the delivery settings without controls as a member of the organization", async () => {
    api({ my_level: "use", operator: atlas, deliver: "all", groups_mentions_only: true });
    renderRoute("/orgs/o1/connections/k1");
    expect(await screen.findByText(sentence("Operated by Atlas"))).toBeInTheDocument();
    expect(screen.getByText(sentence("Atlas (@atlas)"))).toBeInTheDocument();
    expect(screen.getByText("Direct chats and groups")).toBeInTheDocument();
    expect(screen.getByText("Yes")).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Operator" })).toBeNull();
    expect(screen.queryByRole("combobox", { name: "Deliver" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Save delivery" })).toBeNull();
  });

  it("says who chooses an operator that does not exist yet to a member who is not an admin", async () => {
    api({ my_level: "use", operator: undefined });
    renderRoute("/orgs/o1/connections/k1");
    expect(await screen.findByRole("note")).toHaveTextContent(
      "No AI operates this account yet, so nothing arrives through it. An admin of the organization chooses the operator.",
    );
    expect(screen.getAllByText("No operator yet").length).toBeGreaterThan(0);
  });

  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    mockApi({
      "GET /me": json(200, makeMe({ locale: "pt-BR" })),
      "GET /connector-kinds": kinds,
      "GET /orgs/o1": adminAcme,
      "GET /connectors/k1": json(200, base),
      "GET /connectors/k1/chats": json(200, { items: [] }),
      "GET /contacts": json(200, sampleContacts),
    });
    renderRoute("/orgs/o1/connections/k1");
    expect(await screen.findByText(sentence("Operado por Atlas"))).toBeInTheDocument();
    expect(screen.getByText("4 na fila")).toBeInTheDocument();
    expect(await screen.findByRole("combobox", { name: "Operador" })).toHaveValue(atlas.id);
    expect(screen.getByRole("combobox", { name: "Entregar" })).toHaveValue("dms");
    expect(
      screen.getByRole("switch", { name: "Grupos: só quando mencionarem" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Salvar entrega" })).toBeDisabled();
  });
});

/*
 * Connections belong to an organization (SET-05, SET-06, SET-07, ORG-02, ORG-03, ORG-07, OMEM-04):
 * a connection lives at its organization's address, the address names the organization the list
 * filters on, and the old /connectors addresses resolve to the organization that owns them.
 */
const acme = makeMembership({
  org_id: "o1",
  org_name: "Acme",
  org_slug: "acme",
  permission: "admin",
});
const globex = makeMembership({
  org_id: "o2",
  org_name: "Globex",
  org_slug: "globex",
  permission: "member",
});
const initech = makeMembership({
  org_id: "o3",
  org_name: "Initech",
  org_slug: "initech",
  permission: "admin",
});
const memberOf = (...memberships: ReturnType<typeof makeMembership>[]) => viewerIn(...memberships);
const optionsOf = (select: HTMLElement) =>
  within(select)
    .getAllByRole("option")
    .map((o) => o.textContent);

describe("the connections of the organization in the address (SET-05)", () => {
  const all = [
    makeConnector({ id: "k1", name: "ops-bot", org_id: "o1" }),
    makeConnector({ id: "k2", name: "support", org_id: "o2" }),
    makeConnector({ id: "k3", name: "billing", org_id: "o1" }),
  ];
  const api = (items = all, ...memberships: ReturnType<typeof makeMembership>[]) =>
    mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /connectors": json(200, { items }),
      "GET /orgs/o1": adminAcme,
      "GET /orgs/o2": json(200, globexOrg),
      ...memberOf(...memberships),
    });
  const listed = (org = "Acme") =>
    within(screen.getByRole("list", { name: `Connectors of ${org}` }))
      .getAllByRole("listitem")
      .map((li) => li.getAttribute("aria-label"));

  it("lists only the connections of the organization in the address", async () => {
    api(all, acme, globex);
    renderRoute("/orgs/o1/connections");
    expect(
      await screen.findByRole("heading", { level: 1, name: "Connectors" }),
    ).toBeInTheDocument();
    await screen.findByRole("list", { name: "Connectors of Acme" });
    expect(listed()).toEqual(["billing", "ops-bot"]);
    expect(screen.queryByRole("listitem", { name: "support" })).toBeNull();
    expect(within(row("ops-bot")).getByRole("link")).toHaveAttribute(
      "href",
      "/orgs/o1/connections/k1",
    );
    expect(screen.getByText(/accounts of this organization/)).toBeInTheDocument();
  });

  it("marks the Connectors tab of the organization", async () => {
    api(all, acme, globex);
    renderRoute("/orgs/o1/connections");
    const bar = await screen.findByRole("navigation", { name: "Sections of Acme" });
    expect(within(bar).getByRole("link", { name: "Connectors" })).toHaveAttribute(
      "href",
      "/orgs/o1/connections",
    );
    expect(within(bar).getByRole("link", { name: "Connectors" })).toHaveAttribute(
      "aria-current",
      "page",
    );
  });

  it("marks the Connectors tab from the org chart too", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /orgs/o1": adminAcme,
      "GET /orgs/o1/chart": json(200, makeOrgChart()),
      ...memberOf(acme, globex),
    });
    renderRoute("/orgs/o1/chart");
    const bar = await screen.findByRole("navigation", { name: "Sections of Acme" });
    expect(within(bar).getByRole("link", { name: "Connectors" })).toHaveAttribute(
      "href",
      "/orgs/o1/connections",
    );
    expect(within(bar).getByRole("link", { name: "Connectors" })).not.toHaveAttribute(
      "aria-current",
    );
  });

  it("offers Add to an admin and not to a member", async () => {
    const created = makeConnector({ id: "k9", name: "ops-bot", org_id: "o1" });
    const admin = mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /connectors": json(200, { items: [] }),
      "GET /orgs/o1": adminAcme,
      "POST /connectors": json(201, created),
      "GET /connectors/k9": json(200, created),
      "GET /connectors/k9/chats": json(200, { items: [] }),
      ...memberOf(acme),
    });
    const user = userEvent.setup();
    const first = renderRoute("/orgs/o1/connections");
    await user.click(await screen.findByRole("button", { name: "Add connector" }));
    const dialog = await screen.findByRole("dialog", { name: "Add connector" });
    await user.click(within(dialog).getByRole("radio", { name: /Telegram/ }));
    await user.type(within(dialog).getByLabelText("Name"), "ops-bot");
    await user.type(within(dialog).getByLabelText("Bot token"), "123:abc");
    await user.click(within(dialog).getByRole("button", { name: "Add connector" }));
    await waitFor(() =>
      expect(admin.calls.some((c) => c.method === "POST" && c.path === "/connectors")).toBe(true),
    );
    await waitFor(() =>
      expect(first.router.state.location.pathname).toBe("/orgs/o1/connections/k9"),
    );
    first.unmount();

    // A member of the organization reads its connections and is offered no way to add one.
    mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /connectors": json(200, { items: all }),
      "GET /orgs/o1": memberAcme,
      ...memberOf(globex),
    });
    renderRoute("/orgs/o1/connections");
    await screen.findByRole("listitem", { name: "ops-bot" });
    expect(screen.queryByRole("button", { name: "Add connector" })).toBeNull();
  });

  it("shows the organization not found to a non-member", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /orgs/o2": apiError(404, "not_found"),
      ...memberOf(acme, globex),
    });
    renderRoute("/orgs/o2/connections");
    expect(await screen.findByText("This organization is not on your line")).toBeInTheDocument();
  });

  it("says the organization has none when only another organization has connections", async () => {
    api([all[1] as Connector], acme, globex);
    renderRoute("/orgs/o1/connections");
    expect(await screen.findByRole("heading", { name: "No connectors yet" })).toBeInTheDocument();
    expect(screen.queryByRole("listitem", { name: "support" })).toBeNull();
    // An admin of Acme is the one who can add its first connection.
    expect(screen.getByRole("button", { name: "Add connector" })).toBeInTheDocument();
  });

  it("names no owner: the organization is the owner (AD-019)", async () => {
    api(
      [
        makeConnector({
          id: "k1",
          name: "ops-bot",
          org_id: "o1",
          owner: { id: "u9", username: "bob", display_name: "Bob Lima" },
        }),
      ],
      acme,
    );
    renderRoute("/orgs/o1/connections");
    await screen.findByRole("listitem", { name: "ops-bot" });
    expect(screen.queryByText(/by @bob/)).toBeNull();
  });

  it("says its admins add the first connection to a member", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /connectors": json(200, { items: [] }),
      "GET /orgs/o1": memberAcme,
      ...memberOf(globex),
    });
    renderRoute("/orgs/o1/connections");
    expect(await screen.findByRole("heading", { name: "No connectors yet" })).toBeInTheDocument();
    expect(
      screen.getByText("Nothing is connected in Acme yet. Its admins add connections."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add connector" })).toBeNull();
  });

  it("offers no Add on an organization the viewer only belongs to, though they administer another", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /connectors": json(200, { items: all }),
      "GET /orgs/o1": adminAcme,
      "GET /orgs/o2": json(200, globexOrg),
      ...memberOf(acme, globex),
    });
    renderRoute("/orgs/o2/connections");
    await screen.findByRole("listitem", { name: "support" });
    expect(screen.queryByRole("button", { name: "Add connector" })).toBeNull();
  });
});

describe("the old /connectors addresses resolve (SET-06, SET-07)", () => {
  const all = [
    makeConnector({ id: "k1", name: "ops-bot", org_id: "o1" }),
    makeConnector({ id: "k2", name: "support", org_id: "o2" }),
  ];

  it("sends /connectors to the organization the switcher shows", async () => {
    mockApi({
      "GET /me": json(200, makeMe({ default_org_id: "o1" })),
      "GET /connector-kinds": kinds,
      "GET /connectors": json(200, { items: all }),
      "GET /orgs/o1": adminAcme,
      ...memberOf(globex, acme),
    });
    const { router } = renderRoute("/connectors");
    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs/o1/connections"));
    expect(await screen.findByRole("list", { name: "Connectors of Acme" })).toBeInTheDocument();
  });

  it("falls back to the first organization", async () => {
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /connector-kinds": kinds,
      "GET /connectors": json(200, { items: all }),
      "GET /orgs/o2": json(200, globexOrg),
      ...memberOf(globex),
    });
    const { router } = renderRoute("/connectors");
    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs/o2/connections"));
  });

  it("sends a viewer with no organization to the list", async () => {
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /orgs": json(200, { items: [] }),
      "GET /invitations": json(200, { items: [] }),
    });
    const { router } = renderRoute("/connectors");
    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs"));
  });

  it("sends a bookmarked connection to its own organization", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /connectors/k1": json(200, all[0]),
      "GET /connectors/k1/chats": json(200, { items: [] }),
      "GET /contacts": json(200, sampleContacts),
      "GET /orgs/o1": adminAcme,
      ...memberOf(acme),
    });
    const { router } = renderRoute("/connectors/k1");
    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs/o1/connections/k1"));
    expect(await screen.findByRole("heading", { name: "ops-bot" })).toBeInTheDocument();
  });

  it("sends a connection opened under another organization to its own", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /connectors/k1": json(200, all[0]),
      "GET /connectors/k1/chats": json(200, { items: [] }),
      "GET /contacts": json(200, sampleContacts),
      "GET /orgs/o1": adminAcme,
      "GET /orgs/o2": json(200, globexOrg),
      ...memberOf(acme, globex),
    });
    const { router } = renderRoute("/orgs/o2/connections/k1");
    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs/o1/connections/k1"));
    expect(await screen.findByRole("heading", { name: "ops-bot" })).toBeInTheDocument();
  });

  it("sends an unreadable connection to the organizations", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /connectors/k9": apiError(404, "not_found"),
      "GET /invitations": json(200, { items: [] }),
      ...memberOf(acme),
    });
    const { router } = renderRoute("/connectors/k9");
    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs"));
  });
});

describe("adding a connection to an organization (ORG-03)", () => {
  async function openAdd(
    memberships: ReturnType<typeof makeMembership>[],
    routes: Parameters<typeof mockApi>[0] = {},
  ) {
    const api = mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /connectors": json(200, { items: [] }),
      "GET /orgs/o1": adminAcme,
      ...memberOf(...memberships),
      ...routes,
    });
    const user = userEvent.setup();
    const utils = renderRoute("/orgs/o1/connections");
    await user.click(await screen.findByRole("button", { name: "Add connector" }));
    const dialog = await screen.findByRole("dialog", { name: "Add connector" });
    return { ...api, ...utils, user, dialog };
  }
  const orgField = (dialog: HTMLElement) =>
    within(dialog).findByRole("combobox", { name: "Organization" });

  it("asks for the organization among those the viewer administers, never one they only belong to", async () => {
    const { dialog } = await openAdd([acme, globex, initech]);
    const field = await orgField(dialog);
    expect(optionsOf(field)).toEqual(["Acme", "Initech"]);
    expect(field).toHaveValue("o1");
  });

  it("starts on the current organization when the viewer administers it", async () => {
    useLiveStore.setState({ currentOrgId: "o3" });
    const { dialog } = await openAdd([acme, globex, initech]);
    expect(await orgField(dialog)).toHaveValue("o3");
  });

  it("starts on the first organization the viewer administers when they only belong to the current one", async () => {
    const { dialog } = await openAdd([
      makeMembership({ org_id: "o0", org_name: "Aardvark", org_slug: "aardvark" }),
      initech,
    ]);
    expect(await orgField(dialog)).toHaveValue("o3");
  });

  it("creates the connection in the chosen organization", async () => {
    const created = makeConnector({ id: "k9", name: "ops-bot", org_id: "o3" });
    const { user, dialog, calls, router } = await openAdd([acme, globex, initech], {
      "POST /connectors": json(201, created),
      "GET /orgs/o3": json(
        200,
        makeOrg({ id: "o3", name: "Initech", slug: "initech", permission: "admin" }),
      ),
      "GET /connectors/k9": json(200, created),
      "GET /connectors/k9/chats": json(200, { items: [] }),
    });
    await user.selectOptions(await orgField(dialog), "o3");
    await user.click(within(dialog).getByRole("radio", { name: /Telegram/ }));
    await user.type(within(dialog).getByLabelText("Name"), "ops-bot");
    await user.type(within(dialog).getByLabelText("Bot token"), "123:abc");
    await user.click(within(dialog).getByRole("button", { name: "Add connector" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs/o3/connections/k9"));
    expect(calls.find((c) => c.method === "POST" && c.path === "/connectors")?.body).toMatchObject({
      org_id: "o3",
      kind: "telegram",
      name: "ops-bot",
    });
  });

  it("opens a connection created into another organization under that organization", async () => {
    const created = makeConnector({ id: "k9", name: "ops-bot", org_id: "o2" });
    const { user, dialog, router } = await openAdd([acme, globex], {
      "POST /connectors": json(201, created),
      "GET /orgs/o2": json(200, globexOrg),
      "GET /connectors/k9": json(200, created),
      "GET /connectors/k9/chats": json(200, { items: [] }),
    });
    await user.click(within(dialog).getByRole("radio", { name: /Telegram/ }));
    await user.type(within(dialog).getByLabelText("Name"), "ops-bot");
    await user.type(within(dialog).getByLabelText("Bot token"), "123:abc");
    await user.click(within(dialog).getByRole("button", { name: "Add connector" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs/o2/connections/k9"));
  });

  it("names the organization when it is the only one the viewer can add to", async () => {
    const { dialog } = await openAdd([acme, globex]);
    const field = await orgField(dialog);
    expect(optionsOf(field)).toEqual(["Acme"]);
  });

  it("tells a viewer the server turns down that only admins add connections", async () => {
    const { user, dialog } = await openAdd([acme], {
      "POST /connectors": apiError(403, "forbidden", "forbidden"),
    });
    await orgField(dialog);
    await user.click(within(dialog).getByRole("radio", { name: /Telegram/ }));
    await user.type(within(dialog).getByLabelText("Name"), "ops-bot");
    await user.type(within(dialog).getByLabelText("Bot token"), "123:abc");
    await user.click(within(dialog).getByRole("button", { name: "Add connector" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Only admins of the organization can add connections.",
    );
  });
});

describe("the page of a connection in an organization (ORG-03, ORG-07, OMEM-04)", () => {
  const atlas = makeAI();
  const base = makeConnector({
    id: "k1",
    name: "support-line",
    org_id: "o1",
    operator: atlas,
    deliver: "dms",
    groups_mentions_only: false,
    queued: 4,
  });
  const api = (over: Partial<Connector> = {}, routes: Parameters<typeof mockApi>[0] = {}) =>
    mockApi({
      "GET /me": json(200, me),
      "GET /connector-kinds": kinds,
      "GET /orgs/o1": adminAcme,
      "GET /orgs/o2": json(200, globexOrg),
      "GET /connectors/k1": json(200, { ...base, ...over }),
      "GET /connectors/k1/chats": json(200, { items: [makeChat({ id: "c1" })] }),
      "GET /contacts": json(200, sampleContacts),
      ...memberOf(acme, globex),
      ...routes,
    });

  it("names the organization that owns the connection", async () => {
    api({ org_id: "o2", my_level: "use" });
    renderRoute("/orgs/o2/connections/k1");
    expect(await screen.findByText("Organization: Globex")).toBeInTheDocument();
  });

  it("names no owner and offers no way to share it one by one, even to an admin", async () => {
    api({ owner: { id: "u9", username: "bob", display_name: "Bob Lima" } });
    renderRoute("/orgs/o1/connections/k1");
    await screen.findByText("Organization: Acme");
    expect(screen.queryByText(/by @bob/)).toBeNull();
    expect(screen.queryByRole("button", { name: /Share/ })).toBeNull();
    expect(screen.getByRole("button", { name: "Connector settings" })).toBeInTheDocument();
  });

  it("lets a member who is not an admin read it all and change nothing", async () => {
    api({ org_id: "o2", my_level: "use" });
    renderRoute("/orgs/o2/connections/k1");
    expect(await screen.findByText(sentence("Operated by Atlas"))).toBeInTheDocument();
    // Read-only: the operator and the delivery settings are text.
    expect(screen.getByText(sentence("Atlas (@atlas)"))).toBeInTheDocument();
    expect(screen.getByText("Direct chats only")).toBeInTheDocument();
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.queryByRole("button", { name: "Save delivery" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Connector settings" })).toBeNull();
    // How many messages wait is shown; what they say never is (CID-23).
    expect(screen.getByText("4 queued")).toBeInTheDocument();
    expect(screen.queryByRole("log")).toBeNull();
    expect(screen.queryByRole("textbox")).toBeNull();
  });

  it("lets an admin change the operator to another AI of the organization, and only those", async () => {
    const { calls } = api(
      {},
      {
        "PATCH /connectors/k1": (_url, init) =>
          json(200, { ...base, ...JSON.parse(String(init?.body)) }),
      },
    );
    const user = userEvent.setup();
    renderRoute("/orgs/o1/connections/k1");
    const picker = await screen.findByRole("combobox", { name: "Operator" });
    // Acme's AIs: not Lab, which belongs to Globex, and no user.
    expect(optionsOf(picker)).toEqual([
      "Choose an AI",
      "Atlas (@atlas)",
      "Notes (@notes)",
      "Sage (@sage)",
    ]);
    await user.selectOptions(picker, "a2");
    await user.click(screen.getByRole("button", { name: "Save delivery" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "PATCH")).toHaveLength(1));
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ operator_id: "a2" });
  });

  it("says only admins change the operator when the server refuses a member", async () => {
    api({}, { "PATCH /connectors/k1": apiError(403, "forbidden", "forbidden") });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/connections/k1");
    await user.selectOptions(await screen.findByRole("combobox", { name: "Operator" }), "a2");
    await user.click(screen.getByRole("button", { name: "Save delivery" }));
    const refusal = await screen.findByText(
      "Only admins of the connection’s organization can change its operator.",
    );
    expect(refusal).toHaveAttribute("role", "alert");
  });
});
