import { useId } from "react";
import { useIntelligence } from "@/entities/intelligence";
import type { Me } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { PageSection as Section } from "@/shared/ui/molecules/page-section";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { ProfileEditor } from "./profile-editor";

/**
 * The user's own profile (PROF-03), with the same editor as every intelligence: the display name,
 * the @handle, the bio and the photo. The answer patches the account everywhere it shows, the
 * sidebar header included.
 */
export function ProfileSection({ me }: { me: Me }) {
  const t = useT();
  const id = useId();
  const profile = useIntelligence(me.intelligence.id);
  return (
    <Section
      id={`${id}-profile`}
      title={t("account.profile")}
      description={t("account.profileBody")}
    >
      {profile.data ? (
        <ProfileEditor profile={profile.data} />
      ) : profile.isError ? (
        <div className="space-y-2">
          <p role="alert" className="text-sm text-destructive">
            {t("profile.loadError")}
          </p>
          <Button variant="outline" size="sm" onClick={() => profile.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      ) : (
        <div className="space-y-3" aria-hidden="true">
          <Skeleton className="size-24 rounded-full" />
          <Skeleton className="h-9 w-full max-w-sm" />
          <Skeleton className="h-9 w-full max-w-sm" />
        </div>
      )}
    </Section>
  );
}
