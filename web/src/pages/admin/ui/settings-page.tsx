import { useInstanceSettings } from "@/entities/account";
import { AttachmentRetentionSection } from "@/features/set-attachment-retention";
import { useT } from "@/shared/i18n";
import { PageHeader } from "@/shared/ui/molecules/page-header";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";

/** The instance settings an admin changes (RET-01): the attachment retention, one section each. */
export function AdminSettingsPage() {
  const t = useT();
  const settings = useInstanceSettings();
  return (
    <>
      <PageHeader title={t("page.adminSettingsTitle")} description={t("page.adminSettingsBody")} />
      {settings.isPending ? (
        <div className="space-y-3 px-4 py-6 md:px-8" aria-hidden="true">
          <Skeleton className="h-4 w-48" />
          <Skeleton className="h-4 w-64 max-w-full" />
        </div>
      ) : settings.isError ? (
        <div className="px-4 py-10 text-sm md:px-8" role="alert">
          <p className="text-destructive">{t("instance.loadError")}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => settings.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      ) : (
        <div className="max-w-2xl divide-y px-4 md:px-8">
          <AttachmentRetentionSection settings={settings.data} />
        </div>
      )}
    </>
  );
}
