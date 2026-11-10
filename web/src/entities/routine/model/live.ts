import type { QueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/shared/api/query-keys";

/**
 * The routines of an AI changed (agent.schedules, HB-12): the frame carries no routine, so whoever
 * has them cached reads them again.
 */
export function routinesChanged(qc: QueryClient, agentId: string) {
  qc.invalidateQueries({ queryKey: queryKeys.agentRoutines(agentId), exact: true });
}
