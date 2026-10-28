import { zodResolver } from "@hookform/resolvers/zod";
import { createFileRoute, redirect, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { AuthLayout } from "@/app/layouts/auth-layout";
import { setupStatusQuery, useLogin } from "@/entities/account";
import { type LoginForm, loginSchema } from "@/lib/schemas";
import { isApiError } from "@/shared/api/client";
import { type Translate, useT } from "@/shared/i18n";
import { safeRedirect } from "@/shared/lib/forms";
import { FormAlert, TextField } from "@/shared/ui/molecules/form-fields";
import { Button } from "@/shared/ui/primitives/button";
import { Form } from "@/shared/ui/primitives/form";

export interface LoginSearch {
  /** In-app location to return to after login (UI-02). */
  redirect?: string;
}

export const Route = createFileRoute("/login")({
  validateSearch: (search: Record<string, unknown>): LoginSearch => ({
    redirect: typeof search.redirect === "string" ? search.redirect : undefined,
  }),
  // A fresh instance has no accounts yet: setup comes first (UI-01).
  beforeLoad: async ({ context }) => {
    const status = await context.queryClient.fetchQuery(setupStatusQuery());
    if (status.needs_setup) throw redirect({ to: "/setup" });
  },
  component: LoginPage,
});

export function loginErrorText(t: Translate, err: unknown): string {
  if (!isApiError(err)) return t("error.generic");
  if (err.status === 401) return t("login.invalid");
  if (err.status === 429) return t("login.rateLimited");
  if (err.status === 0) return t("error.network");
  return err.message || t("error.generic");
}

function LoginPage() {
  const t = useT();
  const navigate = useNavigate();
  const { redirect: target } = Route.useSearch();
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
    <AuthLayout
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
    </AuthLayout>
  );
}
