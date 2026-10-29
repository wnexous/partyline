import { Link } from "@tanstack/react-router";
import {
  IntelligenceAvatar,
  IntelligenceHandle,
  useIsOnline,
  YouBadge,
} from "@/entities/intelligence";
import { OrgMemberActions } from "@/features/manage-org-member";
import { type ChartMember, PermissionAdmin, PermissionGuest } from "@/shared/api/types.gen";
import { type Translate, useT } from "@/shared/i18n";
import { StatusDot } from "@/shared/ui/atoms/status-dot";
import { Badge } from "@/shared/ui/primitives/badge";
import type { ChartTreeNode } from "../lib/chart-tree";
import type { TreeRowState } from "./chart-tree";

function holdersText(t: Translate, count: number): string {
  if (count === 0) return t("orgs.holdersNone");
  return t(count === 1 ? "orgs.holdersOne" : "orgs.holdersMany", { count });
}

export function ChartRow({
  node,
  state,
  orgName,
  viewerId,
  onMove,
  onRemove,
  onDelete,
}: {
  node: ChartTreeNode;
  state: TreeRowState;
  orgName: string;
  viewerId: string | undefined;
  onMove: (m: ChartMember) => void;
  onRemove: (m: ChartMember) => void;
  onDelete: (m: ChartMember) => void;
}) {
  const t = useT();
  if (node.member) {
    return (
      <ChartMemberRow
        member={node.member}
        state={state}
        orgName={orgName}
        viewerId={viewerId}
        onMove={onMove}
        onRemove={onRemove}
        onDelete={onDelete}
      />
    );
  }
  if (node.position) {
    return (
      <div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-3 gap-y-0.5 py-0.5">
        <span id={state.labelId} className="min-w-0 font-semibold tracking-[-0.01em] break-words">
          {node.position.name}
        </span>
        <span id={state.descId} className="text-xs text-muted-foreground">
          {holdersText(t, node.position.holders.length)}
        </span>
      </div>
    );
  }
  return (
    <div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-3 gap-y-0.5 py-0.5">
      <span id={state.labelId} className="font-semibold tracking-[-0.01em]">
        {t("orgs.unassigned")}
      </span>
      <span id={state.descId} className="text-xs text-muted-foreground">
        {t("orgs.unassignedHint")}
      </span>
    </div>
  );
}

function ChartMemberRow({
  member: m,
  state,
  orgName,
  viewerId,
  onMove,
  onRemove,
  onDelete,
}: {
  member: ChartMember;
  state: TreeRowState;
  orgName: string;
  viewerId: string | undefined;
  onMove: (m: ChartMember) => void;
  onRemove: (m: ChartMember) => void;
  onDelete: (m: ChartMember) => void;
}) {
  const t = useT();
  const who = m.intelligence;
  const isAI = who.kind === "ai";
  // The live presence wins over what the chart said when it was fetched.
  const online = useIsOnline(who.id, m.online);
  const tab = state.tabbable ? 0 : -1;
  const name = who.display_name;

  return (
    <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2 gap-y-1">
      <div className="flex min-w-0 flex-1 basis-48 flex-wrap items-center gap-x-2 gap-y-0.5">
        <span className="flex max-w-full min-w-0 items-center gap-2">
          <IntelligenceAvatar intelligence={who} size="sm" decorative />
          <Link
            id={state.labelId}
            to="/contacts/$id"
            params={{ id: who.id }}
            tabIndex={tab}
            className="min-w-0 truncate rounded-sm font-semibold tracking-[-0.01em] outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring/70"
          >
            {name}
          </Link>
        </span>
        <span
          id={state.descId}
          className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs text-muted-foreground"
        >
          <IntelligenceHandle handle={who.handle} className="max-w-full" />
          <YouBadge intelligence={who} viewerId={viewerId} />
          {m.permission === PermissionAdmin ? (
            <Badge variant="secondary" className="px-1.5 py-0 text-[0.68rem] leading-5">
              {t("profile.permissionAdmin")}
            </Badge>
          ) : null}
          {m.permission === PermissionGuest ? (
            <Badge variant="secondary" className="px-1.5 py-0 text-[0.68rem] leading-5">
              {t("profile.permissionGuest")}
            </Badge>
          ) : null}
          {isAI ? (
            <StatusDot
              online={online}
              label={online ? t("presence.online") : t("presence.offline")}
            />
          ) : null}
        </span>
      </div>
      <OrgMemberActions
        member={m}
        name={name}
        orgName={orgName}
        variant="chart"
        tabIndex={tab}
        onMove={onMove}
        onRemove={onRemove}
        onDelete={onDelete}
      />
    </div>
  );
}
