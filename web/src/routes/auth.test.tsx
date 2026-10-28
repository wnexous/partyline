import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMe } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { renderRoute } from "@/test/render";

vi.mock("@/app/live", () => ({ startLive: vi.fn(() => () => {}) }));

const me = makeMe({ role: "admin" });

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

describe("setup page (UI-01)", () => {
  it("is shown instead of login while the instance needs setup", async () => {
    mockApi({ "GET /setup/status": json(200, { needs_setup: true }) });
    const { router } = renderRoute("/login");
    expect(await screen.findByRole("heading", { name: "Set up partyline" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/setup");
    expect(screen.getByLabelText("Setup token")).toBeInTheDocument();
    expect(screen.getByLabelText("Username")).toBeInTheDocument();
    expect(screen.getByLabelText("Password")).toBeInTheDocument();
  });

  it("redirects to login once the instance has users", async () => {
    mockApi({ "GET /setup/status": json(200, { needs_setup: false }) });
    const { router } = renderRoute("/setup");
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
  });

  it("posts the token and account, then opens the app", async () => {
    const { calls } = mockApi({
      "GET /setup/status": json(200, { needs_setup: true }),
      "POST /setup": json(201, me),
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/setup");
    await user.type(await screen.findByLabelText("Setup token"), "  setup-token-0001 ");
    await user.type(screen.getByLabelText("Username"), "ana");
    await user.type(screen.getByLabelText("Password"), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "Create admin account" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      token: "setup-token-0001",
      username: "ana",
      password: "correct horse battery",
    });
  });

  it("shows 422 field errors under their inputs", async () => {
    mockApi({
      "GET /setup/status": json(200, { needs_setup: true }),
      "POST /setup": apiError(422, "invalid", "validation failed", { username: "already taken" }),
    });
    const user = userEvent.setup();
    renderRoute("/setup");
    await user.type(await screen.findByLabelText("Setup token"), "tok");
    await user.type(screen.getByLabelText("Username"), "ana");
    await user.type(screen.getByLabelText("Password"), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "Create admin account" }));
    expect(await screen.findByText("already taken")).toBeInTheDocument();
    expect(screen.getByLabelText("Username")).toHaveAttribute("aria-invalid", "true");
  });
});

describe("login page (UI-02)", () => {
  const routes = (login: Response) => ({
    "GET /setup/status": json(200, { needs_setup: false }),
    "POST /auth/login": login,
    "GET /me": json(200, me),
  });

  it("logs in and returns to the redirect target", async () => {
    const { calls } = mockApi(routes(json(200, me)));
    const user = userEvent.setup();
    const { router } = renderRoute("/login?redirect=%2Ftokens%3Ftab%3Dmine");
    await user.type(await screen.findByLabelText("Username"), "ana");
    await user.type(screen.getByLabelText("Password"), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "Log in" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/tokens"));
    expect(router.state.location.search).toEqual({ tab: "mine" });
    expect(calls.find((c) => c.path === "/auth/login")?.body).toEqual({
      username: "ana",
      password: "correct horse battery",
    });
  });

  it("ignores redirect targets outside the app", async () => {
    mockApi(routes(json(200, me)));
    const user = userEvent.setup();
    const { router } = renderRoute("/login?redirect=%2F%2Fevil.example%2Fsteal");
    await user.type(await screen.findByLabelText("Username"), "ana");
    await user.type(screen.getByLabelText("Password"), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "Log in" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
    expect(router.state.location.href).toBe("/");
  });

  it("shows a clear error for wrong credentials and stays on login", async () => {
    mockApi(routes(apiError(401, "unauthorized", "invalid credentials")));
    const user = userEvent.setup();
    const { router } = renderRoute("/login?redirect=%2Frooms");
    await user.type(await screen.findByLabelText("Username"), "ana");
    await user.type(screen.getByLabelText("Password"), "wrong password");
    await user.click(screen.getByRole("button", { name: "Log in" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Wrong username or password.");
    expect(router.state.location.pathname).toBe("/login");
  });
});

describe("invite page (UI-03)", () => {
  it("shows the expired state for a used or expired invite", async () => {
    mockApi({ "GET /invites/old-token": apiError(410, "gone", "invite expired") });
    renderRoute("/invite/old-token");
    expect(
      await screen.findByRole("heading", { level: 1, name: "This invite can’t be used" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Go to login" })).toHaveAttribute("href", "/login");
    expect(screen.queryByLabelText("Username")).not.toBeInTheDocument();
  });

  it("validates with the API rules before posting, then creates the account", async () => {
    const { calls } = mockApi({
      "GET /invites/tok-1": json(200, { role: "member", expires_at: "2026-10-26T12:00:00Z" }),
      "POST /invites/tok-1/accept": json(201, makeMe({ username: "bia" })),
      "GET /me": json(200, makeMe({ username: "bia" })),
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/invite/tok-1");
    const username = await screen.findByLabelText("Username");
    expect(screen.getByText(/Invited as a member/)).toBeInTheDocument();

    await user.type(username, "ab");
    await user.type(screen.getByLabelText("Password"), "123456789");
    await user.click(screen.getByRole("button", { name: "Create account" }));
    expect(
      await screen.findByText("3 to 32 characters: lowercase letters, digits, _ or -"),
    ).toBeInTheDocument();
    expect(screen.getByText("At least 10 characters")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST")).toBe(false);

    await user.clear(username);
    await user.type(username, "bia");
    await user.type(screen.getByLabelText("Password"), "0");
    await user.click(screen.getByRole("button", { name: "Create account" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      username: "bia",
      password: "1234567890",
    });
  });
});
