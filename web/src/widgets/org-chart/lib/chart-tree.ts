import { type PositionNode, positionTree } from "@/entities/organization";
import type { ChartMember, ChartPosition, OrgChart } from "@/shared/api/types.gen";

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
