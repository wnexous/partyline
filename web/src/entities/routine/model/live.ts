import type { QueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/shared/api/query-keys";

/**
 * The routines of an AI changed (agent.schedules, HB-12): the frame carries no routine, so whoever
 * has them cached reads them again. A run that is delivered, acknowledged or missed sends it too, so an
 * open history is read again with the list.
 */
export function routinesChanged(qc: QueryClient, agentId: string) {
  qc.invalidateQueries({ queryKey: queryKeys.agentRoutines(agentId), exact: true });
  qc.invalidateQueries({ queryKey: queryKeys.agentRoutineRunsAll(agentId) });
}
