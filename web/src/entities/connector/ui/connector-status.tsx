import type { Connector, ConnectorStatus } from "@/shared/api/types.gen";
import { type MessageKey, useT } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { STATUS_DOT_CLASS } from "@/shared/ui/atoms/status-dot";
import { useConnectorStateStore } from "../model/state-store";

/*
 * Connector status (UI-17): the line metaphor again. Connected is a filled, pulsing green dot like
 * an online agent; connecting blinks grey; error is a solid red dot; stopped is a hollow ring.
 */

/** Which dot each connector state draws: a connected connector is a pulsing online dot. */
const DOT_VARIANT = {
  connected: "online",
  connecting: "connecting",
  error: "error",
  stopped: "stopped",
} as const satisfies Record<ConnectorStatus, keyof typeof STATUS_DOT_CLASS>;

const LABEL: Record<ConnectorStatus, MessageKey> = {
  stopped: "connectors.status.stopped",
  connecting: "connectors.status.connecting",
  connected: "connectors.status.connected",
  error: "connectors.status.error",
};

/** Status and last error, live from connector.status events when one arrived. */
export function useConnectorState(connector: Connector) {
  const live = useConnectorStateStore((s) => s.connectors[connector.id]);
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
      <span
        aria-hidden="true"
        className={cn("size-2 shrink-0 rounded-full", STATUS_DOT_CLASS[DOT_VARIANT[status]])}
      />
      {t(LABEL[status])}
      {enabled ? null : (
        <span className="rounded-[3px] bg-muted px-1 py-px text-[0.68rem] font-medium text-muted-foreground">
          {t("connectors.disabled")}
        </span>
      )}
    </span>
  );
}
