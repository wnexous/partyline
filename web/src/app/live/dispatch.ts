import type { QueryClient } from "@tanstack/react-query";
import { identityLinked, prependActivity } from "@/entities/account";
import type { ConnectorState } from "@/entities/connector";
import { chatsDiscovered, connectorStatusChanged } from "@/entities/connector";
import { agentPresenceChanged, type Presence, patchIntelligence } from "@/entities/intelligence";
import {
  applyMessageUpdate,
  applyReactions,
  prependFiles,
  receiveMessage,
} from "@/entities/message";
import { invalidateOrganizations, invitationReceived } from "@/entities/organization";
import {
  bumpConversation,
  changeRoom,
  markConversationRead,
  noteMessageInRoom,
  removeRoom,
} from "@/entities/room";
import { membersChanged, setAgentOnline } from "@/entities/room-member";
import { queryKeys } from "@/shared/api/query-keys";
import type { Identity, LiveEvent, Message } from "@/shared/api/types.gen";

/*
 * Live event → cache patch. The dispatcher only routes each frame to the entity that owns its
 * data (each entity's `model/live`); the cross-cutting rules (a resource changed for the access
 * list, a lagged socket) stay here. Each handler is a pure function of (query client, store
 * actions, event): no network, no React. Patches only touch data already in the cache, so events
 * about rooms the user never opened are ignored (UI-04 edge case).
 */

/** The store actions a frame writes to. */
export interface LiveActions {
  setPresence: (agentId: string, presence: Presence) => void;
  setConnectorState: (connectorId: string, state: ConnectorState) => void;
  setLastLinkedIdentity: (identity: Identity | null) => void;
  openRoomId: string | null;
}

const RESOURCE_KEYS: Record<string, "rooms" | "agents" | "connectors"> = {
  room: "rooms",
  agent: "agents",
  connector: "connectors",
};

export function handleLiveEvent(qc: QueryClient, actions: LiveActions, event: LiveEvent): void {
  switch (event.type) {
    case "hello":
      return;

    case "room.message": {
      const m: Message = event.data;
      receiveMessage(qc, m);
      noteMessageInRoom(qc, m);
      bumpConversation(qc, actions.openRoomId, m);
      prependActivity(qc, m);
      prependFiles(qc, m);
      return;
    }

    case "room.message_updated":
      // An edit or a delete: the message as it is now replaces the one the cache holds (EDT-06, DLT-06).
      applyMessageUpdate(qc, event.data);
      return;

    case "room.reactions":
      // The reactions of one message, whole: they replace the cache's, and nothing else moves (RCT-06).
      applyReactions(qc, event.data.room_id, event.data.message_id, event.data.reactions);
      return;

    case "room.read":
      markConversationRead(qc, event.data.room_id, event.data.seq);
      return;

    case "room.changed":
      changeRoom(qc, event.data);
      return;

    case "room.deleted":
      removeRoom(qc, event.data.room_id);
      return;

    case "room.members":
      membersChanged(qc, event.data.room_id);
      return;

    case "access.changed": {
      if (event.data.resource_kind === "organization") {
        // An organization the viewer belongs to (or has just left) changed (CHART-02): its pages,
        // the viewer's list of organizations and what is filed under them.
        invalidateOrganizations(qc, event.data.resource_id);
        return;
      }
      const kind = RESOURCE_KEYS[event.data.resource_kind];
      if (!kind) return;
      qc.invalidateQueries({ queryKey: [kind], exact: true });
      qc.invalidateQueries({ queryKey: [kind, event.data.resource_id] });
      if (kind === "agents" || kind === "connectors") {
        // Organization membership decides the AIs and the connections whose people the user sees.
        qc.invalidateQueries({ queryKey: queryKeys.contacts });
      }
      // Membership and DM access decide which conversations the user has.
      if (kind === "rooms")
        qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });
      return;
    }

    case "agent.presence":
      agentPresenceChanged(qc, actions.setPresence, event.data);
      setAgentOnline(qc, event.data.agent_id, event.data.online);
      return;

    case "connector.status":
      connectorStatusChanged(qc, actions.setConnectorState, event.data);
      return;

    case "chat.discovered":
      chatsDiscovered(qc, event.data.connector_id);
      return;

    case "identity.linked":
      identityLinked(qc, actions.setLastLinkedIdentity, event.data);
      return;

    case "intelligence.changed":
      patchIntelligence(qc, event.data);
      return;

    case "invitation_received":
      invitationReceived(qc, event.data);
      return;

    case "lagged":
      // The server dropped events for us: everything visible may be stale.
      qc.invalidateQueries();
      return;
  }
}
