import type { Schedule, ScheduleRequest } from "@/shared/api/types.gen";
import type { MessageKey, Translate } from "@/shared/i18n";
import { formatTimeOfDay, weekdayName } from "@/shared/lib/time";

/*
 * The rule of a routine as a person reads it (HB-13). The server keeps two shapes, `every_minutes`
 * and a 5-field `cron`; the presets a phone offers are read out of them, and any other cron stays
 * a cron, shown as it is written.
 */

export type RoutineRule =
  | { kind: "minutes"; every: number }
  | { kind: "hours"; every: number }
  | { kind: "daily"; hour: number; minute: number }
  | { kind: "weekdays"; hour: number; minute: number }
  | { kind: "weekly"; day: number; hour: number; minute: number }
  | { kind: "cron"; cron: string };

export type RoutineRuleKind = RoutineRule["kind"];

/** The presets in the order the form offers them, the raw cron last. */
export const ROUTINE_RULE_KINDS: readonly RoutineRuleKind[] = [
  "minutes",
  "hours",
  "daily",
  "weekdays",
  "weekly",
  "cron",
];

const number = (field: string, max: number): number | null => {
  if (!/^\d{1,2}$/.test(field)) return null;
  const n = Number(field);
  return n <= max ? n : null;
};

/** The preset a cron expression is, or the cron itself when it is none of them. */
export function ruleOfCron(cron: string): RoutineRule {
  const raw = cron.trim();
  const fields = raw.split(/\s+/);
  if (fields.length === 5) {
    const [m = "", h = "", dom, month, dow = ""] = fields;
    const minute = number(m, 59);
    const hour = number(h, 23);
    if (minute !== null && hour !== null && dom === "*" && month === "*") {
      if (dow === "*") return { kind: "daily", hour, minute };
      if (dow === "1-5") return { kind: "weekdays", hour, minute };
      if (/^[0-7]$/.test(dow)) return { kind: "weekly", day: Number(dow) % 7, hour, minute };
    }
  }
  return { kind: "cron", cron: raw };
}

/** The rule of a stored routine: a whole number of hours reads as hours. */
export function ruleOf(schedule: Pick<Schedule, "every_minutes" | "cron">): RoutineRule {
  const every = schedule.every_minutes;
  if (every) {
    return every % 60 === 0 ? { kind: "hours", every: every / 60 } : { kind: "minutes", every };
  }
  return ruleOfCron(schedule.cron ?? "");
}

/** What the server is sent for a rule: exactly one of `every_minutes` and `cron`. */
export function ruleRequest(rule: RoutineRule): Pick<ScheduleRequest, "every_minutes" | "cron"> {
  switch (rule.kind) {
    case "minutes":
      return { every_minutes: rule.every };
    case "hours":
      return { every_minutes: rule.every * 60 };
    case "daily":
      return { cron: `${rule.minute} ${rule.hour} * * *` };
    case "weekdays":
      return { cron: `${rule.minute} ${rule.hour} * * 1-5` };
    case "weekly":
      return { cron: `${rule.minute} ${rule.hour} * * ${rule.day}` };
    case "cron":
      return { cron: rule.cron.trim() };
  }
}

/** Whether two rules make the same request (an edit that leaves the rule alone does not send it). */
export function sameRule(a: RoutineRule, b: RoutineRule): boolean {
  const x = ruleRequest(a);
  const y = ruleRequest(b);
  return x.every_minutes === y.every_minutes && (x.cron ?? "") === (y.cron ?? "");
}

/** The rule in words: "Todo dia às 09:00", "Every 2 hours". */
export function describeRule(t: Translate, locale: string, rule: RoutineRule): string {
  switch (rule.kind) {
    case "minutes":
      return t("routines.ruleMinutes", { count: rule.every });
    case "hours":
      return rule.every === 1
        ? t("routines.ruleHourOne")
        : t("routines.ruleHoursMany", { count: rule.every });
    case "daily":
      return t("routines.ruleDaily", { time: formatTimeOfDay(rule.hour, rule.minute, locale) });
    case "weekdays":
      return t("routines.ruleWeekdays", { time: formatTimeOfDay(rule.hour, rule.minute, locale) });
    case "weekly":
      return t("routines.ruleWeekly", {
        day: weekdayName(rule.day, locale),
        time: formatTimeOfDay(rule.hour, rule.minute, locale),
      });
    case "cron":
      return t("routines.ruleCron", { cron: rule.cron });
  }
}

/**
 * A stored routine's rule in words, with its timezone when it is not the viewer's own: "09:00" in
 * another zone is another moment.
 */
export function describeRoutine(
  t: Translate,
  locale: string,
  schedule: Pick<Schedule, "every_minutes" | "cron" | "tz">,
  viewerZone: string,
): string {
  const rule = describeRule(t, locale, ruleOf(schedule));
  return schedule.tz && schedule.tz !== viewerZone
    ? t("routines.ruleZone", { rule, zone: schedule.tz })
    : rule;
}

const STATUS_KEYS: Record<string, MessageKey> = {
  pending: "routines.statusPending",
  delivered: "routines.statusDelivered",
  superseded: "routines.statusSuperseded",
  missed: "routines.statusMissed",
  expired: "routines.statusExpired",
  client_outdated: "routines.statusClientOutdated",
  cancelled: "routines.statusCancelled",
};

/** What became of a run, in words (HB-11); an unknown status is shown as the server names it. */
export function runStatusText(t: Translate, status: string): string {
  const key = STATUS_KEYS[status];
  return key ? t(key) : status;
}
