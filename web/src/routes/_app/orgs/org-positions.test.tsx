import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import type { Organization, OrgChart } from "@/lib/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeChartMember,
  makeChartPosition,
  makeIntelligence,
  makeMe,
  makeOrg,
  makeOrgChart,
} from "@/test/fixtures";
import { renderRoute } from "@/test/render";

expect.extend(matchers);

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * The positions editor (T43: POS-01, POS-02, POS-09): the admin's tree of positions, where each
 * can be added under, renamed, moved under another position and deleted. Read through the route,
 * which also sends everyone who is not an admin away.
 */

const AXE_OPTIONS = { rules: { "color-contrast": { enabled: false } } };

const holder = (id: string, name: string) =>
  makeChartMember({ intelligence: makeIntelligence({ id, display_name: name, handle: id }) });

// Admin > Manager > Lead developer > Worktrees, and Sentinel beside Lead developer.
const chart = (over: Partial<OrgChart> = {}): OrgChart =>
  makeOrgChart({
    viewer: { admin: true },
    positions: [
      makeChartPosition({ id: "pos1", name: "Admin", holders: [holder("u1", "Ana")] }),
      makeChartPosition({
        id: "pos2",
        name: "Manager",
        parent_id: "pos1",
        holders: [holder("u2", "Bob"), holder("u3", "Cora")],
      }),
      makeChartPosition({ id: "pos3", name: "Lead developer", parent_id: "pos2" }),
      makeChartPosition({
        id: "pos4",
        name: "Worktrees",
        parent_id: "pos3",
        holders: [holder("a1", "Worker 1")],
      }),
      makeChartPosition({ id: "pos5", name: "Sentinel", parent_id: "pos2" }),
    ],
    ...over,
  });

const acme = (permission: Organization["permission"]) =>
  makeOrg({ id: "o1", name: "Acme", slug: "acme", permission });

function api(
  over: Parameters<typeof mockApi>[0] = {},
  permission: Organization["permission"] = "admin",
  data: OrgChart = chart(),
) {
  return mockApi({
    "GET /me": json(200, makeMe()),
    "GET /orgs": json(200, { items: [acme(permission)] }),
    "GET /orgs/o1": json(200, acme(permission)),
    "GET /orgs/o1/chart": json(200, data),
    "GET /conversations": json(200, { items: [] }),
    ...over,
  });
}

async function open(over: Parameters<typeof mockApi>[0] = {}, data?: OrgChart) {
  const mocks = api(over, "admin", data);
  const user = userEvent.setup();
  const view = renderRoute("/orgs/o1/positions");
  await screen.findByRole("heading", { level: 1, name: "Positions" });
  const list = await screen.findByRole("list", { name: "Positions of Acme" });
  return { ...mocks, ...view, user, list };
}

const parent = (name: string) => screen.getByRole("combobox", { name: `Parent of ${name}` });
const optionsOf = (select: HTMLElement) =>
  within(select)
    .getAllByRole("option")
    .map((o) => o.textContent?.trim());
const item = (list: HTMLElement, name: string) => within(list).getByRole("listitem", { name });
const patches = (calls: { method: string; path: string; body: unknown }[]) =>
  calls.filter((c) => c.method === "PATCH");

beforeEach(() => {
  useLiveStore.setState({ locale: "en", currentOrgId: null });
  window.localStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

describe("the tree of positions (POS-01)", () => {
  it("shows the positions nested under their parents, each with how many hold it", async () => {
    const { list } = await open();
    const admin = item(list, "Admin");
    const manager = within(admin).getByRole("listitem", { name: "Manager" });
    const lead = within(manager).getByRole("listitem", { name: "Lead developer" });
    expect(within(lead).getByRole("listitem", { name: "Worktrees" })).toBeInTheDocument();
    expect(within(manager).getByRole("listitem", { name: "Sentinel" })).toBeInTheDocument();
    // Siblings by name, and a root is not inside another position.
    expect(
      within(manager)
        .getAllByRole("listitem")
        .map((li) => li.getAttribute("aria-label")),
    ).toEqual(["Lead developer", "Worktrees", "Sentinel"]);
    expect(within(list).getAllByRole("listitem")).toHaveLength(5);

    expect(within(admin).getAllByText("1 holder")[0]).toBeInTheDocument();
    expect(within(manager).getAllByText("2 holders")[0]).toBeInTheDocument();
    expect(within(lead).getAllByText("No one holds it")[0]).toBeInTheDocument();
  });

  it("lists several roots side by side", async () => {
    const { list } = await open(
      {},
      makeOrgChart({
        viewer: { admin: true },
        positions: [
          makeChartPosition({ id: "r1", name: "Admin" }),
          makeChartPosition({ id: "r2", name: "Sentinel" }),
        ],
      }),
    );
    expect(
      within(list)
        .getAllByRole("listitem")
        .map((li) => li.getAttribute("aria-label")),
    ).toEqual(["Admin", "Sentinel"]);
  });

  it("invites the admin to add the first position of an organization that has none", async () => {
    api({}, "admin", makeOrgChart({ viewer: { admin: true } }));
    renderRoute("/orgs/o1/positions");
    expect(await screen.findByRole("heading", { name: "No position yet" })).toBeInTheDocument();
    expect(
      screen.getByText("Add the first one: it is the top of Acme’s chart."),
    ).toBeInTheDocument();
  });

  it("says so, and offers to try again, when the positions cannot be read", async () => {
    api({ "GET /orgs/o1/chart": apiError(500, "internal", "boom") });
    renderRoute("/orgs/o1/positions");
    const text = await screen.findByText("Couldn’t load the positions.");
    expect(
      within(text.closest("[role=alert]") as HTMLElement).getByRole("button", {
        name: "Try again",
      }),
    ).toBeInTheDocument();
  });

  it("follows the organization live: a position added by another admin shows without a reload", async () => {
    const { queryClient } = await open();
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /orgs": json(200, { items: [acme("admin")] }),
      "GET /orgs/o1": json(200, acme("admin")),
      "GET /orgs/o1/chart": json(
        200,
        chart({
          positions: [
            ...chart().positions,
            makeChartPosition({ id: "pos9", name: "Designer", parent_id: "pos1" }),
          ],
        }),
      ),
      "GET /conversations": json(200, { items: [] }),
    });
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "access.changed",
        data: { resource_kind: "organization", resource_id: "o1" },
      }),
    );
    expect(await screen.findByRole("listitem", { name: "Designer" })).toBeInTheDocument();
  });
});

describe("only admins edit the chart (OMEM-04)", () => {
  it.each([
    ["member", "member"],
    ["guest", "guest"],
  ] as const)("sends a %s to the members page", async (_, permission) => {
    api({}, permission);
    const { router } = renderRoute("/orgs/o1/positions");
    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs/o1/members"));
    expect(screen.queryByRole("heading", { level: 1, name: "Positions" })).toBeNull();
  });

  it("shows the Positions tab to an admin and to no one else", async () => {
    await open();
    expect(
      within(screen.getByRole("navigation", { name: "Sections of Acme" })).getByRole("link", {
        name: "Positions",
      }),
    ).toHaveAttribute("href", "/orgs/o1/positions");
  });

  it("hides the tab from a member", async () => {
    api({}, "member");
    renderRoute("/orgs/o1/members");
    const bar = await screen.findByRole("navigation", { name: "Sections of Acme" });
    expect(within(bar).queryByRole("link", { name: "Positions" })).toBeNull();
    expect(within(bar).getByRole("link", { name: "Members" })).toBeInTheDocument();
  });
});

describe("moving a position under another (POS-01, POS-02)", () => {
  it("offers a parent select that leaves out the position and everything below it", async () => {
    await open();
    expect(optionsOf(parent("Manager"))).toEqual(["No parent (a root)", "Admin"]);
    expect(optionsOf(parent("Lead developer"))).toEqual([
      "No parent (a root)",
      "Admin",
      "Manager",
      "Sentinel",
    ]);
    expect(optionsOf(parent("Worktrees"))).toEqual([
      "No parent (a root)",
      "Admin",
      "Manager",
      "Lead developer",
      "Sentinel",
    ]);
    expect(optionsOf(parent("Admin"))).toEqual(["No parent (a root)"]);
  });

  it("starts each select on the position's own parent", async () => {
    await open();
    expect(parent("Manager")).toHaveValue("pos1");
    expect(parent("Worktrees")).toHaveValue("pos3");
    expect(parent("Admin")).toHaveValue("");
  });

  it("moves the position when another parent is picked, sending only the parent", async () => {
    const { calls, user } = await open({ "PATCH /orgs/o1/positions/pos4": json(200, {}) });
    await user.selectOptions(parent("Worktrees"), "pos5");
    await waitFor(() => expect(patches(calls)).toHaveLength(1));
    expect(patches(calls)[0]?.path).toBe("/orgs/o1/positions/pos4");
    expect(patches(calls)[0]?.body).toEqual({ parent_id: "pos5" });
    expect(await screen.findByText("Worktrees moved")).toBeInTheDocument();
  });

  it("makes it a root with an empty parent", async () => {
    const { calls, user } = await open({ "PATCH /orgs/o1/positions/pos5": json(200, {}) });
    await user.selectOptions(parent("Sentinel"), "");
    await waitFor(() => expect(patches(calls)).toHaveLength(1));
    expect(patches(calls)[0]?.body).toEqual({ parent_id: "" });
  });

  it("shows the position under its new parent once the server has it", async () => {
    let data = chart();
    const { user, list } = await open({
      "GET /orgs/o1/chart": () => json(200, data),
      "PATCH /orgs/o1/positions/pos5": () => {
        data = chart({
          positions: data.positions.map((p) => (p.id === "pos5" ? { ...p, parent_id: "pos1" } : p)),
        });
        return json(200, {});
      },
    });
    expect(within(item(list, "Manager")).getByRole("listitem", { name: "Sentinel" })).toBeVisible();
    await user.selectOptions(parent("Sentinel"), "pos1");
    await waitFor(() =>
      expect(
        within(item(list, "Manager")).queryByRole("listitem", { name: "Sentinel" }),
      ).toBeNull(),
    );
    expect(within(item(list, "Admin")).getByRole("listitem", { name: "Sentinel" })).toBeVisible();
  });

  it("says a position can't go below itself when the server refuses a cycle (409 position_cycle), and keeps the old parent", async () => {
    const { user } = await open({
      "PATCH /orgs/o1/positions/pos2": apiError(409, "position_cycle", "cycle"),
    });
    await user.selectOptions(parent("Manager"), "");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "A position can’t go below itself or one of its own positions.",
    );
    await waitFor(() => expect(parent("Manager")).toHaveValue("pos1"));
  });

  it("says only admins edit the positions when the server refuses (403), and when it fails otherwise", async () => {
    const { user } = await open({
      "PATCH /orgs/o1/positions/pos4": apiError(403, "forbidden", "forbidden"),
      "PATCH /orgs/o1/positions/pos5": apiError(500, "internal", "boom"),
    });
    await user.selectOptions(parent("Worktrees"), "pos5");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Only admins of Acme edit the positions.",
    );
    await user.selectOptions(parent("Sentinel"), "pos1");
    expect(await screen.findByText("Couldn’t save the change. Try again.")).toBeInTheDocument();
  });
});

describe("naming a position (POS-01)", () => {
  async function adding(parentName?: string) {
    const mocks = await open({
      "POST /orgs/o1/positions": json(201, { id: "pos9", name: "Designer" }),
    });
    await mocks.user.click(
      parentName
        ? screen.getByRole("button", { name: `Add a position under ${parentName}` })
        : screen.getByRole("button", { name: "Add position" }),
    );
    const field = await screen.findByRole("textbox", { name: "Name of the new position" });
    return { ...mocks, field };
  }
  const save = () => screen.getByRole("button", { name: "Save" });

  it("adds a root position with a name, sending no parent", async () => {
    const { calls, user, field } = await adding();
    await user.type(field, "  Designer ");
    await user.click(save());
    expect(await screen.findByText("Position Designer added")).toBeInTheDocument();
    const post = calls.find((c) => c.method === "POST");
    expect(post?.path).toBe("/orgs/o1/positions");
    expect(post?.body).toEqual({ name: "Designer" });
  });

  it("adds a position under another one, sending the parent", async () => {
    const { calls, user, field } = await adding("Manager");
    await user.type(field, "Designer");
    await user.click(save());
    await screen.findByText("Position Designer added");
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      name: "Designer",
      parent_id: "pos2",
    });
  });

  it("refuses a name of 61 characters before asking the server, and accepts one of 60", async () => {
    const { calls, user, field } = await adding();
    await user.click(field);
    await user.paste("x".repeat(61));
    expect(screen.getByText("1 to 60 characters")).toBeInTheDocument();
    expect(save()).toBeDisabled();
    await user.keyboard("{Enter}");
    expect(calls.some((c) => c.method === "POST")).toBe(false);

    await user.clear(field);
    await user.paste("x".repeat(60));
    expect(screen.queryByText("1 to 60 characters")).toBeNull();
    await user.click(save());
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ name: "x".repeat(60) });
  });

  it("counts characters and not bytes: 60 accented letters are fine", async () => {
    const { calls, user, field } = await adding();
    await user.click(field);
    await user.paste("é".repeat(60));
    expect(screen.queryByText("1 to 60 characters")).toBeNull();
    await user.click(save());
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
  });

  it("refuses a blank name", async () => {
    const { calls, user, field } = await adding();
    await user.type(field, "   ");
    expect(screen.getByText("1 to 60 characters")).toBeInTheDocument();
    expect(save()).toBeDisabled();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });

  it("says a name that exists already is taken (409 conflict) and keeps the field open", async () => {
    const mocks = await open({
      "POST /orgs/o1/positions": apiError(409, "conflict", "name taken"),
    });
    await mocks.user.click(screen.getByRole("button", { name: "Add position" }));
    await mocks.user.type(
      await screen.findByRole("textbox", { name: "Name of the new position" }),
      "manager",
    );
    await mocks.user.click(save());
    expect(
      await screen.findByText("A position with that name already exists."),
    ).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Name of the new position" })).toHaveValue(
      "manager",
    );
  });

  it("shows the server's reason for a name it refuses (422)", async () => {
    const mocks = await open({
      "POST /orgs/o1/positions": apiError(422, "invalid", "invalid", {
        parent_id: "unknown parent",
      }),
    });
    await mocks.user.click(screen.getByRole("button", { name: "Add a position under Manager" }));
    await mocks.user.type(
      await screen.findByRole("textbox", { name: "Name of the new position" }),
      "Designer",
    );
    await mocks.user.click(save());
    expect(await screen.findByText("unknown parent")).toBeInTheDocument();
  });

  it("gives up with Escape or Cancel and sends nothing", async () => {
    const { calls, user, field } = await adding();
    await user.type(field, "Designer");
    await user.keyboard("{Escape}");
    await waitFor(() =>
      expect(screen.queryByRole("textbox", { name: "Name of the new position" })).toBeNull(),
    );
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });

  describe("renaming", () => {
    async function renaming(name = "Manager", over: Parameters<typeof mockApi>[0] = {}) {
      const mocks = await open({
        [`PATCH /orgs/o1/positions/${name === "Manager" ? "pos2" : "pos4"}`]: json(200, {}),
        ...over,
      });
      await mocks.user.click(screen.getByRole("button", { name: `Rename ${name}` }));
      const field = await screen.findByRole("textbox", { name: `New name for ${name}` });
      return { ...mocks, field };
    }

    it("starts on the current name and sends the new one", async () => {
      const { calls, user, field } = await renaming();
      expect(field).toHaveValue("Manager");
      await user.clear(field);
      await user.type(field, "Director");
      await user.click(save());
      expect(await screen.findByText("Renamed to Director")).toBeInTheDocument();
      expect(patches(calls)[0]?.path).toBe("/orgs/o1/positions/pos2");
      expect(patches(calls)[0]?.body).toEqual({ name: "Director" });
    });

    it("sends nothing when the name is not changed", async () => {
      const { calls, user } = await renaming();
      await user.click(save());
      await waitFor(() =>
        expect(screen.queryByRole("textbox", { name: "New name for Manager" })).toBeNull(),
      );
      expect(patches(calls)).toHaveLength(0);
    });

    it("refuses 61 characters before asking the server", async () => {
      const { calls, user, field } = await renaming();
      await user.clear(field);
      await user.paste("x".repeat(61));
      expect(screen.getByText("1 to 60 characters")).toBeInTheDocument();
      expect(save()).toBeDisabled();
      expect(patches(calls)).toHaveLength(0);
    });

    it("says the name is taken (409 conflict)", async () => {
      const { user, field } = await renaming("Worktrees", {
        "PATCH /orgs/o1/positions/pos4": apiError(409, "conflict", "name taken"),
      });
      await user.clear(field);
      await user.type(field, "Sentinel");
      await user.click(save());
      expect(
        await screen.findByText("A position with that name already exists."),
      ).toBeInTheDocument();
    });
  });
});

describe("deleting a position (POS-09)", () => {
  async function deleting(name: string, over: Parameters<typeof mockApi>[0] = {}) {
    const mocks = await open(over);
    await mocks.user.click(screen.getByRole("button", { name: `Delete ${name}` }));
    const dialog = await screen.findByRole("alertdialog", { name: `Delete ${name}?` });
    return { ...mocks, dialog };
  }
  const confirm = (dialog: HTMLElement) =>
    within(dialog).getByRole("button", { name: "Delete position" });

  it("says where its holders and child positions go, to the parent by name", async () => {
    const { dialog } = await deleting("Lead developer");
    expect(
      within(dialog).getByText(
        "Its holders and its child positions move to Manager. Nobody is removed from the organization.",
      ),
    ).toBeInTheDocument();
  });

  it("says what happens to a root: its holders hold none and its children become roots", async () => {
    const { dialog } = await deleting("Admin");
    expect(
      within(dialog).getByText(
        "It has no parent, so its holders are left with no position and its child positions become roots. Nobody is removed from the organization.",
      ),
    ).toBeInTheDocument();
  });

  it("deletes it, says so, and shows the chart the server has afterwards", async () => {
    let data = chart();
    const { calls, user, dialog, list } = await deleting("Lead developer", {
      "GET /orgs/o1/chart": () => json(200, data),
      "DELETE /orgs/o1/positions/pos3": () => {
        // The holders and children moved to the parent.
        data = chart({
          positions: data.positions
            .filter((p) => p.id !== "pos3")
            .map((p) => (p.parent_id === "pos3" ? { ...p, parent_id: "pos2" } : p)),
        });
        return new Response(null, { status: 204 });
      },
    });
    await user.click(confirm(dialog));
    expect(await screen.findByText("Position Lead developer deleted")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "DELETE").map((c) => c.path)).toEqual([
      "/orgs/o1/positions/pos3",
    ]);
    await waitFor(() =>
      expect(screen.queryByRole("listitem", { name: "Lead developer" })).toBeNull(),
    );
    expect(
      within(item(list, "Manager")).getByRole("listitem", { name: "Worktrees" }),
    ).toBeVisible();
  });

  it("says only admins delete positions when the server refuses (403), and when it fails otherwise, and stays open", async () => {
    const { user, dialog } = await deleting("Sentinel", {
      "DELETE /orgs/o1/positions/pos5": apiError(403, "forbidden", "forbidden"),
    });
    await user.click(confirm(dialog));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Only admins of Acme edit the positions.",
    );
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
  });

  it("cancels without asking the server", async () => {
    const { calls, user, dialog } = await deleting("Sentinel");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  });
});

describe("in Portuguese and for assistive technology", () => {
  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    api({ "GET /me": json(200, makeMe({ locale: "pt-BR" })) });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/positions");
    expect(await screen.findByRole("heading", { level: 1, name: "Cargos" })).toBeInTheDocument();
    expect(await screen.findByRole("list", { name: "Cargos de Acme" })).toBeInTheDocument();
    expect(optionsOf(screen.getByRole("combobox", { name: "Cargo acima de Manager" }))).toEqual([
      "Nenhum (cargo do topo)",
      "Admin",
    ]);
    await user.click(screen.getByRole("button", { name: "Excluir Lead developer" }));
    expect(
      await screen.findByText(
        "Quem ocupa o cargo e os cargos abaixo dele passam para Manager. Ninguém sai da organização.",
      ),
    ).toBeInTheDocument();
  });

  it("has no axe violations: the tree, the editors and the dialog, in both locales", async () => {
    const { user, container } = await open();
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
    await user.click(screen.getByRole("button", { name: "Rename Manager" }));
    await screen.findByRole("textbox", { name: "New name for Manager" });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: "Add a position under Manager" }));
    await screen.findByRole("textbox", { name: "Name of the new position" });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: "Delete Sentinel" }));
    expect(await axe(await screen.findByRole("alertdialog"), AXE_OPTIONS)).toHaveNoViolations();
  });

  it("has no axe violations in Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    api({ "GET /me": json(200, makeMe({ locale: "pt-BR" })) });
    const { container } = renderRoute("/orgs/o1/positions");
    await screen.findByRole("list", { name: "Cargos de Acme" });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });
});
