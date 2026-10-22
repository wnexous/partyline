import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { isApiError } from "@/lib/api/client";
import { useOpenDM } from "@/lib/api/queries";
import {
  ErrorCodeContactNotFound,
  ErrorCodeNoRouteToPerson,
  type IntelligenceRef,
} from "@/lib/api/types.gen";
import { type Translate, useT } from "@/lib/i18n";

/** Why a DM with `target` could not be opened, in words the user can act on. */
export function startConversationError(
  t: Translate,
  err: unknown,
  target: Pick<IntelligenceRef, "display_name">,
): string {
  const name = target.display_name;
  if (isApiError(err)) {
    if (err.code === ErrorCodeNoRouteToPerson) return t("dm.noRoute", { name });
    if (err.code === ErrorCodeContactNotFound) return t("dm.notFound", { name });
    if (err.status === 0) return t("error.network");
  }
  return t("dm.failed");
}

/**
 * Opens the DM with an intelligence and goes to it (DM-01): the existing one, or a new one. A
 * failure comes back as text for the caller to show; the next attempt clears it.
 */
export function useStartConversation() {
  const t = useT();
  const navigate = useNavigate();
  const open = useOpenDM();
  const [error, setError] = useState<string | null>(null);

  const start = async (target: IntelligenceRef): Promise<boolean> => {
    setError(null);
    try {
      const room = await open.mutateAsync(target.id);
      void navigate({ to: "/rooms/$roomId", params: { roomId: room.id } });
      return true;
    } catch (err) {
      setError(startConversationError(t, err, target));
      return false;
    }
  };

  return { start, pending: open.isPending, error, clearError: () => setError(null) };
}
