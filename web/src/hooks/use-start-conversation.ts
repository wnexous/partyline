import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useOpenDM } from "@/entities/room";
import { startConversationError } from "@/shared/api/error-text";
import type { IntelligenceRef } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";

export { startConversationError };

/**
 * Opens the DM with an intelligence and goes to it (DM-01): the existing one, or a new one. A
 * failure comes back as text for the caller to show (`error`, kept until the next attempt, and
 * `onError` when the caller would rather toast it).
 */
export function useStartConversation({ onError }: { onError?: (message: string) => void } = {}) {
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
      const message = startConversationError(t, err, target);
      setError(message);
      onError?.(message);
      return false;
    }
  };

  return { start, pending: open.isPending, error, clearError: () => setError(null) };
}
