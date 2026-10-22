import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LiveBanner } from "@/components/live-status";
import { api } from "@/lib/api/client";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMe } from "@/test/fixtures";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

describe("authenticated shell", () => {
  beforeEach(() => {
    useLiveStore.setState({ locale: "en", theme: "system" });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  it("redirects an unauthenticated visitor to /login?redirect=/rooms (UI-02)", async () => {
    mockApi({ "GET /me": apiError(401, "unauthorized") });
    const { router } = renderRoute("/rooms");
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(router.state.location.search).toEqual({ redirect: "/rooms" });
  });

  it("lists the destinations in the account menu, without the admin section for members, and marks the current page", async () => {
    mockApi({
      "GET /me": json(200, makeMe({ role: "member" })),
      "GET /conversations": json(200, { items: [] }),
    });
    const user = userEvent.setup();
    renderRoute("/settings");
    await user.click(await screen.findByRole("button", { name: "Account menu" }));
    const menu = await screen.findByRole("menu");
    expect(within(menu).getByRole("menuitem", { name: "Settings" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    for (const name of [
      "Conversations",
      "Contacts",
      "Connectors",
      "Identities",
      "API tokens",
      "Settings",
    ]) {
      expect(within(menu).getByRole("menuitem", { name })).toBeInTheDocument();
    }
    expect(within(menu).queryByRole("menuitem", { name: "Users" })).toBeNull();
    expect(within(menu).queryByText("Admin")).toBeNull();
  });

  it("shows the admin section to admins", async () => {
    mockApi({
      "GET /me": json(200, makeMe({ role: "admin" })),
      "GET /conversations": json(200, { items: [] }),
    });
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await screen.findByRole("button", { name: "Account menu" }));
    expect(await screen.findByRole("menuitem", { name: "Users" })).toHaveAttribute(
      "href",
      "/admin/users",
    );
  });

  it("shows the 403 page when a member opens an admin route (UI-21)", async () => {
    mockApi({ "GET /me": json(200, makeMe({ role: "member" })) });
    renderRoute("/admin/users");
    expect(await screen.findByRole("heading", { name: "Admins only" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Users" })).not.toBeInTheDocument();
  });

  it("sends the user to login with the current route when a request answers 401 (UI-02)", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, makeMe()),
      "GET /rooms": apiError(401, "unauthorized"),
    });
    const { router } = renderRoute("/agents?tab=mine");
    await screen.findByRole("heading", { name: "Agents" });
    await act(() => api("GET", "/rooms").catch(() => undefined));
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(router.state.location.search).toEqual({ redirect: "/agents?tab=mine" });
    expect(calls.some((c) => c.path === "/rooms")).toBe(true);
  });

  it("switches theme and language from the account menu (UI-23, UI-25)", async () => {
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /conversations": json(200, { items: [] }),
    });
    const user = userEvent.setup();
    renderRoute("/");
    // Submenus are driven by keyboard here: happy-dom has no layout for pointer grace areas.
    const pick = async (submenu: string, option: string) => {
      await user.click(screen.getByRole("button", { name: "Account menu" }));
      act(() => screen.getByRole("menuitem", { name: submenu }).focus());
      await user.keyboard("{ArrowRight}");
      act(() => screen.getByRole("menuitemradio", { name: option }).focus());
      await user.keyboard("{Enter}");
    };
    await screen.findByRole("button", { name: "Account menu" });
    await pick("Theme", "Dark");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(localStorage.getItem("partyline.theme")).toBe("dark");

    await pick("Language", "Português (Brasil)");
    await user.click(screen.getByRole("button", { name: "Menu da conta" }));
    expect(await screen.findByRole("menuitem", { name: "Conversas" })).toBeInTheDocument();
    expect(localStorage.getItem("partyline.locale")).toBe("pt-BR");
  });
});

describe("live banner (UI-06)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    useLiveStore.setState({ locale: "en" });
  });
  afterEach(() => vi.useRealTimers());

  it("appears only after the socket has not been open for more than 2 s", () => {
    const reconnecting = "Connection lost. Reconnecting…";
    act(() => useLiveStore.getState().setStatus("open"));
    render(<LiveBanner />);
    // The live region is mounted while connected (empty), so screen readers announce the
    // reconnecting text when it appears inside it.
    const region = screen.getByRole("status");
    expect(region).toHaveAttribute("aria-live", "polite");
    expect(region).toBeEmptyDOMElement();

    act(() => useLiveStore.getState().setStatus("closed"));
    act(() => vi.advanceTimersByTime(1_999));
    expect(screen.getByRole("status")).toBeEmptyDOMElement();
    act(() => vi.advanceTimersByTime(1));
    expect(screen.getByRole("status")).toBe(region);
    expect(region).toHaveTextContent(reconnecting);

    // Retrying (closed -> connecting -> closed) keeps the banner up: the outage continues.
    act(() => useLiveStore.getState().setStatus("connecting"));
    expect(region).toHaveTextContent(reconnecting);
    act(() => useLiveStore.getState().setStatus("closed"));
    expect(region).toHaveTextContent(reconnecting);

    act(() => useLiveStore.getState().setStatus("open"));
    expect(screen.getByRole("status")).toBe(region);
    expect(region).toBeEmptyDOMElement();

    // A short blip under 2 s never shows the banner.
    act(() => useLiveStore.getState().setStatus("connecting"));
    act(() => vi.advanceTimersByTime(1_500));
    act(() => useLiveStore.getState().setStatus("open"));
    act(() => vi.advanceTimersByTime(1_000));
    expect(region).toBeEmptyDOMElement();
  });
});
