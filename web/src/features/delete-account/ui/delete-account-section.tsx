import { zodResolver } from "@hookform/resolvers/zod";
import { useNavigate } from "@tanstack/react-router";
import { useId, useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { useDeleteAccount } from "@/entities/account";
import { isApiError } from "@/shared/api/client";
import { formFailureText } from "@/shared/api/error-text";
import { ErrorCodeLastInstanceAdmin } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { applyFieldErrors } from "@/shared/lib/forms";
import { FormDialog } from "@/shared/ui/molecules/form-dialog";
import { FormAlert, TextField } from "@/shared/ui/molecules/form-fields";
import { PageSection as Section } from "@/shared/ui/molecules/page-section";
import { Button } from "@/shared/ui/primitives/button";
import { Form } from "@/shared/ui/primitives/form";
import { type DeleteAccountForm, deleteAccountSchema } from "../model/schema";

/**
 * Deleting one's own account (POS-06): the password proves who asks, and the dialog says what
 * stays. The AIs, groups and connections belong to their organizations, so they outlive the person.
 */
export function DeleteAccountSection() {
  const t = useT();
  const id = useId();
  const navigate = useNavigate();
  const remove = useDeleteAccount();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const form = useForm<DeleteAccountForm>({
    resolver: zodResolver(deleteAccountSchema),
    defaultValues: { password: "" },
  });

  // Every opening starts with an empty password.
  const openDialog = () => {
    form.reset({ password: "" });
    setError(null);
    setOpen(true);
  };

  const onSubmit = form.handleSubmit(({ password }) => {
    if (remove.isPending) return;
    setError(null);
    remove.mutate(
      { password },
      {
        onSuccess: () => {
          toast(t("account.deleted"));
          void navigate({ to: "/login", search: { redirect: undefined } });
        },
        onError: (err) => {
          if (applyFieldErrors(err, form.setError, ["password"])) return;
          // The last admin of the instance is needed to inherit the organizations (OUPG-02).
          if (isApiError(err) && err.code === ErrorCodeLastInstanceAdmin) {
            setError(t("account.deleteLastAdmin"));
            return;
          }
          setError(formFailureText(t, err));
        },
      },
    );
  });

  return (
    <Section id={`${id}-delete`} title={t("account.delete")} description={t("account.deleteBody")}>
      <Button
        variant="outline"
        className="text-destructive hover:text-destructive"
        onClick={openDialog}
      >
        {t("account.deleteOpen")}
      </Button>
      <Form {...form}>
        <FormDialog
          open={open}
          onOpenChange={setOpen}
          title={t("account.deleteTitle")}
          description={t("account.deleteDialogBody")}
          onSubmit={(event) => void onSubmit(event)}
          submitLabel={t("account.deleteConfirm")}
          destructive
          pending={remove.isPending}
        >
          <FormAlert>{error}</FormAlert>
          <TextField
            control={form.control}
            name="password"
            type="password"
            label={t("auth.password")}
            autoComplete="current-password"
            autoFocus
          />
        </FormDialog>
      </Form>
    </Section>
  );
}
