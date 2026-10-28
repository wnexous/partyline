import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LOCALE_KEY, THEME_KEY } from "@/lib/theme";
import { json, mockApi } from "@/test/fetch";
import { makeMe, makeProfile } from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { useLiveStore } from "@/test/live-store";
import { renderRoute } from "@/test/render";

vi.mock("@/app/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * The account's menu (SET-03, SET-04): the avatar groups the pages that are the person's own
 * under "Account", the ways elsewhere under "Go to" and, for an instance admin, the instance's
 * pages under "Admin"; the page in view is marked, every row is a finger's height, and the theme,
 * the language and Log out stay where they were.
 */

stubLayout();

beforeEach(() => {
  useLiveStore.setState({ locale: "en", theme: "system" });
  window.localStorage.clear();
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function api(role: "member" | "admin" = "member", extra: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, makeMe({ role })),
    "GET /conversations": json(200, { items: [] }),
    "GET /orgs": json(200, { items: [] }),
    "GET /connectors": json(200, { items: [] }),
    "GET /identities": json(200, { items: [] }),
    "GET /intelligences/u1": json(200, makeProfile({ ...makeMe().intelligence, can_edit: true })),
    "GET /me/sessions": json(200, { items: [] }),
    "PATCH /me": json(200, makeMe()),
    "POST /auth/logout": new Response(null, { status: 204 }),
    ...extra,
  });
}

async function openMenu(name = "Account menu") {
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name }));
  return { user, menu: await screen.findByRole("menu") };
}

/** The group whose heading reads `label`: a group is its heading and the rows under it. */
const group = (menu: HTMLElement, label: string) => {
  const heading = within(menu).getByText(label, { selector: "[data-slot=dropdown-menu-label]" });
  return heading.closest("[data-slot=dropdown-menu-group]") as HTMLElement;
};

describe("the account menu (SET-03)", () => {
  it("lists the account's pages under Account", async () => {
    api();
    renderRoute("/");
    const { menu } = await openMenu();

    const account = group(menu, "Account");
    for (const [name, href] of [
      ["My account", "/account"],
      ["Identities", "/identities"],
      ["API tokens", "/tokens"],
      ["Devices", "/devices"],
    ] as const) {
      expect(within(account).getByRole("menuitem", { name })).toHaveAttribute("href", href);
    }

    const goTo = group(menu, "Go to");
    for (const [name, href] of [
      ["Conversations", "/"],
      ["Contacts", "/contacts"],
      ["Organizations", "/orgs"],
    ] as const) {
      expect(within(goTo).getByRole("menuitem", { name })).toHaveAttribute("href", href);
    }

    expect(within(menu).queryByRole("menuitem", { name: "Settings" })).toBeNull();
    expect(within(menu).queryByRole("menuitem", { name: "Connectors" })).toBeNull();
  });

  it("marks the page in view", async () => {
    api();
    const account = renderRoute("/account");
    const { menu } = await openMenu();
    expect(within(menu).getByRole("menuitem", { name: "My account" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    account.unmount();

    api();
    renderRoute("/identities");
    const { menu: identities } = await openMenu();
    expect(within(identities).getByRole("menuitem", { name: "Identities" })).toHaveAttribute(
      "aria-current",
      "page",
    );
  });

  it("shows administration only to an admin", async () => {
    api("admin");
    const admin = renderRoute("/");
    const { menu } = await openMenu();
    const administration = group(menu, "Admin");
    for (const [name, href] of [
      ["Users", "/admin/users"],
      ["Invites", "/admin/invites"],
      ["Audit log", "/admin/audit"],
    ] as const) {
      expect(within(administration).getByRole("menuitem", { name })).toHaveAttribute("href", href);
    }
    admin.unmount();

    api("member");
    renderRoute("/");
    const { menu: memberMenu } = await openMenu();
    expect(within(memberMenu).queryByText("Admin")).toBeNull();
    for (const name of ["Users", "Invites", "Audit log"]) {
      expect(within(memberMenu).queryByRole("menuitem", { name })).toBeNull();
    }
  });

  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    api("member", { "GET /me": json(200, makeMe({ locale: "pt-BR" })) });
    renderRoute("/");
    const { menu } = await openMenu("Menu da conta");

    const account = group(menu, "Conta");
    for (const [name, href] of [
      ["Minha conta", "/account"],
      ["Identidades", "/identities"],
      ["Tokens de API", "/tokens"],
      ["Dispositivos", "/devices"],
    ] as const) {
      expect(within(account).getByRole("menuitem", { name })).toHaveAttribute("href", href);
    }

    const goTo = group(menu, "Ir para");
    for (const [name, href] of [
      ["Conversas", "/"],
      ["Contatos", "/contacts"],
      ["Organizações", "/orgs"],
    ] as const) {
      expect(within(goTo).getByRole("menuitem", { name })).toHaveAttribute("href", href);
    }
  });

  it("gives every row a finger's height", async () => {
    api();
    renderRoute("/");
    const { menu } = await openMenu();
    const rows = within(menu).getAllByRole("menuitem");
    expect(rows.length).toBeGreaterThanOrEqual(7);
    for (const row of rows) {
      expect(row.className).toContain("min-h-11");
      expect(row.className).toContain("md:min-h-9");
    }
  });
});

describe("the controls the menu already owned (SET-03)", () => {
  it("keeps the theme, the language and the logout", async () => {
    const { calls } = api();
    const user = userEvent.setup();
    const { router } = renderRoute("/");
    // Submenus are driven by keyboard here: happy-dom has no layout for pointer grace areas.
    const pick = async (trigger: string, submenu: string, option: string) => {
      await user.click(await screen.findByRole("button", { name: trigger }));
      act(() => screen.getByRole("menuitem", { name: submenu }).focus());
      await user.keyboard("{ArrowRight}");
      act(() => screen.getByRole("menuitemradio", { name: option }).focus());
      await user.keyboard("{Enter}");
    };

    await pick("Account menu", "Theme", "Dark");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(window.localStorage.getItem(THEME_KEY)).toBe("dark");

    await pick("Account menu", "Language", "Português (Brasil)");
    expect(window.localStorage.getItem(LOCALE_KEY)).toBe("pt-BR");
    await waitFor(() =>
      expect(calls.some((c) => c.method === "PATCH" && c.path === "/me")).toBe(true),
    );

    await user.click(await screen.findByRole("button", { name: "Menu da conta" }));
    await user.click(await screen.findByRole("menuitem", { name: "Sair" }));
    await waitFor(() =>
      expect(calls.some((c) => c.method === "POST" && c.path === "/auth/logout")).toBe(true),
    );
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
  });
});
