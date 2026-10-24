import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Session } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { LOCALE_KEY, THEME_KEY } from "@/lib/theme";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMe, makeProfile } from "@/test/fixtures";
import { memberIn, viewerIn } from "@/test/orgs";
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

/*
 * The account's two organization settings (ORG-04, POS-06): the organization new AIs join when
 * they are started without --org, and deleting the account.
 */
describe("default organization (ORG-04)", () => {
  const memberships = [memberIn("o1", "admin"), memberIn("o2", "member"), memberIn("o3", "guest")];
  const withOrgs = (defaultOrg: string | undefined, extra: Parameters<typeof mockApi>[0] = {}) => ({
    ...base,
    "GET /me": json(200, { ...me, ...(defaultOrg ? { default_org_id: defaultOrg } : {}) }),
    "GET /intelligences/u1": json(200, { ...profile, memberships }),
    "GET /orgs": viewerIn(...memberships)["GET /orgs"],
    ...extra,
  });
  const select = () => screen.findByRole("combobox", { name: "Default organization for new AIs" });
  const optionsOf = (el: HTMLElement) =>
    within(el)
      .getAllByRole("option")
      .map((o) => o.textContent);

  it("lists every organization the viewer belongs to, guest ones too, and shows the current default", async () => {
    mockApi(withOrgs("o2"));
    renderRoute("/settings");
    const field = await select();
    expect(optionsOf(field)).toEqual(["Acme", "Globex", "Initech"]);
    expect(field).toHaveValue("o2");
  });

  it("says what the default is for", async () => {
    mockApi(withOrgs("o1"));
    renderRoute("/settings");
    await select();
    expect(
      screen.getByText("New AIs you start join it, unless you start them with --org."),
    ).toBeInTheDocument();
  });

  it("changes it with the id of the organization, and shows the new one", async () => {
    const { calls } = mockApi(
      withOrgs("o2", { "PUT /me/default-org": json(200, { ...me, default_org_id: "o1" }) }),
    );
    const user = userEvent.setup();
    renderRoute("/settings");
    const field = await select();
    await user.selectOptions(field, "o1");
    await waitFor(() => expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1));
    expect(calls.find((c) => c.method === "PUT")?.path).toBe("/me/default-org");
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ org_id: "o1" });
    expect(await screen.findByText("New AIs now join Acme")).toBeInTheDocument();
    expect(field).toHaveValue("o1");
  });

  it("remembers the new default for the rest of the app", async () => {
    mockApi(withOrgs("o2", { "PUT /me/default-org": json(200, {}) }));
    const user = userEvent.setup();
    const { queryClient } = renderRoute("/settings");
    await user.selectOptions(await select(), "o1");
    await screen.findByText("New AIs now join Acme");
    expect(queryClient.getQueryData<{ default_org_id?: string }>(["me"])?.default_org_id).toBe(
      "o1",
    );
  });

  it("shows an error toast and goes back to the old default when the server refuses (403)", async () => {
    const { calls } = mockApi(
      withOrgs("o2", { "PUT /me/default-org": apiError(403, "forbidden", "forbidden") }),
    );
    const user = userEvent.setup();
    renderRoute("/settings");
    const field = await select();
    await user.selectOptions(field, "o3");
    expect(
      await screen.findByText("You can only choose an organization you belong to."),
    ).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1);
    await waitFor(() => expect(field).toHaveValue("o2"));
  });

  it("says so when the default could not be changed for another reason", async () => {
    mockApi(withOrgs("o2", { "PUT /me/default-org": apiError(500, "internal", "boom") }));
    const user = userEvent.setup();
    renderRoute("/settings");
    const field = await select();
    await user.selectOptions(field, "o1");
    expect(
      await screen.findByText("Couldn’t change the default organization. Try again."),
    ).toBeInTheDocument();
    await waitFor(() => expect(field).toHaveValue("o2"));
  });

  it("does not send the choice again when it is the one already made", async () => {
    const { calls } = mockApi(withOrgs("o2", { "PUT /me/default-org": json(200, {}) }));
    const user = userEvent.setup();
    renderRoute("/settings");
    await user.selectOptions(await select(), "o2");
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
  });

  it("asks to choose one when the account has none yet, and picks none for the viewer", async () => {
    mockApi(withOrgs(undefined));
    renderRoute("/settings");
    const field = await select();
    expect(field).toHaveValue("");
    expect(optionsOf(field)[0]).toBe("Choose an organization");
    expect(within(field).getByRole("option", { name: "Choose an organization" })).toBeDisabled();
  });

  it("ignores a default the viewer no longer belongs to", async () => {
    mockApi(withOrgs("o9"));
    renderRoute("/settings");
    expect(await select()).toHaveValue("");
  });

  it("is not there for someone who belongs to no organization, or while that is unknown", async () => {
    mockApi({ ...base });
    renderRoute("/settings");
    await screen.findByRole("heading", { level: 1, name: "Settings" });
    await screen.findByLabelText(/Display name/);
    expect(screen.queryByRole("combobox", { name: "Default organization for new AIs" })).toBeNull();
    expect(screen.queryByRole("heading", { level: 2, name: "Default organization" })).toBeNull();
  });

  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    mockApi(
      withOrgs("o2", { "GET /me": json(200, { ...me, locale: "pt-BR", default_org_id: "o2" }) }),
    );
    renderRoute("/settings");
    const field = await screen.findByRole("combobox", {
      name: "Organização padrão para novas IAs",
    });
    expect(field).toHaveValue("o2");
    expect(
      screen.getByText("As IAs que você iniciar entram nela, a menos que use --org."),
    ).toBeInTheDocument();
  });
});

describe("deleting the account (POS-06)", () => {
  const open = async (extra: Parameters<typeof mockApi>[0] = {}) => {
    const api = mockApi({ ...base, ...extra });
    const user = userEvent.setup();
    const view = renderRoute("/settings");
    await user.click(await screen.findByRole("button", { name: "Delete my account" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete your account?" });
    return { ...api, ...view, user, dialog };
  };
  const password = (dialog: HTMLElement) => within(dialog).getByLabelText("Password");
  const confirm = (dialog: HTMLElement) =>
    within(dialog).getByRole("button", { name: "Delete my account" });
  const deletes = (calls: { method: string; path: string }[]) =>
    calls.filter((c) => c.method === "DELETE" && c.path === "/me");

  it("is a section of its own, with the way in", async () => {
    mockApi(base);
    renderRoute("/settings");
    const section = await screen.findByRole("region", { name: "Delete account" });
    expect(within(section).getByRole("button", { name: "Delete my account" })).toBeInTheDocument();
  });

  it("asks for the password and says what stays: AIs, groups and connections stay in their organizations", async () => {
    const { calls, dialog } = await open();
    // Literal wording (L-032): what is deleted, and what is not.
    expect(dialog).toHaveTextContent(
      "Your account, your sessions and tokens and your direct messages are deleted for good. Your AIs, groups and connections stay in their organizations: your AIs go offline, and an admin of their organization can delete them. This can’t be undone.",
    );
    expect(password(dialog)).toHaveAttribute("type", "password");
    expect(deletes(calls)).toHaveLength(0);
  });

  it("sends nothing without a password", async () => {
    const { calls, user, dialog } = await open();
    await user.click(confirm(dialog));
    expect(await within(dialog).findByText("Required")).toBeInTheDocument();
    expect(deletes(calls)).toHaveLength(0);
  });

  it("shows a wrong password under the field and keeps the dialog open (422)", async () => {
    const { calls, user, dialog } = await open({
      "DELETE /me": apiError(422, "invalid", "validation failed", { password: "is incorrect" }),
    });
    await user.type(password(dialog), "not-my-password");
    await user.click(confirm(dialog));
    expect(await within(dialog).findByText("is incorrect")).toBeInTheDocument();
    expect(password(dialog)).toHaveAttribute("aria-invalid", "true");
    expect(deletes(calls)).toHaveLength(1);
    expect(screen.getByRole("dialog", { name: "Delete your account?" })).toBeInTheDocument();
  });

  it("says the last admin of the instance cannot delete their account (409 last_instance_admin)", async () => {
    const { user, dialog } = await open({
      "DELETE /me": apiError(409, "last_instance_admin", "last instance admin"),
    });
    await user.type(password(dialog), "correct-horse-1");
    await user.click(confirm(dialog));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "You’re the only admin of this instance. Make another user an admin before deleting your account.",
    );
    expect(screen.getByRole("dialog", { name: "Delete your account?" })).toBeInTheDocument();
  });

  it("logs out on success: the password is sent, the session and the cache are gone", async () => {
    const { calls, queryClient, router, user, dialog } = await open({
      "DELETE /me": new Response(null, { status: 204 }),
      // The login page the account leaves for.
      "GET /setup/status": json(200, { needs_setup: false }),
    });
    await user.type(password(dialog), "correct-horse-1");
    await user.click(confirm(dialog));
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(deletes(calls)).toHaveLength(1);
    expect(calls.find((c) => c.method === "DELETE")?.body).toEqual({
      password: "correct-horse-1",
    });
    expect(queryClient.getQueryData(["me"])).toBeUndefined();
    expect(await screen.findByText("Your account was deleted")).toBeInTheDocument();
  });

  it("sends the request once while it runs", async () => {
    let release: () => void = () => {};
    const { calls, user, dialog } = await open({
      "DELETE /me": () =>
        new Promise<Response>((resolve) => {
          release = () => resolve(new Response(null, { status: 204 }));
        }),
    });
    await user.type(password(dialog), "correct-horse-1");
    await user.click(confirm(dialog));
    await waitFor(() => expect(confirm(dialog)).toBeDisabled());
    await user.keyboard("{Enter}");
    expect(deletes(calls)).toHaveLength(1);
    release();
  });

  it("keeps the account when the dialog is cancelled", async () => {
    const { calls, user, dialog } = await open({
      "DELETE /me": new Response(null, { status: 204 }),
    });
    await user.type(password(dialog), "correct-horse-1");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(deletes(calls)).toHaveLength(0);
  });

  it("starts empty each time it opens", async () => {
    const { user, dialog } = await open();
    await user.type(password(dialog), "correct-horse-1");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await user.click(screen.getByRole("button", { name: "Delete my account" }));
    const again = await screen.findByRole("dialog", { name: "Delete your account?" });
    expect(password(again)).toHaveValue("");
  });

  it("says so when the account could not be deleted, and when the server cannot be reached", async () => {
    let attempt = 0;
    const { user, dialog } = await open({
      "DELETE /me": () => {
        attempt += 1;
        if (attempt === 1) return apiError(500, "internal", "boom");
        throw new TypeError("failed to fetch");
      },
    });
    await user.type(password(dialog), "correct-horse-1");
    await user.click(confirm(dialog));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Something went wrong. Try again.",
    );
    await user.click(confirm(dialog));
    await waitFor(() =>
      expect(within(dialog).getByRole("alert")).toHaveTextContent("Can’t reach the server."),
    );
  });

  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    mockApi({ ...base, "GET /me": json(200, { ...me, locale: "pt-BR" }) });
    const user = userEvent.setup();
    renderRoute("/settings");
    await user.click(await screen.findByRole("button", { name: "Excluir minha conta" }));
    const dialog = await screen.findByRole("dialog", { name: "Excluir sua conta?" });
    expect(dialog).toHaveTextContent(
      "Sua conta, suas sessões e tokens e suas mensagens diretas são apagados para sempre. Suas IAs, grupos e conexões continuam nas organizações deles: suas IAs ficam offline, e um administrador da organização pode apagá-las. Isto não pode ser desfeito.",
    );
  });
});
