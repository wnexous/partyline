import type { ProjectRef } from "@/lib/api/types.gen";
import { type AgentView, byLiveness } from "@/lib/presence";

/** Readable names of the harness ids the server records (agents.HarnessLabel). */
const HARNESS_LABELS: Record<string, string> = {
  claude: "Claude Code",
  opencode: "OpenCode",
  pi: "pi",
  omp: "oh-my-pi",
  codex: "Codex",
  gemini: "Gemini",
};

/** The harness's readable name; unknown harnesses ("agent") get undefined. */
export const harnessLabel = (id: string): string | undefined => HARNESS_LABELS[id];

/** Agents of one project; project is undefined for the agents of no project. */
export interface AgentGroup {
  key: string;
  project?: ProjectRef;
  agents: AgentView[];
  online: number;
}

/**
 * Groups agents by project (PROJ-04): projects with an online session first, then by name;
 * the agents of no project last. Inside a group, online agents first (byLiveness).
 */
export function groupByProject(agents: AgentView[]): AgentGroup[] {
  const groups = new Map<string, AgentGroup>();
  for (const a of agents) {
    const key = a.project?.id ?? "";
    let g = groups.get(key);
    if (!g) {
      g = { key, project: a.project, agents: [], online: 0 };
      groups.set(key, g);
    }
    g.agents.push(a);
    if (a.live) g.online++;
  }
  const rank = (g: AgentGroup) => (!g.project ? 2 : g.online > 0 ? 0 : 1);
  return [...groups.values()]
    .map((g) => ({ ...g, agents: [...g.agents].sort(byLiveness) }))
    .sort(
      (a, b) =>
        rank(a) - rank(b) ||
        (a.project?.name ?? "").localeCompare(b.project?.name ?? "", undefined, {
          sensitivity: "base",
        }),
    );
}

/** Distinct non-empty values of pick over agents, sorted. */
export function distinct(agents: AgentView[], pick: (a: AgentView) => string): string[] {
  return [...new Set(agents.map(pick).filter((v) => v !== ""))].sort((a, b) => a.localeCompare(b));
}
