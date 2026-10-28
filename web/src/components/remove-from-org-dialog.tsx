import { useState } from "react";
import { toast } from "sonner";
import { useRemoveFromOrg } from "@/lib/api/queries";
import { isApiError } from "@/shared/api/client";
import {
  ErrorCodeHomeOrganization,
  ErrorCodeLastAdmin,
  type IntelligenceRef,
  type ProfileMembership,
} from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { ConfirmDialog } from "@/shared/ui/molecules/confirm-dialog";

/**
 * Asks before taking an identity out of one organization (POS-05, POS-06): it leaves the
 * organization's groups and its position there and nothing else changes, so the dialog says what
 * stays. `membership` open means the dialog is. A failure is told inside it, where the person
 * is looking, and the dialog stays for another try; `onClose` runs when it closes either way.
 */
export function RemoveFromOrgDialog({
  identity,
  membership,
  onClose,
}: {
  identity: IntelligenceRef;
  /** The organization to take it out of; its id and name are all the dialog reads. */
  membership: Pick<ProfileMembership, "org_id" | "org_name"> | null;
  onClose: () => void;
}) {
  const t = useT();
  const remove = useRemoveFromOrg();
  const [error, setError] = useState<string | null>(null);
  const name = identity.display_name;
  const org = membership?.org_name ?? "";
  const isAI = identity.kind === "ai";

  const close = () => {
    setError(null);
    onClose();
  };

  return (
    <ConfirmDialog
      open={membership !== null}
      onClose={close}
      title={t("profile.removeTitle", { name, org })}
      body={t(isAI ? "profile.removeBodyAI" : "profile.removeBodyPerson", { name, org })}
      confirmLabel={t("profile.removeFrom", { org })}
      pending={remove.isPending}
      onConfirm={() => {
        // Stay open while the request runs: a failure must leave the dialog to retry from.
        if (!membership) return;
        setError(null);
        remove.mutate(
          { orgId: membership.org_id, iid: identity.id },
          {
            onSuccess: () => {
              toast(t("profile.removed", { name, org }));
              close();
            },
            onError: (err) => {
              if (isApiError(err) && err.code === ErrorCodeLastAdmin) {
                setError(t("profile.removeLastAdmin", { name, org }));
              } else if (isApiError(err) && err.code === ErrorCodeHomeOrganization) {
                // An AI is deleted in its home organization, never removed from it.
                setError(t("profile.removeHome", { name, org }));
              } else if (isApiError(err) && err.status === 0) {
                setError(t("error.network"));
              } else {
                setError(t("profile.removeFailed", { name, org }));
              }
            },
          },
        );
      }}
    >
      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
    </ConfirmDialog>
  );
}
