import { zodResolver } from "@hookform/resolvers/zod";
import { ChevronRightIcon } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { type UseFormSetError, useForm, useWatch } from "react-hook-form";
import { toast } from "sonner";
import { describeRule, ROUTINE_RULE_KINDS, type RoutineRuleKind } from "@/entities/routine";
import { isApiError } from "@/shared/api/client";
import { errorText } from "@/shared/api/error-text";
import {
  DefaultScheduleTimezone,
  MaxSchedulePrompt,
  MinScheduleEveryMinutes,
  type Schedule,
  ScheduleCatchUpOnce,
  ScheduleCatchUpSkip,
} from "@/shared/api/types.gen";
import { type MessageKey, useI18n } from "@/shared/i18n";
import { applyFieldErrors } from "@/shared/lib/forms";
import { browserTimeZone, timeZones, weekdayName } from "@/shared/lib/time";
import { ChoiceGroup } from "@/shared/ui/molecules/choice-group";
import { FormDialog } from "@/shared/ui/molecules/form-dialog";
import {
  FormAlert,
  SelectField,
  TextAreaField,
  TextField,
} from "@/shared/ui/molecules/form-fields";
import { Form, FormField } from "@/shared/ui/primitives/form";
import { useCreateRoutine, useUpdateRoutine } from "../api/routines";
import {
  everyFor,
  type RoutineForm,
  routineFormDefaults,
  routineFormSchema,
  routineRequest,
  ruleOfForm,
} from "../model/schema";

const KIND_LABELS: Record<RoutineRuleKind, MessageKey> = {
  minutes: "routines.kindMinutes",
  hours: "routines.kindHours",
  daily: "routines.kindDaily",
  weekdays: "routines.kindWeekdays",
  weekly: "routines.kindWeekly",
  cron: "routines.kindCron",
};

/** Monday first, as a week is read here; Sunday is cron's 0. */
const WEEK = [1, 2, 3, 4, 5, 6, 0] as const;

/**
 * Creates a routine of an AI, or edits one (HB-13): a name, what to ask, and the rule as a phone picks
 * it — every N minutes or hours, every day, Monday to Friday or once a week at a time, or a raw cron —
 * shown in words as it is chosen. The timezone is the browser's by default and sits with the catch-up
 * policy under "More options". The server's refusals of the rule (too often, too many) are said above
 * the buttons; a routine written meanwhile (409) or gone (404) closes the dialog, says so and reads the
 * list again. Mounted only while open, so every opening starts from the routine as it is.
 */
export function RoutineDialog({
  agentId,
  aiName,
  routine,
  open,
  onOpenChange,
}: {
  agentId: string;
  aiName: string;
  /** The routine to edit; absent creates one. */
  routine?: Schedule;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return open ? (
    <RoutineFormDialog
      agentId={agentId}
      aiName={aiName}
      routine={routine}
      onOpenChange={onOpenChange}
    />
  ) : null;
}

function RoutineFormDialog({
  agentId,
  aiName,
  routine,
  onOpenChange,
}: {
  agentId: string;
  aiName: string;
  routine?: Schedule;
  onOpenChange: (open: boolean) => void;
}) {
  const { t, locale } = useI18n();
  const create = useCreateRoutine(agentId);
  const update = useUpdateRoutine(agentId);
  const pending = create.isPending || update.isPending;
  const [error, setError] = useState<string | null>(null);
  const zone = useMemo(() => browserTimeZone(DefaultScheduleTimezone), []);
  const form = useForm<RoutineForm>({
    resolver: zodResolver(routineFormSchema),
    defaultValues: routineFormDefaults(zone, routine),
  });
  const zones = useMemo(() => timeZones([zone, routine?.tz ?? ""]), [zone, routine?.tz]);

  const [kind, every, time, day, cron] = useWatch({
    control: form.control,
    name: ["kind", "every", "time", "day", "cron"],
  });
  const rule = ruleOfForm({ kind, every, time, day, cron });

  // Switching between minutes and hours starts the interval at a number the new unit takes.
  const lastKind = useRef(kind);
  useEffect(() => {
    if (lastKind.current === kind) return;
    lastKind.current = kind;
    form.setValue("every", everyFor(kind, form.getValues("every")));
    form.clearErrors(["every", "time", "cron"]);
  }, [kind, form]);

  const onSubmit = form.handleSubmit((values) => {
    if (pending) return;
    setError(null);
    const body = routineRequest(values, routine);
    if (routine && Object.keys(body).length === 0) {
      onOpenChange(false);
      return;
    }
    const handlers = {
      onSuccess: () => {
        toast.success(routine ? t("routines.saved") : t("routines.created"));
        onOpenChange(false);
      },
      onError: (err: unknown) => {
        if (isApiError(err) && (err.status === 409 || err.status === 404)) {
          // What the form was opened on is not there any more: the list is read again.
          toast.error(errorText(t, err, "routine", locale));
          onOpenChange(false);
          return;
        }
        if (applyRoutineFieldErrors(err, form.setError, values.kind)) return;
        setError(errorText(t, err, "routine", locale));
      },
    };
    if (routine) update.mutate({ id: routine.id, body }, handlers);
    else create.mutate(body, handlers);
  });

  return (
    <FormDialog
      open
      onOpenChange={onOpenChange}
      title={routine ? t("routines.editTitle") : t("routines.newTitle", { name: aiName })}
      description={t("routines.formBody", { name: aiName })}
      onSubmit={onSubmit}
      submitLabel={
        pending
          ? routine
            ? t("routines.saving")
            : t("routines.creating")
          : routine
            ? t("common.save")
            : t("routines.create")
      }
      pending={pending}
      contentClassName="max-h-[calc(100dvh-2rem)] overflow-y-auto"
    >
      <Form {...form}>
        <TextField
          control={form.control}
          name="name"
          label={t("routines.name")}
          description={t("routines.nameHint")}
          autoComplete="off"
          autoFocus
        />
        <TextAreaField
          control={form.control}
          name="prompt"
          label={t("routines.prompt")}
          description={t("routines.promptHint", { name: aiName })}
          max={MaxSchedulePrompt}
          rows={4}
        />
        <SelectField control={form.control} name="kind" label={t("routines.repeat")}>
          {ROUTINE_RULE_KINDS.map((value) => (
            <option key={value} value={value}>
              {t(KIND_LABELS[value])}
            </option>
          ))}
        </SelectField>
        {kind === "minutes" || kind === "hours" ? (
          <TextField
            control={form.control}
            name="every"
            label={kind === "minutes" ? t("routines.everyMinutes") : t("routines.everyHours")}
            description={
              kind === "minutes"
                ? t("routines.everyMinutesHint", { min: MinScheduleEveryMinutes })
                : undefined
            }
            inputMode="numeric"
            autoComplete="off"
          />
        ) : null}
        {kind === "weekly" ? (
          <SelectField control={form.control} name="day" label={t("routines.day")}>
            {WEEK.map((d) => (
              <option key={d} value={String(d)}>
                {weekdayName(d, locale)}
              </option>
            ))}
          </SelectField>
        ) : null}
        {kind === "daily" || kind === "weekdays" || kind === "weekly" ? (
          <TextField
            control={form.control}
            name="time"
            type="time"
            label={t("routines.time")}
            autoComplete="off"
          />
        ) : null}
        {kind === "cron" ? (
          <TextField
            control={form.control}
            name="cron"
            label={t("routines.cron")}
            description={t("routines.cronHint")}
            autoComplete="off"
            inputClassName="font-mono"
          />
        ) : null}
        <p
          aria-live="polite"
          data-testid="routine-preview"
          className="rounded-md bg-muted px-3 py-2 text-sm [overflow-wrap:anywhere]"
        >
          {rule ? t("routines.preview", { rule: describeRule(t, locale, rule) }) : " "}
        </p>

        <details className="group">
          <summary className="flex min-h-11 w-fit cursor-pointer list-none items-center gap-1.5 rounded-sm text-sm font-medium outline-none select-none focus-visible:ring-[3px] focus-visible:ring-ring/70 [&::-webkit-details-marker]:hidden">
            <ChevronRightIcon
              className="size-4 transition-transform duration-150 ease-out group-open:rotate-90 motion-reduce:transition-none"
              aria-hidden="true"
            />
            {t("routines.more")}
          </summary>
          <div className="mt-2 space-y-4">
            <SelectField
              control={form.control}
              name="tz"
              label={t("routines.zone")}
              description={t("routines.zoneHint", { zone })}
            >
              {zones.map((z) => (
                <option key={z} value={z}>
                  {z}
                </option>
              ))}
            </SelectField>
            <FormField
              control={form.control}
              name="catchUp"
              render={({ field }) => (
                <ChoiceGroup
                  legend={t("routines.catchUp", { name: aiName })}
                  value={field.value}
                  onValueChange={field.onChange}
                  idBase="routine-catch-up"
                  options={[
                    { value: ScheduleCatchUpOnce, label: t("routines.catchUpOnce") },
                    { value: ScheduleCatchUpSkip, label: t("routines.catchUpSkip") },
                  ]}
                  itemClassName="min-h-11"
                />
              )}
            />
          </div>
        </details>
        <FormAlert>{error}</FormAlert>
      </Form>
    </FormDialog>
  );
}

/**
 * The server's reasons for the fields it refused, under the inputs they are about: its rule fields
 * are the form's interval, time or cron, whichever the chosen kind shows.
 */
function applyRoutineFieldErrors(
  err: unknown,
  setError: UseFormSetError<RoutineForm>,
  kind: RoutineRuleKind,
): boolean {
  const applied = applyFieldErrors(err, setError, ["name", "prompt", "tz"]);
  if (!isApiError(err) || err.status !== 422) return applied;
  const reason = err.fields.every_minutes ?? err.fields.cron;
  if (!reason) return applied;
  const field =
    kind === "minutes" || kind === "hours" ? "every" : kind === "cron" ? "cron" : "time";
  setError(field, { type: "server", message: reason });
  return true;
}
