import { zodResolver } from "@hookform/resolvers/zod";
import { getRouteApi, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { useLogin } from "@/entities/account";
import { LanguageSwitch } from "@/features/change-language";
import { ThemeToggle } from "@/features/toggle-theme";
import { type LoginForm, loginSchema } from "@/lib/schemas";
import { isApiError } from "@/shared/api/client";
import { type Translate, useT } from "@/shared/i18n";
import { safeRedirect } from "@/shared/lib/forms";
import { AuthFrame } from "@/shared/ui/molecules/auth-frame";
import { FormAlert, TextField } from "@/shared/ui/molecules/form-fields";
import { Button } from "@/shared/ui/primitives/button";
import { Form } from "@/shared/ui/primitives/form";

const routeApi = getRouteApi("/login");

export interface LoginSearch {
  /** In-app location to return to after login (UI-02). */
  redirect?: string;
}

export function loginErrorText(t: Translate, err: unknown): string {
  if (!isApiError(err)) return t("error.generic");
  if (err.status === 401) return t("login.invalid");
  if (err.status === 429) return t("login.rateLimited");
  if (err.status === 0) return t("error.network");
  return err.message || t("error.generic");
}

export function LoginPage() {
  const t = useT();
  const navigate = useNavigate();
  const { redirect: target } = routeApi.useSearch();
  const login = useLogin();
  const [formError, setFormError] = useState<string | null>(null);
  const form = useForm<LoginForm>({
    resolver: zodResolver(loginSchema),
    defaultValues: { username: "", password: "" },
  });

  const onSubmit = form.handleSubmit((values) => {
    setFormError(null);
    login.mutate(values, {
      onSuccess: () => navigate({ href: safeRedirect(target), replace: true }),
      onError: (err) => {
        setFormError(loginErrorText(t, err));
        form.resetField("password");
        form.setFocus("password");
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
      tape={t("login.tape")}
      title={t("login.title")}
      description={t("login.body")}
      footer={t("login.noAccount")}
    >
      <Form {...form}>
        <form onSubmit={onSubmit} noValidate className="space-y-5">
          <FormAlert>{formError}</FormAlert>
          <TextField
            control={form.control}
            name="username"
            label={t("auth.username")}
            autoComplete="username"
            autoFocus
          />
          <TextField
            control={form.control}
            name="password"
            type="password"
            label={t("auth.password")}
            autoComplete="current-password"
          />
          <Button type="submit" className="w-full" disabled={login.isPending}>
            {login.isPending ? t("login.submitting") : t("login.submit")}
          </Button>
        </form>
      </Form>
    </AuthFrame>
  );
}
