import { zodResolver } from "@hookform/resolvers/zod";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { useCreateOrg, useCurrentOrg } from "@/entities/organization";
import { isApiError } from "@/shared/api/client";
import { useT } from "@/shared/i18n";
import { applyFieldErrors } from "@/shared/lib/forms";
import { FormDialog } from "@/shared/ui/molecules/form-dialog";
import { FormAlert, TextField } from "@/shared/ui/molecules/form-fields";
import { Form } from "@/shared/ui/primitives/form";
import { type CreateOrgForm, createOrgSchema } from "../model/schema";

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
  // Mounted only while open: every opening starts with an empty form.
  return open ? <NewOrgForm onOpenChange={onOpenChange} /> : null;
}

function NewOrgForm({ onOpenChange }: { onOpenChange: (open: boolean) => void }) {
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
          onOpenChange(false);
        },
        onError: (err) => {
          if (applyFieldErrors(err, form.setError, ["name"])) return;
          setError(isApiError(err) && err.status === 0 ? t("error.network") : t("error.generic"));
        },
      },
    );
  });

  return (
    <FormDialog
      open
      onOpenChange={onOpenChange}
      title={t("orgs.new")}
      description={t("orgs.newBody")}
      onSubmit={onSubmit}
      submitLabel={create.isPending ? t("orgs.creating") : t("orgs.create")}
      pending={create.isPending}
    >
      <Form {...form}>
        <FormAlert>{error}</FormAlert>
        <TextField
          control={form.control}
          name="name"
          label={t("orgs.name")}
          description={t("orgs.nameHint")}
          autoComplete="off"
          autoFocus
        />
      </Form>
    </FormDialog>
  );
}
