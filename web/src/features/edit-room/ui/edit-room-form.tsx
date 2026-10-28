/*
 * The room settings form (ROOM-01, GAV-06, INS-01, ROOMDEL-01): the group's photo, name, description,
 * instructions for agents, retention, and the delete that closes the form. Opened by the room settings
 * widget; saving calls `onDone`, Cancel too.
 */

import { zodResolver } from "@hookform/resolvers/zod";
import { useId, useState } from "react";
import { type ControllerRenderProps, useForm } from "react-hook-form";
import { toast } from "sonner";
import { useUpdateRoom } from "@/entities/room";
import { type RoomSettingsForm, roomSettingsSchema, textLength } from "@/lib/schemas";
import { isApiError } from "@/shared/api/client";
import { MaxRoomInstructions, type Room, type UpdateRoomRequest } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { applyFieldErrors } from "@/shared/lib/forms";
import { cn } from "@/shared/lib/utils";
import { FieldError, FormAlert, TextField } from "@/shared/ui/molecules/form-fields";
import { SheetFooter } from "@/shared/ui/molecules/sheet";
import { Button } from "@/shared/ui/primitives/button";
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  Form as FormProvider,
  useFormField,
} from "@/shared/ui/primitives/form";
import { Input } from "@/shared/ui/primitives/input";
import { Textarea } from "@/shared/ui/primitives/textarea";
import { DeleteRoom } from "./delete-room";
import { RoomPhoto } from "./room-photo";

export function EditRoomForm({ room, onDone }: { room: Room; onDone: () => void }) {
  const { t } = useI18n();
  const update = useUpdateRoom(room.id);
  const [formError, setFormError] = useState<string | null>(null);
  const form = useForm<RoomSettingsForm>({
    resolver: zodResolver(roomSettingsSchema),
    defaultValues: {
      name: room.name,
      description: room.description,
      retention_days: room.retention_days,
      instructions: room.instructions,
    },
  });

  const onSubmit = form.handleSubmit((values) => {
    setFormError(null);
    const parsed = roomSettingsSchema.parse(values);
    // Instructions go only when edited, so a save elsewhere never rewrites them.
    const req: UpdateRoomRequest = {
      name: parsed.name,
      description: parsed.description,
      retention_days: parsed.retention_days,
      ...(parsed.instructions === room.instructions ? {} : { instructions: parsed.instructions }),
    };
    update.mutate(req, {
      onSuccess: () => {
        toast.success(t("settings.saved"));
        onDone();
      },
      onError: (err) => {
        if (
          applyFieldErrors(err, form.setError, [
            "name",
            "description",
            "retention_days",
            "instructions",
          ])
        ) {
          return;
        }
        setFormError(
          isApiError(err) && err.status === 403 ? t("settings.forbidden") : t("error.generic"),
        );
      },
    });
  });

  return (
    <FormProvider {...form}>
      <form onSubmit={onSubmit} noValidate className="flex min-h-0 flex-1 flex-col">
        <div className="flex-1 space-y-6 overflow-y-auto p-4">
          <FormAlert>{formError}</FormAlert>
          <RoomPhoto room={room} />
          <TextField
            control={form.control}
            name="name"
            label={t("rooms.name")}
            autoComplete="off"
          />
          <TextField
            control={form.control}
            name="description"
            label={t("rooms.description")}
            optional
            autoComplete="off"
          />
          <FormField
            control={form.control}
            name="instructions"
            render={({ field }) => {
              const count = textLength(field.value);
              const over = count > MaxRoomInstructions;
              return (
                <FormItem>
                  <InstructionsBody field={field} count={count} over={over} />
                </FormItem>
              );
            }}
          />
          <FormField
            control={form.control}
            name="retention_days"
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t("settings.retention")}</FormLabel>
                <FormControl>
                  <Input
                    type="number"
                    inputMode="numeric"
                    min={0}
                    step={1}
                    className="w-32 font-mono tabular"
                    name={field.name}
                    ref={field.ref}
                    onBlur={field.onBlur}
                    value={Number.isNaN(field.value) ? "" : field.value}
                    onChange={(e) =>
                      field.onChange(e.target.value === "" ? Number.NaN : e.target.valueAsNumber)
                    }
                  />
                </FormControl>
                <FormDescription className="text-xs">{t("settings.retentionHint")}</FormDescription>
                <FieldError />
              </FormItem>
            )}
          />
          <DeleteRoom room={room} />
        </div>
        <SheetFooter className="flex-row justify-end border-t">
          <Button type="button" variant="outline" onClick={onDone}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" disabled={update.isPending}>
            {t("common.save")}
          </Button>
        </SheetFooter>
      </form>
    </FormProvider>
  );
}

/** The instructions textarea; its hint, counter (A11Y-03) and error describe it. */
function InstructionsBody({
  field,
  count,
  over,
}: {
  field: ControllerRenderProps<RoomSettingsForm, "instructions">;
  count: number;
  over: boolean;
}) {
  const { t } = useI18n();
  const { error, formDescriptionId, formMessageId } = useFormField();
  const counterId = useId();
  return (
    <>
      <FormLabel>
        {t("settings.instructions")}
        <span className="font-normal text-muted-foreground">({t("auth.optional")})</span>
      </FormLabel>
      <FormControl>
        <Textarea
          {...field}
          rows={6}
          aria-describedby={[formDescriptionId, counterId, error ? formMessageId : ""]
            .filter(Boolean)
            .join(" ")}
          className="max-h-80 min-h-32 resize-y leading-relaxed"
        />
      </FormControl>
      <div className="flex items-start justify-between gap-4">
        <FormDescription className="text-xs">{t("settings.instructionsHint")}</FormDescription>
        <span
          id={counterId}
          className={cn(
            "shrink-0 font-mono text-xs text-muted-foreground tabular",
            over && "font-semibold text-destructive",
          )}
        >
          {t("settings.instructionsCount", { count, max: MaxRoomInstructions })}
        </span>
      </div>
      <FieldError />
    </>
  );
}
