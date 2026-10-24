import { useId, useState } from "react";
import { toast } from "sonner";
import { NativeSelect } from "@/components/native-select";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { isApiError } from "@/lib/api/client";
import type { Position } from "@/lib/api/org-types";
import { useUpdateOrgMember } from "@/lib/api/queries";
import { useT } from "@/lib/i18n";
import { movablePositions, type PositionOption } from "@/lib/org-chart";

/** Who is being moved, and where they sit now (`""` for no position). */
export interface MoveTarget {
  id: string;
  name: string;
  positionId: string;
}

/**
 * Moves a member to another position of the organization (POS-04). An admin picks any position, or
 * none; anyone else only a position strictly below their own, so those are the only ones offered.
 * Where the member is now is the starting choice (and is shown even when the viewer could not pick
 * it), and nothing is sent until another one is. The server decides in the end: a refusal is told
 * inside the dialog, which stays open.
 */
export function MoveMemberDialog({
  orgId,
  orgName,
  target,
  positions,
  viewer,
  onClose,
}: {
  orgId: string;
  orgName: string;
  target: MoveTarget | null;
  positions: readonly Position[];
  viewer: { admin: boolean; positionId?: string };
  onClose: () => void;
}) {
  const t = useT();
  return (
    <Dialog open={target !== null} onOpenChange={(open) => (open ? null : onClose())}>
      <DialogContent>
        {target ? (
          <MoveForm
            orgId={orgId}
            orgName={orgName}
            target={target}
            positions={positions}
            viewer={viewer}
            onClose={onClose}
          />
        ) : (
          <DialogHeader>
            <DialogTitle>{t("orgs.move")}</DialogTitle>
          </DialogHeader>
        )}
      </DialogContent>
    </Dialog>
  );
}

function MoveForm({
  orgId,
  orgName,
  target,
  positions,
  viewer,
  onClose,
}: {
  orgId: string;
  orgName: string;
  target: MoveTarget;
  positions: readonly Position[];
  viewer: { admin: boolean; positionId?: string };
  onClose: () => void;
}) {
  const t = useT();
  const id = useId();
  const update = useUpdateOrgMember(orgId);
  const [value, setValue] = useState(target.positionId);
  const [error, setError] = useState<string | null>(null);

  // What the viewer may pick, with where the member is now first when it is not among it.
  const choices: (PositionOption & { disabled?: boolean })[] = viewer.admin
    ? [{ id: "", name: t("orgs.noPosition"), depth: 0 }, ...movablePositions(positions, viewer)]
    : movablePositions(positions, viewer);
  if (!choices.some((c) => c.id === target.positionId)) {
    const now = positions.find((p) => p.id === target.positionId);
    choices.unshift({
      id: target.positionId,
      name: now?.name ?? t("orgs.noPosition"),
      depth: 0,
      disabled: true,
    });
  }
  const chosen = choices.find((c) => c.id === value);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (update.isPending || value === target.positionId) return;
    setError(null);
    update.mutate(
      { iid: target.id, position_id: value },
      {
        onSuccess: () => {
          toast(
            value === ""
              ? t("orgs.movedNone", { name: target.name })
              : t("orgs.moved", { name: target.name, position: chosen?.name ?? "" }),
          );
          onClose();
        },
        onError: (err) => {
          if (isApiError(err) && err.status === 403) setError(t("orgs.moveForbidden"));
          else if (isApiError(err) && err.status === 0) setError(t("error.network"));
          else setError(t("orgs.moveFailed", { name: target.name }));
        },
      },
    );
  };

  return (
    <form onSubmit={submit} className="space-y-4">
      <DialogHeader>
        <DialogTitle>{t("orgs.moveTitle", { name: target.name })}</DialogTitle>
        <DialogDescription>
          {t("orgs.moveBody", { name: target.name, org: orgName })}
        </DialogDescription>
      </DialogHeader>
      <div className="grid gap-2">
        <Label htmlFor={`${id}-position`}>{t("orgs.movePosition")}</Label>
        <NativeSelect
          id={`${id}-position`}
          value={value}
          onChange={(e) => setValue(e.target.value)}
        >
          {choices.map((c) => (
            <option key={c.id || "none"} value={c.id} disabled={c.disabled}>
              {"  ".repeat(c.depth)}
              {c.name}
            </option>
          ))}
        </NativeSelect>
      </div>
      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onClose}>
          {t("common.cancel")}
        </Button>
        <Button type="submit" disabled={update.isPending || value === target.positionId}>
          {t("orgs.move")}
        </Button>
      </DialogFooter>
    </form>
  );
}
