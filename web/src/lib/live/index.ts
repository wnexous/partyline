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
    // Back on screen: the banner measures its grace from the return, not from the drop the hidden
    // tab saw (UI-06). The socket wakes itself in the same visibility handler.
    onVisible: () => store.markVisible(),
    onReconnect: () => {
      void catchUp(qc);
    },
    // Session revoked (close 1008): logged out. Nothing cached belongs to a session any more;
    // the 401 handler sends the user to login and back to this route afterwards (UI-02).
    onSessionEnded: () => {
      qc.clear();
      // A page still mounted for a moment can rebuild a query and refill the cache with data of
      // the session that just ended; the redirect unmounts it first. Clear once it has settled.
      const redirect = notifyUnauthorized();
      if (redirect) void redirect.finally(() => qc.clear());
    },
  });
  socket.start();
  return () => {
    socket.stop();
    useLiveStore.getState().resetLive();
  };
}
