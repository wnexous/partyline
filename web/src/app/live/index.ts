import type { QueryClient } from "@tanstack/react-query";
import { useLinkedIdentityStore } from "@/entities/account";
import { useConnectorStateStore } from "@/entities/connector";
import { usePresenceStore } from "@/entities/intelligence";
import { useOpenRoomStore } from "@/entities/room";
import { notifyUnauthorized } from "@/shared/api/client";
import { useSocketStore } from "@/shared/api/socket-store";
import { catchUp } from "./catch-up";
import { handleLiveEvent, type LiveActions } from "./dispatch";
import { createLiveSocket, type LiveSocketOptions } from "./socket";

/** The store actions and the open conversation, read when a frame arrives. */
const liveActions = (): LiveActions => ({
  setPresence: usePresenceStore.getState().setPresence,
  setConnectorState: useConnectorStateStore.getState().setConnectorState,
  setLastLinkedIdentity: useLinkedIdentityStore.getState().setLastLinkedIdentity,
  openRoomId: useOpenRoomStore.getState().openRoomId,
});

/** Clears what the live socket and its frames filled (the state of one connection). */
function resetLive() {
  useSocketStore.setState({ status: "closed", offlineSince: null });
  usePresenceStore.setState({ presence: {} });
  useConnectorStateStore.setState({ connectors: {} });
  useLinkedIdentityStore.setState({ lastLinkedIdentity: null });
  useOpenRoomStore.setState({ openRoomId: null });
}

/**
 * Connects the live socket to the query cache and the live stores. Returns a stop function;
 * the authenticated shell starts it on mount and stops it on logout/unmount.
 */
export function startLive(
  qc: QueryClient,
  options: Pick<LiveSocketOptions, "url" | "createSocket"> = {},
): () => void {
  const store = useSocketStore.getState();
  const socket = createLiveSocket({
    ...options,
    onEvent: (event) => handleLiveEvent(qc, liveActions(), event),
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
    resetLive();
  };
}
