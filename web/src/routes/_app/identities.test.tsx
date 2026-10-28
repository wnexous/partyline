import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import type { Identity } from "@/shared/api/types.gen";
import { json, mockApi } from "@/test/fetch";
import { makeConnector, makeMe } from "@/test/fixtures";
import { memberIn, viewerIn } from "@/test/orgs";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

const me = makeMe();
const BASE = Date.parse("2026-10-19T12:00:00Z");

const telegramId: Identity = {
  id: "i1",
  connector_id: "c1",
  connector_kind: "telegram",
  external_id: "tg:5550001",
  display: "Ana (phone)",
  created_at: "2026-10-10T12:00:00Z",
};
const whatsappId: Identity = {
  id: "i2",
  connector_id: "c2",
  connector_kind: "whatsapp",
  external_id: "wa:5550002",
  alt_external_id: "lid:77",
  display: "Ana WhatsApp",
  created_at: "2026-10-11T12:00:00Z",
};

beforeEach(() => {
  useLiveStore.setState({ locale: "en", lastLinkedIdentity: null });
  vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
  vi.setSystemTime(BASE);
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const whatsapp = makeConnector({ id: "k1", kind: "whatsapp", name: "family-wa" });
const telegram = makeConnector({ id: "k2", kind: "telegram", name: "ops-bot" });
// A connection of another organization of the viewer's (Globex), where they are a member and not an admin.
const shared = makeConnector({
  id: "k3",
  kind: "discord",
  name: "team-discord",
  org_id: "o2",
  my_level: "use",
  owner: { id: "u9", username: "bia", display_name: "Bia Lima" },
});

const pairing = (code: string, seconds: number, connectorId = "k1") =>
  json(201, {
    code,
    connector_id: connectorId,
    expires_at: new Date(Date.now() + seconds * 1000).toISOString(),
  });

describe("identities page (UI-18)", () => {
  it("says that the AIs you manage are the ones that trust a linked account (AD-018)", async () => {
    mockApi({ "GET /me": json(200, me), "GET /identities": json(200, { items: [] }) });
    renderRoute("/identities");
    expect(
      await screen.findByText(
        "Link the WhatsApp, Telegram or Discord accounts you use. The AIs you manage then receive your messages from them as trusted.",
      ),
    ).toBeInTheDocument();
    expect(document.body).not.toHaveTextContent("Your agents");
  });

  it("lists verified chat accounts", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /identities": json(200, { items: [telegramId, whatsappId] }),
    });
    renderRoute("/identities");
    const tg = await screen.findByRole("listitem", { name: "Ana (phone)" });
    expect(within(tg).getByText("Telegram")).toBeInTheDocument();
    expect(within(tg).getByText("tg:5550001")).toBeInTheDocument();
    expect(screen.getByRole("listitem", { name: "Ana WhatsApp" })).toBeInTheDocument();
  });

  it("deletes an identity after confirmation", async () => {
    let items = [telegramId];
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /identities": () => json(200, { items }),
      "DELETE /identities/i1": () => {
        items = [];
        return new Response(null, { status: 204 });
      },
    });
    const user = userEvent.setup();
    renderRoute("/identities");
    await user.click(await screen.findByRole("button", { name: "Remove Ana (phone)" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent(
      "Messages from this account reach the AIs you manage as untrusted again. You can link it again with a new code.",
    );
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    await user.click(within(confirm).getByRole("button", { name: "Remove identity" }));
    await waitFor(() => expect(screen.queryByRole("listitem", { name: "Ana (phone)" })).toBeNull());
    expect(calls.some((c) => c.method === "DELETE" && c.path === "/identities/i1")).toBe(true);
  });

  it("with one usable connector, requests the code for it and shows the code, the command and a countdown", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /identities": json(200, { items: [] }),
      "GET /connectors": json(200, { items: [whatsapp] }),
      "POST /identities/pairing": () => pairing("K7P2QX", 600),
    });
    const user = userEvent.setup();
    renderRoute("/identities");
    await user.click(await screen.findByRole("button", { name: "Link a chat account" }));
    const dialog = await screen.findByRole("dialog", { name: "Link a chat account" });
    expect(await within(dialog).findByText("K7P2QX")).toBeInTheDocument();
    const posts = calls.filter((c) => c.method === "POST");
    expect(posts).toHaveLength(1);
    expect(posts[0]?.body).toEqual({ connector_id: "k1" });
    expect(within(dialog).getByText("Through family-wa (WhatsApp)")).toBeInTheDocument();
    expect(within(dialog).queryByRole("radiogroup")).toBeNull();
    expect(within(dialog).queryByRole("button", { name: "Use another connector" })).toBeNull();
    const command = within(dialog).getByRole("figure", { name: "Command" });
    expect(command.querySelector("pre")?.textContent).toBe("!link K7P2QX");
    expect(within(dialog).getByText(/\/link K7P2QX/)).toBeInTheDocument();
    expect(within(dialog).getByRole("timer")).toHaveTextContent("10:00");
    act(() => vi.advanceTimersByTime(61_000));
    expect(within(dialog).getByRole("timer")).toHaveTextContent("8:59");
  });

  it("turns to an expired state when the countdown reaches 0, and offers a new code", async () => {
    let n = 0;
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /identities": json(200, { items: [] }),
      "GET /connectors": json(200, { items: [whatsapp] }),
      "POST /identities/pairing": () => {
        n += 1;
        return pairing(n === 1 ? "AAAAAA" : "BBBBBB", 5);
      },
    });
    const user = userEvent.setup();
    renderRoute("/identities");
    await user.click(await screen.findByRole("button", { name: "Link a chat account" }));
    const dialog = await screen.findByRole("dialog", { name: "Link a chat account" });
    await within(dialog).findByText("AAAAAA");
    act(() => vi.advanceTimersByTime(4_000));
    expect(within(dialog).getByRole("timer")).toHaveTextContent("0:01");
    act(() => vi.advanceTimersByTime(1_000));
    expect(within(dialog).getByText("This code expired")).toBeInTheDocument();
    expect(within(dialog).queryByText("AAAAAA")).toBeNull();
    await user.click(within(dialog).getByRole("button", { name: "Get a new code" }));
    expect(await within(dialog).findByText("BBBBBB")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(2);
  });

  it("shows success and refetches the list when identity.linked arrives", async () => {
    let items: Identity[] = [];
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /identities": () => json(200, { items }),
      "GET /connectors": json(200, { items: [whatsapp] }),
      "POST /identities/pairing": () => pairing("K7P2QX", 600),
    });
    const user = userEvent.setup();
    const { queryClient } = renderRoute("/identities");
    await user.click(await screen.findByRole("button", { name: "Link a chat account" }));
    const dialog = await screen.findByRole("dialog", { name: "Link a chat account" });
    await within(dialog).findByText("K7P2QX");
    const listCalls = () => calls.filter((c) => c.method === "GET" && c.path === "/identities");
    const before = listCalls().length;

    items = [telegramId];
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "identity.linked",
        data: telegramId,
      }),
    );
    expect(await within(dialog).findByText(/Ana \(phone\) is linked/)).toBeInTheDocument();
    expect(
      within(dialog).getByText(
        "The AIs you manage now trust your messages from this Telegram account.",
      ),
    ).toBeInTheDocument();
    expect(within(dialog).queryByRole("timer")).toBeNull();
    await waitFor(() => expect(listCalls().length).toBeGreaterThan(before));
    await user.click(within(dialog).getByRole("button", { name: "Done" }));
    expect(await screen.findByRole("listitem", { name: "Ana (phone)" })).toBeInTheDocument();
  });

  it("ignores an identity linked before the dialog opened", async () => {
    useLiveStore.setState({ lastLinkedIdentity: whatsappId });
    mockApi({
      "GET /me": json(200, me),
      "GET /identities": json(200, { items: [whatsappId] }),
      "GET /connectors": json(200, { items: [whatsapp] }),
      "POST /identities/pairing": () => pairing("K7P2QX", 600),
    });
    const user = userEvent.setup();
    renderRoute("/identities");
    await user.click(await screen.findByRole("button", { name: "Link a chat account" }));
    const dialog = await screen.findByRole("dialog", { name: "Link a chat account" });
    expect(await within(dialog).findByText("K7P2QX")).toBeInTheDocument();
    expect(within(dialog).queryByText(/is linked/)).toBeNull();
  });

  it("asks which connector first when several can be used, then pairs through the chosen one", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      ...viewerIn(memberIn("o1", "admin"), memberIn("o2", "member")),
      "GET /identities": json(200, { items: [] }),
      "GET /connectors": json(200, { items: [whatsapp, telegram, shared] }),
      "POST /identities/pairing": () => pairing("T3LGRM", 600, "k2"),
    });
    const user = userEvent.setup();
    renderRoute("/identities");
    await user.click(await screen.findByRole("button", { name: "Link a chat account" }));
    const dialog = await screen.findByRole("dialog", { name: "Link a chat account" });
    const group = await within(dialog).findByRole("radiogroup");
    expect(within(group).getAllByRole("radio")).toHaveLength(3);
    // Each connection says which organization it belongs to, for the admin of one and the member of another.
    expect(await within(dialog).findByText("Discord · Globex")).toBeInTheDocument();
    expect(within(dialog).getByText("WhatsApp · Acme")).toBeInTheDocument();
    expect(within(dialog).getByText("Telegram · Acme")).toBeInTheDocument();
    // Whoever created the connection gives nothing: the organization owns it (AD-019).
    expect(dialog).not.toHaveTextContent("Bia Lima");
    expect(dialog).not.toHaveTextContent("shared by");
    const getCode = within(dialog).getByRole("button", { name: "Get a code" });
    expect(getCode).toBeDisabled();
    expect(calls.some((c) => c.method === "POST")).toBe(false);

    await user.click(within(dialog).getByRole("radio", { name: /ops-bot/ }));
    await user.click(getCode);
    expect(await within(dialog).findByText("T3LGRM")).toBeInTheDocument();
    const posts = calls.filter((c) => c.method === "POST");
    expect(posts).toHaveLength(1);
    expect(posts[0]?.body).toEqual({ connector_id: "k2" });
    expect(within(dialog).getByText("Through ops-bot (Telegram)")).toBeInTheDocument();
    const command = within(dialog).getByRole("figure", { name: "Command" });
    expect(command.querySelector("pre")?.textContent).toBe("/link T3LGRM");
    expect(within(dialog).getByText(/!link T3LGRM works too/)).toBeInTheDocument();

    await user.click(within(dialog).getByRole("button", { name: "Use another connector" }));
    expect(await within(dialog).findByRole("radiogroup")).toBeInTheDocument();
  });

  it("names only the platform while the organizations cannot be read", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /orgs": json(500, { error: { code: "internal", message: "boom" } }),
      "GET /identities": json(200, { items: [] }),
      "GET /connectors": json(200, { items: [whatsapp, shared] }),
    });
    const user = userEvent.setup();
    renderRoute("/identities");
    await user.click(await screen.findByRole("button", { name: "Link a chat account" }));
    const dialog = await screen.findByRole("dialog", { name: "Link a chat account" });
    await within(dialog).findByRole("radiogroup");
    expect(within(dialog).getByText("WhatsApp")).toBeInTheDocument();
    expect(within(dialog).getByText("Discord")).toBeInTheDocument();
    expect(dialog).not.toHaveTextContent("·");
    expect(dialog).not.toHaveTextContent("Bia Lima");
  });

  it("explains that a connector is needed when the user can use none", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /identities": json(200, { items: [] }),
      "GET /connectors": json(200, { items: [] }),
    });
    const user = userEvent.setup();
    renderRoute("/identities");
    await user.click(await screen.findByRole("button", { name: "Link a chat account" }));
    const dialog = await screen.findByRole("dialog", { name: "Link a chat account" });
    expect(await within(dialog).findByText("No connector to link through")).toBeInTheDocument();
    // A connection belongs to an organization: it is added in one the user administers, or its admins add it.
    expect(
      within(dialog).getByText(
        "Connectors belong to organizations: add one in an organization you administer, or ask its admins to, then come back.",
      ),
    ).toBeInTheDocument();
    expect(dialog).not.toHaveTextContent("share");
    expect(within(dialog).getByRole("link", { name: "Go to connectors" })).toHaveAttribute(
      "href",
      "/connectors",
    );
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });
});
