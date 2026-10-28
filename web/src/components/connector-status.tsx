import type { Connector, ConnectorStatus } from "@/shared/api/types.gen";
import { type MessageKey, useT } from "@/shared/i18n";
import { useLiveStore } from "@/lib/live/store";
import { cn } from "@/shared/lib/utils";

/*
 * Connector status (UI-17): the line metaphor again. Connected is a filled, pulsing green dot like
 * an online agent; connecting blinks grey; error is a solid red dot; stopped is a hollow ring.
 */

const LABEL: Record<ConnectorStatus, MessageKey> = {
  stopped: "connectors.status.stopped",
  connecting: "connectors.status.connecting",
  connected: "connectors.status.connected",
  error: "connectors.status.error",
};

const DOT: Record<ConnectorStatus, string> = {
  connected: "animate-line-pulse bg-online",
  connecting: "animate-pulse bg-muted-foreground",
  error: "bg-destructive",
  stopped: "border border-muted-foreground bg-transparent",
};

/** Status and last error, live from connector.status events when one arrived. */
export function useConnectorState(connector: Connector) {
  const live = useLiveStore((s) => s.connectors[connector.id]);
  return {
    status: live?.status ?? connector.status,
    lastError: live ? live.last_error : connector.last_error,
  };
}

export function ConnectorStatusBadge({
  status,
  enabled = true,
  className,
}: {
  status: ConnectorStatus;
  enabled?: boolean;
  className?: string;
}) {
  const t = useT();
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 text-xs",
        status === "error" ? "font-medium text-destructive" : "text-muted-foreground",
        className,
      )}
    >
      <span aria-hidden="true" className={cn("size-2 shrink-0 rounded-full", DOT[status])} />
      {t(LABEL[status])}
      {enabled ? null : (
        <span className="rounded-[3px] bg-muted px-1 py-px text-[0.68rem] font-medium text-muted-foreground">
          {t("connectors.disabled")}
        </span>
      )}
    </span>
  );
}
