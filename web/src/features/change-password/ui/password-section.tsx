import { zodResolver } from "@hookform/resolvers/zod";
import { useId, useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { useChangePassword } from "@/entities/account";
import { type ChangePasswordForm, changePasswordSchema } from "@/lib/schemas";
import { formFailureText } from "@/shared/api/error-text";
import { PasswordMinLength } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { applyFieldErrors } from "@/shared/lib/forms";
import { FormAlert, TextField } from "@/shared/ui/molecules/form-fields";
import { PageSection as Section } from "@/shared/ui/molecules/page-section";
import { Button } from "@/shared/ui/primitives/button";
import { Form } from "@/shared/ui/primitives/form";

export function PasswordSection() {
  const t = useT();
  const id = useId();
  const change = useChangePassword();
  const [error, setError] = useState<string | null>(null);
  const form = useForm<ChangePasswordForm>({
    resolver: zodResolver(changePasswordSchema),
    defaultValues: { current_password: "", new_password: "", repeat_password: "" },
  });
  const onSubmit = form.handleSubmit(({ current_password, new_password }) => {
    setError(null);
    change.mutate(
      { current_password, new_password },
      {
        onSuccess: () => {
          form.reset();
          toast.success(t("account.passwordChanged"));
        },
        onError: (err) => {
          if (applyFieldErrors(err, form.setError, ["current_password", "new_password"])) return;
          setError(formFailureText(t, err));
        },
      },
    );
  });

  return (
    <Section
      id={`${id}-password`}
      title={t("auth.password")}
      description={t("account.passwordBody")}
    >
      <Form {...form}>
        <form
          aria-labelledby={`${id}-password`}
          onSubmit={onSubmit}
          noValidate
          className="space-y-4"
        >
          <FormAlert>{error}</FormAlert>
          <TextField
            control={form.control}
            name="current_password"
            type="password"
            label={t("account.currentPassword")}
            autoComplete="current-password"
          />
          <TextField
            control={form.control}
            name="new_password"
            type="password"
            label={t("account.newPassword")}
            description={t("auth.passwordHint", { min: PasswordMinLength })}
            autoComplete="new-password"
          />
          <TextField
            control={form.control}
            name="repeat_password"
            type="password"
            label={t("account.repeatPassword")}
            autoComplete="new-password"
          />
          <Button type="submit" variant="outline" disabled={change.isPending}>
            {t("account.changePassword")}
          </Button>
        </form>
      </Form>
    </Section>
  );
}
