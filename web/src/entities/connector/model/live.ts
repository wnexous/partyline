import type { QueryClient } from "@tanstack/react-query";
import { patchList } from "@/shared/api/patch-list";
import { queryKeys } from "@/shared/api/query-keys";
import type { Connector, LiveEvent } from "@/shared/api/types.gen";
import type { ConnectorState } from "./state-store";

/*
 * What the live frames do to the cached connectors and their chats.
 */

type ConnectorStatusFrame = Extract<LiveEvent, { type: "connector.status" }>["data"];

/** A connector reported its status (connector.status): the cached connector shows it at once. */
export function connectorStatusChanged(
  qc: QueryClient,
  setConnectorState: (connectorId: string, state: ConnectorState) => void,
  frame: ConnectorStatusFrame,
) {
  const { connector_id, status, last_error } = frame;
  setConnectorState(connector_id, { status, last_error });
  const patch = (c: Connector): Connector => ({ ...c, status, last_error });
  patchList<Connector>(qc, queryKeys.connectors, connector_id, patch);
  qc.setQueryData<Connector>(queryKeys.connector(connector_id), (old) => (old ? patch(old) : old));
}

/** New chats were found by a connector (chat.discovered): its chat list is fetched again. */
export function chatsDiscovered(qc: QueryClient, connectorId: string) {
  qc.invalidateQueries({ queryKey: queryKeys.connectorChats(connectorId) });
}
