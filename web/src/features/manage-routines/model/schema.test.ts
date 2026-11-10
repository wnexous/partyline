import { describe, expect, it } from "vitest";
import { makeRoutine } from "@/test/fixtures";
import {
  type RoutineForm,
  routineFormDefaults,
  routineFormSchema,
  routineRequest,
  ruleOfForm,
} from "./schema";

/*
 * The routine form (HB-13): the rule is checked with the server's limits before it is sent, a new
 * routine sends every field, and an edit sends only what changed (so leaving the rule alone does not
 * start its series again).
 */

const stored = makeRoutine({ prompt: "Summarize yesterday" });

const form = (over: Partial<RoutineForm> = {}): RoutineForm => ({
  ...routineFormDefaults("Europe/Lisbon"),
  name: "Deploys",
  prompt: "Check the deploys",
  ...over,
});

const issues = (values: RoutineForm) => {
  const parsed = routineFormSchema.safeParse(values);
  return parsed.success ? [] : parsed.error.issues.map((i) => `${String(i.path[0])}:${i.message}`);
};

describe("the routine form", () => {
  it("starts a new routine at 09:00 every day in the browser's zone", () => {
    const values = routineFormDefaults("Europe/Lisbon");
    expect(values).toMatchObject({ kind: "daily", time: "09:00", tz: "Europe/Lisbon" });
    expect(ruleOfForm(values)).toEqual({ kind: "daily", hour: 9, minute: 0 });
  });

  it("refuses an interval under the server's minimum and a time that is not one", () => {
    expect(issues(form({ kind: "minutes", every: "14" }))).toEqual([
      "every:validation.routineMinutes",
    ]);
    expect(issues(form({ kind: "minutes", every: "15" }))).toEqual([]);
    expect(issues(form({ kind: "hours", every: "0" }))).toEqual(["every:validation.routineHours"]);
    expect(issues(form({ kind: "daily", time: "25:00" }))).toEqual(["time:validation.routineTime"]);
    expect(issues(form({ kind: "cron", cron: "0 9 * *" }))).toEqual([
      "cron:validation.routineCron",
    ]);
    expect(ruleOfForm(form({ kind: "minutes", every: "5" }))).toBeNull();
  });

  it("sends every field of a new routine", () => {
    expect(routineRequest(form({ kind: "hours", every: "2", catchUp: "skip" }))).toEqual({
      name: "Deploys",
      prompt: "Check the deploys",
      every_minutes: 120,
      tz: "Europe/Lisbon",
      catch_up: "skip",
    });
    expect(routineRequest(form({ kind: "weekly", day: "5", time: "18:30" }))).toMatchObject({
      cron: "30 18 * * 5",
    });
  });

  it("fills an edit with the routine and sends only what changed", () => {
    const values = routineFormDefaults("Europe/Lisbon", stored);
    expect(values).toMatchObject({
      name: "Daily summary",
      kind: "daily",
      time: "09:00",
      tz: "America/Sao_Paulo",
    });
    expect(routineRequest(values, stored)).toEqual({});
    expect(routineRequest({ ...values, name: " Morning summary " }, stored)).toEqual({
      name: "Morning summary",
    });
    expect(routineRequest({ ...values, kind: "weekdays" }, stored)).toEqual({
      cron: "0 9 * * 1-5",
    });
    expect(routineRequest({ ...values, kind: "minutes", every: "30" }, stored)).toEqual({
      every_minutes: 30,
    });
  });
});
