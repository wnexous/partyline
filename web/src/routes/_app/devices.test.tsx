import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DeviceRequest } from "@/shared/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMe } from "@/test/fixtures";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

const me = makeMe();
const CODE = "K7P2QX9M";

function makeRequest(over: Partial<DeviceRequest> = {}): DeviceRequest {
  const now = Date.now();
  return {
    code: CODE,
    machine: "ana-laptop",
    ip: "203.0.113.24",
    status: "pending",
    created_at: new Date(now - 3 * 60_000).toISOString(),
    expires_at: new Date(now + 7 * 60_000).toISOString(),
    ...over,
  };
}

beforeEach(() => useLiveStore.setState({ locale: "en", presence: {} }));
afterEach(() => vi.unstubAllGlobals());

const request = () => screen.getByRole("region", { name: "Approval request" });
const field = (label: string) =>
  within(request()).getByText(label).nextElementSibling?.textContent ?? "";
const posts = (calls: { method: string; path: string; body: unknown }[]) =>
  calls.filter((c) => c.method === "POST").map((c) => [c.path, c.body]);

/** Opens /devices and types code into the code field, then continues. */
async function enterCode(code = "k7p2-qx9m", path = "/devices") {
  const user = userEvent.setup();
  const utils = renderRoute(path);
  const input = await screen.findByLabelText("Code from your terminal");
  await user.type(input, code);
  await user.click(screen.getByRole("button", { name: "Continue" }));
  return { ...utils, user };
}

describe("device approval page (WIZ-02, DEV-02, DEV-05)", () => {
  it("asks for the code before it shows anything about a request", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
    });
    renderRoute("/devices");
    expect(await screen.findByRole("heading", { name: "Approve a machine" })).toBeInTheDocument();
    expect(screen.getByLabelText("Code from your terminal")).toHaveValue("");
    expect(screen.getByText("partyline setup --browser").tagName).toBe("CODE");
    expect(screen.queryByRole("region", { name: "Approval request" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
    expect(calls.filter((c) => c.path.startsWith("/devices"))).toEqual([]);
  });

  it("looks up the typed code in the request body, then shows the machine, address and age", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "POST /devices/lookup": json(200, makeRequest()),
    });
    await enterCode();
    expect(await screen.findByRole("region", { name: "Approval request" })).toBeInTheDocument();
    // Typed with a hyphen and in lower case; sent as the server reads it, never in the URL.
    expect(posts(calls)).toEqual([["/devices/lookup", { code: CODE }]]);
    expect(within(request()).getByText("ana-laptop")).toBeInTheDocument();
    expect(field("Code")).toBe(CODE);
    expect(field("Address")).toBe("203.0.113.24");
    expect(field("Requested")).toBe("3 min. ago");
    expect(field("Expires in")).toMatch(/^[67]:\d\d$/);
    expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Deny" })).toBeEnabled();
  });

  it("refuses a code that is not 8 characters without asking the server", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
    });
    await enterCode("K7P2");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Codes have 8 letters and digits, like ABCD-EFGH.",
    );
    expect(posts(calls)).toEqual([]);
  });

  it("says when no pending request has the code", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "POST /devices/lookup": apiError(404, "not_found", "device request not found"),
    });
    await enterCode();
    expect(await screen.findByRole("alert")).toHaveTextContent("No pending request has this code.");
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
  });

  it("says to wait after too many wrong codes (429)", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "POST /devices/lookup": apiError(429, "rate_limited", "too many wrong codes"),
    });
    await enterCode();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Too many wrong codes. Wait a minute and try again.",
    );
  });

  it("approves with the typed code and says the terminal continues", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "POST /devices/lookup": json(200, makeRequest()),
      "POST /devices/approve": json(200, makeRequest({ status: "approved" })),
    });
    const { user } = await enterCode();
    await user.click(await screen.findByRole("button", { name: "Approve" }));
    expect(
      await screen.findByRole("heading", { name: "ana-laptop is approved" }),
    ).toBeInTheDocument();
    expect(screen.getByText(/terminal finishes setup/)).toBeInTheDocument();
    expect(posts(calls)).toEqual([
      ["/devices/lookup", { code: CODE }],
      ["/devices/approve", { code: CODE }],
    ]);
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
  });

  it("denies the request with the typed code", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "POST /devices/lookup": json(200, makeRequest()),
      "POST /devices/deny": json(200, makeRequest({ status: "denied" })),
    });
    const { user } = await enterCode();
    await user.click(await screen.findByRole("button", { name: "Deny" }));
    expect(
      await screen.findByRole("heading", { name: "ana-laptop was denied" }),
    ).toBeInTheDocument();
    expect(screen.getByText(/approval denied/)).toBeInTheDocument();
    expect(posts(calls).at(-1)).toEqual(["/devices/deny", { code: CODE }]);
  });

  it("goes back to the code field to use another code", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "POST /devices/lookup": json(200, makeRequest()),
    });
    const { user } = await enterCode();
    await user.click(await screen.findByRole("button", { name: "Use another code" }));
    expect(screen.getByLabelText("Code from your terminal")).toHaveValue("");
    expect(screen.queryByRole("region", { name: "Approval request" })).toBeNull();
  });

  it("shows an expired request as expired, without the decision buttons", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "POST /devices/lookup": json(200, makeRequest({ status: "expired" })),
    });
    await enterCode();
    expect(
      await screen.findByRole("heading", { name: "This request expired" }),
    ).toBeInTheDocument();
    expect(screen.getByText("partyline setup --browser").tagName).toBe("CODE");
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
  });

  it("treats a pending request past its expiry as expired", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "POST /devices/lookup": json(
        200,
        makeRequest({ expires_at: new Date(Date.now() - 5_000).toISOString() }),
      ),
    });
    await enterCode();
    expect(
      await screen.findByRole("heading", { name: "This request expired" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
  });

  it("turns to expired when the server answers 410 on approve", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "POST /devices/lookup": json(200, makeRequest()),
      "POST /devices/approve": apiError(410, "gone", "device request expired"),
    });
    const { user } = await enterCode();
    await user.click(await screen.findByRole("button", { name: "Approve" }));
    expect(
      await screen.findByRole("heading", { name: "This request expired" }),
    ).toBeInTheDocument();
  });

  it("shows the decision someone else made when the server answers 409", async () => {
    let current = makeRequest();
    mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "POST /devices/lookup": () => json(200, current),
      "POST /devices/approve": () => {
        current = makeRequest({ status: "denied" });
        return apiError(409, "conflict", "device request already decided");
      },
    });
    const { user } = await enterCode();
    await user.click(await screen.findByRole("button", { name: "Approve" }));
    expect(
      await screen.findByRole("heading", { name: "ana-laptop was denied" }),
    ).toBeInTheDocument();
  });

  it("opens an old /devices/<code> link on the empty code field, without looking the code up", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
    });
    const { router } = renderRoute(`/devices/${CODE}`);
    await waitFor(() => expect(router.state.location.pathname).toBe("/devices"));
    expect(await screen.findByLabelText("Code from your terminal")).toHaveValue("");
    expect(calls.filter((c) => c.path.startsWith("/devices"))).toEqual([]);
  });

  it("sends logged-out visitors through login and back to the code field", async () => {
    let loggedIn = false;
    mockApi({
      "GET /setup/status": json(200, { needs_setup: false }),
      "GET /me": () => (loggedIn ? json(200, me) : apiError(401, "unauthorized", "login required")),
      "POST /auth/login": () => {
        loggedIn = true;
        return json(200, me);
      },
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/devices");
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(router.state.location.search).toEqual({ redirect: "/devices" });

    await user.type(await screen.findByLabelText("Username"), "ana");
    await user.type(screen.getByLabelText("Password"), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "Log in" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/devices"));
    expect(await screen.findByLabelText("Code from your terminal")).toBeInTheDocument();
  });
});
