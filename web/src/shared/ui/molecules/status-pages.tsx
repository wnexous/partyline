import { Link } from "@tanstack/react-router";
import { ShieldOffIcon, TriangleAlertIcon } from "lucide-react";
import { useT } from "@/shared/i18n";
import { EmptyState } from "@/shared/ui/molecules/empty-state";
import { NotFound } from "@/shared/ui/molecules/not-found";
import { Button } from "@/shared/ui/primitives/button";

/** 403 for members opening admin routes (UI-21). */
export function ForbiddenPage() {
  const t = useT();
  return (
    <EmptyState
      icon={ShieldOffIcon}
      title={t("forbidden.title")}
      action={
        <Button asChild variant="outline">
          <Link to="/">{t("forbidden.back")}</Link>
        </Button>
      }
    >
      <p>{t("forbidden.body")}</p>
      <p className="mt-3 font-mono text-xs tabular">403</p>
    </EmptyState>
  );
}

export function NotFoundPage() {
  const t = useT();
  return (
    <main className="grid min-h-[60dvh] place-items-center">
      <NotFound
        title={t("notFound.title")}
        body={t("notFound.body")}
        action={
          <Button asChild variant="outline">
            <Link to="/">{t("notFound.back")}</Link>
          </Button>
        }
      />
    </main>
  );
}

export function CrashPage({ onRetry }: { onRetry: () => void }) {
  const t = useT();
  return (
    <main className="grid min-h-dvh place-items-center bg-background">
      <EmptyState
        icon={TriangleAlertIcon}
        title={t("crash.title")}
        action={<Button onClick={onRetry}>{t("common.retry")}</Button>}
      >
        <p>{t("crash.body")}</p>
      </EmptyState>
    </main>
  );
}
