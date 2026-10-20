import { zodResolver } from "@hookform/resolvers/zod";
import { useNavigate } from "@tanstack/react-router";
import { type ReactNode, useState } from "react";
import { useForm } from "react-hook-form";
import { FormAlert, TextField } from "@/components/form-fields";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Form } from "@/components/ui/form";
import { isApiError } from "@/lib/api/client";
import { useCreateRoom } from "@/lib/api/queries";
import type { Room } from "@/lib/api/types.gen";
import { applyFieldErrors } from "@/lib/forms";
import { useT } from "@/lib/i18n";
import { type CreateRoomForm, createRoomSchema } from "@/lib/schemas";

/** Creates a room (owner = current user, private) and opens it. */
export function CreateRoomDialog({ trigger }: { trigger: ReactNode }) {
  const t = useT();
  const navigate = useNavigate();
  const create = useCreateRoom();
  const [open, setOpen] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const form = useForm<CreateRoomForm>({
    resolver: zodResolver(createRoomSchema),
    defaultValues: { name: "", description: "" },
  });

  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next) {
      form.reset();
      setFormError(null);
    }
  };

  // mutateAsync, not mutate callbacks: the first room replaces the empty state that holds this
  // dialog, and mutate's per-call callbacks never run once the component has unmounted.
  const onSubmit = form.handleSubmit(async (values) => {
    setFormError(null);
    let room: Room;
    try {
      room = await create.mutateAsync(createRoomSchema.parse(values));
    } catch (err) {
      if (applyFieldErrors(err, form.setError, ["name", "description"])) return;
      setFormError(isApiError(err) && err.status === 0 ? t("error.network") : t("error.generic"));
      return;
    }
    setOpen(false);
    form.reset();
    void navigate({ to: "/rooms/$roomId", params: { roomId: room.id } });
  });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("rooms.createTitle")}</DialogTitle>
          <DialogDescription>{t("rooms.createBody")}</DialogDescription>
        </DialogHeader>
        <Form {...form}>
          <form onSubmit={onSubmit} noValidate className="space-y-5">
            <FormAlert>{formError}</FormAlert>
            <TextField
              control={form.control}
              name="name"
              label={t("rooms.name")}
              description={t("rooms.nameHint")}
              autoComplete="off"
              autoFocus
            />
            <TextField
              control={form.control}
              name="description"
              label={t("rooms.description")}
              optional
              autoComplete="off"
            />
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={create.isPending}>
                {create.isPending ? t("rooms.creating") : t("rooms.create")}
              </Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  );
}
