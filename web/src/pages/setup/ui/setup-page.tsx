import { zodResolver } from "@hookform/resolvers/zod";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { useSetup } from "@/entities/account";
import { LanguageSwitch } from "@/features/change-language";
import { ThemeToggle } from "@/features/toggle-theme";
import { type SetupForm, setupSchema } from "@/lib/schemas";
import { isApiError } from "@/shared/api/client";
import { PasswordMinLength } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { applyFieldErrors } from "@/shared/lib/forms";
import { AuthFrame } from "@/shared/ui/molecules/auth-frame";
import { FormAlert, TextField } from "@/shared/ui/molecules/form-fields";
import { Button } from "@/shared/ui/primitives/button";
import { Form } from "@/shared/ui/primitives/form";

const FIELDS = ["token", "username", "password", "display_name"] as const;

export function SetupPage() {
  const t = useT();
  const navigate = useNavigate();
  const setup = useSetup();
  const [formError, setFormError] = useState<string | null>(null);
  const form = useForm<SetupForm>({
    resolver: zodResolver(setupSchema),
    defaultValues: { token: "", username: "", display_name: "", password: "" },
  });

  const onSubmit = form.handleSubmit((values) => {
    setFormError(null);
    setup.mutate(setupSchema.parse(values), {
      onSuccess: () => navigate({ to: "/", replace: true }),
      onError: (err) => {
        if (applyFieldErrors(err, form.setError, FIELDS)) return;
        if (isApiError(err) && (err.status === 401 || err.status === 403)) {
          form.setError("token", { type: "server", message: t("setup.badToken") });
          return;
        }
        setFormError(isApiError(err) && err.status === 0 ? t("error.network") : t("error.generic"));
      },
    });
  });

  return (
    <AuthFrame
      controls={
        <>
          <LanguageSwitch />
          <ThemeToggle />
        </>
      }
      tape={t("setup.tape")}
      title={t("setup.title")}
      description={t("setup.body")}
    >
      <Form {...form}>
        <form onSubmit={onSubmit} noValidate className="space-y-5">
          <FormAlert>{formError}</FormAlert>
          <TextField
            control={form.control}
            name="token"
            label={t("setup.token")}
            description={t("setup.tokenHint")}
            autoComplete="off"
            autoFocus
            inputClassName="font-mono"
          />
          <TextField
            control={form.control}
            name="username"
            label={t("auth.username")}
            description={t("auth.usernameHint")}
            autoComplete="username"
          />
          <TextField
            control={form.control}
            name="display_name"
            label={t("auth.displayName")}
            optional
            autoComplete="name"
          />
          <TextField
            control={form.control}
            name="password"
            type="password"
            label={t("auth.password")}
            description={t("auth.passwordHint", { min: PasswordMinLength })}
            autoComplete="new-password"
          />
          <Button type="submit" className="w-full" disabled={setup.isPending}>
            {setup.isPending ? t("setup.submitting") : t("setup.submit")}
          </Button>
        </form>
      </Form>
    </AuthFrame>
  );
}
