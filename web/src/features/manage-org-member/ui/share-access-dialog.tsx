import { useState } from "react";
import { toast } from "sonner";
import { permissionLabel, useOrgMovePreview, useUpdateOrgMember } from "@/entities/organization";
import { isApiError } from "@/shared/api/client";
import type { IntelligenceRef } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { ConfirmDialog } from "@/shared/ui/molecules/confirm-dialog";

/** The person who would get the viewer's access, with the permission they keep. */
export interface ShareTarget {
  id: string;
  name: string;
  permission: string;
}

/**
 * "Give the same access as me" (ST-02..ST-04): puts a person in the position the viewer holds, with
 * the ordinary position change (POS-04), after saying plainly what it means - they will manage what
 * the viewer manages through that position, and its AIs will take their messages as trusted. The
 * move preview names the AIs that will trust them, what the viewer manages that they still will not
 * (the identities it created or invited) and who would stop managing them. The server decides in
 * the end: a refusal is told inside the dialog, which stays open.
 */
export function ShareAccessDialog({
  orgId,
  orgName,
  target,
  position,
  onClose,
}: {
  orgId: string;
  orgName: string;
  target: ShareTarget | null;
  /** The position the viewer holds. */
  position: { id: string; name: string } | null;
  onClose: () => void;
}) {
  // Mounted only while a share is being confirmed: every opening asks the preview afresh.
  return target && position ? (
    <ShareConfirm
      key={target.id}
      orgId={orgId}
      orgName={orgName}
      target={target}
      position={position}
      onClose={onClose}
    />
  ) : null;
}

function ShareConfirm({
  orgId,
  orgName,
  target,
  position,
  onClose,
}: {
  orgId: string;
  orgName: string;
  target: ShareTarget;
  position: { id: string; name: string };
  onClose: () => void;
}) {
  const { t, locale } = useI18n();
  const [error, setError] = useState<string | null>(null);
  const preview = useOrgMovePreview(orgId, target.id, position.id);
  const update = useUpdateOrgMember(orgId);
  const names = (refs: readonly IntelligenceRef[]) =>
    new Intl.ListFormat(locale, { style: "long", type: "conjunction" }).format(
      refs.map((who) => who.display_name),
    );

  const confirm = () => {
    if (update.isPending) return;
    setError(null);
    update.mutate(
      { iid: target.id, position_id: position.id },
      {
        onSuccess: () => {
          toast(t("orgs.shared", { name: target.name, org: orgName }));
          onClose();
        },
        onError: (err) => {
          if (isApiError(err) && err.status === 403) setError(t("orgs.shareForbidden"));
          else if (isApiError(err) && err.status === 0) setError(t("error.network"));
          else setError(t("orgs.shareFailed", { name: target.name }));
        },
      },
    );
  };

  const effect = preview.data;
  return (
    <ConfirmDialog
      open
      onClose={onClose}
      title={t("orgs.shareTitle", { name: target.name })}
      body={t("orgs.shareBody", { name: target.name, position: position.name, org: orgName })}
      confirmLabel={t("orgs.shareConfirm")}
      destructive={false}
      pending={update.isPending || preview.isPending}
      onConfirm={confirm}
    >
      <div className="space-y-2 text-sm" aria-live="polite" data-testid="share-access-effect">
        {preview.isPending ? (
          <p className="text-muted-foreground">{t("orgs.sharePreviewLoading")}</p>
        ) : preview.isError || !effect ? (
          <p className="text-muted-foreground">{t("orgs.sharePreviewFailed")}</p>
        ) : (
          <>
            <p className="[overflow-wrap:anywhere]">
              {effect.will_be_trusted_by.length > 0
                ? t("orgs.shareTrusted", { names: names(effect.will_be_trusted_by) })
                : t("orgs.shareTrustedNone", { org: orgName })}
            </p>
            {effect.not_shared.length > 0 ? (
              <p className="[overflow-wrap:anywhere]">
                {t("orgs.shareNotShared", {
                  names: names(effect.not_shared),
                  position: position.name,
                })}
              </p>
            ) : null}
            {effect.lose_management.length > 0 ? (
              <p className="[overflow-wrap:anywhere]">
                {t("orgs.shareLoses", {
                  names: names(effect.lose_management),
                  name: target.name,
                })}
              </p>
            ) : null}
          </>
        )}
        <p className="text-muted-foreground">
          {t("orgs.sharePermission", {
            permission: permissionLabel(t, target.permission).toLocaleLowerCase(locale),
            name: target.name,
          })}
        </p>
        {error ? (
          <p role="alert" className="text-destructive">
            {error}
          </p>
        ) : null}
      </div>
    </ConfirmDialog>
  );
}
