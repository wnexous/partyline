import { harnessLabel } from "@/lib/agents";

/**
 * The name of the app an AI runs in (Claude Code, OpenCode…): the label the server sends for the
 * app, then the readable name of the harness, then the raw harness id. One rule for every place
 * that says where an AI runs (WA-06).
 */
export function appLabelOf(source: {
  /** The harness id of the AI (`claude`, `opencode`…). */
  app: string;
  /** The apps of its profile; the first one is where it speaks from now. */
  apps?: readonly { label: string }[];
}): string {
  return source.apps?.[0]?.label || harnessLabel(source.app) || source.app;
}
