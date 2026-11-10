import { z } from "zod";
import {
  type RoutineRule,
  type RoutineRuleKind,
  ruleOf,
  ruleRequest,
  sameRule,
} from "@/entities/routine";
import {
  MaxScheduleEveryMinutes,
  MaxScheduleName,
  MaxSchedulePrompt,
  MinScheduleEveryMinutes,
  type Schedule,
  ScheduleCatchUpOnce,
  ScheduleCatchUpSkip,
  type ScheduleRequest,
} from "@/shared/api/types.gen";
import type { MessageKey } from "@/shared/i18n";
import { msg, textLength } from "@/shared/lib/validation";

/*
 * The form of a routine (HB-13): a name, the prompt, and the rule as a phone picks it, a preset
 * ("every N minutes", "every day at HH:MM"…) or a raw cron. Every field is the text of its input;
 * the schema checks the rule with the server's own limits before it is sent.
 */

const TIME = /^([01]\d|2[0-3]):([0-5]\d)$/;
const WHOLE = /^\d{1,7}$/;

export const routineFormSchema = z
  .object({
    name: z
      .string()
      .trim()
      .min(1, msg("validation.required"))
      .refine((v) => textLength(v) <= MaxScheduleName, msg("validation.routineName")),
    prompt: z
      .string()
      .trim()
      .min(1, msg("validation.required"))
      .refine((v) => textLength(v) <= MaxSchedulePrompt, msg("validation.routinePrompt")),
    kind: z.enum(["minutes", "hours", "daily", "weekdays", "weekly", "cron"]),
    every: z.string().trim(),
    time: z.string().trim(),
    day: z.string(),
    cron: z.string().trim(),
    tz: z.string().trim().min(1, msg("validation.required")),
    catchUp: z.enum([ScheduleCatchUpOnce, ScheduleCatchUpSkip]),
  })
  .superRefine((v, ctx) => {
    for (const issue of ruleIssues(v)) {
      ctx.addIssue({ code: "custom", path: [issue.path], ...msg(issue.key) });
    }
  });

type RuleFields = { kind: RoutineRuleKind; every: string; time: string; cron: string };

/** What is wrong with the rule fields of the form, field by field (the kind decides which count). */
function ruleIssues(v: RuleFields): { path: keyof RuleFields; key: MessageKey }[] {
  const every = WHOLE.test(v.every.trim()) ? Number(v.every.trim()) : Number.NaN;
  if (
    v.kind === "minutes" &&
    !(every >= MinScheduleEveryMinutes && every <= MaxScheduleEveryMinutes)
  ) {
    return [{ path: "every", key: "validation.routineMinutes" }];
  }
  if (v.kind === "hours" && !(every >= 1 && every * 60 <= MaxScheduleEveryMinutes)) {
    return [{ path: "every", key: "validation.routineHours" }];
  }
  if (
    (v.kind === "daily" || v.kind === "weekdays" || v.kind === "weekly") &&
    !TIME.test(v.time.trim())
  ) {
    return [{ path: "time", key: "validation.routineTime" }];
  }
  if (v.kind === "cron" && v.cron.trim().split(/\s+/).filter(Boolean).length !== 5) {
    return [{ path: "cron", key: "validation.routineCron" }];
  }
  return [];
}

export type RoutineForm = z.infer<typeof routineFormSchema>;

const pad = (n: number) => String(n).padStart(2, "0");

/** What the form starts with: the routine's own values, or a new one at 09:00 every day in `zone`. */
export function routineFormDefaults(zone: string, routine?: Schedule): RoutineForm {
  const values: RoutineForm = {
    name: routine?.name ?? "",
    prompt: routine?.prompt ?? "",
    kind: "daily",
    every: "30",
    time: "09:00",
    day: "1",
    cron: "0 9 * * *",
    tz: routine?.tz || zone,
    catchUp: routine?.catch_up === ScheduleCatchUpSkip ? ScheduleCatchUpSkip : ScheduleCatchUpOnce,
  };
  if (!routine) return values;
  const rule = ruleOf(routine);
  values.kind = rule.kind;
  if (rule.kind === "minutes" || rule.kind === "hours") values.every = String(rule.every);
  if (rule.kind === "daily" || rule.kind === "weekdays" || rule.kind === "weekly") {
    values.time = `${pad(rule.hour)}:${pad(rule.minute)}`;
  }
  if (rule.kind === "weekly") values.day = String(rule.day);
  if (rule.kind === "cron") values.cron = rule.cron;
  return values;
}

/** The number a new kind's interval starts at, when the person switches between minutes and hours. */
export function everyFor(kind: RoutineRuleKind, current: string): string {
  const n = Number(current);
  if (kind === "minutes" && !(n >= MinScheduleEveryMinutes)) return "30";
  if (kind === "hours" && !(n >= 1 && n <= 24)) return "1";
  return current;
}

/** The rule the form describes, or null while its fields do not make one. */
export function ruleOfForm(v: RuleFields & { day: string }): RoutineRule | null {
  if (ruleIssues(v).length > 0) return null;
  const [hour = 0, minute = 0] = v.time.trim().split(":").map(Number);
  switch (v.kind) {
    case "minutes":
    case "hours":
      return { kind: v.kind, every: Number(v.every.trim()) };
    case "daily":
    case "weekdays":
      return { kind: v.kind, hour, minute };
    case "weekly":
      return { kind: "weekly", day: Number(v.day) % 7, hour, minute };
    case "cron":
      return { kind: "cron", cron: v.cron.trim().split(/\s+/).join(" ") };
  }
}

/**
 * The request a valid form makes. A new routine sends everything; an edit sends only what changed,
 * so leaving the rule alone does not start its series again, and an untouched edit sends nothing.
 */
export function routineRequest(v: RoutineForm, original?: Schedule): ScheduleRequest {
  const rule = ruleOfForm(v);
  const values = routineFormSchema.parse(v);
  if (!rule) return {};
  if (!original) {
    return {
      name: values.name,
      prompt: values.prompt,
      ...ruleRequest(rule),
      tz: values.tz,
      catch_up: values.catchUp,
    };
  }
  const body: ScheduleRequest = {};
  if (values.name !== original.name) body.name = values.name;
  if (values.prompt !== original.prompt.trim()) body.prompt = values.prompt;
  if (!sameRule(rule, ruleOf(original))) Object.assign(body, ruleRequest(rule));
  if (values.tz !== original.tz) body.tz = values.tz;
  if (values.catchUp !== original.catch_up) body.catch_up = values.catchUp;
  return body;
}
