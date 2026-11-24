import { HistoryForever, HistoryOff } from "@/shared/api/types.gen";
import type { Translate } from "@/shared/i18n";

/**
 * The chat history retentions the settings offer (CH-01): off, 7, 30 and 90 days, and forever. The
 * API takes any day count from 1 to 365 too; a connection set to one of those keeps it in the list.
 */
export const HISTORY_CHOICES: readonly number[] = [HistoryOff, 7, 30, 90, HistoryForever];

/** The choices for a connection whose retention is `current`: the usual ones, plus `current`. */
export function historyChoices(current: number): number[] {
  if (HISTORY_CHOICES.includes(current)) return [...HISTORY_CHOICES];
  const days = HISTORY_CHOICES.filter((d) => d > 0);
  const at = days.findIndex((d) => d > current);
  const ordered = at < 0 ? [...days, current] : [...days.slice(0, at), current, ...days.slice(at)];
  return [HistoryOff, ...ordered, HistoryForever];
}

/** The name of a retention: "Off", "30 days", "Forever". */
export function historyLabel(t: Translate, days: number): string {
  if (days === HistoryOff) return t("connector.history.off");
  if (days === HistoryForever) return t("connector.history.forever");
  return t("connector.history.days", { count: days });
}

/** Whether a change of the retention deletes the whole history: turning it off (CH-03). */
export function historyTurnsOff(before: number, after: number): boolean {
  return before !== HistoryOff && after === HistoryOff;
}
