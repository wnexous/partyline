import { zodResolver } from "@hookform/resolvers/zod";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { LinkIcon } from "lucide-react";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { AuthLayout } from "@/components/auth-layout";
import { useAcceptInvite, useInviteInfo } from "@/lib/api/queries";
import { type AcceptInviteForm, acceptInviteSchema } from "@/lib/schemas";
import { isApiError } from "@/shared/api/client";
import { type InviteInfo, PasswordMinLength, RoleAdmin } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { applyFieldErrors } from "@/shared/lib/forms";
import { formatFull } from "@/shared/lib/time";
import { EmptyState } from "@/shared/ui/molecules/empty-state";
import { FormAlert, TextField } from "@/shared/ui/molecules/form-fields";
import { Button } from "@/shared/ui/primitives/button";
import { Form } from "@/shared/ui/primitives/form";
import { Skeleton } from "@/shared/ui/primitives/skeleton";

export const Route = createFileRoute("/invite/$token")({
  component: InvitePage,
});

const FIELDS = ["username", "password", "display_name"] as const;

/** 404 and 410 both mean the link is dead: expired, revoked or used. */
const isDeadInvite = (err: unknown) =>
  isApiError(err) && (err.status === 404 || err.status === 410);

function InvitePage() {
  const { token } = Route.useParams();
  const { t } = useI18n();
  const info = useInviteInfo(token);

  if (info.isPending) {
    return (
      <AuthLayout tape={t("invite.tape")} title={t("invite.title")}>
        <div className="space-y-4" role="status" aria-busy="true">
          <span className="sr-only">{t("common.loading")}</span>
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-9 w-full" />
        </div>
      </AuthLayout>
    );
  }

  if (info.isError) {
    return (
      <AuthLayout tape={t("invite.expiredTape")} title={t("invite.expiredTitle")}>
        {isDeadInvite(info.error) ? (
          <EmptyState
            icon={LinkIcon}
            title={t("invite.expiredTitle")}
            className="py-2 md:py-2 [&>h2]:sr-only"
            action={
              <Button asChild>
                <Link to="/login" search={{ redirect: undefined }}>
                  {t("invite.toLogin")}
                </Link>
              </Button>
            }
          >
            <p>{t("invite.expiredBody")}</p>
          </EmptyState>
        ) : (
          <FormAlert>{t("error.network")}</FormAlert>
        )}
      </AuthLayout>
    );
  }

  return <AcceptInvite token={token} info={info.data} />;
}

function AcceptInvite({ token, info }: { token: string; info: InviteInfo }) {
  const { t, locale } = useI18n();
  const navigate = useNavigate();
  const accept = useAcceptInvite(token);
  const [formError, setFormError] = useState<string | null>(null);
  const form = useForm<AcceptInviteForm>({
    resolver: zodResolver(acceptInviteSchema),
    defaultValues: { username: "", display_name: "", password: "" },
  });

  const expires = formatFull(info.expires_at, locale);
  const role = info.role === RoleAdmin ? t("invite.roleAdmin") : t("invite.roleMember");

  const onSubmit = form.handleSubmit((values) => {
    setFormError(null);
    accept.mutate(acceptInviteSchema.parse(values), {
      onSuccess: () => navigate({ to: "/", replace: true }),
      onError: (err) => {
        if (applyFieldErrors(err, form.setError, FIELDS)) return;
        if (isDeadInvite(err)) setFormError(t("invite.expiredBody"));
        else
          setFormError(
            isApiError(err) && err.status === 0 ? t("error.network") : t("error.generic"),
          );
      },
    });
  });

  return (
    <AuthLayout
      tape={t("invite.tape")}
      title={t("invite.title")}
      description={t("invite.body", { role, expires })}
    >
      <Form {...form}>
        <form onSubmit={onSubmit} noValidate className="space-y-5">
          <FormAlert>{formError}</FormAlert>
          <TextField
            control={form.control}
            name="username"
            label={t("auth.username")}
            description={t("auth.usernameHint")}
            autoComplete="username"
            autoFocus
          />
          <TextField
            control={form.control}
            name="display_name"
            label={t("auth.displayName")}
            description={t("auth.displayNameHint")}
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
          <Button type="submit" className="w-full" disabled={accept.isPending}>
            {accept.isPending ? t("invite.submitting") : t("invite.submit")}
          </Button>
        </form>
      </Form>
    </AuthLayout>
  );
}
