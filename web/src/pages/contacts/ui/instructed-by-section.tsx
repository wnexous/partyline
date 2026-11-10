import { Link } from "@tanstack/react-router";
import { IntelligenceRow } from "@/entities/intelligence";
import { homeMembershipOf } from "@/entities/organization";
import type { IntelligenceRef, ProfileMembership } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { PageSection } from "@/shared/ui/molecules/page-section";

/**
 * Who can instruct an AI (ST-07): the identities whose messages it treats as trusted, its managers in
 * its home organization as the server computes them (POS-10). The server sends the list only to
 * whoever shares that organization with the AI; an empty list says that nobody can, and the link
 * leads to the Members page where positions are changed.
 */
export function InstructedBySection({
  name,
  instructors,
  memberships,
  viewerId,
}: {
  name: string;
  instructors: readonly IntelligenceRef[];
  memberships: readonly ProfileMembership[];
  viewerId: string | undefined;
}) {
  const t = useT();
  const home = homeMembershipOf(memberships);
  return (
    <PageSection layout="stacked" title={t("profile.instructedBy")}>
      <p className="max-w-[65ch] text-sm text-pretty text-muted-foreground">
        {instructors.length === 0
          ? t("profile.instructedByNone", { name })
          : home
            ? t("profile.instructedByBody", { name, org: home.org_name })
            : t("profile.instructedByBodyNoOrg", { name })}
      </p>
      {instructors.length > 0 ? (
        <ul aria-label={t("profile.instructedBy")} className="divide-y rounded-lg border">
          {instructors.map((who) => (
            <IntelligenceRow key={who.id} intelligence={who} viewerId={viewerId} className="py-3" />
          ))}
        </ul>
      ) : null}
      {home ? (
        <Link
          to="/orgs/$orgId/members"
          params={{ orgId: home.org_id }}
          className="inline-flex min-h-6 items-center text-sm font-medium text-primary underline-offset-2 outline-none hover:underline focus-visible:ring-[3px] focus-visible:ring-ring/70 pointer-coarse:min-h-11"
        >
          {t("profile.instructedByManage")}
        </Link>
      ) : null}
    </PageSection>
  );
}
