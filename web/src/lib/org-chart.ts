import type { ChartMember, ChartPosition, OrgChart } from "@/lib/api/types.gen";

/*
 * The positions of an organization as the pages read them (POS-01, POS-02, POS-04): in tree order,
 * the subtree of one, and the ones a member may be moved to. Pure functions of the list the chart
 * serves; the server decides, these only tell the pages what to offer.
 */

/** What these functions need of a position. */
export interface PositionLike {
  id: string;
  name: string;
  parent_id?: string;
}

/** A position of the tree in order, with how deep it sits. */
export interface PositionOption {
  id: string;
  name: string;
  depth: number;
}

const byName = (a: PositionLike, b: PositionLike) =>
  a.name.localeCompare(b.name, undefined, { sensitivity: "base" }) || (a.id < b.id ? -1 : 1);

/** A position with the positions directly below it, in order. */
export interface PositionNode<T extends PositionLike = PositionLike> {
  position: T;
  children: PositionNode<T>[];
}

/**
 * The positions as a forest, siblings by name. A position whose parent is not in the list is a
 * root, and so is one a parent cycle leaves unreachable (it is the first of its loop by name), so
 * every position appears exactly once.
 */
export function positionTree<T extends PositionLike>(positions: readonly T[]): PositionNode<T>[] {
  const known = new Set(positions.map((p) => p.id));
  const sorted = [...positions].sort(byName);
  const children = new Map<string | undefined, T[]>();
  for (const p of sorted) {
    const parent = p.parent_id && known.has(p.parent_id) ? p.parent_id : undefined;
    children.set(parent, [...(children.get(parent) ?? []), p]);
  }
  const seen = new Set<string>();
  const build = (p: T): PositionNode<T> => {
    seen.add(p.id);
    return {
      position: p,
      children: (children.get(p.id) ?? []).filter((c) => !seen.has(c.id)).map(build),
    };
  };
  const roots = (children.get(undefined) ?? []).map(build);
  // A parent cycle has no root: what the walk did not reach is still listed, once, as a root.
  for (const p of sorted) if (!seen.has(p.id)) roots.push(build(p));
  return roots;
}

/** The positions in tree order: parents before their children, siblings by name, each with its depth. */
export function positionOptions(positions: readonly PositionLike[]): PositionOption[] {
  const out: PositionOption[] = [];
  const walk = (nodes: PositionNode[], depth: number) => {
    for (const n of nodes) {
      out.push({ id: n.position.id, name: n.position.name, depth });
      walk(n.children, depth + 1);
    }
  };
  walk(positionTree(positions), 0);
  return out;
}

/** The ids below a position: its children, theirs and so on. Not the position itself, unless a cycle leads back to it. */
export function descendantsOf(positions: readonly PositionLike[], id: string): Set<string> {
  const out = new Set<string>();
  const queue = [id];
  while (queue.length > 0) {
    const parent = queue.pop();
    for (const p of positions) {
      if (p.parent_id === parent && !out.has(p.id)) {
        out.add(p.id);
        queue.push(p.id);
      }
    }
  }
  return out;
}

/**
 * The positions a member may be moved to (POS-04): an admin picks any; anyone else only those
 * strictly below their own, so a viewer who holds none, or a leaf, can move no one. Depth counts
 * from what is offered.
 */
export function movablePositions(
  positions: readonly PositionLike[],
  viewer: { admin: boolean; positionId?: string },
): PositionOption[] {
  if (viewer.admin) return positionOptions(positions);
  if (!viewer.positionId) return [];
  const below = descendantsOf(positions, viewer.positionId);
  below.delete(viewer.positionId);
  const own = positionOptions(positions).find((o) => o.id === viewer.positionId)?.depth ?? 0;
  return positionOptions(positions)
    .filter((o) => below.has(o.id))
    .map((o) => ({ ...o, depth: o.depth - own - 1 }));
}

/** One thing the chart draws: a position, a member, or the "Unassigned" group. */
export interface ChartTreeNode {
  /** `position:<id>`, `member:<iid>` or `unassigned`: unique in the tree. */
  id: string;
  kind: "position" | "member" | "unassigned";
  /** Set for a position. */
  position?: ChartPosition;
  /** Set for a member: the holder of a position, or one that hangs under another member. */
  member?: ChartMember;
  /** A position: its holders, then the positions below it. A member: the members that hang under it. */
  children: ChartTreeNode[];
}

const byDisplayName = (a: ChartMember, b: ChartMember) =>
  a.intelligence.display_name.localeCompare(b.intelligence.display_name, undefined, {
    sensitivity: "base",
  }) || (a.intelligence.id < b.intelligence.id ? -1 : 1);

/**
 * The chart as one tree to draw (CHART-01, CHART-02): the positions as a forest with their holders
 * and the positions below them; under each member, the members with no position that hang under it
 * (a guest under its inviter, an AI under its creator); and last an "Unassigned" group for the members
 * with no place. Every member is drawn once, so a loop or a member that names someone the chart
 * does not list cannot lose or repeat one.
 */
export function buildChartTree(chart: OrgChart): ChartTreeNode[] {
  const reports = new Map<string, ChartMember[]>();
  for (const m of chart.members) {
    if (m.under && m.under !== m.intelligence.id) {
      reports.set(m.under, [...(reports.get(m.under) ?? []), m]);
    }
  }

  const drawn = new Set<string>();
  /** Draws the members not drawn yet, one by one: a member the walk reaches twice is drawn at the first. */
  const draw = (members: readonly ChartMember[]): ChartTreeNode[] => {
    const nodes: ChartTreeNode[] = [];
    for (const m of [...members].sort(byDisplayName)) {
      if (!drawn.has(m.intelligence.id)) nodes.push(memberNode(m));
    }
    return nodes;
  };
  const memberNode = (m: ChartMember): ChartTreeNode => {
    drawn.add(m.intelligence.id);
    return {
      id: `member:${m.intelligence.id}`,
      kind: "member",
      member: m,
      children: draw(reports.get(m.intelligence.id) ?? []),
    };
  };
  const positionNode = (n: PositionNode<ChartPosition>): ChartTreeNode => {
    const holders = draw(n.position.holders);
    return {
      id: `position:${n.position.id}`,
      kind: "position",
      position: n.position,
      children: [...holders, ...n.children.map(positionNode)],
    };
  };

  const roots = positionTree(chart.positions).map(positionNode);
  // What no position and no member holds: the members nobody hangs them under, those that hang
  // under someone the chart does not list, and any member a loop left out of the walk above.
  const unassigned = draw([...chart.unassigned, ...chart.members]);
  if (unassigned.length > 0) {
    roots.push({ id: "unassigned", kind: "unassigned", children: unassigned });
  }
  return roots;
}

/** A node as the tree shows it now: how deep it sits (1 for a root) and which node holds it. */
export interface VisibleNode {
  node: ChartTreeNode;
  level: number;
  parent: string | null;
}

/** The nodes a viewer sees, in the order the tree draws them: what a collapsed node holds is left out. */
export function visibleNodes(
  nodes: readonly ChartTreeNode[],
  collapsed: ReadonlySet<string>,
): VisibleNode[] {
  const out: VisibleNode[] = [];
  const walk = (list: readonly ChartTreeNode[], level: number, parent: string | null) => {
    for (const node of list) {
      out.push({ node, level, parent });
      if (!collapsed.has(node.id)) walk(node.children, level + 1, node.id);
    }
  };
  walk(nodes, 1, null);
  return out;
}
