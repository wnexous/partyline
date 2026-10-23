import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Session } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { LOCALE_KEY, THEME_KEY } from "@/lib/theme";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMe, makeProfile } from "@/test/fixtures";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

const me = makeMe();
const profile = makeProfile({ ...me.intelligence, can_edit: true });
const sessions: Session[] = [
  {
    id: "s1",
    created_at: "2026-10-01T12:00:00Z",
    last_seen_at: "2026-10-19T12:00:00Z",
    expires_at: "2026-11-18T12:00:00Z",
    user_agent: "Mozilla/5.0 (X11; Linux x86_64) Chrome/140.0",
    ip: "192.0.2.10",
    current: true,
  },
  {
    id: "s2",
    created_at: "2026-10-05T12:00:00Z",
    last_seen_at: "2026-10-18T09:00:00Z",
    expires_at: "2026-11-17T12:00:00Z",
    user_agent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Safari/604.1",
    ip: "198.51.100.7",
    current: false,
  },
];

const base = {
  "GET /me": json(200, me),
  "GET /intelligences/u1": json(200, profile),
  "GET /me/sessions": json(200, { items: sessions }),
};

beforeEach(() => {
  window.localStorage.clear();
  useLiveStore.setState({ locale: "en", theme: "system" });
});
afterEach(() => vi.unstubAllGlobals());

describe("settings page", () => {
  it("persists the theme choice in localStorage (UI-23)", async () => {
    mockApi(base);
    const user = userEvent.setup();
    renderRoute("/settings");
    await user.click(await screen.findByRole("radio", { name: "Dark" }));
    expect(window.localStorage.getItem(THEME_KEY)).toBe("dark");
    expect(useLiveStore.getState().theme).toBe("dark");
    await user.click(screen.getByRole("radio", { name: "System" }));
    expect(window.localStorage.getItem(THEME_KEY)).toBeNull();
  });

  it("persists the language in localStorage and on the account (UI-25)", async () => {
    const { calls } = mockApi({ ...base, "PATCH /me": json(200, { ...me, locale: "pt-BR" }) });
    const user = userEvent.setup();
    renderRoute("/settings");
    await user.click(await screen.findByRole("radio", { name: "Português (Brasil)" }));
    expect(window.localStorage.getItem(LOCALE_KEY)).toBe("pt-BR");
    expect(
      await screen.findByRole("heading", { level: 1, name: "Configurações" }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(calls.find((c) => c.method === "PATCH" && c.path === "/me")?.body).toEqual({
        locale: "pt-BR",
      }),
    );
  });

  it("adopts the account language on a browser without a stored choice", async () => {
    mockApi({ ...base, "GET /me": json(200, { ...me, locale: "pt-BR" }) });
    renderRoute("/settings");
    expect(
      await screen.findByRole("heading", { level: 1, name: "Configurações" }),
    ).toBeInTheDocument();
  });

  it("keeps the browser's stored language over the account's", async () => {
    window.localStorage.setItem(LOCALE_KEY, "en");
    mockApi({ ...base, "GET /me": json(200, { ...me, locale: "pt-BR" }) });
    renderRoute("/settings");
    expect(await screen.findByRole("heading", { level: 1, name: "Settings" })).toBeInTheDocument();
  });

  it("saves the profile and shows the new name in the sidebar right away (PROF-03)", async () => {
    const saved = { ...profile, display_name: "Ana S.", bio: "On call this week." };
    const { calls } = mockApi({ ...base, "PATCH /intelligences/u1": json(200, saved) });
    const user = userEvent.setup();
    renderRoute("/settings");
    const name = await screen.findByLabelText(/Display name/);
    expect(name).toHaveValue("Ana Souza");
    await user.clear(name);
    await user.type(name, "Ana S.");
    await user.type(screen.getByLabelText("Bio"), "On call this week.");
    await user.click(screen.getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PATCH")).toBeDefined());
    expect(calls.find((c) => c.method === "PATCH")?.path).toBe("/intelligences/u1");
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({
      display_name: "Ana S.",
      bio: "On call this week.",
    });
    // The account menu reads the account's own data, which the answer patched.
    await user.click(screen.getByRole("button", { name: "Account menu" }));
    expect(await screen.findByText("Ana S.")).toBeInTheDocument();
  });

  it("edits the @handle and the bio of one's own profile (PROF-03)", async () => {
    const { calls } = mockApi({
      ...base,
      "PATCH /intelligences/u1": json(200, { ...profile, handle: "ana-souza" }),
    });
    const user = userEvent.setup();
    renderRoute("/settings");
    const handle = await screen.findByLabelText(/Handle/);
    expect(handle).toHaveValue("ana");
    await user.clear(handle);
    await user.type(handle, "@Ana-Souza");
    await user.click(screen.getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PATCH")).toBeDefined());
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ handle: "ana-souza" });
  });

  it("offers no app-notification choice and asks for none (CID-22)", async () => {
    const { calls } = mockApi(base);
    renderRoute("/settings");
    await screen.findByRole("heading", { level: 1, name: "Settings" });
    await screen.findByLabelText(/Display name/);
    expect(screen.queryByText("Notifications")).toBeNull();
    expect(screen.queryByRole("combobox", { name: "Send them to" })).toBeNull();
    expect(calls.some((c) => c.path.startsWith("/me/notify"))).toBe(false);
  });

  it("changes the password, checking the confirmation first", async () => {
    let attempt = 0;
    const { calls } = mockApi({
      ...base,
      "POST /me/password": () => {
        attempt += 1;
        return attempt === 1
          ? apiError(422, "invalid", "validation failed", { current_password: "is incorrect" })
          : new Response(null, { status: 204 });
      },
    });
    const user = userEvent.setup();
    renderRoute("/settings");
    const form = await screen.findByRole("form", { name: "Password" });
    await user.type(within(form).getByLabelText("Current password"), "old-password-1");
    await user.type(within(form).getByLabelText("New password"), "new-password-22");
    await user.type(within(form).getByLabelText("Repeat new password"), "new-password-23");
    await user.click(within(form).getByRole("button", { name: "Change password" }));
    expect(await within(form).findByText("Passwords don’t match")).toBeInTheDocument();
    expect(calls.some((c) => c.path === "/me/password")).toBe(false);

    await user.clear(within(form).getByLabelText("Repeat new password"));
    await user.type(within(form).getByLabelText("Repeat new password"), "new-password-22");
    await user.click(within(form).getByRole("button", { name: "Change password" }));
    expect(await within(form).findByText("is incorrect")).toBeInTheDocument();

    await user.click(within(form).getByRole("button", { name: "Change password" }));
    await waitFor(() => expect(within(form).getByLabelText("Current password")).toHaveValue(""));
    expect(calls.filter((c) => c.path === "/me/password").at(-1)?.body).toEqual({
      current_password: "old-password-1",
      new_password: "new-password-22",
    });
  });

  it("lists sessions, marks this device and revokes another", async () => {
    const { calls } = mockApi({
      ...base,
      "DELETE /me/sessions/s2": new Response(null, { status: 204 }),
    });
    const user = userEvent.setup();
    renderRoute("/settings");
    const list = await screen.findByRole("list", { name: "Sessions" });
    const items = within(list).getAllByRole("listitem");
    expect(items).toHaveLength(2);
    expect(within(items[0] as HTMLElement).getByText("This device")).toBeInTheDocument();
    expect(within(items[0] as HTMLElement).queryByRole("button", { name: /Log out/ })).toBeNull();
    expect(within(items[1] as HTMLElement).getByText(/198\.51\.100\.7/)).toBeInTheDocument();
    await user.click(within(items[1] as HTMLElement).getByRole("button", { name: /Log out/ }));
    await waitFor(() =>
      expect(calls.some((c) => c.method === "DELETE" && c.path === "/me/sessions/s2")).toBe(true),
    );
  });
});
