import { create } from "zustand";
import type { ConnectorStatus } from "@/shared/api/types.gen";

/** The status a connector reported on the live socket (connector.status), by connector id. */
export interface ConnectorState {
  status: ConnectorStatus;
  last_error?: string;
}

export interface ConnectorStateStore {
  connectors: Record<string, ConnectorState>;
  setConnectorState: (connectorId: string, state: ConnectorState) => void;
}

export const useConnectorStateStore = create<ConnectorStateStore>()((set) => ({
  connectors: {},
  setConnectorState: (connectorId, state) =>
    set((s) => ({ connectors: { ...s.connectors, [connectorId]: state } })),
}));
