import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DeviceRequest } from "@/lib/api/types.gen";
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

describe("device approval page (WIZ-02, DEV-02)", () => {
  it("shows the machine, code, address and age of a pending request", async () => {
    mockApi({ "GET /me": json(200, me), [`GET /devices/${CODE}`]: json(200, makeRequest()) });
    renderRoute(`/devices/${CODE}`);
    expect(await screen.findByRole("heading", { name: "Approve a machine" })).toBeInTheDocument();
    expect(within(request()).getByText("ana-laptop")).toBeInTheDocument();
    expect(field("Code")).toBe(CODE);
    expect(field("Address")).toBe("203.0.113.24");
    expect(field("Requested")).toBe("3 min. ago");
    expect(field("Expires in")).toMatch(/^[67]:\d\d$/);
    expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Deny" })).toBeEnabled();
  });

  it("approves the request and says the terminal continues", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      [`GET /devices/${CODE}`]: json(200, makeRequest()),
      [`POST /devices/${CODE}/approve`]: json(200, makeRequest({ status: "approved" })),
    });
    const user = userEvent.setup();
    renderRoute(`/devices/${CODE}`);
    await user.click(await screen.findByRole("button", { name: "Approve" }));
    expect(
      await screen.findByRole("heading", { name: "ana-laptop is approved" }),
    ).toBeInTheDocument();
    expect(screen.getByText(/terminal finishes setup/)).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "POST").map((c) => c.path)).toEqual([
      `/devices/${CODE}/approve`,
    ]);
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
  });

  it("denies the request", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      [`GET /devices/${CODE}`]: json(200, makeRequest()),
      [`POST /devices/${CODE}/deny`]: json(200, makeRequest({ status: "denied" })),
    });
    const user = userEvent.setup();
    renderRoute(`/devices/${CODE}`);
    await user.click(await screen.findByRole("button", { name: "Deny" }));
    expect(
      await screen.findByRole("heading", { name: "ana-laptop was denied" }),
    ).toBeInTheDocument();
    expect(screen.getByText(/approval denied/)).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "POST").map((c) => c.path)).toEqual([
      `/devices/${CODE}/deny`,
    ]);
  });

  it("shows an expired request as expired, without the decision buttons", async () => {
    mockApi({
      "GET /me": json(200, me),
      [`GET /devices/${CODE}`]: json(200, makeRequest({ status: "expired" })),
    });
    renderRoute(`/devices/${CODE}`);
    expect(
      await screen.findByRole("heading", { name: "This request expired" }),
    ).toBeInTheDocument();
    expect(screen.getByText("partyline setup --browser").tagName).toBe("CODE");
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
  });

  it("treats a pending request past its expiry as expired", async () => {
    mockApi({
      "GET /me": json(200, me),
      [`GET /devices/${CODE}`]: json(
        200,
        makeRequest({ expires_at: new Date(Date.now() - 5_000).toISOString() }),
      ),
    });
    renderRoute(`/devices/${CODE}`);
    expect(
      await screen.findByRole("heading", { name: "This request expired" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Approve" })).toBeNull();
  });

  it("turns to expired when the server answers 410 on approve", async () => {
    mockApi({
      "GET /me": json(200, me),
      [`GET /devices/${CODE}`]: json(200, makeRequest()),
      [`POST /devices/${CODE}/approve`]: apiError(410, "gone", "device request expired"),
    });
    const user = userEvent.setup();
    renderRoute(`/devices/${CODE}`);
    await user.click(await screen.findByRole("button", { name: "Approve" }));
    expect(
      await screen.findByRole("heading", { name: "This request expired" }),
    ).toBeInTheDocument();
  });

  it("shows the decision someone else made when the server answers 409", async () => {
    let current = makeRequest();
    mockApi({
      "GET /me": json(200, me),
      [`GET /devices/${CODE}`]: () => json(200, current),
      [`POST /devices/${CODE}/approve`]: () => {
        current = makeRequest({ status: "denied" });
        return apiError(409, "conflict", "device request already decided");
      },
    });
    const user = userEvent.setup();
    renderRoute(`/devices/${CODE}`);
    await user.click(await screen.findByRole("button", { name: "Approve" }));
    expect(
      await screen.findByRole("heading", { name: "ana-laptop was denied" }),
    ).toBeInTheDocument();
  });

  it("says when no request has this code", async () => {
    mockApi({
      "GET /me": json(200, me),
      [`GET /devices/${CODE}`]: apiError(404, "not_found", "device request not found"),
    });
    renderRoute(`/devices/${CODE}`);
    expect(
      await screen.findByRole("heading", { name: "No request with this code" }),
    ).toBeInTheDocument();
  });

  it("sends logged-out visitors through login and back to the request", async () => {
    let loggedIn = false;
    mockApi({
      "GET /setup/status": json(200, { needs_setup: false }),
      "GET /me": () => (loggedIn ? json(200, me) : apiError(401, "unauthorized", "login required")),
      "POST /auth/login": () => {
        loggedIn = true;
        return json(200, me);
      },
      [`GET /devices/${CODE}`]: json(200, makeRequest()),
    });
    const user = userEvent.setup();
    const { router } = renderRoute(`/devices/${CODE}`);
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(router.state.location.search).toEqual({ redirect: `/devices/${CODE}` });

    await user.type(await screen.findByLabelText("Username"), "ana");
    await user.type(screen.getByLabelText("Password"), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "Log in" }));
    await waitFor(() => expect(router.state.location.pathname).toBe(`/devices/${CODE}`));
    expect(await screen.findByRole("button", { name: "Approve" })).toBeInTheDocument();
  });
});
