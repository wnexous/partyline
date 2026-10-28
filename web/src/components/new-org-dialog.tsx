import { zodResolver } from "@hookform/resolvers/zod";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { FormAlert, TextField } from "@/shared/ui/molecules/form-fields";
import { Button } from "@/shared/ui/primitives/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/shared/ui/primitives/dialog";
import { Form } from "@/shared/ui/primitives/form";
import { useCurrentOrg } from "@/hooks/use-current-org";
import { isApiError } from "@/lib/api/client";
import { useCreateOrg } from "@/lib/api/queries";
import { applyFieldErrors } from "@/shared/lib/forms";
import { useT } from "@/shared/i18n";
import { type CreateOrgForm, createOrgSchema } from "@/lib/schemas";
import { isCoarsePointer } from "@/shared/lib/device";

/**
 * "New organization" (ORG-01): a name of 1 to 100 characters. The creator becomes its admin and the
 * organization the current one, so the sidebar shows it at once. The server's reason for a name it
 * refuses (422) shows under the field; a lost connection is told above the form, which stays open.
 * The caller owns whether it is open, so a menu item and a page button can both open it.
 */
export function NewOrgDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const t = useT();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent closeLabel={t("common.close")} touchFirst={isCoarsePointer()}>
        <DialogHeader>
          <DialogTitle>{t("orgs.new")}</DialogTitle>
          <DialogDescription>{t("orgs.newBody")}</DialogDescription>
        </DialogHeader>
        {/* Mounted only while open: every opening starts with an empty form. */}
        {open ? <NewOrgForm onDone={() => onOpenChange(false)} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function NewOrgForm({ onDone }: { onDone: () => void }) {
  const t = useT();
  const create = useCreateOrg();
  const { select } = useCurrentOrg();
  const [error, setError] = useState<string | null>(null);
  const form = useForm<CreateOrgForm>({
    resolver: zodResolver(createOrgSchema),
    defaultValues: { name: "" },
  });

  const onSubmit = form.handleSubmit((values) => {
    if (create.isPending) return;
    setError(null);
    create.mutate(
      { name: createOrgSchema.parse(values).name },
      {
        onSuccess: (org) => {
          select(org.id);
          toast.success(t("orgs.created", { org: org.name }));
          onDone();
        },
        onError: (err) => {
          if (applyFieldErrors(err, form.setError, ["name"])) return;
          setError(isApiError(err) && err.status === 0 ? t("error.network") : t("error.generic"));
        },
      },
    );
  });

  return (
    <Form {...form}>
      <form onSubmit={onSubmit} noValidate className="space-y-4">
        <FormAlert>{error}</FormAlert>
        <TextField
          control={form.control}
          name="name"
          label={t("orgs.name")}
          description={t("orgs.nameHint")}
          autoComplete="off"
          autoFocus
        />
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onDone}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" disabled={create.isPending}>
            {create.isPending ? t("orgs.creating") : t("orgs.create")}
          </Button>
        </DialogFooter>
      </form>
    </Form>
  );
}
