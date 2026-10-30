import type { MessageKey } from "@/shared/i18n";
import type { StatusVariant } from "@/shared/ui/atoms/status-dot";

/*
 * What a container's state means (CA-06), in one place: the dot it draws and the word beside it.
 * `state` is what the runner last reported and `desired` what the owner wants, so a container that
 * is stopped while wanted running is on its way back.
 */

export type ContainerState = "running" | "restarting" | "stopped" | "error" | "unknown";

const VARIANTS = {
  running: "online",
  // A restart loop is not serving anything: the dot is the destructive one, never the green.
  restarting: "error",
  stopped: "stopped",
  error: "error",
  unknown: "connecting",
} as const satisfies Record<string, StatusVariant>;

const LABELS: Record<ContainerState, MessageKey> = {
  running: "container.state.running",
  restarting: "container.state.restarting",
  stopped: "container.state.stopped",
  error: "container.state.error",
  unknown: "container.state.unknown",
};

const DESIRED: Record<string, MessageKey> = {
  running: "container.desired.running",
  stopped: "container.desired.stopped",
};

/** The dot variant of a state the runner reported; anything unknown is still connecting. */
export const containerVariant = (state: string): StatusVariant =>
  VARIANTS[state as ContainerState] ?? "connecting";

/** The line's own word for the state. */
export const containerStateLabel = (state: string): MessageKey =>
  LABELS[state as ContainerState] ?? "container.state.unknown";

/** What the owner asked for: running or stopped. */
export const containerDesiredLabel = (desired: string): MessageKey =>
  DESIRED[desired] ?? "container.desired.running";
