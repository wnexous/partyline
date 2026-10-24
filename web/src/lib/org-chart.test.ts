import { describe, expect, it } from "vitest";
import type { Position } from "@/lib/api/types.gen";
import {
  makeChartMember,
  makeChartPosition,
  makeIntelligence,
  makeOrgChart,
} from "@/test/fixtures";
import {
  buildChartTree,
  type ChartTreeNode,
  descendantsOf,
  movablePositions,
  positionOptions,
  positionTree,
  visibleNodes,
} from "./org-chart";

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

describe("the positions as a forest", () => {
  const shape = (nodes: ReturnType<typeof positionTree>): unknown[] =>
    nodes.map((n) => [n.position.name, shape(n.children)]);

  it("nests each position under its parent, siblings by name", () => {
    expect(shape(positionTree(chart))).toEqual([
      [
        "Admin",
        [
          [
            "Director",
            [
              ["Designer", []],
              [
                "Manager",
                [
                  ["Lead developer", [["Worktrees", []]]],
                  ["Sentinel", []],
                ],
              ],
            ],
          ],
        ],
      ],
    ]);
  });

  it("keeps whatever the positions carry, so a page can hang its own data on them", () => {
    const [root] = positionTree([{ id: "a", name: "A", holders: 3 }]);
    expect(root?.position.holders).toBe(3);
  });

  it("lists a position once even when a parent cycle hides it from every root", () => {
    const looped = [
      { id: "a", name: "A", parent_id: "b" },
      { id: "b", name: "B", parent_id: "a" },
      { id: "c", name: "C" },
    ];
    const all = (nodes: ReturnType<typeof positionTree>): string[] =>
      nodes.flatMap((n) => [n.position.id, ...all(n.children)]);
    expect(all(positionTree(looped)).sort()).toEqual(["a", "b", "c"]);
  });
});

/*
 * The chart as one tree to draw (CHART-01, CHART-02): the positions with their holders, the
 * members with no position under the member they hang under, and the rest under "Unassigned".
 */
describe("the chart as one tree (CHART-01, CHART-02)", () => {
  const who = (id: string, name: string) =>
    makeChartMember({ intelligence: makeIntelligence({ id, display_name: name, handle: id }) });
  const ana = who("u1", "Ana");
  const bob = who("u2", "Bob");
  const cora = who("u3", "Cora");
  const gus = { ...who("u4", "Gus"), under: "u2" };
  const hal = { ...who("u5", "Hal"), under: "u4" };
  const eli = who("u6", "Eli");

  /** A tree as nested [label, children] pairs: positions by name, members by name, "Unassigned". */
  const shape = (nodes: ChartTreeNode[]): unknown[] =>
    nodes.map((n) => [
      n.kind === "position"
        ? `position ${n.position?.name}`
        : n.kind === "member"
          ? `member ${n.member?.intelligence.display_name}`
          : "unassigned",
      shape(n.children),
    ]);

  it("draws the holders of a position, then the positions below it", () => {
    const chart = makeOrgChart({
      positions: [
        makeChartPosition({ id: "p1", name: "Admin", holders: [ana] }),
        makeChartPosition({ id: "p2", name: "Manager", parent_id: "p1", holders: [cora, bob] }),
      ],
    });
    expect(shape(buildChartTree(chart))).toEqual([
      [
        "position Admin",
        [
          ["member Ana", []],
          [
            "position Manager",
            [
              ["member Bob", []],
              ["member Cora", []],
            ],
          ],
        ],
      ],
    ]);
  });

  it("hangs a member with no position under the member it names, however deep the chain", () => {
    const chart = makeOrgChart({
      positions: [makeChartPosition({ id: "p1", name: "Manager", holders: [bob] })],
      members: [hal, gus],
    });
    expect(shape(buildChartTree(chart))).toEqual([
      ["position Manager", [["member Bob", [["member Gus", [["member Hal", []]]]]]]],
    ]);
  });

  it("puts the members nobody hangs them under in an Unassigned group after the positions", () => {
    const chart = makeOrgChart({
      positions: [makeChartPosition({ id: "p1", name: "Admin", holders: [ana] })],
      unassigned: [eli],
    });
    expect(shape(buildChartTree(chart))).toEqual([
      ["position Admin", [["member Ana", []]]],
      ["unassigned", [["member Eli", []]]],
    ]);
  });

  it("has no Unassigned group when everyone has a place", () => {
    const chart = makeOrgChart({
      positions: [makeChartPosition({ id: "p1", name: "Admin", holders: [ana] })],
    });
    expect(buildChartTree(chart).map((n) => n.kind)).toEqual(["position"]);
  });

  it("files a member whose 'under' names no one the chart lists under Unassigned instead of losing it", () => {
    const stray = { ...who("u7", "Stray"), under: "gone" };
    const chart = makeOrgChart({ members: [stray] });
    expect(shape(buildChartTree(chart))).toEqual([["unassigned", [["member Stray", []]]]]);
  });

  it("draws each member once even when two members name each other (a loop)", () => {
    const a = { ...who("u8", "A"), under: "u9" };
    const b = { ...who("u9", "B"), under: "u8" };
    const chart = makeOrgChart({ members: [a, b] });
    const ids = (nodes: ChartTreeNode[]): string[] =>
      nodes.flatMap((n) => [n.id, ...ids(n.children)]);
    const all = ids(buildChartTree(chart)).filter((id) => id.startsWith("member:"));
    expect(all.sort()).toEqual(["member:u8", "member:u9"]);
  });

  it("gives every node an id that tells its kind and that nothing else shares", () => {
    const chart = makeOrgChart({
      positions: [makeChartPosition({ id: "u1", name: "Admin", holders: [ana] })],
      unassigned: [eli],
    });
    const ids = (nodes: ChartTreeNode[]): string[] =>
      nodes.flatMap((n) => [n.id, ...ids(n.children)]);
    expect(ids(buildChartTree(chart))).toEqual([
      "position:u1",
      "member:u1",
      "unassigned",
      "member:u6",
    ]);
  });

  it("lists what is visible, in tree order, leaving out what a collapsed node hides", () => {
    const chart = makeOrgChart({
      positions: [
        makeChartPosition({ id: "p1", name: "Admin", holders: [ana] }),
        makeChartPosition({ id: "p2", name: "Manager", parent_id: "p1", holders: [bob] }),
      ],
      members: [gus],
    });
    const tree = buildChartTree(chart);
    expect(visibleNodes(tree, new Set()).map((v) => v.node.id)).toEqual([
      "position:p1",
      "member:u1",
      "position:p2",
      "member:u2",
      "member:u4",
    ]);
    expect(visibleNodes(tree, new Set(["position:p2"])).map((v) => v.node.id)).toEqual([
      "position:p1",
      "member:u1",
      "position:p2",
    ]);
    const v = visibleNodes(tree, new Set());
    expect(v.map((x) => [x.node.id, x.level, x.parent])).toEqual([
      ["position:p1", 1, null],
      ["member:u1", 2, "position:p1"],
      ["position:p2", 2, "position:p1"],
      ["member:u2", 3, "position:p2"],
      ["member:u4", 4, "member:u2"],
    ]);
  });
});
