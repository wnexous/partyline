import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AdminUser, AuditEntry, CreatedInvite, Invite } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeMe } from "@/test/fixtures";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

const admin = makeMe({ role: "admin" });
const member = makeMe({ role: "member" });
const ana = { id: "u1", username: "ana", display_name: "Ana Souza" };

const users: AdminUser[] = [
  { ...ana, role: "admin", disabled: false, created_at: "2026-10-01T12:00:00Z" },
  {
    id: "u2",
    username: "bruno",
    display_name: "Bruno Lima",
    role: "member",
    disabled: false,
    created_at: "2026-10-02T12:00:00Z",
  },
  {
    id: "u3",
    username: "carla",
    display_name: "",
    role: "member",
    disabled: true,
    disabled_at: "2026-10-10T12:00:00Z",
    created_at: "2026-10-03T12:00:00Z",
  },
];

const pending: Invite = {
  id: "inv1",
  role: "member",
  status: "pending",
  created_by: ana,
  created_at: "2026-10-18T12:00:00Z",
  expires_at: "2026-10-25T12:00:00Z",
};
const used: Invite = {
  id: "inv2",
  role: "admin",
  status: "used",
  created_by: ana,
  created_at: "2026-10-01T12:00:00Z",
  expires_at: "2026-10-08T12:00:00Z",
  used_by: { id: "u2", username: "bruno", display_name: "Bruno Lima" },
  used_at: "2026-10-02T12:00:00Z",
};

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

const row = (name: string) => screen.getByRole("listitem", { name });

describe("admin access (UI-21)", () => {
  for (const path of ["/admin/users", "/admin/invites", "/admin/audit"]) {
    it(`shows the 403 page to a member opening ${path}`, async () => {
      const { calls } = mockApi({ "GET /me": json(200, member) });
      renderRoute(path);
      expect(await screen.findByRole("heading", { name: "Admins only" })).toBeInTheDocument();
      expect(calls.some((c) => c.path.startsWith("/admin"))).toBe(false);
    });
  }

  it("lists the admin pages in the account menu for admins", async () => {
    mockApi({
      "GET /me": json(200, admin),
      "GET /admin/users": json(200, { items: users }),
      "GET /conversations": json(200, { items: [] }),
    });
    const user = userEvent.setup();
    renderRoute("/admin/users");
    await user.click(await screen.findByRole("button", { name: "Account menu" }));
    expect(await screen.findByRole("menuitem", { name: "Invites" })).toHaveAttribute(
      "href",
      "/admin/invites",
    );
    expect(screen.getByRole("menuitem", { name: "Audit log" })).toHaveAttribute(
      "href",
      "/admin/audit",
    );
  });
});

describe("admin users (UI-20)", () => {
  it("lists accounts with role and status", async () => {
    mockApi({ "GET /me": json(200, admin), "GET /admin/users": json(200, { items: users }) });
    renderRoute("/admin/users");
    await screen.findByRole("listitem", { name: "Bruno Lima" });
    expect(within(row("Bruno Lima")).getByRole("radio", { name: "Member" })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    expect(within(row("carla")).getByText("Disabled")).toBeInTheDocument();
    expect(within(row("Ana Souza")).getByText("You")).toBeInTheDocument();
  });

  it("changes a user's role", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, admin),
      "GET /admin/users": json(200, { items: users }),
      "PATCH /admin/users/u2": json(200, { ...users[1], role: "admin" }),
    });
    const user = userEvent.setup();
    renderRoute("/admin/users");
    await screen.findByRole("listitem", { name: "Bruno Lima" });
    await user.click(within(row("Bruno Lima")).getByRole("radio", { name: "Admin" }));
    await waitFor(() =>
      expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ role: "admin" }),
    );
    expect(calls.find((c) => c.method === "PATCH")?.path).toBe("/admin/users/u2");
    await waitFor(() =>
      expect(within(row("Bruno Lima")).getByRole("radio", { name: "Admin" })).toHaveAttribute(
        "aria-checked",
        "true",
      ),
    );
  });

  it("disables an account after confirmation and enables it directly", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, admin),
      "GET /admin/users": json(200, { items: users }),
      "PATCH /admin/users/u2": json(200, { ...users[1], disabled: true }),
      "PATCH /admin/users/u3": json(200, { ...users[2], disabled: false }),
    });
    const user = userEvent.setup();
    renderRoute("/admin/users");
    await screen.findByRole("listitem", { name: "Bruno Lima" });
    await user.click(within(row("Bruno Lima")).getByRole("switch", { name: /Active/ }));
    const confirm = await screen.findByRole("alertdialog");
    expect(calls.some((c) => c.method === "PATCH")).toBe(false);
    await user.click(within(confirm).getByRole("button", { name: "Disable account" }));
    await waitFor(() =>
      expect(calls.find((c) => c.path === "/admin/users/u2")?.body).toEqual({ disabled: true }),
    );

    await user.click(within(row("carla")).getByRole("switch", { name: /Active/ }));
    await waitFor(() =>
      expect(calls.find((c) => c.path === "/admin/users/u3")?.body).toEqual({ disabled: false }),
    );
  });

  it("does not let admins demote or disable themselves", async () => {
    mockApi({ "GET /me": json(200, admin), "GET /admin/users": json(200, { items: users }) });
    renderRoute("/admin/users");
    await screen.findByRole("listitem", { name: "Ana Souza" });
    expect(within(row("Ana Souza")).getByRole("radio", { name: "Member" })).toBeDisabled();
    expect(within(row("Ana Souza")).getByRole("switch", { name: /Active/ })).toBeDisabled();
  });
});

describe("admin invites (UI-20)", () => {
  const URL = "https://partyline.example.test/invite/Zk3pQ9synthetic";

  it("creates an invite with a role and shows the link once, ready to copy", async () => {
    let items: Invite[] = [used];
    const created: CreatedInvite = { invite: { ...pending, id: "inv3", role: "admin" }, url: URL };
    const { calls } = mockApi({
      "GET /me": json(200, admin),
      "GET /admin/invites": () => json(200, { items }),
      "POST /admin/invites": () => {
        items = [created.invite, used];
        return json(201, created);
      },
    });
    const user = userEvent.setup();
    renderRoute("/admin/invites");
    await user.click(await screen.findByRole("button", { name: "New invite" }));
    const dialog = await screen.findByRole("dialog", { name: "New invite" });
    await user.click(within(dialog).getByRole("radio", { name: /Admin/ }));
    await user.click(within(dialog).getByRole("button", { name: "Create invite" }));

    const shown = await screen.findByRole("dialog", { name: "Send this invite link" });
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ role: "admin" });
    await user.click(within(shown).getByRole("button", { name: "Copy Invite link" }));
    const copied = await navigator.clipboard.readText();
    expect(copied).toBe(URL);
    expect(copied).toMatch(/\/invite\/[^/]+$/);
    await user.click(within(shown).getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.body.textContent).not.toContain("Zk3pQ9synthetic");
  });

  it("says what a role gets in terms of organizations, not of what it owns or is shared with it", async () => {
    mockApi({ "GET /me": json(200, admin), "GET /admin/invites": json(200, { items: [used] }) });
    const user = userEvent.setup();
    renderRoute("/admin/invites");
    await user.click(await screen.findByRole("button", { name: "New invite" }));
    const dialog = await screen.findByRole("dialog", { name: "New invite" });
    expect(
      within(dialog).getByText(
        "Gets an organization of their own and joins the ones they are invited to.",
      ),
    ).toBeInTheDocument();
    expect(dialog).not.toHaveTextContent("shared with them");
    expect(dialog).not.toHaveTextContent("they own");
  });

  it("lists invites with status and revokes a pending one", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, admin),
      "GET /admin/invites": json(200, { items: [pending, used] }),
      "DELETE /admin/invites/inv1": new Response(null, { status: 204 }),
    });
    const user = userEvent.setup();
    renderRoute("/admin/invites");
    const list = await screen.findByRole("list", { name: "Invites" });
    const [first, second] = within(list).getAllByRole("listitem") as HTMLElement[];
    expect(within(first as HTMLElement).getByText("Pending")).toBeInTheDocument();
    expect(within(second as HTMLElement).getByText("Used")).toBeInTheDocument();
    expect(within(second as HTMLElement).getByText(/@bruno/)).toBeInTheDocument();
    expect(within(second as HTMLElement).queryByRole("button", { name: /Revoke/ })).toBeNull();
    await user.click(within(first as HTMLElement).getByRole("button", { name: /Revoke/ }));
    const confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Revoke invite" }));
    await waitFor(() =>
      expect(calls.some((c) => c.method === "DELETE" && c.path === "/admin/invites/inv1")).toBe(
        true,
      ),
    );
  });
});

describe("admin audit log", () => {
  const entry = (n: number): AuditEntry => ({
    id: `e${n}`,
    actor: ana,
    action: n % 2 ? "auth.login" : "token.create",
    target_kind: "user",
    target_id: "u1",
    detail: { kind: "agent" },
    ip: "192.0.2.10",
    created_at: new Date(Date.parse("2026-10-19T12:00:00Z") - n * 60_000).toISOString(),
  });

  it("shows entries newest first and pages with before=<last id>", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, admin),
      "GET /admin/audit": (url) =>
        url.searchParams.get("before") === "e2"
          ? json(200, { items: [entry(3)], has_more: false })
          : json(200, { items: [entry(1), entry(2)], has_more: true }),
    });
    const user = userEvent.setup();
    renderRoute("/admin/audit");
    const list = await screen.findByRole("list", { name: "Audit log" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(2);
    expect(within(list).getAllByText("auth.login")).toHaveLength(1);
    expect(within(list).getAllByText(/@ana/).length).toBeGreaterThan(0);
    await user.click(screen.getByRole("button", { name: "Load older entries" }));
    await waitFor(() => expect(within(list).getAllByRole("listitem")).toHaveLength(3));
    expect(
      calls
        .filter((c) => c.path === "/admin/audit")
        .at(-1)
        ?.url.searchParams.get("before"),
    ).toBe("e2");
    expect(screen.queryByRole("button", { name: "Load older entries" })).toBeNull();
  });
});
