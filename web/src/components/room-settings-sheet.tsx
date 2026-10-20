import { zodResolver } from "@hookform/resolvers/zod";
import { useNavigate } from "@tanstack/react-router";
import { type ReactNode, useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { FieldError, FormAlert, TextField } from "@/components/form-fields";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
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
import { Textarea } from "@/components/ui/textarea";
import { isApiError } from "@/lib/api/client";
import { useDeleteRoom, useUpdateRoom } from "@/lib/api/queries";
import { MaxRoomInstructions, type Room, type UpdateRoomRequest } from "@/lib/api/types.gen";
import { applyFieldErrors } from "@/lib/forms";
import { useI18n } from "@/lib/i18n";
import { type RoomSettingsForm, roomSettingsSchema, textLength } from "@/lib/schemas";
import { cn } from "@/lib/utils";

/**
 * Room settings for managers: name, description, instructions for agents (INS-01), chat bridging
 * (DLV-05), broadcast (ROUTE-06; both locked off in a catch-all room, ROUTE-07), retention, and
 * deleting the room (ROOMDEL-01).
 */
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
      broadcast: room.broadcast,
      retention_days: room.retention_days,
      instructions: room.instructions,
    },
  });

  const onSubmit = form.handleSubmit((values) => {
    setFormError(null);
    const { instructions, broadcast, ...rest } = roomSettingsSchema.parse(values);
    // Instructions and broadcast go only when edited, so a save elsewhere never rewrites them.
    const req: UpdateRoomRequest = {
      ...rest,
      ...(instructions === room.instructions ? {} : { instructions }),
      ...(broadcast === room.broadcast ? {} : { broadcast }),
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
            "bridge_chats",
            "broadcast",
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
                  <FormLabel>
                    {t("settings.instructions")}
                    <span className="font-normal text-muted-foreground">
                      ({t("auth.optional")})
                    </span>
                  </FormLabel>
                  <FormControl>
                    <Textarea
                      {...field}
                      rows={6}
                      className="max-h-80 min-h-32 resize-y leading-relaxed"
                    />
                  </FormControl>
                  <div className="flex items-start justify-between gap-4">
                    <FormDescription className="text-xs">
                      {t("settings.instructionsHint")}
                    </FormDescription>
                    <span
                      className={cn(
                        "shrink-0 font-mono text-xs text-muted-foreground tabular",
                        over && "font-semibold text-destructive",
                      )}
                    >
                      {t("settings.instructionsCount", { count, max: MaxRoomInstructions })}
                    </span>
                  </div>
                  <FieldError />
                </FormItem>
              );
            }}
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
                    disabled={room.catch_all}
                    aria-label={t("settings.bridge")}
                  />
                </FormControl>
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name="broadcast"
            render={({ field }) => (
              <FormItem>
                <div className="flex items-start justify-between gap-4">
                  <div className="space-y-1">
                    <FormLabel>{t("settings.broadcast")}</FormLabel>
                    <FormDescription className="text-xs">
                      {room.catch_all ? t("settings.catchAllLocked") : t("settings.broadcastHint")}
                    </FormDescription>
                  </div>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                      disabled={room.catch_all}
                      aria-label={t("settings.broadcast")}
                    />
                  </FormControl>
                </div>
                <FieldError />
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

/** Deletes the room after an explicit confirmation, then returns to the rooms list (ROOMDEL-02). */
function DeleteRoom({ room }: { room: Room }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const remove = useDeleteRoom();
  return (
    <div className="space-y-2 border-t pt-5">
      <p className="text-sm text-muted-foreground">{t("room.deleteHint")}</p>
      <AlertDialog>
        <AlertDialogTrigger asChild>
          <Button
            type="button"
            variant="outline"
            className="text-destructive hover:text-destructive"
          >
            {t("room.delete")}
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("room.deleteTitle", { name: room.name })}</AlertDialogTitle>
            <AlertDialogDescription>{t("room.deleteBody")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              disabled={remove.isPending}
              onClick={(e) => {
                e.preventDefault();
                remove.mutate(room.id, {
                  onSuccess: () => {
                    toast(t("room.deleted", { name: room.name }));
                    void navigate({ to: "/rooms" });
                  },
                  onError: () => toast.error(t("room.deleteFailed")),
                });
              }}
            >
              {t("room.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
