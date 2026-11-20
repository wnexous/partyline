import { Trash2Icon, UserMinusIcon } from "lucide-react";
import { removalOf } from "@/entities/organization";
import { useT } from "@/shared/i18n";
import { Button } from "@/shared/ui/primitives/button";

/** What the viewer may do with a member of an organization (the members table). */
export interface OrgMemberActionsTarget {
  can_delete: boolean;
  delete_effect: string;
}

/**
 * The delete and remove buttons of an organization member in the members table, described by its
 * row. A delete takes an AI away for good (its host or an admin, SR2-12, POS-06), a remove only takes
 * the identity out of the organization (an admin, SR2-12): the `removalOf` rule decides which of the
 * two is offered, from what the server says the viewer may do.
 */
export function OrgMemberActions<M extends OrgMemberActionsTarget>({
  member,
  orgName,
  describedBy,
  onRemove,
  onDelete,
}: {
  member: M;
  orgName: string;
  /** The table row's name id, which describes each button. */
  describedBy?: string;
  onRemove: (m: M) => void;
  onDelete: (m: M) => void;
}) {
  const t = useT();
  const removal = removalOf(member);
  if (removal === null) return null;
  return (
    <div className="flex flex-wrap gap-2 xl:justify-end">
      {removal === "delete" ? (
        <Button
          variant="outline"
          size="sm"
          className="text-destructive hover:text-destructive pointer-coarse:h-11"
          aria-describedby={describedBy}
          onClick={() => onDelete(member)}
        >
          <Trash2Icon aria-hidden="true" />
          {t("orgs.deleteMember")}
        </Button>
      ) : (
        <Button
          variant="outline"
          size="sm"
          className="text-destructive hover:text-destructive pointer-coarse:h-11"
          aria-describedby={describedBy}
          onClick={() => onRemove(member)}
        >
          <UserMinusIcon aria-hidden="true" />
          {t("profile.removeFrom", { org: orgName })}
        </Button>
      )}
    </div>
  );
}
