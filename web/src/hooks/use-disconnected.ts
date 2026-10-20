import { useEffect, useState } from "react";
import { useLiveStore } from "@/lib/live/store";

/** Grace period before the UI admits the live line is down (UI-06). */
export const DISCONNECTED_GRACE_MS = 2_000;

/** True once the live socket has been down for longer than `graceMs` (retries included). */
export function useDisconnected(graceMs = DISCONNECTED_GRACE_MS): boolean {
  const since = useLiveStore((s) => s.offlineSince);
  const [elapsed, setElapsed] = useState(false);

  useEffect(() => {
    if (since === null) {
      setElapsed(false);
      return;
    }
    const remaining = since + graceMs - Date.now();
    if (remaining <= 0) {
      setElapsed(true);
      return;
    }
    setElapsed(false);
    const timer = setTimeout(() => setElapsed(true), remaining);
    return () => clearTimeout(timer);
  }, [since, graceMs]);

  return since !== null && elapsed;
}
