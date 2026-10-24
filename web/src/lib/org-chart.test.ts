import { describe, expect, it } from "vitest";
import type { ChartMember, Position } from "@/lib/api/org-types";
import {
  makeChartMember,
  makeChartPosition,
  makeIntelligence,
  makeOrgChart,
} from "@/test/fixtures";
import { chartMembers, descendantsOf, movablePositions, positionOptions } from "./org-chart";

/*
 * The positions of an organization as the pages use them (POS-01, POS-02, POS-04): in tree order,
 * the subtree of one, and the ones a member may be moved to. The spec's chart: Admin > Director >
 * Manager > Lead developer > Worktrees, with Sentinel beside Lead developer.
 */

const admin: Position = { id: "p1", name: "Admin" };
const director: Position = { id: "p2", name: "Director", parent_id: "p1" };
const manager: Position = { id: "p3", name: "Manager", parent_id: "p2" };
const lead: Position = { id: "p4", name: "Lead developer", parent_id: "p3" };
const worktrees: Position = { id: "p5", name: "Worktrees", parent_id: "p4" };
const sentinel: Position = { id: "p6", name: "Sentinel", parent_id: "p3" };
const designer: Position = { id: "p7", name: "Designer", parent_id: "p2" };
const chart = [worktrees, sentinel, lead, admin, designer, manager, director];

const names = (options: { name: string; depth: number }[]) =>
  options.map((o) => `${o.depth}:${o.name}`);

describe("the positions in tree order", () => {
  it("lists parents before their children and siblings by name, each with its depth", () => {
    expect(names(positionOptions(chart))).toEqual([
      "0:Admin",
      "1:Director",
      "2:Designer",
      "2:Manager",
      "3:Lead developer",
      "4:Worktrees",
      "3:Sentinel",
    ]);
  });

  it("sorts siblings ignoring case and accents", () => {
    expect(
      names(
        positionOptions([
          { id: "a", name: "zebra" },
          { id: "b", name: "Árbitro" },
          { id: "c", name: "banco" },
        ]),
      ),
    ).toEqual(["0:Árbitro", "0:banco", "0:zebra"]);
  });

  it("makes a root of a position whose parent is not in the list, and lists several roots", () => {
    expect(
      names(
        positionOptions([
          { id: "a", name: "Orphan", parent_id: "gone" },
          { id: "b", name: "Admin" },
        ]),
      ),
    ).toEqual(["0:Admin", "0:Orphan"]);
  });

  it("does not loop on a parent cycle, and still lists each position once", () => {
    const looped = [
      { id: "a", name: "A", parent_id: "b" },
      { id: "b", name: "B", parent_id: "a" },
    ];
    expect(positionOptions(looped)).toHaveLength(2);
  });

  it("is empty for an organization with no position", () => {
    expect(positionOptions([])).toEqual([]);
  });
});

describe("the subtree of a position", () => {
  it("holds its children, theirs and so on, and not the position itself", () => {
    expect([...descendantsOf(chart, "p3")].sort()).toEqual(["p4", "p5", "p6"]);
    expect([...descendantsOf(chart, "p4")]).toEqual(["p5"]);
  });

  it("is empty for a leaf and for an unknown position", () => {
    expect(descendantsOf(chart, "p5").size).toBe(0);
    expect(descendantsOf(chart, "nope").size).toBe(0);
  });

  it("ends on a parent cycle", () => {
    const looped = [
      { id: "a", name: "A", parent_id: "b" },
      { id: "b", name: "B", parent_id: "a" },
    ];
    expect([...descendantsOf(looped, "a")].sort()).toEqual(["a", "b"]);
  });
});

describe("where a member may be moved (POS-04)", () => {
  it("lets an admin pick any position", () => {
    expect(movablePositions(chart, { admin: true, positionId: "p3" }).map((o) => o.name)).toEqual([
      "Admin",
      "Director",
      "Designer",
      "Manager",
      "Lead developer",
      "Worktrees",
      "Sentinel",
    ]);
  });

  it("lets anyone else pick only the positions strictly below their own", () => {
    expect(movablePositions(chart, { admin: false, positionId: "p3" }).map((o) => o.name)).toEqual([
      "Lead developer",
      "Worktrees",
      "Sentinel",
    ]);
    expect(movablePositions(chart, { admin: false, positionId: "p4" }).map((o) => o.name)).toEqual([
      "Worktrees",
    ]);
  });

  it("offers a manager nothing from a leaf position, nor when they hold none", () => {
    expect(movablePositions(chart, { admin: false, positionId: "p5" })).toEqual([]);
    expect(movablePositions(chart, { admin: false })).toEqual([]);
  });

  it("measures the depth of what it offers from the manager's own position", () => {
    expect(names(movablePositions(chart, { admin: false, positionId: "p3" }))).toEqual([
      "0:Lead developer",
      "1:Worktrees",
      "0:Sentinel",
    ]);
  });
});

describe("the members a chart lists", () => {
  const who = (id: string): ChartMember =>
    makeChartMember({ intelligence: makeIntelligence({ id, display_name: id, handle: id }) });

  it("are the holders of the positions, then the members with no position, once each", () => {
    const chart = makeOrgChart({
      positions: [
        makeChartPosition({ id: "p1", holders: [who("a"), who("b")] }),
        makeChartPosition({ id: "p2", holders: [who("c")] }),
      ],
      members: [who("d")],
      unassigned: [who("e")],
    });
    expect(chartMembers(chart).map((m) => m.intelligence.id)).toEqual(["a", "b", "c", "d", "e"]);
  });

  it("lists a member who appears twice only once, with the first entry's flags", () => {
    const first = makeChartMember({ ...who("a"), can_delete: true });
    const chart = makeOrgChart({
      positions: [makeChartPosition({ holders: [first] })],
      members: [who("a")],
    });
    const all = chartMembers(chart);
    expect(all).toHaveLength(1);
    expect(all[0]?.can_delete).toBe(true);
  });

  it("are none for a chart with nothing in it", () => {
    expect(chartMembers(makeOrgChart())).toEqual([]);
  });
});
