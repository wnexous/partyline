import type { QueryClient } from "@tanstack/react-query";
import { notifyUnauthorized } from "@/lib/api/client";
import { catchUp, handleLiveEvent } from "./handlers";
import { createLiveSocket, type LiveSocketOptions } from "./socket";
import { useLiveStore } from "./store";

/**
 * Connects the live socket to the query cache and the live store. Returns a stop function;
 * the authenticated shell starts it on mount and stops it on logout/unmount.
 */
export function startLive(
  qc: QueryClient,
  options: Pick<LiveSocketOptions, "url" | "createSocket"> = {},
): () => void {
  const store = useLiveStore.getState();
  const socket = createLiveSocket({
    ...options,
    onEvent: (event) => handleLiveEvent(qc, useLiveStore.getState(), event),
    onStatus: (status) => store.setStatus(status),
    onReconnect: () => {
      void catchUp(qc);
    },
    // Session revoked (close 1008): logged out. Nothing cached belongs to a session any more;
    // the 401 handler sends the user to login and back to this route afterwards (UI-02).
    onSessionEnded: () => {
      qc.clear();
      notifyUnauthorized();
    },
  });
  socket.start();
  return () => {
    socket.stop();
    useLiveStore.getState().resetLive();
  };
}
