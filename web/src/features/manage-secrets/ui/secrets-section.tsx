import { KeyRoundIcon, PencilIcon, Trash2Icon } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";
import { useDeleteSecret, useSecrets } from "@/entities/secret";
import { errorText } from "@/shared/api/error-text";
import type { Secret } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { RelativeTime } from "@/shared/ui/atoms/relative-time";
import { ConfirmDialog } from "@/shared/ui/molecules/confirm-dialog";
import { EmptyState } from "@/shared/ui/molecules/empty-state";
import { PageSection } from "@/shared/ui/molecules/page-section";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { SecretDialog } from "./secret-dialog";

/**
 * "Secrets" (CA-03): the names of the owner's sealed credentials, when each was last replaced, a
 * form that writes a new one or a new value under an old name, and a confirmation to delete one.
 * A value is never rendered anywhere: the API does not return it (CA-08).
 */
export function SecretsSection() {
  const { t, locale } = useI18n();
  const id = useId();
  const secrets = useSecrets();
  const remove = useDeleteSecret();
  const [editing, setEditing] = useState<{ name?: string } | null>(null);
  const [removing, setRemoving] = useState<Secret | null>(null);
  const [error, setError] = useState<string | null>(null);
  const list = secrets.data ?? [];

  const confirmRemove = () => {
    if (!removing) return;
    remove.mutate(removing.name, {
      onSuccess: () => {
        toast(t("secrets.deleted", { name: removing.name }));
        setRemoving(null);
      },
      onError: (err) => setError(errorText(t, err, "secret")),
    });
  };

  return (
    <PageSection id={`${id}-secrets`} title={t("secrets.title")} description={t("secrets.body")}>
      <div className="space-y-3">
        {secrets.isPending ? (
          <Skeleton className="h-16 w-full" />
        ) : secrets.isError ? (
          <p role="alert" className="text-sm text-destructive">
            {t("secrets.loadError")}
          </p>
        ) : list.length === 0 ? (
          <EmptyState
            icon={KeyRoundIcon}
            title={t("secrets.emptyTitle")}
            headingLevel="h3"
            className="px-0 py-8"
          >
            <p>{t("secrets.emptyBody")}</p>
          </EmptyState>
        ) : (
          <ul aria-label={t("secrets.listLabel")} className="divide-y rounded-md border">
            {list.map((secret) => (
              <li
                key={secret.name}
                data-testid={`secret-${secret.name}`}
                className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2.5"
              >
                <span
                  translate="no"
                  className="min-w-0 flex-1 font-mono text-sm [overflow-wrap:anywhere]"
                >
                  {secret.name}
                </span>
                <RelativeTime
                  value={secret.updated_at}
                  locale={locale}
                  className="shrink-0 font-mono text-xs text-muted-foreground tabular"
                >
                  {(time) => t("secrets.updated", { time })}
                </RelativeTime>
                <div className="flex shrink-0 items-center gap-1">
                  <Button
                    variant="outline"
                    size="sm"
                    aria-label={t("secrets.replaceName", { name: secret.name })}
                    onClick={() => setEditing({ name: secret.name })}
                  >
                    <PencilIcon aria-hidden="true" />
                    {t("secrets.replace")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="text-muted-foreground hover:text-destructive pointer-coarse:size-11"
                    aria-label={t("secrets.delete", { name: secret.name })}
                    onClick={() => {
                      setError(null);
                      setRemoving(secret);
                    }}
                  >
                    <Trash2Icon aria-hidden="true" />
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}
        <Button variant="outline" size="sm" onClick={() => setEditing({})}>
          {t("secrets.add")}
        </Button>
      </div>

      {editing ? <SecretDialog name={editing.name} onClose={() => setEditing(null)} /> : null}

      <ConfirmDialog
        open={removing !== null}
        onClose={() => setRemoving(null)}
        title={t("secrets.deleteTitle", { name: removing?.name ?? "" })}
        body={t("secrets.deleteBody")}
        confirmLabel={t("secrets.deleteConfirm")}
        pending={remove.isPending}
        onConfirm={confirmRemove}
      >
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
      </ConfirmDialog>
    </PageSection>
  );
}
