import { queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { items } from "@/shared/api/list";
import { queryKeys } from "@/shared/api/query-keys";
import type {
  Chat,
  Connector,
  ConnectorKind,
  CreateConnectorRequest,
  List,
  UpdateConnectorRequest,
} from "@/shared/api/types.gen";

export const useConnectorKinds = (enabled = true) =>
  useQuery({
    queryKey: queryKeys.connectorKinds,
    queryFn: () => api<List<ConnectorKind>>("GET", "/connector-kinds").then(items),
    staleTime: Number.POSITIVE_INFINITY,
    enabled,
  });

export const connectorsQuery = () =>
  queryOptions({
    queryKey: queryKeys.connectors,
    queryFn: () => api<List<Connector>>("GET", "/connectors").then(items),
  });

export const connectorQuery = (id: string) =>
  queryOptions({
    queryKey: queryKeys.connector(id),
    queryFn: () => api<Connector>("GET", `/connectors/${id}`),
  });

export const useConnectors = () => useQuery(connectorsQuery());

export const useConnector = (id: string) => useQuery(connectorQuery(id));

export function useCreateConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateConnectorRequest) => api<Connector>("POST", "/connectors", req),
    onSuccess: (c) => {
      // Generated secrets are shown once by the caller and never cached (reads mask them).
      const { generated_secrets: _secrets, ...stored } = c;
      qc.setQueryData<Connector>(queryKeys.connector(c.id), stored);
      qc.invalidateQueries({ queryKey: queryKeys.connectors, exact: true });
    },
  });
}

export function useUpdateConnector(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: UpdateConnectorRequest) => api<Connector>("PATCH", `/connectors/${id}`, req),
    onSuccess: (c) => {
      qc.setQueryData(queryKeys.connector(id), c);
      qc.invalidateQueries({ queryKey: queryKeys.connectors, exact: true });
    },
  });
}

export function useDeleteConnector() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/connectors/${id}`),
    onSuccess: (_, id) => {
      qc.removeQueries({ queryKey: queryKeys.connector(id) });
      qc.invalidateQueries({ queryKey: queryKeys.connectors, exact: true });
    },
  });
}

export const useConnectorChats = (id: string) =>
  useQuery({
    queryKey: queryKeys.connectorChats(id),
    queryFn: () => api<List<Chat>>("GET", `/connectors/${id}/chats`).then(items),
  });

export function useRefreshChats(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<void>("POST", `/connectors/${id}/chats/refresh`),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.connectorChats(id) }),
  });
}
