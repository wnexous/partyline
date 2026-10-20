import { zodResolver } from "@hookform/resolvers/zod";
import { type ReactNode, useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { FieldError, FormAlert, TextField } from "@/components/form-fields";
import { Button } from "@/components/ui/button";
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  Form as FormProvider,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { isApiError } from "@/lib/api/client";
import { useUpdateRoom } from "@/lib/api/queries";
import type { Room } from "@/lib/api/types.gen";
import { applyFieldErrors } from "@/lib/forms";
import { useI18n } from "@/lib/i18n";
import { type RoomSettingsForm, roomSettingsSchema } from "@/lib/schemas";

/** Room settings for managers: name, description, chat bridging (DLV-05) and retention. */
export function RoomSettingsSheet({ room, trigger }: { room: Room; trigger: ReactNode }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>{trigger}</SheetTrigger>
      <SheetContent className="w-full gap-0 sm:max-w-md">
        <SheetHeader className="border-b">
          <SheetTitle>{t("settings.title")}</SheetTitle>
          <SheetDescription>{t("settings.body")}</SheetDescription>
        </SheetHeader>
        {open ? <SettingsForm room={room} onDone={() => setOpen(false)} /> : null}
      </SheetContent>
    </Sheet>
  );
}

function SettingsForm({ room, onDone }: { room: Room; onDone: () => void }) {
  const { t } = useI18n();
  const update = useUpdateRoom(room.id);
  const [formError, setFormError] = useState<string | null>(null);
  const form = useForm<RoomSettingsForm>({
    resolver: zodResolver(roomSettingsSchema),
    defaultValues: {
      name: room.name,
      description: room.description,
      bridge_chats: room.bridge_chats,
      retention_days: room.retention_days,
    },
  });

  const onSubmit = form.handleSubmit((values) => {
    setFormError(null);
    update.mutate(roomSettingsSchema.parse(values), {
      onSuccess: () => {
        toast.success(t("settings.saved"));
        onDone();
      },
      onError: (err) => {
        if (
          applyFieldErrors(err, form.setError, [
            "name",
            "description",
            "bridge_chats",
            "retention_days",
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
            name="bridge_chats"
            render={({ field }) => (
              <FormItem className="flex items-start justify-between gap-4">
                <div className="space-y-1">
                  <FormLabel>{t("settings.bridge")}</FormLabel>
                  <FormDescription className="text-xs">{t("settings.bridgeHint")}</FormDescription>
                </div>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    aria-label={t("settings.bridge")}
                  />
                </FormControl>
              </FormItem>
            )}
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
