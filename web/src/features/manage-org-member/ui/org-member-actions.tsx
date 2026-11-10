import { KeyRoundIcon, MoveRightIcon, Trash2Icon, UserMinusIcon } from "lucide-react";
import { removalOf } from "@/entities/organization";
import { useT } from "@/shared/i18n";
import { Button } from "@/shared/ui/primitives/button";

/** What the viewer may do with a member of an organization (the chart and the members table). */
export interface OrgMemberActionsTarget {
  can_move: boolean;
  can_delete: boolean;
  delete_effect: string;
  /** The viewer may put this person in its own position (ST-01); the members list carries it. */
  can_share_access?: boolean;
}

/**
 * The move, delete and remove buttons of an organization member, the one set for the members table
 * (`table`, described by its row) and for the chart (`chart`, each button names its member). A
 * delete takes the identity away (POS-06), a remove only leaves the organization (POS-05): the
 * `removalOf` rule decides which of the two is offered. "Give the same access as me" (ST-02) shows
 * where the server offers it and the page handles it (`onShare`).
 */
export function OrgMemberActions<M extends OrgMemberActionsTarget>({
  member,
  name,
  orgName,
  variant,
  describedBy,
  tabIndex,
  onMove,
  onShare,
  onRemove,
  onDelete,
}: {
  member: M;
  /** The member's display name, for the chart's labels. */
  name: string;
  orgName: string;
  variant: "table" | "chart";
  /** The table row's name id, which describes each button. */
  describedBy?: string;
  tabIndex?: number;
  onMove: (m: M) => void;
  /** Gives the member the viewer's position; without it the action is not offered. */
  onShare?: (m: M) => void;
  onRemove: (m: M) => void;
  onDelete: (m: M) => void;
}) {
  const t = useT();
  const chart = variant === "chart";
  const removal = removalOf(member);
  const share = member.can_share_access && onShare ? onShare : null;
  if (!member.can_move && !share && removal === null) return null;
  return (
    <div
      className={
        chart ? "flex flex-wrap items-center gap-1.5" : "flex flex-wrap gap-2 xl:justify-end"
      }
    >
      {member.can_move ? (
        <Button
          variant="outline"
          size="sm"
          tabIndex={tabIndex}
          className="pointer-coarse:h-11"
          aria-describedby={describedBy}
          aria-label={chart ? t("orgs.chartMove", { name }) : undefined}
          onClick={() => onMove(member)}
        >
          <MoveRightIcon aria-hidden="true" />
          {t("orgs.move")}
        </Button>
      ) : null}
      {share ? (
        <Button
          variant="outline"
          size="sm"
          tabIndex={tabIndex}
          className="pointer-coarse:h-11"
          aria-describedby={describedBy}
          onClick={() => share(member)}
        >
          <KeyRoundIcon aria-hidden="true" />
          {t("orgs.share")}
        </Button>
      ) : null}
      {removal === "delete" ? (
        <Button
          variant="outline"
          size="sm"
          tabIndex={tabIndex}
          className="text-destructive hover:text-destructive pointer-coarse:h-11"
          aria-describedby={describedBy}
          aria-label={chart ? t("orgs.chartDelete", { name }) : undefined}
          onClick={() => onDelete(member)}
        >
          <Trash2Icon aria-hidden="true" />
          {t("orgs.deleteMember")}
        </Button>
      ) : null}
      {removal === "remove" ? (
        <Button
          variant="outline"
          size="sm"
          tabIndex={tabIndex}
          className="text-destructive hover:text-destructive pointer-coarse:h-11"
          aria-describedby={describedBy}
          aria-label={chart ? t("orgs.chartRemove", { name, org: orgName }) : undefined}
          onClick={() => onRemove(member)}
        >
          <UserMinusIcon aria-hidden="true" />
          {chart ? t("orgs.removeShort") : t("profile.removeFrom", { org: orgName })}
        </Button>
      ) : null}
    </div>
  );
}
