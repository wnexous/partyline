import { ChevronRightIcon } from "lucide-react";
import {
  type KeyboardEvent,
  type ReactNode,
  useCallback,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import { useT } from "@/shared/i18n";
import { type ChartTreeNode, visibleNodes } from "@/lib/org-chart";
import { cn } from "@/shared/lib/utils";

/*
 * The org chart as an ARIA tree (CHART-04): `role="tree"` with a `treeitem` for each position,
 * member and group, `aria-level`, `aria-setsize`, `aria-posinset` and `aria-expanded` on those that
 * hold others, and one tab stop that moves with the focus (a roving tabindex). The keys are the
 * ARIA authoring practices':
 *   Down / Up       the next / previous item that is showing
 *   Right           open a closed item; on an open one, go to its first child
 *   Left            close an open item; on a closed one or a leaf, go to its parent
 *   Home / End      the first / last item that is showing
 *   Enter / Space   open or close an item that holds others; on one that holds nothing, `onOpen`
 * What an item looks like is the caller's (`row`); the tree gives it the ids to name and describe
 * the item, and whether its own focusable parts belong in the tab order (only the tab stop's do).
 */

export interface TreeRowState {
  /** Put on the element that names the item. */
  labelId: string;
  /** Put on the element that describes it. */
  descId: string;
  /** Whether the item is the tab stop: its links and buttons are reachable with Tab only then. */
  tabbable: boolean;
}

/** What names a node, for the label of its arrow. */
function nameOf(node: ChartTreeNode, unassigned: string): string {
  if (node.kind === "position") return node.position?.name ?? "";
  if (node.kind === "member") return node.member?.intelligence.display_name ?? "";
  return unassigned;
}

export function ChartTree({
  nodes,
  label,
  row,
  onOpen,
}: {
  nodes: ChartTreeNode[];
  label: string;
  row: (node: ChartTreeNode, state: TreeRowState) => ReactNode;
  /** Enter or Space on an item that holds no others. */
  onOpen: (node: ChartTreeNode) => void;
}) {
  const t = useT();
  const prefix = useId();
  // The items the viewer closed; everything else is open, so a new item shows.
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(() => new Set());
  const [active, setActive] = useState<string | null>(null);
  const items = useRef(new Map<string, HTMLDivElement>());
  const visible = useMemo(() => visibleNodes(nodes, collapsed), [nodes, collapsed]);
  const ids = useMemo(() => visible.map((v) => v.node.id), [visible]);
  // The tab stop is the item last focused while it still shows, else the first.
  const stop = active !== null && ids.includes(active) ? active : (ids[0] ?? null);

  const focusItem = useCallback((id: string) => {
    setActive(id);
    items.current.get(id)?.focus();
  }, []);
  const toggle = useCallback((id: string, open: boolean) => {
    setCollapsed((now) => {
      const next = new Set(now);
      if (open) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.altKey || e.ctrlKey || e.metaKey) return;
    // A link or a button inside an item keeps its own keys: only an item has a node id.
    const id = (e.target as HTMLElement).dataset.nodeId;
    const at = id ? ids.indexOf(id) : -1;
    const here = visible[at];
    if (!id || !here) return;
    const holds = here.node.children.length > 0;
    const open = holds && !collapsed.has(id);
    const go = (to: string | null | undefined) => {
      if (to) focusItem(to);
    };
    switch (e.key) {
      case "ArrowDown":
        go(ids[at + 1]);
        break;
      case "ArrowUp":
        go(ids[at - 1]);
        break;
      case "Home":
        go(ids[0]);
        break;
      case "End":
        go(ids[ids.length - 1]);
        break;
      case "ArrowRight":
        if (holds && !open) toggle(id, true);
        else if (open) go(here.node.children[0]?.id);
        break;
      case "ArrowLeft":
        if (open) {
          toggle(id, false);
          setActive(id);
        } else {
          go(here.parent);
        }
        break;
      case "Enter":
      case " ":
        if (holds) toggle(id, !open);
        else onOpen(here.node);
        break;
      default:
        return;
    }
    e.preventDefault();
  };

  const render = (list: ChartTreeNode[], level: number): ReactNode =>
    list.map((node, i) => {
      const holds = node.children.length > 0;
      const open = holds && !collapsed.has(node.id);
      const labelId = `${prefix}-${node.id}-label`;
      const descId = `${prefix}-${node.id}-desc`;
      const name = nameOf(node, t("orgs.unassigned"));
      return (
        <div
          key={node.id}
          ref={(el) => {
            if (el) items.current.set(node.id, el);
            else items.current.delete(node.id);
          }}
          role="treeitem"
          data-node-id={node.id}
          aria-level={level}
          aria-setsize={list.length}
          aria-posinset={i + 1}
          aria-expanded={holds ? open : undefined}
          aria-labelledby={labelId}
          aria-describedby={descId}
          tabIndex={node.id === stop ? 0 : -1}
          className="outline-none"
        >
          <div
            data-row=""
            className="flex min-w-0 items-start gap-1 rounded-md py-1 pr-1 [[role=treeitem]:focus-visible>&]:bg-accent/60 [[role=treeitem]:focus-visible>&]:ring-2 [[role=treeitem]:focus-visible>&]:ring-ring/70"
          >
            {holds ? (
              <button
                type="button"
                tabIndex={-1}
                aria-label={t(open ? "orgs.collapse" : "orgs.expand", { name })}
                onClick={() => {
                  toggle(node.id, !open);
                  focusItem(node.id);
                }}
                className="grid size-7 shrink-0 place-items-center rounded-md text-muted-foreground outline-none hover:bg-accent hover:text-foreground"
              >
                <ChevronRightIcon
                  aria-hidden="true"
                  className={cn("size-4 transition-transform", open && "rotate-90")}
                />
              </button>
            ) : (
              <span aria-hidden="true" className="size-7 shrink-0" />
            )}
            {row(node, { labelId, descId, tabbable: node.id === stop })}
          </div>
          {open ? (
            // Past four levels the items stop moving right, so a deep chart still fits a phone.
            // biome-ignore lint/a11y/useSemanticElements: the ARIA tree pattern groups the children of an item with role="group"
            <div role="group" className={level < 5 ? "ml-3 border-l pl-2 md:ml-4" : undefined}>
              {render(node.children, level + 1)}
            </div>
          ) : null}
        </div>
      );
    });

  return (
    <div
      role="tree"
      aria-label={label}
      onKeyDown={onKeyDown}
      onFocus={(e) => {
        const target = e.target as HTMLElement;
        if (target.getAttribute("role") === "treeitem" && target.dataset.nodeId) {
          setActive(target.dataset.nodeId);
        }
      }}
    >
      {render(nodes, 1)}
    </div>
  );
}
