import type { QueryClient } from "@tanstack/react-query";
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
  });
  socket.start();
  return () => {
    socket.stop();
    useLiveStore.getState().resetLive();
  };
}
