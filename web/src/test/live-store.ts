/*
 * Test facade over the live stores: one `useLiveStore` with the state and actions of all of
 * them, so a test reads and seeds the live state the way it did before the stores were split.
 */

import { type LinkedIdentityState, useLinkedIdentityStore } from "@/entities/account";
import { type ConnectorStateStore, useConnectorStateStore } from "@/entities/connector";
import { type PresenceState, usePresenceStore } from "@/entities/intelligence";
import { type CurrentOrgState, useCurrentOrgStore } from "@/entities/organization";
import { type OpenRoomState, useOpenRoomStore } from "@/entities/room";
import { type SocketState, useSocketStore } from "@/shared/api/socket-store";
import { type PrefsState, usePrefsStore } from "@/shared/lib/prefs-store";

/** The live dispatcher, for tests that push a server event into the stores. */
export { handleLiveEvent } from "@/app/live/dispatch";

const parts = [
  useSocketStore,
  usePresenceStore,
  useConnectorStateStore,
  useLinkedIdentityStore,
  useOpenRoomStore,
  useCurrentOrgStore,
  usePrefsStore,
] as const;

type Merged = SocketState &
  PresenceState &
  ConnectorStateStore &
  LinkedIdentityState &
  OpenRoomState &
  CurrentOrgState &
  PrefsState & { resetLive: () => void };

/** What the app's live cleanup does (app/live): the state of one connection is dropped. */
function resetLive() {
  useSocketStore.setState({ status: "closed", offlineSince: null });
  usePresenceStore.setState({ presence: {} });
  useConnectorStateStore.setState({ connectors: {} });
  useLinkedIdentityStore.setState({ lastLinkedIdentity: null });
  useOpenRoomStore.setState({ openRoomId: null });
}

const merged = (): Merged =>
  ({ ...Object.assign({}, ...parts.map((p) => p.getState())), resetLive }) as Merged;

function useLiveStoreFacade<T>(selector: (state: Merged) => T): T {
  // Every part is subscribed to, in a fixed order, so the hook count never changes.
  const states = parts.map((p) => p());
  return selector(Object.assign({}, ...states) as Merged);
}

export const useLiveStore = Object.assign(useLiveStoreFacade, {
  getState: merged,
  setState: (partial: Partial<Merged>) => {
    for (const p of parts) {
      const keys = Object.keys(p.getState());
      const picked = Object.fromEntries(Object.entries(partial).filter(([k]) => keys.includes(k)));
      if (Object.keys(picked).length > 0) (p.setState as (s: object) => void)(picked);
    }
  },
});
