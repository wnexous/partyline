import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Identity } from "@/lib/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeMe } from "@/test/fixtures";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

const me = makeMe();
const BASE = Date.parse("2026-10-19T12:00:00Z");

const telegramId: Identity = {
  id: "i1",
  connector_kind: "telegram",
  external_id: "tg:5550001",
  display: "Ana (phone)",
  created_at: "2026-10-10T12:00:00Z",
};
const whatsappId: Identity = {
  id: "i2",
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

const pairing = (code: string, seconds: number) =>
  json(201, { code, expires_at: new Date(Date.now() + seconds * 1000).toISOString() });

describe("identities page (UI-18)", () => {
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
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    await user.click(within(confirm).getByRole("button", { name: "Remove identity" }));
    await waitFor(() => expect(screen.queryByRole("listitem", { name: "Ana (phone)" })).toBeNull());
    expect(calls.some((c) => c.method === "DELETE" && c.path === "/identities/i1")).toBe(true);
  });

  it("shows the pairing code, the command to send and a countdown", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /identities": json(200, { items: [] }),
      "POST /identities/pairing": () => pairing("K7P2QX", 600),
    });
    const user = userEvent.setup();
    renderRoute("/identities");
    await user.click(await screen.findByRole("button", { name: "Link a chat account" }));
    const dialog = await screen.findByRole("dialog", { name: "Link a chat account" });
    expect(await within(dialog).findByText("K7P2QX")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
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
      "POST /identities/pairing": () => pairing("K7P2QX", 600),
    });
    const user = userEvent.setup();
    renderRoute("/identities");
    await user.click(await screen.findByRole("button", { name: "Link a chat account" }));
    const dialog = await screen.findByRole("dialog", { name: "Link a chat account" });
    expect(await within(dialog).findByText("K7P2QX")).toBeInTheDocument();
    expect(within(dialog).queryByText(/is linked/)).toBeNull();
  });
});
