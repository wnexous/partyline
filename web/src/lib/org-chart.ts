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
