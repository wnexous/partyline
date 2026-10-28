import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import type { ChartMember, IntelligenceRef, Organization, OrgChart } from "@/shared/api/types.gen";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeAI,
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
 * The org chart page (T44: CHART-01, CHART-02, CHART-03, CHART-04): the positions as a tree with
 * who holds each, guests under whoever invited them, the others under their creator or under
 * "Unassigned", and the actions the server's flags allow. Read through the route, which also decides
 * what a non-member sees.
 */

const AXE_OPTIONS = { rules: { "color-contrast": { enabled: false } } };

const ana = makeIntelligence();
const dana = makeIntelligence({ id: "u2", display_name: "Dana Reis", handle: "dana" });
const bob = makeIntelligence({ id: "u3", display_name: "Bob Lima", handle: "bob" });
const gus = makeIntelligence({ id: "u4", display_name: "Gus Reis", handle: "gus" });
const eli = makeIntelligence({ id: "u5", display_name: "Eli Costa", handle: "eli" });
const lead = makeAI({ id: "a1", display_name: "Lead", handle: "lead" });
const w1 = makeAI({ id: "a2", display_name: "Worker 1", handle: "w1" });
const w2 = makeAI({ id: "a3", display_name: "Worker 2", handle: "w2" });
const w3 = makeAI({ id: "a4", display_name: "Worker 3", handle: "w3" });
const w4 = makeAI({ id: "a5", display_name: "Worker 4", handle: "w4" });
const sentinel = makeAI({ id: "a6", display_name: "Sentinel", handle: "sentinel" });

type Flags = Partial<Pick<ChartMember, "can_delete" | "can_move">>;
const member = (
  who: IntelligenceRef,
  permission: ChartMember["permission"] = "member",
  over: Partial<ChartMember> & Flags = {},
): ChartMember =>
  makeChartMember({
    intelligence: who,
    permission,
    delete_effect: who.kind === "ai" ? "delete" : "remove",
    ...over,
  });

/**
 * The spec's chart: Admin > Director > Manager > Lead developer > Worktrees, with Security beside
 * Lead developer. Ana holds Admin, Dana Director, Bob Manager, Lead (an AI) Lead developer, three
 * guest workers Worktrees and Sentinel (an AI) Security. Gus, a guest, hangs under Bob who invited
 * him; Worker 4, a guest with no position, under Lead who started it; Eli has no place.
 * `flags` says what the viewer may do about each member by id.
 */
function chartFor(flags: Record<string, Flags> = {}, admin = false): OrgChart {
  const m = (who: IntelligenceRef, permission: ChartMember["permission"] = "member", over = {}) =>
    member(who, permission, { ...flags[who.id], ...over });
  return makeOrgChart({
    viewer: { admin },
    positions: [
      makeChartPosition({ id: "pos1", name: "Admin", holders: [m(ana, "admin")] }),
      makeChartPosition({ id: "pos2", name: "Director", parent_id: "pos1", holders: [m(dana)] }),
      makeChartPosition({ id: "pos3", name: "Manager", parent_id: "pos2", holders: [m(bob)] }),
      makeChartPosition({
        id: "pos4",
        name: "Lead developer",
        parent_id: "pos3",
        holders: [m(lead, "member", { online: true })],
      }),
      makeChartPosition({
        id: "pos5",
        name: "Worktrees",
        parent_id: "pos4",
        holders: [m(w1, "guest"), m(w2, "guest"), m(w3, "guest")],
      }),
      makeChartPosition({
        id: "pos6",
        name: "Security",
        parent_id: "pos3",
        holders: [m(sentinel)],
      }),
    ],
    members: [
      { ...m(gus, "guest"), under: "u3" },
      { ...m(w4, "guest"), under: "a1" },
    ],
    unassigned: [m(eli)],
  });
}

const MAY = { can_delete: true, can_move: true } as const;
/** An admin may delete and move everyone but themselves. */
const adminChart = chartFor(
  Object.fromEntries([dana, bob, gus, eli, lead, w1, w2, w3, w4, sentinel].map((w) => [w.id, MAY])),
  true,
);
/**
 * Lead answers for the guest workers it started, which it may move and delete, and manages Sentinel,
 * a member: it may move it but not delete it (only a guest is deleted by whoever manages it).
 */
const leadChart = chartFor({ a2: MAY, a3: MAY, a4: MAY, a5: MAY, a6: { can_move: true } });
/** Bob answers for the guest he invited. */
const inviterChart = chartFor({ u4: MAY });
/** Dana manages Bob, a member below her: she may move him but not remove him (only a guest is removed by whoever manages it). */
const directorChart = chartFor({ u3: { can_move: true } });
/** An inviter with no position may delete the guest it invited but has no position to move it to. */
const inviterWithoutPosition = chartFor({ u4: { can_delete: true } });
const plainChart = chartFor();

const acmeAs = (permission: Organization["permission"]) =>
  makeOrg({ id: "o1", name: "Acme", slug: "acme", permission });

function api(
  data: OrgChart = plainChart,
  permission: Organization["permission"] = "member",
  over: Parameters<typeof mockApi>[0] = {},
) {
  return mockApi({
    "GET /me": json(200, makeMe()),
    "GET /orgs": json(200, { items: [acmeAs(permission)] }),
    "GET /orgs/o1": json(200, acmeAs(permission)),
    "GET /orgs/o1/chart": json(200, data),
    "GET /conversations": json(200, { items: [] }),
    ...over,
  });
}

async function open(
  data: OrgChart = plainChart,
  permission: Organization["permission"] = "member",
  over: Parameters<typeof mockApi>[0] = {},
) {
  const mocks = api(data, permission, over);
  const user = userEvent.setup();
  const view = renderRoute("/orgs/o1/chart");
  await screen.findByRole("heading", { level: 1, name: "Org chart" });
  const tree = await screen.findByRole("tree", { name: "Org chart of Acme" });
  return { ...mocks, ...view, user, tree };
}

const treeitem = (name: string) => screen.getByRole("treeitem", { name });
/** The row of a treeitem: the line that names it, not the items nested inside. */
const rowOf = (name: string) => treeitem(name).firstElementChild as HTMLElement;
const names = (tree: HTMLElement) =>
  within(tree)
    .getAllByRole("treeitem")
    .map(
      (i) =>
        i.getAttribute("aria-labelledby") &&
        document.getElementById(i.getAttribute("aria-labelledby") ?? "")?.textContent,
    );
const focusedName = () => {
  const el = document.activeElement as HTMLElement | null;
  return el?.getAttribute("role") === "treeitem"
    ? document.getElementById(el.getAttribute("aria-labelledby") ?? "")?.textContent
    : null;
};
/** Puts the focus on an item the way a Tab or a click would, letting React see it. */
const focusItem = (name: string) => act(() => treeitem(name).focus());
const buttonsOf = (row: HTMLElement) =>
  within(row)
    .queryAllByRole("button")
    .map((b) => b.getAttribute("aria-label"))
    .filter((l) => l && /^(Move|Delete|Remove) /.test(l));

// Every visible item of the default chart, in tree order.
const ORDER = [
  "Admin",
  "Ana Souza",
  "Director",
  "Dana Reis",
  "Manager",
  "Bob Lima",
  "Gus Reis",
  "Lead developer",
  "Lead",
  "Worker 4",
  "Worktrees",
  "Worker 1",
  "Worker 2",
  "Worker 3",
  "Security",
  "Sentinel",
  "Unassigned",
  "Eli Costa",
];

beforeEach(() => {
  useLiveStore.setState({ locale: "en", currentOrgId: null, presence: {} });
  window.localStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

describe("the tree and its fields (CHART-01)", () => {
  it("draws the positions with their holders, guests and the unassigned as one tree", async () => {
    const { tree } = await open();
    expect(names(tree)).toEqual(ORDER);
    const levels = within(tree)
      .getAllByRole("treeitem")
      .map((i) => Number(i.getAttribute("aria-level")));
    expect(levels).toEqual([1, 2, 2, 3, 3, 4, 5, 4, 5, 6, 5, 6, 6, 6, 4, 5, 1, 2]);
  });

  it("puts a guest inside the item of the member who invited it, and an AI inside its creator's", async () => {
    await open();
    expect(within(treeitem("Bob Lima")).getByRole("treeitem", { name: "Gus Reis" })).toBeVisible();
    expect(within(treeitem("Lead")).getByRole("treeitem", { name: "Worker 4" })).toBeVisible();
    // Not anywhere else.
    expect(
      within(treeitem("Director")).getAllByRole("treeitem", { name: "Gus Reis" }),
    ).toHaveLength(1);
    expect(within(treeitem("Worktrees")).queryByRole("treeitem", { name: "Worker 4" })).toBeNull();
  });

  it("puts a member with no place under Unassigned", async () => {
    await open();
    expect(
      within(treeitem("Unassigned")).getByRole("treeitem", { name: "Eli Costa" }),
    ).toBeVisible();
  });

  it("has no Unassigned group when everyone has a place", async () => {
    await open({ ...plainChart, unassigned: [] });
    expect(screen.queryByRole("treeitem", { name: "Unassigned" })).toBeNull();
  });

  it("shows each holder's picture, name, @handle and part, and the online dot of an AI, with no kind badge (AD-024)", async () => {
    await open();
    const leadRow = rowOf("Lead");
    expect(within(leadRow).getByTestId("intelligence-avatar")).toHaveTextContent("L");
    expect(within(leadRow).getByText("@lead")).toBeInTheDocument();
    expect(within(leadRow).queryByText(/^(AI|IA)$/)).toBeNull();
    expect(within(leadRow).getByText("Online")).toBeInTheDocument();

    const anaRow = rowOf("Ana Souza");
    expect(within(anaRow).getByText("@ana")).toBeInTheDocument();
    expect(within(anaRow).queryByText(/^(Person|Pessoa)$/)).toBeNull();
    expect(within(anaRow).getByText("Admin")).toBeInTheDocument();
    // A person has no presence: no dot.
    expect(within(anaRow).queryByText("Online")).toBeNull();
    expect(within(anaRow).queryByText("Offline")).toBeNull();

    const worker = rowOf("Worker 1");
    expect(within(worker).getByText("Guest")).toBeInTheDocument();
    expect(within(worker).getByText("Offline")).toBeInTheDocument();
    // A plain member carries no permission badge.
    expect(within(rowOf("Dana Reis")).queryByText("Member")).toBeNull();
  });

  it("marks the viewer", async () => {
    await open();
    expect(within(rowOf("Ana Souza")).getByText("You")).toBeInTheDocument();
  });

  it("shows how many hold each position", async () => {
    await open();
    expect(within(rowOf("Worktrees")).getByText("3 holders")).toBeInTheDocument();
    expect(within(rowOf("Admin")).getByText("1 holder")).toBeInTheDocument();
  });

  it("follows the live presence of an AI over what the chart said", async () => {
    await open();
    expect(within(rowOf("Lead")).getByText("Online")).toBeInTheDocument();
    act(() => useLiveStore.getState().setPresence("a1", { online: false }));
    expect(await within(rowOf("Lead")).findByText("Offline")).toBeInTheDocument();
    act(() => useLiveStore.getState().setPresence("a2", { online: true }));
    expect(await within(rowOf("Worker 1")).findByText("Online")).toBeInTheDocument();
  });

  it("links the name of a member to their profile", async () => {
    await open();
    expect(within(rowOf("Bob Lima")).getByRole("link", { name: "Bob Lima" })).toHaveAttribute(
      "href",
      "/contacts/u3",
    );
  });

  it("describes each item for assistive technology with the handle and the part, and no kind", async () => {
    await open();
    expect(treeitem("Worker 1")).toHaveAccessibleDescription(/@w1.*Guest/);
    expect(treeitem("Ana Souza")).toHaveAccessibleDescription(/@ana.*Admin/);
    for (const name of ["Worker 1", "Ana Souza"]) {
      expect(treeitem(name)).not.toHaveAccessibleDescription(/\b(AI|Person)\b/);
    }
  });

  it("numbers the items among their siblings", async () => {
    await open();
    expect(treeitem("Worker 2")).toHaveAttribute("aria-posinset", "2");
    expect(treeitem("Worker 2")).toHaveAttribute("aria-setsize", "3");
    // The roots: the position tree, then Unassigned.
    expect(treeitem("Admin")).toHaveAttribute("aria-posinset", "1");
    expect(treeitem("Admin")).toHaveAttribute("aria-setsize", "2");
    expect(treeitem("Unassigned")).toHaveAttribute("aria-posinset", "2");
  });

  it("marks only the items that hold others as expandable, and starts with them open", async () => {
    await open();
    expect(treeitem("Admin")).toHaveAttribute("aria-expanded", "true");
    expect(treeitem("Bob Lima")).toHaveAttribute("aria-expanded", "true");
    expect(treeitem("Worker 1")).not.toHaveAttribute("aria-expanded");
    expect(treeitem("Gus Reis")).not.toHaveAttribute("aria-expanded");
  });

  it("is refreshed live when the organization changes", async () => {
    const { queryClient } = await open();
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /orgs": json(200, { items: [acmeAs("member")] }),
      "GET /orgs/o1": json(200, acmeAs("member")),
      "GET /orgs/o1/chart": json(200, { ...plainChart, unassigned: [] }),
      "GET /conversations": json(200, { items: [] }),
    });
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "access.changed",
        data: { resource_kind: "organization", resource_id: "o1" },
      }),
    );
    await waitFor(() => expect(screen.queryByRole("treeitem", { name: "Eli Costa" })).toBeNull());
  });
});

describe("the keyboard (CHART-04)", () => {
  it("makes one item the tab stop, the first, and every other item reachable by the arrows", async () => {
    const { tree } = await open();
    const stops = within(tree)
      .getAllByRole("treeitem")
      .filter((i) => i.getAttribute("tabindex") === "0");
    expect(stops).toHaveLength(1);
    expect(stops[0]).toBe(treeitem("Admin"));
  });

  it("takes the tab stop into the tree with Tab and walks down and up with the arrows", async () => {
    const { user } = await open();
    await user.tab();
    // The skip link, the page's own controls and the sidebar come first: focus the stop directly.
    focusItem("Admin");
    expect(focusedName()).toBe("Admin");
    await user.keyboard("{ArrowDown}");
    expect(focusedName()).toBe("Ana Souza");
    await user.keyboard("{ArrowDown}{ArrowDown}");
    expect(focusedName()).toBe("Dana Reis");
    await user.keyboard("{ArrowUp}");
    expect(focusedName()).toBe("Director");
    // The tab stop follows the focus.
    expect(treeitem("Director")).toHaveAttribute("tabindex", "0");
    expect(treeitem("Admin")).toHaveAttribute("tabindex", "-1");
  });

  it("goes to the first and the last visible item with Home and End", async () => {
    const { user } = await open();
    focusItem("Admin");
    await user.keyboard("{End}");
    expect(focusedName()).toBe("Eli Costa");
    await user.keyboard("{Home}");
    expect(focusedName()).toBe("Admin");
  });

  it("does not go past the ends", async () => {
    const { user } = await open();
    focusItem("Admin");
    await user.keyboard("{ArrowUp}");
    expect(focusedName()).toBe("Admin");
    await user.keyboard("{End}{ArrowDown}");
    expect(focusedName()).toBe("Eli Costa");
  });

  it("closes an open item with ArrowLeft and opens it with ArrowRight, hiding and showing what it holds", async () => {
    const { user } = await open();
    focusItem("Worktrees");
    expect(screen.getByRole("treeitem", { name: "Worker 1" })).toBeVisible();
    await user.keyboard("{ArrowLeft}");
    expect(treeitem("Worktrees")).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("treeitem", { name: "Worker 1" })).toBeNull();
    expect(focusedName()).toBe("Worktrees");
    // The items after it are still there, and Down skips what is hidden.
    await user.keyboard("{ArrowDown}");
    expect(focusedName()).toBe("Security");
    await user.keyboard("{ArrowUp}{ArrowRight}");
    expect(treeitem("Worktrees")).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("treeitem", { name: "Worker 1" })).toBeVisible();
    expect(focusedName()).toBe("Worktrees");
  });

  it("goes into an open item with ArrowRight and back to its parent with ArrowLeft", async () => {
    const { user } = await open();
    focusItem("Worktrees");
    await user.keyboard("{ArrowRight}");
    expect(focusedName()).toBe("Worker 1");
    await user.keyboard("{ArrowLeft}");
    expect(focusedName()).toBe("Worktrees");
  });

  it("does nothing on ArrowRight at an item that holds nothing, and goes to the parent on ArrowLeft", async () => {
    const { user } = await open();
    focusItem("Worker 2");
    await user.keyboard("{ArrowRight}");
    expect(focusedName()).toBe("Worker 2");
    await user.keyboard("{ArrowLeft}");
    expect(focusedName()).toBe("Worktrees");
    focusItem("Admin");
    await user.keyboard("{ArrowLeft}{ArrowLeft}");
    // A root closed already has no parent to go to; the first press closes it.
    expect(treeitem("Admin")).toHaveAttribute("aria-expanded", "false");
    expect(focusedName()).toBe("Admin");
  });

  it("toggles an item with Enter and with Space", async () => {
    const { user } = await open();
    focusItem("Manager");
    await user.keyboard("{Enter}");
    expect(treeitem("Manager")).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("treeitem", { name: "Bob Lima" })).toBeNull();
    await user.keyboard("{Enter}");
    expect(treeitem("Manager")).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("treeitem", { name: "Bob Lima" })).toBeVisible();
    await user.keyboard(" ");
    expect(treeitem("Manager")).toHaveAttribute("aria-expanded", "false");
  });

  it("opens the profile of a member that holds nothing with Enter", async () => {
    const { user, router } = await open();
    focusItem("Gus Reis");
    await user.keyboard("{Enter}");
    await waitFor(() => expect(router.state.location.pathname).toBe("/contacts/u4"));
  });

  it("keeps the focus on the closed item when the focused one was inside it", async () => {
    const { user } = await open();
    focusItem("Worker 1");
    await user.keyboard("{ArrowLeft}{ArrowLeft}");
    expect(focusedName()).toBe("Worktrees");
    expect(treeitem("Worktrees")).toHaveAttribute("aria-expanded", "false");
    expect(treeitem("Worktrees")).toHaveAttribute("tabindex", "0");
  });

  it("falls back to the first item when the tab stop disappears from the chart", async () => {
    const { queryClient } = await open();
    focusItem("Eli Costa");
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /orgs": json(200, { items: [acmeAs("member")] }),
      "GET /orgs/o1": json(200, acmeAs("member")),
      "GET /orgs/o1/chart": json(200, { ...plainChart, unassigned: [] }),
      "GET /conversations": json(200, { items: [] }),
    });
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "access.changed",
        data: { resource_kind: "organization", resource_id: "o1" },
      }),
    );
    await waitFor(() => expect(screen.queryByRole("treeitem", { name: "Eli Costa" })).toBeNull());
    expect(treeitem("Admin")).toHaveAttribute("tabindex", "0");
  });

  it("makes the item that is clicked the tab stop", async () => {
    const { user } = await open();
    await user.click(rowOf("Dana Reis"));
    expect(treeitem("Dana Reis")).toHaveAttribute("tabindex", "0");
    expect(treeitem("Admin")).toHaveAttribute("tabindex", "-1");
  });

  it("opens and closes an item with the click of its arrow", async () => {
    const { user } = await open();
    await user.click(screen.getByRole("button", { name: "Collapse Worktrees" }));
    expect(treeitem("Worktrees")).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("treeitem", { name: "Worker 1" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Expand Worktrees" }));
    expect(screen.getByRole("treeitem", { name: "Worker 1" })).toBeVisible();
  });

  it("leaves the keys of a button inside an item to the button", async () => {
    const { user } = await open(adminChart, "admin");
    const move = screen.getByRole("button", { name: "Move Worker 1" });
    move.focus();
    await user.keyboard("{Enter}");
    expect(await screen.findByRole("dialog", { name: "Move Worker 1" })).toBeInTheDocument();
    // The item behind it was neither toggled nor moved.
    expect(screen.queryByRole("treeitem", { name: "Worker 1" })).toBeNull();
  });

  it("reaches the actions of the focused item with Tab and not those of the others", async () => {
    const { user } = await open(adminChart, "admin");
    focusItem("Worker 1");
    expect(screen.getByRole("button", { name: "Move Worker 1" })).toHaveAttribute("tabindex", "0");
    expect(screen.getByRole("button", { name: "Move Worker 2" })).toHaveAttribute("tabindex", "-1");
    await user.tab();
    expect(document.activeElement).toBe(screen.getByRole("link", { name: "Worker 1" }));
    await user.tab();
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Move Worker 1" }));
  });
});

describe("what the viewer may do about each member (CHART-03)", () => {
  it("offers a plain member no action at all, and no way to edit the positions", async () => {
    const { tree } = await open();
    expect(within(tree).queryByRole("button", { name: /^(Move|Delete|Remove) / })).toBeNull();
    expect(screen.queryByRole("link", { name: "Edit positions" })).toBeNull();
  });

  it("offers Lead Delete on the guest workers it started and on no one else, with Move beside it", async () => {
    await open(leadChart);
    for (const w of ["Worker 1", "Worker 2", "Worker 3", "Worker 4"]) {
      expect(buttonsOf(rowOf(w))).toEqual([`Move ${w}`, `Delete ${w}`]);
    }
    for (const other of ["Dana Reis", "Bob Lima", "Gus Reis", "Eli Costa", "Ana Souza"]) {
      expect(buttonsOf(rowOf(other))).toEqual([]);
    }
    // Sentinel is a member: Lead may move it but not delete it.
    expect(buttonsOf(rowOf("Sentinel"))).toEqual(["Move Sentinel"]);
    // A position has none of its own.
    expect(buttonsOf(rowOf("Worktrees"))).toEqual([]);
  });

  it("offers a manager Move but not Remove on a member below them: only a guest is removed by whoever manages it", async () => {
    await open(directorChart);
    expect(buttonsOf(rowOf("Bob Lima"))).toEqual(["Move Bob Lima"]);
  });

  it("offers an inviter who holds no position Remove on the guest but no Move, which needs a position", async () => {
    await open(inviterWithoutPosition);
    expect(buttonsOf(rowOf("Gus Reis"))).toEqual(["Remove Gus Reis from Acme"]);
  });

  it("offers an inviter Remove on the guest it invited and nothing on anyone else", async () => {
    await open(inviterChart);
    expect(buttonsOf(rowOf("Gus Reis"))).toEqual(["Move Gus Reis", "Remove Gus Reis from Acme"]);
    for (const other of ["Worker 1", "Sentinel", "Dana Reis", "Eli Costa"]) {
      expect(buttonsOf(rowOf(other))).toEqual([]);
    }
  });

  it("offers an admin Remove on a person and Delete on an AI, Move on both, and nothing on themselves", async () => {
    await open(adminChart, "admin");
    expect(buttonsOf(rowOf("Dana Reis"))).toEqual(["Move Dana Reis", "Remove Dana Reis from Acme"]);
    expect(buttonsOf(rowOf("Eli Costa"))).toEqual(["Move Eli Costa", "Remove Eli Costa from Acme"]);
    expect(buttonsOf(rowOf("Sentinel"))).toEqual(["Move Sentinel", "Delete Sentinel"]);
    expect(buttonsOf(rowOf("Lead"))).toEqual(["Move Lead", "Delete Lead"]);
    expect(buttonsOf(rowOf("Ana Souza"))).toEqual([]);
    // The visible words are short; the names say who.
    expect(
      within(rowOf("Dana Reis")).getByRole("button", { name: /^Remove Dana Reis/ }),
    ).toHaveTextContent("Remove");
  });

  it("shows 'Edit positions' to an admin only, linking to the editor", async () => {
    await open(adminChart, "admin");
    expect(screen.getByRole("link", { name: "Edit positions" })).toHaveAttribute(
      "href",
      "/orgs/o1/positions",
    );
  });

  it("shows no 'Edit positions' to a member whose chart says they are no admin", async () => {
    await open(leadChart, "member");
    expect(screen.queryByRole("link", { name: "Edit positions" })).toBeNull();
  });

  it("removes a person from the organization after asking, and the chart no longer shows them", async () => {
    let data = adminChart;
    const { calls, user } = await open(adminChart, "admin", {
      "GET /orgs/o1/chart": () => json(200, data),
      "DELETE /orgs/o1/members/u5": () => {
        data = { ...adminChart, unassigned: [] };
        return new Response(null, { status: 204 });
      },
    });
    await user.click(screen.getByRole("button", { name: "Remove Eli Costa from Acme" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Remove Eli Costa from Acme?" });
    expect(
      within(dialog).getByText(
        "Eli Costa leaves the groups of Acme and loses their position there. Removing someone only takes them out of Acme: their account and their other organizations stay.",
      ),
    ).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Remove from Acme" }));
    await waitFor(() => expect(screen.queryByRole("treeitem", { name: "Eli Costa" })).toBeNull());
    expect(calls.filter((c) => c.method === "DELETE").map((c) => c.path)).toEqual([
      "/orgs/o1/members/u5",
    ]);
  });

  it("deletes an AI of the organization after asking, and the chart no longer shows it", async () => {
    let data = leadChart;
    const { calls, user } = await open(leadChart, "member", {
      "GET /orgs/o1/chart": () => json(200, data),
      "DELETE /agents/a2": () => {
        data = {
          ...leadChart,
          positions: leadChart.positions.map((p) =>
            p.id === "pos5"
              ? { ...p, holders: p.holders.filter((h) => h.intelligence.id !== "a2") }
              : p,
          ),
        };
        return new Response(null, { status: 204 });
      },
    });
    await user.click(screen.getByRole("button", { name: "Delete Worker 1" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Delete Worker 1?" });
    await user.click(within(dialog).getByRole("button", { name: "Delete AI" }));
    await waitFor(() => expect(screen.queryByRole("treeitem", { name: "Worker 1" })).toBeNull());
    expect(calls.filter((c) => c.method === "DELETE").map((c) => c.path)).toEqual(["/agents/a2"]);
  });

  it("moves a member to another position from the chart", async () => {
    const { calls, user } = await open(adminChart, "admin", {
      "PATCH /orgs/o1/members/u4": json(200, {}),
    });
    await user.click(screen.getByRole("button", { name: "Move Gus Reis" }));
    const dialog = await screen.findByRole("dialog", { name: "Move Gus Reis" });
    const select = within(dialog).getByRole("combobox", { name: "Position" });
    expect(
      within(select)
        .getAllByRole("option")
        .map((o) => o.textContent?.trim()),
    ).toEqual([
      "No position",
      "Admin",
      "Director",
      "Manager",
      "Lead developer",
      "Worktrees",
      "Security",
    ]);
    await user.selectOptions(select, "pos4");
    await user.click(within(dialog).getByRole("button", { name: "Move" }));
    expect(await screen.findByText("Gus Reis moved to Lead developer")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ position_id: "pos4" });
    expect(calls.find((c) => c.method === "PATCH")?.path).toBe("/orgs/o1/members/u4");
  });

  it("starts the move on the position the member holds now", async () => {
    const { user } = await open(adminChart, "admin");
    await user.click(screen.getByRole("button", { name: "Move Worker 1" }));
    const dialog = await screen.findByRole("dialog", { name: "Move Worker 1" });
    expect(within(dialog).getByRole("combobox", { name: "Position" })).toHaveValue("pos5");
  });
});

describe("who sees the chart (CHART-02)", () => {
  it("shows it to a guest too", async () => {
    await open(plainChart, "guest");
    expect(screen.getByRole("treeitem", { name: "Worker 1" })).toBeVisible();
  });

  it("is not found for someone who is not a member (404)", async () => {
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /orgs": json(200, { items: [] }),
      "GET /orgs/o1": apiError(404, "not_found", "not found"),
      "GET /orgs/o1/chart": apiError(404, "not_found", "not found"),
      "GET /conversations": json(200, { items: [] }),
    });
    renderRoute("/orgs/o1/chart");
    expect(
      await screen.findByRole("heading", { name: "This organization is not on your line" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("tree")).toBeNull();
  });

  it("is not found too when only the chart says so (the viewer was removed a moment ago)", async () => {
    api(plainChart, "member", { "GET /orgs/o1/chart": apiError(404, "not_found", "not found") });
    renderRoute("/orgs/o1/chart");
    expect(
      await screen.findByRole("heading", { name: "This organization is not on your line" }),
    ).toBeInTheDocument();
  });

  it("says so, and offers to try again, when the chart cannot be read", async () => {
    api(plainChart, "member", { "GET /orgs/o1/chart": apiError(500, "internal", "boom") });
    renderRoute("/orgs/o1/chart");
    const text = await screen.findByText("Couldn’t load the org chart.");
    expect(
      within(text.closest("[role=alert]") as HTMLElement).getByRole("button", {
        name: "Try again",
      }),
    ).toBeInTheDocument();
  });
});

describe("opening the organization", () => {
  it("lands on the chart, the first page of an organization", async () => {
    api();
    const { router } = renderRoute("/orgs/o1");
    await screen.findByRole("tree", { name: "Org chart of Acme" });
    expect(router.state.location.pathname).toBe("/orgs/o1/chart");
  });

  it("puts the chart first among the sections, then the members, the positions for admins, the connections and the settings", async () => {
    api(adminChart, "admin");
    renderRoute("/orgs/o1/chart");
    const bar = await screen.findByRole("navigation", { name: "Sections of Acme" });
    expect(
      within(bar)
        .getAllByRole("link")
        .map((l) => l.textContent),
    ).toEqual(["Org chart", "Members", "Positions", "Connectors", "Settings"]);
    expect(within(bar).getByRole("link", { name: "Org chart" })).toHaveAttribute(
      "aria-current",
      "page",
    );
  });

  it("is where the list of organizations leads: each row links to its chart", async () => {
    api(plainChart, "member", { "GET /invitations": json(200, { items: [] }) });
    renderRoute("/orgs");
    const row = await screen.findByRole("listitem", { name: "Acme" });
    expect(within(row).getByRole("link", { name: "Org chart of Acme" })).toHaveAttribute(
      "href",
      "/orgs/o1/chart",
    );
  });

  it("is where the organization's entry leads", async () => {
    api();
    const user = userEvent.setup();
    const { router } = renderRoute("/");
    // The gear beside the switcher is the organization's entry (SET-09).
    await user.click(await screen.findByRole("button", { name: "Pages of Acme" }));
    await user.click(await screen.findByRole("menuitem", { name: "Org chart" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs/o1/chart"));
  });
});

describe("a small screen (CHART-04)", () => {
  it("stops moving the nested items right after four levels, so a deep chart still fits", async () => {
    const chain = Array.from({ length: 8 }, (_, i) =>
      makeChartPosition({
        id: `d${i}`,
        name: `Level ${i + 1}`,
        ...(i > 0 ? { parent_id: `d${i - 1}` } : {}),
        holders: i === 7 ? [member(ana, "admin")] : [],
      }),
    );
    const { tree } = await open(makeOrgChart({ viewer: { admin: false }, positions: chain }));
    const groups = within(tree).getAllByRole("group");
    expect(groups).toHaveLength(8);
    const indented = groups.filter((g) => g.classList.contains("ml-3"));
    expect(indented).toHaveLength(4);
    // The four deepest are not.
    expect(groups.slice(4).every((g) => !g.classList.contains("ml-3"))).toBe(true);
  });

  it("lets a long name give way instead of pushing the row past the screen", async () => {
    const long = makeIntelligence({
      id: "u9",
      display_name: "A very long display name that would not fit a phone",
      handle: "longname",
    });
    await open(makeOrgChart({ unassigned: [member(long)] }));
    const name = within(rowOf("A very long display name that would not fit a phone")).getByRole(
      "link",
      { name: "A very long display name that would not fit a phone" },
    );
    expect(name.className).toContain("truncate");
  });
});

describe("in Portuguese and for assistive technology", () => {
  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    api(adminChart, "admin", { "GET /me": json(200, makeMe({ locale: "pt-BR" })) });
    renderRoute("/orgs/o1/chart");
    expect(
      await screen.findByRole("heading", { level: 1, name: "Organograma" }),
    ).toBeInTheDocument();
    expect(await screen.findByRole("tree", { name: "Organograma de Acme" })).toBeInTheDocument();
    expect(screen.getByRole("treeitem", { name: "Sem lugar definido" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remover Eli Costa de Acme" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Apagar Worker 1" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Editar cargos" })).toBeInTheDocument();
    expect(within(rowOf("Eli Costa")).queryByText("Pessoa")).toBeNull();
    expect(within(rowOf("Eli Costa")).getByText("@eli")).toBeInTheDocument();
  });

  it("has no axe violations: the tree for each viewer and with a dialog open, in both locales", async () => {
    for (const [data, permission] of [
      [plainChart, "member"],
      [leadChart, "member"],
      [adminChart, "admin"],
    ] as const) {
      const { container, unmount } = await open(data, permission);
      expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
      unmount();
    }

    const { container, user } = await open(adminChart, "admin");
    focusItem("Worktrees");
    await user.keyboard("{ArrowLeft}");
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
    await user.click(screen.getByRole("button", { name: "Move Gus Reis" }));
    expect(await axe(await screen.findByRole("dialog"), AXE_OPTIONS)).toHaveNoViolations();
  });

  it("has no axe violations in Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    api(adminChart, "admin", { "GET /me": json(200, makeMe({ locale: "pt-BR" })) });
    const { container } = renderRoute("/orgs/o1/chart");
    await screen.findByRole("tree", { name: "Organograma de Acme" });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });
});
